// Package analytics is BEThoven's OPTIONAL usage-tracking subsystem. It records
// player events (accesses, bets, screen views, admin actions) into its OWN
// SQLite database, written asynchronously so it can never block or fail the
// betting/scoring path. When disabled (the default) nothing in here is wired in
// and the server behaves exactly as before.
//
// The design mirrors internal/live: a nil-able port on the service. The two
// guarantees that keep analytics a "side thing":
//
//  1. Separate DB + connection — analytics writes never contend with the domain
//     DB's single-writer lock, so they can't cause "database is locked" on a bet.
//  2. Async, fire-and-forget — Recorder.Track buffers and returns immediately,
//     dropping rather than blocking when the buffer is full, and never errors.
package analytics

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

//go:embed schema.sql
var schema string

// Open connects to the analytics SQLite database at path (its own file, distinct
// from the domain DB), enabling WAL, and applies the schema (idempotent). Use
// ":memory:" for ephemeral tests. Mirrors internal/db.Open — single writer, WAL,
// busy timeout — but over a separate connection so the two never share a lock.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open analytics sqlite %s: %w", path, err)
	}
	// SQLite is single-writer; one connection avoids "database is locked".
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping analytics sqlite: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("apply analytics schema: %w", err)
	}
	return conn, nil
}
