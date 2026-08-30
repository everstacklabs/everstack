package migrations

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"database/sql"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// Conventions
// - Migration files live under internal/database/migrations/sql/{dialect}/
// - Folder style: {name}_{YYYYMMDDHHMMSS}/up.sql and down.sql
// - Legacy flat files supported: {name}_{YYYYMMDDHHMMSS}.up.sql and .down.sql
// - A generic set can live under internal/database/migrations/sql/common/

var (
	downSuffix    = ".down.sql"
	fileNameRegex = regexp.MustCompile(`^(?P<name>.+)_(?P<ts>\d{14})\.(up|down)\.sql$`)
	dirNameRegex  = regexp.MustCompile(`^(?P<name>.+)_(?P<ts>\d{14})$`)
)

type Direction string

const (
	DirUp   Direction = "up"
	DirDown Direction = "down"
)

type Migration struct {
	Name      string
	Version   int64
	Direction Direction
	Path      string
	SQL       string
}

// Ensure ensures the schema_migrations table exists, validates, and applies all pending migrations (common + dialect-specific).
func Ensure(ctx context.Context, db *sqlx.DB, dialect string) error {
	if db == nil {
		return errors.New("nil db")
	}
	if err := ensureMigrationsTable(ctx, db, dialect); err != nil {
		return err
	}
	// Validate common and dialect before applying
	if err := Validate(ctx, db, "common"); err != nil {
		return fmt.Errorf("migrations validation failed (common): %w", err)
	}
	if err := Validate(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrations validation failed (%s): %w", dialect, err)
	}
	// Apply common first, then dialect-specific
	if err := MigrateUp(ctx, db, "common"); err != nil {
		return err
	}
	return MigrateUp(ctx, db, dialect)
}

// EnsureForService runs service-scoped migrations using base path services/{service}/internal/database/migrations/{dialect}
func EnsureForService(ctx context.Context, db *sqlx.DB, dialect, service string) error {
	if db == nil {
		return errors.New("nil db")
	}
	if strings.TrimSpace(service) == "" {
		return errors.New("service name is required")
	}
	if err := ensureMigrationsTable(ctx, db, dialect); err != nil {
		return err
	}
	// Service migrations don't use "common"; only service-local folders
	if err := ValidateForService(ctx, db, dialect, service); err != nil {
		return fmt.Errorf("migrations validation failed (%s:%s): %w", dialect, service, err)
	}
	return MigrateUpForService(ctx, db, dialect, service)
}

// EnsureOnSchema runs gateway migrations inside a specific Postgres schema.
// It acquires a dedicated connection from the pool, sets search_path to the
// target schema, creates a local schema_migrations table there, then applies
// pending common + postgres migrations. This is used by the cloud orchestrator
// to provision tenant schemas.
func EnsureOnSchema(ctx context.Context, db *sqlx.DB, schemaName string) error {
	if db == nil {
		return errors.New("nil db")
	}
	conn, err := db.Connx(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer func() {
		// Reset search_path before returning the connection to the pool.
		// Without this, the pooled connection retains the tenant search_path
		// and subsequent queries on the same pool may resolve to the wrong schema.
		_, _ = conn.ExecContext(ctx, "SET search_path TO everstack, public")
		conn.Close()
	}()

	// Set search_path so all unqualified table names resolve to the tenant schema.
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET search_path TO %s, public", schemaName)); err != nil {
		return fmt.Errorf("set search_path to %s: %w", schemaName, err)
	}

	// Create schema_migrations in the tenant schema (unqualified).
	if _, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    dialect    TEXT    NOT NULL,
    version    BIGINT  NOT NULL,
    name       TEXT    NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dialect, version)
);`); err != nil {
		return fmt.Errorf("create schema_migrations in %s: %w", schemaName, err)
	}

	// Wrap the connection as a sqlx.DB-like interface by using the conn's
	// underlying *sql.Conn through a single-connection pool trick.
	// Since sqlx.Conn implements the same query methods, we create a thin
	// wrapper DB that always uses this connection.
	wrapper := &connDB{conn: conn}

	// Load applied migrations from the tenant's schema_migrations table.
	applied, err := loadAppliedOnConn(ctx, wrapper)
	if err != nil {
		return fmt.Errorf("load applied migrations for %s: %w", schemaName, err)
	}

	// Apply common migrations first, then postgres-specific.
	for _, dialect := range []string{"common", "postgres"} {
		ups, err := loadMigrationsFromDisk(dialect, DirUp)
		if err != nil {
			return fmt.Errorf("load %s migrations: %w", dialect, err)
		}
		sort.Slice(ups, func(i, j int) bool { return ups[i].Version < ups[j].Version })

		for _, m := range ups {
			key := fmt.Sprintf("%s:%d", dialectKey(dialect), m.Version)
			if _, ok := applied[key]; ok {
				continue
			}
			// Skip system-level migrations in tenant schemas.
			// These operate on the global "system" schema (e.g. instances table)
			// and must only run once in the default/everstack schema.
			if isTenantExcludedMigration(m.Name) {
				continue
			}
			logger.Infof("migrations[%s]: applying %s_%014d", schemaName, m.Name, m.Version)
			if err := execSQLOnConn(ctx, wrapper, m.SQL); err != nil {
				return fmt.Errorf("apply migration %s in %s: %w", m.Name, schemaName, err)
			}
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO schema_migrations(dialect, version, name) VALUES ($1,$2,$3)`,
				dialectKey(dialect), m.Version, m.Name,
			); err != nil {
				return fmt.Errorf("record migration %s in %s: %w", m.Name, schemaName, err)
			}
			applied[key] = struct{}{}
		}
	}
	return nil
}

