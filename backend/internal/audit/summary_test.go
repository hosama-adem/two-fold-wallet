package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPrepareSummaryAllowsAuthOutcomes(t *testing.T) {
	t.Parallel()

	// These are the summaries the auth and PIN paths actually write.
	allowed := []map[string]any{
		{"result": "ok"},
		{"result": "invalid_credentials"},
		{"via": "api"},
		{"via": "logout"},
		{"pin_set_before": false, "pin_set_after": true},
		{"failed_count": 1, "tries_remaining": 4, "locked": false},
		{"failed_count": 5, "locked_until": "2026-11-08T00:00:00Z", "duration_m": 15},
		nil,
	}
	for _, summary := range allowed {
		if _, err := prepareSummary(summary); err != nil {
			t.Fatalf("prepareSummary(%v)=%v", summary, err)
		}
	}
}

func TestPrepareSummaryRejectsSecrets(t *testing.T) {
	t.Parallel()

	type tagged struct {
		Code string `json:"code"`
	}

	cases := []struct {
		name    string
		summary map[string]any
		field   string
	}{
		{"password", map[string]any{"password": "hunter2"}, "password"},
		{"hyphenated refresh token", map[string]any{"refresh-token": "r"}, "refresh-token"},
		{"camel pin", map[string]any{"oldPin": "4829"}, "oldPin"},
		{"nested otp", map[string]any{"meta": map[string]any{"otp": "123456"}}, "meta.otp"},
		{"array pin", map[string]any{"events": []any{map[string]any{"pin": "4829"}}}, "events[0].pin"},
		{"json tag on a struct", map[string]any{"body": tagged{Code: "123456"}}, "body.code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := prepareSummary(tc.summary)
			if !errors.Is(err, ErrSecretSummary) {
				t.Fatalf("err=%v", err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error %q does not name %s", err.Error(), tc.field)
			}
			if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "123456") || strings.Contains(err.Error(), "4829") {
				t.Fatalf("secret value leaked into error: %v", err)
			}
		})
	}
}

func TestRecordRejectsSecretWithoutDatabase(t *testing.T) {
	t.Parallel()

	err := NewRecorder(nil).Record(context.Background(), Entry{
		ActorKind: ActorAnonymous,
		Action:    ActionLoginFailure,
		Summary:   map[string]any{"password": "hunter2"},
	})
	if !errors.Is(err, ErrSecretSummary) {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("secret value leaked into error: %v", err)
	}
}
