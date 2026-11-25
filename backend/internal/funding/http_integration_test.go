package funding_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/funding"
	"github.com/hosama-adem/twofold/backend/internal/idempotency"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/provider"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const hmacSecret = "test-topup-hmac-secret"

func TestTopUpDuplicateWebhookCreditsOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	rail := newFakeRail(hmacSecret)
	sms := &auth.RecordingSMSSender{}
	srv, _ := newFundingServer(t, pool, sms, rail)
	ctx := context.Background()

	phone := uniquePhone()
	registerVerify(t, srv, sms, phone, "Abel Bekele")
	token := login(t, srv, phone)
	_, walletID := walletIDs(t, ctx, pool, phone)
	before := balanceOf(t, ctx, pool, walletID)

	created := createTopUp(t, srv, token, "telebirr", 10_000)
	if created.TopUp.Status != "pending" {
		t.Fatalf("status=%q", created.TopUp.Status)
	}

	evt := webhookEvent(created, provider.StatusSucceeded)
	body, _ := json.Marshal(evt)
	rec1 := postWebhook(t, srv, "telebirr", body, time.Now())
	if rec1.Code != http.StatusOK {
		t.Fatalf("webhook1=%d body=%s", rec1.Code, rec1.Body.String())
	}
	rec2 := postWebhook(t, srv, "telebirr", body, time.Now())
	if rec2.Code != http.StatusOK {
		t.Fatalf("webhook2=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var ack struct {
		Duplicate bool `json:"duplicate"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &ack)
	if !ack.Duplicate {
		t.Fatalf("second webhook should be duplicate: %s", rec2.Body.String())
	}

	got := getTopUp(t, srv, token, created.TopUp.ID)
	if got.Status != "completed" {
		t.Fatalf("status=%q want completed", got.Status)
	}
	after := balanceOf(t, ctx, pool, walletID)
	if after-before != 10_000 {
		t.Fatalf("credited=%d want 10000 (before=%d after=%d)", after-before, before, after)
	}

	var journals int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM journal_entries
		WHERE transaction_id = $1 AND kind = 'top_up'
	`, created.TopUp.ID).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 1 {
		t.Fatalf("top_up journals=%d want 1", journals)
	}
}

func TestTopUpOutOfOrderFailureThenSuccessGoesToSuspense(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	rail := newFakeRail(hmacSecret)
	sms := &auth.RecordingSMSSender{}
	srv, _ := newFundingServer(t, pool, sms, rail)
	ctx := context.Background()

	phone := uniquePhone()
	registerVerify(t, srv, sms, phone, "Selam Bekele")
	token := login(t, srv, phone)
	_, walletID := walletIDs(t, ctx, pool, phone)
	before := balanceOf(t, ctx, pool, walletID)

	created := createTopUp(t, srv, token, "telebirr", 7_500)

	failEvt := webhookEvent(created, provider.StatusFailed)
	failEvt.EventID = "evt_fail_" + uuid.NewString()
	bodyFail, _ := json.Marshal(failEvt)
	if rec := postWebhook(t, srv, "telebirr", bodyFail, time.Now()); rec.Code != http.StatusOK {
		t.Fatalf("fail webhook=%d %s", rec.Code, rec.Body.String())
	}
	mid := getTopUp(t, srv, token, created.TopUp.ID)
	if mid.Status != "failed" {
		t.Fatalf("after fail status=%q", mid.Status)
	}

	okEvt := webhookEvent(created, provider.StatusSucceeded)
	okEvt.EventID = "evt_late_" + uuid.NewString()
	bodyOK, _ := json.Marshal(okEvt)
	if rec := postWebhook(t, srv, "telebirr", bodyOK, time.Now()); rec.Code != http.StatusOK {
		t.Fatalf("late success webhook=%d %s", rec.Code, rec.Body.String())
	}

	got := getTopUp(t, srv, token, created.TopUp.ID)
	if got.Status != "needs_review" {
		t.Fatalf("status=%q want needs_review", got.Status)
	}
	after := balanceOf(t, ctx, pool, walletID)
	if after != before {
		t.Fatalf("wallet must not be credited on late success: before=%d after=%d", before, after)
	}

	suspense, err := ledgerdb.New(pool).GetAccountByCode(ctx, ledger.CodeSuspense)
	if err != nil {
		t.Fatal(err)
	}
	var suspensePostings int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM postings p
		JOIN journal_entries j ON j.id = p.journal_id
		WHERE j.transaction_id = $1 AND p.account_id = $2 AND p.direction = 'credit'
	`, created.TopUp.ID, suspense.ID).Scan(&suspensePostings); err != nil {
		t.Fatal(err)
	}
	if suspensePostings != 1 {
		t.Fatalf("suspense credits=%d want 1", suspensePostings)
	}

	var alerts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs WHERE action = 'topup.late_success' AND target_id = $1
	`, created.TopUp.ID).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("late_success alerts=%d want 1", alerts)
	}
}

