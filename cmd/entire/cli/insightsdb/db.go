// Package insightsdb provides a SQLite cache layer for session analytics.
// It stores session metadata for fast querying by the insights and improve commands.
package insightsdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // SQLite driver
)

// InsightsDB wraps a SQLite database for insights caching.
type InsightsDB struct {
	db *sql.DB
}

// Open opens (or creates) the insights cache at the given path.
// It sets WAL mode and busy timeout for safe concurrent access,
// then runs migrations to ensure all tables exist.
func Open(dbPath string) (*InsightsDB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err = applyPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	idb := &InsightsDB{db: db}
	if err = idb.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return idb, nil
}

// applyPragmas sets performance and safety pragmas on the database.
func applyPragmas(db *sql.DB) error {
	ctx := context.Background()
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	}
	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("apply pragma %q: %w", pragma, err)
		}
	}
	return nil
}

// Close closes the underlying database connection.
func (idb *InsightsDB) Close() error {
	if err := idb.db.Close(); err != nil {
		return fmt.Errorf("close insights database: %w", err)
	}
	return nil
}

// migrate creates all tables if they do not already exist.
// It is safe to call multiple times (idempotent).
func (idb *InsightsDB) migrate() error {
	ctx := context.Background()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS cache_meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			checkpoint_id        TEXT NOT NULL,
			session_id           TEXT NOT NULL,
			session_index        INTEGER NOT NULL,
			agent                TEXT,
			model                TEXT,
			branch               TEXT,
			owner_name           TEXT,
			owner_email          TEXT,
			created_at           TEXT NOT NULL,
			input_tokens         INTEGER DEFAULT 0,
			cache_tokens         INTEGER DEFAULT 0,
			output_tokens        INTEGER DEFAULT 0,
			total_tokens         INTEGER DEFAULT 0,
			api_call_count       INTEGER DEFAULT 0,
			duration_ms          INTEGER DEFAULT 0,
			turn_count           INTEGER DEFAULT 0,
			intent               TEXT,
			outcome              TEXT,
			agent_percentage     REAL DEFAULT 0,
			overall_score        REAL,
			score_token_efficiency REAL,
			score_first_pass     REAL,
			score_friction       REAL,
			score_focus          REAL,
			has_summary          INTEGER DEFAULT 0,
			has_facets           INTEGER DEFAULT 0,
			PRIMARY KEY (checkpoint_id, session_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_agent   ON sessions(agent)`,
		`CREATE TABLE IF NOT EXISTS files_touched (
			checkpoint_id TEXT NOT NULL,
			session_index INTEGER NOT NULL,
			file_path     TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS friction (
			checkpoint_id TEXT NOT NULL,
			session_index INTEGER NOT NULL,
			text          TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_friction_text ON friction(text)`,
		`CREATE TABLE IF NOT EXISTS learnings (
			checkpoint_id TEXT NOT NULL,
			session_index INTEGER NOT NULL,
			scope         TEXT NOT NULL,
			finding       TEXT NOT NULL,
			path          TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS tool_calls (
			checkpoint_id TEXT NOT NULL,
			session_index INTEGER NOT NULL,
			tool_name     TEXT NOT NULL,
			count         INTEGER DEFAULT 1
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(checkpoint_id, session_index)`,
		`CREATE TABLE IF NOT EXISTS repeated_user_instructions (
			checkpoint_id TEXT NOT NULL,
			session_index INTEGER NOT NULL,
			instruction   TEXT NOT NULL,
			evidence      TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS missing_context_signals (
			checkpoint_id TEXT NOT NULL,
			session_index INTEGER NOT NULL,
			item          TEXT NOT NULL,
			evidence      TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS failure_loops (
			checkpoint_id TEXT NOT NULL,
			session_index INTEGER NOT NULL,
			description   TEXT NOT NULL,
			count         INTEGER DEFAULT 1,
			evidence      TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS skill_signals (
			checkpoint_id       TEXT NOT NULL,
			session_index       INTEGER NOT NULL,
			skill_name          TEXT NOT NULL,
			skill_path          TEXT,
			friction            TEXT,
			missing_instruction TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS suggestions (
			id          TEXT PRIMARY KEY,
			file_type   TEXT NOT NULL,
			category    TEXT NOT NULL,
			title       TEXT NOT NULL,
			description TEXT,
			diff        TEXT,
			priority    TEXT DEFAULT 'medium',
			status      TEXT DEFAULT 'pending',
			created_at  TEXT NOT NULL,
			resolved_at TEXT
		)`,
	}

	for _, stmt := range statements {
		if _, err := idb.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("execute migration statement: %w", err)
		}
	}
	for _, alter := range []string{
		`ALTER TABLE sessions ADD COLUMN owner_name TEXT`,
		`ALTER TABLE sessions ADD COLUMN owner_email TEXT`,
	} {
		if _, err := idb.db.ExecContext(ctx, alter); err != nil && !isDuplicateColumnError(err) {
			return fmt.Errorf("execute migration statement: %w", err)
		}
	}
	return nil
}

func isDuplicateColumnError(err error) bool {
	return err != nil && (contains(err.Error(), "duplicate column name") || contains(err.Error(), "already exists"))
}

func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// ListTables returns the names of all user tables in the database.
// This is used in tests to verify migrations ran correctly.
func (idb *InsightsDB) ListTables(ctx context.Context) ([]string, error) {
	rows, err := idb.db.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("query tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tables: %w", err)
	}
	return tables, nil
}
