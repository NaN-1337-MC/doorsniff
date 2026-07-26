# swoomps — documentation

This directory holds long-form documentation for the project. The README at
the repository root is the entry point; the files here go deeper.

## MVP flow

The MVP is a flag-driven CLI:

```
swoomps --scan --seeds seeds.txt --ports 1-65535 --qps 50 --workers 64
```

The scanner reads user-supplied seed IPs, scans a port range with TCP
connect, checks each open port for a Minecraft server (SLP + Query),
persists the results to SQLite, and prints each finding live to stdout.

The TUI dashboard is deferred to phase 9 — it will run when `swoomps`
is invoked with no flag.

## Index

| File | Purpose |
|---|---|
| [plan.md](./plan.md) | Roadmap of phases, status, upcoming work |
| [architecture.md](./architecture.md) | Stack, decisions, module layout, data flow |
| [protocols.md](./protocols.md) | Minecraft SLP (1.7+) and Query protocol specs |
| [storage.md](./storage.md) | SQLite schema, migrations, queries |
| [quickstart.md](./quickstart.md) | Build, run, dev workflow |
| [ethics.md](./ethics.md) | Legal/ethical considerations, opt-out, rate limits |

## Reading order

If this is your first time reading the documentation:

1. [plan.md](./plan.md) — what the project is and what stage it is at
2. [architecture.md](./architecture.md) — how the code is organised
3. [protocols.md](./protocols.md) — the network protocols we speak
4. [storage.md](./storage.md) — the data model
5. [quickstart.md](./quickstart.md) — how to run the code
6. [ethics.md](./ethics.md) — what we will and will not do