func TestTopUpSuccessThenFailureStaysCompleted(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	rail := newFakeRail(hmacSecret)
	sms := &auth.RecordingSMSSender{}
	srv, _ := newFundingServer(t, pool, sms, rail)
	ctx := context.Background()

	phone := uniquePhone()
	registerVerify(t, srv, sms, phone, "Dawit Bekele")
	token := login(t, srv, phone)
	_, walletID := walletIDs(t, ctx, pool, phone)
	before := balanceOf(t, ctx, pool, walletID)

	created := createTopUp(t, srv, token, "chapa", 5_000)
	okEvt := webhookEvent(created, provider.StatusSucceeded)
	okEvt.EventID = "evt_ok_" + uuid.NewString()
	bodyOK, _ := json.Marshal(okEvt)
	if rec := postWebhook(t, srv, "chapa", bodyOK, time.Now()); rec.Code != http.StatusOK {
		t.Fatalf("success webhook=%d %s", rec.Code, rec.Body.String())
	}

	failEvt := webhookEvent(created, provider.StatusFailed)
	failEvt.EventID = "evt_late_fail_" + uuid.NewString()
	bodyFail, _ := json.Marshal(failEvt)
	if rec := postWebhook(t, srv, "chapa", bodyFail, time.Now()); rec.Code != http.StatusOK {
		t.Fatalf("late fail webhook=%d %s", rec.Code, rec.Body.String())
	}

	got := getTopUp(t, srv, token, created.TopUp.ID)
	if got.Status != "completed" {
		t.Fatalf("status=%q want completed", got.Status)
	}
	after := balanceOf(t, ctx, pool, walletID)
	if after-before != 5_000 {
		t.Fatalf("balance delta=%d want 5000", after-before)
	}
}

