package httpserver_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
)

func TestHealth(t *testing.T) {
	t.Parallel()

	srv := httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyWithoutPool(t *testing.T) {
	t.Parallel()

	srv := httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
