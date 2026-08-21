package farfield

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultEndpoint = "http://127.0.0.1:8787"

var ErrDropped = errors.New("farfield: event dropped by before-send hook")

type BeforeSend func(context.Context, CaptureInput) (*CaptureInput, error)

type Client struct {
	endpoint   string
	token      string
	http       *http.Client
	retries    int
	retryDelay time.Duration
	headers    map[string]string
	defaults   Scope
	beforeSend BeforeSend
	now        func() time.Time
}

type Option func(*Client) error

func WithEndpoint(endpoint string) Option {
	return func(client *Client) error {
		client.endpoint = endpoint
		return nil
	}
}

func WithToken(token string) Option {
	return func(client *Client) error {
		client.token = token
		return nil
	}
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) error {
		if httpClient == nil {
			return errors.New("farfield: HTTP client cannot be nil")
		}
		client.http = httpClient
		return nil
	}
}

func WithRetries(retries int, initialDelay time.Duration) Option {
	return func(client *Client) error {
		if retries < 0 || initialDelay < 0 {
			return errors.New("farfield: retries and retry delay cannot be negative")
		}
		client.retries = retries
		client.retryDelay = initialDelay
		return nil
	}
}

func WithHeaders(headers map[string]string) Option {
	return func(client *Client) error {
		client.headers = cloneTags(headers)
		return nil
	}
}

func WithDefaults(defaults Scope) Option {
	return func(client *Client) error {
		client.defaults = cloneScope(defaults)
		return nil
	}
}

func WithBeforeSend(hook BeforeSend) Option {
	return func(client *Client) error {
		client.beforeSend = hook
		return nil
	}
}

