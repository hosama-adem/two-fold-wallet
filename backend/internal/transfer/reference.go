package transfer

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// Crockford base32 without I, L, O, U — readable on receipts and support calls.
const referenceAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewReference builds a receipt id like TF-7K2M-9QXA.
func NewReference() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("transfer: reference entropy: %w", err)
	}
	chars := make([]byte, 8)
	for i, b := range buf {
		chars[i] = referenceAlphabet[int(b)%len(referenceAlphabet)]
	}
	return fmt.Sprintf("TF-%s-%s", chars[0:4], chars[4:8]), nil
}

func validReference(ref string) bool {
	// Shape: TF-XXXX-XXXX (dashes at indexes 2 and 7).
	if len(ref) != 12 {
		return false
	}
	if !strings.HasPrefix(ref, "TF-") || ref[7] != '-' {
		return false
	}
	for i, r := range ref {
		if i == 2 || i == 7 {
			continue
		}
		if !strings.ContainsRune(referenceAlphabet, r) {
			return false
		}
	}
	return true
}
