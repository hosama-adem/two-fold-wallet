package auth

import (
	"regexp"
	"strings"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

var ethiopianMobile = regexp.MustCompile(`^\+251[79][0-9]{8}$`)

// NormalizePhone trims and validates an Ethiopian E.164 mobile number.
func NormalizePhone(raw string) (string, error) {
	phone := strings.TrimSpace(raw)
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")

	// Accept local 09XXXXXXXX / 07XXXXXXXX → +2519… / +2517…
	if strings.HasPrefix(phone, "09") && len(phone) == 10 {
		phone = "+251" + phone[1:]
	}
	if strings.HasPrefix(phone, "07") && len(phone) == 10 {
		phone = "+251" + phone[1:]
	}
	if strings.HasPrefix(phone, "251") && len(phone) == 12 {
		phone = "+" + phone
	}

	if !ethiopianMobile.MatchString(phone) {
		return "", httpx.ValidationFailed("Phone must be an Ethiopian mobile number in E.164 (+2519… or +2517…).")
	}
	return phone, nil
}
