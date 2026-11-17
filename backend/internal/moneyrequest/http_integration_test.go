package moneyrequest_test

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/idempotency"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/moneyrequest"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/transfer"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const testPIN = "2468"
const amountMinor = int64(5_000) // under waive_below → fee 0

// TestMoneyRequestConcurrentPayOnce proves FR-REQ-4: two concurrent pays on the
// same pending request move money exactly once.
func TestMoneyRequestConcurrentPayOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	phoneA := uniquePhone()
	phoneB := uniquePhone()
	registerVerifyPIN(t, srv, sms, phoneA, "Abel Bekele")
	registerVerifyPIN(t, srv, sms, phoneB, "Selam Bekele")
	tokenA := login(t, srv, phoneA)
	tokenB := login(t, srv, phoneB)

	payerAccount, _ := walletIDs(t, ctx, pool, phoneB)
	fund(t, ctx, pool, payerAccount, 50_000)

	created := createRequest(t, srv, tokenA, phoneB, amountMinor, "lunch")
	if created.Status != "pending" {
		t.Fatalf("status=%q want pending", created.Status)
	}

	var okCount atomic.Int32
	var conflictCount atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := payRequest(t, srv, tokenB, created.ID, 0, uuid.NewString())
			switch rec.Code {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusConflict:
				conflictCount.Add(1)
			default:
				t.Errorf("pay status=%d body=%s", rec.Code, rec.Body.String())
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if okCount.Load() != 1 {
		t.Fatalf("successful pays=%d want 1", okCount.Load())
	}
	if conflictCount.Load() != 1 {
		t.Fatalf("conflicts=%d want 1", conflictCount.Load())
	}

	var status string
	var paidTxn *uuid.UUID
	var completedCount int
	err := pool.QueryRow(ctx, `
		SELECT status, paid_transaction_id,
		       (SELECT count(*) FROM transactions WHERE id = money_requests.paid_transaction_id AND status = 'completed')
		FROM money_requests WHERE id = $1
	`, created.ID).Scan(&status, &paidTxn, &completedCount)
	if err != nil {
		t.Fatal(err)
	}
	if status != "paid" || paidTxn == nil || completedCount != 1 {
		t.Fatalf("request status=%s paid_txn=%v completed=%d", status, paidTxn, completedCount)
	}

	var xferCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactions
		WHERE from_wallet_id = (SELECT id FROM wallets WHERE account_id = $1)
		  AND amount_minor = $2
		  AND status = 'completed'
	`, payerAccount, amountMinor).Scan(&xferCount); err != nil {
		t.Fatal(err)
	}
	if xferCount != 1 {
		t.Fatalf("completed transfers from payer=%d want 1", xferCount)
	}
}

func TestMoneyRequestDeclineAndCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)

	phoneA := uniquePhone()
	phoneB := uniquePhone()
	phoneC := uniquePhone()
	registerVerifyPIN(t, srv, sms, phoneA, "Abel Bekele")
	registerVerifyPIN(t, srv, sms, phoneB, "Selam Bekele")
	registerVerifyPIN(t, srv, sms, phoneC, "Dawit Bekele")
	tokenA := login(t, srv, phoneA)
	tokenB := login(t, srv, phoneB)
	tokenC := login(t, srv, phoneC)

	toDecline := createRequest(t, srv, tokenA, phoneB, amountMinor, "decline me")
	rec := postJSON(t, srv, "/v1/requests/"+toDecline.ID.String()+"/decline", tokenB, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("decline status=%d body=%s", rec.Code, rec.Body.String())
	}
	var declined moneyrequest.Request
	if err := json.Unmarshal(rec.Body.Bytes(), &declined); err != nil {
		t.Fatal(err)
	}
	if declined.Status != "declined" {
		t.Fatalf("status=%q want declined", declined.Status)
	}

	toCancel := createRequest(t, srv, tokenA, phoneB, amountMinor, "cancel me")
	rec = postJSON(t, srv, "/v1/requests/"+toCancel.ID.String()+"/cancel", tokenA, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Stranger cannot decline.
	other := createRequest(t, srv, tokenA, phoneB, amountMinor, "not yours")
	rec = postJSON(t, srv, "/v1/requests/"+other.ID.String()+"/decline", tokenC, map[string]any{})
	assertProblem(t, rec, http.StatusForbidden, "forbidden")

	incoming := listBox(t, srv, tokenB, "incoming")
	outgoing := listBox(t, srv, tokenA, "outgoing")
	if len(incoming.Items) == 0 || len(outgoing.Items) == 0 {
		t.Fatalf("expected list items incoming=%d outgoing=%d", len(incoming.Items), len(outgoing.Items))
	}
}

func createRequest(t *testing.T, srv *httpserver.Server, bearer, payer string, amount int64, note string) moneyrequest.Request {
	t.Helper()
	rec := postJSON(t, srv, "/v1/requests", bearer, map[string]any{
		"payer": payer, "amount_minor": amount, "currency": "ETB", "note": note,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out moneyrequest.Request
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func payRequest(t *testing.T, srv *httpserver.Server, bearer string, id uuid.UUID, fee int64, idem string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"pin": testPIN, "expected_fee_minor": fee,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/requests/"+id.String()+"/pay", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Idempotency-Key", idem)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

func listBox(t *testing.T, srv *httpserver.Server, bearer, box string) moneyrequest.ListResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/requests?box="+box, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out moneyrequest.ListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
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
	transferHandler := transfer.NewHandler(
		transfers, pins, authHandler.RequireAccess(),
		idempotency.Middleware(idemStore, auth.UserIDFromContext),
		auth.UserIDFromContext,
	)
	moneyHandler := moneyrequest.NewHandler(
		moneyrequest.NewService(pool, transfers),
		pins,
		authHandler.RequireAccess(),
		idempotency.Middleware(idemStore, auth.UserIDFromContext),
		auth.UserIDFromContext,
	)
	return httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, pool, log, httpserver.Options{
		Auth:          authHandler,
		Transfers:     transferHandler,
		MoneyRequests: moneyHandler,
	})
}

func registerVerifyPIN(t *testing.T, srv *httpserver.Server, sms *auth.RecordingSMSSender, phone, name string) {
	t.Helper()
	rec := postJSON(t, srv, "/v1/auth/register", "", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": name,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, srv, "/v1/auth/verify-phone", "", map[string]string{
		"phone": phone, "code": sms.LastCode,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", rec.Code, rec.Body.String())
	}
	tok := login(t, srv, phone)
	rec = putJSON(t, srv, "/v1/me/pin", tok, map[string]string{
		"password": "correct-horse-1", "pin": testPIN,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set pin status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func login(t *testing.T, srv *httpserver.Server, phone string) string {
	t.Helper()
	rec := postJSON(t, srv, "/v1/auth/login", "", map[string]string{
		"phone": phone, "password": "correct-horse-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rec.Code, rec.Body.String())
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil || tok.AccessToken == "" {
		t.Fatalf("token: %v body=%s", err, rec.Body.String())
	}
	return tok.AccessToken
}

func walletIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, phone string) (accountID, walletID uuid.UUID) {
	t.Helper()
	err := pool.QueryRow(ctx, `
		SELECT w.account_id, w.id
		FROM wallets w
		JOIN users u ON u.id = w.user_id
		WHERE u.phone_e164 = $1 AND w.currency = 'ETB'
	`, phone).Scan(&accountID, &walletID)
	if err != nil {
		t.Fatal(err)
	}
	return accountID, walletID
}

func fund(t *testing.T, ctx context.Context, pool *pgxpool.Pool, walletAccount uuid.UUID, amount int64) {
	t.Helper()
	clearing, err := ledgerdb.New(pool).GetAccountByCode(ctx, ledger.CodeProviderClearingTelebirr)
	if err != nil {
		t.Fatal(err)
	}
	money, err := ledger.NewMoney(amount, ledger.CurrencyETB)
	if err != nil {
		t.Fatal(err)
	}
	debit, err := ledger.NewLine(clearing.ID, ledger.Debit, money)
	if err != nil {
		t.Fatal(err)
	}
	credit, err := ledger.NewLine(walletAccount, ledger.Credit, money)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := ledger.NewJournal(ledger.KindTopUp, []ledger.Line{debit, credit})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d want %d body=%s", rec.Code, status, rec.Body.String())
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != code {
		t.Fatalf("code=%q want %q", p.Code, code)
	}
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
	path := filepath.Join(filepath.Dir(file), "..", "auth", "testdata", "jwt_ed25519.pem")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
