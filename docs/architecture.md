# Architecture

## Goals

- **I/O concurrency first.** The scanner is fundamentally a network-heavy
  application. Go's goroutine model is the right fit.
- **No magic.** The codebase should be readable by a developer who has just
  finished a tour of "Tour of Go". No DI framework, no reflection, no code
  generation.
- **Unix-shaped.** The CLI is flag-driven; it reads from a seed file (or
  inline flags) and writes to a SQLite database and to stdout.
- **Distributed-ready.** The architecture should not block running many
  workers across many machines later, even if the MVP is a single binary.

## Stack

| Concern | Choice | Why |
|---|---|---|
| Language | Go (1.26) | I/O concurrency, single static binary, low memory footprint |
| CLI | `flag` (stdlib) | MVP surface is small; flags on the root keep the binary simple |
| TUI (deferred) | `bubbletea` + `lipgloss` | Elm architecture, declarative, beautiful defaults |
| Storage | `modernc.org/sqlite` | Pure-Go driver, no CGO toolchain required |
| Configuration | `viper` | Layered precedence (flag > env > file > default), `mapstructure` decoding |
| Concurrency | `golang.org/x/sync/errgroup` | Built-in `SetLimit` replaces hand-rolled worker pools |
| Rate limiting | `golang.org/x/time/rate` | Token bucket, configurable QPS |
| Logging | `log/slog` (stdlib) | Structured, color-aware, no extra deps |

## Why flag-based CLI

Cobra is the de facto standard for Go CLIs, but it pays for itself mainly
when a project has many subcommands, persistent flags, and shell
completions. `swoomps` is single-purpose: "scan a seed list, write
results". The MVP is a single binary with a handful of boolean and
value flags. The `flag` package on the root command is enough. The TUI
phase (9) will be wired into the root invocation when the time comes.

## Module layout

```
github.com/renanbray/swoomps
├── cmd/
│   └── swoomps/
│       └── main.go              # entry point; calls cli.Run
├── internal/
│   ├── cli/                     # flag parsing + dispatch
│   │   ├── root.go              # flag definitions, --scan, --list, --seed-add, etc.
│   │   ├── scan.go              # --scan handler
│   │   ├── list.go              # --list handler
│   │   ├── seed.go              # --seed-add / --seed-list / --seed-remove
│   │   └── init.go              # --init handler
│   ├── scanner/                 # orchestration
│   │   ├── scanner.go           # top-level Run(ctx)
│   │   ├── targets.go           # seed × port × expand target generator
│   │   ├── pool.go              # worker pool + rate limiter
│   │   ├── portscan.go          # TCP connect probe
│   │   └── expand.go            # CIDR /24 neighbour expansion
│   ├── protocol/
│   │   ├── slp/                 # Server List Ping (1.7+)
│   │   │   ├── client.go
│   │   │   ├── packet.go
│   │   │   └── parser.go
│   │   └── query/               # Query protocol
│   │       ├── client.go
│   │       ├── handshake.go
│   │       └── parser.go
│   ├── storage/                 # SQLite driver + repo
│   │   ├── sqlite.go
│   │   ├── schema.go
│   │   └── repo.go
│   ├── model/                   # domain types
│   │   └── server.go
│   ├── config/                  # viper setup
│   │   └── config.go
│   └── tui/                     # Bubble Tea dashboard (phase 9)
│       ├── model.go
│       ├── update.go
│       ├── view.go
│       └── styles.go
├── seeds/
│   └── default.txt
├── docs/
├── go.mod
├── go.sum
├── Makefile
├── README.md
└── .gitignore
```

`internal/` keeps the package surface from being importable by other
modules — appropriate for an application.

## MVP flow

```
                       FLAG PARSING (root)
                              |
                     CONFIG (viper) + DB INIT
                              |
                ┌─────────────┴─────────────┐
                |                           |
          LOAD SEEDS                  PRINT HEADER
        (file / inline)             (lipgloss styled)
                |
                ▼
   ┌──────────────────────────────┐
   │  TARGET GENERATOR            │
   │  for each seed IP:           │
   │    for each port in range:   │
   │      yield (ip, port)        │
   │  CIDR expansion (optional):  │
   │    for each /24 neighbour:   │
   │      yield (ip, port)        │
   └──────────────┬───────────────┘
                  ▼ chan Target (buf 1024)
   ┌──────────────────────────────────────────┐
   │  WORKER POOL (errgroup, SetLimit N)      │
   │  per target:                             │
   │    rate.Limiter.Wait()                   │
   │    TCP connect (timeout)                 │
   │    if closed:  drop                      │
   │    if open:                              │
   │        SLP ping                          │
   │        if Minecraft:                     │
   │            Query protocol (optional)     │
   │            yield Result                  │
   │            print to stdout (lipgloss)    │
   └──────────────┬───────────────────────────┘
                  ▼ chan Result (buf 256)
   ┌──────────────────────────────┐
   │  DB WRITER (single goroutine)│
   │  upsert into servers table   │
   └──────────────────────────────┘
                  |
                  ▼
   ┌──────────────────────────────┐
   │  AT END OF SCAN:             │
   │  print summary table         │
   │  totals / found / errors     │
   └──────────────────────────────┘
```

