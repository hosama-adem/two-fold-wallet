package transfer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/migrate"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/transfer"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const (
	// opposingEachWay is the issue's shape: 500 A→B and 500 B→A, released together.
	opposingEachWay = 500
	opposingAmount  = int64(100)

	// drainAttempts spend one wallet. The balance covers drainWillPay of them.
	drainAttempts = 10
	drainUnit     = int64(1_000)
	drainWillPay  = 4

	idempotencyRacers = 50
	idempotencyAmount = int64(250)
)

// Concurrency suite for POST /v1/transfers (PRD §8.3, ADR 0003).
//
// Each scenario parks its goroutines on one barrier and opens the gate
// together, against a real Postgres. A pass means no deadlock (40P01), no
// serialization failure (40001), no negative wallet balance, and a cached
// balance that still equals the postings.

func TestConcurrencyOpposingTransfers(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	a := w.openUser(t, "Abel Bekele")
	b := w.openUser(t, "Selam Bekele")
	// Worst case is every A→B landing before any B→A. Funding each side for
	// all 500 of its own sends means every request must succeed, and both
	// balances must end where they started.
	opening := int64(opposingEachWay) * opposingAmount
	w.fund(t, a.accountID, opening)
	w.fund(t, b.accountID, opening)
	startA := accountBalance(t, ctx, w.pool, a.accountID)
	startB := accountBalance(t, ctx, w.pool, b.accountID)

	bodyAB := transferBody(t, b.phone, opposingAmount)
	bodyBA := transferBody(t, a.phone, opposingAmount)

	started := time.Now()
	outcomes := fanOut(opposingEachWay*2, func(i int) outcome {
		if i < opposingEachWay {
			return sendTransfer(w.srv, a.token, bodyAB, uuid.NewString())
		}
		return sendTransfer(w.srv, b.token, bodyBA, uuid.NewString())
	})
	t.Logf("opposing: %d transfers in %s", len(outcomes), time.Since(started).Round(time.Millisecond))

	var bad []outcome
	for _, o := range outcomes {
		if o.status != http.StatusCreated {
			bad = append(bad, o)
		}
	}
	if len(bad) > 0 {
		w.failf(t, "opposing transfers: %d/%d failed, first %s", len(bad), len(outcomes), bad[0])
	}
	w.assertNoContention(t)

	ab, ba := countDirections(t, ctx, w.pool, a.walletID, b.walletID)
	if ab != opposingEachWay || ba != opposingEachWay {
		w.failf(t, "completed directions A→B=%d B→A=%d, want %d each", ab, ba, opposingEachWay)
	}
	if n := countNotCompleted(t, ctx, w.pool, a.walletID, b.walletID); n != 0 {
		w.failf(t, "non-completed transfer rows: %d", n)
	}

	endA := accountBalance(t, ctx, w.pool, a.accountID)
	endB := accountBalance(t, ctx, w.pool, b.accountID)
	if endA != startA || endB != startB {
		w.failf(t, "balances drifted: A %d→%d B %d→%d", startA, endA, startB, endB)
	}
	if endA+endB != startA+startB {
		w.failf(t, "sum not conserved: start %d end %d", startA+startB, endA+endB)
	}
	assertZeroDrift(t, ctx, w.pool, a.accountID, b.accountID)
	assertPostingsNonNegative(t, ctx, w.pool, a.accountID, b.accountID)
}