// connDB wraps a *sqlx.Conn to satisfy the execer interface used by migration helpers.
type connDB struct {
	conn *sqlx.Conn
}

func (c *connDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return c.conn.ExecContext(ctx, query, args...)
}

func (c *connDB) QueryxContext(ctx context.Context, query string, args ...interface{}) (*sqlx.Rows, error) {
	return c.conn.QueryxContext(ctx, query, args...)
}

func (c *connDB) BeginTxx(ctx context.Context, opts *sql.TxOptions) (*sqlx.Tx, error) {
	return c.conn.BeginTxx(ctx, opts)
}

func loadAppliedOnConn(ctx context.Context, db *connDB) (map[string]struct{}, error) {
	rows, err := db.QueryxContext(ctx, `SELECT dialect, version FROM schema_migrations`)
	if err != nil {
		return map[string]struct{}{}, nil // table may not exist yet
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var d string
		var v int64
		if err := rows.Scan(&d, &v); err != nil {
			return nil, err
		}
		out[fmt.Sprintf("%s:%d", d, v)] = struct{}{}
	}
	return out, rows.Err()
}

func execSQLOnConn(ctx context.Context, db *connDB, sqlStr string) error {
	stripped := stripSQLComments(sqlStr)
	parts := splitStatements(stripped)
	tx, _ := db.BeginTxx(ctx, nil)
	useTx := tx != nil
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if useTx {
			if _, err := tx.ExecContext(ctx, p); err != nil {
				_ = tx.Rollback()
				return err
			}
		} else {
			if _, err := db.ExecContext(ctx, p); err != nil {
				return err
			}
		}
	}
	if useTx {
		return tx.Commit()
	}
	return nil
}

// Validate checks migration files and DB state for issues.
// It validates that:
// - No duplicate versions exist on disk
// - For each up migration on disk, a down migration exists (folder or flat)
// - For each applied DB version, a down migration exists on disk (to allow rollback)
func Validate(ctx context.Context, db *sqlx.DB, dialect string) error {
	// gather ups
	ups, err := loadMigrationsFromDisk(dialect, DirUp)
	if err != nil {
		return err
	}
	// seen holds versions and names found on disk for this dialect's global migrations
	seen := make(map[int64]string)
	for _, m := range ups {
		if _, ok := seen[m.Version]; ok {
			return fmt.Errorf("duplicate migration version %d on disk", m.Version)
		}
		seen[m.Version] = m.Name
		// ensure matching down exists
		if _, err := findDownMigration(dialect, m.Name, m.Version); err != nil {
			return fmt.Errorf("missing down migration for %s_%014d: %w", m.Name, m.Version, err)
		}
	}
	// ensure applied have a down available
	applied, err := listAppliedDescending(ctx, db, dialect)
	if err != nil {
		return err
	}
	for _, a := range applied {
		// Only validate downs for versions that exist in the global disk set for this dialect.
		// This avoids failing when service-scoped migrations (recorded in the same table) are present.
		if diskName, ok := seen[a.Version]; !ok || diskName != a.Name {
			// Not a global migration on disk; likely service-scoped. Skip global down check.
			continue
		}
		if _, err := findDownMigration(dialect, a.Name, a.Version); err != nil {
			return fmt.Errorf("applied migration %s_%014d has no down migration available: %w", a.Name, a.Version, err)
		}
	}
	return nil
}

// ValidateForService checks migrations for a specific service path
func ValidateForService(ctx context.Context, db *sqlx.DB, dialect, service string) error {
	ups, err := loadServiceMigrationsFromDisk(service, dialect, DirUp)
	if err != nil {
		return err
	}
	seen := make(map[int64]string)
	for _, m := range ups {
		if _, ok := seen[m.Version]; ok {
			return fmt.Errorf("duplicate migration version %d on disk", m.Version)
		}
		seen[m.Version] = m.Name
		if _, err := findServiceDownMigration(service, dialect, m.Name, m.Version); err != nil {
			return fmt.Errorf("missing down migration for %s_%014d: %w", m.Name, m.Version, err)
		}
	}
	// Ensure applied have matching downs
	applied, err := listAppliedDescending(ctx, db, dialect)
	if err != nil {
		return err
	}
	for _, a := range applied {
		if _, err := findServiceDownMigration(service, dialect, a.Name, a.Version); err != nil {
			// don't hard-fail if migration belongs to another area; service sets shouldn't need global verification
			// we simply require that service's own ups have downs on disk
			continue
		}
	}
	return nil
}

