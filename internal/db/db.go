// Package db owns the SQLite connection, schema migration, the typed query
// layer (Store), and fixture seeding.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

//go:embed schema.sql
var schema string

// Open connects to the SQLite database at path, enabling foreign keys and WAL,
// and applies the schema (idempotent). Use ":memory:" for ephemeral tests.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// SQLite is single-writer; one connection avoids "database is locked".
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// migrate applies additive column migrations that the embedded schema's
// CREATE TABLE IF NOT EXISTS can't reach on a DB that already exists. Each
// ADD COLUMN is idempotent: a "duplicate column name" error means a prior boot
// (or a freshly-created schema) already has the column, so it's ignored. Keep
// every migration here additive and nullable — never a destructive change.
func migrate(conn *sql.DB) error {
	stmts := []string{
		`ALTER TABLE matches ADD COLUMN penalty_a INTEGER`,
		`ALTER TABLE matches ADD COLUMN penalty_b INTEGER`,
	}
	for _, q := range stmts {
		if _, err := conn.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("migrate %q: %w", q, err)
		}
	}
	return nil
}
