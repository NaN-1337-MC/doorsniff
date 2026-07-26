# Lessons learned

A record of the technical and design lessons surfaced during the
feature freeze and refactor of the scanner package. Each entry
captures a bug, its root cause, and the lesson — written so the
next person (or the next me) does not re-discover them.

## 1. `atomic.Int64` must not be copied

**Bug.** `scanner.Stats` carried `atomic.Int64` fields. Three call
sites copied it by value: `Run` returned `Stats` (not `*Stats`),
`printSummary` took `Stats` by value. Three `go vet` warnings.

**Root cause.** `atomic.Int64` contains an internal `noCopy` marker.
Copying it gives you two counters that look identical but do not
share state.

**Lesson.** When a struct holds any `atomic.*`, `sync.Mutex`, or
`sync.WaitGroup`, the struct is implicitly a pointer type. Treat
its API as `*Struct` everywhere — fields, return values, parameters.
`go vet` is a strict gate; warnings about `noCopy` are never false
positives.

## 2. `atomic.Pointer[T]` over a stack-local is a dangling pointer

**Bug.** `state.CurrentSeed.Store(&s.Host)` in the target generator
appeared to work for short scans (one IP, eleven ports) but broke
in long ones (4096 IPs). The render read `<nil>` even after the
generator had run.

**Root cause.** `for _, s := range seeds` reuses a single `s`
variable across iterations. `&s.Host` is a stack address; after
the iteration ends, the address points to recycled stack memory.
`atomic.Pointer.Store` does not allocate — it just stores the
address. The `*cp` read is then undefined behavior (or, with Go's
current compilers, an empty string that looks like `nil`).

**Lesson.** When passing a pointer to `atomic.Pointer.Store` (or
into any concurrent context), heap-allocate explicitly:
`p := new(string); *p = value; atomic.Store(p)`. The GC keeps the
target alive while the pointer is reachable. Never store the
address of a stack-local.

## 3. Two `ProgressState` instances — silent desynchronisation

**Bug.** The per-IP progress bar and the `scanning:` label showed
`<nil>` and `0/1001` even though the generator was running
correctly. The bug was invisible in unit tests because the test
called `Run` directly and never used the generator.

**Root cause.** `RunScan` created a `ProgressState` and passed it
to `generateTargets`. `Run` created a **second** `ProgressState` and
passed it to `Progress`. The renderer read the second one; the
generator wrote to the first. They were independent variables in
the same struct shape.

**Lesson.** When a piece of state is shared between two
goroutines/owners, there must be **one** instance and a single
source of truth — not "an instance per owner that happens to have
the same type." `PoolConfig` accepting a `*ProgressState` is the
right contract: the caller owns it, the callee reads it.

## 4. Increment ownership matters in concurrent counters

**Bug.** The per-IP bar reached 100% almost immediately, because
`CurrentScanned` was incremented twice per target (once by the
generator when sending to the channel, once by the worker when
receiving). A 1001-port seed showed 100% after ~500ms.

**Root cause.** Two writers, no ownership statement. Both the
generator and the worker "helpfully" incremented the same counter.

**Lesson.** A counter that reflects "work dispatched" and a
counter that reflects "work completed" are different things. Pick
one meaning, document it, and assign exactly one owner. When two
goroutines both need to advance the same counter, the question to
ask is "who knows the truth?" — the answer is usually the one
that emits the event being counted.

In the final design, the generator is the only writer of
`CurrentScanned`: it knows when a seed starts (reset to 0) and when
each port is dispatched (`Add(1)`). Workers only update
`Current` (last IP:Port processed). The renderer reads both,
knowing the generator is at most one channel buffer ahead of the
workers.

## 5. The Go memory model and the `for ... range` loop variable

**Bug.** Same as lesson 2 — different symptom. `addr := t.Address()`
in the worker, followed by `atomic.Pointer.Store(&addr)`, made the
`scanning:` label show the most recent target's IP:Port most of the
time, but flicker to garbage on the iteration boundary.

**Root cause.** Two compounding issues:
1. `&addr` is a stack address. The variable lives until the loop
   iterates again, then is logically destroyed.
2. The Go memory model does not guarantee that a write to a
   stack-local in one goroutine is visible in another goroutine
   without synchronisation, even if both goroutines have a pointer
   to it.

**Lesson.** `atomic.Pointer.Store`/`Load` provides the
synchronisation, but only over the pointer value, not over the
pointed-to memory. The pointed-to memory must be synchronised
itself — usually by living on the heap (GC keeps it alive) and
being assigned once (no torn reads). `new(T)` plus a single
`*p = value` write satisfies both.

## 6. Resume progress was being written only on `--resume`

**Bug.** A scan started without `--resume` never wrote to
`scan_progress`, even though the generator called `onSeedDone`
after each seed completed. The next run with `--resume` therefore
skipped nothing.

**Root cause.** The original code only created the `onSeedDone`
callback when `opts.Resume` was true. The intent was "don't bother
saving progress when the user doesn't want to resume." The actual
behaviour was "the first run never records, so resume is useless
the first time."

