# Storage

The scanner persists every probe result and every resume checkpoint
to a local SQLite database. The driver is `modernc.org/sqlite` —
pure-Go, no CGO toolchain required.

## File locations

XDG-compliant:

- Config: `~/.config/swoomps/config.yaml`
- Seeds: `~/.config/swoomps/seeds/`
- Database: `~/.local/share/swoomps/swoomps.db`

Override via env (`SWOOMPS_DATABASE_PATH`, `SWOOMPS_SEEDS_DIR`) or the
binary's `--database` / `--seeds` flags. `swoomps probe --file PATH`
reads a single seed file instead of the seeds directory.

## Schema

Two tables.

### `servers` (migration v1)

Every discovered server. `UNIQUE(ip, port)` guards against duplicates
— the same Minecraft server on a different port is recorded as a
separate row.

```sql
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
```

Columns:

- `ip` — IPv4 or IPv6 string, as resolved.
- `port` — the port that responded. Default 25565 if the server only
  advertises the default port.
- `first_seen` / `last_seen` — set once on first insert, updated on
  every hit.
- `last_latency_ms` — measured dial + handshake round-trip.
- `version_protocol` / `version_name` — populated from SLP.
- `motd` — JSON encoding of the description (string or chat object).
- `num_players` / `max_players` — from SLP.
- `sample_players` — JSON array of `{name, id}` from SLP
  `players.sample`.
- `query_enabled` — `true` if the Query protocol responded (future
  use).
- `plugins` — JSON array of `{name, version}` from Query stat
  (future use).
- `country` — ISO-2 (e.g. `BR`); populated only if GeoIP is enabled.
- `raw_query` — raw full-stat response, kept for debugging.
- `probe_kind` — how the server was discovered: `slp` (modern 1.7+)
  or `slp-legacy` (1.6 fallback used when the modern handshake RSTs).
  The Query value is reserved for the planned UDP probe.
- `map` — current map name, populated from Query protocol.
- `gametype` — game type (e.g. `survival`), populated from Query
  protocol.
- `login_mode` — login handshake result: `online` (premium/Mojang
  auth), `offline` (cracked/no auth), `whitelisted`, `banned`,
  `kicked`, `full`, `disconnect` (server responded with something
  else), `unknown` (no login handshake performed).

### `scan_progress` (migration v2)

Per-seed resume checkpoint. One row per fully-scanned seed IP.

```sql
CREATE TABLE IF NOT EXISTS scan_progress (
  seed_ip    TEXT    PRIMARY KEY,
  last_port  INTEGER NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

`last_port` is the highest port scanned for that seed in the last
run. A row is written only after the seed has scanned every port in
the range, so an interrupted run re-scans the in-flight seed from
`last_port + 1` and skips all fully-completed ones.

## Migrations

The schema is versioned via a `schema_version` table:

```sql
CREATE TABLE IF NOT EXISTS schema_version (
  version INTEGER PRIMARY KEY,
  applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

`storage.EnsureSchema` is called on every open. It compares the
current version, runs each pending migration in a transaction, and
writes the new version row. Migrations are listed in
`internal/storage/schema.go` and applied in order:

| Version | Adds |
|---|---|
| v1 | `servers` table + 5 indexes |
| v2 | `scan_progress` table (resume support) |
| v3 | `map` and `gametype` columns (query protocol) |
| v4 | `login_mode` column (online/offline detection) |

Each migration's SQL is idempotent on its own
(`CREATE TABLE IF NOT EXISTS`), so re-runs after a partial crash are
safe.

## Repository

The `storage.Repository` type is the only writer to the database. It
exposes:

```go
type Repository struct { ... }

func NewRepository(db *DB) *Repository
func (r *Repository) UpsertServer(ctx, s) error
func (r *Repository) ListServers(ctx, opts) ([]Server, error)
func (r *Repository) ListServersFiltered(ctx, q ListQuery) ([]Server, error)
func (r *Repository) GetStats(ctx) (Stats, error)
func (r *Repository) LoadProgress(ctx) (map[string]uint16, error)
func (r *Repository) SaveProgress(ctx, seedIP, lastPort) error
```

`UpsertServer` is the hot path. It is implemented as:

```sql
INSERT INTO servers (ip, port, first_seen, last_seen, ...)
VALUES (?, ?, ?, ?, ...)
ON CONFLICT (ip, port) DO UPDATE SET
  last_seen = excluded.last_seen,
  last_latency_ms = excluded.last_latency_ms,
  version_protocol = excluded.version_protocol,
  ...;
```

`ListServersFiltered` is what `swoomps list` calls. It takes a
`ListQuery` with optional player count SQL, version substrings, engine
substring, MOTD substring, and a port range. Filters are AND'd; the
version / engine / MOTD lists are OR'd within their group.

`LoadProgress` and `SaveProgress` are the resume hooks. `LoadProgress`
returns a `map[seed_ip]last_port`; `SaveProgress` is an UPSERT keyed
on `seed_ip`.

The repository is the only place that touches `*sql.DB`. The scanner
layer constructs `Repository` once and passes it to the DB writer
goroutine.

## Concurrency

One `*sql.DB` instance is shared. `sql.DB` is already goroutine-safe;
the driver is configured with reasonable pool defaults. Write
contention is negligible at the QPS budget (200 by default).

In practice, the scanner funnels every result through a single DB
writer goroutine. This serialises writes (correct for SQLite) and
keeps lock contention at zero. The `SaveProgress` calls from
`generateTargets` are also funnelled through this goroutine via the
`onSeedDone` callback (which runs in the generator's goroutine, but
the SQLite call itself is sub-millisecond and happens between
`out <-` operations on the target channel).

## Query patterns (`swoomps list`)

The CLI ships seven filters on top of the table:

| Flag | Effect |
|---|---|
| `--version` | `version_name LIKE %X%` (comma = OR) |
| `--engine` | `version_name LIKE %X%` (single substring) |
| `--players` | `num_players OP N` (operators: `=`, `>`, `<`, `>=`, `<=`, `-` for range) |
| `--port` | `port = X` or `port BETWEEN A AND B` |
| `--motd` | `motd LIKE %X%` |
| `--sort` | `last_seen DESC` (default) / `num_players DESC` / `last_latency_ms ASC` / `version_name ASC` / `port ASC` / `first_seen ASC` |
| `--limit` | default 50 |

Stats aggregation:

```sql
SELECT version_protocol, version_name, COUNT(*) AS n
FROM servers
WHERE version_protocol IS NOT NULL
GROUP BY version_protocol, version_name
ORDER BY n DESC
LIMIT 10;
```

```sql
SELECT country, COUNT(*) AS n
FROM servers
WHERE country IS NOT NULL
GROUP BY country
ORDER BY n DESC
LIMIT 20;
```

## Backup

The file is a single SQLite database. `cp` while the scan is running
is safe (WAL mode is enabled in `storage.Open`); `sqlite3 vacuum` is
not needed.

## Future

- A `probes` table would record every probe attempt (success and
  failure), not just successful hits. Useful for hit-rate stats.
- A `seeds` table would persist the seed list (today the seeds
  directory holds plain text files).
- A `secrets` table for tokens, if a future feature needs auth.
- A `--format json` flag on `swoomps list` would feed downstream
  pipelines without changing the schema.
