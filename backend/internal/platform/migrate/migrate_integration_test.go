package migrate_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/migrate"
)

func TestUpDownUp(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}

	dir := migrationsDir(t)
	if err := migrate.Up(dsn, dir); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := migrate.Down(dsn, dir); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := migrate.Up(dsn, dir); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations"))
}
