package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
)

// BalanceDelta is how a posting changes an account's cached balance.
// Same side as the account's normal balance raises the cache; the opposite
// side lowers it. Post adds this into accounts.balance_minor and stores the
// result on the posting as balance_after_minor.
func BalanceDelta(normal, direction Side, amountMinor int64) int64 {
	if IncreasesBalance(normal, direction) {
		return amountMinor
	}
	return -amountMinor
}

// RecomputeBalance rebuilds one account's balance from its postings.
// The drift monitor compares this to accounts.balance_minor. An account with
// no postings recomputes to 0. A missing account is an error.
func RecomputeBalance(ctx context.Context, db ledgerdb.DBTX, accountID uuid.UUID) (int64, error) {
	if db == nil {
		return 0, errors.New("ledger: RecomputeBalance requires a database")
	}
	if accountID == uuid.Nil {
		return 0, ErrInvalidAccountID
	}
	balance, err := ledgerdb.New(db).RecomputeAccountBalance(ctx, accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("ledger: account %s not found", accountID)
		}
		return 0, fmt.Errorf("ledger: recompute balance: %w", err)
	}
	return balance, nil
}
