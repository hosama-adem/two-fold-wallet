package wallet

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// ethiopianMobile matches users_phone_e164_format. Masking refuses anything else
// so a shorter string cannot accidentally pass through unmasked.
var ethiopianMobile = regexp.MustCompile(`^\+251[79][0-9]{8}$`)

var handlePattern = regexp.MustCompile(`^[A-Za-z0-9._]{1,32}$`)

const recipientMissing = "No recipient matches that phone or handle."

// MaskPhone formats +2519XXXXXXXX as "+251 9•• ••• XXX" (FR-TRF-1).
// The full national number is not a substring of the result.
func MaskPhone(e164 string) (string, error) {
	if !ethiopianMobile.MatchString(e164) {
		return "", errors.New("wallet: phone is not an Ethiopian mobile number")
	}
	national := e164[4:]
	masked := "+251 " + national[:1] + "•• ••• " + national[6:]
	if strings.Contains(masked, e164) || strings.Contains(masked, national) {
		return "", errors.New("wallet: mask leaked the phone number")
	}
	return masked, nil
}

func looksLikePhone(q string) bool {
	digits := 0
	for _, r := range q {
		switch {
		case unicode.IsDigit(r):
			digits++
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')':
		default:
			return false
		}
	}
	return digits >= 9
}

func normalizeHandle(raw string) (string, error) {
	h := strings.TrimSpace(raw)
	h = strings.TrimPrefix(h, "@")
	if !handlePattern.MatchString(h) {
		return "", errors.New("wallet: handle is empty or has unsupported characters")
	}
	return h, nil
}
