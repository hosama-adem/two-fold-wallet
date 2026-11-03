package config_test

import (
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/config"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SIGNING_KEY", "customer-key")
	t.Setenv("STAFF_JWT_SIGNING_KEY", "staff-key")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is empty")
	}
}

func TestLoadOK(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://twofold:twofold@localhost:5432/twofold?sslmode=disable")
	t.Setenv("JWT_SIGNING_KEY", "customer-key")
	t.Setenv("STAFF_JWT_SIGNING_KEY", "staff-key")
	t.Setenv("HTTP_ADDR", ":9090")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
}