## Port scan strategy

A naive sequential scan of 65535 ports per IP would take days. The MVP
relies on three levers:

1. **`net.DialTimeout` with a short timeout (default 3s).** A closed port
   returns "connection refused" almost instantly; the wasted time is on
   filtered ports.
2. **Worker pool (`errgroup.SetLimit(N)`).** N concurrent dial attempts.
   Default 64; configurable via `--workers`.
3. **Global rate limiter (`rate.Limiter`).** Even with N workers, the
   effective QPS is bounded by `--qps` (default 50). The limiter
   prevents the scanner from being mistaken for a SYN flood.

The flow above means SLP is only attempted on ports that already
responded to TCP, so the expensive protocol parser never runs against
the 99% of closed ports.

## Concurrency model

- `errgroup.SetLimit(N)` is the worker pool's upper bound; no hand-rolled
  pool.
- `rate.Limiter` is shared across workers to enforce a global QPS budget.
- A `chan Target` carries targets from the generator to the pool.
- A single DB writer goroutine consumes `chan Result` and writes to
  SQLite. This serialises writes (correct for SQLite) without blocking
  workers.
- The CLI output is `lipgloss` styled, written to stdout directly from
  workers (`slog` to stderr). Workers do not call `fmt.Println` on
  hot paths — they hand a `Found` struct to a small printer goroutine
  driven by the same channel.

## Configuration precedence

Viper resolves in this order (first set wins):

1. explicit `Set` (programmatic)
2. bound flag
3. env var (`SWOOMPS_*`)
4. config file (`~/.config/swoomps/config.yaml`)
5. default in code

All three of `SetEnvPrefix("SWOOMPS")`, `SetEnvKeyReplacer(...)`, and
`AutomaticEnv()` are wired together — missing any one of them silently
breaks nested-key resolution.

## TUI design (deferred)

The dashboard is a single Bubble Tea program on the root invocation
(`swoomps` with no flag). It reads from the database and a shared
events channel. The four Golden Rules from the bubbletea skill apply:

- always account for borders in height calculations
- never auto-wrap in bordered panels — truncate text explicitly
- match mouse detection to layout orientation
- weight-based sizing, not pixel-based

The TUI is a pure consumer of the events channel; it never writes to the
database. This keeps the UI simple and the model testable.

## Error handling

- Errors are wrapped with `fmt.Errorf("context: %w", err)`.
- `errors.Is` / `errors.As` for sentinel / type matching.
- `errors.As(err, &viper.ConfigFileNotFoundError{})` is checked explicitly
  so a missing config file does not crash a binary that could run on env
  vars alone.
- Errors are logged with `log/slog` at the system boundary, otherwise
  returned. The single-handling rule is enforced: do not log and return.
- `errors.Join` (Go 1.20+) is used when several independent errors need
  to be aggregated (e.g. closing multiple storage handles).

## Testing

Smoke tests only, per the user's choice. Coverage areas:

- `slp/client_test.go` — one happy-path probe against a public server.
- `query/client_test.go` — one happy-path probe against a query-enabled
  server.
- `storage/repo_test.go` — insert + list round trip.
- `scanner/portscan_test.go` — TCP connect against a known-open port
  (e.g. `127.0.0.1:22` or a test server).
- `scanner/expand_test.go` — neighbour expansion from seed IPs.

Build tag `integration` is reserved for any future tests that depend on
network or external services.

## Distribution

Single static binary. `go build -o bin/swoomps ./cmd/swoomps`. No Docker
image in the MVP; the user has a Linux box and a Go toolchain.

## Future hooks

- A queue (`chan` or external broker) at the front of the worker pool
  would scale to multiple binaries running in parallel.
- A separate `cmd/indexer` could replay the SQLite database into a search
  index without touching the scanner.
- A `--format json` flag on `--list` would feed downstream pipelines.
- The TUI inherits the same data source, so adding it is additive.
