# Quickstart

## Requirements

- Go 1.26+ (`go version`)
- A POSIX shell (bash / zsh)
- ~50 MB of disk for the SQLite database and its growth room

## Build

```bash
make build
```

The binary lands at `bin/swoomps`. To install it system-wide:

```bash
make build
sudo cp bin/swoomps /usr/local/bin/
```

Or `go install ./cmd/swoomps` to drop it into `$GOBIN`.

## Flag-based CLI

The MVP is flag-based. There are no subcommands — every action is a
flag on the root:

| Flag | Purpose |
|---|---|
| `--scan` | Run a scan |
| `--list` | Query the database |
| `--seed-add HOST` | Add a seed host |
| `--seed-list` | List seeds |
| `--seed-remove ID` | Remove a seed by id |
| `--init` | Create config and database |
| `--config PATH` | Override config path |
| `--database PATH` | Override database path |
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

The TUI (phase 9) will be invoked with `swoomps` (no flag).

## First run

```bash
./bin/swoomps --init
```

This creates the config file and the empty database. Files live at:

- `~/.config/swoomps/config.yaml`
- `~/.local/share/swoomps/swoomps.db`

## Add seeds

```bash
./bin/swoomps --seed-add mc.hypixel.net
./bin/swoomps --seed-add 192.168.1.10:25565
./bin/swoomps --seed-list
```

A bundled `seeds/default.txt` exists with twenty well-known public
servers. It is not auto-loaded — read its contents with `cat` and pipe
into `--seed-add` calls, or pass the file directly to `--scan --seeds`.

## Scan

```bash
./bin/swoomps --scan --seeds seeds.txt
./bin/swoomps --scan --seeds mc.hypixel.net,2b2t.org
./bin/swoomps --scan --seeds seeds.txt --ports 25565
./bin/swoomps --scan --seeds seeds.txt --ports 1-65535 --qps 50 --workers 64
./bin/swoomps --scan --seeds seeds.txt --ports 25565-25600 --no-expand
./bin/swoomps --scan --seeds seeds.txt --no-query --timeout 5s --verbose
```

Defaults: `qps 50`, `workers 64`, `timeout 3s`, `ports 1-65535`,
`expand on`, `query on`. Override via flag or env (`SWOOMPS_QPS`,
`SWOOMPS_WORKERS`, etc.).

Each found server is printed to stdout in real time, lipgloss coloured:

```
[+] 192.168.1.10:25565   1.20.4  42/100  "A Minecraft Server"
[+] mc.hypixel.net:25565   1.20.4  78,943/200,000  "Hypixel Network"
```

Ctrl+C stops the scan; results written so far are persisted.

## Query results

```bash
./bin/swoomps --list                     # last 50 seen
./bin/swoomps --list --top players       # top 50 by online players
./bin/swoomps --list --version 1.20.4
./bin/swoomps --list --country BR
./bin/swoomps --list --format json
./bin/swoomps --list --format csv
```

## Configuration

```bash
./bin/swoomps --config show               # print effective config
./bin/swoomps --config edit               # opens $EDITOR
./bin/swoomps --config path               # print the file location
```

The file is YAML. Keys: `qps`, `workers`, `timeout`, `ports`,
`database_path`, `enable_query`, `enable_expansion`, `seeds_path`,
`geoip_path`.

## Development

```bash
make help          # list make targets
make build         # compile binary
make test          # smoke tests
make test-race     # with race detector
make vet           # go vet
make tidy          # go mod tidy
make clean         # remove bin/
```

## Environment

| Env var | Maps to | Default |
|---|---|---|
| `SWOOMPS_QPS` | `qps` | 50 |
| `SWOOMPS_WORKERS` | `workers` | 64 |
| `SWOOMPS_TIMEOUT` | `timeout` | 3s |
| `SWOOMPS_PORTS` | `ports` | 1-65535 |
| `SWOOMPS_DATABASE_PATH` | `database_path` | `$XDG_DATA_HOME/swoomps/swoomps.db` |
| `SWOOMPS_CONFIG_PATH` | `config_path` | `$XDG_CONFIG_HOME/swoomps/config.yaml` |
| `SWOOMPS_NO_QUERY` | `enable_query` (inverted) | false |
| `SWOOMPS_NO_EXPAND` | `enable_expansion` (inverted) | false |
| `SWOOMPS_VERBOSE` | `verbose` | false |

## Troubleshooting

**"no such host" on a seed.** The DNS resolution failed. Check with
`dig mc.hypixel.net`. The seed is dropped, the scan continues.

**High error rate.** Lower `--qps`. Some servers and ISPs throttle
network probes heavily; the scanner keeps going but the hit rate drops.

**Database locked.** Another process is writing. The binary uses WAL; a
rogue `sqlite3` shell holding a write transaction can lock writers.
Close the shell and retry.

**Slow scan over `1-65535`.** Expected. With 64 workers and 50 QPS, a
single IP takes 5-15 minutes depending on the network. Use `--ports`
to narrow the range.
