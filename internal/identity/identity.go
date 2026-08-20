// Package identity generates opaque Farfield identifiers.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func New(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
