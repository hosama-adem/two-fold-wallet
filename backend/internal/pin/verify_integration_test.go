package pin_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/user"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func setupPINUser(t *testing.T, rawPIN string) (*pin.Service, *user.Repository, *pgxpool.Pool, uuid.UUID, *fixedClock) {
	t.Helper()
	pool := testutil.Postgres(t)
	users := user.NewRepository(pool)
	audits := audit.NewRecorder(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := &fixedClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}

	hash, err := auth.HashPIN(rawPIN)
	if err != nil {
		t.Fatal(err)
	}
	u, err := users.Create(ctxBackground(), user.CreateParams{
		PhoneE164:    uniquePhone(),
		DisplayName:  "PIN Tester",
		PasswordHash: "x", // unused in these tests
		Status:       "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.UpdatePinHash(ctxBackground(), u.ID, hash); err != nil {
		t.Fatal(err)
	}

	svc := pin.New(pin.Deps{
		Users: users,
		Audit: audits,
		Match: auth.VerifyPIN,
		Log:   log,
		Clock: clk,
	})
	return svc, users, pool, u.ID, clk
}

func ctxBackground() context.Context { return context.Background() }

func uniquePhone() string {
	n := uuid.New().ID() % 100_000_000
	return "+2519" + pad8(n)
}

func pad8(n uint32) string {
	s := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		s[i] = byte('0' + n%10)
		n /= 10
	}
	return string(s)
}

func TestVerifyTriesLeftAndLockout(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	const good = "4829"
	svc, users, _, userID, _ := setupPINUser(t, good)
	ctx := ctxBackground()
	meta := pin.Meta{IP: "127.0.0.1", UserAgent: "test", RequestID: "req-pin-1"}

	for i := 1; i <= 4; i++ {
		err := svc.Verify(ctx, userID, "7391", meta)
		he, ok := httpx.AsError(err)
		if !ok {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if he.Code != "invalid_pin" || he.Status != 401 {
			t.Fatalf("attempt %d: code=%q status=%d", i, he.Code, he.Status)
		}
		wantTries := pin.MaxFailures - i
		if he.TriesRemaining == nil || *he.TriesRemaining != wantTries {
			t.Fatalf("attempt %d: tries=%v want %d", i, he.TriesRemaining, wantTries)
		}
		u, _ := users.GetByID(ctx, userID)
		if u.PinFailedCount != int32(i) {
			t.Fatalf("attempt %d: failed_count=%d", i, u.PinFailedCount)
		}
		if u.PinLockedUntil != nil {
			t.Fatalf("attempt %d: unexpected lock %v", i, u.PinLockedUntil)
		}
	}

	// 5th failure → lockout (403), not another invalid_pin.
	err := svc.Verify(ctx, userID, "7391", meta)
	he, ok := httpx.AsError(err)
	if !ok {
		t.Fatalf("5th: %v", err)
	}
	if he.Code != "pin_locked" || he.Status != 403 {
		t.Fatalf("5th: code=%q status=%d detail=%q", he.Code, he.Status, he.Detail)
	}
	if he.LockedUntil == nil {
		t.Fatal("5th: missing locked_until")
	}
	if he.TriesRemaining != nil {
		t.Fatal("5th: locked response must not include tries_remaining")
	}

	u, err := users.GetByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.PinFailedCount != 5 || u.PinLockedUntil == nil {
		t.Fatalf("after lock: count=%d until=%v", u.PinFailedCount, u.PinLockedUntil)
	}
}

func TestVerifyWhileLockedDoesNotBurnTries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	users := user.NewRepository(pool)
	audits := audit.NewRecorder(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := &fixedClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}

	const good = "4829"
	hash, err := auth.HashPIN(good)
	if err != nil {
		t.Fatal(err)
	}
	u, err := users.Create(ctxBackground(), user.CreateParams{
		PhoneE164:    uniquePhone(),
		DisplayName:  "PIN Locked",
		PasswordHash: "x",
		Status:       "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.UpdatePinHash(ctxBackground(), u.ID, hash); err != nil {
		t.Fatal(err)
	}

	var matchCalls atomic.Int32
	svc := pin.New(pin.Deps{
		Users: users,
		Audit: audits,
		Match: func(encoded, raw string) (bool, error) {
			matchCalls.Add(1)
			return auth.VerifyPIN(encoded, raw)
		},
		Log:   log,
		Clock: clk,
	})
	ctx := ctxBackground()
	meta := pin.Meta{RequestID: "req-locked"}

	for i := 0; i < pin.MaxFailures; i++ {
		_ = svc.Verify(ctx, u.ID, "0001", meta)
	}
	before, err := users.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.PinLockedUntil == nil {
		t.Fatal("expected lock")
	}
	lockUntil := *before.PinLockedUntil
	failed := before.PinFailedCount
	callsAfterLockSetup := matchCalls.Load()

	// Still inside the lock window — even the correct PIN must be rejected,
	// with no argon2 (Match) and no counter bump.
	err = svc.Verify(ctx, u.ID, good, meta)
	he, ok := httpx.AsError(err)
	if !ok || he.Code != "pin_locked" {
		t.Fatalf("correct-while-locked: %v", err)
	}
	if he.LockedUntil == nil || !he.LockedUntil.Equal(lockUntil.UTC()) {
		t.Fatalf("locked_until changed: got %v want %v", he.LockedUntil, lockUntil)
	}
	if matchCalls.Load() != callsAfterLockSetup {
		t.Fatalf("Match called while locked: before=%d after=%d", callsAfterLockSetup, matchCalls.Load())
	}

	after, err := users.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PinFailedCount != failed {
		t.Fatalf("failed_count burned: %d → %d", failed, after.PinFailedCount)
	}
	_ = clk // lock window still current
}

func TestVerifyUnlocksAfterExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	const good = "4829"
	svc, users, _, userID, clk := setupPINUser(t, good)
	ctx := ctxBackground()
	meta := pin.Meta{RequestID: "req-expiry"}

	for i := 0; i < pin.MaxFailures; i++ {
		_ = svc.Verify(ctx, userID, "1112", meta)
	}
	locked, _ := users.GetByID(ctx, userID)
	if locked.PinLockedUntil == nil {
		t.Fatal("expected lock")
	}

	// Jump past lock expiry.
	clk.t = locked.PinLockedUntil.Add(time.Second)

	if err := svc.Verify(ctx, userID, good, meta); err != nil {
		t.Fatalf("verify after expiry: %v", err)
	}
	u, err := users.GetByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.PinFailedCount != 0 || u.PinLockedUntil != nil {
		t.Fatalf("expected clear lockout, count=%d until=%v", u.PinFailedCount, u.PinLockedUntil)
	}
}

func TestVerifySuccessResetsFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	const good = "4829"
	svc, users, _, userID, _ := setupPINUser(t, good)
	ctx := ctxBackground()
	meta := pin.Meta{RequestID: "req-reset"}

	for i := 0; i < 3; i++ {
		_ = svc.Verify(ctx, userID, "9998", meta)
	}
	if err := svc.Verify(ctx, userID, good, meta); err != nil {
		t.Fatal(err)
	}
	u, err := users.GetByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.PinFailedCount != 0 || u.PinLockedUntil != nil {
		t.Fatalf("count=%d until=%v", u.PinFailedCount, u.PinLockedUntil)
	}
}

func TestVerifyConcurrentFailuresReachLock(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, users, _, userID, _ := setupPINUser(t, "4829")
	ctx := ctxBackground()

	const workers = 12
	var locked atomic.Int32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			err := svc.Verify(ctx, userID, "2468", pin.Meta{RequestID: "race"})
			if he, ok := httpx.AsError(err); ok && he.Code == "pin_locked" {
				locked.Add(1)
			}
		}()
	}
	wg.Wait()

	u, err := users.GetByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.PinLockedUntil == nil {
		t.Fatalf("expected lock after concurrent failures; count=%d locked_hits=%d", u.PinFailedCount, locked.Load())
	}
	if u.PinFailedCount < int32(pin.MaxFailures) {
		t.Fatalf("failed_count=%d want >= %d", u.PinFailedCount, pin.MaxFailures)
	}
}

func TestVerifyFailureAndLockoutAudit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	const good = "4829"
	const wrong = "7391"
	svc, _, pool, userID, _ := setupPINUser(t, good)
	ctx := ctxBackground()

	failIDs := make([]string, 0, pin.MaxFailures)
	for i := 0; i < pin.MaxFailures; i++ {
		id := "pin-fail-" + uuid.NewString()
		failIDs = append(failIDs, id)
		_ = svc.Verify(ctx, userID, wrong, pin.Meta{
			IP:        "10.0.0.8",
			UserAgent: "pin-audit-test",
			RequestID: id,
		})
	}

	// Locked retry must not write another failure/lockout row.
	lockedRetryID := "pin-locked-retry-" + uuid.NewString()
	err := svc.Verify(ctx, userID, good, pin.Meta{RequestID: lockedRetryID})
	if he, ok := httpx.AsError(err); !ok || he.Code != "pin_locked" {
		t.Fatalf("locked retry: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT action, summary::text, request_id
		FROM audit_logs
		WHERE request_id = ANY($1::text[])
		ORDER BY created_at ASC
	`, append(failIDs, lockedRetryID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type auditHit struct {
		action    string
		summary   string
		requestID string
	}
	var hits []auditHit
	for rows.Next() {
		var h auditHit
		if err := rows.Scan(&h.action, &h.summary, &h.requestID); err != nil {
			t.Fatal(err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	var failures, lockouts int
	for _, h := range hits {
		if h.requestID == lockedRetryID {
			t.Fatalf("locked retry must not audit: %+v", h)
		}
		if strings.Contains(h.summary, wrong) || strings.Contains(h.summary, good) {
			t.Fatalf("PIN leaked into audit summary: %s", h.summary)
		}
		var summary map[string]any
		if err := json.Unmarshal([]byte(h.summary), &summary); err != nil {
			t.Fatal(err)
		}
		for _, secretKey := range []string{"pin", "old_pin", "pin_hash", "password"} {
			if _, ok := summary[secretKey]; ok {
				t.Fatalf("secret key %q in summary %s", secretKey, h.summary)
			}
		}
		switch h.action {
		case audit.ActionPINFailure:
			failures++
		case audit.ActionPINLockout:
			lockouts++
		default:
			t.Fatalf("unexpected action %q", h.action)
		}
	}
	if failures != pin.MaxFailures {
		t.Fatalf("pin.failure rows=%d want %d", failures, pin.MaxFailures)
	}
	if lockouts != 1 {
		t.Fatalf("pin.lockout rows=%d want 1", lockouts)
	}
}
