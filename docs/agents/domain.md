# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`GLOSSARY.md`** at the repo root
- **`docs/adr/`**: read ADRs that touch the area you're about to work in

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

This is a **single-context** repo:

```
/
├── GLOSSARY.md
├── docs/
│   ├── adr/
│   │   ├── 0001-example-decision.md
│   │   └── 0002-another-decision.md
│   └── agents/          ← this folder; skill configuration, not domain docs
├── cmd/
├── internal/
└── version/
```

Don't introduce `GLOSSARY-MAP.md` or per-package glossaries unless the repo actually splits into separate bounded contexts. It currently doesn't.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `GLOSSARY.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

Established terms in this project already carry specific meaning — e.g. **server**, **scan**, **sample**, **player list**, **incremental accumulation**. Prefer them exactly as the existing docs use them; `docs/players-incremental.md` and `STATE.md` are the current de-facto vocabulary sources.

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0007 (event-sourced orders), but worth reopening because…_
