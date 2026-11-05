package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/platform/config"
)

func testPEM(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "auth", "testdata", "jwt_ed25519.pem"))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SIGNING_KEY", testPEM(t))
	t.Setenv("STAFF_JWT_SIGNING_KEY", "staff-key")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is empty")
	}
}

func TestLoadOK(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://twofold:twofold@localhost:5432/twofold?sslmode=disable")
	t.Setenv("JWT_SIGNING_KEY", strings.ReplaceAll(testPEM(t), "\n", `\n`))
	t.Setenv("STAFF_JWT_SIGNING_KEY", "staff-key")
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("JWT_KID", "test-kid")
	t.Setenv("ACCESS_TOKEN_TTL", "10m")
	t.Setenv("REFRESH_TOKEN_TTL", "48h")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.JWTKid != "test-kid" {
		t.Fatalf("JWTKid = %q", cfg.JWTKid)
	}
	if cfg.AccessTokenTTL != 10*time.Minute {
		t.Fatalf("AccessTokenTTL = %v", cfg.AccessTokenTTL)
	}
	if cfg.RefreshTokenTTL != 48*time.Hour {
		t.Fatalf("RefreshTokenTTL = %v", cfg.RefreshTokenTTL)
	}
	if !strings.Contains(cfg.JWTSigningKey, "BEGIN PRIVATE KEY") {
		t.Fatalf("JWTSigningKey not normalized PEM: %q", cfg.JWTSigningKey[:40])
	}
}
