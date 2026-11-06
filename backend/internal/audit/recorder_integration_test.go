package audit_test

import (
	"context"
	"testing"

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
	if err := rec.Record(ctx, audit.Entry{
		ActorKind: audit.ActorSystem,
		Action:    "test.append",
		Summary:   map[string]any{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}

	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM audit_logs ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	_, err := pool.Exec(ctx, `UPDATE audit_logs SET action = 'mutated' WHERE id = $1`, id)
	if err == nil {
		t.Fatal("expected UPDATE to fail")
	}
	_, err = pool.Exec(ctx, `DELETE FROM audit_logs WHERE id = $1`, id)
	if err == nil {
		t.Fatal("expected DELETE to fail")
	}
}
