package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

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
