# swoomps

> Minecraft server scanner written in Go.

`swoomps` reads a list of seed IPs, scans a configurable port range,
checks each open port for a Minecraft server (SLP + Query), persists
the results to a local SQLite database, and prints each finding live
to the terminal.

## Status

**Phase 0 — scaffold complete.** MVP scope is defined. The CLI is
flag-based; the TUI dashboard is deferred to phase 9. See
[docs/plan.md](docs/plan.md) for the roadmap.

## Features (MVP)

- Flag-based CLI: `swoomps --scan --seeds ... --ports ...`
- Configurable port range (default `1-65535`)
- TCP connect probe with rate limit (default 50 QPS)
- Server List Ping (1.7+) — protocol handshake + JSON response
- Query protocol (UDP) — basic and full stat
- Bounded CIDR expansion (the `/24` around each seed)
- Local SQLite storage (pure-Go driver, no CGO)
- Live CLI output, lipgloss styled
- Distributed-ready architecture (errgroup + rate.Limiter)

## Deferred

- TUI dashboard (phase 9): `swoomps` with no flag will open it.
- Legacy SLP from 1.6 and earlier.
- RCON.

## Quick start

```bash
make build
./bin/swoomps --help
```

A fuller walkthrough lives in [docs/quickstart.md](docs/quickstart.md).

## Flags

| Flag | Description |
|---|---|
| `--scan` | Run a scan |
| `--list` | Query and display found servers |
| `--seed-add HOST` | Add a seed host |
| `--seed-list` | List seeds |
| `--seed-remove ID` | Remove a seed by id |
| `--init` | Initialize config and database |
| `--seeds PATH_OR_LIST` | Seed file path or inline `host1,host2,...` |
| `--ports RANGE` | Port range, e.g. `1-65535`, `25565`, `25500-25600` |
| `--qps N` | Global rate limit (default 50) |
| `--workers N` | Worker pool size (default 64) |
| `--timeout DUR` | Per-probe timeout (default 3s) |
| `--no-query` | Skip Query protocol |
| `--no-expand` | Skip CIDR expansion |
| `--verbose` | Verbose logging |
| `--help` | Print help |
| `--version` | Print version |

## Documentation

Long-form docs live in [docs/](docs/README.md):

- [plan.md](docs/plan.md) — roadmap and phases
- [architecture.md](docs/architecture.md) — stack, decisions, module layout
- [protocols.md](docs/protocols.md) — SLP and Query protocol specs
- [storage.md](docs/storage.md) — SQLite schema and repository
- [quickstart.md](docs/quickstart.md) — build, run, dev workflow
- [ethics.md](docs/ethics.md) — legal and ethical considerations

## Development

```bash
make help          # list available make targets
make build         # compile binary
make test          # run smoke tests
make run ARGS=...  # convenience runner
```

## License

Personal project — license TBD.
