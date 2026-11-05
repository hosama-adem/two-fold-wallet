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
)

func registerAndVerify(t *testing.T, srv *httpserver.Server, sms *auth.RecordingSMSSender, phone, password string) {
	t.Helper()
	rec := postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone":        phone,
		"password":     password,
		"display_name": "Login User",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{
		"phone": phone,
		"code":  sms.LastCode,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func decodeTokens(t *testing.T, rec *httptest.ResponseRecorder) auth.TokenResponse {
	t.Helper()
	var tok auth.TokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" || tok.TokenType != "Bearer" || tok.ExpiresIn != 900 {
		t.Fatalf("unexpected tokens: %+v body=%s", tok, rec.Body.String())
	}
	return tok
}

func getJSON(t *testing.T, srv *httpserver.Server, path, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

func TestLoginHappyPathAndMe(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	password := "correct-horse-1"
	registerAndVerify(t, srv, sms, phone, password)

	rec := postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone":    phone,
		"password": password,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rec.Code, rec.Body.String())
	}
	tok := decodeTokens(t, rec)

	me := getJSON(t, srv, "/v1/me", tok.AccessToken)
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}
	var profile map[string]any
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile["phone"] != phone || profile["status"] != "active" {
		t.Fatalf("profile=%v", profile)
	}
	if _, ok := profile["password_hash"]; ok {
		t.Fatal("password_hash leaked")
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	registerAndVerify(t, srv, sms, phone, "correct-horse-1")

	cases := []struct {
		name string
		body map[string]string
	}{
		{"unknown phone", map[string]string{"phone": uniquePhone(), "password": "correct-horse-1"}},
		{"wrong password", map[string]string{"phone": phone, "password": "wrong-password"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, srv, "/v1/auth/login", tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var p httpx.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if p.Code != "invalid_credentials" {
				t.Fatalf("code=%q", p.Code)
			}
		})
	}
}

func TestLoginRejectsUnverified(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	rec := postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone":        phone,
		"password":     "correct-horse-1",
		"display_name": "Pending",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d", rec.Code)
	}
	rec = postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone":    phone,
		"password": "correct-horse-1",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var p httpx.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Code != "invalid_credentials" {
		t.Fatalf("code=%q", p.Code)
	}
}

func TestRefreshRotationAndReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	registerAndVerify(t, srv, sms, phone, "correct-horse-1")

	login := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": "correct-horse-1",
	}))

	rotated := decodeTokens(t, postJSON(t, srv, "/v1/auth/refresh", map[string]string{
		"refresh_token": login.RefreshToken,
	}))
	if rotated.RefreshToken == login.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	reuse := postJSON(t, srv, "/v1/auth/refresh", map[string]string{
		"refresh_token": login.RefreshToken,
	})
	if reuse.Code != http.StatusUnauthorized {
		t.Fatalf("reuse status=%d body=%s", reuse.Code, reuse.Body.String())
	}
	var p httpx.Problem
	_ = json.Unmarshal(reuse.Body.Bytes(), &p)
	if p.Code != "invalid_refresh_token" {
		t.Fatalf("code=%q", p.Code)
	}

	// Family revoked: the rotated token should also fail after reuse detection.
	again := postJSON(t, srv, "/v1/auth/refresh", map[string]string{
		"refresh_token": rotated.RefreshToken,
	})
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("family token status=%d body=%s", again.Code, again.Body.String())
	}
}

func TestLogoutRevokesRefresh(t *testing.T) {
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

	rec := postJSON(t, srv, "/v1/auth/logout", map[string]string{
		"refresh_token": tok.RefreshToken,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", rec.Code, rec.Body.String())
	}

	refresh := postJSON(t, srv, "/v1/auth/refresh", map[string]string{
		"refresh_token": tok.RefreshToken,
	})
	if refresh.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout status=%d", refresh.Code)
	}

	me := getJSON(t, srv, "/v1/me", tok.AccessToken)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout status=%d body=%s", me.Code, me.Body.String())
	}
}

func TestMeRejectsExpiredAndMissingToken(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, fixedClock{t: now})
	phone := uniquePhone()
	registerAndVerify(t, srv, sms, phone, "correct-horse-1")
	tok := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": "correct-horse-1",
	}))

	missing := getJSON(t, srv, "/v1/me", "")
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing status=%d", missing.Code)
	}

	// Advance clock past access TTL; rebuild server sharing DB with advanced clock.
	srv2 := newAuthServer(t, sms, fixedClock{t: now.Add(16 * time.Minute)})
	expired := getJSON(t, srv2, "/v1/me", tok.AccessToken)
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("expired status=%d body=%s", expired.Code, expired.Body.String())
	}
}
