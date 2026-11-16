package history_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
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
	"github.com/hosama-adem/twofold/backend/internal/history"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

type listItem struct {
	ID                uuid.UUID `json:"id"`
	Reference         string    `json:"reference"`
	Direction         string    `json:"direction"`
	SignedAmountMinor int64     `json:"signed_amount_minor"`
	AmountMinor       int64     `json:"amount_minor"`
	FeeMinor          int64     `json:"fee_minor"`
}

type listResp struct {
	Items      []listItem `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

// TestHistoryPaginationNoDuplicatesOrGaps proves the keyset list is stable while
// new transactions arrive mid-pagination (FR-HIS-1): paging walks the snapshot
// that existed when paging began, so no older row is duplicated or skipped, and
// rows inserted after the first page never leak into later pages.
func TestHistoryPaginationNoDuplicatesOrGaps(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	phoneA := uniquePhone()
	phoneB := uniquePhone()
	registerVerify(t, srv, sms, phoneA, "Abel Bekele")
	registerVerify(t, srv, sms, phoneB, "Selam Bekele")
	tokenA := login(t, srv, phoneA)

	userA, walletA := userAndWallet(t, ctx, pool, phoneA)
	_, walletB := userAndWallet(t, ctx, pool, phoneB)

	// 15 original rows with staggered created_at so newest-first order is exact.
	base := time.Now().Add(-2 * time.Hour)
	original := map[uuid.UUID]bool{}
	for i := 0; i < 15; i++ {
		id := insertTx(t, ctx, pool, userA, walletA, walletB, base.Add(time.Duration(i)*time.Minute), "")
		original[id] = true
	}

	// Page 1 is taken against the 15-row snapshot.
	page1 := listPage(t, srv, tokenA, "limit=5")
	if len(page1.Items) != 5 {
		t.Fatalf("page1 size=%d want 5", len(page1.Items))
	}
	if page1.NextCursor == nil {
		t.Fatal("page1 missing next_cursor")
	}

	seen := map[uuid.UUID]int{}
	for _, it := range page1.Items {
		seen[it.ID]++
		// A is the sender on every row: money out, signed = -(amount+fee).
		if it.Direction != "out" {
			t.Fatalf("direction=%q want out", it.Direction)
		}
		if it.SignedAmountMinor != -(it.AmountMinor + it.FeeMinor) {
			t.Fatalf("signed=%d want %d", it.SignedAmountMinor, -(it.AmountMinor + it.FeeMinor))
		}
	}

	// Newer rows arrive between page 1 and the rest of the walk.
	newer := map[uuid.UUID]bool{}
	for i := 0; i < 3; i++ {
		id := insertTx(t, ctx, pool, userA, walletA, walletB, time.Now().Add(time.Duration(i)*time.Second), "")
		newer[id] = true
	}

	// Walk the remaining pages using the cursor captured before the inserts.
	cursor := *page1.NextCursor
	for cursor != "" {
		page := listPage(t, srv, tokenA, "limit=5&cursor="+cursor)
		for _, it := range page.Items {
			seen[it.ID]++
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}

	// Every original row appears exactly once; no duplicates.
	for id := range original {
		if seen[id] != 1 {
			t.Fatalf("original %s seen %d times, want 1", id, seen[id])
		}
	}
	if len(seen) != len(original) {
		t.Fatalf("saw %d distinct ids, want %d (newer rows must not leak into older pages)", len(seen), len(original))
	}
	// None of the mid-pagination inserts leaked into the older pages.
	for id := range newer {
		if seen[id] != 0 {
			t.Fatalf("newer row %s leaked into pagination", id)
		}
	}
}

// TestHistoryReceiptParticipantsOnly checks both participants can read a receipt
// and a non-participant gets 404 — existence never leaks (FR-HIS-2).
func TestHistoryReceiptParticipantsOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	phoneA := uniquePhone()
	phoneB := uniquePhone()
	phoneC := uniquePhone()
	registerVerify(t, srv, sms, phoneA, "Abel Bekele")
	registerVerify(t, srv, sms, phoneB, "Selam Bekele")
	registerVerify(t, srv, sms, phoneC, "Dawit Bekele")
	tokenA := login(t, srv, phoneA)
	tokenB := login(t, srv, phoneB)
	tokenC := login(t, srv, phoneC)

	userA, walletA := userAndWallet(t, ctx, pool, phoneA)
	_, walletB := userAndWallet(t, ctx, pool, phoneB)

	id := insertTx(t, ctx, pool, userA, walletA, walletB, time.Now().Add(-time.Minute), "")
	ref := referenceOf(t, ctx, pool, id)

	recA := getAuth(t, srv, "/v1/transactions/"+ref, tokenA)
	if recA.Code != http.StatusOK {
		t.Fatalf("A receipt status=%d body=%s", recA.Code, recA.Body.String())
	}
	var rA history.Receipt
	if err := json.Unmarshal(recA.Body.Bytes(), &rA); err != nil {
		t.Fatal(err)
	}
	if rA.Direction != "out" || rA.Reference != ref {
		t.Fatalf("A receipt=%+v", rA)
	}
	if rA.CreatedAtAddis == "" {
		t.Fatal("A receipt missing created_at_addis")
	}

	recB := getAuth(t, srv, "/v1/transactions/"+ref, tokenB)
	if recB.Code != http.StatusOK {
		t.Fatalf("B receipt status=%d body=%s", recB.Code, recB.Body.String())
	}
	var rB history.Receipt
	if err := json.Unmarshal(recB.Body.Bytes(), &rB); err != nil {
		t.Fatal(err)
	}
	if rB.Direction != "in" {
		t.Fatalf("B direction=%q want in", rB.Direction)
	}
	// The list uses the same participant rule as the receipt: the recipient
	// sees the row, a stranger does not.
	if !listHas(t, srv, tokenB, ref, "in") {
		t.Fatal("recipient list missing the transaction")
	}
	if listHas(t, srv, tokenC, ref, "") {
		t.Fatal("non-participant list leaked the transaction")
	}

	recC := getAuth(t, srv, "/v1/transactions/"+ref, tokenC)
	assertProblem(t, recC, http.StatusNotFound, "not_found")

	// Unknown reference is also 404, identical to the non-participant case.
	recMiss := getAuth(t, srv, "/v1/transactions/HIS-does-not-exist", tokenA)
	assertProblem(t, recMiss, http.StatusNotFound, "not_found")
}

// TestHistoryExportCSVInjection checks the stream carries the right headers and
// that a note beginning with a formula character is neutralised (FR-HIS-3).
func TestHistoryExportCSVInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	sms := &auth.RecordingSMSSender{}
	srv := newServer(t, pool, sms)
	ctx := context.Background()

	phoneA := uniquePhone()
	phoneB := uniquePhone()
	registerVerify(t, srv, sms, phoneA, "Abel Bekele")
	registerVerify(t, srv, sms, phoneB, "Selam Bekele")
	tokenA := login(t, srv, phoneA)

	userA, walletA := userAndWallet(t, ctx, pool, phoneA)
	_, walletB := userAndWallet(t, ctx, pool, phoneB)

	insertTx(t, ctx, pool, userA, walletA, walletB, time.Now().Add(-time.Minute), "=SUM(A1:A9)")

	rec := getAuth(t, srv, "/v1/transactions/export.csv", tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("content-type=%q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="transactions.csv"` {
		t.Fatalf("content-disposition=%q", cd)
	}

	records, err := csv.NewReader(bytes.NewReader(rec.Body.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("want header + 1 row, got %d", len(records))
	}
	header := records[0]
	noteCol := indexOf(header, "note")
	if noteCol < 0 {
		t.Fatalf("no note column in %v", header)
	}
	note := records[1][noteCol]
	if !strings.HasPrefix(note, "'=") {
		t.Fatalf("note cell %q not escaped against CSV injection", note)
	}
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
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
	historyHandler := history.NewHandler(history.NewService(pool), authHandler.RequireAccess(), auth.UserIDFromContext)
	return httpserver.New(config.Config{AppEnv: "test", HTTPAddr: ":0"}, pool, log, httpserver.Options{
		Auth:    authHandler,
		History: historyHandler,
	})
}

func listHas(t *testing.T, srv *httpserver.Server, bearer, ref, direction string) bool {
	t.Helper()
	page := listPage(t, srv, bearer, "limit=50")
	for _, it := range page.Items {
		if it.Reference == ref {
			if direction != "" && it.Direction != direction {
				t.Fatalf("reference %s direction=%q want %q", ref, it.Direction, direction)
			}
			return true
		}
	}
	return false
}

func listPage(t *testing.T, srv *httpserver.Server, bearer, query string) listResp {
	t.Helper()
	rec := getAuth(t, srv, "/v1/transactions?"+query, bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v body=%s", err, rec.Body.String())
	}
	return out
}

func insertTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, initiator, from, to uuid.UUID, createdAt time.Time, note string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	ref := "HIS-" + id.String()
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO transactions (
			id, reference, kind, status, initiator_user_id,
			from_wallet_id, to_wallet_id, amount_minor, fee_minor, currency,
			note, created_at, completed_at
		) VALUES ($1,$2,'p2p','completed',$3,$4,$5,$6,0,'ETB',$7,$8,$8)
	`, id, ref, initiator, from, to, int64(100), notePtr, createdAt)
	if err != nil {
		t.Fatalf("insert tx: %v", err)
	}
	return id
}

func referenceOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var ref string
	if err := pool.QueryRow(ctx, `SELECT reference FROM transactions WHERE id = $1`, id).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func userAndWallet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, phone string) (userID, walletID uuid.UUID) {
	t.Helper()
	err := pool.QueryRow(ctx, `
		SELECT u.id, w.id
		FROM users u
		JOIN wallets w ON w.user_id = u.id
		WHERE u.phone_e164 = $1 AND w.currency = 'ETB'
	`, phone).Scan(&userID, &walletID)
	if err != nil {
		t.Fatal(err)
	}
	return userID, walletID
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

func registerVerify(t *testing.T, srv *httpserver.Server, sms *auth.RecordingSMSSender, phone, name string) {
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

func getAuth(t *testing.T, srv *httpserver.Server, path, bearer string) *httptest.ResponseRecorder {
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
	path := filepath.Join(filepath.Dir(file), "..", "auth", "testdata", "jwt_ed25519.pem")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
