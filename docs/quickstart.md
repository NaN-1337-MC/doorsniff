# Quickstart

## Requirements

- Go 1.26+ (`go version`)
- A POSIX shell (bash / zsh)
- ~50 MB of disk for the SQLite database and its growth room

## Build

```bash
go build -o swoomps ./cmd/swoomps
```

The binary lands at `./swoomps`. To install it system-wide:

```bash
go build -o swoomps ./cmd/swoomps
sudo cp swoomps /usr/local/bin/
```

Or `go install ./cmd/swoomps` to drop it into `$GOBIN`.

## Subcommand CLI

The CLI is subcommand-based. Every action is a subcommand of
`swoomps`:

| Subcommand | Purpose |
|---|---|
| `swoomps probe` | Run a scan against the seeds directory or a file |
| `swoomps list` | Query the database of found servers |
| `swoomps update` | Re-probe known servers to refresh their data |
| `swoomps init` | Initialize config and database |
| `swoomps tui` | Live dashboard (em breve) |
| `swoomps seed` | Manage seeds (em breve) |
| `swoomps version` | Print version |
| `swoomps help <sub>` | Subcommand-specific help |

`tui` and `seed` are stubs. They print a "not implemented yet"
message today. The stable surface is `probe`, `list`, `update`, and
`init`.

## `swoomps probe`

Runs a scan.

| Flag | Default | Purpose |
|---|---|---|
| `--ports RANGE` | `1-65535` | Port range (`25565`, `25500-25600`, `1-65535`) |
| `--qps N` | `200` | Global QPS limit |
| `--workers N` | `256` | Worker pool size |
| `--timeout DUR` | `1s` | Per-probe timeout |
| `--file PATH` | — | Read seeds from a single file (overrides `--seeds`) |
| `--seeds DIR` | XDG default | Override seeds directory |
| `--database PATH` | XDG default | Override database path |
| `--resume` | `false` | Skip seeds that were fully scanned in a previous run |
| `--verbose`, `-v` | `false` | Per-target error lines + debug logging |
| `--ascii` | `false` | Plain output (no colour, ASCII bar) |

### Examples

```bash
# Run a scan against all seeds in the XDG seeds directory.
swoomps probe

# Run a scan against a single seed file.
swoomps probe --file ./seed.txt

# Narrow the port range and lower the QPS.
swoomps probe --file ./seed.txt --ports 25565 --qps 50

# Full port range with 256 workers and 1s timeout.
swoomps probe --file ./seed.txt --ports 1-65535 --qps 200 --workers 256

# Resume an interrupted scan.
swoomps probe --file ./seed.txt --resume
```

A bundled `seed.txt` ships with hosting provider ranges. Each line is
either a host (`mc.example.com:25565`), an IP (`10.0.0.1`), or a CIDR
(`69.9.176.0/20`). CIDR entries are expanded to one seed per IP at
load time.

### Resume

Progress is checkpointed per seed IP. When a seed has scanned every
port in the range, a row is written to `scan_progress`. On
`--resume`:

- Seeds in `scan_progress` are skipped entirely.
- The seed that was in flight at the time of `Ctrl+C` is restarted
  from `last_port + 1` (and the first seed with no entry starts at
  port 1, as usual).

The worst case is re-scanning one seed (65535 ports). Compared to the
millions of targets in a typical CIDR bundle, this is negligible.

### Output

Each found server is printed to stdout in real time, with lipgloss
colours:

```
[+] 192.168.1.10:25565
   Version:  1.20.4    Players:  42/100    Latency:  87ms    Kind:  slp
   MOTD:     A Minecraft Server
```

A two-line sticky progress bar runs at the bottom of the terminal:

```
████░░░░░░░░░░░░░░░░░░░░░░░░░░  13.3%  (8730/65.5K)  scanning: 69.9.176.0
██████████████████████████████  100.0%  (268M/268M)  ETA 0:00  143 pps
```

Line 1 = per-IP progress. Line 2 = overall progress. The `scanning:`
label and IP sit at the end of the first line.

`Ctrl+C` stops the scan. Results written so far are persisted; use
`--resume` to continue.

## `swoomps list`

Queries and displays found servers. The output is a lipgloss-rendered
table with conditional colours (green when players > 0; latency on a
gradient from green to red).

| Flag | Default | Purpose |
|---|---|---|
| `--version VER` | — | Comma-separated version substrings, OR'd (e.g. `1.21.1,26.1.2`) |
| `--engine ENG` | — | Engine substring, case-insensitive (e.g. `paper`, `purpur`) |
| `--players EXPR` | — | Player predicate: `>1`, `<50`, `1-10`, `=5`, `5` (shorthand for `=5`) |
| `--maxplayers EXPR` | — | Max players predicate: `>=100`, `50-200`, `<500` |
| `--port PORT` | — | Port or port range (`25565` or `25500-25600`) |
| `--motd TEXT` | — | MOTD substring search |
| `--login MODE` | — | Login mode filter (`cracked`, `premium`, `whitelist`, `banned`, `kicked`, `full`) |
| `--sort KEY` | `recent` | Sort: `players`, `latency`, `version`, `port`, `recent`, `oldest`, `login` |
| `--limit N` | `50` | Max results |
| `--database PATH` | XDG default | Override database path |

