package auth_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func TestSetAndChangePIN(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	password := "correct-horse-1"
	registerAndVerify(t, srv, sms, phone, password)
	tok := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": password,
	}))

	set := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
		"password": password,
		"pin":      "4829",
	})
	if set.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", set.Code, set.Body.String())
	}
	var setMsg auth.MessageResponse
	if err := json.Unmarshal(set.Body.Bytes(), &setMsg); err != nil {
		t.Fatal(err)
	}
	if setMsg.Message != "PIN set." {
		t.Fatalf("message=%q", setMsg.Message)
	}
	if bytesContainAny(set.Body.Bytes(), "pin_hash", "4829", "password_hash") {
		t.Fatalf("sensitive material leaked: %s", set.Body.String())
	}

	// Set again without old_pin must fail once PIN exists.
	again := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
		"password": password,
		"pin":      "7391",
	})
	if again.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-set with password status=%d body=%s", again.Code, again.Body.String())
	}

	change := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
		"old_pin": "4829",
		"pin":     "7391",
	})
	if change.Code != http.StatusOK {
		t.Fatalf("change status=%d body=%s", change.Code, change.Body.String())
	}
	var changeMsg auth.MessageResponse
	_ = json.Unmarshal(change.Body.Bytes(), &changeMsg)
	if changeMsg.Message != "PIN changed." {
		t.Fatalf("message=%q", changeMsg.Message)
	}

	wrongOld := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
		"old_pin": "4829",
		"pin":     "8462",
	})
	if wrongOld.Code != http.StatusUnauthorized {
		t.Fatalf("wrong old pin status=%d body=%s", wrongOld.Code, wrongOld.Body.String())
	}
	var p httpx.Problem
	_ = json.Unmarshal(wrongOld.Body.Bytes(), &p)
	if p.Code != "invalid_pin" {
		t.Fatalf("code=%q", p.Code)
	}
	if p.TriesRemaining == nil || *p.TriesRemaining != 4 {
		t.Fatalf("tries_remaining=%v want 4", p.TriesRemaining)
	}
}

func TestChangePINLockoutAfterFiveFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	password := "correct-horse-1"
	registerAndVerify(t, srv, sms, phone, password)
	tok := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": password,
	}))
	set := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
		"password": password, "pin": "4829",
	})
	if set.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", set.Code, set.Body.String())
	}

	for i := 1; i <= 4; i++ {
		rec := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
			"old_pin": "0001", "pin": "7391",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("fail %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
		var p httpx.Problem
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		if p.Code != "invalid_pin" {
			t.Fatalf("fail %d code=%q", i, p.Code)
		}
		want := 5 - i
		if p.TriesRemaining == nil || *p.TriesRemaining != want {
			t.Fatalf("fail %d tries=%v want %d", i, p.TriesRemaining, want)
		}
	}

	fifth := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
		"old_pin": "0001", "pin": "7391",
	})
	if fifth.Code != http.StatusForbidden {
		t.Fatalf("5th status=%d body=%s", fifth.Code, fifth.Body.String())
	}
	var locked httpx.Problem
	_ = json.Unmarshal(fifth.Body.Bytes(), &locked)
	if locked.Code != "pin_locked" {
		t.Fatalf("code=%q", locked.Code)
	}
	if locked.LockedUntil == nil {
		t.Fatal("missing locked_until")
	}

	// While locked: another attempt must not change tries / must stay pin_locked.
	again := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
		"old_pin": "4829", "pin": "7391",
	})
	if again.Code != http.StatusForbidden {
		t.Fatalf("locked retry status=%d body=%s", again.Code, again.Body.String())
	}
	var againP httpx.Problem
	_ = json.Unmarshal(again.Body.Bytes(), &againP)
	if againP.Code != "pin_locked" {
		t.Fatalf("locked retry code=%q", againP.Code)
	}
	if againP.TriesRemaining != nil {
		t.Fatalf("locked retry must not advertise tries: %v", againP.TriesRemaining)
	}
}

func TestSetPINRequiresAuthAndPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	password := "correct-horse-1"
	registerAndVerify(t, srv, sms, phone, password)
	tok := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": password,
	}))

	unauth := putJSON(t, srv, "/v1/me/pin", "", map[string]string{
		"password": password, "pin": "4829",
	})
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", unauth.Code)
	}

	wrongPass := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
		"password": "wrong-password", "pin": "4829",
	})
	if wrongPass.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d body=%s", wrongPass.Code, wrongPass.Body.String())
	}
	var p httpx.Problem
	_ = json.Unmarshal(wrongPass.Body.Bytes(), &p)
	if p.Code != "invalid_password" {
		t.Fatalf("code=%q", p.Code)
	}
}

func TestWeakPINRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	sms := &auth.RecordingSMSSender{}
	srv := newAuthServer(t, sms, nil)
	phone := uniquePhone()
	password := "correct-horse-1"
	registerAndVerify(t, srv, sms, phone, password)
	tok := decodeTokens(t, postJSON(t, srv, "/v1/auth/login", map[string]string{
		"phone": phone, "password": password,
	}))

	for _, pin := range []string{"0000", "1234", "1111", "2345", "5432"} {
		rec := putJSON(t, srv, "/v1/me/pin", tok.AccessToken, map[string]string{
			"password": password, "pin": pin,
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("weak %q status=%d body=%s", pin, rec.Code, rec.Body.String())
		}
		var p httpx.Problem
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		if p.Code != "validation_failed" {
			t.Fatalf("weak %q code=%q", pin, p.Code)
		}
	}
}

func bytesContainAny(b []byte, needles ...string) bool {
	s := string(b)
	for _, n := range needles {
		if n != "" && strings.Contains(s, n) {
			return true
		}
	}
	return false
}
