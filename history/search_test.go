package history

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Farfield-Dev/farfield/storage"
)

func TestSearchRanksTextAndAppliesExactFilters(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	agent := "researcher"
	trace := "trace_search"
	status := "completed"
	values := []AppendInput{
		{RecordID: "rec_search_1", ConversationID: "conv_search", Kind: "message.assistant", Agent: &agent, TraceID: &trace, Status: &status, OccurredAt: at(base), Tags: map[string]string{"env": "prod"}, Content: []byte(`{"text":"Durable agent traces need idempotent capture and checksummed recovery."}`)},
		{RecordID: "rec_search_2", ConversationID: "conv_search", Kind: "tool.result", Agent: &agent, TraceID: &trace, OccurredAt: at(base.Add(time.Second)), Tags: map[string]string{"env": "test"}, Content: []byte(`{"text":"Execution is durable when every checkpoint is committed.","encrypted_content":"opaqueuniquetoken"}`)},
		{RecordID: "rec_search_3", ConversationID: "conv_other", Kind: "message.assistant", OccurredAt: at(base.Add(2 * time.Second)), Content: []byte(`{"text":"A gardening conversation about durable gloves."}`)},
		{RecordID: "rec_search_4", ConversationID: "conv_search", Kind: "message.assistant", OccurredAt: at(base.Add(3 * time.Second)), Content: []byte(`{"text":"Unrelated agent output."}`)},
	}
	for _, value := range values {
		if _, err := service.Append(context.Background(), value); err != nil {
			t.Fatal(err)
		}
	}

	result, err := service.Search(context.Background(), SearchQuery{Text: `"durable agent"`, ConversationID: "conv_search", Kind: "message.assistant", Tags: map[string]string{"env": "prod"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Hits) != 1 || result.Hits[0].Record.ID != "rec_search_1" || result.Hits[0].Score <= 0 {
		t.Fatalf("phrase search = %#v", result)
	}
	if result.IndexedRecords != 4 || !strings.Contains(strings.ToLower(result.Hits[0].Snippet), "durable agent") {
		t.Fatalf("search metadata = %#v", result)
	}

	prefix, err := service.Search(context.Background(), SearchQuery{Text: "check*", ConversationID: "conv_search", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if prefix.Total != 2 {
		t.Fatalf("prefix search = %#v", prefix)
	}
	opaque, err := service.Search(context.Background(), SearchQuery{Text: "opaqueuniquetoken", Limit: 10})
	if err != nil || opaque.Total != 0 {
		t.Fatalf("opaque provider field search = %#v, %v", opaque, err)
	}

	filtered, err := service.Search(context.Background(), SearchQuery{TraceID: trace, Status: status, Limit: 10})
	if err != nil || filtered.Total != 1 || filtered.Hits[0].Record.ID != "rec_search_1" {
		t.Fatalf("filtered search = %#v, %v", filtered, err)
	}
}

func TestSearchObservesAppendImmediatelyAfterWarmup(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(context.Background(), SearchQuery{Text: "missing", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Append(context.Background(), AppendInput{RecordID: "rec_live_search", ConversationID: "conv_live_search", Kind: "message.user", Content: []byte(`{"text":"newly searchable evidence"}`)}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), SearchQuery{Text: "searchable evidence", Limit: 10})
	if err != nil || result.Total != 1 || result.Hits[0].Record.ID != "rec_live_search" {
		t.Fatalf("immediate search = %#v, %v", result, err)
	}
}

func TestSearchCacheIsDisposableAndRepairable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := storage.OpenLocal(filepath.Join(root, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(root, "cache", "search.json.gz")
	service, err := New(store, WithSearchCache(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Append(context.Background(), AppendInput{RecordID: "rec_cached", ConversationID: "conv_cached", Kind: "message.user", Content: []byte(`{"text":"cache recovery works"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(context.Background(), SearchQuery{Text: "recovery", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("search cache was not written: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(store, WithSearchCache(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	result, err := reopened.Search(context.Background(), SearchQuery{Text: "cache recovery", Limit: 10})
	if err != nil || result.Total != 1 || result.Hits[0].Record.ID != "rec_cached" {
		t.Fatalf("rebuilt search = %#v, %v", result, err)
	}
}

type blockingListStore struct {
	storage.Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (store *blockingListStore) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix == historySegmentsPrefix {
		store.once.Do(func() { close(store.started) })
		select {
		case <-store.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return store.Store.List(ctx, prefix)
}

func TestVerifiedSearchCacheServesBeforeRemoteRefresh(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	base, err := storage.OpenLocal(filepath.Join(root, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(root, "search.json.gz")
	service, err := New(base, WithSearchCache(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Append(context.Background(), AppendInput{RecordID: "rec_restart", ConversationID: "conv_restart", Kind: "message.user", Content: []byte(`{"text":"instant restart search"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(context.Background(), SearchQuery{Text: "restart", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	blocked := &blockingListStore{Store: base, started: make(chan struct{}), release: make(chan struct{})}
	reopened, err := New(blocked, WithSearchCache(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		value, searchErr := reopened.Search(context.Background(), SearchQuery{Text: "restart", Limit: 10})
		if searchErr == nil && (value.Total != 1 || value.Hits[0].Record.ID != "rec_restart") {
			searchErr = fmt.Errorf("unexpected result: %#v", value)
		}
		result <- searchErr
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(blocked.release)
		t.Fatal("cached search waited for remote LIST")
	}
	close(blocked.release)
}

func TestSearchRejectsInvalidSyntax(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []SearchQuery{{Text: `"unfinished`, Limit: 10}, {Text: "a*", Limit: 10}, {Limit: 1001}} {
		if _, err := service.Search(context.Background(), query); err == nil {
			t.Fatalf("Search(%#v) succeeded", query)
		}
	}
}

func at(value time.Time) *time.Time { return &value }

func BenchmarkSearchTenThousandAgentRecords(b *testing.B) {
	projection := &searchProjection{now: time.Now, loaded: true, sources: map[string]string{}}
	projection.documents = make([]searchDocument, 10_000)
	for index := range projection.documents {
		agent := "researcher"
		projection.documents[index] = searchDocument{
			SourceKey: fmt.Sprintf("source-%05d", index/100),
			Record:    Record{ID: fmt.Sprintf("rec_%05d", index), ConversationID: fmt.Sprintf("conv_%04d", index/10), Kind: "tool.result", Agent: &agent, OccurredAt: time.Unix(int64(index), 0).UTC(), Tags: map[string]string{"env": "prod"}},
			Text:      "durable agent execution checkpoint recovery object storage trace evidence",
		}
	}
	projection.rebuildLocked()
	query := SearchQuery{Text: `"checkpoint recovery" object*`, Agent: "researcher", Tags: map[string]string{"env": "prod"}, Limit: 20}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := projection.execute(query); err != nil {
			b.Fatal(err)
		}
	}
}
