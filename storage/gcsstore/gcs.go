// Package gcsstore implements Farfield storage on Google Cloud Storage.
package gcsstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"cloud.google.com/go/storage"
	farfieldstorage "github.com/Farfield-Dev/farfield/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

type Store struct {
	client *storage.Client
	bucket string
	prefix string
}

// Open creates a GCS-backed store using Application Default Credentials.
func Open(ctx context.Context, uri string) (*Store, error) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "gs" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid GCS URI %q", uri)
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}
	store, err := New(client, parsed.Host, strings.Trim(parsed.Path, "/"))
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return store, nil
}

func New(client *storage.Client, bucket, prefix string) (*Store, error) {
	if client == nil || bucket == "" {
		return nil, errors.New("GCS client and bucket are required")
	}
	if prefix != "" {
		var err error
		prefix, err = farfieldstorage.ValidateKey(prefix)
		if err != nil {
			return nil, err
		}
	}
	return &Store{client: client, bucket: bucket, prefix: prefix}, nil
}

func (store *Store) Description() string {
	if store.prefix == "" {
		return "gs://" + store.bucket
	}
	return "gs://" + store.bucket + "/" + store.prefix
}

func (store *Store) objectKey(key string) (string, error) {
	safe, err := farfieldstorage.ValidateKey(key)
	if err != nil {
		return "", err
	}
	if store.prefix == "" {
		return safe, nil
	}
	return store.prefix + "/" + safe, nil
}

// PutIfAbsent uses GCS's generation-match precondition. DoesNotExist maps to
// ifGenerationMatch=0, so concurrent writers cannot overwrite an object.
func (store *Store) PutIfAbsent(ctx context.Context, key string, data []byte, options farfieldstorage.PutOptions) (bool, error) {
	objectKey, err := store.objectKey(key)
	if err != nil {
		return false, err
	}
	writer := store.client.Bucket(store.bucket).Object(objectKey).
		If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	if options.ContentType != "" {
		writer.ContentType = options.ContentType
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.CloseWithError(err)
		return false, fmt.Errorf("write GCS object %q: %w", key, err)
	}
	if err := writer.Close(); err == nil {
		return true, nil
	} else if !isPreconditionFailed(err) {
		return false, fmt.Errorf("commit GCS object %q: %w", key, err)
	}

	existing, err := store.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("read conflicting GCS object: %w", err)
	}
	if !bytes.Equal(existing, data) {
		return false, fmt.Errorf("%w: %s", farfieldstorage.ErrConflict, key)
	}
	return false, nil
}

func (store *Store) Get(ctx context.Context, key string) ([]byte, error) {
	objectKey, err := store.objectKey(key)
	if err != nil {
		return nil, err
	}
	reader, err := store.client.Bucket(store.bucket).Object(objectKey).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, fmt.Errorf("%w: %s", farfieldstorage.ErrNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("get GCS object %q: %w", key, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read GCS object %q: %w", key, err)
	}
	return data, nil
}

func (store *Store) List(ctx context.Context, prefix string) ([]string, error) {
	objectPrefix, err := store.objectKey(prefix)
	if err != nil {
		return nil, err
	}
	objectPrefix = strings.TrimSuffix(objectPrefix, "/") + "/"
	objects := store.client.Bucket(store.bucket).Objects(ctx, &storage.Query{Prefix: objectPrefix})
	var keys []string
	for {
		attributes, err := objects.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list GCS objects under %q: %w", prefix, err)
		}
		key := attributes.Name
		if store.prefix != "" {
			key = strings.TrimPrefix(key, store.prefix+"/")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func isPreconditionFailed(err error) bool {
	var apiError *googleapi.Error
	return errors.As(err, &apiError) && apiError.Code == 412
}
