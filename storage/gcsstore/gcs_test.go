package gcsstore

import (
	"context"
	"testing"

	"cloud.google.com/go/storage"
)

func TestNewAndDescription(t *testing.T) {
	t.Parallel()
	store, err := New(&storage.Client{}, "bucket", "/tenant/agent/")
	if err != nil {
		t.Fatal(err)
	}
	if store.Description() != "gs://bucket/tenant/agent" {
		t.Fatalf("description = %q", store.Description())
	}
	key, err := store.objectKey("conformance/objects/a.json")
	if err != nil || key != "tenant/agent/conformance/objects/a.json" {
		t.Fatalf("object key = %q, %v", key, err)
	}
}

func TestOpenRejectsInvalidURI(t *testing.T) {
	t.Parallel()
	for _, uri := range []string{"gs://", "s3://bucket", "gs://bucket/path?query=bad", "gs://bucket/path#fragment"} {
		if _, err := Open(context.Background(), uri); err == nil {
			t.Fatalf("Open(%q) succeeded", uri)
		}
	}
}