func TestConcurrencyDrainOneWallet(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	sender := w.openUser(t, "Abel Bekele")
	recipient := w.openUser(t, "Selam Bekele")
	funded := int64(drainWillPay) * drainUnit
	w.fund(t, sender.accountID, funded)

	body := transferBody(t, recipient.phone, drainUnit)
	outcomes := fanOut(drainAttempts, func(int) outcome {
		return sendTransfer(w.srv, sender.token, body, uuid.NewString())
	})

	var paid, broke int
	var bad []outcome
	for _, o := range outcomes {
		switch {
		case o.status == http.StatusCreated:
			paid++
		case o.status == http.StatusUnprocessableEntity && o.code == transfer.CodeInsufficientFunds:
			broke++
		default:
			bad = append(bad, o)
		}
	}
	if len(bad) > 0 {
		w.failf(t, "drain: unexpected outcome %s", bad[0])
	}
	if paid != drainWillPay || broke != drainAttempts-drainWillPay {
		w.failf(t, "drain: paid=%d insufficient=%d, want paid=%d", paid, broke, drainWillPay)
	}
	w.assertNoContention(t)

	if got := accountBalance(t, ctx, w.pool, sender.accountID); got != 0 {
		w.failf(t, "sender balance = %d, want 0", got)
	}
	if got := accountBalance(t, ctx, w.pool, recipient.accountID); got != funded {
		w.failf(t, "recipient balance = %d, want %d", got, funded)
	}
	if got := countFrom(t, ctx, w.pool, sender.walletID); got != drainWillPay {
		w.failf(t, "completed sends = %d, want %d", got, drainWillPay)
	}
	assertZeroDrift(t, ctx, w.pool, sender.accountID, recipient.accountID)
	assertPostingsNonNegative(t, ctx, w.pool, sender.accountID, recipient.accountID)
}

func TestConcurrencyIdempotencyKeyOnce(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	sender := w.openUser(t, "Abel Bekele")
	recipient := w.openUser(t, "Selam Bekele")
	const opening int64 = 10_000
	w.fund(t, sender.accountID, opening)

	body := transferBody(t, recipient.phone, idempotencyAmount)
	key := uuid.NewString()
	outcomes := fanOut(idempotencyRacers, func(int) outcome {
		return sendTransfer(w.srv, sender.token, body, key)
	})

	var originals, replays, inProgress int
	var bad []outcome
	for _, o := range outcomes {
		switch {
		case o.status == http.StatusCreated && !o.replay:
			originals++
		case o.status == http.StatusCreated && o.replay:
			replays++
		case o.status == http.StatusConflict && o.code == idempotency.CodeInProgress && o.retryAfter == "1":
			inProgress++
		default:
			bad = append(bad, o)
		}
	}
	if len(bad) > 0 || originals != 1 || originals+replays+inProgress != idempotencyRacers {
		sample := outcome{}
		if len(bad) > 0 {
			sample = bad[0]
		}
		w.failf(t, "idempotency: originals=%d replays=%d in_progress=%d bad=%d sample=%s",
			originals, replays, inProgress, len(bad), sample)
	}
	w.assertNoContention(t)

	if got := countIdempotency(t, ctx, w.pool, sender.walletID, strings.ToLower(key)); got != 1 {
		w.failf(t, "transaction rows for key = %d, want 1", got)
	}
	if got := accountBalance(t, ctx, w.pool, sender.accountID); got != opening-idempotencyAmount {
		w.failf(t, "sender balance = %d, want %d", got, opening-idempotencyAmount)
	}
	if got := accountBalance(t, ctx, w.pool, recipient.accountID); got != idempotencyAmount {
		w.failf(t, "recipient balance = %d, want %d", got, idempotencyAmount)
	}
	assertZeroDrift(t, ctx, w.pool, sender.accountID, recipient.accountID)
	assertPostingsNonNegative(t, ctx, w.pool, sender.accountID)
}

