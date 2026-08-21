// Package server exposes Farfield History and Runtime over a small versioned HTTP API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Farfield-Dev/farfield/history"
	"github.com/Farfield-Dev/farfield/ingest/otlp"
	farfieldruntime "github.com/Farfield-Dev/farfield/runtime"
)

const maxRequestBytes = history.DefaultMaxSegmentBytes + 1024*1024

type Server struct {
	history *history.Service
	runtime *farfieldruntime.Journal
	otlp    *otlp.Ingestor
	mux     *http.ServeMux
}

type Option func(*Server)

func WithRuntime(journal *farfieldruntime.Journal) Option {
	return func(server *Server) { server.runtime = journal }
}

func New(service *history.Service, options ...Option) (*Server, error) {
	if service == nil {
		return nil, fmt.Errorf("history service is required")
	}
	otlpIngestor, err := otlp.New(service)
	if err != nil {
		return nil, err
	}
	server := &Server{history: service, otlp: otlpIngestor, mux: http.NewServeMux()}
	for _, option := range options {
		option(server)
	}
	server.routes()
	return server, nil
}

func (server *Server) Handler() http.Handler { return server.mux }

func (server *Server) routes() {
	server.mux.HandleFunc("GET /", server.index)
	server.mux.HandleFunc("GET /v1/health", server.health)
	server.mux.HandleFunc("POST /v1/traces", server.exportOTLPTraces)
	server.mux.HandleFunc("POST /v1/otel/v1/traces", server.exportOTLPTraces)
	server.mux.HandleFunc("POST /v1/history/records", server.appendRecord)
	server.mux.HandleFunc("POST /v1/history/segments", server.appendSegment)
	server.mux.HandleFunc("GET /v1/history/records", server.queryRecords)
	server.mux.HandleFunc("GET /v1/history/records/{recordID}", server.getRecord)
	server.mux.HandleFunc("GET /v1/history/search", server.searchHistory)
	server.mux.HandleFunc("GET /v1/history/conversations", server.conversations)
	server.mux.HandleFunc("GET /v1/history/conversations/{conversationID}/timeline", server.timeline)
	if server.runtime != nil {
		server.mux.HandleFunc("POST /v1/runtime/runs", server.createRun)
		server.mux.HandleFunc("GET /v1/runtime/runs/{runID}", server.getRun)
		server.mux.HandleFunc("GET /v1/runtime/runs/{runID}/events", server.runEvents)
		server.mux.HandleFunc("POST /v1/runtime/runs/{runID}/transitions", server.transitionRun)
		server.mux.HandleFunc("POST /v1/runtime/runs/{runID}/checkpoints", server.saveCheckpoint)
	}
}

func ListenAndServe(ctx context.Context, address string, handler http.Handler) error {
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		case <-stopped:
		}
	}()
	err := server.ListenAndServe()
	close(stopped)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type appendRequest struct {
	ID             string            `json:"id"`
	ConversationID string            `json:"conversation_id"`
	Kind           string            `json:"kind"`
	Content        json.RawMessage   `json:"content"`
	OccurredAt     *time.Time        `json:"occurred_at"`
	Sequence       *uint64           `json:"sequence"`
	TraceID        *string           `json:"trace_id"`
	SpanID         *string           `json:"span_id"`
	ParentID       *string           `json:"parent_id"`
	Agent          *string           `json:"agent"`
	Tool           *string           `json:"tool"`
	Status         *string           `json:"status"`
	Tags           map[string]string `json:"tags"`
}

type appendSegmentRequest struct {
	ID      string          `json:"id"`
	Records []appendRequest `json:"records"`
}

func (server *Server) appendRecord(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var value appendRequest
	if err := decoder.Decode(&value); err != nil {
		writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", "request body must be one valid JSON object", err)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", "request body must contain exactly one JSON object", err)
		return
	}
	if value.Content == nil {
		writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", "content is required; use null for an empty payload", nil)
		return
	}
	record, err := server.history.Append(request.Context(), value.appendInput())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, record)
}

func (server *Server) appendSegment(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var value appendSegmentRequest
	if err := decoder.Decode(&value); err != nil {
		writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", "request body must be one valid JSON object", err)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", "request body must contain exactly one JSON object", err)
		return
	}
	inputs := make([]history.AppendInput, 0, len(value.Records))
	for index, record := range value.Records {
		if record.Content == nil {
			writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", fmt.Sprintf("records[%d].content is required; use null for an empty payload", index), nil)
			return
		}
		inputs = append(inputs, record.appendInput())
	}
	segment, err := server.history.AppendBatch(request.Context(), history.AppendBatchInput{SegmentID: value.ID, Records: inputs})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, segment)
}

