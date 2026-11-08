package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrSecretSummary means the summary named a secret field (password, OTP,
// refresh token, PIN, or the same idea under another spelling).
// The audit row is not written. The error names the field, never the value.
var ErrSecretSummary = errors.New("audit summary contains a secret field")

// secretSummaryKeys are canonical field names. Canonical form is lowercase
// with underscores, hyphens, and spaces removed, so "refresh-token" and
// "refresh_token" hit the same entry.
//
// Match the whole key, not a substring. pin_set_before is a boolean
// before/after flag (FR-AUD-1), not the PIN.
var secretSummaryKeys = map[string]struct{}{
	"password":         {},
	"passwordhash":     {},
	"oldpassword":      {},
	"newpassword":      {},
	"otp":              {},
	"otpcode":          {},
	"onetimepassword":  {},
	"verificationcode": {},
	"code":             {},
	"refreshtoken":     {},
	"refreshhash":      {},
	"accesstoken":      {},
	"token":            {},
	"bearer":           {},
	"pin":              {},
	"oldpin":           {},
	"newpin":           {},
	"pinhash":          {},
	"secret":           {},
	"authorization":    {},
	"apikey":           {},
}

func prepareSummary(summary map[string]any) ([]byte, error) {
	if summary == nil {
		summary = map[string]any{}
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("audit summary: %w", err)
	}
	// Walk the JSON shape, not the Go value. A nested struct becomes a map
	// with its json tags, which is how a password field would actually land.
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("audit summary: %w", err)
	}
	if err := rejectSecretFields(decoded); err != nil {
		return nil, err
	}
	return raw, nil
}

func rejectSecretFields(v any) error {
	return walkSecretFields(v, "")
}

func walkSecretFields(v any, path string) error {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			name := k
			if path != "" {
				name = path + "." + k
			}
			if secretAuditKey(k) {
				return fmt.Errorf("%w: %s", ErrSecretSummary, name)
			}
			if err := walkSecretFields(child, name); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range t {
			name := fmt.Sprintf("[%d]", i)
			if path != "" {
				name = fmt.Sprintf("%s[%d]", path, i)
			}
			if err := walkSecretFields(child, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func secretAuditKey(k string) bool {
	_, ok := secretSummaryKeys[canonicalAuditKey(k)]
	return ok
}

func canonicalAuditKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	var b strings.Builder
	b.Grow(len(k))
	for _, r := range k {
		if r == '_' || r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
