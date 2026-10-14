## Context

See proposal.md, *Why* and *Measured state*. `docs/agents/test-deadlines.md` already classifies every raw `time.After` deadline into five constructs and says the fix for an under-sized real-I/O timeout is "a shared, named, generous constant". It holds that fix until there is a reproduction. What it lacks is the rule a reader applies before then: which sites the fix would cover, what counts as evidence, and what to do with a new deadline today.

## Goals / Non-Goals

**Goals:**
- An agent sizing or reviewing a single deadline can decide from the note alone whether that deadline is a candidate for generous sizing, and what it may do about it now.
- The evidence state behind "no number yet" is recorded with its date and the window it covers, so a later reader can tell whether it is still current.

**Non-Goals:**
- No machine check. The note is prose that a reviewer applies.
- No edits to `_test.go` files.

## Decisions

### D1. A note, not a guard (kartaladev/wrkflw#98, maintainer decision)

The maintainer decided that the gap becomes a note in `docs/agents/test-deadlines.md`.

Rejected options:
- **A static guard that flags short real-I/O Timeouts.** It would need a per-site test for "waits on real I/O" and "is not a watchdog". Measured: the only cheap signal, a file-level check for container provisioning, places 29 of the 58 sites in 13 files. Those files mix watchdogs, negative windows and drains, so the guard would flag sites that must stay short.
- **Introducing the constant now.** With no qualifying evidence it would have no defensible value, and every site moved onto it would be a widening with no reproduction behind it.

### D2. The note fixes no number

Rejected option: **10 s by analogy with `eventuallyBudget`.** #98 rejects this explicitly, and the measurement agrees. None of the 117 retrievable failed CI logs (2026-07-12 to 2026-10-14) carries the elector flake's signature, and 19 older logs have expired. A value chosen without a recurrence or a reproduction makes the symptom rarer and teaches nothing. CLAUDE.md's proof rule ("defects need proof") applies. Instead, the note names the two acceptable triggers, a recurrence with the same signature or a deliberate reproduction under induced contention, and says the constant's value is derived from that evidence.

### D3. Qualification is per site, by what the awaited channel is driven by

A deadline qualifies only when all three hold:
- its construct is **Timeout**;
- it is not a hang watchdog whose duration encodes a property under test;
- the event it waits for is produced across a process or container boundary, such as a database, a broker or an SMTP server.

Rejected option: **qualification by file or by package**. That is the heuristic D1 measured, and it pulls in sites from the constructs the document says must stay short. The note cites `scheduler/elector_test.go:179` as the worked qualifying example and `scheduler/trigger_test.go:749` as the worked watchdog exclusion. Both were re-measured.

### D4. Counts are re-derived, not edited by hand

The construct table's `n` column is stale: it shows 45 sites in 27 files, and `main` now has 58 in 32. The note relies on that table, so the counts are re-derived by the AST classification pass the document prescribes, with an UNCLASSIFIED bucket that is read rather than hand-filed.

Rejected option: **deleting the counts so they cannot rot**. Whether descriptive prose may carry derived figures is the question kartaladev/wrkflw#100 raises, and `fix-doc-rot` owns it. This change does not pre-empt that decision.

### D5. No dependency on `cut-v0-release`

This change sits in the post-release group, but it does not depend on `cut-v0-release`.
- It touches only an agent-facing document, which no release artifact, API surface or consumer `go.mod` depends on.
- Nothing the release does changes the test deadlines the note governs, so neither landing order invalidates the other.
- `cut-v0-release` depends on fifteen changes and needs a human to make it ready. A `Depends on` naming it would keep a docs-only S change off the frontier until the whole release lands, and the wait would buy nothing. That change's `Blocks` line does not list this one, which is consistent with no edge.

Rejected option: **depending on it to keep the group ordered**. That would be grouping by schedule, not by dependency. The Coordination block is the only dependency graph, so it must not carry a false edge.

## Cross-change coupling

- **`fix-doc-rot` (kartaladev/wrkflw#100)** edits the same file's claims about what descriptive prose may contain. This change owns the new sizing note and the Timeout row's "Wants to be" cell. `fix-doc-rot` owns any guard or rule about derived figures in prose. Either may land first, and whichever lands second rebases its edit to that file.
- **`clarify-terminal-event-delivery` (kartaladev/wrkflw#190)** adds a bounded receive in `eventing/inprocess_test.go` and classifies it per this document. If it lands first, the re-derived counts include its deadline. If it lands second, its new deadline is classified under the note. Neither order needs a `Depends on`.
- **`harden-ci-guard-wiring`** owns `scripts/check-test-timeout.sh`. This change does not touch the script.

## Risks / Trade-offs

- **The flake recurs and nobody connects it to the note** → the note names the signature to search for (the test name and `advisory lock not obtained`) and the action to take, so the recurrence becomes the trigger rather than a retry.
- **The counts rot again as deadlines are added** → the note points at the classification pass as the source of truth. A permanent fix is `fix-doc-rot`'s call (D4).
- **A reader treats "generous" as permission to widen without evidence** → the note states the default as "do not widen" and lists the excluded constructs first.
