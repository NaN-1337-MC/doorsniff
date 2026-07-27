# Plan

## Vision

`doorsniff` is a Minecraft server scanner written in Go. Given a list of
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

- TUI dashboard behind `doorsniff tui` (Bubbletea).
- Query protocol (UDP) — basic and full stat, with plugin list.
- Offline-mode detection: after SLP finds a server, attempt a Login
  handshake to determine online-mode (premium) vs offline-mode (cracked).
  Detects whitelist/ban via disconnect messages.
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
| 4 | Query protocol | done | UDP handshake + full stat parser, FakeServer, worker integration |
| 5 | Port scanner | done | TCP connect probe with rate limit, errgroup worker pool |
| 6 | Scanner core | done | target generator (seeds × ports), CIDR expansion, worker pool |
| 7 | CLI subcommands | done | `probe`, `list`, `init`, `seed`; `scan` and `tui` are stubs |
| 8 | Storage | done | upsert with port + probe_kind; `list` with 7 filters |
| 9 | Polish | done | sticky progress bar, per-IP bar, `--resume`, ASCII mode, seeds.txt population |
| 10 | TUI dashboard | pending | Bubbletea dashboard behind `doorsniff tui` |
| 11 | Offline-mode detection | done | Login handshake to detect cracked/premium/whitelisted servers |

## Current focus

Query protocol (Phase 4) is complete:

- `internal/protocol/query/` — UDP client: handshake + full-stat
  request, `Response` struct, `ParseFullStat` parser, `ParsePlugins`
  for Bukkit-format plugin strings.
- `internal/protocol/query/querytest/` — `FakeServer` test double
  speaking real Query protocol (handshake + full stat).
- Worker integration: after SLP success, Query is called to enrich
  server data with `map`, `gametype`, `plugins`, and `player_names`.
  `ProbeKind` set to `slp+query` when Query responds.
- Migration v3: `map` and `gametype` columns added to `servers` table.
- `model.Server` updated with `Map` and `GameType` fields.
- E2E tests: 4 tests covering full stat, timeout, plugin parsing,
  and empty server.

## Decision logs

The interesting decisions are written down in
[architecture.md](./architecture.md#decisions). The plan itself
intentionally does not restate every "why" — that lives in the
architecture doc.

## Versioning

There is no version yet. The binary prints `doorsniff dev (no version yet)`
on `doorsniff version`. Tags will start once the TUI lands.
