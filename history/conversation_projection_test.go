package history

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Farfield-Dev/farfield/storage"
)

type projectionFailureStore struct {
	storage.Store
	mu       sync.Mutex
	failed   bool
	dropOnly bool
}

func (store *projectionFailureStore) PutIfAbsent(ctx context.Context, key string, data []byte, options storage.PutOptions) (bool, error) {
	if !strings.HasPrefix(key, conversationDeltaPrefix+"/") {
		return store.Store.PutIfAbsent(ctx, key, data, options)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.dropOnly {
		return true, nil
	}
	if !store.failed {
		store.failed = true
		return false, fmt.Errorf("simulated projection write failure")
	}
	return store.Store.PutIfAbsent(ctx, key, data, options)
}

type projectionCountingStore struct {
	storage.Store
	mu    sync.Mutex
	gets  map[string]int
	lists map[string]int
}

type projectionReadFailureStore struct {
	storage.Store
	fail bool
}

func (store *projectionReadFailureStore) Get(ctx context.Context, key string) ([]byte, error) {
	if store.fail && strings.HasPrefix(key, conversationDeltaPrefix+"/") {
		return nil, fmt.Errorf("simulated corrupt projection delta")
	}
	return store.Store.Get(ctx, key)
}

func (store *projectionCountingStore) operationCounts() (gets, lists int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, count := range store.gets {
		gets += count
	}
	for _, count := range store.lists {
		lists += count
	}
	return gets, lists
}

func (store *projectionCountingStore) Get(ctx context.Context, key string) ([]byte, error) {
	store.mu.Lock()
	store.gets[key]++
	store.mu.Unlock()
	return store.Store.Get(ctx, key)
}

func (store *projectionCountingStore) List(ctx context.Context, prefix string) ([]string, error) {
	store.mu.Lock()
	store.lists[prefix]++
	store.mu.Unlock()
	return store.Store.List(ctx, prefix)
}

func TestProjectionFailureIsRepairableByExactRetry(t *testing.T) {
	t.Parallel()
	local, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &projectionFailureStore{Store: local}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	input := AppendInput{RecordID: "rec_projection_retry", ConversationID: "conv_projection", Kind: "message.user", Content: []byte(`{"text":"hello"}`)}
	_, err = service.Append(context.Background(), input)
	var domainError *Error
	if !errors.As(err, &domainError) || domainError.Code != "FH_PROJECTION_WRITE_FAILED" {
		t.Fatalf("first append error = %v", err)
	}
	if _, err := service.ReadRecord(context.Background(), input.RecordID); err != nil {
		t.Fatalf("authoritative record was not committed: %v", err)
	}
	if _, err := service.Append(context.Background(), input); err != nil {
		t.Fatalf("exact retry did not repair projection: %v", err)
	}
	conversations, err := service.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 1 || conversations[0].RecordCount != 1 {
		t.Fatalf("conversations = %#v, %v", conversations, err)
	}
	keys, err := local.List(context.Background(), conversationDeltaPrefix)
	if err != nil || len(keys) != 1 {
		t.Fatalf("delta keys = %#v, %v", keys, err)
	}
}

func TestExplicitRebuildRecoversAuthoritativeHistoryAndColdStartAvoidsSourceGets(t *testing.T) {
	t.Parallel()
	local, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	withoutProjectionWrites, err := New(&projectionFailureStore{Store: local, dropOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutProjectionWrites.AppendBatch(context.Background(), AppendBatchInput{
		SegmentID: "seg_rebuild_projection",
		Records: []AppendInput{
			{RecordID: "rec_rebuild_one", ConversationID: "conv_rebuild", Kind: "message.user", Content: []byte(`{"text":"one"}`)},
			{RecordID: "rec_rebuild_two", ConversationID: "conv_rebuild", Kind: "message.assistant", Content: []byte(`{"text":"two"}`)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	first, err := New(local)
	if err != nil {
		t.Fatal(err)
	}
	conversations, err := first.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 0 {
		t.Fatalf("ordinary read scanned authoritative history: %#v, %v", conversations, err)
	}
	rebuilt, err := first.RebuildConversationProjection(context.Background())
	if err != nil || rebuilt.ConversationCount != 1 || rebuilt.SourceCount != 1 {
		t.Fatalf("rebuild = %#v, %v", rebuilt, err)
	}
	conversations, err = first.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 1 || conversations[0].RecordCount != 2 {
		t.Fatalf("rebuilt conversations = %#v, %v", conversations, err)
	}
	snapshots, err := local.List(context.Background(), conversationSnapshotPrefix)
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("snapshot keys = %#v, %v", snapshots, err)
	}

	counting := &projectionCountingStore{Store: local, gets: map[string]int{}, lists: map[string]int{}}
	restarted, err := New(counting)
	if err != nil {
		t.Fatal(err)
	}
	conversations, err = restarted.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 1 || conversations[0].RecordCount != 2 {
		t.Fatalf("cold conversations = %#v, %v", conversations, err)
	}
	for key, count := range counting.gets {
		if strings.HasPrefix(key, historySegmentsPrefix+"/") && count > 0 {
			t.Fatalf("cold projection read fetched authoritative source %s", key)
		}
	}
	if counting.lists[historySegmentsPrefix] != 0 {
		t.Fatalf("cold projection listed authoritative sources: %#v", counting.lists)
	}
	beforeGets, beforeLists := counting.operationCounts()
	if _, err := restarted.Conversations(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	afterGets, afterLists := counting.operationCounts()
	if afterGets != beforeGets || afterLists != beforeLists {
		t.Fatalf("warm projection performed object operations: gets=%#v lists=%#v", counting.gets, counting.lists)
	}
}

func TestProjectionReadFailureRequiresExplicitRebuild(t *testing.T) {
	t.Parallel()
	local, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := New(local)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(context.Background(), AppendInput{
		RecordID: "rec_projection_corrupt", ConversationID: "conv_projection_corrupt",
		Kind: "message.user", Content: []byte(`{"text":"recover me"}`),
	}); err != nil {
		t.Fatal(err)
	}
	store := &projectionReadFailureStore{Store: local, fail: true}
	reader, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.Conversations(context.Background(), 10)
	var domainError *Error
	if !errors.As(err, &domainError) || domainError.Code != "FH_PROJECTION_READ_FAILED" {
		t.Fatalf("projection read error = %v", err)
	}
	rebuilt, err := reader.RebuildConversationProjection(context.Background())
	if err != nil || rebuilt.ConversationCount != 1 || rebuilt.SourceCount != 1 {
		t.Fatalf("rebuild = %#v, %v", rebuilt, err)
	}
	conversations, err := reader.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 1 || conversations[0].ID != "conv_projection_corrupt" {
		t.Fatalf("conversations after rebuild = %#v, %v", conversations, err)
	}
}

func TestSegmentProjectionAggregatesAgentsKindsAndTimes(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	agent := "planner"
	if _, err := service.AppendBatch(context.Background(), AppendBatchInput{
		SegmentID: "seg_projection_summary",
		Records: []AppendInput{
			{RecordID: "rec_projection_one", ConversationID: "conv_summary", Kind: "message.user", Agent: &agent, Content: []byte(`{"text":"one"}`)},
			{RecordID: "rec_projection_two", ConversationID: "conv_summary", Kind: "tool.result", Agent: &agent, Content: []byte(`{"ok":true}`)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	conversations, err := service.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("conversations = %#v, %v", conversations, err)
	}
	got := conversations[0]
	if got.RecordCount != 2 || len(got.Agents) != 1 || got.Agents[0] != agent || strings.Join(got.Kinds, ",") != "message.user,tool.result" {
		t.Fatalf("summary = %#v", got)
	}
}

func TestProjectionDiscoversExternalWriterAfterRefreshInterval(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	reader, err := New(store, withClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	conversations, err := reader.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 0 {
		t.Fatalf("initial conversations = %#v, %v", conversations, err)
	}
	writer, err := New(store, withClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(context.Background(), AppendInput{
		RecordID: "rec_external_writer", ConversationID: "conv_external_writer",
		Kind: "message.user", Content: []byte(`{"text":"new"}`),
	}); err != nil {
		t.Fatal(err)
	}
	conversations, err = reader.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 0 {
		t.Fatalf("view was not bounded-stale before refresh: %#v, %v", conversations, err)
	}
	clock = clock.Add(conversationRefreshInterval)
	conversations, err = reader.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 1 || conversations[0].ID != "conv_external_writer" {
		t.Fatalf("refreshed conversations = %#v, %v", conversations, err)
	}
}

func TestSegmentProjectionFailureIsRepairableByExactRetry(t *testing.T) {
	t.Parallel()
	local, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &projectionFailureStore{Store: local}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	input := AppendBatchInput{
		SegmentID: "seg_projection_retry",
		Records: []AppendInput{
			{RecordID: "rec_projection_batch_one", ConversationID: "conv_projection_batch", Kind: "message.user", Content: []byte(`{"text":"one"}`)},
			{RecordID: "rec_projection_batch_two", ConversationID: "conv_projection_batch", Kind: "message.assistant", Content: []byte(`{"text":"two"}`)},
		},
	}
	_, err = service.AppendBatch(context.Background(), input)
	var domainError *Error
	if !errors.As(err, &domainError) || domainError.Code != "FH_PROJECTION_WRITE_FAILED" {
		t.Fatalf("first append error = %v", err)
	}
	if _, err := service.ReadRecord(context.Background(), "rec_projection_batch_two"); err != nil {
		t.Fatalf("authoritative segment was not committed: %v", err)
	}
	if _, err := service.AppendBatch(context.Background(), input); err != nil {
		t.Fatalf("exact retry did not repair segment projection: %v", err)
	}
	conversations, err := service.Conversations(context.Background(), 10)
	if err != nil || len(conversations) != 1 || conversations[0].RecordCount != 2 {
		t.Fatalf("conversations = %#v, %v", conversations, err)
	}
}