func New(options ...Option) (*Client, error) {
	endpoint := os.Getenv("FARFIELD_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	client := &Client{
		endpoint: endpoint,
		token:    os.Getenv("FARFIELD_TOKEN"),
		http:     &http.Client{Timeout: 10 * time.Second},
		retries:  2, retryDelay: 100 * time.Millisecond,
		headers: map[string]string{},
		now:     func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		if err := option(client); err != nil {
			return nil, err
		}
	}
	parsed, err := url.Parse(strings.TrimRight(client.endpoint, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("farfield: invalid endpoint %q", client.endpoint)
	}
	client.endpoint = strings.TrimRight(client.endpoint, "/")
	return client, nil
}

func (client *Client) Capture(ctx context.Context, input CaptureInput) (Record, error) {
	prepared, err := client.prepareCapture(ctx, input)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := client.doJSON(ctx, http.MethodPost, "/v1/history/records", prepared, &record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (client *Client) CaptureBatch(ctx context.Context, input BatchInput) (Segment, error) {
	if len(input.Records) == 0 {
		return Segment{}, errors.New("farfield: batch requires at least one record")
	}
	records := make([]CaptureInput, 0, len(input.Records))
	conversationID := ""
	for _, value := range input.Records {
		prepared, err := client.prepareCapture(ctx, value)
		if errors.Is(err, ErrDropped) {
			continue
		}
		if err != nil {
			return Segment{}, err
		}
		if conversationID == "" {
			conversationID = prepared.ConversationID
		} else if prepared.ConversationID != conversationID {
			return Segment{}, errors.New("farfield: every record in a batch must belong to one conversation")
		}
		records = append(records, prepared)
	}
	if len(records) == 0 {
		return Segment{}, ErrDropped
	}
	if input.ID == "" {
		var err error
		input.ID, err = newID("seg_")
		if err != nil {
			return Segment{}, fmt.Errorf("farfield: generate segment ID: %w", err)
		}
	}
	input.Records = records
	var segment Segment
	if err := client.doJSON(ctx, http.MethodPost, "/v1/history/segments", input, &segment); err != nil {
		return Segment{}, err
	}
	return segment, nil
}

func (client *Client) Query(ctx context.Context, query HistoryQuery) ([]Record, error) {
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.Limit < 1 || query.Limit > 1000 {
		return nil, errors.New("farfield: limit must be between 1 and 1000")
	}
	parameters := url.Values{"limit": []string{strconv.Itoa(query.Limit)}}
	addParameter(parameters, "conversation_id", query.ConversationID)
	addParameter(parameters, "trace_id", query.TraceID)
	addParameter(parameters, "kind", query.Kind)
	addParameter(parameters, "agent", query.Agent)
	addParameter(parameters, "tool", query.Tool)
	addParameter(parameters, "status", query.Status)
	if query.Since != nil {
		parameters.Set("since", query.Since.UTC().Format(time.RFC3339Nano))
	}
	if query.Until != nil {
		parameters.Set("until", query.Until.UTC().Format(time.RFC3339Nano))
	}
	queryKeys := make([]string, 0, len(query.Tags))
	for key := range query.Tags {
		queryKeys = append(queryKeys, key)
	}
	sort.Strings(queryKeys)
	for _, key := range queryKeys {
		parameters.Add("tag", key+"="+query.Tags[key])
	}
	var records []Record
	if err := client.doJSON(ctx, http.MethodGet, "/v1/history/records?"+parameters.Encode(), nil, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (client *Client) Search(ctx context.Context, query SearchQuery) (SearchResult, error) {
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.Limit < 1 || query.Limit > 1000 {
		return SearchResult{}, errors.New("farfield: limit must be between 1 and 1000")
	}
	parameters := url.Values{"limit": []string{strconv.Itoa(query.Limit)}}
	addParameter(parameters, "q", query.Text)
	addParameter(parameters, "conversation_id", query.ConversationID)
	addParameter(parameters, "trace_id", query.TraceID)
	addParameter(parameters, "kind", query.Kind)
	addParameter(parameters, "agent", query.Agent)
	addParameter(parameters, "tool", query.Tool)
	addParameter(parameters, "status", query.Status)
	if query.Since != nil {
		parameters.Set("since", query.Since.UTC().Format(time.RFC3339Nano))
	}
	if query.Until != nil {
		parameters.Set("until", query.Until.UTC().Format(time.RFC3339Nano))
	}
	keys := make([]string, 0, len(query.Tags))
	for key := range query.Tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parameters.Add("tag", key+"="+query.Tags[key])
	}
	var result SearchResult
	if err := client.doJSON(ctx, http.MethodGet, "/v1/history/search?"+parameters.Encode(), nil, &result); err != nil {
		return SearchResult{}, err
	}
	return result, nil
}

func (client *Client) GetRecord(ctx context.Context, recordID string) (Entry, error) {
	var entry Entry
	if err := client.doJSON(ctx, http.MethodGet, "/v1/history/records/"+url.PathEscape(recordID), nil, &entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (client *Client) Conversations(ctx context.Context, limit int) ([]ConversationSummary, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("farfield: limit must be between 1 and 1000")
	}
	var conversations []ConversationSummary
	path := "/v1/history/conversations?limit=" + strconv.Itoa(limit)
	if err := client.doJSON(ctx, http.MethodGet, path, nil, &conversations); err != nil {
		return nil, err
	}
	return conversations, nil
}

func (client *Client) Timeline(ctx context.Context, conversationID string) ([]Entry, error) {
	var entries []Entry
	path := "/v1/history/conversations/" + url.PathEscape(conversationID) + "/timeline"
	if err := client.doJSON(ctx, http.MethodGet, path, nil, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (client *Client) Health(ctx context.Context) error {
	var health struct {
		OK      bool   `json:"ok"`
		Service string `json:"service"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/v1/health", nil, &health); err != nil {
		return err
	}
	if !health.OK || health.Service != "farfield" {
		return &TransportError{Method: http.MethodGet, Path: "/v1/health", Err: errors.New("unexpected health response")}
	}
	return nil
}

func (client *Client) CreateRun(ctx context.Context, input CreateRunInput) (RuntimeEvent, error) {
	if input.ID == "" {
		var err error
		input.ID, err = newID("run_")
		if err != nil {
			return RuntimeEvent{}, fmt.Errorf("farfield: generate run ID: %w", err)
		}
	}
	if input.OperationID == "" {
		var err error
		input.OperationID, err = newID("op_")
		if err != nil {
			return RuntimeEvent{}, fmt.Errorf("farfield: generate operation ID: %w", err)
		}
	}
	var event RuntimeEvent
	if err := client.doJSON(ctx, http.MethodPost, "/v1/runtime/runs", input, &event); err != nil {
		return RuntimeEvent{}, err
	}
	return event, nil
}

func (client *Client) GetRun(ctx context.Context, runID string) (Run, error) {
	var run Run
	if err := client.doJSON(ctx, http.MethodGet, "/v1/runtime/runs/"+url.PathEscape(runID), nil, &run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (client *Client) RunEvents(ctx context.Context, runID string) ([]RuntimeEvent, error) {
	var events []RuntimeEvent
	if err := client.doJSON(ctx, http.MethodGet, "/v1/runtime/runs/"+url.PathEscape(runID)+"/events", nil, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func (client *Client) TransitionRun(ctx context.Context, runID string, input TransitionRunInput) (RuntimeEvent, error) {
	if input.OperationID == "" {
		var err error
		input.OperationID, err = newID("op_")
		if err != nil {
			return RuntimeEvent{}, fmt.Errorf("farfield: generate operation ID: %w", err)
		}
	}
	var event RuntimeEvent
	path := "/v1/runtime/runs/" + url.PathEscape(runID) + "/transitions"
	if err := client.doJSON(ctx, http.MethodPost, path, input, &event); err != nil {
		return RuntimeEvent{}, err
	}
	return event, nil
}

func (client *Client) CheckpointRun(ctx context.Context, runID string, input CheckpointRunInput) (RuntimeEvent, error) {
	if input.OperationID == "" {
		var err error
		input.OperationID, err = newID("op_")
		if err != nil {
			return RuntimeEvent{}, fmt.Errorf("farfield: generate operation ID: %w", err)
		}
	}
	var event RuntimeEvent
	path := "/v1/runtime/runs/" + url.PathEscape(runID) + "/checkpoints"
	if err := client.doJSON(ctx, http.MethodPost, path, input, &event); err != nil {
		return RuntimeEvent{}, err
	}
	return event, nil
}

func (client *Client) Conversation(conversationID string) Conversation {
	return Conversation{client: client, id: conversationID}
}

func (client *Client) NewConversation() (Conversation, error) {
	conversationID, err := newID("conv_")
	if err != nil {
		return Conversation{}, fmt.Errorf("farfield: generate conversation ID: %w", err)
	}
	return client.Conversation(conversationID), nil
}

type Conversation struct {
	client *Client
	id     string
}

func (conversation Conversation) ID() string { return conversation.id }

func (conversation Conversation) Capture(ctx context.Context, input CaptureInput) (Record, error) {
	input.ConversationID = conversation.id
	return conversation.client.Capture(ctx, input)
}

func (conversation Conversation) Message(ctx context.Context, role string, content any) (Record, error) {
	return conversation.Capture(ctx, CaptureInput{Kind: "message." + role, Content: content})
}

func (conversation Conversation) ToolResult(ctx context.Context, tool string, content any, status string) (Record, error) {
	if status == "" {
		status = "completed"
	}
	return conversation.Capture(ctx, CaptureInput{Kind: "tool.result", Tool: tool, Status: status, Content: content})
}

func (conversation Conversation) CaptureBatch(ctx context.Context, records []CaptureInput) (Segment, error) {
	for index := range records {
		records[index].ConversationID = conversation.id
	}
	return conversation.client.CaptureBatch(ctx, BatchInput{Records: records})
}

func (client *Client) prepareCapture(ctx context.Context, input CaptureInput) (CaptureInput, error) {
	scope := client.defaults
	if contextual, ok := ScopeFromContext(ctx); ok {
		scope = mergeScope(scope, contextual)
	}
	if input.ConversationID == "" {
		input.ConversationID = scope.ConversationID
	}
	if input.TraceID == "" {
		input.TraceID = scope.TraceID
	}
	if input.SpanID == "" {
		input.SpanID = scope.SpanID
	}
	if input.ParentID == "" {
		input.ParentID = scope.ParentID
	}
	if input.Agent == "" {
		input.Agent = scope.Agent
	}
	input.Tags = mergeTags(scope.Tags, input.Tags)
	if input.ID == "" {
		var err error
		input.ID, err = newID("rec_")
		if err != nil {
			return CaptureInput{}, fmt.Errorf("farfield: generate record ID: %w", err)
		}
	}
	if input.OccurredAt == nil {
		occurredAt := client.now().UTC()
		input.OccurredAt = &occurredAt
	}
	if client.beforeSend != nil {
		prepared, err := client.beforeSend(ctx, input)
		if err != nil {
			return CaptureInput{}, fmt.Errorf("farfield: before-send hook: %w", err)
		}
		if prepared == nil {
			return CaptureInput{}, ErrDropped
		}
		input = *prepared
	}
	if input.ID == "" {
		var err error
		input.ID, err = newID("rec_")
		if err != nil {
			return CaptureInput{}, fmt.Errorf("farfield: generate record ID: %w", err)
		}
	}
	if input.OccurredAt == nil {
		occurredAt := client.now().UTC()
		input.OccurredAt = &occurredAt
	}
	if input.ConversationID == "" || input.Kind == "" {
		return CaptureInput{}, errors.New("farfield: conversation ID and kind are required")
	}
	return input, nil
}

func (client *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return fmt.Errorf("farfield: encode request: %w", err)
		}
	}
	for attempt := 0; attempt <= client.retries; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, method, client.endpoint+path, bytes.NewReader(body))
		if requestErr != nil {
			return &TransportError{Method: method, Path: path, Err: requestErr}
		}
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "farfield-go/"+Version)
		if client.token != "" {
			request.Header.Set("Authorization", "Bearer "+client.token)
		}
		for key, value := range client.headers {
			request.Header.Set(key, value)
		}
		response, requestErr := client.http.Do(request)
		if requestErr != nil {
			if attempt < client.retries && ctx.Err() == nil {
				if waitErr := client.wait(ctx, attempt, ""); waitErr != nil {
					return waitErr
				}
				continue
			}
			return &TransportError{Method: method, Path: path, Err: requestErr}
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 32*1024*1024))
		response.Body.Close()
		if readErr != nil {
			return &TransportError{Method: method, Path: path, Err: readErr}
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if output == nil || len(data) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, output); err != nil {
				return &TransportError{Method: method, Path: path, Err: fmt.Errorf("decode response: %w", err)}
			}
			return nil
		}
		retryable := retryableStatus(response.StatusCode)
		if retryable && attempt < client.retries {
			if waitErr := client.wait(ctx, attempt, response.Header.Get("Retry-After")); waitErr != nil {
				return waitErr
			}
			continue
		}
		return decodeAPIError(response.StatusCode, retryable, data)
	}
	return &TransportError{Method: method, Path: path, Err: errors.New("retry budget exhausted")}
}

func (client *Client) wait(ctx context.Context, attempt int, retryAfter string) error {
	delay, explicit := retryAfterDuration(retryAfter, client.now())
	if !explicit {
		delay = client.retryDelay * time.Duration(1<<attempt)
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return &TransportError{Err: ctx.Err()}
	case <-timer.C:
		return nil
	}
}

func retryAfterDuration(value string, now time.Time) (time.Duration, bool) {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return max(parsed.Sub(now), 0), true
	}
	return 0, false
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (err *APIError) Error() string {
	if err.Code == "" {
		return fmt.Sprintf("farfield: HTTP %d: %s", err.StatusCode, err.Message)
	}
	return fmt.Sprintf("farfield: %s: %s", err.Code, err.Message)
}

type TransportError struct {
	Method string
	Path   string
	Err    error
}

func (err *TransportError) Error() string {
	if err.Method == "" {
		return "farfield: transport: " + err.Err.Error()
	}
	return fmt.Sprintf("farfield: %s %s: %v", err.Method, err.Path, err.Err)
}

func (err *TransportError) Unwrap() error { return err.Err }

func decodeAPIError(status int, retryable bool, data []byte) error {
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &envelope) == nil && envelope.Error.Message != "" {
		return &APIError{StatusCode: status, Code: envelope.Error.Code, Message: envelope.Error.Message, Retryable: retryable || envelope.Error.Retryable}
	}
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = http.StatusText(status)
	}
	return &APIError{StatusCode: status, Message: message, Retryable: retryable}
}

func newID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

func mergeScope(base, overlay Scope) Scope {
	result := cloneScope(base)
	if overlay.ConversationID != "" {
		result.ConversationID = overlay.ConversationID
	}
	if overlay.TraceID != "" {
		result.TraceID = overlay.TraceID
	}
	if overlay.SpanID != "" {
		result.SpanID = overlay.SpanID
	}
	if overlay.ParentID != "" {
		result.ParentID = overlay.ParentID
	}
	if overlay.Agent != "" {
		result.Agent = overlay.Agent
	}
	result.Tags = mergeTags(base.Tags, overlay.Tags)
	return result
}

func mergeTags(values ...map[string]string) map[string]string {
	result := map[string]string{}
	for _, value := range values {
		for key, item := range value {
			result[key] = item
		}
	}
	return result
}

func cloneTags(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	return mergeTags(value)
}

func addParameter(parameters url.Values, key, value string) {
	if value != "" {
		parameters.Set(key, value)
	}
}
