package s3store

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/Farfield-Dev/farfield/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type memoryAPI struct {
	objects map[string][]byte
}

func (api *memoryAPI) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	key := aws.ToString(input.Key)
	if _, exists := api.objects[key]; exists {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "already exists"}
	}
	data, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	api.objects[key] = data
	return &s3.PutObjectOutput{}, nil
}

func (api *memoryAPI) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	data, exists := api.objects[aws.ToString(input.Key)]
	if !exists {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing"}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (api *memoryAPI) ListObjectsV2(_ context.Context, input *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	var keys []string
	for key := range api.objects {
		if strings.HasPrefix(key, aws.ToString(input.Prefix)) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	result := &s3.ListObjectsV2Output{}
	for _, key := range keys {
		result.Contents = append(result.Contents, types.Object{Key: aws.String(key)})
	}
	return result, nil
}

func TestS3StoreContract(t *testing.T) {
	t.Parallel()
	api := &memoryAPI{objects: map[string][]byte{}}
	store, err := New(api, "bucket", "tenant")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := store.PutIfAbsent(ctx, "conformance/objects/a.json", []byte("one"), storage.PutOptions{ContentType: "application/json"})
	if err != nil || !created {
		t.Fatalf("first PutIfAbsent = %v, %v", created, err)
	}
	created, err = store.PutIfAbsent(ctx, "conformance/objects/a.json", []byte("one"), storage.PutOptions{})
	if err != nil || created {
		t.Fatalf("idempotent PutIfAbsent = %v, %v", created, err)
	}
	if _, err := store.PutIfAbsent(ctx, "conformance/objects/a.json", []byte("two"), storage.PutOptions{}); err == nil {
		t.Fatal("conflicting PutIfAbsent succeeded")
	}
	keys, err := store.List(ctx, "conformance/objects")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "conformance/objects/a.json" {
		t.Fatalf("List = %#v", keys)
	}
}