// MigrateUpForService applies pending ups for a service
func MigrateUpForService(ctx context.Context, db *sqlx.DB, dialect, service string) error {
	applied, err := loadApplied(ctx, db, dialect)
	if err != nil {
		return err
	}
	ups, err := loadServiceMigrationsFromDisk(service, dialect, DirUp)
	if err != nil {
		return err
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].Version < ups[j].Version })
	appliedAny := false
	for _, m := range ups {
		key := fmt.Sprintf("%s:%d", dialectKey(dialect), m.Version)
		if _, ok := applied[key]; ok {
			continue
		}
		logger.Infof("migrations(%s/%s): applying %s_%014d (%s)", service, strings.ToLower(dialect), m.Name, m.Version, filepath.Base(m.Path))
		logger.Debugf("migrations: SQL to apply (up)\n%s", m.SQL)
		if err := execSQL(ctx, db, m.SQL); err != nil {
			return fmt.Errorf("apply migration %s failed: %w", filepath.Base(m.Path), err)
		}
		if err := recordApplied(ctx, db, dialect, m); err != nil {
			return err
		}
		appliedAny = true
	}
	if !appliedAny {
		rows, err := listAppliedDescending(ctx, db, dialect)
		if err == nil && len(rows) > 0 {
			logger.Infof("migrations(%s/%s): up-to-date; latest %s_%014d", service, strings.ToLower(dialect), rows[0].Name, rows[0].Version)
		}
	}
	return nil
}

// UpToLatest applies all missing up migrations.
func MigrateUp(ctx context.Context, db *sqlx.DB, dialect string) error {
	applied, err := loadApplied(ctx, db, dialect)
	if err != nil {
		return err
	}
	ups, err := loadMigrationsFromDisk(dialect, DirUp)
	if err != nil {
		return err
	}
	// Sort by version ascending
	sort.Slice(ups, func(i, j int) bool { return ups[i].Version < ups[j].Version })
	appliedAny := false
	for _, m := range ups {
		key := fmt.Sprintf("%s:%d", dialectKey(dialect), m.Version)
		if _, ok := applied[key]; ok {
			continue
		}
		logger.Infof("migrations: applying %s_%014d (%s) for %s", m.Name, m.Version, filepath.Base(m.Path), strings.ToLower(dialect))
		logger.Debugf("migrations: SQL to apply (up)\n%s", m.SQL)
		if err := execSQL(ctx, db, m.SQL); err != nil {
			return fmt.Errorf("apply migration %s failed: %w", filepath.Base(m.Path), err)
		}
		if err := recordApplied(ctx, db, dialect, m); err != nil {
			return err
		}
		logger.Infof("migrations: applied %s_%014d", m.Name, m.Version)
		appliedAny = true
	}
	if !appliedAny {
		rows, err := listAppliedDescending(ctx, db, dialect)
		if err == nil {
			if len(rows) > 0 {
				logger.Infof("migrations: up-to-date for %s; latest %s_%014d", strings.ToLower(dialect), rows[0].Name, rows[0].Version)
			}
		}
	}
	return nil
}

// Down rolls back the last N applied migrations for the given dialect. If steps == -1 and all=true, roll back all.
func MigrateDown(ctx context.Context, db *sqlx.DB, dialect string, steps int, all bool) error {
	if all {
		steps = -1
	}
	applied, err := listAppliedDescending(ctx, db, dialect)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		return nil
	}
	count := len(applied)
	if steps >= 0 && steps < count {
		count = steps
	}
	for i := 0; i < count; i++ {
		a := applied[i]
		// Find matching down migration file on disk
		downMig, err := findDownMigration(dialect, a.Name, a.Version)
		if err != nil {
			return err
		}
		logger.Infof("migrations: rolling back %s_%014d (%s) for %s", a.Name, a.Version, filepath.Base(downMig.Path), strings.ToLower(dialect))
		logger.Debugf("migrations: SQL to apply (down)\n%s", downMig.SQL)
		if err := execSQL(ctx, db, downMig.SQL); err != nil {
			return fmt.Errorf("rollback migration %s failed: %w", filepath.Base(downMig.Path), err)
		}
		if err := deleteApplied(ctx, db, dialect, a.Version); err != nil {
			return err
		}
	}
	return nil
}

