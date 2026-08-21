package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestLocalStoreContract(t *testing.T) {
	t.Parallel()
	store, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := store.PutIfAbsent(ctx, "conformance/objects/a.json", []byte("one"), PutOptions{})
	if err != nil || !created {
		t.Fatalf("first PutIfAbsent = %v, %v", created, err)
	}
	created, err = store.PutIfAbsent(ctx, "conformance/objects/a.json", []byte("one"), PutOptions{})
	if err != nil || created {
		t.Fatalf("idempotent PutIfAbsent = %v, %v", created, err)
	}
	if _, err := store.PutIfAbsent(ctx, "conformance/objects/a.json", []byte("two"), PutOptions{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting PutIfAbsent error = %v", err)
	}
	data, err := store.Get(ctx, "conformance/objects/a.json")
	if err != nil || string(data) != "one" {
		t.Fatalf("Get = %q, %v", data, err)
	}
	keys, err := store.List(ctx, "conformance/objects")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keys, []string{"conformance/objects/a.json"}) {
		t.Fatalf("List = %#v", keys)
	}
}

func TestLocalRejectsUnsafeKeys(t *testing.T) {
	t.Parallel()
	store, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "../escape", "a/../../escape", `a\\b`} {
		if _, err := store.Get(context.Background(), key); err == nil {
			t.Fatalf("Get accepted unsafe key %q", key)
		}
	}
}
