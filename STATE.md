# Project State

> Snapshot of the swoomps project as of the last session. Read this
> first if you're picking up where a previous session left off.

## What this is

**swoomps** is a Minecraft server scanner written in Go. Given a list
of seed IPs, it scans a configurable port range, checks each open
port for a Minecraft server (SLP + legacy SLP fallback), and persists
results to a local SQLite database.

**Goal**: learning artifact. Hands-on with Go's concurrency, the
Minecraft wire protocols, and the Charm TUI stack.

**Scope**: hybrid discovery (seeds + port range), TCP connect probe,
modern SLP (1.7+) with 1.6 legacy fallback, SQLite persistence, lipgloss
output. The TUI dashboard is deferred.

## Status

The vertical slice is **working end-to-end**. We can:

- Load seeds from a directory of `.txt` files
- Scan a port range with TCP connect (rate-limited, worker pool)
- Probe each open port with modern SLP, fall back to 1.6 legacy on RST
- Persist every successful discovery to SQLite (`UNIQUE(ip, port)`)
- Print `[+]` for found servers, `[-]` only with `--verbose`/`-v`
- Cancel cleanly with Ctrl+C; partial results are written

A real run against the user's seed IPs found **21+ unique Minecraft
servers** on `147.185.221.211` alone (Paper, Velocity, Purpur, vanilla
in ports 6900-33310). The other 4 IPs are still being scanned.

## What is not done yet

- `swoomps tui` is a stub ("not implemented yet") — phase 9
- `swoomps scan`, `list`, `seed`, `init` are stubs — same dispatcher
- No `--list` query interface yet (DB has the data; no CLI to read it)
- No Query protocol (UDP) — phase 3 was skipped
- No CIDR expansion of seed IPs (only direct seeds)
- No GeoIP / country lookup
- No lipgloss styling on output (plain text)
- No deduplication heuristic in pool (we saw a few duplicates in one
  test run, root cause not yet identified; DB is unaffected because of
  `ON CONFLICT`)

## Architecture at a glance

```
                ┌───────────────┐
                │  seed loader  │   reads ~/.config/swoomps/seeds/*.txt
                └───────┬───────┘
                        ▼
   ┌────────────────────────────────────┐
   │  target generator (chan Target)    │   seeds × ports
   └────────────────┬───────────────────┘
                    ▼
   ┌────────────────────────────────────┐
   │  worker pool (errgroup, N workers) │
   │  rate.Limiter (global QPS)         │
   │    per target:                     │
   │      net.DialTimeout (TCP)         │
   │      if open:                      │
   │        slp.Ping (modern)            │
   │        on RST:                      │
   │          slp.PingLegacy (1.6)       │
   │        on success:                 │
   │          -> chan Result            │
   └────────────────┬───────────────────┘
                    ▼
   ┌────────────────────────────────────┐
   │  DB writer (single goroutine)      │   storage.Repository.UpsertServer
   │  + stdout lipgloss-style print     │   ON CONFLICT (ip, port) DO UPDATE
   └────────────────────────────────────┘
```

## File structure

