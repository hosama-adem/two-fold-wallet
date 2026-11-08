package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
)

// TestAuthAuditTrail records the four auth events and reads them back.
// Summaries are outcomes. The password, OTP, and tokens used in the requests
// must not appear in the row.
func TestAuthAuditTrail(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}

	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)

	password := "correct-horse-1"
	wrong := "wrong-password-zz"
	phone := uniquePhone()
	registerAndVerify(t, srv, sms, phone, password)

	anonID := "audit-anon-" + uuid.NewString()
	failID := "audit-fail-" + uuid.NewString()
	ok1ID := "audit-ok1-" + uuid.NewString()
	ok2ID := "audit-ok2-" + uuid.NewString()
	revokeID := "audit-revoke-" + uuid.NewString()
	logoutID := "audit-logout-" + uuid.NewString()

	anon := doAudited(t, srv, http.MethodPost, "/v1/auth/login", "", anonID, map[string]string{
		"phone":    uniquePhone(),
		"password": wrong,
	})
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("unknown phone status=%d body=%s", anon.Code, anon.Body.String())
	}

	fail := doAudited(t, srv, http.MethodPost, "/v1/auth/login", "", failID, map[string]string{
		"phone":    phone,
		"password": wrong,
	})
	if fail.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d body=%s", fail.Code, fail.Body.String())
	}

	ok1 := doAudited(t, srv, http.MethodPost, "/v1/auth/login", "", ok1ID, map[string]string{
		"phone":    phone,
		"password": password,
	})
	if ok1.Code != http.StatusOK {
		t.Fatalf("login1 status=%d body=%s", ok1.Code, ok1.Body.String())
	}
	tok1 := decodeTokens(t, ok1)

	ok2 := doAudited(t, srv, http.MethodPost, "/v1/auth/login", "", ok2ID, map[string]string{
		"phone":    phone,
		"password": password,
	})
	if ok2.Code != http.StatusOK {
		t.Fatalf("login2 status=%d body=%s", ok2.Code, ok2.Body.String())
	}
	tok2 := decodeTokens(t, ok2)

	me := getJSON(t, srv, "/v1/me", tok2.AccessToken)
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}
	var profile struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}

	list := getJSON(t, srv, "/v1/me/sessions", tok2.AccessToken)
	if list.Code != http.StatusOK {
		t.Fatalf("sessions status=%d body=%s", list.Code, list.Body.String())
	}
	var sessions struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	var currentID, otherID string
	for _, s := range sessions.Sessions {
		if s.Current {
			currentID = s.ID
		} else {
			otherID = s.ID
		}
	}
	if len(sessions.Sessions) != 2 || currentID == "" || otherID == "" {
		t.Fatalf("sessions=%+v", sessions.Sessions)
	}

	rev := doAudited(t, srv, http.MethodDelete, "/v1/me/sessions/"+otherID, tok2.AccessToken, revokeID, nil)
	if rev.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", rev.Code, rev.Body.String())
	}

	out := doAudited(t, srv, http.MethodPost, "/v1/auth/logout", "", logoutID, map[string]string{
		"refresh_token": tok2.RefreshToken,
	})
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", out.Code, out.Body.String())
	}

	secrets := map[string]string{
		"password":        password,
		"wrong password":  wrong,
		"otp":             sms.LastCode,
		"access token 1":  tok1.AccessToken,
		"access token 2":  tok2.AccessToken,
		"refresh token 1": tok1.RefreshToken,
		"refresh token 2": tok2.RefreshToken,
	}

	rows := loadAuditRows(t, pool, []string{anonID, failID, ok1ID, ok2ID, revokeID, logoutID})
	if len(rows) != 6 {
		t.Fatalf("audit rows=%d", len(rows))
	}

	want := map[string]auditWant{
		anonID: {
			action: "login.failure", actor: "anonymous",
			targetType: "auth", summary: map[string]any{"result": "invalid_credentials"},
		},
		failID: {
			action: "login.failure", actor: "user", userID: profile.ID,
			targetType: "auth", summary: map[string]any{"result": "invalid_credentials"},
		},
		ok1ID: {
			action: "login.success", actor: "user", userID: profile.ID,
			targetType: "session", targetID: otherID,
			summary: map[string]any{"result": "ok"},
		},
		ok2ID: {
			action: "login.success", actor: "user", userID: profile.ID,
			targetType: "session", targetID: currentID,
			summary: map[string]any{"result": "ok"},
		},
		revokeID: {
			action: "session.revoke", actor: "user", userID: profile.ID,
			targetType: "session", targetID: otherID,
			summary: map[string]any{"via": "api"},
		},
		logoutID: {
			action: "session.logout", actor: "user", userID: profile.ID,
			targetType: "session", targetID: currentID,
			summary: map[string]any{"via": "logout"},
		},
	}

	for _, row := range rows {
		exp, ok := want[row.requestID]
		if !ok {
			t.Fatalf("unexpected request_id %s", row.requestID)
		}
		for _, col := range []struct{ name, value string }{
			{"summary", row.summary},
			{"user_agent", row.userAgent},
			{"ip", row.ip},
			{"request_id", row.requestID},
			{"action", row.action},
		} {
			for label, secret := range secrets {
				if secret != "" && strings.Contains(col.value, secret) {
					t.Fatalf("%s %s contains %s", row.requestID, col.name, label)
				}
			}
		}
		if row.action != exp.action || row.actor != exp.actor || row.targetType != exp.targetType {
			t.Fatalf("%s: got action=%s actor=%s target=%s", row.requestID, row.action, row.actor, row.targetType)
		}
		if exp.userID == "" {
			if row.userID != "" {
				t.Fatalf("%s: actor_user_id=%s, want null", row.requestID, row.userID)
			}
		} else if row.userID != exp.userID {
			t.Fatalf("%s: actor_user_id=%s, want %s", row.requestID, row.userID, exp.userID)
		}
		if exp.targetID == "" {
			if row.targetID != "" {
				t.Fatalf("%s: target_id=%s, want null", row.requestID, row.targetID)
			}
		} else if row.targetID != exp.targetID {
			t.Fatalf("%s: target_id=%s, want %s", row.requestID, row.targetID, exp.targetID)
		}
		assertSummary(t, row.requestID, row.summary, exp.summary)
		if row.userAgent != "twofold-audit-test" {
			t.Fatalf("%s user_agent=%q", row.requestID, row.userAgent)
		}
	}
}