func TestTopUpRejectsBadSignature(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	rail := newFakeRail(hmacSecret)
	sms := &auth.RecordingSMSSender{}
	srv, _ := newFundingServer(t, pool, sms, rail)

	phone := uniquePhone()
	registerVerify(t, srv, sms, phone, "Helen Bekele")
	token := login(t, srv, phone)
	created := createTopUp(t, srv, token, "telebirr", 3_000)

	evt := webhookEvent(created, provider.StatusSucceeded)
	body, _ := json.Marshal(evt)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/providers/telebirr", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(provider.HeaderSignature, "t=1,v1=deadbeef")
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}

	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM webhook_events WHERE event_id = $1
	`, evt.EventID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("forged event %s was stored (count=%d)", evt.EventID, n)
	}
}

func newFundingServer(t *testing.T, pool *pgxpool.Pool, sms auth.SMSSender, rail provider.Provider) (*httpserver.Server, *funding.Service) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	users := user.NewRepository(pool)
	sessions := session.NewRepository(pool)
	audits := audit.NewRecorder(pool)
	wallets := wallet.NewService(pool)
	pins := pin.New(pin.Deps{Users: users, Audit: audits, Match: auth.VerifyPIN, Log: log})
	limits := limit.NewService(pool)
	tokens, err := auth.NewTokenSigner(loadPEM(t), "test", 15*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(auth.ServiceDeps{
		Users: users, Sessions: sessions, Tokens: tokens, Audit: audits, Pins: pins,
		Pool: pool, Wallets: wallets, Limits: limits, SMS: sms, Log: log, RefreshTTL: time.Hour,
	})
	authHandler := auth.NewHandler(authSvc, nil)
	idemStore := idempotency.NewStore(pool)
	box, err := funding.NewSecretBox("twofold-dev-payout-key-32bytes!!")
	if err != nil {
		t.Fatal(err)
	}
	svc := funding.NewService(pool, rail, audits, box, log)
	fundingHandler := funding.NewHandler(
		svc,
		pins,
		authHandler.RequireAccess(),
		idempotency.Middleware(idemStore, auth.UserIDFromContext),
		auth.UserIDFromContext,
	)
	srv := httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, pool, log, httpserver.Options{
		Auth:    authHandler,
		Funding: fundingHandler,
	})
	return srv, svc
}

type createTopUpResponse struct {
	TopUp struct {
		ID          uuid.UUID `json:"id"`
		Status      string    `json:"status"`
		AmountMinor int64     `json:"amount_minor"`
		Currency    string    `json:"currency"`
		Reference   string    `json:"reference"`
		Provider    string    `json:"provider"`
		ProviderRef string    `json:"provider_ref"`
	} `json:"top_up"`
	Checkout struct {
		ProviderRef string `json:"provider_ref"`
	} `json:"checkout"`
}

func createTopUp(t *testing.T, srv *httpserver.Server, token, providerName string, amount int64) createTopUpResponse {
	t.Helper()
	body := map[string]any{
		"provider":     providerName,
		"amount_minor": amount,
		"currency":     "ETB",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/topups", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create topup status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out createTopUpResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func getTopUp(t *testing.T, srv *httpserver.Server, token string, id uuid.UUID) funding.TopUp {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/topups/"+id.String(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get topup status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out funding.TopUp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func webhookEvent(created createTopUpResponse, status provider.Status) provider.WebhookEvent {
	return provider.WebhookEvent{
		EventID:     "evt_" + uuid.NewString(),
		Provider:    created.TopUp.Provider,
		ProviderRef: created.TopUp.ProviderRef,
		Kind:        "collection",
		Status:      status,
		AmountMinor: created.TopUp.AmountMinor,
		Currency:    created.TopUp.Currency,
		Reference:   created.TopUp.Reference,
		OccurredAt:  time.Now().UTC(),
	}
}

func postWebhook(t *testing.T, srv *httpserver.Server, providerName string, body []byte, now time.Time) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/providers/"+providerName, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(provider.HeaderSignature, provider.SignWebhook(hmacSecret, now, body))
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

func balanceOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, walletID uuid.UUID) int64 {
	t.Helper()
	var bal int64
	err := pool.QueryRow(ctx, `
		SELECT a.balance_minor
		FROM wallets w JOIN accounts a ON a.id = w.account_id
		WHERE w.id = $1
	`, walletID).Scan(&bal)
	if err != nil {
		t.Fatal(err)
	}
	return bal
}

func registerVerify(t *testing.T, srv *httpserver.Server, sms *auth.RecordingSMSSender, phone, name string) {
	t.Helper()
	rec := postJSON(t, srv, "/v1/auth/register", "", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": name,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register=%d %s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, srv, "/v1/auth/verify-phone", "", map[string]string{
		"phone": phone, "code": sms.LastCode,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify=%d %s", rec.Code, rec.Body.String())
	}
}

func login(t *testing.T, srv *httpserver.Server, phone string) string {
	t.Helper()
	rec := postJSON(t, srv, "/v1/auth/login", "", map[string]string{
		"phone": phone, "password": "correct-horse-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login=%d %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil || tok.AccessToken == "" {
		t.Fatalf("token parse: %v", err)
	}
	return tok.AccessToken
}

func walletIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, phone string) (accountID, walletID uuid.UUID) {
	t.Helper()
	err := pool.QueryRow(ctx, `
		SELECT w.account_id, w.id
		FROM wallets w JOIN users u ON u.id = w.user_id
		WHERE u.phone_e164 = $1 AND w.currency = 'ETB'
	`, phone).Scan(&accountID, &walletID)
	if err != nil {
		t.Fatal(err)
	}
	return accountID, walletID
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

const testPIN = "2468"

func TestWithdrawalFailedPayoutRestoresAvailableBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	rail := newFakeRail(hmacSecret)
	sms := &auth.RecordingSMSSender{}
	srv, _ := newFundingServer(t, pool, sms, rail)
	ctx := context.Background()

	phone := uniquePhone()
	registerVerifyPIN(t, srv, sms, phone, "Abel Cashout")
	if _, err := pool.Exec(ctx, `UPDATE users SET kyc_tier = 1 WHERE phone_e164 = $1`, phone); err != nil {
		t.Fatal(err)
	}
	token := login(t, srv, phone)
	accountID, walletID := walletIDs(t, ctx, pool, phone)
	fundWallet(t, ctx, pool, accountID, 50_000)

	before := availableOf(t, ctx, pool, walletID)
	dest := createDestination(t, srv, token, phone)

	wd := createWithdrawal(t, srv, token, dest.ID, 10_000, 0)
	if wd.Status != "processing" {
		t.Fatalf("status=%q want processing", wd.Status)
	}
	mid := availableOf(t, ctx, pool, walletID)
	if mid >= before {
		t.Fatalf("available should drop after reserve: before=%d mid=%d", before, mid)
	}

	evt := provider.WebhookEvent{
		EventID:     "evt_pay_fail_" + uuid.NewString(),
		Provider:    wd.Provider,
		ProviderRef: wd.ProviderRef,
		Kind:        "payout",
		Status:      provider.StatusFailed,
		AmountMinor: wd.AmountMinor,
		Currency:    wd.Currency,
		Reference:   wd.Reference,
		OccurredAt:  time.Now().UTC(),
	}
	body, _ := json.Marshal(evt)
	if rec := postWebhook(t, srv, "telebirr", body, time.Now()); rec.Code != http.StatusOK {
		t.Fatalf("fail webhook=%d %s", rec.Code, rec.Body.String())
	}

	got := getWithdrawal(t, srv, token, wd.ID)
	if got.Status != "failed" {
		t.Fatalf("status=%q want failed", got.Status)
	}
	after := availableOf(t, ctx, pool, walletID)
	if after != before {
		t.Fatalf("available after refund=%d want %d (exact restore)", after, before)
	}
}

func registerVerifyPIN(t *testing.T, srv *httpserver.Server, sms *auth.RecordingSMSSender, phone, name string) {
	t.Helper()
	registerVerify(t, srv, sms, phone, name)
	tok := login(t, srv, phone)
	rec := putJSON(t, srv, "/v1/me/pin", tok, map[string]string{
		"password": "correct-horse-1", "pin": testPIN,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set pin=%d %s", rec.Code, rec.Body.String())
	}
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

func fundWallet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, walletAccount uuid.UUID, amount int64) {
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
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func availableOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, walletID uuid.UUID) int64 {
	t.Helper()
	var avail int64
	if err := pool.QueryRow(ctx, `SELECT available_minor FROM wallet_balances WHERE wallet_id = $1`, walletID).Scan(&avail); err != nil {
		t.Fatal(err)
	}
	return avail
}

type destResponse struct {
	ID uuid.UUID `json:"id"`
}

func createDestination(t *testing.T, srv *httpserver.Server, token, phone string) destResponse {
	t.Helper()
	rec := postJSON(t, srv, "/v1/payout-destinations", token, map[string]any{
		"provider":   "telebirr",
		"kind":       "mobile_money",
		"label":      "My Telebirr",
		"identifier": phone,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create destination=%d %s", rec.Code, rec.Body.String())
	}
	var out destResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func createWithdrawal(t *testing.T, srv *httpserver.Server, token string, destID uuid.UUID, amount, expectedFee int64) funding.Withdrawal {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"destination_id":     destID.String(),
		"amount_minor":       amount,
		"currency":           "ETB",
		"pin":                testPIN,
		"expected_fee_minor": expectedFee,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/withdrawals", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create withdrawal=%d %s", rec.Code, rec.Body.String())
	}
	var out funding.Withdrawal
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func getWithdrawal(t *testing.T, srv *httpserver.Server, token string, id uuid.UUID) funding.Withdrawal {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/withdrawals/"+id.String(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get withdrawal=%d %s", rec.Code, rec.Body.String())
	}
	var out funding.Withdrawal
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