```
swoomps/
├── cmd/swoomps/main.go              # entry point → cli.Run
├── internal/
│   ├── cli/                          # flag parsing + dispatch
│   │   ├── dispatch.go               # top-level command router
│   │   ├── probe.go                  # `swoomps probe` handler (REAL)
│   │   ├── scan.go                   # stub: "not implemented yet"
│   │   ├── tui.go                    # stub
│   │   ├── list.go                   # stub
│   │   ├── seed.go                   # stub
│   │   └── init.go                   # stub
│   ├── scanner/                      # network orchestration
│   │   ├── seeds.go                 # LoadSeeds(dir) → []model.Seed
│   │   ├── seeds_test.go
│   │   ├── portrange.go             # ParsePortRange("1-65535") → (lo, hi)
│   │   ├── portrange_test.go
│   │   ├── portscan.go              # TCP Probe; ECONNREFUSED detection
│   │   ├── portscan_test.go
│   │   ├── pool.go                  # Run(ctx, PoolConfig, targets) → Stats
│   │   ├── pool_test.go             # duplicate-processing test
│   │   └── scanner.go               # RunScan — top-level
│   ├── protocol/
│   │   ├── slp/
│   │   │   ├── packet.go            # varint, string, ushort
│   │   │   ├── client.go            # Ping — modern (1.7+) SLP
│   │   │   ├── parser.go            # Response + Parse + flattenDescription
│   │   │   ├── legacy.go            # PingLegacy — 1.6 SLP
│   │   │   ├── client_test.go
│   │   │   ├── legacy_test.go
│   │   │   └── integration_test.go  # //go:build integration
│   │   └── query/                   # stub (phase 3, deferred)
│   ├── storage/                     # SQLite
│   │   ├── sqlite.go                # DB type, Open, Close, WAL
│   │   ├── schema.go                # migrations + EnsureSchema
│   │   ├── repo.go                  # Repository: Upsert/List/GetStats
│   │   └── repo_test.go
│   ├── model/
│   │   ├── server.go                # Target, Server, SamplePlayer, Plugin, Seed
│   │   └── server_test.go
│   ├── config/
│   │   ├── config.go                # Config struct, Load, LoadFrom, XDG paths
│   │   └── config_test.go
│   └── tui/                         # stub (phase 9, deferred)
├── seeds/default.txt                # 20 well-known public servers
├── docs/
│   ├── README.md
│   ├── plan.md
│   ├── architecture.md
│   ├── protocols.md
│   ├── storage.md
│   ├── quickstart.md
│   └── ethics.md
├── go.mod / go.sum
├── Makefile
├── README.md
└── STATE.md                         # this file
```

## How to use

### Build

```bash
make build
# → bin/swoomps
```

### First run

```bash
mkdir -p ~/.config/swoomps/seeds
echo "mc.hypixel.net" > ~/.config/swoomps/seeds/minecraft.txt
./bin/swoomps probe --ports 25565 --qps 50 --workers 50 --timeout 3s
```

### Common commands

```bash
# Smoke test (just port 25565)
./bin/swoomps probe --ports 25565 --qps 50

# Minecraft port range
./bin/swoomps probe --ports 25500-25600 --qps 200 --workers 200 --timeout 1s

# Full 1-65535 (slow on filtered IPs)
./bin/swoomps probe --ports 1-65535 --qps 300 --workers 300 --timeout 1s

# Show all per-target error lines (verbose)
./bin/swoomps probe --ports 1-65535 -v

# Short flag alias
./bin/swoomps probe --ports 1-65535 -v
```

### Query the database

```bash
# All servers, most recent first
sqlite3 -header -column ~/.local/share/swoomps/swoomps.db \
  "SELECT ip, port, version_name, num_players, max_players, last_seen
   FROM servers ORDER BY last_seen DESC;"

# Top by players
sqlite3 -header -column ~/.local/share/swoomps/swoomps.db \
  "SELECT ip, port, version_name, num_players
   FROM servers ORDER BY num_players DESC LIMIT 20;"

# Export to CSV
sqlite3 -header -csv ~/.local/share/swoomps/swoomps.db \
  "SELECT * FROM servers;" > servers.csv
```

## Configuration

- **Config file**: `~/.config/swoomps/config.yaml` (optional, auto-created on demand)
- **Seeds dir**: `~/.config/swoomps/seeds/` (every `.txt` file is parsed)
- **Database**: `~/.local/share/swoomps/swoomps.db` (WAL mode, pure-Go driver)

Defaults (from `config.Load`):
- `qps = 50`
- `workers = 64`
- `timeout = 3s`
- `ports = 1-65535`
- `enable_query = true`
- `enable_expansion = true`

Env override: `SWOOMPS_QPS`, `SWOOMPS_WORKERS`, `SWOOMPS_TIMEOUT`,
`SWOOMPS_PORTS`, etc.

## Database schema

```sql
CREATE TABLE servers (
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
  sample_players   TEXT,    -- JSON
  query_enabled    BOOLEAN,
  plugins          TEXT,    -- JSON (always NULL — query not implemented)
  country          TEXT,    -- always NULL — GeoIP not implemented
  raw_query        TEXT,
  probe_kind       TEXT,    -- 'slp' | 'slp-legacy'
  UNIQUE(ip, port)
);
-- + indexes on last_seen, version, players, country, port
```

## Skills loaded (8)

