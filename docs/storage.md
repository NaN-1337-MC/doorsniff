# Storage

The scanner persists every probe result to a local SQLite database. The
driver is `modernc.org/sqlite` — pure-Go, no CGO toolchain required.

## File locations

XDG-compliant:

- Config: `~/.config/swoomps/config.yaml`
- Database: `~/.local/share/swoomps/swoomps.db`

Override via env (`SWOOMPS_DATABASE_PATH`) or the binary's
`--database` flag.

## Schema

A single `servers` table holds every discovered server. `UNIQUE(ip, port)`
guards against duplicates — the same Minecraft server on a different
port is recorded as a separate row.

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
- `first_seen` / `last_seen` — set once on first insert, updated on every
  hit.
- `last_latency_ms` — measured dial + handshake round-trip.
- `version_protocol` / `version_name` — populated from SLP.
- `motd` — JSON encoding of the description (string or chat object).
- `num_players` / `max_players` — from SLP.
- `sample_players` — JSON array of `{name, id}` from SLP `players.sample`.
- `query_enabled` — `true` if the Query protocol responded.
- `plugins` — JSON array of `{name, version}` from Query stat.
- `country` — ISO-2 (e.g. `BR`); populated only if GeoIP is enabled.
- `raw_query` — raw full-stat response, kept for debugging.
- `probe_kind` — how the server was discovered: `slp`, `query`, or
  `slp+query`. The two protocol attempts are independent; a server
  may respond to SLP but not to Query.

## Migrations

The schema is versioned via a `schema_version` table:

```sql
CREATE TABLE IF NOT EXISTS schema_version (
  version INTEGER PRIMARY KEY,
  applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

`storage.EnsureSchema` is called on every open. It compares the current
version, runs each migration in a transaction, and updates the version
row. The MVP ships a single migration (`v1`).

## Repository

The `storage.Repository` type is the only writer to the database. It
exposes:

```go
type Repository interface {
    UpsertServer(ctx context.Context, s model.Server) error
    ListServers(ctx context.Context, opts ListOptions) ([]model.Server, error)
    GetStats(ctx context.Context) (Stats, error)
    Close() error
}
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

The repository is the only place that touches `*sql.DB`. The scanner
layer constructs `Repository` once and passes it to the DB writer
goroutine.

## Concurrency

One `*sql.DB` instance is shared. `sql.DB` is already goroutine-safe;
we set `SetMaxOpenConns(N)` where `N` matches the worker pool size
(default 64). Write contention is negligible at our QPS budget (50 by
default).

In practice, the scanner funnels every `Result` through a single DB
writer goroutine. This serialises writes (correct for SQLite) and keeps
the lock contention at zero.

## Query patterns

`--list` examples:

- "Top by players": `ORDER BY num_players DESC LIMIT 50`
- "Recently seen": `ORDER BY last_seen DESC LIMIT 50`
- "By version": `WHERE version_protocol = ?` + filter
- "By country": `WHERE country = ?` + filter
- "By port": `WHERE port = ?` (e.g. only non-default ports)

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

```sql
SELECT port, COUNT(*) AS n
FROM servers
WHERE port != 25565
GROUP BY port
ORDER BY n DESC
LIMIT 20;
```

## Backup

The file is a single SQLite database. `cp` while the scan is running is
safe (WAL mode is enabled by default in `modernc.org/sqlite`); `sqlite3
vacuum` is not needed.

## Future

- A second table `probes` would record every probe attempt (success and
  failure), not just successful hits. Useful for hit-rate stats.
- A `seen_ips` table would track which IPs have been probed in the
  current `seen_ttl` window (default 24h) to avoid re-scanning.
- A `seeds` table would persist the seed list (the MVP keeps seeds in
  the config file).
- A `secrets` table for tokens, if a future feature needs auth.