type auditWant struct {
	action     string
	actor      string
	userID     string
	targetType string
	targetID   string
	summary    map[string]any
}

type auditRow struct {
	action     string
	actor      string
	userID     string
	targetType string
	targetID   string
	summary    string
	requestID  string
	userAgent  string
	ip         string
}

func loadAuditRows(t *testing.T, pool *pgxpool.Pool, ids []string) []auditRow {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT action,
		       actor_kind,
		       COALESCE(actor_user_id::text, ''),
		       COALESCE(target_type, ''),
		       COALESCE(target_id::text, ''),
		       summary::text,
		       request_id,
		       COALESCE(user_agent, ''),
		       COALESCE(ip, '')
		FROM audit_logs
		WHERE request_id = ANY($1::text[])
		ORDER BY created_at ASC
	`, ids)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []auditRow
	for rows.Next() {
		var row auditRow
		if err := rows.Scan(
			&row.action,
			&row.actor,
			&row.userID,
			&row.targetType,
			&row.targetID,
			&row.summary,
			&row.requestID,
			&row.userAgent,
			&row.ip,
		); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSummary(t *testing.T, requestID, raw string, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("%s summary=%s", requestID, raw)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s summary=%s", requestID, raw)
		}
	}
	for _, key := range []string{
		"password", "password_hash", "otp", "otp_code", "code",
		"refresh_token", "refresh_hash", "access_token", "token",
		"pin", "old_pin", "pin_hash", "secret",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("%s summary contains %s", requestID, key)
		}
	}
}

// TestPINSetChangeAuditTrail proves pin.set / pin.change land in audit_logs
// with before/after flags only — never password, PIN, or pin_hash.
func TestPINSetChangeAuditTrail(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}

	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)

	password := "correct-horse-1"
	phone := uniquePhone()
	registerAndVerify(t, srv, sms, phone, password)
	tok := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": password,
	}))

	me := getJSON(t, srv, "/v1/me", tok.AccessToken)
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}
	var profile struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}

	setID := "audit-pin-set-" + uuid.NewString()
	changeID := "audit-pin-change-" + uuid.NewString()
	oldPIN := "4829"
	newPIN := "7391"

	set := doAudited(t, srv, http.MethodPut, "/v1/me/pin", tok.AccessToken, setID, map[string]string{
		"password": password,
		"pin":      oldPIN,
	})
	if set.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", set.Code, set.Body.String())
	}

	change := doAudited(t, srv, http.MethodPut, "/v1/me/pin", tok.AccessToken, changeID, map[string]string{
		"old_pin": oldPIN,
		"pin":     newPIN,
	})
	if change.Code != http.StatusOK {
		t.Fatalf("change status=%d body=%s", change.Code, change.Body.String())
	}

	rows := loadAuditRows(t, pool, []string{setID, changeID})
	if len(rows) != 2 {
		t.Fatalf("audit rows=%d", len(rows))
	}

	want := map[string]auditWant{
		setID: {
			action: "pin.set", actor: "user", userID: profile.ID,
			targetType: "user", targetID: profile.ID,
			summary: map[string]any{"pin_set_before": false, "pin_set_after": true},
		},
		changeID: {
			action: "pin.change", actor: "user", userID: profile.ID,
			targetType: "user", targetID: profile.ID,
			summary: map[string]any{"pin_set_before": true, "pin_set_after": true},
		},
	}

	secrets := map[string]string{
		"password": password,
		"old pin":  oldPIN,
		"new pin":  newPIN,
	}

	for _, row := range rows {
		exp, ok := want[row.requestID]
		if !ok {
			t.Fatalf("unexpected request_id %s", row.requestID)
		}
		for _, col := range []struct{ name, value string }{
			{"summary", row.summary},
			{"user_agent", row.userAgent},
			{"ip", row.ip},
			{"request_id", row.requestID},
			{"action", row.action},
		} {
			for label, secret := range secrets {
				if secret != "" && strings.Contains(col.value, secret) {
					t.Fatalf("%s %s contains %s", row.requestID, col.name, label)
				}
			}
		}
		if row.action != exp.action || row.actor != exp.actor || row.targetType != exp.targetType {
			t.Fatalf("%s: got action=%s actor=%s target=%s", row.requestID, row.action, row.actor, row.targetType)
		}
		if row.userID != exp.userID {
			t.Fatalf("%s: actor_user_id=%s, want %s", row.requestID, row.userID, exp.userID)
		}
		if row.targetID != exp.targetID {
			t.Fatalf("%s: target_id=%s, want %s", row.requestID, row.targetID, exp.targetID)
		}
		assertSummary(t, row.requestID, row.summary, exp.summary)
	}
}

func doAudited(t *testing.T, srv *httpserver.Server, method, path, bearer, requestID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "twofold-audit-test")
	req.Header.Set("X-Request-ID", requestID)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}
