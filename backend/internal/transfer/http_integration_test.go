package transfer_test

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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/idempotency"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
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

const testPIN = "4829"

func TestTransferHappyPathAndJournal(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	senderPhone := uniquePhone()
	recipientPhone := uniquePhone()
	registerVerifyPIN(t, srv, sms, senderPhone, "Abel Bekele")
	registerVerifyPIN(t, srv, sms, recipientPhone, "Selam Bekele")
	senderToken := login(t, srv, senderPhone)

	senderAccount, senderWallet := walletIDs(t, ctx, pool, senderPhone)
	recipientAccount, recipientWallet := walletIDs(t, ctx, pool, recipientPhone)
	fund(t, ctx, pool, senderAccount, 10_000)

	const amount int64 = 2_500
	rec := postTransfer(t, srv, senderToken, map[string]any{
		"recipient":          recipientPhone,
		"amount_minor":       amount,
		"currency":           "ETB",
		"pin":                testPIN,
		"note":               "tea",
		"expected_fee_minor": 0,
	}, uuid.NewString())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var out transfer.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	tx := out.Transaction
	if tx.Status != "completed" || tx.Kind != "p2p" || tx.AmountMinor != amount || tx.FeeMinor != 0 {
		t.Fatalf("transaction=%+v", tx)
	}
	if tx.FromWalletID != senderWallet || tx.ToWalletID != recipientWallet {
		t.Fatalf("wallets from=%s to=%s", tx.FromWalletID, tx.ToWalletID)
	}
	if !strings.HasPrefix(tx.Reference, "TF-") || len(tx.Reference) != 12 {
		t.Fatalf("reference=%q", tx.Reference)
	}
	if tx.CompletedAt == nil {
		t.Fatal("completed_at missing")
	}
	if tx.Note == nil || *tx.Note != "tea" {
		t.Fatalf("note=%v", tx.Note)
	}

	assertBalances(t, ctx, pool, senderAccount, 10_000-amount, recipientAccount, amount)
	assertJournalMatchesPRD(t, ctx, pool, tx.ID, senderAccount, recipientAccount, amount)

	var eventCount int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transaction_events
		WHERE transaction_id = $1 AND from_status = 'pending' AND to_status = 'completed'
	`, tx.ID).Scan(&eventCount)
	if err != nil || eventCount != 1 {
		t.Fatalf("events=%d err=%v", eventCount, err)
	}
}

func TestTransferErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	senderPhone := uniquePhone()
	recipientPhone := uniquePhone()
	registerVerifyPIN(t, srv, sms, senderPhone, "Abel Bekele")
	registerVerifyPIN(t, srv, sms, recipientPhone, "Selam Bekele")
	senderToken := login(t, srv, senderPhone)

	senderAccount, senderWallet := walletIDs(t, ctx, pool, senderPhone)
	fund(t, ctx, pool, senderAccount, 1_000)

	t.Run("insufficient_funds", func(t *testing.T) {
		rec := postTransfer(t, srv, senderToken, map[string]any{
			"recipient": recipientPhone, "amount_minor": 5_000, "pin": testPIN, "expected_fee_minor": 0,
		}, uuid.NewString())
		assertProblem(t, rec, http.StatusUnprocessableEntity, "insufficient_funds")
	})

	t.Run("self_transfer", func(t *testing.T) {
		rec := postTransfer(t, srv, senderToken, map[string]any{
			"recipient": senderPhone, "amount_minor": 100, "pin": testPIN, "expected_fee_minor": 0,
		}, uuid.NewString())
		assertProblem(t, rec, http.StatusUnprocessableEntity, "self_transfer")
	})

	t.Run("recipient_not_found", func(t *testing.T) {
		rec := postTransfer(t, srv, senderToken, map[string]any{
			"recipient": uniquePhone(), "amount_minor": 100, "pin": testPIN, "expected_fee_minor": 0,
		}, uuid.NewString())
		assertProblem(t, rec, http.StatusNotFound, "not_found")
	})

	t.Run("frozen_sender", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			UPDATE wallets
			SET status = 'frozen', freeze_mode = 'debit', frozen_reason = 'compliance review'
			WHERE id = $1
		`, senderWallet)
		if err != nil {
			t.Fatal(err)
		}
		rec := postTransfer(t, srv, senderToken, map[string]any{
			"recipient": recipientPhone, "amount_minor": 100, "pin": testPIN, "expected_fee_minor": 0,
		}, uuid.NewString())
		assertProblem(t, rec, http.StatusForbidden, "wallet_frozen")

		_, err = pool.Exec(ctx, `
			UPDATE wallets
			SET status = 'active', freeze_mode = 'none', frozen_reason = NULL
			WHERE id = $1
		`, senderWallet)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("frozen_recipient_full", func(t *testing.T) {
		_, recipientWallet := walletIDs(t, ctx, pool, recipientPhone)
		_, err := pool.Exec(ctx, `
			UPDATE wallets
			SET status = 'frozen', freeze_mode = 'full', frozen_reason = 'fraud hold'
			WHERE id = $1
		`, recipientWallet)
		if err != nil {
			t.Fatal(err)
		}
		rec := postTransfer(t, srv, senderToken, map[string]any{
			"recipient": recipientPhone, "amount_minor": 100, "pin": testPIN, "expected_fee_minor": 0,
		}, uuid.NewString())
		assertProblem(t, rec, http.StatusForbidden, "wallet_frozen")
	})
}

func TestTransferIdempotentReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	senderPhone := uniquePhone()
	recipientPhone := uniquePhone()
	registerVerifyPIN(t, srv, sms, senderPhone, "Abel Bekele")
	registerVerifyPIN(t, srv, sms, recipientPhone, "Selam Bekele")
	senderToken := login(t, srv, senderPhone)
	senderAccount, _ := walletIDs(t, ctx, pool, senderPhone)
	fund(t, ctx, pool, senderAccount, 8_000)

	key := uuid.NewString()
	body := map[string]any{
		"recipient": recipientPhone, "amount_minor": 1_200, "pin": testPIN, "expected_fee_minor": 0,
	}
	first := postTransfer(t, srv, senderToken, body, key)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := postTransfer(t, srv, senderToken, body, key)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status=%d body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("expected Idempotent-Replayed")
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("body mismatch\n%s\n%s", first.Body.String(), second.Body.String())
	}

	var senderCount int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactions t
		JOIN wallets w ON w.id = t.from_wallet_id
		JOIN users u ON u.id = w.user_id
		WHERE u.phone_e164 = $1 AND t.kind = 'p2p'
	`, senderPhone).Scan(&senderCount)
	if err != nil || senderCount != 1 {
		t.Fatalf("sender transfers=%d err=%v", senderCount, err)
	}
}

func assertJournalMatchesPRD(t *testing.T, ctx context.Context, pool *pgxpool.Pool, txID, fromAccount, toAccount uuid.UUID, amount int64) {
	t.Helper()
	var journalID uuid.UUID
	var kind string
	err := pool.QueryRow(ctx, `
		SELECT id, kind FROM journal_entries WHERE transaction_id = $1
	`, txID).Scan(&journalID, &kind)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "p2p" {
		t.Fatalf("journal kind=%q", kind)
	}
	postings, err := ledgerdb.New(pool).ListPostingsByJournalID(ctx, journalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 2 {
		t.Fatalf("want 2 postings (fee 0), got %d", len(postings))
	}

	var debit, credit *ledgerdb.Posting
	for i := range postings {
		p := &postings[i]
		switch p.Direction {
		case "debit":
			debit = p
		case "credit":
			credit = p
		}
	}
	if debit == nil || credit == nil {
		t.Fatalf("postings=%+v", postings)
	}
	// PRD §7.3 P2P with fee 0: Dr sender amount / Cr recipient amount.
	if debit.AccountID != fromAccount || debit.AmountMinor != amount {
		t.Fatalf("debit=%+v want account=%s amount=%d", debit, fromAccount, amount)
	}
	if credit.AccountID != toAccount || credit.AmountMinor != amount {
		t.Fatalf("credit=%+v want account=%s amount=%d", credit, toAccount, amount)
	}
}

func assertBalances(t *testing.T, ctx context.Context, pool *pgxpool.Pool, senderAccount uuid.UUID, wantSender int64, recipientAccount uuid.UUID, wantRecipient int64) {
	t.Helper()
	q := ledgerdb.New(pool)
	s, err := q.GetAccountByID(ctx, senderAccount)
	if err != nil {
		t.Fatal(err)
	}
	r, err := q.GetAccountByID(ctx, recipientAccount)
	if err != nil {
		t.Fatal(err)
	}
	if s.BalanceMinor != wantSender || r.BalanceMinor != wantRecipient {
		t.Fatalf("balances sender=%d want=%d recipient=%d want=%d", s.BalanceMinor, wantSender, r.BalanceMinor, wantRecipient)
	}
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != code {
		t.Fatalf("code=%q want %q body=%s", p.Code, code, rec.Body.String())
	}
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
	pins := pin.New(pin.Deps{
		Users: users,
		Audit: audits,
		Match: auth.VerifyPIN,
		Log:   log,
	})
	svc := auth.NewService(auth.ServiceDeps{
		Users:      users,
		Sessions:   sessions,
		Tokens:     tokens,
		Audit:      audits,
		Pins:       pins,
		Pool:       pool,
		Wallets:    wallets,
		SMS:        sms,
		Log:        log,
		RefreshTTL: 30 * 24 * time.Hour,
	})
	authHandler := auth.NewHandler(svc, ratelimit.NewMemory(ratelimit.Config{
		Burst:  1000,
		Window: time.Minute,
	}))
	walletHandler := wallet.NewHandler(wallets, authHandler.RequireAccess(), auth.UserIDFromContext)
	idemStore := idempotency.NewStore(pool)
	transferHandler := transfer.NewHandler(
		transfer.NewService(pool),
		pins,
		authHandler.RequireAccess(),
		idempotency.Middleware(idemStore, auth.UserIDFromContext),
		auth.UserIDFromContext,
	)
	return httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, pool, log, httpserver.Options{
		Auth:      authHandler,
		Wallets:   walletHandler,
		Transfers: transferHandler,
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

func postTransfer(t *testing.T, srv *httpserver.Server, bearer string, body any, idemKey string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Idempotency-Key", idemKey)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	return rec
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
	req.Header.Set("Authorization", "Bearer "+bearer)
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
