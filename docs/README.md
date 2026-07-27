# doorsniff — documentation

This directory holds long-form documentation for the project. The
README at the repository root is the entry point; the files here go
deeper.

## Subcommand surface

The current CLI is subcommand-based:

```
doorsniff probe                  # run a scan
doorsniff list                   # query the database
doorsniff update                 # re-probe known servers
doorsniff init                   # initialize config and database
doorsniff version                # print version
doorsniff help <sub>             # subcommand help
```

`tui` and `seed` are reserved subcommands. They print "em breve" for
now and will be implemented in upcoming phases.

## Index

| File | Purpose |
|---|---|
| [plan.md](./plan.md) | Roadmap of phases, status, upcoming work |
| [architecture.md](./architecture.md) | Stack, decisions, module layout, data flow, known tech debt |
| [protocols.md](./protocols.md) | Minecraft SLP (modern 1.7+ and legacy 1.6) |
| [storage.md](./storage.md) | SQLite schema, migrations (v1, v2), queries |
| [quickstart.md](./quickstart.md) | Build, run, dev workflow, subcommand examples |
| [ethics.md](./ethics.md) | Legal/ethical considerations, rate limits, opt-out status |
| [lessons.md](./lessons.md) | Technical and design lessons from the feature freeze and refactor |

## Reading order

If this is your first time reading the documentation:

1. [plan.md](./plan.md) — what the project is and what stage it is at
2. [architecture.md](./architecture.md) — how the code is organised
3. [protocols.md](./protocols.md) — the network protocols we speak
4. [storage.md](./storage.md) — the data model
5. [quickstart.md](./quickstart.md) — how to run the code
6. [ethics.md](./ethics.md) — what we will and will not do

`lessons.md` is a side document — read it before the next refactor
or when onboarding a contributor who will touch the scanner
package.

## Recent changes

The docs were last revised to reflect:

- The switch from a flag-based CLI (`--scan --seeds ...`) to
  subcommands (`doorsniff probe`).
- The new `--file` flag and `--resume` support.
- The two-line progress bar (per-IP + overall) and the legacy SLP
  fallback for BungeeCord-style proxies.
- The v2 migration that adds the `scan_progress` table.
- The current scope: no Query protocol, no TUI, no blacklist —
  these are still planned, not built.

See [plan.md](./plan.md#current-focus) for what is up next.
