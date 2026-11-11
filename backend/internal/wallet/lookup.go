package wallet

import (
	"regexp"
	"strings"

	"github.com/hosama-adem/twofold/backend/internal/phone"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// Handle is 3–20 chars: lowercase letters, digits, underscore (FR-ID-9).
var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// QueryKind is how a recipient lookup string was interpreted.
type QueryKind int

const (
	// QueryPhone means the input normalized to E.164.
	QueryPhone QueryKind = iota
	// QueryHandle means the input is an @handle (with or without @).
	QueryHandle
)

// LookupQuery is a normalized recipient lookup.
type LookupQuery struct {
	Kind  QueryKind
	Value string
}

// ParseLookup turns a phone or @handle into a normalized lookup query.
func ParseLookup(raw string) (LookupQuery, error) {
	q := strings.TrimSpace(raw)
	if q == "" || len(q) > 32 {
		return LookupQuery{}, httpx.ValidationFailed("Enter a phone number or @handle.")
	}
	if strings.HasPrefix(q, "@") {
		handle := strings.ToLower(strings.TrimPrefix(q, "@"))
		if !handlePattern.MatchString(handle) {
			return LookupQuery{}, httpx.ValidationFailed("Handle must be 3–20 characters: lowercase letters, digits, or underscore.")
		}
		return LookupQuery{Kind: QueryHandle, Value: handle}, nil
	}
	if looksLikePhone(q) {
		normalized, err := phone.Normalize(q)
		if err != nil {
			return LookupQuery{}, httpx.ValidationFailed("Phone must be an Ethiopian mobile number in E.164 (+2519… or +2517…).")
		}
		return LookupQuery{Kind: QueryPhone, Value: normalized}, nil
	}
	handle := strings.ToLower(q)
	if !handlePattern.MatchString(handle) {
		return LookupQuery{}, httpx.ValidationFailed("Enter a phone number or @handle.")
	}
	return LookupQuery{Kind: QueryHandle, Value: handle}, nil
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
