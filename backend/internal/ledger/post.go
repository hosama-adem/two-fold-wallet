package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
)

type postingDraft struct {
	line              Line
	balanceAfterMinor int64
	accountVersion    int64
}

// Post writes a journal and its postings inside the caller's transaction.
// It is the only function in this codebase allowed to move money.
//
// Validation runs in Go first (empty, currency, balance). Postgres still
// rejects an unbalanced journal at COMMIT via the deferred constraint trigger.
// Cached account balances and versions are not updated here — that is Day 11.
func Post(ctx context.Context, tx pgx.Tx, journal Journal) (uuid.UUID, error) {
	if tx == nil {
		return uuid.Nil, errors.New("ledger: Post requires a transaction")
	}
	if err := journal.validate(); err != nil {
		return uuid.Nil, err
	}

	q := ledgerdb.New(tx)

	type running struct {
		currency      string
		normal        Side
		balanceMinor  int64
		version       int64
		allowNegative bool
	}
	accounts := make(map[uuid.UUID]*running, len(journal.lines))
	drafts := make([]postingDraft, 0, len(journal.lines))

	for _, line := range journal.lines {
		state, ok := accounts[line.accountID]
		if !ok {
			acct, err := q.GetAccountByID(ctx, line.accountID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return uuid.Nil, fmt.Errorf("ledger: account %s not found", line.accountID)
				}
				return uuid.Nil, fmt.Errorf("ledger: load account: %w", err)
			}
			if acct.Currency != line.amount.currency {
				return uuid.Nil, ErrCurrencyMismatch
			}
			state = &running{
				currency:      acct.Currency,
				normal:        Side(acct.NormalBalance),
				balanceMinor:  acct.BalanceMinor,
				version:       acct.Version,
				allowNegative: acct.AllowNegative,
			}
			accounts[line.accountID] = state
		} else if state.currency != line.amount.currency {
			return uuid.Nil, ErrCurrencyMismatch
		}

		delta := BalanceDelta(state.normal, line.direction, line.amount.amountMinor)
		next, ok := addMinor(state.balanceMinor, delta)
		if !ok {
			return uuid.Nil, fmt.Errorf("ledger: balance overflow on account %s", line.accountID)
		}
		if !state.allowNegative && next < 0 {
			return uuid.Nil, fmt.Errorf("ledger: account %s would go negative", line.accountID)
		}
		state.balanceMinor = next
		drafts = append(drafts, postingDraft{
			line:              line,
			balanceAfterMinor: next,
			accountVersion:    state.version,
		})
	}

	journalID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("ledger: journal id: %w", err)
	}

	var description *string
	if journal.description != "" {
		description = &journal.description
	}

	if _, err := q.InsertJournalEntry(ctx, ledgerdb.InsertJournalEntryParams{
		ID:            journalID,
		TransactionID: journal.transactionID,
		Kind:          string(journal.kind),
		Description:   description,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("ledger: insert journal: %w", err)
	}

	for _, draft := range drafts {
		postingID, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, fmt.Errorf("ledger: posting id: %w", err)
		}
		if _, err := q.InsertPosting(ctx, ledgerdb.InsertPostingParams{
			ID:                postingID,
			JournalID:         journalID,
			AccountID:         draft.line.accountID,
			Direction:         string(draft.line.direction),
			AmountMinor:       draft.line.amount.amountMinor,
			Currency:          draft.line.amount.currency,
			BalanceAfterMinor: draft.balanceAfterMinor,
			AccountVersion:    draft.accountVersion,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("ledger: insert posting: %w", err)
		}
	}

	return journalID, nil
}
