package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
)

const (
	OTPLength      = 6
	OTPMaxAttempts = 5
)

// GenerateOTP returns a cryptographically random 6-digit code (leading zeros allowed).
func GenerateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("otp: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// HashOTP returns a hex-encoded SHA-256 digest of the code.
func HashOTP(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// OTPMatches compares a plaintext code to a stored hash in constant time.
func OTPMatches(code, codeHash string) bool {
	got := HashOTP(code)
	if len(got) != len(codeHash) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(codeHash)) == 1
}
