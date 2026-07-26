# Plan

## Vision

`swoomps` is a Minecraft server scanner written in Go. Given a list of
seed IPs (or CIDR ranges), it scans a configurable port range, checks
each open port for a Minecraft server (SLP, with a legacy 1.6 fallback
for BungeeCord-style proxies), persists the results to a local SQLite
database, and prints each finding live to a styled terminal.

The project is a learning artifact: the goal is to internalise Go's
concurrency model, the Minecraft wire protocols, and the Charm TUI stack
on a real, useful codebase.

## Scope

In scope (current):

- Seed list from file or directory; one line per seed or CIDR.
- CIDR expansion: `69.9.176.0/20` in a seed file expands to 4096
  individual IPs, each scanned independently.
- Configurable port range (`1-65535`, `25565`, `25500-25600`).
- TCP connect probe with timeout.
- Server List Ping (TCP) — modern 1.7+ handshake + JSON response.
- Legacy SLP fallback (1.6) when the modern handshake is RST'd by a
  proxy (BungeeCord / Velocity with `ping_passthrough` disabled).
- Bounded, configurable QPS (token-bucket `rate.Limiter`).
- Worker pool with `errgroup.SetLimit`.
- Local SQLite persistence (pure-Go driver, no CGO).
- Per-IP and overall progress bars rendered with `lipgloss` and
  `termenv` cursor control.
- Resume support: progress is checkpointed per seed, so an interrupted
  scan can be continued with `--resume`.
- Subcommand CLI: `probe`, `list`, `scan`, `tui`, `init`, `seed`.
- Found-server cards with version, player count, latency, MOTD.

In scope (planned):

- TUI dashboard behind `swoomps tui` (Bubbletea).
- Query protocol (UDP) — basic and full stat, with plugin list.
- GeoIP enrichment of found servers.
- JSON / CSV export.

Out of scope:

- Legacy SLP for *very* old servers (1.4 and earlier).
- RCON (admin remote command).
- Cloud component, distributed scanner, web UI.
- Random IPv4 wide-range scanning. The user provides seed IPs; CIDR
  expansion is opt-in and the user controls its size.

## Phases

| # | Phase | Status | Notes |
|---|---|---|---|
| 0 | Setup | done | Module init, dirs, deps, dispatcher, stubs, Makefile, .gitignore, README |
| 1 | Foundation | done | `model.Server`, `config` package, SQLite storage + migrations |
| 2 | SLP protocol | done | TCP 1.7+ handshake, JSON response parser |
| 3 | Legacy SLP | done | 1.6 ping used as fallback when modern handshake RSTs |
| 4 | Query protocol | pending | UDP handshake, basic + full stat parsers |
| 5 | Port scanner | done | TCP connect probe with rate limit, errgroup worker pool |
| 6 | Scanner core | done | target generator (seeds × ports), CIDR expansion, worker pool |
| 7 | CLI subcommands | done | `probe`, `list`, `init`, `seed`; `scan` and `tui` are stubs |
| 8 | Storage | done | upsert with port + probe_kind; `list` with 7 filters |
| 9 | Polish | done | sticky progress bar, per-IP bar, `--resume`, ASCII mode, seeds.txt population |
| 10 | TUI dashboard | pending | Bubbletea dashboard behind `swoomps tui` |

## Current focus

Documentation reflection of the current code, plus a refactoring pass
to address the technical debt surfaced by the feature freeze review
(see [architecture.md](./architecture.md#known-tech-debt)).

The scanner refactor is complete:

1. ✅ `Stats` passed by pointer everywhere (no `go vet` warnings).
2. ✅ `ProgressState` struct extracted (generator ↔ renderer decoupled).
3. ✅ `worker` struct replaces 12-param `runWorker` function.
4. ✅ `Options.Timeout` is `time.Duration` throughout (default 1s).
5. ✅ `TestRunProcessesEachTargetOnce` uses `ProcessedHook` callback.
6. ✅ `Options.Seeds` unified — `SeedsDir` removed from scanner.

Next deliverable is E2E testing infrastructure (see `slptest/` package
and `//go:build integration` tests).

The E2E test suite is complete:

- `slptest/server.go` — `FakeServer` test double speaking real SLP
  (modern + legacy), with RST support for fallback testing.
- `scanner/e2e_test.go` — 5 tests: seed-file-to-DB, legacy fallback,
  port-closed, CIDR expansion (4 loopback servers), resume skip.
- `cli/e2e_test.go` — 2 tests: flag parsing (6 subcases), pipeline
  integration (`runAndSummarize` end-to-end).

## Decision logs

The interesting decisions are written down in
[architecture.md](./architecture.md#decisions). The plan itself
intentionally does not restate every "why" — that lives in the
architecture doc.

## Versioning

There is no version yet. The binary prints `swoomps dev (no version yet)`
on `swoomps version`. Tags will start once the TUI lands.
