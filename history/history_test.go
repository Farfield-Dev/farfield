package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Farfield-Dev/farfield/storage"
)

func TestAppendReadAndVerify(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := storage.OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, time.August, 19, 12, 0, 0, 123456000, time.UTC)
	service, err := New(store, withClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	input := AppendInput{
		RecordID: "rec_test", ConversationID: "conv_test", Kind: "model.response",
		Content: []byte(`{"text":"hello","model":"gpt-5"}`), Tags: map[string]string{"env": "test"},
	}
	first, err := service.Append(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	second, err := service.Append(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second.RecordSHA256 != first.RecordSHA256 || !second.RecordedAt.Equal(first.RecordedAt) {
		t.Fatal("idempotent retry did not return the original sealed record")
	}
	loaded, err := service.ReadRecord(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	content, err := service.ReadContent(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"model":"gpt-5","text":"hello"}` {
		t.Fatalf("canonical content = %s", content)
	}
	verification, err := service.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !verification.OK || verification.Records != 1 || verification.Blobs != 1 {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestAppendRejectsRecordIDReuse(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	base := AppendInput{RecordID: "rec_same", ConversationID: "conv_test", Kind: "message", Content: []byte(`{"n":1}`)}
	if _, err := service.Append(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.Content = []byte(`{"n":2}`)
	_, err = service.Append(context.Background(), base)
	var domainError *Error
	if !errors.As(err, &domainError) || domainError.Code != "FH_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflicting append error = %v", err)
	}
}

func TestVerifyDetectsCorruptBlob(t *testing.T) {
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
	record, err := service.Append(context.Background(), AppendInput{
		ConversationID: "conv_test", Kind: "message", Content: []byte(`{"safe":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	blobPath := filepath.Join(root, filepath.FromSlash(record.Content.Key))
	if err := os.WriteFile(blobPath, []byte(`{"safe":false}`), 0o600); err != nil {
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
