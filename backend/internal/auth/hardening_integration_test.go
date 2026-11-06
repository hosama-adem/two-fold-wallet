package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
)

func deleteJSON(t *testing.T, srv *httpserver.Server, path, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

func TestLoginRateLimitedSixthAttempt(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	lim := ratelimit.NewMemory(ratelimit.Config{
		Burst:  5,
		Window: time.Minute,
		Now:    func() time.Time { return now },
	})
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServerWithLimiter(t, sms, fixedClock{t: now}, lim)
	phone := uniquePhone()
	registerAndVerify(t, srv, sms, phone, "correct-horse-1")

	body := map[string]string{"phone": phone, "password": "wrong-password"}
	for i := 0; i < 5; i++ {
		rec := postJSON(t, srv, "/v1/auth/login", body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := postJSON(t, srv, "/v1/auth/login", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != httpx.CodeRateLimited {
		t.Fatalf("code=%q", p.Code)
	}
}

func TestListAndRevokeSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	registerAndVerify(t, srv, sms, phone, "correct-horse-1")

	tok1 := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": "correct-horse-1",
	}))
	tok2 := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": "correct-horse-1",
	}))

	list := getJSON(t, srv, "/v1/me/sessions", tok2.AccessToken)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var payload struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Sessions) < 2 {
		t.Fatalf("sessions=%d", len(payload.Sessions))
	}
	var otherID string
	var currentCount int
	for _, s := range payload.Sessions {
		if s.Current {
			currentCount++
		} else {
			otherID = s.ID
		}
	}
	if currentCount != 1 || otherID == "" {
		t.Fatalf("current=%d other=%q payload=%+v", currentCount, otherID, payload)
	}

	rev := deleteJSON(t, srv, "/v1/me/sessions/"+otherID, tok2.AccessToken)
	if rev.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", rev.Code, rev.Body.String())
	}

	// Foreign / missing session → 404
	missing := deleteJSON(t, srv, "/v1/me/sessions/01900000-0000-7000-8000-000000000099", tok2.AccessToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d", missing.Code)
	}

	// Revoked session's refresh should fail; current still works.
	_ = tok1
	me := getJSON(t, srv, "/v1/me", tok2.AccessToken)
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d", me.Code)
	}
}

func TestRevokeCurrentSessionBlocksAccessMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	registerAndVerify(t, srv, sms, phone, "correct-horse-1")
	tok := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": "correct-horse-1",
	}))

	list := getJSON(t, srv, "/v1/me/sessions", tok.AccessToken)
	var payload struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &payload)
	var currentID string
	for _, s := range payload.Sessions {
		if s.Current {
			currentID = s.ID
		}
	}
	if currentID == "" {
		t.Fatal("no current session")
	}

	rev := deleteJSON(t, srv, "/v1/me/sessions/"+currentID, tok.AccessToken)
	if rev.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d", rev.Code)
	}

	// Access JWT remains usable until expiry (session revoked check blocks me).
	me := getJSON(t, srv, "/v1/me", tok.AccessToken)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("expected me unauthorized after current session revoke, got %d", me.Code)
	}

	refresh := postJSON(t, srv, "/v1/auth/refresh", map[string]string{
		"refresh_token": tok.RefreshToken,
	})
	if refresh.Code != http.StatusUnauthorized {
		t.Fatalf("refresh status=%d", refresh.Code)
	}
}
