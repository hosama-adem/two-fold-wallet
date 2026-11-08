package migrate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSplitStatementsKeepsSQLAfterLeadingComment(t *testing.T) {
	body := `
-- Row triggers do not fire for TRUNCATE.
-- The function is created in an earlier migration.
CREATE TRIGGER audit_logs_no_truncate
    BEFORE TRUNCATE ON audit_logs
    FOR EACH STATEMENT
    EXECUTE PROCEDURE audit_logs_append_only();
-- nothing to see here
`
	stmts := splitStatements(body)
	if len(stmts) != 1 {
		t.Fatalf("got %d stmts: %#v", len(stmts), stmts)
	}
	if !strings.HasPrefix(stmts[0], "CREATE TRIGGER") {
		t.Fatalf("statement swallowed by comment: %q", stmts[0])
	}
}

func TestAuditTruncateMigrationIsExecutable(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "00004_audit_logs_no_truncate.sql"))
	up, down, err := splitGoose(path)
	if err != nil {
		t.Fatal(err)
	}
	upStmts := splitStatements(up)
	if len(upStmts) != 1 || !strings.Contains(upStmts[0], "BEFORE TRUNCATE") {
		t.Fatalf("up statements: %#v", upStmts)
	}
	downStmts := splitStatements(down)
	if len(downStmts) != 1 || !strings.Contains(downStmts[0], "DROP TRIGGER") {
		t.Fatalf("down statements: %#v", downStmts)
	}
}

func TestSplitStatementsHonorsGooseBlocks(t *testing.T) {
	body := `
CREATE TABLE t (id INT);
-- +goose StatementBegin
CREATE FUNCTION f() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'nope';
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER tr BEFORE UPDATE ON t FOR EACH ROW EXECUTE PROCEDURE f();
`
	stmts := splitStatements(body)
	if len(stmts) != 3 {
		t.Fatalf("got %d stmts: %#v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[1], "RAISE EXCEPTION") {
		t.Fatalf("function body split incorrectly: %q", stmts[1])
	}
}

func TestListMigrationsRejectsInvalidAndDuplicateVersions(t *testing.T) {
	tests := []struct {
		name  string
		files []string
	}{
		{
			name:  "invalid version",
			files: []string{"not-a-migration.sql"},
		},
		{
			name:  "duplicate version",
			files: []string{"00001_users.sql", "00001_more_users.sql"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
					t.Fatalf("write migration: %v", err)
				}
			}
			if _, err := listMigrations(dir); err == nil {
				t.Fatal("listMigrations returned nil error")
			}
		})
	}
}