func TestConcurrencySystemAccountsStayUnlocked(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	sender := w.openUser(t, "Abel Bekele")
	recipient := w.openUser(t, "Selam Bekele")
	w.fund(t, sender.accountID, 5_000)

	before := systemBalances(t, ctx, w.pool)
	release := lockSystemAccounts(t, w.pool)
	defer release()

	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := sendTransferCtx(w.srv, sender.token, transferBody(t, recipient.phone, 100), uuid.NewString(), reqCtx)
	if out.status != http.StatusCreated {
		w.failf(t, "transfer blocked or failed while system accounts were locked (%s) — hot rows must stay unlocked", out)
	}
	w.assertNoContention(t)

	var created transfer.Result
	if err := json.Unmarshal(out.body, &created); err != nil {
		t.Fatal(err)
	}
	var systemLines int
	err := w.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM postings p
		JOIN journal_entries j ON j.id = p.journal_id
		JOIN accounts a ON a.id = p.account_id
		WHERE j.transaction_id = $1
		  AND a.owner_user_id IS NULL
	`, created.Transaction.ID).Scan(&systemLines)
	if err != nil {
		t.Fatal(err)
	}
	if systemLines != 0 {
		t.Fatalf("peer journal posted %d lines on system accounts", systemLines)
	}
	after := systemBalances(t, ctx, w.pool)
	if !slices.Equal(before, after) {
		t.Fatalf("system balances changed while a peer transfer ran: before %v after %v", before, after)
	}
}

type party struct {
	phone     string
	token     string
	accountID uuid.UUID
	walletID  uuid.UUID
}

type world struct {
	pool *pgxpool.Pool
	srv  *httpserver.Server
	sms  *auth.RecordingSMSSender
	logs *logCapture
}

func newWorld(t *testing.T) *world {
	t.Helper()
	logs := &logCapture{}
	pool := concurrencyPool(t)
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelError}))
	sms := &auth.RecordingSMSSender{}
	return &world{
		pool: pool,
		srv:  newConcurrencyServer(t, pool, sms, log),
		sms:  sms,
		logs: logs,
	}
}

func (w *world) openUser(t *testing.T, name string) party {
	t.Helper()
	phone := uniquePhone()
	registerVerifyPIN(t, w.srv, w.sms, phone, name)
	accountID, walletID := walletIDs(t, context.Background(), w.pool, phone)
	return party{
		phone:     phone,
		token:     login(t, w.srv, phone),
		accountID: accountID,
		walletID:  walletID,
	}
}

func (w *world) fund(t *testing.T, accountID uuid.UUID, amount int64) {
	t.Helper()
	fund(t, context.Background(), w.pool, accountID, amount)
}

func (w *world) assertNoContention(t *testing.T) {
	t.Helper()
	logs := w.logs.String()
	lower := strings.ToLower(logs)
	if strings.Contains(logs, "40P01") || strings.Contains(logs, "40001") || strings.Contains(lower, "deadlock") {
		t.Fatalf("postgres contention failure:\n%s", logs)
	}
}

func (w *world) failf(t *testing.T, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	logs := strings.TrimSpace(w.logs.String())
	if logs == "" {
		t.Fatal(msg)
	}
	t.Fatalf("%s\n--- server log ---\n%s", msg, tail(logs, 8000))
}

// concurrencyPool is wider than the default pool so many transfers sit inside
// Postgres at once. Deadlocks need overlapping transactions, not a queue of one.
func concurrencyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — start Postgres (./dev.sh up) or run CI with the postgres service")
	}
	if err := migrate.Up(dsn, concurrencyMigrationsDir(t)); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if err := pool.Ping(pingCtx); err != nil {
		t.Fatal(err)
	}
	return pool
}

func concurrencyMigrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
}

// newConcurrencyServer is the real POST /v1/transfers stack with one swap:
// PIN matching does not run argon2id. A production verify spends 64 MiB and
// three passes, so a thousand of them would measure the hasher. The handler,
// the idempotency claim, the sorted wallet locks, and the journal still run.
func newConcurrencyServer(t *testing.T, pool *pgxpool.Pool, sms *auth.RecordingSMSSender, log *slog.Logger) *httpserver.Server {
	t.Helper()
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
		Match: fastTestPIN,
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
		Auth:           authHandler,
		Wallets:        walletHandler,
		Transfers:      transferHandler,
		RequestTimeout: 3 * time.Minute,
	})
}

// fastTestPIN accepts the suite's PIN without argon2id.
// encoded is checked only so an empty hash still fails closed.
func fastTestPIN(encoded, raw string) (bool, error) {
	if encoded == "" {
		return false, nil
	}
	return raw == testPIN, nil
}

type outcome struct {
	status     int
	code       string
	replay     bool
	retryAfter string
	body       []byte
}

func (o outcome) String() string {
	return fmt.Sprintf("status=%d code=%s replay=%v retry_after=%s body=%s",
		o.status, o.code, o.replay, o.retryAfter, o.body)
}

func sendTransfer(srv *httpserver.Server, token string, body []byte, key string) outcome {
	return sendTransferCtx(srv, token, body, key, context.Background())
}

func sendTransferCtx(srv *httpserver.Server, token string, body []byte, key string, ctx context.Context) outcome {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/transfers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)

	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &problem)
	return outcome{
		status:     rec.Code,
		code:       problem.Code,
		replay:     rec.Header().Get("Idempotent-Replayed") == "true",
		retryAfter: rec.Header().Get("Retry-After"),
		body:       append([]byte(nil), rec.Body.Bytes()...),
	}
}

func transferBody(t *testing.T, recipient string, amount int64) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"recipient":          recipient,
		"amount_minor":       amount,
		"currency":           "ETB",
		"pin":                testPIN,
		"expected_fee_minor": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fanOut parks n goroutines on a barrier, then releases them together.
func fanOut(n int, fn func(i int) outcome) []outcome {
	out := make([]outcome, n)
	var ready sync.WaitGroup
	ready.Add(n)
	start := make(chan struct{})
	var done sync.WaitGroup
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			out[i] = fn(i)
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	return out
}

func accountBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) int64 {
	t.Helper()
	acct, err := ledgerdb.New(pool).GetAccountByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return acct.BalanceMinor
}

func assertZeroDrift(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Helper()
	q := ledgerdb.New(pool)
	for _, id := range ids {
		acct, err := q.GetAccountByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		recomputed, err := ledger.RecomputeBalance(ctx, pool, id)
		if err != nil {
			t.Fatal(err)
		}
		if acct.BalanceMinor != recomputed {
			t.Fatalf("drift on %s: cache=%d postings=%d", id, acct.BalanceMinor, recomputed)
		}
		if !acct.AllowNegative && acct.BalanceMinor < 0 {
			t.Fatalf("negative balance on %s: %d", id, acct.BalanceMinor)
		}
	}
}

func assertPostingsNonNegative(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		var n int
		err := pool.QueryRow(ctx, `
			SELECT count(*) FROM postings
			WHERE account_id = $1 AND balance_after_minor < 0
		`, id).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("account %s has %d postings with a negative running balance", id, n)
		}
	}
}

func countDirections(t *testing.T, ctx context.Context, pool *pgxpool.Pool, a, b uuid.UUID) (ab, ba int) {
	t.Helper()
	err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE from_wallet_id = $1 AND to_wallet_id = $2),
			count(*) FILTER (WHERE from_wallet_id = $2 AND to_wallet_id = $1)
		FROM transactions
		WHERE status = 'completed'
		  AND kind = 'p2p'
		  AND (
		        (from_wallet_id = $1 AND to_wallet_id = $2)
		     OR (from_wallet_id = $2 AND to_wallet_id = $1)
		  )
	`, a, b).Scan(&ab, &ba)
	if err != nil {
		t.Fatal(err)
	}
	return ab, ba
}

