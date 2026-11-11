package phone

import (
	"errors"
	"regexp"
	"strings"
)

// ErrInvalid means raw is not an Ethiopian mobile number.
var ErrInvalid = errors.New("phone: not an Ethiopian mobile number")

var ethiopianMobile = regexp.MustCompile(`^\+251[79][0-9]{8}$`)

// Normalize trims an Ethiopian mobile number into E.164 (+2519… or +2517…).
// Local 09/07 and a bare 251 prefix are accepted.
func Normalize(raw string) (string, error) {
	phone := strings.TrimSpace(raw)
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")

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
		return "", ErrInvalid
	}
	return phone, nil
}