func (value appendRequest) appendInput() history.AppendInput {
	return history.AppendInput{
		RecordID: value.ID, ConversationID: value.ConversationID, Kind: value.Kind,
		Content: value.Content, OccurredAt: value.OccurredAt, Sequence: value.Sequence,
		TraceID: value.TraceID, SpanID: value.SpanID, ParentID: value.ParentID,
		Agent: value.Agent, Tool: value.Tool, Status: value.Status, Tags: value.Tags,
	}
}

func (server *Server) getRecord(writer http.ResponseWriter, request *http.Request) {
	record, err := server.history.ReadRecord(request.Context(), request.PathValue("recordID"))
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	content, err := server.history.ReadContent(request.Context(), record)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, history.Entry{Record: record, Content: json.RawMessage(content)})
}

func (server *Server) queryRecords(writer http.ResponseWriter, request *http.Request) {
	limit, ok := parseLimit(writer, request)
	if !ok {
		return
	}
	query := history.Query{
		ConversationID: request.URL.Query().Get("conversation_id"),
		TraceID:        request.URL.Query().Get("trace_id"),
		Kind:           request.URL.Query().Get("kind"),
		Agent:          request.URL.Query().Get("agent"),
		Tool:           request.URL.Query().Get("tool"),
		Status:         request.URL.Query().Get("status"),
		Limit:          limit,
	}
	query.Tags, ok = parseTagFilters(writer, request)
	if !ok {
		return
	}
	for parameter, target := range map[string]**time.Time{"since": &query.Since, "until": &query.Until} {
		if !parseTimeFilter(writer, request, parameter, target) {
			return
		}
	}
	records, err := server.history.Query(request.Context(), query)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, records)
}

func (server *Server) searchHistory(writer http.ResponseWriter, request *http.Request) {
	limit, ok := parseLimit(writer, request)
	if !ok {
		return
	}
	query := history.SearchQuery{
		Text: request.URL.Query().Get("q"), ConversationID: request.URL.Query().Get("conversation_id"),
		TraceID: request.URL.Query().Get("trace_id"), Kind: request.URL.Query().Get("kind"),
		Agent: request.URL.Query().Get("agent"), Tool: request.URL.Query().Get("tool"),
		Status: request.URL.Query().Get("status"), Limit: limit, Tags: map[string]string{},
	}
	query.Tags, ok = parseTagFilters(writer, request)
	if !ok {
		return
	}
	for parameter, target := range map[string]**time.Time{"since": &query.Since, "until": &query.Until} {
		if !parseTimeFilter(writer, request, parameter, target) {
			return
		}
	}
	result, err := server.history.Search(request.Context(), query)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func parseTagFilters(writer http.ResponseWriter, request *http.Request) (map[string]string, bool) {
	values := map[string]string{}
	for _, value := range request.URL.Query()["tag"] {
		key, tagValue, found := strings.Cut(value, "=")
		if !found || key == "" {
			writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", "tag must use key=value syntax", nil)
			return nil, false
		}
		values[key] = tagValue
	}
	return values, true
}

func parseTimeFilter(writer http.ResponseWriter, request *http.Request, parameter string, target **time.Time) bool {
	value := request.URL.Query().Get(parameter)
	if value == "" {
		return true
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", parameter+" must be an RFC3339 timestamp", err)
		return false
	}
	*target = &parsed
	return true
}

func (server *Server) conversations(writer http.ResponseWriter, request *http.Request) {
	limit, ok := parseLimit(writer, request)
	if !ok {
		return
	}
	values, err := server.history.Conversations(request.Context(), limit)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, values)
}

func (server *Server) timeline(writer http.ResponseWriter, request *http.Request) {
	entries, err := server.history.Timeline(request.Context(), request.PathValue("conversationID"))
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, entries)
}

type createRunRequest struct {
	ID          string          `json:"id"`
	OperationID string          `json:"operation_id"`
	Checkpoint  json.RawMessage `json:"checkpoint"`
}

type transitionRunRequest struct {
	OperationID string                 `json:"operation_id"`
	To          farfieldruntime.Status `json:"to"`
	Checkpoint  json.RawMessage        `json:"checkpoint"`
}

type checkpointRequest struct {
	OperationID string          `json:"operation_id"`
	Checkpoint  json.RawMessage `json:"checkpoint"`
}

func (server *Server) createRun(writer http.ResponseWriter, request *http.Request) {
	var value createRunRequest
	if !decodeRequest(writer, request, &value) {
		return
	}
	event, err := server.runtime.Create(request.Context(), farfieldruntime.CreateInput{
		RunID: value.ID, OperationID: value.OperationID, Checkpoint: value.Checkpoint,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, event)
}

func (server *Server) getRun(writer http.ResponseWriter, request *http.Request) {
	run, err := server.runtime.Get(request.Context(), request.PathValue("runID"))
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, run)
}

