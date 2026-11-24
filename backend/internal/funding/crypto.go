package funding

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

// SecretBox seals payout destination identifiers with AES-256-GCM.
type SecretBox struct {
	gcm cipher.AEAD
}

// NewSecretBox builds a box from a 32-byte key (raw or standard base64).
func NewSecretBox(keyMaterial string) (*SecretBox, error) {
	key, err := parseAESKey(keyMaterial)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("funding: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("funding: gcm: %w", err)
	}
	return &SecretBox{gcm: gcm}, nil
}

func parseAESKey(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("funding: payout encryption key is empty")
	}
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("funding: payout encryption key must be 32 bytes (or base64 of 32 bytes)")
}

// Seal encrypts plaintext and returns ciphertext + 12-byte nonce.
func (b *SecretBox) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	if b == nil || b.gcm == nil {
		return nil, nil, fmt.Errorf("funding: secret box is not configured")
	}
	nonce = make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("funding: nonce: %w", err)
	}
	ciphertext = b.gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Open decrypts ciphertext with nonce.
func (b *SecretBox) Open(ciphertext, nonce []byte) ([]byte, error) {
	if b == nil || b.gcm == nil {
		return nil, fmt.Errorf("funding: secret box is not configured")
	}
	plain, err := b.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("funding: decrypt: %w", err)
	}
	return plain, nil
}

// MaskIdentifier hides the sensitive destination for API responses.
// Mobile-money phones use the wallet mask; other identifiers keep last 4.
func MaskIdentifier(kind, identifier string) (string, error) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return "", errDestinationIdentifier()
	}
	if kind == destinationMobileMoney {
		if masked, err := wallet.MaskPhone(id); err == nil {
			return masked, nil
		}
	}
	runes := []rune(id)
	if len(runes) <= 4 {
		return strings.Repeat("•", utf8.RuneCountInString(id)), nil
	}
	return strings.Repeat("•", len(runes)-4) + string(runes[len(runes)-4:]), nil
}
