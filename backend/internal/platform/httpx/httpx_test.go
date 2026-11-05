package httpx_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func testEngine(t *testing.T) *gin.Engine {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := gin.New()
	for _, mw := range httpx.Standard(httpx.WithLogger(log), httpx.WithBodyLimit(1<<20)) {
		r.Use(mw)
	}

	r.GET("/boom", func(c *gin.Context) {
		panic("kaboom")
	})
	r.GET("/err/:kind", func(c *gin.Context) {
		switch c.Param("kind") {
		case "bad":
			httpx.WriteError(c, httpx.BadRequest("Missing phone number."))
		case "missing":
			httpx.WriteError(c, httpx.NotFound("User not found."))
		case "conflict":
			httpx.WriteError(c, httpx.Conflict("Phone number already registered."))
		case "validation":
			httpx.WriteError(c, httpx.ValidationFailed("Amount must be positive."))
		case "internal":
			httpx.WriteError(c, errors.New(`pq: relation "users" does not exist`))
		default:
			httpx.WriteError(c, httpx.NotFound("Unknown kind."))
		}
	})
	r.POST("/echo", func(c *gin.Context) {
		var body map[string]any
		if err := httpx.BindJSON(c, &body); err != nil {
			httpx.WriteError(c, err)
			return
		}
		c.JSON(http.StatusOK, body)
	})
	r.NoRoute(func(c *gin.Context) {
		httpx.WriteError(c, httpx.NotFound("The requested path was not found."))
	})
	return r
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v body=%s", err, rec.Body.String())
	}
	return p
}

func TestProblemShapes(t *testing.T) {
	t.Parallel()
	r := testEngine(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{name: "400", method: http.MethodGet, path: "/err/bad", status: 400, code: httpx.CodeBadRequest},
		{name: "404", method: http.MethodGet, path: "/err/missing", status: 404, code: httpx.CodeNotFound},
		{name: "409", method: http.MethodGet, path: "/err/conflict", status: 409, code: httpx.CodeConflict},
		{name: "422", method: http.MethodGet, path: "/err/validation", status: 422, code: httpx.CodeValidationFailed},
		{name: "500", method: http.MethodGet, path: "/err/internal", status: 500, code: httpx.CodeInternal},
		{name: "404-noroute", method: http.MethodGet, path: "/nope", status: 404, code: httpx.CodeNotFound},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("X-Request-ID", "test-req-"+tc.name)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.status, rec.Body.String())
			}
			p := decodeProblem(t, rec)
			if p.Code != tc.code {
				t.Fatalf("code = %q, want %q", p.Code, tc.code)
			}
			if p.Status != tc.status {
				t.Fatalf("problem.status = %d, want %d", p.Status, tc.status)
			}
			if p.RequestID != "test-req-"+tc.name {
				t.Fatalf("request_id = %q", p.RequestID)
			}
			if p.Type == "" || p.Title == "" {
				t.Fatalf("missing type/title: %+v", p)
			}
			if tc.status >= 500 && strings.Contains(strings.ToLower(p.Detail), "pq:") {
				t.Fatalf("leaked internal detail: %q", p.Detail)
			}
			if rec.Header().Get("X-Request-ID") != "test-req-"+tc.name {
				t.Fatalf("missing echoed X-Request-ID")
			}
		})
	}
}

func TestPayloadTooLarge413(t *testing.T) {
	t.Parallel()
	r := testEngine(t)

	// Valid JSON larger than 1 MiB so BindJSON actually hits the body limit.
	var body bytes.Buffer
	body.WriteString(`{"data":"`)
	body.Write(bytes.Repeat([]byte("a"), (1<<20)+64))
	body.WriteString(`"}`)
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "test-req-413")
	req.ContentLength = int64(body.Len())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 body=%s", rec.Code, rec.Body.String())
	}
	p := decodeProblem(t, rec)
	if p.Code != httpx.CodePayloadTooLarge {
		t.Fatalf("code = %q", p.Code)
	}
	if p.RequestID != "test-req-413" {
		t.Fatalf("request_id = %q", p.RequestID)
	}
}

func TestUnknownJSONField400(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	typed := gin.New()
	for _, mw := range httpx.Standard(httpx.WithLogger(log)) {
		typed.Use(mw)
	}
	type payload struct {
		OK bool `json:"ok"`
	}
	typed.POST("/echo", func(c *gin.Context) {
		var body payload
		if err := httpx.BindJSON(c, &body); err != nil {
			httpx.WriteError(c, err)
			return
		}
		c.JSON(http.StatusOK, body)
	})

	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"ok":true,"nope":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "test-req-unknown")
	rec := httptest.NewRecorder()
	typed.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	p := decodeProblem(t, rec)
	if p.Code != httpx.CodeBadRequest {
		t.Fatalf("code = %q", p.Code)
	}
}

func TestPanicReturnsProblemJSONNoStack(t *testing.T) {
	t.Parallel()
	r := testEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	req.Header.Set("X-Request-ID", "test-req-panic")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 body=%s", rec.Code, rec.Body.String())
	}
	p := decodeProblem(t, rec)
	if p.Code != httpx.CodeInternal {
		t.Fatalf("code = %q", p.Code)
	}
	if p.RequestID != "test-req-panic" {
		t.Fatalf("request_id = %q", p.RequestID)
	}
	body := rec.Body.String()
	if strings.Contains(body, "goroutine") || strings.Contains(body, "runtime/") || strings.Contains(body, "kaboom") {
		t.Fatalf("stack/panic leaked to client: %s", body)
	}
}

func TestGeneratesRequestIDWhenMissing(t *testing.T) {
	t.Parallel()
	r := testEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/err/missing", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	id := rec.Header().Get("X-Request-ID")
	if id == "" {
		t.Fatal("expected generated X-Request-ID")
	}
	p := decodeProblem(t, rec)
	if p.RequestID != id {
		t.Fatalf("body request_id=%q header=%q", p.RequestID, id)
	}
}
