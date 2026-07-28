# doorsniff

> Minecraft server scanner written in Go.

`doorsniff` takes a list of seed hosts (IPs, CIDR ranges, or
hostnames), scans a configurable port range against each one, and
checks every open port for a Minecraft server. Findings are saved to
a local SQLite database and printed live to the terminal as the scan
runs.

This is a learning project: the goal is to get hands-on with Go's
concurrency, the Minecraft wire protocols (SLP, legacy SLP, Login,
Query), and the Charm TUI stack on a real codebase.

> [!NOTE]
> Este projeto foi escrito majoritariamente com o auxílio de IA
> ("vibecode"). O código, os testes e boa parte da documentação foram
> gerados e revisados em parceria com um modelo de linguagem. Use por
> sua conta e risco; revise antes de confiar em qualquer parte para
> uso em produção.

## Quick start

```bash
# 1. Build
go build -o doorsniff ./cmd/doorsniff

# 2. Initialize config and database (~/.config/doorsniff,
#    ~/.local/share/doorsniff)
./doorsniff init

# 3. Drop a host into the seeds directory
echo "mc.hypixel.net" > ~/.config/doorsniff/seeds/minecraft.txt

# 4. Run a scan
./doorsniff probe --file ~/.config/doorsniff/seeds/minecraft.txt \
                  --ports 25565 --qps 50

# 5. List what was found
./doorsniff list
```

A fuller walkthrough lives in [docs/quickstart.md](docs/quickstart.md).

## How it works

```
   seeds/*.txt                      chan Target
        │                          (seed × port)
        ▼                               │
  ┌──────────────┐                      ▼
  │ seed loader  │            ┌──────────────────────┐
  │  + CIDR      │            │  worker pool         │
  │  expansion   │            │  (errgroup, N=256)   │
  └──────────────┘            │  + rate.Limiter      │
                              │                      │
                              │  TCP connect ──┐     │
                              │     │          │     │
                              │     ▼          │     │
                              │   SLP ping     │     │
                              │     │ RST →    │     │
                              │     ▼          │     │
                              │  legacy SLP    │     │
                              │     │          │     │
                              │     ▼          │     │
                              │  chan Result   │     │
                              └────────┬───────┘     │
                                       ▼             │
                              ┌──────────────────────┐
                              │  SQLite writer       │
                              │  + stdout (lipgloss) │
                              └──────────────────────┘
```

For each open TCP port, doorsniff tries the modern Server List Ping
(1.7+); if the server resets the modern handshake — a BungeeCord /
Velocity proxy with `ping_passthrough` disabled — it falls back to
the legacy 1.6 SLP. Servers that respond are persisted to SQLite and
printed to the terminal with version, player count, latency, and MOTD.

## Subcommands

| Command | What it does |
|---|---|
| `doorsniff probe` | Run a scan against the seeds directory or a file |
| `doorsniff list` | Query the database of found servers |
| `doorsniff update` | Re-probe known servers to refresh their data |
| `doorsniff init` | Create config file and database with defaults |
| `doorsniff version` | Print the version string |

Run `doorsniff help <command>` for the full flag list, or see
[docs/quickstart.md](docs/quickstart.md).

## Features

- **TCP port scan** with rate-limited worker pool (default 200 QPS,
  256 workers, 1 s per-probe timeout).
- **Modern SLP** (1.7+) with full JSON response parsing — version,
  description, players, sample player list, latency.
- **Legacy SLP fallback** (1.6) when a proxy resets the modern
  handshake.
- **Login handshake** to detect online / offline (cracked) /
  whitelisted / banned / full servers.
- **Query protocol** (UDP, full stat) — driver and parser are in
  place; `model.Server` carries `map` and `gametype` columns.
- **CIDR expansion**: `69.9.176.0/20` in a seed file becomes 4096
  individual seeds, each scanned independently.
- **Resume support**: `--resume` skips seeds that were fully scanned
  in a previous run; an interrupted seed restarts from
  `last_port + 1`.
- **Lipgloss + termenv** sticky two-line progress bar (per-IP +
  overall), styled result cards, and `--ascii` for plain terminals.
- **XDG paths**: config, seeds, and database under
  `$XDG_CONFIG_HOME/doorsniff/` and `$XDG_DATA_HOME/doorsniff/`.
- **Pure-Go SQLite** (`modernc.org/sqlite`), no CGO toolchain
  required.
- **Single static binary** that runs on Linux, macOS, and Windows.

## Configuration

Doorsniff reads three layers, in order of precedence:

1. CLI flags (`--ports`, `--qps`, `--workers`, …)
2. Environment variables (`SWOOMPS_QPS`, `SWOOMPS_WORKERS`, …)
3. Config file (`~/.config/doorsniff/config.yaml`)

The config file is created by `doorsniff init` with documented
defaults. Run `doorsniff help probe` (or `list`, or `update`) for the
full list of flags and their defaults.

## Use responsibly

Doorsniff speaks the same protocols the official Minecraft client
uses to populate the multiplayer server list. It is **not** an
exploitation or denial-of-service tool, but it still sends real
traffic to real hosts. Before running it:

1. **Stay below the noise floor.** The default rate limit is 200 QPS.
   Lower it with `--qps` when in doubt.
2. **Don't scan random IPv4 ranges.** The scanner only probes IPs
   you put in your seed files. CIDR expansion is opt-in and you
   control its size.
3. **Respect local law and the target's terms of service.** Some
   jurisdictions and hosting providers explicitly prohibit
   unsolicited scanning.
4. **Don't publish harvested lists.** The database is local.
5. **There is no auth, no brute force, no RCON, no exfiltration.**
   If a server wants to be excluded from your scans, edit your
   seed files.

For the full ethical position, see [docs/ethics.md](docs/ethics.md).

## Documentation

Long-form docs live under [docs/](docs/README.md):

| File | What's in it |
|---|---|
| [docs/quickstart.md](docs/quickstart.md) | Build, run, subcommand examples, env vars, troubleshooting |
| [docs/architecture.md](docs/architecture.md) | Stack, module layout, data flow, concurrency model, decisions |
| [docs/protocols.md](docs/protocols.md) | Modern SLP and legacy 1.6 fallback in detail |
| [docs/storage.md](docs/storage.md) | SQLite schema, migrations, repository queries |
| [docs/plan.md](docs/plan.md) | Roadmap and per-phase status |
| [docs/ethics.md](docs/ethics.md) | Legal and ethical considerations, opt-out status |
| [docs/lessons.md](docs/lessons.md) | Design and technical lessons from the refactor |

## Development

```bash
go build ./...            # compile
go test ./...             # run tests
go test -race ./...       # with the race detector
go vet ./...              # static analysis
go mod tidy               # clean module graph
```

Requires Go 1.26+. No CGO.

## Roadmap

- `doorsniff tui` — a Bubbletea dashboard over the live scan
- GeoIP enrichment of found servers
- JSON / CSV export from `doorsniff list`
- Per-host blacklist file

## License

Personal project — license TBD.
