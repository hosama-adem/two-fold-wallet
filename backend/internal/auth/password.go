package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16

	minPasswordLen = 10
)

// CommonPasswords is a small local blocklist (extensible later for HIBP-style checks).
var CommonPasswords = map[string]struct{}{
	"password": {}, "password123": {}, "password1234": {}, "1234567890": {},
	"123456789": {}, "qwerty1234": {}, "qwertyuiop": {}, "iloveyou12": {},
	"admin12345": {}, "welcome123": {}, "letmein123": {}, "ethiopia1": {},
	"twofold123": {}, "abcdefghij": {}, "1111111111": {}, "0000000000": {},
}

// ValidatePassword enforces length and the local common-password blocklist.
func ValidatePassword(password string) error {
	if len(password) < minPasswordLen {
		return httpx.ValidationFailed("Password must be at least 10 characters.")
	}
	if len(password) > 128 {
		return httpx.ValidationFailed("Password must be at most 128 characters.")
	}
	lower := strings.ToLower(password)
	if _, blocked := CommonPasswords[lower]; blocked {
		return httpx.ValidationFailed("Password is too common. Choose a stronger password.")
	}
	return nil
}

// HashPassword returns a PHC-style argon2id encoded hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword checks a password against an encoded argon2id hash.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("unsupported hash format")
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false, fmt.Errorf("parse params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
