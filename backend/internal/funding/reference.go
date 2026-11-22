package funding

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// Crockford base32 without I, L, O, U — same alphabet as transfer receipts.
const referenceAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewReference builds a top-up receipt id like TU-7K2M-9QXA.
func NewReference() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("funding: reference entropy: %w", err)
	}
	chars := make([]byte, 8)
	for i, b := range buf {
		chars[i] = referenceAlphabet[int(b)%len(referenceAlphabet)]
	}
	return fmt.Sprintf("TU-%s-%s", chars[0:4], chars[4:8]), nil
}

func validReference(ref string) bool {
	// Shape: TU-XXXX-XXXX. Prefix "TU" is a kind label (U is not in Crockford);
	// only the eight entropy chars must be in the alphabet.
	if len(ref) != 12 {
		return false
	}
	if !strings.HasPrefix(ref, "TU-") || ref[7] != '-' {
		return false
	}
	for _, i := range []int{3, 4, 5, 6, 8, 9, 10, 11} {
		if !strings.ContainsRune(referenceAlphabet, rune(ref[i])) {
			return false
		}
	}
	return true
}
