package limit_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/user"
)

func TestCheckPerTxnBoundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	uid := seedUser(t, ctx, pool)

	const cap int64 = 100_000 // tier 0 per_txn
	for _, tc := range []struct {
		name    string
		amount  int64
		wantErr bool
	}{
		{"limit-1", cap - 1, false},
		{"limit", cap, false},
		{"limit+1", cap + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := limit.Check(ctx, pool, uid, "p2p", tc.amount)
			if tc.wantErr {
				assertLimit(t, err, limit.LimitPerTxn)
				return
			}
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
		})
	}
}

func TestCheckReceiveBalanceCap(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	uid := seedUser(t, ctx, pool)

	const maxBal int64 = 500_000 // tier 0
	balance := maxBal - 1_000

	if err := limit.CheckReceive(ctx, pool, uid, balance, 1_000); err != nil {
		t.Fatalf("at cap: %v", err)
	}
	err := limit.CheckReceive(ctx, pool, uid, balance, 1_001)
	assertLimit(t, err, limit.LimitMaxBalance)
}

func TestCheckCashOutBlockedAtTier0(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	uid := seedUser(t, ctx, pool)

	err := limit.Check(ctx, pool, uid, limit.KindCashOut, 100)
	assertLimit(t, err, limit.LimitCashOut)
}

func assertLimit(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected limit_exceeded")
	}
	var he *httpx.Error
	if !errors.As(err, &he) {
		t.Fatalf("err=%v", err)
	}
	if he.Code != limit.CodeLimitExceeded {
		t.Fatalf("code=%s want %s detail=%s", he.Code, limit.CodeLimitExceeded, he.Detail)
	}
	if he.Limit == nil || *he.Limit != want {
		t.Fatalf("limit=%v want %s", he.Limit, want)
	}
	if he.RemainingMinor == nil {
		t.Fatal("remaining_minor missing")
	}
}

func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	repo := user.NewRepository(pool)
	u, err := repo.Create(ctx, user.CreateParams{
		PhoneE164:    uniquePhone(),
		DisplayName:  "Limit Tester",
		PasswordHash: "x",
		KYCTier:      0,
		Status:       "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func uniquePhone() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("+2519%08d", n.Int64())
}
