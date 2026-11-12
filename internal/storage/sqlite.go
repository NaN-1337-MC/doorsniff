// Package storage persists scan results to a local SQLite database.
//
// The driver is modernc.org/sqlite — pure Go, no CGO toolchain. The
// *sql.DB pool is capped to a single connection: SQLite allows only one
// writer at a time, and multiple pooled connections racing to write
// produce SQLITE_BUSY. A single connection serializes every query so no
// intra-process lock contention can occur.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // driver registration
)

// DefaultBusyTimeoutMs is the default SQLite busy_timeout: how long a
// connection waits for a locked database before returning SQLITE_BUSY.
const DefaultBusyTimeoutMs = 5000

// DB is a thin wrapper around *sql.DB that owns the connection pool
// and the database lifecycle.
type DB struct {
	conn *sql.DB
	path string
}

type openOptions struct {
	busyTimeoutMs int
}

// Option configures Open.
type Option func(*openOptions)

// WithBusyTimeout sets the SQLite busy_timeout in milliseconds. It
// controls how long a connection waits for a locked database before
// returning SQLITE_BUSY — mainly relevant when another process (e.g. a
// concurrent `doorsniff list`) contends on the same file.
func WithBusyTimeout(ms int) Option {
	return func(o *openOptions) { o.busyTimeoutMs = ms }
}

// Open opens a SQLite database at path. For an in-memory database, pass
// ":memory:".
//
// For file-based databases the connection string carries modernc-style
// `_pragma` parameters so every pooled connection inherits WAL mode, a
// busy timeout, and the other settings. PRAGMAs set via ExecContext only
// affect whichever single connection the pool happened to hand out,
// which silently leaves the other connections on SQLITE_BUSY-prone
// defaults. The pool is additionally capped to one connection so the
// single SQLite writer cannot be contended in-process.
func Open(ctx context.Context, path string, opts ...Option) (*DB, error) {
	o := openOptions{busyTimeoutMs: DefaultBusyTimeoutMs}
	for _, opt := range opts {
		opt(&o)
	}
	if o.busyTimeoutMs < 0 {
		o.busyTimeoutMs = 0
	}

	dsn := path
	if path != ":memory:" {
		params := fmt.Sprintf(
			"_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(ON)&_txlock=immediate",
			o.busyTimeoutMs,
		)
		if strings.Contains(path, "?") {
			dsn = path + "&" + params
		} else {
			dsn = path + "?" + params
		}
	}

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite at %q: %w", path, err)
	}

	// Serialize all access on a single connection. SQLite permits one
	// writer; a pool of several writer connections is exactly what
	// produces SQLITE_BUSY even under WAL.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("pinging sqlite at %q: %w", path, err)
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
