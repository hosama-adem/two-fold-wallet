package httpserver_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func TestHealth(t *testing.T) {
	t.Parallel()

	srv := httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected X-Request-ID on health")
	}
}

func TestReadyWithoutPool(t *testing.T) {
	t.Parallel()

	srv := httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	req.Header.Set("X-Request-ID", "ready-test")
	rec := httptest.NewRecorder()

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "database_unavailable" {
		t.Fatalf("code = %q", p.Code)
	}
	if p.RequestID != "ready-test" {
		t.Fatalf("request_id = %q", p.RequestID)
	}
}

func TestNotFoundProblem(t *testing.T) {
	t.Parallel()

	srv := httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != httpx.CodeNotFound {
		t.Fatalf("code = %q", p.Code)
	}
}
