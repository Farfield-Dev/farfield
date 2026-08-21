package s3store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Farfield-Dev/farfield/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3CompatibleIntegration(t *testing.T) {
	endpoint := os.Getenv("FARFIELD_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("FARFIELD_TEST_S3_ENDPOINT is not set")
	}
	ctx := context.Background()
	region := "us-east-1"
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	bucket := fmt.Sprintf("farfield-test-%d", time.Now().UnixNano())
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	store, err := New(client, bucket, "conformance")
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.PutIfAbsent(ctx, "conformance/objects/record.json", []byte(`{"ok":true}`), storage.PutOptions{ContentType: "application/json"})
	if err != nil || !created {
		t.Fatalf("first PutIfAbsent = %v, %v", created, err)
	}
	created, err = store.PutIfAbsent(ctx, "conformance/objects/record.json", []byte(`{"ok":true}`), storage.PutOptions{ContentType: "application/json"})
	if err != nil || created {
		t.Fatalf("idempotent PutIfAbsent = %v, %v", created, err)
	}
	data, err := store.Get(ctx, "conformance/objects/record.json")
	if err != nil || string(data) != `{"ok":true}` {
		t.Fatalf("Get = %s, %v", data, err)
	}
	keys, err := store.List(ctx, "conformance/objects")
	if err != nil || len(keys) != 1 || keys[0] != "conformance/objects/record.json" {
		t.Fatalf("List = %#v, %v", keys, err)
	}
}
