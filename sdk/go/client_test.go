package farfield

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestCaptureRetriesExactBodyAndAppliesScope(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/history/records" || request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("User-Agent") != "farfield-go/"+Version {
			t.Errorf("request = %s, headers = %#v", request.URL.Path, request.Header)
		}
		var body json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), body...))
		attempt := len(bodies)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":{"code":"FH_UNAVAILABLE","message":"retry","retryable":true}}`))
			return
		}
		var value map[string]any
		_ = json.Unmarshal(body, &value)
		_ = json.NewEncoder(writer).Encode(map[string]any{"id": value["id"], "conversation_id": value["conversation_id"], "kind": value["kind"]})
	}))
	defer server.Close()

	client, err := New(
		WithEndpoint(server.URL), WithToken("secret"), WithRetries(1, 0),
		WithDefaults(Scope{Agent: "researcher", Tags: map[string]string{"env": "test", "shared": "default"}}),
		WithBeforeSend(func(_ context.Context, input CaptureInput) (*CaptureInput, error) {
			content := input.Content.(map[string]any)
			delete(content, "secret")
			input.Content = content
			return &input, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithScope(context.Background(), Scope{ConversationID: "conv_go", TraceID: "trace_go", Tags: map[string]string{"shared": "scope"}})
	record, err := client.Capture(ctx, CaptureInput{
		Kind: "model.response", Content: map[string]any{"text": "hello", "secret": "remove"},
		Tags: map[string]string{"request": "42"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.ID == "" || record.ConversationID != "conv_go" {
		t.Fatalf("record = %#v", record)
	}
	if len(bodies) != 2 || !reflect.DeepEqual(bodies[0], bodies[1]) {
		t.Fatalf("retry bodies differ: %q, %q", bodies[0], bodies[1])
	}
	var sent map[string]any
	if err := json.Unmarshal(bodies[0], &sent); err != nil {
		t.Fatal(err)
	}
	if sent["agent"] != "researcher" || sent["trace_id"] != "trace_go" || sent["occurred_at"] == nil {
		t.Fatalf("sent = %#v", sent)
	}
	tags := sent["tags"].(map[string]any)
	if tags["env"] != "test" || tags["shared"] != "scope" || tags["request"] != "42" {
		t.Fatalf("tags = %#v", tags)
	}
	content := sent["content"].(map[string]any)
	if _, exists := content["secret"]; exists {
		t.Fatalf("before-send did not redact content: %#v", content)
	}
}

func TestCaptureBatchAndConversationHelpers(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/history/segments" {
			t.Errorf("path = %s", request.URL.Path)
		}
		var value struct {
			ID      string         `json:"id"`
			Records []CaptureInput `json:"records"`
		}
		if err := json.NewDecoder(request.Body).Decode(&value); err != nil {
			t.Error(err)
		}
		if value.ID == "" || len(value.Records) != 2 || value.Records[0].ID == "" || value.Records[0].ConversationID != "conv_batch" {
			t.Errorf("batch = %#v", value)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"id": value.ID, "conversation_id": "conv_batch", "entries": []any{map[string]any{}, map[string]any{}}})
	}))
	defer server.Close()
	client, _ := New(WithEndpoint(server.URL), WithRetries(0, 0))
	segment, err := client.Conversation("conv_batch").CaptureBatch(context.Background(), []CaptureInput{
		{Kind: "message.input", Content: map[string]any{"text": "hello"}},
		{Kind: "message.output", Content: map[string]any{"text": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if segment.ID == "" || segment.ConversationID != "conv_batch" || len(segment.Entries) != 2 {
		t.Fatalf("segment = %#v", segment)
	}
}

func TestBeforeSendCanDrop(t *testing.T) {
	t.Parallel()
	client, err := New(WithBeforeSend(func(context.Context, CaptureInput) (*CaptureInput, error) { return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Capture(context.Background(), CaptureInput{ConversationID: "conv", Kind: "private", Content: "secret"})
	if !errors.Is(err, ErrDropped) {
		t.Fatalf("error = %v", err)
	}
}

func TestBackgroundProcessorBatchesByConversationAndFlushes(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var segments []BatchInput
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var value BatchInput
		if err := json.NewDecoder(request.Body).Decode(&value); err != nil {
			t.Error(err)
		}
		mu.Lock()
		segments = append(segments, value)
		mu.Unlock()
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"id": value.ID, "conversation_id": value.Records[0].ConversationID, "entries": []any{},
		})
	}))
	defer server.Close()
	client, _ := New(WithEndpoint(server.URL), WithRetries(0, 0), WithDefaults(Scope{Tags: map[string]string{"env": "test"}}))
	processor, err := NewBackgroundProcessor(client, ProcessorOptions{MaxBatchSize: 10, ScheduleDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithScope(context.Background(), Scope{ConversationID: "conv_one", Agent: "researcher"})
	for _, kind := range []string{"message.user", "message.assistant"} {
		accepted, submitErr := processor.Submit(ctx, CaptureInput{Kind: kind, Content: kind})
		if submitErr != nil || !accepted {
			t.Fatalf("submit = %v, %v", accepted, submitErr)
		}
	}
	accepted, err := processor.Submit(context.Background(), CaptureInput{ConversationID: "conv_two", Kind: "tool.result", Tool: "search", Content: true})
	if err != nil || !accepted {
		t.Fatalf("submit = %v, %v", accepted, err)
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := processor.Flush(flushCtx); err != nil {
		t.Fatal(err)
	}
	if err := processor.Shutdown(flushCtx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(segments) != 2 {
		t.Fatalf("segments = %#v", segments)
	}
	counts := map[string]int{}
	for _, segment := range segments {
		counts[segment.Records[0].ConversationID] = len(segment.Records)
		if segment.Records[0].ConversationID == "conv_one" && (segment.Records[0].Agent != "researcher" || segment.Records[0].Tags["env"] != "test") {
			t.Fatalf("snapshotted record = %#v", segment.Records[0])
		}
	}
	if counts["conv_one"] != 2 || counts["conv_two"] != 1 {
		t.Fatalf("counts = %#v", counts)
	}
	stats := processor.Stats()
	if stats.Enqueued != 3 || stats.Committed != 3 || stats.Pending != 0 || stats.Batches != 2 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestBackgroundProcessorReportsDeliveryFailures(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"error":{"code":"FH_BUSY","message":"busy","retryable":true}}`))
	}))
	defer server.Close()
	client, _ := New(WithEndpoint(server.URL), WithRetries(0, 0))
	errorsSeen := make(chan error, 1)
	processor, _ := NewBackgroundProcessor(client, ProcessorOptions{ScheduleDelay: time.Millisecond, OnError: func(err error) { errorsSeen <- err }})
	accepted, err := processor.Submit(context.Background(), CaptureInput{ConversationID: "conv_fail", Kind: "message.user", Content: "hello"})
	if err != nil || !accepted {
		t.Fatalf("submit = %v, %v", accepted, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := processor.Flush(ctx); err == nil {
		t.Fatal("flush unexpectedly succeeded")
	}
	if err := processor.Shutdown(ctx); err == nil {
		t.Fatal("shutdown unexpectedly succeeded after a permanent delivery failure")
	}
	select {
	case <-errorsSeen:
	default:
		t.Fatal("on-error callback was not called")
	}
	if stats := processor.Stats(); stats.Failed != 1 || stats.Committed != 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestHistoryReadSurface(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/health":
			_, _ = writer.Write([]byte(`{"ok":true,"service":"farfield"}`))
		case "/v1/history/records":
			if request.URL.Query().Get("conversation_id") != "conv_read" || request.URL.Query().Get("limit") != "25" || request.URL.Query().Get("tag") != "env=test" {
				t.Errorf("query = %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"id":"rec_read","conversation_id":"conv_read","kind":"message.user"}]`))
		case "/v1/history/records/rec_read":
			_, _ = writer.Write([]byte(`{"record":{"id":"rec_read","conversation_id":"conv_read","kind":"message.user"},"content":{"text":"hello"}}`))
		case "/v1/history/search":
			if request.URL.Query().Get("q") != "hello world" || request.URL.Query().Get("tag") != "env=test" {
				t.Errorf("search query = %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"hits":[{"record":{"id":"rec_read","conversation_id":"conv_read","kind":"message.user"},"score":1.5,"snippet":"hello world"}],"total":1,"took_ms":0.2,"indexed_records":1,"index_updated_at":"2026-01-01T00:00:00Z"}`))
		case "/v1/history/conversations":
			if request.URL.Query().Get("limit") != "10" {
				t.Errorf("query = %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"id":"conv_read","record_count":1,"first_seen_at":"2026-01-01T00:00:00Z","last_seen_at":"2026-01-01T00:00:00Z","agents":[],"kinds":["message.user"]}]`))
		case "/v1/history/conversations/conv_read/timeline":
			_, _ = writer.Write([]byte(`[{"record":{"id":"rec_read","conversation_id":"conv_read","kind":"message.user"},"content":{"text":"hello"}}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := New(WithEndpoint(server.URL), WithRetries(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.Health(ctx); err != nil {
		t.Fatal(err)
	}
	records, err := client.Query(ctx, HistoryQuery{ConversationID: "conv_read", Tags: map[string]string{"env": "test"}, Limit: 25})
	if err != nil || len(records) != 1 || records[0].ID != "rec_read" {
		t.Fatalf("records = %#v, err = %v", records, err)
	}
	search, err := client.Search(ctx, SearchQuery{Text: "hello world", Tags: map[string]string{"env": "test"}, Limit: 25})
	if err != nil || search.Total != 1 || search.Hits[0].Record.ID != "rec_read" {
		t.Fatalf("search = %#v, err = %v", search, err)
	}
	entry, err := client.GetRecord(ctx, "rec_read")
	if err != nil || entry.Record.ID != "rec_read" || string(entry.Content) != `{"text":"hello"}` {
		t.Fatalf("entry = %#v, err = %v", entry, err)
	}
	conversations, err := client.Conversations(ctx, 10)
	if err != nil || len(conversations) != 1 || conversations[0].ID != "conv_read" {
		t.Fatalf("conversations = %#v, err = %v", conversations, err)
	}
	timeline, err := client.Timeline(ctx, "conv_read")
	if err != nil || len(timeline) != 1 || timeline[0].Record.ID != "rec_read" {
		t.Fatalf("timeline = %#v, err = %v", timeline, err)
	}
}

func TestAPIErrorIsTyped(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"error":{"code":"FH_IDEMPOTENCY_CONFLICT","message":"reused","retryable":false}}`))
	}))
	defer server.Close()
	client, _ := New(WithEndpoint(server.URL), WithRetries(0, time.Millisecond))
	_, err := client.Capture(context.Background(), CaptureInput{ConversationID: "conv", Kind: "event", Content: nil})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusConflict || apiError.Code != "FH_IDEMPOTENCY_CONFLICT" || apiError.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

func TestRetryAfterZeroIsExplicit(t *testing.T) {
	delay, explicit := retryAfterDuration("0", time.Now())
	if !explicit || delay != 0 {
		t.Fatalf("delay = %s, explicit = %v", delay, explicit)
	}
}
