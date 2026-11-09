package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
)

type postingDraft struct {
	line              Line
	balanceAfterMinor int64
	accountVersion    int64
}

type accountState struct {
	currency      string
	normal        Side
	balanceMinor  int64
	version       int64
	loadedVersion int64
	allowNegative bool
}

// movement is one journal Post or Reverse is about to commit.
type movement struct {
	kind              Kind
	lines             []Line
	description       string
	transactionID     *uuid.UUID
	reversesJournalID *uuid.UUID
}

// Post writes a journal and its postings inside the caller's transaction.
// It is the only function in this codebase allowed to move money, other than
// Reverse, which posts the undoing journal.
//
// Accounts are locked in id order before their balances are read. The cache
// update then requires the version that was locked. A path that forgets the
// lock fails that predicate instead of overwriting a newer balance.
func Post(ctx context.Context, tx pgx.Tx, journal Journal) (uuid.UUID, error) {
	if tx == nil {
		return uuid.Nil, errors.New("ledger: Post requires a transaction")
	}
	if err := journal.validate(); err != nil {
		return uuid.Nil, err
	}
	return writeMovement(ctx, tx, movement{
		kind:          journal.kind,
		lines:         journal.lines,
		description:   journal.description,
		transactionID: journal.transactionID,
	})
}

func writeMovement(ctx context.Context, tx pgx.Tx, m movement) (uuid.UUID, error) {
	if m.kind == KindReversal {
		if m.reversesJournalID == nil || *m.reversesJournalID == uuid.Nil {
			return uuid.Nil, ErrReversalDirect
		}
	} else if m.reversesJournalID != nil {
		return uuid.Nil, ErrReversalDirect
	}

	probe := Journal{kind: KindAdjustment, lines: m.lines}
	if err := probe.validateLines(); err != nil {
		return uuid.Nil, err
	}

	q := ledgerdb.New(tx)
	states, err := lockAccounts(ctx, q, m.lines)
	if err != nil {
		return uuid.Nil, err
	}
	drafts, err := applyLines(states, m.lines)
	if err != nil {
		return uuid.Nil, err
	}

	journalID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("ledger: journal id: %w", err)
	}
	if m.reversesJournalID != nil && *m.reversesJournalID == journalID {
		return uuid.Nil, ErrReversalDirect
	}

	if m.kind == KindReversal {
		_, err = q.InsertReversalJournal(ctx, ledgerdb.InsertReversalJournalParams{
			ID:                journalID,
			TransactionID:     m.transactionID,
			ReversesJournalID: m.reversesJournalID,
			Description:       optionalText(m.description),
		})
	} else {
		_, err = q.InsertJournalEntry(ctx, ledgerdb.InsertJournalEntryParams{
			ID:            journalID,
			TransactionID: m.transactionID,
			Kind:          string(m.kind),
			Description:   optionalText(m.description),
		})
	}
	if err != nil {
		if isReversalConflict(err) {
			return uuid.Nil, ErrAlreadyReversed
		}
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

	if err := persistBalances(ctx, q, states); err != nil {
		return uuid.Nil, err
	}
	return journalID, nil
}

// lockAccounts takes FOR UPDATE in sorted id order. Two journals that share
// accounts then cannot each hold one row and wait for the other.
//
// Every account in the journal is locked, including system accounts. Skipping
// those hot rows would leave balance_after_minor computed from a stale cache.
// The cache stays provable on every account this way.
func lockAccounts(ctx context.Context, q *ledgerdb.Queries, lines []Line) (map[uuid.UUID]*accountState, error) {
	ids := accountIDs(lines)
	states := make(map[uuid.UUID]*accountState, len(ids))
	for _, id := range ids {
		acct, err := q.GetAccountForUpdate(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("ledger: account %s not found", id)
			}
			return nil, fmt.Errorf("ledger: lock account: %w", err)
		}
		states[id] = &accountState{
			currency:      acct.Currency,
			normal:        Side(acct.NormalBalance),
			balanceMinor:  acct.BalanceMinor,
			version:       acct.Version,
			loadedVersion: acct.Version,
			allowNegative: acct.AllowNegative,
		}
	}
	return states, nil
}

// applyLines walks the journal in caller order. balance_after_minor is the
// running balance after each line, so two lines on one account (a sale and
// its fee) stay distinguishable. The non-negative rule looks at the balance
// after the whole journal: the lines commit together, and the café journal
// debits the fee before it credits the sale.
func applyLines(states map[uuid.UUID]*accountState, lines []Line) ([]postingDraft, error) {
	drafts := make([]postingDraft, 0, len(lines))
	for _, line := range lines {
		state, ok := states[line.accountID]
		if !ok {
			return nil, fmt.Errorf("ledger: account %s not found", line.accountID)
		}
		if state.currency != line.amount.currency {
			return nil, ErrCurrencyMismatch
		}
		delta := BalanceDelta(state.normal, line.direction, line.amount.amountMinor)
		next, ok := addMinor(state.balanceMinor, delta)
		if !ok {
			return nil, fmt.Errorf("ledger: balance overflow on account %s", line.accountID)
		}
		nextVersion, ok := addMinor(state.version, 1)
		if !ok {
			return nil, fmt.Errorf("ledger: version overflow on account %s", line.accountID)
		}
		state.balanceMinor = next
		state.version = nextVersion
		drafts = append(drafts, postingDraft{
			line:              line,
			balanceAfterMinor: next,
			accountVersion:    nextVersion,
		})
	}
	for id, state := range states {
		if !state.allowNegative && state.balanceMinor < 0 {
			return nil, fmt.Errorf("%w: %s", ErrNegativeBalance, id)
		}
	}
	return drafts, nil
}

func persistBalances(ctx context.Context, q *ledgerdb.Queries, states map[uuid.UUID]*accountState) error {
	ids := make([]uuid.UUID, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int {
		return bytes.Compare(a[:], b[:])
	})
	for _, id := range ids {
		state := states[id]
		n, err := q.UpdateAccountBalance(ctx, ledgerdb.UpdateAccountBalanceParams{
			ID:              id,
			BalanceMinor:    state.balanceMinor,
			NextVersion:     state.version,
			ExpectedVersion: state.loadedVersion,
		})
		if err != nil {
			return fmt.Errorf("ledger: update balance: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("%w: %s", ErrVersionConflict, id)
		}
	}
	return nil
}

func accountIDs(lines []Line) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(lines))
	ids := make([]uuid.UUID, 0, len(lines))
	for _, line := range lines {
		if _, ok := seen[line.accountID]; ok {
			continue
		}
		seen[line.accountID] = struct{}{}
		ids = append(ids, line.accountID)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int {
		return bytes.Compare(a[:], b[:])
	})
	return ids
}

func optionalText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func isReversalConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "journal_entries_reverses_unique"
}
