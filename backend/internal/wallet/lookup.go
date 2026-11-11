package wallet

import (
	"regexp"
	"strings"

	"github.com/hosama-adem/twofold/backend/internal/phone"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// Handle is 3–20 chars: lowercase letters, digits, underscore (FR-ID-9).
var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

type queryKind int

const (
	queryPhone queryKind = iota
	queryHandle
)

type lookupQuery struct {
	kind  queryKind
	value string
}

func parseLookup(raw string) (lookupQuery, error) {
	q := strings.TrimSpace(raw)
	if q == "" || len(q) > 32 {
		return lookupQuery{}, httpx.ValidationFailed("Enter a phone number or @handle.")
	}
	if strings.HasPrefix(q, "@") {
		handle := strings.ToLower(strings.TrimPrefix(q, "@"))
		if !handlePattern.MatchString(handle) {
			return lookupQuery{}, httpx.ValidationFailed("Handle must be 3–20 characters: lowercase letters, digits, or underscore.")
		}
		return lookupQuery{kind: queryHandle, value: handle}, nil
	}
	if looksLikePhone(q) {
		normalized, err := phone.Normalize(q)
		if err != nil {
			return lookupQuery{}, httpx.ValidationFailed("Phone must be an Ethiopian mobile number in E.164 (+2519… or +2517…).")
		}
		return lookupQuery{kind: queryPhone, value: normalized}, nil
	}
	handle := strings.ToLower(q)
	if !handlePattern.MatchString(handle) {
		return lookupQuery{}, httpx.ValidationFailed("Enter a phone number or @handle.")
	}
	return lookupQuery{kind: queryHandle, value: handle}, nil
}

func looksLikePhone(q string) bool {
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')':
			return -1
		default:
			return r
		}
	}, q)
	if compact == "" {
		return false
	}
	if strings.HasPrefix(compact, "+") {
		compact = compact[1:]
	}
	for _, r := range compact {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
