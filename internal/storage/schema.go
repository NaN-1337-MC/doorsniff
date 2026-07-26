package storage

import (
	"context"
	"fmt"
)

// currentSchemaVersion is the latest schema version. Bump on every
// backward-incompatible change and add a new entry to migrations.
const currentSchemaVersion = 2

type migration struct {
	version int
	sql     string
}

// migrations is applied in order. Each entry must be idempotent on its
// own (CREATE TABLE IF NOT EXISTS) so re-runs after a partial crash
// are safe.
var migrations = []migration{
	{version: 1, sql: schemaV1},
	{version: 2, sql: schemaV2},
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS servers (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  ip               TEXT    NOT NULL,
  port             INTEGER NOT NULL,
  first_seen       TIMESTAMP NOT NULL,
  last_seen        TIMESTAMP NOT NULL,
  last_latency_ms  INTEGER,
  version_protocol INTEGER,
  version_name     TEXT,
  motd             TEXT,
  num_players      INTEGER,
  max_players      INTEGER,
  sample_players   TEXT,
  query_enabled    BOOLEAN,
  plugins          TEXT,
  country          TEXT,
  raw_query        TEXT,
  probe_kind       TEXT,
  UNIQUE(ip, port)
);

CREATE INDEX IF NOT EXISTS idx_servers_last_seen
  ON servers(last_seen DESC);

CREATE INDEX IF NOT EXISTS idx_servers_version
  ON servers(version_protocol);

CREATE INDEX IF NOT EXISTS idx_servers_players
  ON servers(num_players DESC);

CREATE INDEX IF NOT EXISTS idx_servers_country
  ON servers(country);

CREATE INDEX IF NOT EXISTS idx_servers_port
  ON servers(port);
`

const schemaV2 = `
CREATE TABLE IF NOT EXISTS scan_progress (
  seed_ip    TEXT    PRIMARY KEY,
  last_port  INTEGER NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// EnsureSchema applies any pending migrations. Safe to call on every
// open — the schema_version table records which migrations have run.
func (db *DB) EnsureSchema(ctx context.Context) error {
	if _, err := db.conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_version (
  version INTEGER PRIMARY KEY,
  applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`); err != nil {
		return fmt.Errorf("creating schema_version table: %w", err)
	}

	var current int
	if err := db.conn.QueryRowContext(ctx, `
SELECT COALESCE(MAX(version), 0) FROM schema_version;
`).Scan(&current); err != nil {
		return fmt.Errorf("reading current schema version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("applying migration v%d: %w", m.version, err)
		}
	}
	return nil
}

func (db *DB) applyMigration(ctx context.Context, m migration) error {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("exec migration: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_version (version) VALUES (?);
`, m.version); err != nil {
		return fmt.Errorf("recording version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
