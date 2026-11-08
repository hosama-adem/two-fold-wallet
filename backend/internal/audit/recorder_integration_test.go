package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestAuditLogAppendOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	rec := audit.NewRecorder(pool)
	ctx := context.Background()

	requestID := "audit-append-" + uuid.NewString()
	if err := rec.Record(ctx, audit.Entry{
		ActorKind: audit.ActorSystem,
		Action:    "test.append",
		Summary:   map[string]any{"ok": true},
		RequestID: requestID,
	}); err != nil {
		t.Fatal(err)
	}

	var id string
	var action string
	if err := pool.QueryRow(ctx, `
		SELECT id::text, action
		FROM audit_logs
		WHERE request_id = $1
	`, requestID).Scan(&id, &action); err != nil {
		t.Fatal(err)
	}
	if action != "test.append" {
		t.Fatalf("action=%s", action)
	}

	// A failed statement aborts the transaction. Savepoints let the next
	// mutation attempt run, and rolling back keeps a missing trigger from
	// truncating the shared database.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	assertRejected := func(stmt string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, "SAVEPOINT audit_mut"); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(ctx, stmt, args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Fatalf("%s: got %v", stmt, err)
		}
		if pgErr.Code != "23000" || pgErr.Message != "audit_logs is append-only" {
			t.Fatalf("%s: code=%s message=%q", stmt, pgErr.Code, pgErr.Message)
		}
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT audit_mut"); err != nil {
			t.Fatal(err)
		}
	}

	assertRejected(`UPDATE audit_logs SET action = 'mutated' WHERE id = $1`, id)
	assertRejected(`DELETE FROM audit_logs WHERE id = $1`, id)
	assertRejected(`TRUNCATE audit_logs`)

	var after string
	if err := tx.QueryRow(ctx, `SELECT action FROM audit_logs WHERE id = $1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != "test.append" {
		t.Fatalf("row changed: action=%s", after)
	}
}