// MigrateDownForService rolls back service-scoped migrations from services/{service}/internal/database/migrations/{dialect}
func MigrateDownForService(ctx context.Context, db *sqlx.DB, dialect, service string, steps int, all bool) error {
	if db == nil {
		return errors.New("nil db")
	}
	if strings.TrimSpace(service) == "" {
		return errors.New("service is required")
	}
	if all {
		steps = -1
	}

	// Load applied for this dialect, but we need to filter only those whose files exist under the service dir
	applied, err := listAppliedDescending(ctx, db, dialect)
	if err != nil {
		return err
	}

	// Build set of service migration versions from disk
	ups, err := loadServiceMigrationsFromDisk(service, dialect, DirUp)
	if err != nil {
		return err
	}
	svcVers := make(map[int64]struct{}, len(ups))
	for _, m := range ups {
		svcVers[m.Version] = struct{}{}
	}

	count := 0
	for _, a := range applied {
		if _, ok := svcVers[a.Version]; !ok {
			continue
		}
		downMig, err := findServiceDownMigration(service, dialect, a.Name, a.Version)
		if err != nil {
			return err
		}
		logger.Infof("migrations(%s/%s): rolling back %s_%014d (%s)", service, strings.ToLower(dialect), a.Name, a.Version, filepath.Base(downMig.Path))
		logger.Debugf("migrations: SQL to apply (down)\n%s", downMig.SQL)
		if err := execSQL(ctx, db, downMig.SQL); err != nil {
			return err
		}
		if err := deleteApplied(ctx, db, dialect, a.Version); err != nil {
			return err
		}
		count++
		if steps >= 0 && count >= steps {
			break
		}
	}
	return nil
}

// New creates folder-style up/down migration files for a dialect with a timestamped name.
func New(dialect, name string) (upPath, downPath string, err error) {
	return NewAt(dialect, name, time.Now())
}

// NewAt creates folder-style up/down migration files for a dialect using the provided timestamp.
func NewAt(dialect, name string, when time.Time) (upPath, downPath string, err error) {
	safeName := toSnake(name)
	baseDir := baseDirForDialect(dialect)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", "", err
	}
	// Guard: disallow another migration with the same base name to avoid accidental duplicates
	if entries, readErr := os.ReadDir(baseDir); readErr == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if strings.HasPrefix(strings.ToLower(e.Name()), safeName+"_") {
				return "", "", fmt.Errorf("migration with name '%s' already exists: %s", safeName, e.Name())
			}
		}
	}
	ts := when.Format("20060102150405")
	dir := filepath.Join(baseDir, fmt.Sprintf("%s_%s", safeName, ts))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	upPath = filepath.Join(dir, "up.sql")
	downPath = filepath.Join(dir, "down.sql")
	if err := os.WriteFile(upPath, []byte("-- write your UP migration SQL here\n"), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(downPath, []byte("-- write your DOWN migration SQL here\n"), 0o644); err != nil {
		return "", "", err
	}
	return upPath, downPath, nil
}

// NewForService creates service-scoped folder-style up/down migration files under services/{service}/migrations/{dialect}
func NewForService(dialect, service, name string) (upPath, downPath string, err error) {
	return NewForServiceAt(dialect, service, name, time.Now())
}

// NewForServiceAt is a timestamp-injectable variant for tests
func NewForServiceAt(dialect, service, name string, when time.Time) (upPath, downPath string, err error) {
	if strings.TrimSpace(service) == "" {
		return "", "", fmt.Errorf("service is required")
	}
	safeName := toSnake(name)
	baseDir := baseDirForService(service, dialect)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", "", err
	}
	// Guard duplicate names in service dir
	if entries, readErr := os.ReadDir(baseDir); readErr == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if strings.HasPrefix(strings.ToLower(e.Name()), safeName+"_") {
				return "", "", fmt.Errorf("migration with name '%s' already exists in service '%s': %s", safeName, service, e.Name())
			}
		}
	}
	ts := when.Format("20060102150405")
	dir := filepath.Join(baseDir, fmt.Sprintf("%s_%s", safeName, ts))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	upPath = filepath.Join(dir, "up.sql")
	downPath = filepath.Join(dir, "down.sql")
	if err := os.WriteFile(upPath, []byte("-- write your UP migration SQL here\n"), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(downPath, []byte("-- write your DOWN migration SQL here\n"), 0o644); err != nil {
		return "", "", err
	}
	return upPath, downPath, nil
}

// Helpers

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func execSQL(ctx context.Context, db *sqlx.DB, sqlStr string) error {
	// Strip comments BEFORE splitting so that semicolons inside comments
	// (e.g. "-- skipped; users who ...") are not treated as statement
	// delimiters. Dollar-quoted blocks are preserved by the comment
	// stripper so PL/pgSQL bodies stay intact.
	stripped := stripSQLComments(sqlStr)
	parts := splitStatements(stripped)
	tx, _ := db.BeginTxx(ctx, nil)
	useTx := tx != nil
	var e execer
	if useTx {
		e = tx
	} else {
		e = db
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		logger.Debugf("migrations: executing statement\n%s", p)
		if _, err := e.ExecContext(ctx, p); err != nil {
			if useTx {
				_ = tx.Rollback()
			}
			return err
		}
	}
	if useTx {
		return tx.Commit()
	}
	return nil
}

