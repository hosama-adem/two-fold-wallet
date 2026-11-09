package ledger_test

import (
	"context"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestJournalReversalLink(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	original := insertAdjustmentJournal(t, ctx, pool)

	reversal := newID()
	_, err := pool.Exec(ctx, `
		INSERT INTO journal_entries (id, kind, reverses_journal_id, description)
		VALUES ($1, 'reversal', $2, 'undo the probe')
	`, reversal, original)
	if err != nil {
		t.Fatalf("first reversal: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO journal_entries (id, kind, reverses_journal_id)
		VALUES ($1, 'reversal', $2)
	`, newID(), original)
	requireViolation(t, err, "23505", "journal_entries_reverses_unique")

	_, err = pool.Exec(ctx, `
		INSERT INTO journal_entries (id, kind, reverses_journal_id)
		VALUES ($1, 'adjustment', $2)
	`, newID(), original)
	requireViolation(t, err, "23514", "journal_entries_reversal_link")

	_, err = pool.Exec(ctx, `
		INSERT INTO journal_entries (id, kind)
		VALUES ($1, 'reversal')
	`, newID())
	requireViolation(t, err, "23514", "journal_entries_reversal_link")

	// An INSERT that points a row at its own id dies on the foreign key,
	// because the new row is not visible yet. UPDATE used to be how the
	// self-link CHECK was reached. History is append-only now, so the
	// update is rejected before that CHECK runs.
	_, err = pool.Exec(ctx, `
		UPDATE journal_entries
		SET reverses_journal_id = id
		WHERE id = $1
	`, reversal)
	requireAppendOnly(t, err, "journal_entries")

	got, err := ledgerdb.New(pool).GetJournalEntryByID(ctx, reversal)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReversesJournalID == nil || *got.ReversesJournalID != original {
		t.Fatalf("reversal link = %v, want %s", got.ReversesJournalID, original)
	}
}

func TestJournalKindMustBeKnown(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO journal_entries (id, kind)
		VALUES ($1, 'gift')
	`, newID())
	requireViolation(t, err, "23514", "journal_entries_kind_known")
}
