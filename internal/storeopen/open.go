package storeopen

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Farfield-Dev/farfield/storage"
	"github.com/Farfield-Dev/farfield/storage/s3store"
)

func Open(ctx context.Context, uri string) (storage.Store, error) {
	if strings.HasPrefix(uri, "s3://") {
		pathStyle, err := strconv.ParseBool(valueOr(os.Getenv("FARFIELD_S3_PATH_STYLE"), "false"))
		if err != nil {
			return nil, fmt.Errorf("FARFIELD_S3_PATH_STYLE must be true or false: %w", err)
		}
		return s3store.Open(ctx, uri, s3store.Options{
			Endpoint:  os.Getenv("FARFIELD_S3_ENDPOINT"),
			Region:    os.Getenv("FARFIELD_S3_REGION"),
			PathStyle: pathStyle,
		})
	}
	return storage.OpenLocal(strings.TrimPrefix(uri, "file://"))
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
