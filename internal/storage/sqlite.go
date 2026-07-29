// Package storage persists scan results to a local SQLite database.
//
// The driver is modernc.org/sqlite — pure Go, no CGO toolchain. Only one
// goroutine writes at a time (the DB writer owned by the scanner), so
// the *sql.DB is configured for moderate parallelism.
package storage

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // driver registration
)

// DB is a thin wrapper around *sql.DB that owns the connection pool
// and the database lifecycle.
type DB struct {
	conn *sql.DB
	path string
}

// Open opens a SQLite database at path. For an in-memory database, pass
// ":memory:". WAL mode is enabled for file-based databases.
func Open(ctx context.Context, path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite at %q: %w", path, err)
	}

	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("pinging sqlite at %q: %w", path, err)
	}

	if path != ":memory:" {
		// WAL is set per-connection; modernc.org/sqlite respects the
		// `_pragma` URL parameter, but a single Exec is more portable.
		if _, err := conn.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("setting WAL mode: %w", err)
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=NORMAL"); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("setting synchronous mode: %w", err)
		}
		// Wait up to 5 s before returning SQLITE_BUSY so that brief
		// lock contentions (common at high write throughput) are retried
		// instead of propagated as errors.
		if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("setting busy_timeout: %w", err)
		}
	}

	return &DB{conn: conn, path: path}, nil
}

// Path returns the path the database was opened with.
func (db *DB) Path() string {
	return db.path
}

// Conn returns the underlying *sql.DB. Use sparingly — the Repository
// type is the preferred access path.
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// Close releases the connection pool.
func (db *DB) Close() error {
	return db.conn.Close()
}
