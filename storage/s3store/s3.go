// Package s3store implements Farfield storage on S3-compatible APIs.
package s3store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/Farfield-Dev/farfield/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type Options struct {
	Endpoint  string
	Region    string
	PathStyle bool
}

type api interface {
	s3.ListObjectsV2APIClient
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type Store struct {
	client api
	bucket string
	prefix string
}

func Open(ctx context.Context, uri string, options Options) (*Store, error) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "s3" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid S3 URI %q", uri)
	}
	loadOptions := []func(*config.LoadOptions) error{}
	if options.Region != "" {
		loadOptions = append(loadOptions, config.WithRegion(options.Region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(value *s3.Options) {
		if options.Endpoint != "" {
			value.BaseEndpoint = aws.String(options.Endpoint)
		}
		value.UsePathStyle = options.PathStyle
	})
	return New(client, parsed.Host, strings.Trim(parsed.Path, "/"))
}

func New(client api, bucket, prefix string) (*Store, error) {
	if client == nil || bucket == "" {
		return nil, fmt.Errorf("S3 client and bucket are required")
	}
	if prefix != "" {
		var err error
		prefix, err = storage.ValidateKey(prefix)
		if err != nil {
			return nil, err
		}
	}
	return &Store{client: client, bucket: bucket, prefix: prefix}, nil
}

func (store *Store) Description() string {
	if store.prefix == "" {
		return "s3://" + store.bucket
	}
	return "s3://" + store.bucket + "/" + store.prefix
}

func (store *Store) objectKey(key string) (string, error) {
	safe, err := storage.ValidateKey(key)
	if err != nil {
		return "", err
	}
	if store.prefix == "" {
		return safe, nil
	}
	return store.prefix + "/" + safe, nil
}

func (store *Store) PutIfAbsent(ctx context.Context, key string, data []byte, options storage.PutOptions) (bool, error) {
	objectKey, err := store.objectKey(key)
	if err != nil {
		return false, err
	}
	input := &s3.PutObjectInput{
		Bucket:      aws.String(store.bucket),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(data),
		IfNoneMatch: aws.String("*"),
	}
	if options.ContentType != "" {
		input.ContentType = aws.String(options.ContentType)
	}
	_, err = store.client.PutObject(ctx, input)
	if err == nil {
		return true, nil
	}
	if isConflict(err) {
		existing, readErr := store.Get(ctx, key)
		if readErr != nil {
			return false, fmt.Errorf("read conflicting S3 object: %w", readErr)
		}
		if !bytes.Equal(existing, data) {
			return false, fmt.Errorf("%w: %s", storage.ErrConflict, key)
		}
		return false, nil
	}
	if code := apiCode(err); code == "InvalidRequest" || code == "NotImplemented" || code == "NotImplementedException" {
		return false, fmt.Errorf("S3 endpoint does not support atomic If-None-Match writes: %w", err)
	}
	return false, fmt.Errorf("put S3 object %q: %w", key, err)
}

func (store *Store) Get(ctx context.Context, key string) ([]byte, error) {
	objectKey, err := store.objectKey(key)
	if err != nil {
		return nil, err
	}
	result, err := store.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, key)
		}
		return nil, fmt.Errorf("get S3 object %q: %w", key, err)
	}
	defer result.Body.Close()
	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("read S3 object %q: %w", key, err)
	}
	return data, nil
}

func (store *Store) List(ctx context.Context, prefix string) ([]string, error) {
	objectPrefix, err := store.objectKey(prefix)
	if err != nil {
		return nil, err
	}
	objectPrefix = strings.TrimSuffix(objectPrefix, "/") + "/"
	paginator := s3.NewListObjectsV2Paginator(store.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(store.bucket),
		Prefix: aws.String(objectPrefix),
	})
	var keys []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list S3 objects under %q: %w", prefix, err)
		}
		for _, item := range page.Contents {
			key := aws.ToString(item.Key)
			if store.prefix != "" {
				key = strings.TrimPrefix(key, store.prefix+"/")
			}
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func apiCode(err error) string {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		return apiError.ErrorCode()
	}
	return ""
}

func statusCode(err error) int {
	var responseError *smithyhttp.ResponseError
	if errors.As(err, &responseError) {
		return responseError.HTTPStatusCode()
	}
	return 0
}

func isConflict(err error) bool {
	code := apiCode(err)
	status := statusCode(err)
	return code == "PreconditionFailed" || code == "ConditionalRequestConflict" || status == 409 || status == 412
}

func isNotFound(err error) bool {
	code := apiCode(err)
	return code == "NoSuchKey" || code == "NotFound" || statusCode(err) == 404
}
