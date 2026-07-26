# Architecture

## Goals

- **I/O concurrency first.** The scanner is fundamentally a network-heavy
  application. Go's goroutine model is the right fit.
- **No magic.** The codebase should be readable by a developer who has
  just finished a tour of "Tour of Go". No DI framework, no reflection,
  no code generation.
- **Unix-shaped.** The CLI is subcommand-based; it reads from a seed
  file (or directory) and writes to a SQLite database and to stdout.
- **Distributed-ready.** The architecture should not block running many
  workers across many machines later, even if the MVP is a single
  binary.

## Stack

| Concern | Choice | Why |
|---|---|---|
| Language | Go (1.26) | I/O concurrency, single static binary, low memory footprint |
| CLI | `flag` (stdlib) per subcommand | Surface is bigger than the original MVP; one FlagSet per subcommand |
| TUI (planned) | `bubbletea` + `lipgloss` | Elm architecture, declarative, beautiful defaults |
| Storage | `modernc.org/sqlite` | Pure-Go driver, no CGO toolchain required |
| Configuration | `viper` | Layered precedence (flag > env > file > default), `mapstructure` decoding |
| Concurrency | `golang.org/x/sync/errgroup` | Built-in `SetLimit` replaces hand-rolled worker pools |
| Rate limiting | `golang.org/x/time/rate` | Token bucket, configurable QPS |
| Logging | `log/slog` (stdlib) | Structured, color-aware, no extra deps |
| Terminal styling | `charmbracelet/lipgloss` | `Style`, `table`, ANSI-aware padding |
| Terminal control | `muesli/termenv` | Cursor save/restore, clear-line for the sticky progress bar |

## Why subcommands

The CLI grew out of the original flag-based MVP. The current surface is
six top-level subcommands (`probe`, `scan`, `tui`, `list`, `seed`,
`init`), each with its own set of flags. A single root `FlagSet` would
have grown past 20 flags with overlaps and aliases. Subcommands keep
each surface small and self-contained, at the cost of typing
`swoomps probe` instead of `swoomps --scan`.

Cobra is the de-facto standard for this, but the surface is still
small enough that the `flag` package per subcommand wins on
dependencies. If a seventh subcommand is added, or shell completion
becomes a requirement, Cobra is the next move.

## Module layout

```
github.com/renanbray/swoomps
├── cmd/
│   └── swoomps/
│       └── main.go              # entry point; calls cli.Run
├── internal/
│   ├── cli/                     # subcommand surface
│   │   ├── dispatch.go          # routes `swoomps <cmd>` to the right handler
│   │   ├── probe.go             # `swoomps probe` — run a scan
│   │   ├── list.go              # `swoomps list` — query and display found servers
│   │   ├── listfilter.go        # filter parsers for `list`
│   │   ├── scan.go              # `swoomps scan` (stub)
│   │   ├── tui.go               # `swoomps tui` (stub)
│   │   ├── seed.go              # `swoomps seed` (stub)
│   │   └── init.go              # `swoomps init` (stub)
│   ├── scanner/                 # orchestration
│   │   ├── scanner.go           # RunScan, Options, generateTargets
│   │   ├── pool.go              # worker pool + rate limiter + Stats
│   │   ├── portscan.go          # TCP connect probe
│   │   ├── portrange.go         # parse "1-65535"
│   │   ├── render.go            # Styles, Ticker (sticky progress), Progress
│   │   └── seeds.go             # seed file parser + CIDR expansion
│   ├── protocol/
│   │   ├── slp/                 # Server List Ping (modern 1.7+ and legacy 1.6)
│   │   │   ├── client.go
│   │   │   ├── packet.go        # VarInt, string encoding
│   │   │   ├── parser.go
│   │   │   ├── legacy.go        # 1.6 ping (RST fallback)
│   │   │   └── slp.go
│   │   └── query/               # Query protocol (planned, not wired yet)
│   │       └── query.go
│   ├── storage/                 # SQLite driver + repo
│   │   ├── sqlite.go            # Open, WAL, Close
│   │   ├── schema.go            # migrations v1 (servers) and v2 (scan_progress)
│   │   └── repo.go              # Upsert, List, ListServersFiltered, GetStats,
│   │                            #   LoadProgress, SaveProgress
│   ├── model/                   # domain types
│   │   ├── server.go            # Server, Target, SamplePlayer, Plugin, Seed
│   │   └── server_test.go
│   ├── config/                  # viper setup + XDG paths
│   │   ├── config.go
│   │   └── config_test.go
│   └── tui/                     # Bubble Tea dashboard (planned)
│       └── tui.go
├── seeds/                       # bundled default seed files
├── docs/                        # long-form documentation
├── go.mod
├── go.sum
├── Makefile
├── README.md
└── .gitignore
```

`internal/` keeps the package surface from being importable by other
modules — appropriate for an application.

## Data flow (`swoomps probe`)

