// Package canonicaljson provides the single JSON encoding used for persisted hashes.
package canonicaljson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/gowebpki/jcs"
)

// Marshal encodes JSON using RFC 8785 JSON Canonicalization Scheme (JCS).
func Marshal(value any) ([]byte, error) {
	intermediate, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode JSON value: %w", err)
	}
	canonical, err := jcs.Transform(intermediate)
	if err != nil {
		return nil, fmt.Errorf("canonicalize JSON value: %w", err)
	}
	return canonical, nil
}

// Normalize validates arbitrary JSON and returns Farfield's deterministic encoding.
func Normalize(input []byte) ([]byte, error) {
	if len(bytes.TrimSpace(input)) == 0 {
		input = []byte("null")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode JSON: multiple values")
		}
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return Marshal(value)
}
