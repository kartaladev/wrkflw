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

## State — updated 2026-08-04

**▶ Pick up here: delivery 2b (ADR-0164) is IMPLEMENTED and sitting at the
Delivery Gate on `parked/terminal-transitions` @ `b13483c` — one squashed feature
bundle, 21 files, engine-verified and clean. What remains is owner-gated and
nothing else: (1) the full repo suite, which needs Docker; (2) `/code-review`;
(3) `/security-review`; then merge `--no-ff` into `main` and push.** Fold any
findings with `--amend` — the commit is local and unpushed. The per-delivery
detail, every adjudication, and the deferred list live in that delivery's plan
`▶ Progress` block (`docs/plans/2026-08-02-terminal-transitions.md`).

⚠ **Two resurrection routes were found AT THE GATE, after the bundle had already
survived a rule-#9 audit and a premise sweep** — a surviving sibling token plus an
in-flight `ActionCompleted`/`ActionFailed` flipped an already **Failed** instance
to **Completed**. Both are fixed and pinned. The lesson is in the plan: the design
audits validated the design, and the *built thing* still carried defects only an
adversarial pass over shipped code could find.

⚠ **Ask before using Docker** (standing owner instruction, 2026-07-31 — other
sessions saturate the daemon). `engine` is provably container-free (`go list
-deps -test ./engine/...` → zero testcontainers hits), so engine-only
verification never needs permission. The full suite does. **The owner approved
Docker for delivery 2a's merged-tree run specifically; that approval does not
carry over.** The ADR-0160 lesson stands: run it on the **merged** tree, not just
the branch.

| | |
|---|---|
| `main` | `85fbb38` — delivery 2a merged and pushed 2026-08-04, plus handover docs, clean |
| `parked/terminal-transitions` | `b13483c` — **delivery 2b, IMPLEMENTED, at the gate.** One squashed bundle off `main` @ `85fbb38`: ADR-0164, the ADR-0109 correction note, the plan, the premise sweep, and the code |
| `feat/scope-lifecycle-correctness` | merged and pushed; delete or ignore |
| `parked/scope-and-fanout-design` | ADR-0158 draft (delivery 3) + a superseded ADR-0162 draft. **Do not read its 0162** — the authoritative one is on the delivery-2a branch |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept only because `audit-signal-arm-fanout-r1/-r2` tags point into it |
| `feat/durable-waiters-delivery-correctness` | `434535d` — older parked bundle, docs only |
| Latest ADR on `main` | **0163**. 0164 lands with 2b, 0158 with delivery 3, 0155–0157 reserved by the older parked branch. Next free is **0165** |
| v0.1.0 | not tagged |

## The immediate next steps

1. **Delivery Gate on 2b — steps 1 and 2 of 3 are DONE.** Branch head `5ad0083`.
   - **Merged-tree suite: green, run TWICE** (before and after `/code-review`'s
     fixes, because the first run certified a tree that no longer existed).
     Latest: `-race ./...` **EXIT=0, 64 ok, 0 FAIL, 0 skips**; `golangci-lint
     run ./...` **EXIT=0**; repo **73.6%**; engine **91.8%**; notifier load-flake
     did not trip.
   - **`/code-review`: 4 findings — 3 fixed, 1 owner-adjudicated.** It found
     **three more unguarded handlers** of the same resurrection class
     (`handleSubInstanceCompleted` High, `handleSubInstanceFailed` and
     `handleResolveIncident` Medium). All three now guarded, RED-first and
     mutation-verified; the `runtime/calllink` idempotency contract is pinned by
     a new cross-layer test. Finding 4 (incident history) is owner-decided to
     **revisit in its own ADR** — see the gaps section below.
   - **`/security-review`: 0 vulnerabilities.** Cleared the `UpdateTask`
     deep-copy at all eight construction sites, the authz paths, the four new
     `slog` calls, and the widened compensation guard. Net assessment: strictly
     less unauthorized-state-transition surface than before.
2. **Then delivery 3** (ADR-0158 fan-out), which **still needs its own rule-#9
   audit in split form**.

### The two lessons 2b produced

**An audited bundle decays when its base moves.** The audit was valid; the base
was not held still. The decay is invisible because the documents still read as
authoritative — a line-numbered instruction that once pointed at dead code now
pointed at a just-shipped fix, and its stated justification made deleting that
fix sound reasoned. **Before implementing any bundle whose base has advanced,
re-verify its premises against current source.** Prefer symbol names to line
numbers when writing plans, for exactly this reason. 2b's steps were rewritten
to name symbols; the `:318` instruction that would have deleted ADR-0162's drain
check was removed, and the guard shipped intact.