### Table columns

| # | ADDRESS | VERSION | LOGIN | PLAYERS | LATENCY | LAST SEEN | MOTD | MAP |
|---|---------|---------|-------|---------|---------|-----------|------|-----|

- `#` — ordinal row number
- `LOGIN` — login mode: `cracked`, `premium`, `whitelist`, `banned`,
  `kicked`, `full`, `disconnect`, `-`
- `LAST SEEN` — `YYYY-MM-DD HH:MM` format

### Examples

```bash
# Last 50 servers seen.
swoomps list

# Paper servers with 10+ players, sorted by player count.
swoomps list --engine paper --players '>10' --sort players

# 1.21.1 servers on non-default ports.
swoomps list --version 1.21.1 --port '25566-26000'

# Servers with "survival" in the MOTD.
swoomps list --motd survival
```

## `swoomps update`

Re-probes known servers to refresh their data. Selects servers from
the database using the same filters as `list`, then re-dials each
one to get fresh player counts, MOTD, and version info.

| Flag | Default | Purpose |
|---|---|---|
| `--version VER` | — | Filter by version substring |
| `--engine ENG` | — | Filter by engine substring |
| `--players EXPR` | — | Filter by player count |
| `--maxplayers EXPR` | — | Filter by max players |
| `--port PORT` | — | Filter by port or range |
| `--motd TEXT` | — | Filter by MOTD substring |
| `--qps N` | `200` | Global QPS limit |
| `--workers N` | `64` | Worker pool size |
| `--limit N` | `0` | Max servers to update (0 = all) |
| `--login` | `false` | Perform login handshake to detect online/offline mode |
| `--login-user NAME` | `Herobrine` | Username for login handshake |
| `--debug-login` | `false` | Dump raw login packets to stderr |
| `--ascii` | `false` | Plain output (no colour, ASCII bar) |
| `--database PATH` | XDG default | Override database path |

### Examples

```bash
# Update all known servers.
swoomps update

# Update only Paper servers with players.
swoomps update --engine paper --players '>0'

# Update with login handshake detection.
swoomps update --login

# Update max 100 servers on non-default ports.
swoomps update --port '25500-25600' --limit 100
```

## Configuration

The config file is YAML at `~/.config/swoomps/config.yaml`. Keys:

```yaml
qps: 200
workers: 256
timeout: 1s
ports: 1-65535
database_path: /home/you/.local/share/swoomps/swoomps.db
seeds_dir: /home/you/.config/swoomps/seeds
enable_query: true
enable_expansion: true
verbose: false
```

Resolution order (first set wins): bound flag > env var (`SWOOMPS_*`)
> config file > default in code.

## Development

```bash
go build ./...            # compile
go test ./...             # run tests
go test -race ./...       # with race detector
go vet ./...              # static analysis
go mod tidy               # clean module graph
```

## Environment

| Env var | Maps to | Default |
|---|---|---|
| `SWOOMPS_QPS` | `qps` | 200 |
| `SWOOMPS_WORKERS` | `workers` | 256 |
| `SWOOMPS_TIMEOUT` | `timeout` | 1s |
| `SWOOMPS_PORTS` | `ports` | 1-65535 |
| `SWOOMPS_DATABASE_PATH` | `database_path` | `$XDG_DATA_HOME/swoomps/swoomps.db` |
| `SWOOMPS_SEEDS_DIR` | `seeds_dir` | `$XDG_CONFIG_HOME/swoomps/seeds` |
| `SWOOMPS_CONFIG_PATH` | `config_path` | `$XDG_CONFIG_HOME/swoomps/config.yaml` |
| `SWOOMPS_DATA_PATH` | (db dir) | `$XDG_DATA_HOME/swoomps` |
| `SWOOMPS_VERBOSE` | `verbose` | false |

## Troubleshooting

**"no such host" on a seed.** The DNS resolution failed. Check with
`dig mc.hypixel.net`. The seed is dropped, the scan continues.

**High error rate.** Lower `--qps`. Some servers and ISPs throttle
network probes heavily; the scanner keeps going but the hit rate
drops.

**Database locked.** Another process is writing. The binary uses WAL;
a rogue `sqlite3` shell holding a write transaction can lock writers.
Close the shell and retry.

**Slow scan over `1-65535`.** Expected. With 256 workers and 200 QPS,
a single IP takes 5-15 minutes depending on the network. Use
`--ports` to narrow the range. Use `--resume` to continue an
interrupted run without restarting from port 1.

**Stuck progress bar (one seed, `--resume`).** Verify with
`sqlite3 ~/.local/share/swoomps/swoomps.db "SELECT * FROM scan_progress;"`.
A row means that seed is fully done; an empty set means the first
`--resume` call hasn't completed a seed yet.
