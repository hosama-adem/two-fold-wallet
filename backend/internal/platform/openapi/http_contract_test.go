package openapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/history"
	"github.com/hosama-adem/twofold/backend/internal/idempotency"
	"github.com/hosama-adem/twofold/backend/internal/kyc"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/moneyrequest"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/openapi"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/transfer"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

// TestHTTPResponsesMatchOpenAPI walks a Week-3 happy path and asserts each
// JSON response still matches docs/openapi.yaml (Done when for #50).
func TestHTTPResponsesMatchOpenAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	doc, err := openapi.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v, err := openapi.NewValidator(doc)
	if err != nil {
		t.Fatal(err)
	}

	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)

	check := func(method, path string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if err := v.CheckRecorder(ctx, method, path, rec); err != nil {
			t.Fatalf("%s %s status=%d: %v\nbody=%s", method, path, rec.Code, err, rec.Body.String())
		}
	}

	rec := get(t, srv, "/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("health=%d", rec.Code)
	}
	check(http.MethodGet, "/health", rec)

	phoneA := uniquePhone()
	phoneB := uniquePhone()

	rec = postJSON(t, srv, "/v1/auth/register", "", map[string]string{
		"phone": phoneA, "password": "correct-horse-1", "display_name": "Abel Bekele",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register A=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodPost, "/v1/auth/register", rec)

	rec = postJSON(t, srv, "/v1/auth/verify-phone", "", map[string]string{
		"phone": phoneA, "code": sms.LastCode,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify A=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodPost, "/v1/auth/verify-phone", rec)

	rec = postJSON(t, srv, "/v1/auth/register", "", map[string]string{
		"phone": phoneB, "password": "correct-horse-1", "display_name": "Selam Bekele",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register B=%d", rec.Code)
	}
	rec = postJSON(t, srv, "/v1/auth/verify-phone", "", map[string]string{
		"phone": phoneB, "code": sms.LastCode,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify B=%d", rec.Code)
	}

	rec = postJSON(t, srv, "/v1/auth/login", "", map[string]string{
		"phone": phoneA, "password": "correct-horse-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodPost, "/v1/auth/login", rec)
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tokens); err != nil || tokens.AccessToken == "" {
		t.Fatal(err)
	}
	token := tokens.AccessToken

	rec = get(t, srv, "/v1/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("me=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodGet, "/v1/me", rec)

	rec = putJSON(t, srv, "/v1/me/pin", token, map[string]string{
		"password": "correct-horse-1", "pin": "2468",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("pin=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodPut, "/v1/me/pin", rec)

	rec = get(t, srv, "/v1/wallets", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("wallets=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodGet, "/v1/wallets", rec)

	rec = get(t, srv, "/v1/recipients/lookup?q="+phoneB, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodGet, "/v1/recipients/lookup", rec)

	rec = postJSON(t, srv, "/v1/transfers/preview", token, map[string]any{
		"recipient": phoneB, "amount_minor": 1000, "currency": "ETB",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodPost, "/v1/transfers/preview", rec)

	rec = get(t, srv, "/v1/transactions?limit=5", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("history=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodGet, "/v1/transactions", rec)

	rec = postJSON(t, srv, "/v1/requests", token, map[string]any{
		"payer": phoneB, "amount_minor": 1500, "currency": "ETB", "note": "tea",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("request=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodPost, "/v1/requests", rec)

	rec = get(t, srv, "/v1/requests?box=outgoing", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("requests list=%d body=%s", rec.Code, rec.Body.String())
	}
	check(http.MethodGet, "/v1/requests", rec)
}

func newServer(t *testing.T, pool *pgxpool.Pool, sms *auth.RecordingSMSSender) *httpserver.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	users := user.NewRepository(pool)
	sessions := session.NewRepository(pool)
	audits := audit.NewRecorder(pool)
	tokens, err := auth.NewTokenSigner(loadPEM(t), "dev-1", 15*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	wallets := wallet.NewService(pool)
	pins := pin.New(pin.Deps{Users: users, Audit: audits, Match: auth.VerifyPIN, Log: log})
	svc := auth.NewService(auth.ServiceDeps{
		Users: users, Sessions: sessions, Tokens: tokens, Audit: audits, Pins: pins,
		Pool: pool, Wallets: wallets, Limits: limit.NewService(pool), SMS: sms, Log: log,
		RefreshTTL: 30 * 24 * time.Hour,
	})
	authHandler := auth.NewHandler(svc, ratelimit.NewMemory(ratelimit.Config{Burst: 1000, Window: time.Minute}))
	idemStore := idempotency.NewStore(pool)
	transfers := transfer.NewService(pool)
	return httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, pool, log, httpserver.Options{
		Auth:      authHandler,
		Wallets:   wallet.NewHandler(wallets, authHandler.RequireAccess(), auth.UserIDFromContext),
		Transfers: transfer.NewHandler(transfers, pins, authHandler.RequireAccess(), idempotency.Middleware(idemStore, auth.UserIDFromContext), auth.UserIDFromContext),
		KYC:       kyc.NewHandler(kyc.NewService(pool), authHandler.RequireAccess(), auth.UserIDFromContext),
		History:   history.NewHandler(history.NewService(pool), authHandler.RequireAccess(), auth.UserIDFromContext),
		MoneyRequests: moneyrequest.NewHandler(
			moneyrequest.NewService(pool, transfers), pins,
			authHandler.RequireAccess(),
			idempotency.Middleware(idemStore, auth.UserIDFromContext),
			auth.UserIDFromContext,
		),
	})
}

func postJSON(t *testing.T, srv *httpserver.Server, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

func putJSON(t *testing.T, srv *httpserver.Server, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

func get(t *testing.T, srv *httpserver.Server, path, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

func uniquePhone() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("+2519%08d", n.Int64())
}

func loadPEM(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "auth", "testdata", "jwt_ed25519.pem")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