**A design audit cannot find a defect that only exists in the built thing.** The
2b bundle survived a rule-#9 audit *and* a premise sweep, and the whole-branch
review still found — and proved by execution — two resurrection routes in which
a surviving sibling token flipped an already **Failed** instance to
**Completed**. Neither is reachable from the design documents; both are obvious
once you read `handleActionCompleted`/`handleActionFailed` next to their sibling
`handleTimerFired`. Worse, one fix wave *introduced* an ADR sentence asserting
the second route was safe. **Review the built thing adversarially, not just the
design** — and re-verify any new claim a fix wave adds to an ADR.

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

## The three-delivery sequence — 2 of 3 shipped, 2b at the gate

Auditing the parked signal/message bundle turned one ADR into three, and
established that the dependency runs the **opposite** way from the original
packaging: ADR-0158 amplifies the defects the other two fix, so it ships last.

1. **ADR-0161 — stale-command filtering.** ✅ **SHIPPED** — merge `bcde851`,
   bundle `e37ab93`, pushed 2026-08-01.
2. **Scope-lifecycle correctness — split into 2a and 2b.**
   - **2a** — ADR-0162 + ADR-0163. ✅ **SHIPPED** — merge `168fb06`, pushed
     2026-08-04.
   - **2b** — ADR-0164 (terminal transitions; a terminal instance is never
     resumed). ✅ **IMPLEMENTED, at the gate**, `b13483c`.
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

- ⚠⚠ **Zombie scopes are STILL open, and 2b did not close them despite ADR-0162
  saying it would.** Four terminal transitions still set a terminal `Status`
  without pruning `s.Scopes`. Shipped ADR-0162 says *"Closing that is
  `endInstance`'s job in ADR-0164 (delivery 2b). Until 2b lands, this ADR claims
  the narrower thing that is true"* — but `endInstance` as delivered sweeps
  status, `EndedAt`, the cursor, orphaned incidents, tasks and scheduled work, and
  **never touches `s.Scopes`**. So ADR-0162's sentence goes stale the moment 2b
  merges.
  Deliberate: pruning scopes at a terminal site interacts with
  `archiveCompensations`, the persisted snapshot shape and the `service/` audit
  view, none of which ADR-0164 analysed, and bolting it on at the gate would push
  an unaudited change through the one path every terminal transition now takes.
  **ADR-0164's Consequences records this explicitly so the reference resolves to
  a decision rather than dangling. It needs its own ADR — treat it as a
  first-class backlog item, not a footnote.**
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

## Known gaps accepted in 2b — carried forward deliberately

- ⚠⚠ **THE BIG ONE — `tokenAwaiting` has no status check and `drive` has no
  status guard.** Three successive review passes found **1 → 2 → 5** resurrection
  routes of this one shape, each increase arriving *after* the ADR claimed
  closure. 2b guarded **five of fifteen** handlers individually
  (`handleActionCompleted`, `handleActionFailed`, `handleSubInstanceCompleted`,
  `handleSubInstanceFailed`, `handleResolveIncident`, alongside the pre-existing
  `handleTimerFired`/`handleHumanCandidatesResolved`). **The class is NOT closed**
  — the rest are protected by convention only, and a handler added later gets
  nothing by construction.
  **Owner-decided: the structural fix is its own ADR.** It must classify every
  trigger as resumption-shaped vs deliberately terminal-tolerant and enforce it in
  `Step`'s dispatch, threading three shipped carve-outs: a plain full rollback
  must still work on a terminal instance, cancel re-delivery is deliberate, and
  `HumanCompleted` must keep erroring (external caller via `service.CompleteTask`).
  It should also decide whether `service/` surfaces "instance is terminal" for an
  admin `ResolveIncident` the engine now silently refuses.
- **Incident history on the two token-dropping paths — owner-decided to REVISIT.**
  The narrow sweep still erases every incident at `forceTerminate` and cancel's
  immediate branch, so the audit view renders empty, `incident_count` drops to 0,
  and `terminalEventErr` degrades to `"cancelled"`. Token linkage ships in 2b (it
  discharges ADR-0163's forward reference), but the retention design gets its own
  ADR — most likely carrying the incident error into the **terminal event
  payload**, which is the right layer and also fixes the "reports `cancelled`
  instead of the real failure" case.
- **`processtest/park.go`'s `Park.Incidents` is the one unpinned consumer** of
  the incident-lifetime change: its own test drives a synthetic `StatusRunning`
  state and passes either way. Same family as the still-open harness gap where
  `Classify` derives `AwaitingSignals` from `Token.AwaitSignal` only.
- Two test-hygiene minors triaged CARRY by the final review — an injected task
  reusing `NodeID:"gate"`, and two conditional `CancelTimer` assertions gated by
  a preceding exact-slice assertion. Detail in the plan's `▶ Progress`.

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
