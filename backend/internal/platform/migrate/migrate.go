package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Goose-compatible SQL migrations (-- +goose Up / -- +goose Down).
// Kept intentionally small so money-path dependencies stay lean.

const (
	gooseTable   = "goose_db_version"
	advisoryLock = 7242019345621 // arbitrary stable key for migrate mutex
)

// Up applies all pending migrations in dir (lexicographic order).
func Up(databaseURL, dir string) error {
	return withLock(databaseURL, func(db *sql.DB) error {
		if err := ensureVersionTable(db); err != nil {
			return err
		}

		files, err := listMigrations(dir)
		if err != nil {
			return err
		}

		applied, err := appliedVersions(db)
		if err != nil {
			return err
		}

		for _, f := range files {
			version, err := versionFromName(f)
			if err != nil {
				return err
			}
			if applied[version] {
				continue
			}
			upSQL, _, err := splitGoose(filepath.Join(dir, f))
			if err != nil {
				return err
			}
			if err := execMigration(db, version, f, upSQL, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// Down rolls back the most recently applied migration.
func Down(databaseURL, dir string) error {
	return withLock(databaseURL, func(db *sql.DB) error {
		if err := ensureVersionTable(db); err != nil {
			return err
		}

		var version int64
		var fileName string
		err := db.QueryRow(`
			SELECT version_id, source
			FROM `+gooseTable+`
			WHERE version_id > 0
			ORDER BY version_id DESC
			LIMIT 1`).Scan(&version, &fileName)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lookup latest migration: %w", err)
		}

		path := filepath.Join(dir, fileName)
		if _, err := os.Stat(path); err != nil {
			files, listErr := listMigrations(dir)
			if listErr != nil {
				return listErr
			}
			path = ""
			for _, f := range files {
				candidateVersion, versionErr := versionFromName(f)
				if versionErr != nil {
					return versionErr
				}
				if candidateVersion == version {
					path = filepath.Join(dir, f)
					fileName = f
					break
				}
			}
			if path == "" {
				return fmt.Errorf("migration file for version %d not found", version)
			}
		}

		_, downSQL, err := splitGoose(path)
		if err != nil {
			return err
		}
		return execMigration(db, version, fileName, downSQL, false)
	})
}

func withLock(databaseURL string, fn func(*sql.DB) error) error {
	db, err := open(databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryLock); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLock)
	}()

	return fn(db)
}

func open(databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return db, nil
}

func ensureVersionTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ` + gooseTable + ` (
			id SERIAL PRIMARY KEY,
			version_id BIGINT NOT NULL,
			is_applied BOOLEAN NOT NULL,
			tstamp TIMESTAMPTZ NOT NULL DEFAULT now(),
			source TEXT NOT NULL DEFAULT ''
		)`)
	return err
}

func appliedVersions(db *sql.DB) (map[int64]bool, error) {
	rows, err := db.Query(`SELECT version_id FROM ` + gooseTable + ` WHERE is_applied = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]bool{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func listMigrations(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var files []string
	versions := make(map[int64]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := versionFromName(e.Name())
		if err != nil {
			return nil, err
		}
		if previous, ok := versions[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, e.Name())
		}
		versions[version] = e.Name()
		files = append(files, e.Name())
	}
	sort.Strings(files)
	return files, nil
}

func versionFromName(name string) (int64, error) {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	num, _, ok := strings.Cut(base, "_")
	if !ok || num == "" {
		return 0, fmt.Errorf("invalid migration filename %q: expected <positive-version>_<name>.sql", name)
	}
	version, err := strconv.ParseInt(num, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid migration filename %q: version must be a positive integer", name)
	}
	return version, nil
}

func splitGoose(path string) (up, down string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	content := string(raw)
	upMarker := "-- +goose Up"
	downMarker := "-- +goose Down"

	upIdx := strings.Index(content, upMarker)
	downIdx := strings.Index(content, downMarker)
	if upIdx < 0 {
		return "", "", fmt.Errorf("%s: missing %q section", path, upMarker)
	}
	if downIdx < 0 {
		return "", "", fmt.Errorf("%s: missing %q section", path, downMarker)
	}
	if downIdx < upIdx {
		return "", "", fmt.Errorf("%s: Down section before Up", path)
	}

	up = strings.TrimSpace(content[upIdx+len(upMarker) : downIdx])
	down = strings.TrimSpace(content[downIdx+len(downMarker):])
	return up, down, nil
}

func execMigration(db *sql.DB, version int64, source, sqlBody string, applying bool) error {
	// Extensions must run outside the table DDL transaction.
	extSQL, restSQL := partitionExtensionStmts(sqlBody)
	for _, stmt := range extSQL {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("execute %s: %w\nstatement: %s", source, err, stmt)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range restSQL {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("execute %s: %w\nstatement: %s", source, err, stmt)
		}
	}

	if applying {
		if _, err := tx.Exec(
			`INSERT INTO `+gooseTable+` (version_id, is_applied, source) VALUES ($1, true, $2)`,
			version, source,
		); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(
			`DELETE FROM `+gooseTable+` WHERE version_id = $1`,
			version,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func partitionExtensionStmts(body string) (ext []string, rest []string) {
	for _, stmt := range splitStatements(body) {
		upper := strings.ToUpper(stmt)
		if strings.HasPrefix(upper, "CREATE EXTENSION") || strings.HasPrefix(upper, "DROP EXTENSION") {
			ext = append(ext, stmt)
			continue
		}
		rest = append(rest, stmt)
	}
	return ext, rest
}

func splitStatements(body string) []string {
	const (
		beginMark = "-- +goose StatementBegin"
		endMark   = "-- +goose StatementEnd"
	)
	var out []string
	rest := body
	for {
		beginIdx := strings.Index(rest, beginMark)
		if beginIdx < 0 {
			out = append(out, splitSimpleStatements(rest)...)
			break
		}
		out = append(out, splitSimpleStatements(rest[:beginIdx])...)
		afterBegin := rest[beginIdx+len(beginMark):]
		endIdx := strings.Index(afterBegin, endMark)
		if endIdx < 0 {
			// Unclosed block — treat remainder as one statement.
			stmt := strings.TrimSpace(afterBegin)
			if stmt != "" {
				out = append(out, stmt)
			}
			break
		}
		stmt := strings.TrimSpace(afterBegin[:endIdx])
		if stmt != "" {
			out = append(out, stmt)
		}
		rest = afterBegin[endIdx+len(endMark):]
	}
	return out
}

func splitSimpleStatements(body string) []string {
	var out []string
	for _, stmt := range strings.Split(body, ";") {
		// A leading comment must not hide the SQL that follows it.
		// 00004 is only a CREATE TRIGGER under a header comment; skipping
		// that chunk recorded the migration and left TRUNCATE allowed.
		stmt = strings.TrimSpace(stripLeadingComments(stmt))
		if stmt == "" {
			continue
		}
		out = append(out, stmt)
	}
	return out
}

// stripLeadingComments drops full-line -- comments and /* */ blocks that
// sit in front of a statement. A line comment with no newline and no SQL
// after it drops the whole chunk.
func stripLeadingComments(stmt string) string {
	for {
		stmt = strings.TrimSpace(stmt)
		switch {
		case stmt == "":
			return ""
		case strings.HasPrefix(stmt, "--"):
			newline := strings.IndexByte(stmt, '\n')
			if newline < 0 {
				return ""
			}
			stmt = stmt[newline+1:]
		case strings.HasPrefix(stmt, "/*"):
			end := strings.Index(stmt, "*/")
			if end < 0 {
				return ""
			}
			stmt = stmt[end+2:]
		default:
			return stmt
		}
	}
}
