// Package db owns the SQLite connection, schema migration, the typed query
// layer (Store), and fixture seeding.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"

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
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return conn, nil
}

// migrate applies additive schema changes that CREATE TABLE IF NOT EXISTS can't
// make to an already-existing table (SQLite has no ADD COLUMN IF NOT EXISTS).
// Each step is idempotent: it checks for the column before adding it, so a fresh
// DB (where schema.sql already created the column) and an old one both converge.
func migrate(conn *sql.DB) error {
	addColumn := func(table, column, ddl string) error {
		has, err := hasColumn(conn, table, column)
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		_, err = conn.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, ddl))
		return err
	}
	// matches.external_ref: links a fixture to its results-feed match id.
	return addColumn("matches", "external_ref", "external_ref TEXT NOT NULL DEFAULT ''")
}

// hasColumn reports whether a table already has a column, via PRAGMA table_info.
func hasColumn(conn *sql.DB, table, column string) (bool, error) {
	rows, err := conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
