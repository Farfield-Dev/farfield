package history

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Farfield-Dev/farfield/storage"
)

type countingStore struct {
	storage.Store
	gets map[string]int
}

type timelineObservingStore struct {
	storage.Store
	mu        sync.Mutex
	active    int
	maxActive int
	lists     []string
}

func (store *timelineObservingStore) Get(ctx context.Context, key string) ([]byte, error) {
	if !strings.HasPrefix(key, historySegmentsPrefix+"/") {
		return store.Store.Get(ctx, key)
	}
	store.mu.Lock()
	store.active++
	if store.active > store.maxActive {
		store.maxActive = store.active
	}
	store.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	data, err := store.Store.Get(ctx, key)
	store.mu.Lock()
	store.active--
	store.mu.Unlock()
	return data, err
}

func (store *timelineObservingStore) List(ctx context.Context, prefix string) ([]string, error) {
	store.mu.Lock()
	store.lists = append(store.lists, prefix)
	store.mu.Unlock()
	return store.Store.List(ctx, prefix)
}

func (store *countingStore) Get(ctx context.Context, key string) ([]byte, error) {
	store.gets[key]++
	return store.Store.Get(ctx, key)
}

func TestQueryTimelineAndConversations(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	agent := "researcher"
	for _, input := range []AppendInput{
		{RecordID: "rec_1", ConversationID: "conv_a", Kind: "user.message", Content: []byte(`{"text":"question"}`), OccurredAt: &first, Agent: &agent},
		{RecordID: "rec_2", ConversationID: "conv_a", Kind: "model.response", Content: []byte(`{"text":"answer"}`), OccurredAt: &second, Agent: &agent, Tags: map[string]string{"env": "test"}},
		{RecordID: "rec_3", ConversationID: "conv_b", Kind: "user.message", Content: []byte(`{"text":"other"}`), OccurredAt: &second},
	} {
		if _, err := service.Append(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	until := second.Add(time.Second)
	records, err := service.Query(context.Background(), Query{ConversationID: "conv_a", Kind: "model.response", Tags: map[string]string{"env": "test"}, Until: &until})
	if err != nil || len(records) != 1 || records[0].ID != "rec_2" {
		t.Fatalf("Query = %#v, %v", records, err)
	}
	timeline, err := service.Timeline(context.Background(), "conv_a")
	if err != nil || len(timeline) != 2 || timeline[0].Record.ID != "rec_1" {
		t.Fatalf("Timeline = %#v, %v", timeline, err)
	}
	conversations, err := service.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 2 || conversations[0].ID != "conv_a" || conversations[0].RecordCount != 2 {
		t.Fatalf("Conversations = %#v, %v", conversations, err)
	}
}

func TestTimelineReadsEachSegmentOnce(t *testing.T) {
	t.Parallel()
	local, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &countingStore{Store: local, gets: map[string]int{}}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	segment, err := service.AppendBatch(context.Background(), AppendBatchInput{
		SegmentID: "seg_timeline_once",
		Records: []AppendInput{
			{RecordID: "rec_batch_1", ConversationID: "conv_batch", Kind: "message.user", Content: []byte(`{"text":"one"}`)},
			{RecordID: "rec_batch_2", ConversationID: "conv_batch", Kind: "message.assistant", Content: []byte(`{"text":"two"}`)},
			{RecordID: "rec_batch_3", ConversationID: "conv_batch", Kind: "tool.result", Content: []byte(`{"ok":true}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := segmentKey(segment.ID, segment.ConversationID)
	store.gets = map[string]int{}
	timeline, err := service.Timeline(context.Background(), "conv_batch")
	if err != nil || len(timeline) != 3 {
		t.Fatalf("Timeline = %#v, %v", timeline, err)
	}
	if store.gets[key] != 1 {
		t.Fatalf("segment GETs = %d, want 1", store.gets[key])
	}
}

func TestTimelineListsOnlyTheConversationAndReadsSegmentsConcurrently(t *testing.T) {
	t.Parallel()
	local, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := New(local)
	if err != nil {
		t.Fatal(err)
	}
	for index := range 8 {
		_, err := writer.AppendBatch(context.Background(), AppendBatchInput{
			SegmentID: "seg_parallel_" + string(rune('a'+index)),
			Records: []AppendInput{{
				RecordID: "rec_parallel_" + string(rune('a'+index)), ConversationID: "conv_parallel",
				Kind: "message", Content: []byte(`{"ok":true}`),
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Append(context.Background(), AppendInput{
		RecordID: "rec_unrelated", ConversationID: "conv_unrelated", Kind: "message", Content: []byte(`{"ok":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	observed := &timelineObservingStore{Store: local}
	reader, err := New(observed)
	if err != nil {
		t.Fatal(err)
	}
	timeline, err := reader.Timeline(context.Background(), "conv_parallel")
	if err != nil || len(timeline) != 8 {
		t.Fatalf("timeline = %#v, %v", timeline, err)
	}
	if observed.maxActive < 2 {
		t.Fatalf("segment reads were serial; max concurrency = %d", observed.maxActive)
	}
	wantPrefix := conversationSegmentPrefix("conv_parallel")
	if len(observed.lists) != 1 || observed.lists[0] != wantPrefix {
		t.Fatalf("timeline LIST prefixes = %#v, want only %q", observed.lists, wantPrefix)
	}
}
