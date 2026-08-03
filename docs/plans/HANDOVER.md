# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack of twenty "PREVIOUS RESUME
> POINT" blocks and was silently abandoned for 45 ADRs — see
> `docs/plans/HANDOVER-archive.md`. Per-delivery detail does **not** belong here:
> it belongs in that delivery's plan under a `▶ Progress` block, where it dies
> with the plan. This file carries only: where `main` is, what is in flight, and
> what to do next.

## State — updated 2026-08-03

**▶ Pick up here: delivery 2b (ADR-0164) is the next work, and it is NOT ready to
implement. Its branch `parked/terminal-transitions` is rebased onto the current
`main`, but the bundle was audited against the PRE-2a base and delivery 2a then
rewrote the very files 2b edits. A premise sweep must complete and be folded in
before Phase 1. ⚠ One of the plan's instructions would, if followed literally,
DELETE ADR-0162's drain check and silently re-open the permanent-instance-wedge
defect — read that branch's plan `▶ Progress` block before anything else.**

⚠ **Ask before using Docker** (standing owner instruction, 2026-07-31 — other
sessions saturate the daemon). `engine` is provably container-free (`go list
-deps -test ./engine/...` → zero testcontainers hits), so engine-only
verification never needs permission. The full suite does. **The owner approved
Docker for delivery 2a's merged-tree run specifically; that approval does not
carry over.** The ADR-0160 lesson stands: run it on the **merged** tree, not just
the branch.

| | |
|---|---|
| `main` | `168fb06` — **delivery 2a merged and pushed 2026-08-04**, clean |
| `feat/scope-lifecycle-correctness` | merged and pushed; delete or ignore |
| `parked/terminal-transitions` | `18f1aa9` — **delivery 2b**, ADR-0164 + the ADR-0109 correction + `docs/plans/2026-08-02-terminal-transitions.md`. Branched off `main`, so it carries **no stale copy** of the 2a docs. Rebase onto the new `main` after 2a merges |
| `parked/scope-and-fanout-design` | ADR-0158 draft (delivery 3) + a superseded ADR-0162 draft. **Do not read its 0162** — the authoritative one is on the delivery-2a branch |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept only because `audit-signal-arm-fanout-r1/-r2` tags point into it |
| `feat/durable-waiters-delivery-correctness` | `434535d` — older parked bundle, docs only |
| Latest ADR on `main` | **0163**. 0164 lands with 2b, 0158 with delivery 3, 0155–0157 reserved by the older parked branch. Next free is **0165** |
| v0.1.0 | not tagged |

## The immediate next steps

1. **⚠ Fold the completed premise sweep into the 2b bundle, — its one owner decision is resolved.** The sweep is **DONE** and lives at
   `docs/plans/2026-08-04-delivery-2b-premise-sweep.md` **on the
   `parked/terminal-transitions` branch** (`git show
   parked/terminal-transitions:docs/plans/2026-08-04-delivery-2b-premise-sweep.md`).
   Its corrections are **not yet folded into the phase steps**. Four things it
   found, in order of importance:
   - **Two dangerous instructions**, not one. Besides the `:318` deletion that
     would remove ADR-0162's drain check, `exitNestedEventSubprocessScope` has
     the identical ordering problem at a site the plan never mentions — and
     there the "just delete it" fix is **unsafe**, because that call must still
     run on the non-terminal resume path. The sweep gives the correct
     restructure for both.
   - **✅ OWNER DECISION, RESOLVED 2026-08-04 (§4, I-3): narrow the incident
     clear.** `endInstance` must **not** do `s.Incidents = nil`; it clears only
     incidents whose token no longer exists. Wholesale clearing would blind
     `runtime/outbox.go`'s `terminalEventErr`, `processdriver_action.go`'s
     `terminalErr`, the `service/` audit view and `incident_count` — an instance
     cancelled while parked on an incident would report `"cancelled"` instead of
     the real failure. Narrowing is also literally what shipped ADR-0163
     promises: *"incidents do not outlive the token they describe."*
     ⚠ **This makes the two-vs-four correction load-bearing rather than
     cosmetic**, and the sweep flags an ordering trap: the incident sweep must
     run **before** `s.Tokens = nil`, or every incident looks orphaned and the
     narrowing collapses back into the wholesale clear.
   - **Non-negotiable doc fix:** ADR-0164 **never mentions incidents at all**,
     yet shipped, pushed code (`engine/step_cancel.go:53-55` and ADR-0163)
     forward-references it for exactly that. Fix ADR-0164 or those citations
     point at a decision that does not exist.
   - **Phase 4's arm-retirement test would be dead** under its own mutation
     unless it is pinned to the `exitRootScope` path.
