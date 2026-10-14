## Why

`scripts/check-test-timeout.sh` bounds the sum of a package's test deadlines from above. Nothing in the repo says how long a single deadline must be when it waits on real I/O across a container boundary. The `TestSchedulerElectorOnLeadershipAcquired` flake that prompted #66 was a deadline that was too short, and the ceiling cannot see that direction. `docs/agents/test-deadlines.md` names the eventual fix, "a shared, named, generous constant", but gives no rule for when it applies, to which sites, or what evidence sets its value. An agent who sizes a deadline today has to guess, and the obvious guess (make it longer) is wrong for the constructs the same document says must stay short.

## What Changes

Every premise below was re-measured on `main` (`eebe55ba`) before it was listed.

- **kartaladev/wrkflw#98 (absorbed).** By maintainer decision the gap is closed by a **note** in `docs/agents/test-deadlines.md`, not by a new guard or constant. The note states the sizing convention for deadlines that wait on real I/O:
  - It applies only to the **Timeout** construct, and only to a Timeout that is not a hang watchdog. Negative windows and drain exits are paid on every green run and stay short. Watchdogs whose duration bounds an algorithmic property (`scheduler/trigger_test.go:749`, re-measured: still present at that line) are excluded, because widening one weakens what it detects.
  - A generous duration is set by evidence. That means a recurrence that carries the failure's signature, or a reproduction under induced contention. Taste is not evidence. Until such evidence exists, no number is fixed and no site is widened. This is the issue's own position ("Deliberately not proposing a number"), and the maintainer decision keeps it.
  - When the evidence arrives, the fix is one shared, named constant applied to the qualifying sites. Each site is classified under the document's construct taxonomy first.
  - The note records the evidence state as measured, with the date and the window the measurement covered. Unlike the counts, this cannot be derived from source.
- **Re-derive the taxonomy counts the note relies on.** The note's subject is a subset of the Timeout row, so the table's counts must be current before the note cites them.

**Measured state (re-verified, not inherited)**
- The flaky site is still `case <-time.After(3 * time.Second)` at `scheduler/elector_test.go:179`. The issue says `:180` (stale by one line); the doc's `:179` is current.
- **Zero recurrences found.** None of the 117 retrievable failed CI run logs (2026-07-12 to 2026-10-14) contains `TestSchedulerElectorOnLeadershipAcquired` or `advisory lock not obtained`. Another 19 logs, including all of the oldest, have expired (HTTP 410). The flake therefore remains an observation and has no reproduction, so the issue's prerequisite for choosing a number is still unmet.
- The other real-I/O failures in that window, `TestEmailSendsViaMailpit` (`send: EOF`), `TestTimerTxFlowSameTxAtomicityPostgres` and `TestRehydrateTimersDurable/postgres` (both 0.00–0.04 s), are not deadline expiries. They are not evidence for a sizing floor.
- **Stale in `docs/agents/test-deadlines.md`:** the table says 45 raw `time.After` deadlines across 27 files. `main` now has 58 across 32. The table's per-construct counts are therefore out of date and must be re-derived by an AST classification pass, as the document itself prescribes.
- A file-level heuristic (a test file that both uses `time.After` and provisions a container or database) puts 29 of the 58 sites in 13 files. That figure is an **upper bound and a hypothesis**: it is file-level, not per-site, and it does not exclude watchdogs or negative windows. The note's per-site count comes from the classification pass.

**Out of scope**
- Introducing the shared constant, or widening any deadline, including `scheduler/elector_test.go:179`. Both wait on the evidence the note describes. A future change owns them once a recurrence or reproduction exists.
- Any new CI guard or extension of `scripts/check-test-timeout.sh`. The maintainer decided against a guard. Guard wiring generally is owned by `harden-ci-guard-wiring`.
- A mechanism that keeps descriptive prose true, including the "Timeout wants to be generous" contradiction kartaladev/wrkflw#100 lists against this document. That is owned by `fix-doc-rot`. This change rewrites only the Timeout row's "Wants to be" cell, because the note refines it.
- Bounding the bare receives in `eventing/inprocess_test.go` (kartaladev/wrkflw#190). That is owned by `clarify-terminal-event-delivery`, which will classify its new deadline per this document.

**BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected. **Consumer go.mod:** not affected. Only an agent-facing document changes.

**Coordination**
- Size: S
- Depends on: nothing
- Blocks: nothing
- Readiness: agent

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. The change edits contributor and agent documentation and no library behavior, so `skip_specs: true` is set.

## Impact

- **Docs:** `docs/agents/test-deadlines.md` gets the new sizing note, re-derived counts in the construct table, and a rewritten Timeout "Wants to be" cell.
- **Not affected:** Go source, tests, CI scripts, exported identifiers, persisted formats, the default security posture, any consumer's `go.mod`.