func (server *Server) runEvents(writer http.ResponseWriter, request *http.Request) {
	events, err := server.runtime.Events(request.Context(), request.PathValue("runID"))
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, events)
}

func (server *Server) transitionRun(writer http.ResponseWriter, request *http.Request) {
	var value transitionRunRequest
	if !decodeRequest(writer, request, &value) {
		return
	}
	event, err := server.runtime.Transition(request.Context(), farfieldruntime.TransitionInput{
		RunID: request.PathValue("runID"), OperationID: value.OperationID,
		To: value.To, Checkpoint: value.Checkpoint,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, event)
}

func (server *Server) saveCheckpoint(writer http.ResponseWriter, request *http.Request) {
	var value checkpointRequest
	if !decodeRequest(writer, request, &value) {
		return
	}
	if value.Checkpoint == nil {
		writeError(writer, http.StatusBadRequest, "FR_INVALID_REQUEST", "checkpoint is required; use null only when it is meaningful state", nil)
		return
	}
	event, err := server.runtime.SaveCheckpoint(request.Context(), farfieldruntime.CheckpointInput{
		RunID: request.PathValue("runID"), OperationID: value.OperationID, Checkpoint: value.Checkpoint,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, event)
}

func decodeRequest(writer http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(writer, http.StatusBadRequest, "FR_INVALID_REQUEST", "request body must be one valid JSON object", err)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "FR_INVALID_REQUEST", "request body must contain exactly one JSON object", err)
		return false
	}
	return true
}

func (server *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "service": "farfield"})
}

func parseLimit(writer http.ResponseWriter, request *http.Request) (int, bool) {
	raw := request.URL.Query().Get("limit")
	if raw == "" {
		return 100, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 1000 {
		writeError(writer, http.StatusBadRequest, "FH_INVALID_REQUEST", "limit must be between 1 and 1000", err)
		return 0, false
	}
	return limit, true
}

type errorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

func writeDomainError(writer http.ResponseWriter, err error) {
	var runtimeError *farfieldruntime.Error
	if errors.As(err, &runtimeError) {
		status := http.StatusUnprocessableEntity
		switch runtimeError.Code {
		case "FR_NOT_FOUND":
			status = http.StatusNotFound
		case "FR_IDEMPOTENCY_CONFLICT", "FR_CONCURRENT_TRANSITION":
			status = http.StatusConflict
		case "FR_CHECKPOINT_TOO_LARGE":
			status = http.StatusRequestEntityTooLarge
		case "FR_INVALID_CONFIGURATION", "FR_INVALID_OPERATION", "FR_INVALID_RUN", "FR_INVALID_CHECKPOINT", "FR_INVALID_EVENT", "FR_INVALID_TRANSITION", "FR_INVALID_REQUEST":
			status = http.StatusBadRequest
		case "FR_EVENT_CORRUPT":
			status = http.StatusInternalServerError
		case "FR_EVENT_WRITE_FAILED", "FR_EVENT_READ_FAILED", "FR_EVENT_LIST_FAILED":
			status = http.StatusServiceUnavailable
		}
		writeError(writer, status, runtimeError.Code, runtimeError.Message, runtimeError.Cause)
		return
	}
	var domainError *history.Error
	if !errors.As(err, &domainError) {
		writeError(writer, http.StatusInternalServerError, "FH_INTERNAL", "internal error", err)
		return
	}
	status := http.StatusUnprocessableEntity
	switch domainError.Code {
	case "FH_NOT_FOUND":
		status = http.StatusNotFound
	case "FH_IDEMPOTENCY_CONFLICT":
		status = http.StatusConflict
	case "FH_CONTENT_TOO_LARGE", "FH_SEGMENT_TOO_LARGE":
		status = http.StatusRequestEntityTooLarge
	case "FH_INVALID_JSON", "FH_INVALID_RECORD", "FH_INVALID_BATCH", "FH_INVALID_REQUEST", "FH_INVALID_SEARCH", "FH_SEARCH_PREFIX_TOO_BROAD":
		status = http.StatusBadRequest
	case "FH_DUPLICATE_RECORD":
		status = http.StatusConflict
	case "FH_RECORD_CORRUPT", "FH_SEGMENT_CORRUPT", "FH_BLOB_CORRUPT", "FH_BLOB_MISSING":
		status = http.StatusInternalServerError
	case "FH_SEARCH_INDEX_FAILED":
		status = http.StatusServiceUnavailable
	}
	writeError(writer, status, domainError.Code, domainError.Message, domainError.Cause)
}

func writeError(writer http.ResponseWriter, status int, code, message string, _ error) {
	var envelope errorEnvelope
	envelope.Error.Code = code
	envelope.Error.Message = message
	writeJSON(writer, status, envelope)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
