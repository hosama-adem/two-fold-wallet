package auth

import (
	"github.com/hosama-adem/twofold/backend/internal/phone"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// NormalizePhone trims and validates an Ethiopian E.164 mobile number.
func NormalizePhone(raw string) (string, error) {
	normalized, err := phone.Normalize(raw)
	if err != nil {
		return "", httpx.ValidationFailed("Phone must be an Ethiopian mobile number in E.164 (+2519… or +2517…).")
	}
	return normalized, nil
}