Always available in any session:
- `golang-code-style`
- `golang-error-handling`
- `golang-concurrency`
- `golang-spf13-viper`
- `golang-performance`
- `golang-testing`
- `bubbletea`
- `charm-stack`

If you start a fresh session and these are not loaded, the user must
reinstall them (see `docs/` or run `npx skills add ...`).

## Decisions log

The full rationale lives in `docs/architecture.md`. Key choices that
shaped the code:

1. **Go + Charm stack** (no Cobra). The CLI is small enough that the
   stdlib `flag` package is sufficient; Charm covers the TUI later.
2. **Pure-Go SQLite** (`modernc.org/sqlite`) so there is no CGO toolchain.
3. **XDG paths** for config, seeds, database.
4. **Hybrid discovery**: seeds × port range. No wide-range IPv4
   scanning (per `docs/ethics.md`).
5. **1.7+ SLP only** with a 1.6 legacy fallback when the modern
   handshake RSTs.
6. **errgroup + rate.Limiter** for the worker pool. `errgroup.SetLimit`
   replaces hand-rolled pools.
7. **`UNIQUE(ip, port)`** on the servers table — each (ip, port) is one
   row; conflict updates `last_seen` and metadata.
8. **Flag-based CLI**: `swoomps probe [flags]` etc. Subcommand stubs
   (`scan`, `tui`, `list`, `seed`, `init`) remain in the dispatcher
   but are not implemented yet.

## Known issues / sharp edges

- **Slow on filtered IPs**. When most ports are filtered (timeout)
  instead of refused (RST), the scan takes minutes per IP because
  every probe waits for the full `--timeout` duration. There is no
  shortcut for "give up early on this IP".
- **Duplicate `[+]` prints** observed in one long run. Cause not
  yet identified; unit test (`TestRunProcessesEachTargetOnce`)
  shows the pool processes each target exactly once. The DB is
  unaffected because of `ON CONFLICT (ip, port) DO UPDATE`.
- **CLI dispatcher has stub subcommands** (`scan`, `tui`, `list`,
  `seed`, `init`) that just print "not implemented yet". The user
  should use `probe` for now.
- **No `init` subcommand** — config and DB are created lazily on
  first scan. There is no `init` to verify the setup.

## What we were doing last session

1. Implemented the vertical slice end-to-end (fases 1-5 + 6 parcial
   + 7 parcial + legacy SLP).
2. Ran a real scan against the user's 5 IPs. Found 14+ unique
   Minecraft servers on `147.185.221.211` (Paper, Velocity, Purpur,
   vanilla) on random high ports (6900-33310).
3. Added `--verbose`/`-v` flag to suppress noisy `[-]` lines.
4. Wrote extensive docs in `docs/` and `README.md`.

## Where to go next (open questions for the user)

The user said "agora vamos focar em aperfeiçoar oque a gente tem" (let's
now focus on perfecting what we have). Candidates:

1. **Replace stub subcommands with real implementations** (or remove
   them): `init`, `seed add/list/remove`, `list` (query DB).
2. **Lipgloss styling** on the `[+]` and `[-]` lines for a polished
   look.
3. **Stats panel** in CLI output: per-IP table at the end showing
   found servers grouped by seed.
4. **Smart rate limiting** that backs off when a target consistently
   RSTs.
5. **Deduplication hardening** in the pool to chase down the
   reported duplicates.
6. **Pre-flight probe**: detect "all ports filtered" early and skip
   the IP.
7. **Better error classification**: distinguish "host unreachable"
   vs "no service" vs "firewalled" in the summary.
8. **First-class `--list` command** that prints a styled table
   directly (no `sqlite3` shell).
9. **Tests for the pool** with a real SLP mock server (a TCP
   listener that emits a fake SLP response).

## Quick reference (TL;DR)

```bash
# build
make build

# run scan (default flags: --ports 1-65535 --qps 50 --workers 64)
./bin/swoomps probe

# run scan (faster, narrower)
./bin/swoomps probe --ports 25500-25600 --qps 200 --workers 200 --timeout 1s

# run scan (show all per-target lines)
./bin/swoomps probe -v

# stop
Ctrl-C

# query DB
sqlite3 ~/.local/share/swoomps/swoomps.db "SELECT * FROM servers;"
```
