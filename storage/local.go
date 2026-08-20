package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Local struct {
	root string
}

func OpenLocal(root string) (*Local, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &Local{root: absolute}, nil
}

func (store *Local) Description() string { return store.root }

func (store *Local) path(key string) (string, error) {
	safe, err := ValidateKey(key)
	if err != nil {
		return "", err
	}
	path := filepath.Join(store.root, filepath.FromSlash(safe))
	relative, err := filepath.Rel(store.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe object key %q", key)
	}
	return path, nil
}

func (store *Local) PutIfAbsent(_ context.Context, key string, data []byte, _ PutOptions) (bool, error) {
	path, err := store.path(key)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create object directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return false, fmt.Errorf("create temporary object: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return false, fmt.Errorf("secure temporary object: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return false, fmt.Errorf("write temporary object: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return false, fmt.Errorf("sync temporary object: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close temporary object: %w", err)
	}
	if err := os.Link(temporaryName, path); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return false, fmt.Errorf("commit immutable object: %w", err)
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return false, fmt.Errorf("read conflicting object: %w", readErr)
		}
		if !bytes.Equal(existing, data) {
			return false, fmt.Errorf("%w: %s", ErrConflict, key)
		}
		return false, nil
	}
	if directory, err := os.Open(filepath.Dir(path)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return true, nil
}

func (store *Local) Get(_ context.Context, key string) ([]byte, error) {
	path, err := store.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data, nil
}

func (store *Local) List(_ context.Context, prefix string) ([]string, error) {
	safe, err := ValidateKey(prefix)
	if err != nil {
		return nil, err
	}
	base, err := store.path(safe)
	if err != nil {
		return nil, err
	}
	var keys []string
	err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		relative, err := filepath.Rel(store.root, path)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(relative))
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	sort.Strings(keys)
	return keys, nil
}