```
                        FLAG PARSING (probe.go)
                               |
                    CONFIG + DB OPEN (storage.Open + EnsureSchema)
                               |
                ┌──────────────┴──────────────┐
                |                             |
         LOAD SEEDS (seeds.go)         PRINT HEADER (lipgloss)
         (file or --file flag)        (seeds: / db: lines)
                |
                ▼
    ┌──────────────────────────────┐
    │  generateTargets (scanner)   │
    │  for each seed IP:           │
    │    for each port in range:   │
    │      yield (ip, port)        │
    │  resume: skip completed IPs, │
    │          restart interrupted │
    │          IP from lastPort+1  │
    └──────────────┬───────────────┘
                   ▼ chan model.Target (buf 1024)
    ┌──────────────────────────────────────────┐
    │  WORKER POOL (errgroup, SetLimit N)      │
    │  per target:                             │
    │    rate.Limiter.Wait()                   │
    │    TCP connect (timeout)                 │
    │    if closed:  drop                      │
    │    if open:                              │
    │        SLP ping (modern 1.7+)            │
    │        on RST → legacy SLP 1.6 fallback  │
    │        if Minecraft:                     │
    │            yield Result                  │
    │            print to stdout (lipgloss     │
    │            + termenv cursor control)     │
    └──────────────┬───────────────────────────┘
                   ▼ chan model.Server (buf 256)
    ┌──────────────────────────────┐
    │  DB WRITER (single goroutine)│
    │  upsert into servers table   │
    └──────────────────────────────┘
                   |
                   ▼
    ┌──────────────────────────────┐
    │  AT END OF SCAN:             │
    │  print summary block         │
    │  totals / found / errors     │
    └──────────────────────────────┘
```

`generateTargets` runs in a single goroutine. Workers consume from the
channel concurrently, but at any given moment all workers are scanning
ports of the same seed (because the channel is sequential per seed).
This is what makes the per-IP progress bar work — the generator
announces the active seed once per IP, resets the per-IP counter, and
workers only increment it.

## Port scan strategy

A naive sequential scan of 65535 ports per IP would take days. The MVP
relies on three levers:

1. **`net.Dialer` with a short timeout (default 1s).** A closed port
   returns "connection refused" almost instantly; the wasted time is on
   filtered ports.
2. **Worker pool (`errgroup.SetLimit(N)`).** N concurrent dial
   attempts. Default 256; configurable via `--workers`.
3. **Global rate limiter (`rate.Limiter`).** Even with N workers, the
   effective QPS is bounded by `--qps` (default 200). The limiter
   prevents the scanner from being mistaken for a SYN flood.

The flow above means SLP is only attempted on ports that already
responded to TCP, so the expensive protocol parser never runs against
the 99% of closed ports.

## Concurrency model

- `errgroup.SetLimit(N)` is the worker pool's upper bound; no
  hand-rolled pool.
- `rate.Limiter` is shared across workers to enforce a global QPS
  budget.
- A `chan model.Target` carries targets from the generator to the pool.
- A single DB writer goroutine consumes `chan model.Server` and writes
  to SQLite. This serialises writes (correct for SQLite) without
  blocking workers.
- The progress bar is a single `time.Ticker` goroutine that polls
  atomic counters and atomics shared with the generator
  (`CurrentSeed`, `CurrentSeedScanned`).
- Found-server cards are printed from workers via
  `Ticker.InsertAbove`, which uses `termenv` cursor control to slot the
  card above the sticky bar without scrolling it.

## Progress bar

The live output is a 2-line sticky block at the bottom of the terminal:

```
████░░░░░░░░░░░░░░░░░░░░░░░░░░  13.3%  (8730/65.5K)  scanning: 69.9.176.0
██████████████████████████████  100.0%  (268M/268M)  ETA 0:00  143 pps
```

- Line 1: per-IP progress. The `scanning:` label and IP sit at the
  end of the line, after the counter.
- Line 2: overall progress. ETA in `m:ss` or `h:mm:ss` format.

The bar characters are Unicode `█` / `░` by default; `--ascii`
switches to `#` / `.` for plain terminals.

`termenv.SaveCursorPosition` is called once after the two lines are
printed. On every refresh (`time.Ticker`, 500ms) the goroutine moves
the cursor up 2 lines, clears both, and reprints. `InsertAbove` does
the same move, prints the found-server card, and reprints the bars.

## Resume

When `swoomps probe --resume` is invoked, the scanner:

1. Loads `scan_progress` into a `map[seed_ip]last_port` from the DB.
2. Strips fully-completed seeds from the seed list.
3. Passes the progress map to `generateTargets`. The generator:
   - Skips seeds whose `last_port >= hi` (fully done).
   - Restarts an interrupted seed from `last_port+1`.
4. Calls `onSeedDone(seed_ip, last_port)` after every seed finishes
   its last port, which writes the checkpoint back to the DB.

A single goroutine owns the reset of `CurrentSeedScanned` (the
per-IP counter). Workers only increment. This is the design choice
that prevents the per-IP bar from getting stuck at 100% when many
workers transition to a new IP simultaneously.

