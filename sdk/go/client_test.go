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

func TestHistoryReadSurface(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/health":
			_, _ = writer.Write([]byte(`{"ok":true,"service":"farfield"}`))
		case "/v1/history/records":
			if request.URL.Query().Get("conversation_id") != "conv_read" || request.URL.Query().Get("limit") != "25" {
				t.Errorf("query = %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"id":"rec_read","conversation_id":"conv_read","kind":"message.user"}]`))
		case "/v1/history/records/rec_read":
			_, _ = writer.Write([]byte(`{"record":{"id":"rec_read","conversation_id":"conv_read","kind":"message.user"},"content":{"text":"hello"}}`))
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
	records, err := client.Query(ctx, HistoryQuery{ConversationID: "conv_read", Limit: 25})
	if err != nil || len(records) != 1 || records[0].ID != "rec_read" {
		t.Fatalf("records = %#v, err = %v", records, err)
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

func TestRuntimeClient(t *testing.T) {
	t.Parallel()
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/runtime/runs":
			var value CreateRunInput
			_ = json.NewDecoder(request.Body).Decode(&value)
			if value.ID == "" || value.OperationID == "" {
				t.Errorf("create = %#v", value)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"run_id": value.ID, "operation_id": value.OperationID, "to": "queued"})
		case "/v1/runtime/runs/run_go/transitions":
			_ = json.NewEncoder(writer).Encode(map[string]any{"run_id": "run_go", "to": "running"})
		case "/v1/runtime/runs/run_go/checkpoints":
			_ = json.NewEncoder(writer).Encode(map[string]any{"run_id": "run_go", "to": "running"})
		case "/v1/runtime/runs/run_go/events":
			_ = json.NewEncoder(writer).Encode([]any{map[string]any{"run_id": "run_go", "to": "queued"}})
		case "/v1/runtime/runs/run_go":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "run_go", "status": "running"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, _ := New(WithEndpoint(server.URL), WithRetries(0, 0))
	ctx := context.Background()
	created, err := client.CreateRun(ctx, CreateRunInput{ID: "run_go", Checkpoint: map[string]any{"step": 0}})
	if err != nil || created.RunID != "run_go" {
		t.Fatalf("created = %#v, err = %v", created, err)
	}
	if _, err := client.TransitionRun(ctx, "run_go", TransitionRunInput{To: Running}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CheckpointRun(ctx, "run_go", CheckpointRunInput{Checkpoint: map[string]any{"step": 1}}); err != nil {
		t.Fatal(err)
	}
	if run, err := client.GetRun(ctx, "run_go"); err != nil || run.Status != Running {
		t.Fatalf("run = %#v, err = %v", run, err)
	}
	if events, err := client.RunEvents(ctx, "run_go"); err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, err = %v", events, err)
	}
	if len(paths) != 5 {
		t.Fatalf("paths = %#v", paths)
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