**Lesson.** Save state unconditionally; load state conditionally.
"Save" is cheap (one UPSERT per seed, every 5s at 200 qps). "Load"
is what the `--resume` flag controls. This is a generalisation of
"idempotency of side effects": every run that touches the same
artefact should leave it in a state that the next run (with or
without flags) can resume from.

## 7. The legacy 1.6 SLP fires on a RST, not a timeout

**Bug.** None — this is a documentation note. The scanner tries
the modern SLP first, then falls back to the 1.6 ping **only** when
the modern handshake receives a TCP RST (`ECONNRESET` or "reset by
peer"). The fallback does not run on timeouts, parse errors, or
empty responses.

**Why this matters.** BungeeCord / Velocity proxies with
`ping_passthrough` disabled will RST the connection immediately
after the modern SLP packet — that's the fingerprint. A server
that hangs up cleanly is something else (likely a proxy that
rejected the connection for a different reason); the 1.6 fallback
would be wasted work. The condition is the connection state, not
the absence of a JSON response.

## 8. The progress bar is two sticky lines, not one

**Bug.** The original design called for one progress line. Adding
"current IP" required a second line, which forced the cursor
control in `termenv` to move up 2 lines, not 1, on every refresh
and on every `InsertAbove` call.

**Lesson.** When adding a second sticky line, every cursor move
that worked for one line must be repeated. The pattern is
`CursorUp(1); ClearLine(); CursorUp(1); ClearLine(); print; 
SaveCursorPosition`. Forgetting the second `ClearLine` leaves
fragments of the old line visible. The unit test for this is
"visually scan a minute of output" — there is no test that would
catch a forgotten `ClearLine`.

## 9. `lipgloss/table` is the right tool for `swoomps list`

**Lesson.** A previous version of the list view rendered each
cell with manual width accounting. It was 80 lines of brittle
padding math. The replacement uses `table.New()` with a
`StyleFunc` that picks the colour per cell. Result: half the code,
correct ANSI-aware padding, no truncation bugs. Any time a table
view is needed in a TUI/CLI, check `lipgloss/table` before writing
the renderer.

## 10. Doc lag is a feature-freeze tax

**Observation.** When this feature freeze began, `docs/plan.md`
described a 9-phase plan that ended with a TUI in phase 9. The
actual code had 6 subcommands, a 2-line progress bar, resume
support, CIDR expansion, and a 1.6 SLP fallback — none of which
were in the docs. Users (and future contributors) reading the
docs would have been misled.

**Lesson.** Before any refactor, sweep the docs. `docs/plan.md`,
`docs/architecture.md`, `docs/storage.md`, `docs/quickstart.md`,
`docs/protocols.md`, and `docs/ethics.md` were all rewritten in
this pass. The "Current focus" section of `plan.md` now points at
the actual refactor target, not at a phase that shipped months
ago. The "Known tech debt" section of `architecture.md` lists the
items the refactor addresses, with file references.

A documentation update is a 30-minute cost; a contributor
debugging stale assumptions is hours.

## 11. Refactor sequence matters

**Observation.** The P0 → P1 → P2 sequence chosen for this refactor
worked well because each level was a prerequisite for the next:

- **P0 (correctness).** Fix `Stats` copy, fix nil panic, fix the
  `Target.Address` hot path. Without P0, the test suite doesn't
  run cleanly and we can't measure P1.
- **P1 (structure).** Extract `ProgressState`, `worker`,
  `prepareResume`. These unlock testability and feature work. They
  also surface hidden bugs (lesson 3, lesson 4, lesson 6) because
  the new code paths are explicit and reviewable.
- **P2 (UX).** `Options.Timeout time.Duration`, helpers in
  `runProbe`, test fixes. Quality-of-life. None of these block
  P0 or P1.

**Lesson.** Order refactors so that each step makes the next step
visible. Start with bugs that hide other bugs, then structure
that exposes design issues, then UX.

## 12. The hard bugs are the ones the test suite would never catch

**Observation.** Lessons 2, 3, 4, and 5 are all bugs that **passed
the test suite**. The test in `pool_test.go` calls `Run` directly
with a hand-built target channel; it never runs `generateTargets`,
so the per-IP progress state was never tested. It was a manual
`./swoomps probe --file ./seed.txt` run that surfaced the bug.

**Lesson.** The most dangerous bugs live at the boundaries between
components the test suite covers. The fix is integration tests
that exercise the whole pipeline (`RunScan` end-to-end, even if
it's slow). We added one such test during this pass; more are
needed.

## 13. `fmt.Sprintf` is not `net.JoinHostPort`

**Bug.** `runWorker` was formatting `addr` as
`fmt.Sprintf("%s:%d", t.IP, t.Port)`. For IPv6 this produces
`::1:25565` (wrong) instead of `[::1]:25565` (correct). The bug
was invisible because the bundled seed file is IPv4-only.

**Lesson.** When the domain type has a method that does the
canonical formatting (`(t Target) Address()`), use it. Don't
reimplement string formatting locally. The next IPv6 seed file
would have surfaced this; it would have surfaced in the worst
possible way (broken CLI output, or a 5-minute debugging session
into "why is this address malformed").
