# swoomps

> Minecraft server scanner written in Go.

`swoomps` reads a list of seed IPs (or CIDR ranges), scans a
configurable port range, checks each open port for a Minecraft
server (modern Server List Ping, with a 1.6 fallback for
BungeeCord-style proxies), persists the results to a local SQLite
database, and prints each finding live to a styled terminal.

## Status

**Phases 0-8 complete, 9 partial.** The `probe` and `list`
subcommands are stable. `scan`, `tui`, `init`, and `seed` are
reserved but not yet implemented. Resume, per-IP progress bar, and
CIDR expansion in seed files all work. See
[docs/plan.md](docs/plan.md) for the roadmap.

## Features

- Subcommand CLI: `swoomps probe` and `swoomps list`
- Configurable port range (default `1-65535`)
- TCP connect probe with rate limit (default 200 QPS, 256 workers)
- Server List Ping (modern 1.7+) with JSON response
- Legacy SLP (1.6) as a fallback when the modern handshake is RST'd
  by a proxy
- CIDR expansion in seed files (`69.9.176.0/20` becomes 4096 seeds)
- Local SQLite storage (pure-Go driver, no CGO)
- Live terminal output, lipgloss styled
- Two-line sticky progress bar: per-IP + overall
- Resume support: `--resume` skips seeds that were fully scanned
  in a previous run
- Seven filters on `swoomps list` (version, engine, players, port,
  MOTD, sort, limit)
- ASCII mode for plain terminals (`--ascii`)
- XDG-compliant config and database paths
- Cross-platform (Linux, macOS, Windows) — single static binary

## Quick start

```bash
go build -o swoomps ./cmd/swoomps
./swoomps probe --file ./seed.txt
./swoomps list
```

A fuller walkthrough lives in [docs/quickstart.md](docs/quickstart.md).

## `swoomps probe`

| Flag | Default | Purpose |
|---|---|---|
| `--ports RANGE` | `1-65535` | Port range |
| `--qps N` | `200` | Global QPS limit |
| `--workers N` | `256` | Worker pool size |
| `--timeout DUR` | `1s` | Per-probe timeout |
| `--file PATH` | — | Read seeds from a single file |
| `--seeds DIR` | XDG default | Override seeds directory |
| `--database PATH` | XDG default | Override database path |
| `--resume` | `false` | Skip seeds fully scanned in a previous run |
| `--verbose`, `-v` | `false` | Per-target error lines + debug logging |
| `--ascii` | `false` | Plain output (no colour, ASCII bar) |

`Ctrl+C` stops the scan. Use `--resume` to continue from the last
checkpoint.

## `swoomps list`

| Flag | Default | Purpose |
|---|---|---|
| `--version VER` | — | Comma-separated version substrings, OR'd |
| `--engine ENG` | — | Engine substring, case-insensitive |
| `--players EXPR` | — | Player predicate (`>1`, `<50`, `1-10`, `=5`) |
| `--maxplayers EXPR` | — | Max players predicate (`>=100`, `50-200`, `<500`) |
| `--port PORT` | — | Port or port range |
| `--motd TEXT` | — | MOTD substring search |
| `--sort KEY` | `recent` | Sort: `players`, `latency`, `version`, `port`, `recent`, `oldest` |
| `--limit N` | `50` | Max results |
| `--database PATH` | XDG default | Override database path |

## Documentation

Long-form docs live in [docs/](docs/README.md):

- [plan.md](docs/plan.md) — roadmap and phases
- [architecture.md](docs/architecture.md) — stack, decisions, module layout, known tech debt
- [protocols.md](docs/protocols.md) — modern SLP and legacy 1.6 fallback
- [storage.md](docs/storage.md) — SQLite schema, migrations, repository
- [quickstart.md](docs/quickstart.md) — build, run, subcommand examples
- [ethics.md](docs/ethics.md) — legal and ethical considerations

## Development

```bash
go build ./...            # compile
go test ./...             # run tests
go test -race ./...       # with race detector
go vet ./...              # static analysis
go mod tidy               # clean module graph
```

## Planned

- TUI dashboard behind `swoomps tui` (Bubbletea)
- Query protocol (UDP) — basic and full stat
- GeoIP enrichment of found servers
- JSON / CSV export from `swoomps list`
- Per-host blacklist file

## License

Personal project — license TBD.