2. **Then implement 2b** with `superpowers:subagent-driven-development` — one
   subagent at a time, since every file is in package `engine` (concurrent
   agents in one working tree break each other's `go test` compile).
3. **Then the Delivery Gate**: verification, `/code-review`,
   `/security-review`, merge `--no-ff`, push.
4. **Then delivery 3** (ADR-0158 fan-out), which **still needs its own rule-#9
   audit in split form**.

### The lesson 2b is currently demonstrating

**An audited bundle decays when its base moves.** The audit was valid; the base
was not held still. The decay is invisible because the documents still read as
authoritative — a line-numbered instruction that once pointed at dead code now
points at a just-shipped fix, and its stated justification makes deleting that
fix sound reasoned. **Before implementing any bundle whose base has advanced,
re-verify its premises against current source.** Prefer symbol names to line
numbers when writing plans, for exactly this reason.

### Delivery 2a's gate record (2026-08-04)

Merged-tree verification: `go test -race -count=1 ./...` **EXIT=0, 64 ok, 0
FAIL**; `golangci-lint run ./...` 0 issues; `engine` **91.6%** (floor 85, prior
baseline 90.8%); repo 73.5%; **16/16 mutations produced their predicted
failure**; `go doc -all ./engine` 108 declarations unchanged.

`/code-review` found **2 findings, both real, both fixed and re-reviewed**:
- A `SubProcess` whose scope was closed by the **event-sub-process** path never
  had its own `CompensateAction` recorded — it was **silently non-compensable**.
  `exitRegularSubprocessScope` was the only site recording it. Fixed by
  extracting `recordSubProcessCompensation` and calling it from both sites.
  ⚠ Fixing it revealed the **pre-existing** regular-site call was itself
  unpinned — removing it broke zero tests. Both sites are now mutation-verified.
- A code comment stated the incident-retirement invariant more broadly than the
  code enforces it, disagreeing with ADR-0163's already-narrowed wording.

`/security-review`: **0 vulnerabilities.** Assessed net security-positive — the
shallow→deep `UpdateTask` copy removed a path by which a consumer-supplied
`TaskStore` could mutate committed engine state's eligibility spec, candidate
list or claim actor.

### What the pending repo-wide run should be watched for

The final whole-branch review named these, by package, in priority order:

- **`runtime/`** — highest risk, purely because command *volume* changed: more
  `UpdateTask` commands now reach `performUpdateTask` → `tasks.Upsert` on
  interrupt and compensation-begin paths. Watch anything asserting an exact
  `Upsert` call count, and `runtime/scope_compensation_test.go`, which reads
  `ArchivedCompensations["sub"]` and now runs against a path that archives in
  more cases.
- **`service/`** — `ProcessInstance.ActiveTasks` (ADR-0142) filters on
  `IsOpen()`, so a task cancelled by an interrupt now drops out of that
  projection where it previously persisted.
- **`internal/persistence/`** — no schema or serialization change. Snapshots are
  marginally larger (`ArchivedCompensations` populated where it was dropped).
  `Scopes` slice order is load-bearing for `descendantScopeIDs` and was verified
  to round-trip exactly through `Store.Load`.
- **`transport/`** — no exposure.

## The three-delivery sequence — 1 of 3 shipped, 2a at the gate

Auditing the parked signal/message bundle turned one ADR into three, and
established that the dependency runs the **opposite** way from the original
packaging: ADR-0158 amplifies the defects the other two fix, so it ships last.

1. **ADR-0161 — stale-command filtering.** ✅ **SHIPPED** — merge `bcde851`,
   bundle `e37ab93`, pushed 2026-08-01.
2. **Scope-lifecycle correctness — split into 2a and 2b.**
   - **2a** — ADR-0162 + ADR-0163. ✅ **IMPLEMENTED, at the gate.**
   - **2b** — ADR-0164 (terminal transitions; a terminal instance is never
     resumed), parked at `18f1aa9`, designed and audited, not started.
3. **ADR-0158 — a broadcast signal fires every matching arm per family.** Not
   started. Draft on `parked/scope-and-fanout-design`. **Still needs its own
   rule-#9 audit in split form.**

## What delivery 2a fixes

One root cause: **teardown enumerated one level where the thing being torn down
is a tree.** Full detail is in the spec and the ADRs; one line each here.

| # | defect | ADR |
|---|---|---|
| 1 | **A permanently wedged instance.** Drain checks enumerated direct children, so a **grandchild** scope holding the live token was invisible; `closeScope` pruned it transitively and every subsequent `Step` failed in `defForScope`. Unrecoverable without DB surgery | 0162 |
| 2 | An interrupt left descendant scopes running | 0162 |
| 3 | Zombie scopes on a completed instance after a root-level interrupting event sub-process | 0162 |
| 4 | Compensation records silently dropped on abnormal teardown, so completed work became permanently uncompensable | 0162 |
| 10 | **Found by the round-2 audit.** `exitEventSubprocessScope` closed **unconditionally** — no child check to widen, and it never archived. Defects 1 and 4 both survived there, on a **normal** exit, at a site the first ADR draft declared "unaffected" | 0162 |
| — | **Found by Phase 4's mutation sweep, predicted by nobody.** A root-level event sub-process's arms were silently retired **while the instance was still working**, so a later signal would never re-trigger it. Fixed as a side effect of the widening | 0162 |
| 5, 7 | **Cancelling a token orphaned its human task.** An interrupting boundary on a `UserTask` host emitted zero `UpdateTask`, leaving an uncompletable inbox entry on a **still-running** instance | 0163 |
| 8 | Six `UpdateTask` emitters handed a shallow copy to the public `TaskStore` | 0163 |

Five design decisions were owner-made and are load-bearing — **do not
re-litigate them**: archive on abnormal teardown (not defer); task/incident
cleanup inside `cancelTokenWaits` (not per-site); `endInstance` unifies state
**and** sweeps (2b); the compensation guard rejects **resuming** intent (2b);
and the 2a/2b split for **churn attribution**, not size.

⚠ `descendantScopeIDs` must have **no existence guard**, while `closeScope`
keeps its own. `scopeByID("")` is always nil because the root scope is implicit:
guarding the helper makes root-level teardown a silent no-op; removing it from
`closeScope` turns `closeScope("")` into an instance-wide scope wipe.

## Known gaps accepted in 2a — carried forward deliberately

- **Zombie scopes are closed only on the two abnormal-teardown paths.** Four
  terminal transitions still set a terminal `Status` without pruning `s.Scopes`
  (`forceTerminate`, `handleCancelRequested`'s and `handleUnhandledError`'s
  immediate branches, `handleSubInstanceFailed`'s tail). Closing that is
  `endInstance`'s job in **ADR-0164, delivery 2b**. ADR-0162 states this
  explicitly rather than claiming the general case.
- **A newly reachable hard error.** A nested *non-interrupting* event
  sub-process inside an event sub-process can now produce "enclosing node %q has
  no outgoing flows in grandparent definition". Pre-fix that topology was a
  **silent permanent wedge**, so a loud recoverable error is strictly better.
  Resume semantics for it need their own ADR.
- **`stop` travels through a `*Token` the caller already invalidated** (via
  `removeToken`'s reallocation) at all four scope exits. Correct today *only*
  because the write and the one read that matters share the same stale pointer —
  accidentally correct, and fragile to any refactor of `removeToken` or
  `firstActive`. Fix is to thread `stopped` as a value. **Backlog.**
- Smaller follow-ups (fixture duplication, two test-file placements, an
  unexercised sibling-sparing guard) are listed in the plan's `▶ Progress`.

## After that — pre-v0.1.0 blockers

1. **Strict definition decoding** (`DisallowUnknownFields` / `KnownFields(true)`).
   Lenient decode plus a fail-open `AuthzSpec` means future `eligible_*` tag
   drift silently degrades to allow-all. Harmless while untagged; a genuine
   security finding the moment v0.1.0 exists.
2. **A zero `next_run` cannot be armed on MySQL.** `runtime/timerops.go:156-159`
   arms with a zero `nextRun` when `TriggerSpec.Next` reports `ok == false`;
   `DATETIME(6) NOT NULL` rejects the `'0000-00-00'` the driver emits under
   strict mode, so the step fails. Postgres and SQLite store it fine. Needs a
   reject-vs-normalise decision, so it needs its own ADR.
3. `Upsert` can persist `State: Claimed, Claim: nil` — the read path upholds the
   invariant, the write path does not.
4. **ADR-0159 names two symbols that do not exist** (`0159:93` says
   `EncodeArmedCursor` / `DecodeArmedCursor`; the shipped names are
   `EncodeArmedTimerCursor` / `DecodeArmedTimerCursor`,
   `runtime/kernel/armed_timer_paging.go:64,81`). That ADR is merged and pushed,
   so it takes its own small `docs:` commit, not an amend.
5. **`TestPgxNotifierListenDrainsBeforePollInterval` is load-flaky.** Pre-existing.
   Root cause is the assertion budget: `require.Eventually(..., 5*time.Second, 25ms)`
   at `internal/persistence/store/notifier_pgx_test.go:98` waits on a
   NOTIFY-driven relay drain while a dozen containers boot concurrently.
   Interacts with the suite-speed item; do not silence the assertion — it guards
   NOTIFY wakeup vs a 30s poll.
6. **`processtest` cannot drive a boundary-arm-only park.** `Classify` derives
   `AwaitingSignals` from `Token.AwaitSignal` only (`processtest/park.go:107`),
   not from `state.SignalWaiters()`, so `Harness.PublishSignal`
   (`processtest/handlers.go:89-97`) passes forever on a definition parked purely
   on signal boundary arms. Same class as the bug ADR-0154 fixed in `runtime/`,
   still live in the **public** harness. Blocks consumers from testing
   delivery 3's headline scenario.
7. **Suite speed.** Go builds one test binary per package, so `internal/dbtest`'s
   `sync.Once` container boot fires per package, not per run → 12 Postgres + 7
   MySQL boots (~60s of a ~2min suite). Fix: honour
   `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with testcontainers as
   the fallback, plus `scripts/testdb.sh up|down` and CI service wiring.

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's
  plan in `docs/plans/`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Designs** — `docs/specs/`.
- **Conventions and gates** — `CLAUDE.md`.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
