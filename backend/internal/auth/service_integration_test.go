package auth_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/user"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newAuthServer(t *testing.T, sms *auth.RecordingSMSSender, clk auth.Clock) *httpserver.Server {
	t.Helper()
	pool := testutil.Postgres(t)
	users := user.NewRepository(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := auth.NewService(users, sms, log)
	if clk != nil {
		svc.WithClock(clk)
	}
	h := auth.NewHandler(svc)
	return httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, pool, log, httpserver.Options{Auth: h})
}

func postJSON(t *testing.T, srv *httpserver.Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

func TestRegisterVerifyHappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)

	rec := postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone":        "+251911223344",
		"password":     "correct-horse-1",
		"display_name": "Selam",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sms.Calls != 1 || sms.LastCode == "" {
		t.Fatalf("expected OTP send, calls=%d", sms.Calls)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(sms.LastCode)) {
		t.Fatal("OTP leaked in register response")
	}

	rec = postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{
		"phone": "+251911223344",
		"code":  sms.LastCode,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRegisterDuplicatePhoneSameShape(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	body := map[string]string{
		"phone":        "+251911000111",
		"password":     "correct-horse-1",
		"display_name": "Dawit",
	}
	first := postJSON(t, newAuthServer(t, sms, fixedClock{t: now}), "/v1/auth/register", body)
	second := postJSON(t, newAuthServer(t, sms, fixedClock{t: now.Add(61 * time.Second)}), "/v1/auth/register", body)

	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("codes %d / %d bodies %s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	var a, b map[string]string
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(second.Body.Bytes(), &b)
	if a["message"] == "" || a["message"] != b["message"] {
		t.Fatalf("messages differ: %q vs %q", a["message"], b["message"])
	}
}

func TestVerifyWrongCode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := "+251922334455"
	postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": "Hana",
	})
	rec := postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{"phone": phone, "code": "000000"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong code status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestVerifyTooManyAttempts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := "+251922334466"
	postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": "Hana",
	})
	code := sms.LastCode
	for i := 0; i < auth.OTPMaxAttempts; i++ {
		postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{"phone": phone, "code": "111111"})
	}
	rec := postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{"phone": phone, "code": code})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("after max attempts status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestVerifyExpiredOTP(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	phone := "+251922334477"
	postJSON(t, newAuthServer(t, sms, fixedClock{t: now}), "/v1/auth/register", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": "Hana",
	})
	code := sms.LastCode
	rec := postJSON(t, newAuthServer(t, sms, fixedClock{t: now.Add(6 * time.Minute)}), "/v1/auth/verify-phone", map[string]string{
		"phone": phone, "code": code,
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expired status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResendCooldown(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	now := time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)
	phone := "+251933445566"
	postJSON(t, newAuthServer(t, sms, fixedClock{t: now}), "/v1/auth/register", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": "Yonas",
	})
	if sms.Calls != 1 {
		t.Fatalf("calls=%d", sms.Calls)
	}

	rec := postJSON(t, newAuthServer(t, sms, fixedClock{t: now.Add(10 * time.Second)}), "/v1/auth/resend-otp", map[string]string{"phone": phone})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("cooldown status=%d body=%s", rec.Code, rec.Body.String())
	}
	var p httpx.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Code != "otp_cooldown" {
		t.Fatalf("code=%q", p.Code)
	}

	rec = postJSON(t, newAuthServer(t, sms, fixedClock{t: now.Add(61 * time.Second)}), "/v1/auth/resend-otp", map[string]string{"phone": phone})
	if rec.Code != http.StatusOK {
		t.Fatalf("resend status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sms.Calls != 2 {
		t.Fatalf("expected second send, calls=%d", sms.Calls)
	}
}

func TestReuseConsumedOTPFails(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := "+251944556677"
	postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": "Reuse",
	})
	code := sms.LastCode
	rec := postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{"phone": phone, "code": code})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{"phone": phone, "code": code})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reuse status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestVerifiedAccountRegisterDoesNotSendOTP(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	now := time.Date(2026, 11, 5, 16, 0, 0, 0, time.UTC)
	phone := "+251955667788"
	srv := newAuthServer(t, sms, fixedClock{t: now})
	postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": "Done",
	})
	code := sms.LastCode
	postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{"phone": phone, "code": code})
	before := sms.Calls

	rec := postJSON(t, newAuthServer(t, sms, fixedClock{t: now.Add(2 * time.Minute)}), "/v1/auth/register", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": "Done",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d", rec.Code)
	}
	if sms.Calls != before {
		t.Fatalf("OTP sent to verified account: calls %d → %d", before, sms.Calls)
	}
}
