package auth_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/auth"
)

func loadFixturePEM(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "jwt_ed25519.pem")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAccessTokenRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	signer, err := auth.NewTokenSigner(loadFixturePEM(t), "dev-1", 15*time.Minute, fixedClock{t: now})
	if err != nil {
		t.Fatal(err)
	}
	uid := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	sid := uuid.MustParse("01900000-0000-7000-8000-000000000002")
	tok, exp, err := signer.IssueAccessToken(uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("exp = %v", exp)
	}
	claims, err := signer.ParseAccessToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != uid.String() || claims.SessionID != sid {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestAccessTokenExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	signer, err := auth.NewTokenSigner(loadFixturePEM(t), "dev-1", time.Minute, fixedClock{t: now})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := signer.IssueAccessToken(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	signer.WithClock(fixedClock{t: now.Add(2 * time.Minute)})
	if _, err := signer.ParseAccessToken(tok); err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestAccessTokenTampered(t *testing.T) {
	t.Parallel()
	signer, err := auth.NewTokenSigner(loadFixturePEM(t), "dev-1", 15*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := signer.IssueAccessToken(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	tampered := tok[:len(tok)-4] + "xxxx"
	if _, err := signer.ParseAccessToken(tampered); err == nil {
		t.Fatal("expected signature error")
	}
}