func splitStatements(sql string) []string {
	// Split on ';' boundaries while respecting SQL strings, quoted
	// identifiers, and dollar-quoted blocks (e.g. DO $$ ... END $$;).
	// PostgreSQL COMMENT statements commonly contain semicolons in their
	// quoted descriptions, so treating every non-dollar-quoted semicolon as a
	// delimiter corrupts otherwise-valid migrations.
	var out []string
	var buf strings.Builder
	inDollarQuote := false
	tag := "" // the dollar-quote tag (e.g. "$$" or "$fn$")
	inSingleQuote := false
	inDoubleQuote := false
	escapeString := false
	i := 0
	runes := []rune(sql)
	for i < len(runes) {
		ch := runes[i]

		if inDollarQuote {
			if ch == '$' {
				if t, end, ok := readDollarTag(runes, i); ok && t == tag {
					buf.WriteString(tag)
					i = end
					inDollarQuote = false
					tag = ""
					continue
				}
			}
			buf.WriteRune(ch)
			i++
			continue
		}

		if inSingleQuote {
			buf.WriteRune(ch)
			if escapeString && ch == '\\' && i+1 < len(runes) {
				buf.WriteRune(runes[i+1])
				i += 2
				continue
			}
			if ch == '\'' {
				if i+1 < len(runes) && runes[i+1] == '\'' {
					buf.WriteRune(runes[i+1])
					i += 2
					continue
				}
				inSingleQuote = false
				escapeString = false
			}
			i++
			continue
		}

		if inDoubleQuote {
			buf.WriteRune(ch)
			if ch == '"' {
				if i+1 < len(runes) && runes[i+1] == '"' {
					buf.WriteRune(runes[i+1])
					i += 2
					continue
				}
				inDoubleQuote = false
			}
			i++
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			escapeString = hasEscapeStringPrefix(runes, i)
			buf.WriteRune(ch)
			i++
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			buf.WriteRune(ch)
			i++
			continue
		}
		if ch == '$' {
			// Try to read a dollar-quote tag: $<optional_tag>$
			if t, end, ok := readDollarTag(runes, i); ok {
				inDollarQuote = true
				tag = t
				buf.WriteString(tag)
				i = end
				continue
			}
		}

		if ch == ';' {
			if s := strings.TrimSpace(buf.String()); s != "" {
				out = append(out, s)
			}
			buf.Reset()
			i++
			continue
		}

		buf.WriteRune(ch)
		i++
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func hasEscapeStringPrefix(runes []rune, quotePos int) bool {
	if quotePos == 0 || (runes[quotePos-1] != 'E' && runes[quotePos-1] != 'e') {
		return false
	}
	if quotePos == 1 {
		return true
	}
	return !isSQLIdentifierRune(runes[quotePos-2])
}

func isSQLIdentifierRune(r rune) bool {
	return r == '_' || r == '$' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

// readDollarTag tries to read a dollar-quote tag starting at position pos.
// Returns the full tag string (e.g. "$$" or "$fn$"), the index after the tag,
// and whether a valid tag was found.
func readDollarTag(runes []rune, pos int) (string, int, bool) {
	if pos >= len(runes) || runes[pos] != '$' {
		return "", 0, false
	}
	j := pos + 1
	// Scan optional tag identifier (letters, digits, underscore)
	for j < len(runes) && (runes[j] == '_' || (runes[j] >= 'a' && runes[j] <= 'z') ||
		(runes[j] >= 'A' && runes[j] <= 'Z') || (runes[j] >= '0' && runes[j] <= '9')) {
		j++
	}
	if j >= len(runes) || runes[j] != '$' {
		return "", 0, false
	}
	j++ // consume closing '$'
	return string(runes[pos:j]), j, true
}

// stripSQLComments removes line (--) and block (/* */) comments while
// preserving comment markers inside strings, quoted identifiers, and
// dollar-quoted strings (e.g. DO $$ ... END $$).
func stripSQLComments(s string) string {
	runes := []rune(s)
	var buf strings.Builder
	buf.Grow(len(s))
	inDollarQuote := false
	dollarTag := ""
	inSingleQuote := false
	inDoubleQuote := false
	escapeString := false
	i := 0

	for i < len(runes) {
		ch := runes[i]

		if inDollarQuote {
			if ch == '$' {
				if t, end, ok := readDollarTag(runes, i); ok && t == dollarTag {
					buf.WriteString(t)
					i = end
					inDollarQuote = false
					dollarTag = ""
					continue
				}
			}
			buf.WriteRune(ch)
			i++
			continue
		}

		if inSingleQuote {
			buf.WriteRune(ch)
			if escapeString && ch == '\\' && i+1 < len(runes) {
				buf.WriteRune(runes[i+1])
				i += 2
				continue
			}
			if ch == '\'' {
				if i+1 < len(runes) && runes[i+1] == '\'' {
					buf.WriteRune(runes[i+1])
					i += 2
					continue
				}
				inSingleQuote = false
				escapeString = false
			}
			i++
			continue
		}

		if inDoubleQuote {
			buf.WriteRune(ch)
			if ch == '"' {
				if i+1 < len(runes) && runes[i+1] == '"' {
					buf.WriteRune(runes[i+1])
					i += 2
					continue
				}
				inDoubleQuote = false
			}
			i++
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			escapeString = hasEscapeStringPrefix(runes, i)
			buf.WriteRune(ch)
			i++
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			buf.WriteRune(ch)
			i++
			continue
		}
		if ch == '$' {
			if t, end, ok := readDollarTag(runes, i); ok {
				inDollarQuote = true
				dollarTag = t
				buf.WriteString(t)
				i = end
				continue
			}
		}

		// --- line comment (--) ---
		if ch == '-' && i+1 < len(runes) && runes[i+1] == '-' {
			// skip to end of line
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			continue
		}

		// --- block comment (/* ... */) ---
		if ch == '/' && i+1 < len(runes) && runes[i+1] == '*' {
			i += 2
			depth := 1
			for i < len(runes) && depth > 0 {
				if runes[i] == '/' && i+1 < len(runes) && runes[i+1] == '*' {
					depth++
					i += 2
					continue
				}
				if runes[i] == '*' && i+1 < len(runes) && runes[i+1] == '/' {
					depth--
					i += 2
					continue
				}
				i++
			}
			buf.WriteRune(' ')
			continue
		}

		buf.WriteRune(ch)
		i++
	}

	return buf.String()
}

func ensureMigrationsTable(ctx context.Context, db *sqlx.DB, dialect string) error {
	d := strings.ToLower(dialect)
	switch d {
	case "postgres":
		_, err := db.ExecContext(ctx, `
CREATE SCHEMA IF NOT EXISTS everstack;
CREATE TABLE IF NOT EXISTS everstack.schema_migrations (
    dialect    TEXT    NOT NULL,
    version    BIGINT  NOT NULL,
    name       TEXT    NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dialect, version)
);`)
		return err
	case "clickhouse":
		// ClickHouse driver forbids multi-statement Exec; use execSQL to split and run sequentially
		return execSQL(ctx, db, `CREATE DATABASE IF NOT EXISTS everstack;
CREATE TABLE IF NOT EXISTS everstack.schema_migrations (
    dialect String,
    version Int64,
    name String,
    applied_at DateTime DEFAULT now()
) ENGINE = MergeTree() ORDER BY (dialect, version);`)
	case "common":
		// common piggybacks on the same table created by concrete dialects
		return nil
	default:
		// generic SQL fallback
		_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    dialect    TEXT    NOT NULL,
    version    BIGINT  NOT NULL,
    name       TEXT    NOT NULL,
    applied_at TEXT    NOT NULL,
    PRIMARY KEY (dialect, version)
);`)
		return err
	}
}

func loadApplied(ctx context.Context, db *sqlx.DB, dialect string) (map[string]struct{}, error) {
	rows, err := db.QueryxContext(ctx, `SELECT dialect, version FROM everstack.schema_migrations`)
	if err != nil {
		// fallback to default table name for generic SQL
		rows, err = db.QueryxContext(ctx, `SELECT dialect, version FROM schema_migrations`)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	m := make(map[string]struct{})
	for rows.Next() {
		var d string
		var v int64
		if err := rows.Scan(&d, &v); err != nil {
			return nil, err
		}
		m[fmt.Sprintf("%s:%d", d, v)] = struct{}{}
	}
	return m, nil
}

type appliedRow struct {
	Name    string
	Version int64
}

func listAppliedDescending(ctx context.Context, db *sqlx.DB, dialect string) ([]appliedRow, error) {
	q := `SELECT name, version FROM everstack.schema_migrations WHERE dialect = $1 ORDER BY version DESC`
	// ClickHouse uses ? placeholders; but sqlx will rewrite for pgx only. We'll branch by dialect
	d := strings.ToLower(dialect)
	if d == "clickhouse" {
		q = `SELECT name, version FROM everstack.schema_migrations WHERE dialect = ? ORDER BY version DESC`
	}
	rows, err := db.QueryxContext(ctx, q, dialectKey(dialect))
	if err != nil {
		// fallback generic
		q2 := `SELECT name, version FROM schema_migrations WHERE dialect = ? ORDER BY version DESC`
		if d == "postgres" {
			q2 = `SELECT name, version FROM schema_migrations WHERE dialect = $1 ORDER BY version DESC`
		}
		rows, err = db.QueryxContext(ctx, q2, dialectKey(dialect))
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	out := make([]appliedRow, 0)
	for rows.Next() {
		var r appliedRow
		if err := rows.Scan(&r.Name, &r.Version); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func recordApplied(ctx context.Context, db *sqlx.DB, dialect string, m Migration) error {
	d := strings.ToLower(dialect)
	switch d {
	case "postgres":
		_, err := db.ExecContext(ctx, `INSERT INTO everstack.schema_migrations(dialect, version, name) VALUES ($1,$2,$3)`, dialectKey(dialect), m.Version, m.Name)
		return err
	case "clickhouse":
		_, err := db.ExecContext(ctx, `INSERT INTO everstack.schema_migrations(dialect, version, name) VALUES (?,?,?)`, dialectKey(dialect), m.Version, m.Name)
		return err
	default:
		_, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(dialect, version, name, applied_at) VALUES (?,?,?,?)`, dialectKey(dialect), m.Version, m.Name, time.Now().UTC().Format(time.RFC3339))
		return err
	}
}

func deleteApplied(ctx context.Context, db *sqlx.DB, dialect string, version int64) error {
	d := strings.ToLower(dialect)
	switch d {
	case "postgres":
		_, err := db.ExecContext(ctx, `DELETE FROM everstack.schema_migrations WHERE dialect = $1 AND version = $2`, dialectKey(dialect), version)
		return err
	case "clickhouse":
		_, err := db.ExecContext(ctx, `ALTER TABLE everstack.schema_migrations DELETE WHERE dialect = ? AND version = ?`, dialectKey(dialect), version)
		return err
	default:
		_, err := db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE dialect = ? AND version = ?`, dialectKey(dialect), version)
		return err
	}
}

func findDownMigration(dialect, name string, version int64) (*Migration, error) {
	baseDir := baseDirForDialect(dialect)
	// First try folder-style down.sql
	dirName := fmt.Sprintf("%s_%014d", toSnake(name), version)
	folderDown := filepath.Join(baseDir, dirName, "down.sql")
	if b, err := os.ReadFile(folderDown); err == nil {
		return &Migration{Name: name, Version: version, Direction: DirDown, Path: folderDown, SQL: string(b)}, nil
	}
	// Fallback to legacy flat file style
	legacy := filepath.Join(baseDir, fmt.Sprintf("%s_%014d%s", toSnake(name), version, downSuffix))
	b, err := os.ReadFile(legacy)
	if err != nil {
		return nil, fmt.Errorf("down migration not found: %s", legacy)
	}
	return &Migration{Name: name, Version: version, Direction: DirDown, Path: legacy, SQL: string(b)}, nil
}

func loadMigrationsFromDisk(dialect string, dir Direction) ([]Migration, error) {
	root := baseDirForDialect(dialect)
	list := make([]Migration, 0)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Folder style: <name>_<ts>/up.sql|down.sql
			base := filepath.Base(path)
			match := dirNameRegex.FindStringSubmatch(base)
			if len(match) == 0 {
				return nil
			}
			nameIdx := dirNameRegex.SubexpIndex("name")
			tsIdx := dirNameRegex.SubexpIndex("ts")
			migName := match[nameIdx]
			ts := match[tsIdx]
			ver, err := parseTimestampVersion(ts)
			if err != nil {
				return nil
			}
			target := "up.sql"
			if dir == DirDown {
				target = "down.sql"
			}
			filePath := filepath.Join(path, target)
			if b, readErr := os.ReadFile(filePath); readErr == nil {
				list = append(list, Migration{
					Name:      migName,
					Version:   ver,
					Direction: dir,
					Path:      filePath,
					SQL:       string(b),
				})
			}
			return nil
		}
		// Legacy flat file style: <name>_<ts>.up.sql/down.sql
		if !strings.HasSuffix(path, string(dir)+".sql") {
			return nil
		}
		file := filepath.Base(path)
		match := fileNameRegex.FindStringSubmatch(file)
		if len(match) == 0 {
			return nil
		}
		idx := fileNameRegex.SubexpIndex("ts")
		ts := match[idx]
		ver, err := parseTimestampVersion(ts)
		if err != nil {
			return nil
		}
		nameIdx := fileNameRegex.SubexpIndex("name")
		name := match[nameIdx]
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		list = append(list, Migration{
			Name:      name,
			Version:   ver,
			Direction: dir,
			Path:      path,
			SQL:       string(b),
		})
		return nil
	})
	return list, nil
}

// Service-scoped loaders
func loadServiceMigrationsFromDisk(service, dialect string, dir Direction) ([]Migration, error) {
	root := baseDirForService(service, dialect)
	list := make([]Migration, 0)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			match := dirNameRegex.FindStringSubmatch(base)
			if len(match) == 0 {
				return nil
			}
			nameIdx := dirNameRegex.SubexpIndex("name")
			tsIdx := dirNameRegex.SubexpIndex("ts")
			migName := match[nameIdx]
			ts := match[tsIdx]
			ver, err := parseTimestampVersion(ts)
			if err != nil {
				return nil
			}
			target := "up.sql"
			if dir == DirDown {
				target = "down.sql"
			}
			filePath := filepath.Join(path, target)
			if b, readErr := os.ReadFile(filePath); readErr == nil {
				list = append(list, Migration{
					Name:      migName,
					Version:   ver,
					Direction: dir,
					Path:      filePath,
					SQL:       string(b),
				})
			}
			return nil
		}
		if !strings.HasSuffix(path, string(dir)+".sql") {
			return nil
		}
		file := filepath.Base(path)
		match := fileNameRegex.FindStringSubmatch(file)
		if len(match) == 0 {
			return nil
		}
		idx := fileNameRegex.SubexpIndex("ts")
		ts := match[idx]
		ver, err := parseTimestampVersion(ts)
		if err != nil {
			return nil
		}
		nameIdx := fileNameRegex.SubexpIndex("name")
		name := match[nameIdx]
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		list = append(list, Migration{
			Name:      name,
			Version:   ver,
			Direction: dir,
			Path:      path,
			SQL:       string(b),
		})
		return nil
	})
	return list, nil
}

func baseDirForService(service, dialect string) string {
	// services/{service}/internal/database/migrations/{dialect}
	return filepath.Join("services", service, "internal", "database", "migrations", strings.ToLower(dialect))
}

func findServiceDownMigration(service, dialect, name string, version int64) (*Migration, error) {
	baseDir := baseDirForService(service, dialect)
	dirName := fmt.Sprintf("%s_%014d", toSnake(name), version)
	folderDown := filepath.Join(baseDir, dirName, "down.sql")
	if b, err := os.ReadFile(folderDown); err == nil {
		return &Migration{Name: name, Version: version, Direction: DirDown, Path: folderDown, SQL: string(b)}, nil
	}
	legacy := filepath.Join(baseDir, fmt.Sprintf("%s_%014d%s", toSnake(name), version, downSuffix))
	b, err := os.ReadFile(legacy)
	if err != nil {
		return nil, fmt.Errorf("down migration not found: %s", legacy)
	}
	return &Migration{Name: name, Version: version, Direction: DirDown, Path: legacy, SQL: string(b)}, nil
}

func baseDirForDialect(dialect string) string {
	d := strings.ToLower(dialect)
	switch d {
	case "postgres":
		return filepath.Join("internal", "database", "migrations", "sql", "postgres")
	case "clickhouse":
		return filepath.Join("internal", "database", "migrations", "sql", "clickhouse")
	case "common":
		return filepath.Join("internal", "database", "migrations", "sql", "common")
	default:
		return filepath.Join("internal", "database", "migrations", "sql", d)
	}
}

func toSnake(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ToLower(s)
	// collapse multiple underscores
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return s
}

func dialectKey(d string) string {
	return strings.ToLower(d)
}

func parseTimestampVersion(ts string) (int64, error) {
	// ts is expected to be 14 digits YYYYMMDDHHMMSS
	if len(ts) != 14 {
		return 0, fmt.Errorf("invalid timestamp length: %s", ts)
	}
	// avoid importing strconv at top by parsing via time then formatting back
	t, err := time.Parse("20060102150405", ts)
	if err != nil {
		return 0, err
	}
	// Preserve the 14-digit timestamp as integer by formatting back
	// Note: converting to int64 may drop leading zeros, but we pad when generating filenames
	// Use Unix milli-style uniqueness not required; the 14-digit time encodes ordering sufficiently
	var buf = t.Format("20060102150405")
	var n int64 = 0
	for i := 0; i < len(buf); i++ {
		n = n*10 + int64(buf[i]-'0')
	}
	return n, nil
}

// Exported helpers for CLI status
func LoadAppliedForCLI(ctx context.Context, db *sqlx.DB, dialect string) (map[string]struct{}, error) {
	return loadApplied(ctx, db, dialect)
}
func LoadDiskForCLI(dialect string) ([]Migration, error) {
	ups, err := loadMigrationsFromDisk(dialect, DirUp)
	if err != nil {
		return nil, err
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].Version < ups[j].Version })
	return ups, nil
}

// LoadServiceDiskForCLI lists service-scoped up migrations from disk, sorted by version.
func LoadServiceDiskForCLI(service, dialect string) ([]Migration, error) {
	ups, err := loadServiceMigrationsFromDisk(service, dialect, DirUp)
	if err != nil {
		return nil, err
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].Version < ups[j].Version })
	return ups, nil
}

// tenantExcludedPrefixes lists migration name prefixes that should be skipped
// when applying migrations to a tenant schema. These are system-level
// migrations that operate on global objects (e.g. the "system" schema)
// and must only run once in the default/everstack schema.
var tenantExcludedPrefixes = []string{
	"instances_",     // system.instances table (references system. schema)
	"device_signing", // device signing keys (global, not per-tenant)
}

// isTenantExcludedMigration returns true if the named migration should be
// skipped in per-tenant schema provisioning.
func isTenantExcludedMigration(name string) bool {
	for _, prefix := range tenantExcludedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
