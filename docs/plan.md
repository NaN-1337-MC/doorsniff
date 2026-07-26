# Plan

## Vision

`swoomps` is a Minecraft server scanner written in Go. Given a list of
seed IPs, it scans a configurable port range, checks each open port for a
Minecraft server (SLP + Query), persists the results to a local SQLite
database, and prints each finding live to the terminal.

The project is a learning artifact: the goal is to internalise Go's
concurrency model, the Minecraft wire protocols, and the Charm TUI stack
on a real, useful codebase.

## Scope

In scope (MVP):

- Seed list (file or inline).
- Configurable port range (e.g. `1-65535`).
- TCP connect probe with timeout.
- Server List Ping (TCP/25565-or-wherever-it-is) — modern 1.7+ handshake.
- Query protocol (UDP/25565) — basic and full stat.
- Bounded CIDR expansion (neighbours of seed IPs).
- Local SQLite persistence (pure-Go driver, no CGO).
- Live CLI output (lipgloss styled) of found servers.
- Flag-based CLI (no subcommands in the MVP).
- Distributed-ready architecture (errgroup + rate.Limiter).

Out of scope (for the MVP):

- Legacy SLP from 1.6 and earlier.
- RCON (admin remote command).
- TUI dashboard. The TUI is **deferred** to phase 9 — `swoomps` with no
  flag will open the dashboard once it exists.
- Web UI or cloud component.
- Random IPv4 wide-range scanning.

## Phases

| # | Phase | Status | Notes |
|---|---|---|---|
| 0 | Setup | done | Module init, dirs, deps, dispatcher, stubs, Makefile, .gitignore, README |
| 1 | Foundation | pending | `model.Server`, viper config, SQLite storage + migrations |
| 2 | SLP protocol | pending | TCP 1.7+ handshake, response parser |
| 3 | Query protocol | pending | UDP handshake, basic + full stat parsers |
| 4 | Port scanner | pending | TCP connect probe with rate limit; SIGPIPE-safe |
| 5 | Scanner core | pending | target generator (seeds × ports), CIDR expansion, worker pool |
| 6 | CLI flags | pending | refactor dispatcher to flag-based CLI on root |
| 7 | Storage | pending | upsert with port + probe_kind; query paths from `list` |
| 8 | Polish | pending | seeds.txt population, smoke tests, README |
| 9 | TUI (deferred) | pending | Bubble Tea dashboard behind `swoomps` (no flag) |

## Current focus

Documentation reflection of the MVP. Once docs are aligned, the next
phase is **1 (Foundation)** — three deliverables:

1. `internal/model/server.go` — the unified `Server` value type.
2. `internal/config/config.go` — viper-loaded configuration with XDG paths
   and `SWOOMPS_*` env overrides.
3. `internal/storage/sqlite.go` — `modernc.org/sqlite` driver, schema
   migrations, and a repository with `UpsertServer`, `ListServers`,
   `GetStats`.

## Decision logs

The interesting decisions are written down in [architecture.md](./architecture.md).
The plan itself intentionally does not restate every "why" — that lives in
the architecture doc.

## Versioning

There is no version yet. The binary prints `swoomps dev (no version yet)`
on `swoomps --version`. Tags will start once phase 8 lands.
