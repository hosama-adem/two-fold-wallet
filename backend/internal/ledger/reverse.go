package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
)

// Reverse posts a new journal that undoes journalID. Every posting keeps its
// amount and flips direction. The new row's reverses_journal_id points at
// the original, and that column is unique, so a second reverse fails.
//
// reason is stored as the reversal's description. The original journal is
// not modified. A reversal can itself be reversed; that is a new journal,
// not an edit.
func Reverse(ctx context.Context, tx pgx.Tx, journalID uuid.UUID, reason string) (uuid.UUID, error) {
	if tx == nil {
		return uuid.Nil, errors.New("ledger: Reverse requires a transaction")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return uuid.Nil, ErrReasonRequired
	}
	if journalID == uuid.Nil {
		return uuid.Nil, ErrJournalNotFound
	}

	q := ledgerdb.New(tx)
	original, err := q.GetJournalEntryForUpdate(ctx, journalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrJournalNotFound
		}
		return uuid.Nil, fmt.Errorf("ledger: lock journal: %w", err)
	}
	if _, err := q.FindReversalOfJournal(ctx, &journalID); err == nil {
		return uuid.Nil, ErrAlreadyReversed
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("ledger: lookup reversal: %w", err)
	}

	postings, err := q.ListPostingsByJournalID(ctx, journalID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ledger: load postings: %w", err)
	}
	lines := make([]Line, 0, len(postings))
	for _, posting := range postings {
		flipped, err := opposite(Side(posting.Direction))
		if err != nil {
			return uuid.Nil, err
		}
		amount, err := NewMoney(posting.AmountMinor, posting.Currency)
		if err != nil {
			return uuid.Nil, fmt.Errorf("ledger: reversal amount: %w", err)
		}
		line, err := NewLine(posting.AccountID, flipped, amount)
		if err != nil {
			return uuid.Nil, err
		}
		lines = append(lines, line)
	}

	return writeMovement(ctx, tx, movement{
		kind:              KindReversal,
		lines:             lines,
		description:       reason,
		transactionID:     original.TransactionID,
		reversesJournalID: &journalID,
	})
}

func opposite(direction Side) (Side, error) {
	switch direction {
	case Debit:
		return Credit, nil
	case Credit:
		return Debit, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownDirection, direction)
	}
}
