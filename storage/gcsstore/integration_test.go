package gcsstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	farfieldstorage "github.com/Farfield-Dev/farfield/storage"
)

func TestGCSIntegration(t *testing.T) {
	uri := os.Getenv("FARFIELD_TEST_GCS_URI")
	if uri == "" {
		t.Skip("FARFIELD_TEST_GCS_URI is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, fmt.Sprintf("%s/conformance/%d", uri, time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	key := "records/v1/record.json"
	data := []byte(`{"ok":true}`)
	created, err := store.PutIfAbsent(ctx, key, data, farfieldstorage.PutOptions{ContentType: "application/json"})
	if err != nil || !created {
		t.Fatalf("first PutIfAbsent = %v, %v", created, err)
	}
	created, err = store.PutIfAbsent(ctx, key, data, farfieldstorage.PutOptions{})
	if err != nil || created {
		t.Fatalf("idempotent PutIfAbsent = %v, %v", created, err)
	}
	if _, err := store.PutIfAbsent(ctx, key, []byte(`{"ok":false}`), farfieldstorage.PutOptions{}); !errors.Is(err, farfieldstorage.ErrConflict) {
		t.Fatalf("conflicting PutIfAbsent error = %v", err)
	}
	stored, err := store.Get(ctx, key)
	if err != nil || string(stored) != string(data) {
		t.Fatalf("Get = %s, %v", stored, err)
	}
	keys, err := store.List(ctx, "records/v1")
	if err != nil || len(keys) != 1 || keys[0] != key {
		t.Fatalf("List = %#v, %v", keys, err)
	}
	if _, err := store.Get(ctx, "records/v1/missing.json"); !errors.Is(err, farfieldstorage.ErrNotFound) {
		t.Fatalf("missing Get error = %v", err)
	}

	concurrentKey := "records/v1/concurrent.json"
	var createdCount atomic.Int32
	var wait sync.WaitGroup
	errorsFound := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			created, err := store.PutIfAbsent(ctx, concurrentKey, data, farfieldstorage.PutOptions{})
			if err != nil {
				errorsFound <- err
				return
			}
			if created {
				createdCount.Add(1)
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent PutIfAbsent: %v", err)
	}
	if createdCount.Load() != 1 {
		t.Fatalf("concurrent created count = %d", createdCount.Load())
	}
}
