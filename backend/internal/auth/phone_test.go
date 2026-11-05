package auth_test

import (
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/auth"
)

func TestNormalizePhone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
		ok       bool
	}{
		{in: "+251911223344", want: "+251911223344", ok: true},
		{in: "+251711223344", want: "+251711223344", ok: true},
		{in: "0911223344", want: "+251911223344", ok: true},
		{in: "0711223344", want: "+251711223344", ok: true},
		{in: "251911223344", want: "+251911223344", ok: true},
		{in: "+251811223344", ok: false},
		{in: "+1234567890", ok: false},
	}
	for _, tc := range cases {
		got, err := auth.NormalizePhone(tc.in)
		if tc.ok && err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s: expected error", tc.in)
		}
		if tc.ok && got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.in, got, tc.want)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()
	if err := auth.ValidatePassword("short"); err == nil {
		t.Fatal("expected short password error")
	}
	if err := auth.ValidatePassword("password123"); err == nil {
		t.Fatal("expected blocklist error")
	}
	if err := auth.ValidatePassword("correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := auth.VerifyPassword(hash, "correct-horse-battery")
	if err != nil || !ok {
		t.Fatalf("verify failed: ok=%v err=%v", ok, err)
	}
	ok, err = auth.VerifyPassword(hash, "wrong-password-here")
	if err != nil || ok {
		t.Fatalf("expected mismatch: ok=%v err=%v", ok, err)
	}
}

func TestOTPHashMatch(t *testing.T) {
	t.Parallel()
	code, err := auth.GenerateOTP()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatalf("len=%d", len(code))
	}
	hash := auth.HashOTP(code)
	if !auth.OTPMatches(code, hash) {
		t.Fatal("expected match")
	}
	if auth.OTPMatches("000000", hash) && code != "000000" {
		t.Fatal("unexpected match")
	}
}
