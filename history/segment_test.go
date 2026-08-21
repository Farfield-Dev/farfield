package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Farfield-Dev/farfield/storage"
)

type ambiguousOnceStore struct {
	storage.Store
	mu     sync.Mutex
	failed bool
}

func (store *ambiguousOnceStore) PutIfAbsent(ctx context.Context, key string, data []byte, options storage.PutOptions) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	created, err := store.Store.PutIfAbsent(ctx, key, data, options)
	if err != nil {
		return created, err
	}
	if !store.failed {
		store.failed = true
		return false, fmt.Errorf("simulated response loss after commit")
	}
	return created, nil
}

func TestAppendBatchCommitsOneSegment(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, time.August, 19, 12, 0, 0, 123456000, time.UTC)
	service, err := New(store, withClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	segment, err := service.AppendBatch(context.Background(), AppendBatchInput{
		SegmentID: "seg_test",
		Records: []AppendInput{
			{RecordID: "rec_one", ConversationID: "conv_test", Kind: "message.input", Content: []byte(`{"text":"hello"}`)},
			{RecordID: "rec_two", ConversationID: "conv_test", Kind: "tool.output", Content: []byte(`{"ok":true}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if segment.SegmentSHA256 == "" || len(segment.Entries) != 2 {
		t.Fatalf("segment = %#v", segment)
	}
	if segment.Entries[0].Record.SchemaVersion != RecordSchema || segment.Entries[0].Record.Content.Storage != "segment" {
		t.Fatalf("record content reference = %#v", segment.Entries[0].Record.Content)
	}
	keys, err := store.List(context.Background(), historySegmentsPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != segmentKey("seg_test", "conv_test") {
		t.Fatalf("segment keys = %#v", keys)
	}
	blobKeys, err := store.List(context.Background(), historyBlobPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(blobKeys) != 0 {
		t.Fatalf("blob keys = %#v", blobKeys)
	}
	record, err := service.ReadRecord(context.Background(), "rec_two")
	if err != nil {
		t.Fatal(err)
	}
	content, err := service.ReadContent(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"ok":true}` {
		t.Fatalf("content = %s", content)
	}
	verification, err := service.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !verification.OK || verification.Records != 2 || verification.Segments != 1 || verification.Blobs != 0 {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestAppendBatchStoresLargeContentAsBlob(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, WithMaxInlineContentBytes(8))
	if err != nil {
		t.Fatal(err)
	}
	segment, err := service.AppendBatch(context.Background(), AppendBatchInput{
		SegmentID: "seg_artifact",
		Records:   []AppendInput{{RecordID: "rec_artifact", ConversationID: "conv_test", Kind: "tool.output", Content: []byte(`{"output":"large"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := segment.Entries[0].Record
	if record.Content.Storage != "blob" || record.Content.EntryIndex != nil || len(segment.Entries[0].Content) != 0 {
		t.Fatalf("external content reference = %#v", record.Content)
	}
	content, err := service.ReadContent(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"output":"large"}` {
		t.Fatalf("content = %s", content)
	}
	verification, err := service.Verify(context.Background())
	if err != nil || !verification.OK || verification.Blobs != 1 || verification.Segments != 1 {
		t.Fatalf("verification = %#v, %v", verification, err)
	}
}

func TestAppendBatchRetryReturnsCommittedSegment(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	service, err := New(store, withClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	input := AppendBatchInput{
		SegmentID: "seg_retry",
		Records:   []AppendInput{{ConversationID: "conv_test", Kind: "message", Content: []byte(`{"b":2,"a":1}`)}},
	}
	first, err := service.AppendBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	second, err := service.AppendBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.SegmentSHA256 != second.SegmentSHA256 || first.Entries[0].Record.ID != second.Entries[0].Record.ID || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatal("idempotent retry did not return the committed segment")
	}
}

func TestAppendBatchRecoversAmbiguousCommit(t *testing.T) {
	t.Parallel()
	base, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &ambiguousOnceStore{Store: base}
	clock := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	service, err := New(store, withClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	input := AppendBatchInput{
		SegmentID: "seg_ambiguous",
		Records:   []AppendInput{{ConversationID: "conv_test", Kind: "message", Content: []byte(`{"ok":true}`)}},
	}
	if _, err := service.AppendBatch(context.Background(), input); err == nil {
		t.Fatal("ambiguous first commit unexpectedly succeeded")
	}
	clock = clock.Add(time.Hour)
	segment, err := service.AppendBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if segment.ID != "seg_ambiguous" || len(segment.Entries) != 1 {
		t.Fatalf("recovered segment = %#v", segment)
	}
	verification, err := service.Verify(context.Background())
	if err != nil || !verification.OK || verification.Segments != 1 {
		t.Fatalf("verification = %#v, %v", verification, err)
	}
}

func TestConcurrentAppendBatchIsIdempotent(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	input := AppendBatchInput{
		SegmentID: "seg_concurrent",
		Records:   []AppendInput{{ConversationID: "conv_test", Kind: "message", Content: []byte(`{"ok":true}`)}},
	}
	const writers = 16
	segments := make(chan Segment, writers)
	errorsFound := make(chan error, writers)
	var group sync.WaitGroup
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			segment, err := service.AppendBatch(context.Background(), input)
			if err != nil {
				errorsFound <- err
				return
			}
			segments <- segment
		}()
	}
	group.Wait()
	close(segments)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var committed string
	for segment := range segments {
		if committed == "" {
			committed = segment.SegmentSHA256
		}
		if segment.SegmentSHA256 != committed {
			t.Fatalf("writers returned different segments: %s and %s", committed, segment.SegmentSHA256)
		}
	}
	keys, err := store.List(context.Background(), historySegmentsPrefix)
	if err != nil || len(keys) != 1 {
		t.Fatalf("segment keys = %#v, %v", keys, err)
	}
}

func TestAppendBatchRejectsSegmentIDReuse(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	input := AppendBatchInput{
		SegmentID: "seg_same",
		Records:   []AppendInput{{ConversationID: "conv_test", Kind: "message", Content: []byte(`{"n":1}`)}},
	}
	if _, err := service.AppendBatch(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	input.Records[0].Content = []byte(`{"n":2}`)
	_, err = service.AppendBatch(context.Background(), input)
	var domainError *Error
	if !errors.As(err, &domainError) || domainError.Code != "FH_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflicting append error = %v", err)
	}
}

func TestSingleAndBatchSegmentsShareOneTimeline(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Append(context.Background(), AppendInput{
		RecordID: "rec_single", ConversationID: "conv_test", Kind: "message.input", Content: []byte(`{"n":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBatch(context.Background(), AppendBatchInput{
		SegmentID: "seg_new",
		Records:   []AppendInput{{RecordID: "rec_segment", ConversationID: "conv_test", Kind: "message.output", Content: []byte(`{"n":2}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Timeline(context.Background(), "conv_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("timeline entries = %d", len(entries))
	}
	verification, err := service.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !verification.OK || verification.Records != 2 || verification.Segments != 2 || verification.Blobs != 0 {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestAppendBatchRejectsMixedConversations(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AppendBatch(context.Background(), AppendBatchInput{Records: []AppendInput{
		{ConversationID: "conv_one", Kind: "message", Content: []byte(`null`)},
		{ConversationID: "conv_two", Kind: "message", Content: []byte(`null`)},
	}})
	var domainError *Error
	if !errors.As(err, &domainError) || domainError.Code != "FH_INVALID_BATCH" {
		t.Fatalf("mixed conversation error = %v", err)
	}
}

func TestAppendBatchRejectsDuplicateRecordIDs(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AppendBatch(context.Background(), AppendBatchInput{
		SegmentID: "seg_duplicates",
		Records: []AppendInput{
			{RecordID: "rec_same", ConversationID: "conv_test", Kind: "message", Content: []byte(`{"n":1}`)},
			{RecordID: "rec_same", ConversationID: "conv_test", Kind: "message", Content: []byte(`{"n":2}`)},
		},
	})
	var domainError *Error
	if !errors.As(err, &domainError) || domainError.Code != "FH_INVALID_SEGMENT" {
		t.Fatalf("duplicate record error = %v", err)
	}
	keys, listErr := store.List(context.Background(), historySegmentsPrefix)
	if listErr != nil || len(keys) != 0 {
		t.Fatalf("invalid segment was written: %#v, %v", keys, listErr)
	}
}

func TestVerifyDetectsCorruptSegment(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := storage.OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBatch(context.Background(), AppendBatchInput{
		SegmentID: "seg_corrupt",
		Records:   []AppendInput{{ConversationID: "conv_test", Kind: "message", Content: []byte(`{"safe":true}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(segmentKey("seg_corrupt", "conv_test")))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for index := range data {
		if data[index] == 't' {
			data[index] = 'f'
			break
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := service.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || len(result.Issues) != 1 {
		t.Fatalf("verification = %#v", result)
	}
}
