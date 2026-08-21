// Package storage defines Farfield's object-storage contract.
package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound = errors.New("object not found")
	ErrConflict = errors.New("immutable object conflict")
)

type PutOptions struct {
	ContentType string
}

// Store is deliberately object-shaped rather than filesystem-shaped.
// PutIfAbsent must be atomic: a backend that implements HEAD followed by PUT
// does not satisfy this interface. Once PutIfAbsent succeeds, later Get and
// List calls must observe the object; immutable projections depend on strong
// read-after-write and list consistency.
type Store interface {
	Description() string
	PutIfAbsent(ctx context.Context, key string, data []byte, options PutOptions) (created bool, err error)
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
}

func ValidateKey(key string) (string, error) {
	normalized := strings.Trim(key, "/")
	if normalized == "" || strings.Contains(normalized, "\\") {
		return "", fmt.Errorf("unsafe object key %q", key)
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("unsafe object key %q", key)
		}
	}
	return normalized, nil
}