## Configuration precedence

Viper resolves in this order (first set wins):

1. explicit `Set` (programmatic)
2. bound flag
3. env var (`SWOOMPS_*`)
4. config file (`~/.config/swoomps/config.yaml`)
5. default in code

`SetEnvPrefix("SWOOMPS")`, `SetEnvKeyReplacer(...)`, and
`AutomaticEnv()` are wired together — missing any one of them silently
breaks nested-key resolution.

## TUI design (planned)

The dashboard is a single Bubble Tea program under `swoomps tui`. It
reads from the database and from the in-memory `ProgressState` shared
by `swoomps probe`. The four Golden Rules from the bubbletea skill
apply:

- always account for borders in height calculations
- never auto-wrap in bordered panels — truncate text explicitly
- match mouse detection to layout orientation
- weight-based sizing, not pixel-based

The TUI is a pure consumer of the events channel; it never writes to
the database. This keeps the UI simple and the model testable.

## Error handling

- Errors are wrapped with `fmt.Errorf("context: %w", err)`.
- `errors.Is` / `errors.As` for sentinel / type matching.
- `errors.As(err, &viper.ConfigFileNotFoundError{})` is checked
  explicitly so a missing config file does not crash a binary that
  could run on env vars alone.
- Errors are logged with `log/slog` at the system boundary, otherwise
  returned. The single-handling rule is enforced: do not log and
  return.
- `errors.Join` (Go 1.20+) is used when several independent errors
  need to be aggregated.

## Known tech debt

The scanner package is internally consistent but has accumulated some
debt during the feature freeze. The items below are tracked in
[plan.md](./plan.md#current-focus) and slated for the next refactor
sprint.

- `Stats` carries `atomic.Int64` and must be passed by pointer, never
  copied. Three `go vet` warnings today; latent data race the moment
  another atomic (or a mutex) is added.
- `PoolConfig` is a catch-all (14 fields). `CurrentSeed` and
  `CurrentSeedScanned` are cross-cutting (generator ↔ renderer) and
  belong in a dedicated `ProgressState` struct.
- `runWorker` has 11 parameters. A `worker` struct would make
  per-feature additions (e.g. an `--on-found webhook` hook) a
  one-line change.
- `Options.Timeout string` and `PoolConfig.Timeout time.Duration`
  disagree on the default (3s vs 1s). Use one type throughout.
- `pool_test.go` calls `Run` without a progress bar and panics on
  `currentSeedScanned.Add(1)`. Either nil-check or always
  initialise.

## Testing

- `model/server_test.go` — `Address()` and `IsZero()` for all types.
- `protocol/slp/client_test.go` — one happy-path probe against a
  public server (build tag `integration`).
- `protocol/slp/legacy_test.go` — one happy-path probe for the 1.6
  fallback.
- `storage/repo_test.go` — insert + list round trip.
- `scanner/portscan_test.go` — TCP connect against a known-open
  port.
- `scanner/portrange_test.go` — `"1-65535"`, `"25565"`, `"25500-25600"`.
- `scanner/seeds_test.go` — seed file parsing, CIDR expansion.
- `scanner/pool_test.go` — every target is processed exactly once
  with high concurrency.
- `config/config_test.go` — env, file, flag precedence.

Build tag `integration` is reserved for any tests that depend on the
network or external services.

## Distribution

Single static binary. `go build -o swoomps ./cmd/swoomps`. No Docker
image yet; the user has a Linux box and a Go toolchain.

## Future hooks

- A queue (`chan` or external broker) at the front of the worker pool
  would scale to multiple binaries running in parallel.
- A separate `cmd/indexer` could replay the SQLite database into a
  search index without touching the scanner.
- A `--format json` flag on `swoomps list` would feed downstream
  pipelines.
- The TUI inherits the same data source, so adding it is additive.
- An `--on-found` webhook hook would need `worker` to be a struct,
  not a function — which is why the refactor matters.

## Decisions

- **Subcommand CLI over root flags.** Surface grew past 15 root flags;
  per-subcommand `FlagSet` keeps each one short.
- **Pure-Go SQLite (`modernc.org/sqlite`).** Avoids the CGO toolchain
  for the build.
- **`errgroup.SetLimit` over hand-rolled pool.** Stdlib-adjacent; the
  alternative is reinventing the wheel.
- **Two-line progress bar.** Per-IP + overall is more useful than just
  overall, and the second line costs little.
- **Resume is opt-in (`--resume`).** Default is "always start fresh";
  users who want it know to ask.
- **Legacy SLP as a RST fallback, not a parallel probe.** Saves a
  round-trip on the common case; the 1.6 path only runs when the
  modern path is provably broken (RST).
- **Stats is "flat" — `Probed`/`Found`/`Errors`/`Duration` only.**
  Anything richer (e.g. per-IP) belongs in `ProgressState`, not here.
