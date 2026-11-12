package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOpenAppliesPragmas guards against the modernc DSN silently
// regressing to mattn-style params (_journal_mode, _busy_timeout) that
// modernc.org/sqlite ignores. When the pragmas are dropped, WAL mode
// and the busy timeout stop applying on the pooled connection, which
// reintroduces immediate SQLITE_BUSY under concurrent writes.
func TestOpenAppliesPragmas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	db, err := Open(ctx, filepath.Join(dir, "test.db"), WithBusyTimeout(30000))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var journalMode string
	if err := db.Conn().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := db.Conn().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != 30000 {
		t.Errorf("busy_timeout = %d, want 30000", busyTimeout)
	}
}

// TestOpenDefaultBusyTimeout verifies the default busy_timeout is
// applied when no option is given.
func TestOpenDefaultBusyTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	db, err := Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var busyTimeout int
	if err := db.Conn().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != DefaultBusyTimeoutMs {
		t.Errorf("busy_timeout = %d, want %d", busyTimeout, DefaultBusyTimeoutMs)
	}
}
