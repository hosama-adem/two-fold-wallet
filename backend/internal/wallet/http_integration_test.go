package wallet_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

func TestWalletsShowSpendableBalanceAndLookupHidesPhone(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}

	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	senderPhone := uniquePhone()
	recipientPhone := uniquePhone()
	registerAndVerify(t, srv, sms, senderPhone, "Abel Bekele")
	registerAndVerify(t, srv, sms, recipientPhone, "Selam Bekele")

	if _, err := pool.Exec(ctx, `UPDATE users SET handle = $2 WHERE phone_e164 = $1`, recipientPhone, "selam_k"); err != nil {
		t.Fatal(err)
	}

	senderToken := login(t, srv, senderPhone)
	recipientToken := login(t, srv, recipientPhone)

	var recipientWallet uuid.UUID
	var recipientAccount uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT w.id, w.account_id
		FROM wallets w
		JOIN users u ON u.id = w.user_id
		WHERE u.phone_e164 = $1 AND w.currency = 'ETB'
	`, recipientPhone).Scan(&recipientWallet, &recipientAccount)
	if err != nil {
		t.Fatal(err)
	}

	const ledgerMinor int64 = 10_000
	const heldMinor int64 = 2_500
	fund(t, ctx, pool, recipientAccount, ledgerMinor)

	holdID := uuid.Must(uuid.NewV7())
	_, err = pool.Exec(ctx, `
		INSERT INTO wallet_holds (id, wallet_id, amount_minor, currency, kind, status)
		VALUES ($1, $2, $3, 'ETB', 'withdrawal', 'pending')
	`, holdID, recipientWallet, heldMinor)
	if err != nil {
		t.Fatal(err)
	}

	body := getJSON(t, srv, "/v1/wallets", recipientToken)
	var listed struct {
		Wallets []struct {
			ID                    uuid.UUID `json:"id"`
			LedgerBalanceMinor    int64     `json:"ledger_balance_minor"`
			AvailableBalanceMinor int64     `json:"available_balance_minor"`
			HeldMinor             int64     `json:"held_minor"`
			Currency              string    `json:"currency"`
		} `json:"wallets"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode wallets: %v body=%s", err, body)
	}
	if len(listed.Wallets) != 1 {
		t.Fatalf("wallets=%d body=%s", len(listed.Wallets), body)
	}
	got := listed.Wallets[0]
	if got.ID != recipientWallet || got.Currency != "ETB" {
		t.Fatalf("wallet=%+v", got)
	}
	if got.LedgerBalanceMinor != ledgerMinor || got.HeldMinor != heldMinor || got.AvailableBalanceMinor != ledgerMinor-heldMinor {
		t.Fatalf("balances ledger=%d held=%d available=%d", got.LedgerBalanceMinor, got.HeldMinor, got.AvailableBalanceMinor)
	}

	senderBody := getJSON(t, srv, "/v1/wallets", senderToken)
	if strings.Contains(string(senderBody), recipientWallet.String()) {
		t.Fatalf("sender can see the recipient wallet: %s", senderBody)
	}

	masked, err := wallet.MaskPhone(recipientPhone)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{recipientPhone, "0" + recipientPhone[4:], "@Selam_K"} {
		raw := string(getJSON(t, srv, "/v1/recipients/lookup?q="+url.QueryEscape(q), senderToken))
		if strings.Contains(raw, recipientPhone) || strings.Contains(raw, recipientPhone[1:]) || strings.Contains(raw, recipientPhone[4:]) {
			t.Fatalf("lookup %q returned the phone: %s", q, raw)
		}
		if !strings.Contains(raw, masked) {
			t.Fatalf("lookup %q missing mask %q: %s", q, masked, raw)
		}
		if strings.Contains(raw, "phone_e164") {
			t.Fatalf("lookup exposed phone_e164: %s", raw)
		}
		if !strings.Contains(raw, "Selam Bekele") {
			t.Fatalf("lookup missing display name: %s", raw)
		}
	}

	missing := getRaw(t, srv, http.MethodGet, "/v1/recipients/lookup?q="+url.QueryEscape(uniquePhone()), senderToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing recipient status=%d body=%s", missing.Code, missing.Body.String())
	}
	if strings.Contains(missing.Body.String(), recipientPhone) {
		t.Fatal("not-found body contained a phone")
	}

	anon := getRaw(t, srv, http.MethodGet, "/v1/wallets", "")
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon status=%d", anon.Code)
	}

	_, err = pool.Exec(ctx, `
		UPDATE wallet_holds
		SET status = 'released', released_at = now()
		WHERE id = $1
	`, holdID)
	if err != nil {
		t.Fatal(err)
	}
	released := getJSON(t, srv, "/v1/wallets", recipientToken)
	if err := json.Unmarshal(released, &listed); err != nil {
		t.Fatal(err)
	}
	got = listed.Wallets[0]
	if got.HeldMinor != 0 || got.AvailableBalanceMinor != ledgerMinor || got.LedgerBalanceMinor != ledgerMinor {
		t.Fatalf("after release ledger=%d held=%d available=%d", got.LedgerBalanceMinor, got.HeldMinor, got.AvailableBalanceMinor)
	}
}

func TestFreezeConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()

	userID := uuid.Must(uuid.NewV7())
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		t.Fatal(err)
	}
	phone := fmt.Sprintf("+2519%08d", n.Int64())
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, phone_e164, display_name, password_hash, status, phone_verified_at)
		VALUES ($1, $2, 'Freeze', 'hash', 'active', now())
	`, userID, phone)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := wallet.NewService(pool).OpenETB(ctx, tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx, `
		UPDATE wallets SET status = 'frozen', freeze_mode = 'none' WHERE id = $1
	`, opened.WalletID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "wallets_freeze_consistent" {
		t.Fatalf("want wallets_freeze_consistent, got %v", err)
	}

	_, err = pool.Exec(ctx, `
		UPDATE wallets
		SET status = 'frozen', freeze_mode = 'debit', frozen_reason = 'compliance review'
		WHERE id = $1
	`, opened.WalletID)
	if err != nil {
		t.Fatal(err)
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
	svc := auth.NewService(auth.ServiceDeps{
		Users:      users,
		Sessions:   sessions,
		Tokens:     tokens,
		Audit:      audits,
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
	return httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, pool, log, httpserver.Options{
		Auth:    authHandler,
		Wallets: walletHandler,
	})
}

func registerAndVerify(t *testing.T, srv *httpserver.Server, sms *auth.RecordingSMSSender, phone, name string) {
	t.Helper()
	rec := postJSON(t, srv, "/v1/auth/register", map[string]string{
		"phone": phone, "password": "correct-horse-1", "display_name": name,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s status=%d body=%s", phone, rec.Code, rec.Body.String())
	}
	rec = postJSON(t, srv, "/v1/auth/verify-phone", map[string]string{
		"phone": phone, "code": sms.LastCode,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify %s status=%d body=%s", phone, rec.Code, rec.Body.String())
	}
}

func login(t *testing.T, srv *httpserver.Server, phone string) string {
	t.Helper()
	rec := postJSON(t, srv, "/v1/auth/login", map[string]string{
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

func postJSON(t *testing.T, srv *httpserver.Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, srv, http.MethodPost, path, "", body)
}

func getJSON(t *testing.T, srv *httpserver.Server, path, bearer string) []byte {
	t.Helper()
	rec := getRaw(t, srv, http.MethodGet, path, bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

func getRaw(t *testing.T, srv *httpserver.Server, method, path, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, srv, method, path, bearer, nil)
}

func doJSON(t *testing.T, srv *httpserver.Server, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
