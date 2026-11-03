package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// New returns a random opaque identifier (hex-encoded 16 bytes).
// UUIDv7 arrives with the users migration; this keeps early code ID-ready.
func New() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// MustNew is like New but panics on failure.
func MustNew() string {
	id, err := New()
	if err != nil {
		panic(err)
	}
	return id
}
