package history

import (
	"context"
	"testing"
	"time"

	"github.com/Farfield-Dev/farfield/storage"
)

type countingStore struct {
	storage.Store
	gets map[string]int
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
		{RecordID: "rec_2", ConversationID: "conv_a", Kind: "model.response", Content: []byte(`{"text":"answer"}`), OccurredAt: &second, Agent: &agent},
		{RecordID: "rec_3", ConversationID: "conv_b", Kind: "user.message", Content: []byte(`{"text":"other"}`), OccurredAt: &second},
	} {
		if _, err := service.Append(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	records, err := service.Query(context.Background(), Query{ConversationID: "conv_a", Kind: "model.response"})
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
