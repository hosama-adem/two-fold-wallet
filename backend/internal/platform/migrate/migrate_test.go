package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