func countNotCompleted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, a, b uuid.UUID) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactions
		WHERE status <> 'completed'
		  AND (from_wallet_id = $1 OR from_wallet_id = $2
		    OR to_wallet_id = $1 OR to_wallet_id = $2)
	`, a, b).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func countFrom(t *testing.T, ctx context.Context, pool *pgxpool.Pool, walletID uuid.UUID) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactions
		WHERE from_wallet_id = $1 AND status = 'completed' AND kind = 'p2p'
	`, walletID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func countIdempotency(t *testing.T, ctx context.Context, pool *pgxpool.Pool, walletID uuid.UUID, key string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactions
		WHERE from_wallet_id = $1 AND idempotency_key = $2
	`, walletID, key).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func systemBalances(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []int64 {
	t.Helper()
	q := ledgerdb.New(pool)
	out := make([]int64, 0, len(ledger.SystemAccounts))
	for _, sys := range ledger.SystemAccounts {
		acct, err := q.GetAccountByCode(ctx, sys.Code)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, acct.BalanceMinor)
	}
	return out
}

// lockSystemAccounts holds FOR UPDATE on every seeded system account until
// the returned function rolls the transaction back. A transfer that also
// locks those rows blocks until the context on the request fires.
func lockSystemAccounts(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	ctx := context.Background()
	q := ledgerdb.New(pool)
	ids := make([]uuid.UUID, 0, len(ledger.SystemAccounts))
	for _, sys := range ledger.SystemAccounts {
		acct, err := q.GetAccountByCode(ctx, sys.Code)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, acct.ID)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int {
		return bytes.Compare(a[:], b[:])
	})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locked := ledgerdb.New(tx)
	for _, id := range ids {
		if _, err := locked.GetAccountForUpdate(ctx, id); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
	}
	return func() { _ = tx.Rollback(ctx) }
}

type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logCapture) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
