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

## State — updated 2026-08-01

**▶ Pick up here: nothing is in flight. `main` is clean and pushed. The next work
is delivery 2 — ADR-0162, scope-lifecycle correctness** (draft on
`parked/scope-and-fanout-design`, defects listed below). It has **not** been
through the rule-#9 audit in its split form: re-audit the spec + ADR + plan
bundle together before writing any code.

⚠ **Ask before using Docker** (standing owner instruction, 2026-07-31 — other
sessions saturate the daemon). `engine` is provably container-free (`go list
-deps -test ./engine/...` → zero testcontainers/dbtest hits), so engine-only
verification never needs permission. The full suite does. The ADR-0160 lesson
stands: run it on the **merged** tree, not just the branch.

| | |
|---|---|
| `main` | `bcde851` — **ADR-0161 merged (`--no-ff`) and PUSHED 2026-08-01**, clean |
| `feat/stale-command-filter` | merged; delete or ignore |
| `parked/scope-and-fanout-design` | ADR-0158 + ADR-0162 drafts, docs only, audited but re-packaged — **delivery 2 starts here** |
| `feat/signal-arm-fanout` | `67cb055` — **superseded** 3-ADR packaging. It carries a **stale `docs/adr/0161-…md`**; the authoritative ADR-0161 is on `main`. Kept only because the `audit-signal-arm-fanout-r1/-r2` tags point into it |
| `feat/durable-waiters-delivery-correctness` | `434535d` — the older parked bundle, docs only |
| Latest ADR on `main` | **0161**. 0162 is claimed by delivery 2, 0158 by delivery 3; 0155–0157 stay reserved by the older parked branch. Next genuinely free number is **0163** |
| v0.1.0 | not tagged |

## The three-delivery sequence — 1 of 3 shipped

Auditing the parked signal/message bundle's engine-pure half turned one ADR into
three, and the audit established that the dependency runs the **opposite** way
from the original packaging. ADR-0158 is an *amplifier* of the defects the other
two fix, so it ships last:

1. **ADR-0161 — stale-command filtering.** ✅ **SHIPPED** — merge `bcde851`,
   feature bundle `e37ab93`, pushed 2026-08-01. `Step` drops accumulated commands
   whose awaiter it cancelled in the same pass, cancels a dropped `AwaitHuman`'s
   still-open record, and logs one Warn per drop. Gate on the merged tree:
   `go test -race -count=1 ./...` exit 0, **64 ok / 0 FAIL / 0 skips**,
   `golangci-lint run ./...` 0 issues, `engine` **90.8%** with all three filter
   functions 100%, repo total 73.4%. `/code-review` 3 findings → 3 folded;
   `/security-review` **0 vulnerabilities**. Full evidence, adjudications and what
   is deliberately left undone: `docs/plans/2026-07-31-stale-command-filter.md`.
2. **ADR-0162 — scope-lifecycle correctness.** Not started. Subtree teardown on
   abnormal scope destruction, **plus** the four defects the audit found orbiting
   it (below). Draft ADR on `parked/scope-and-fanout-design`.
3. **ADR-0158 — a broadcast signal fires every matching arm per family.** Not
   started. Draft ADR on `parked/scope-and-fanout-design`; lands on a clean base
   so "does not ship an amplified defect" is true by construction.

Five Opus audit briefs over two rounds produced ~64 findings against the earlier
packaging. Those belonging to deliveries 2 and 3 are recorded in the folded
drafts on the parked branch and **must be re-audited with those bundles** — they
have not been through rule #9 in their split form.

## Defects the audit found, queued for delivery 2

All verified against `main`; none are caused by ADR-0161 (items 5, 7 and 8 are
the ones it touches at the edges — read those notes before assuming scope).

1. **An instance can wedge permanently.** The three sub-process drain checks
   (`engine/step_nodes.go:306,357,408`) enumerate **direct children only**, so a
   grandchild scope holding the live token is invisible. `closeScope` then prunes
   the subtree transitively (`state_compensation.go:290-310`), leaving a token
   whose `ScopeID` is absent from `s.Scopes`. `defForScope` errors
   (`step_state.go:26-27`), `drive` propagates it (`step.go:166-169`), and
   **every subsequent `Step` fails identically**. Repro: a 3-level nest where the
   middle scope's token has descended and a sibling branch reaches the middle
   scope's end event. Fix with a `tokensInScopeSubtree` built on the
   `descendantScopeIDs` helper ADR-0162 introduces.
2. **An interrupt leaves descendant scopes running.** Both abnormal teardowns
   (`step_eventsubprocess.go:189-207`, `step_errors.go:377-394`) match tokens on
   exact scope equality. This is ADR-0162's core.
3. **Zombie scopes.** After a root-level interrupting event sub-process, cancelled
   descendant scopes are never closed, so a *completed* instance can carry open
   `Scopes` entries.
4. **Compensation records silently dropped.** Neither abnormal teardown calls
   `archiveCompensations`, which the normal exit (`step_nodes.go:422`) always
   does — so compensable work inside a torn-down subtree becomes unreachable.
5. **Cross-step orphaned tasks and dangling incidents.** `cancelTokenWaits` never
   touches `s.Tasks` or `s.Incidents`, so cancelling a token parked on a
   `UserTask` in an earlier step leaves an inbox task nothing can complete.
   ADR-0161 fixes only the same-step case.

6. **A force-terminated instance can be resurrected.** No terminal transition
   clears `s.Compensating` — not `forceTerminate` (`engine/step_nodes.go:478-504`),
   `handleUnhandledError` (`engine/step_errors.go:246`), `handleSubInstanceFailed`
   (`engine/step_triggers.go:830`) nor `handleCancelRequested`'s immediate tail —
   which contradicts the invariant asserted in `beginCompensation`'s own comment
   (`engine/step_compensation.go:300-303`). A step can therefore commit with
   `Status == StatusTerminated` **and** a live cursor carrying a stale
   `ResumeNode`. A later plain `CompensateRequested` then passes the terminal
   guard — scoped strictly to `t.ReverseNode != ""` at
   `engine/step_compensation.go:131` — and `beginCompensation` inherits the stale
   `ResumeNode` at `:306`, so `applyFinish` sets `Status = StatusRunning`, clears
   `EndedAt` and places a token at the stale node. Repro: a fork whose first
   branch reaches a `CompensateThrow` and whose second reaches an
   `End(WithForceTermination)`, then deliver `CompensateRequested`. Fix with a
   single `endWalk()` helper called wherever `EndedAt` is stamped. ADR-0161's
   terminal exclusion is defensive against this and stays correct once it is
   fixed.

7. **An interrupted scope leaves its human tasks open, across step boundaries.**
   `propagateError`'s enclosing-scope teardown (`engine/step_errors.go:377-395`)
   calls `cancelTokenWaits` per token, which cancels timers, arms and the token
   but **never touches `s.Tasks`**. `humantask.Cancelled` is written in only three
   places (`engine/state.go:301`, `engine/step_timers.go:89`, and ADR-0161's
   filter), none of them on this path. Reproduced: a sub-process containing
   `fork ⇒ {review: UserTask, work: ServiceTask}` with an error boundary; an
   `ActionFailed` in a later step leaves `task … state=unclaimed open=true` on a
   **completed** instance, with no `UpdateTask` emitted — an inbox entry nothing
   can ever complete, still served by `ClaimableBy`/`AssignedTo`. Collapsing the
   same topology into one step now yields `Cancelled`, because ADR-0161's filter
   catches it — so the outcome depends on step granularity. Fix with a
   `cancelOpenTasksInScope(errScopeID)` sibling of `cancelOpenTasks` in that
   `consume` closure. Pre-existing; ADR-0161 records the asymmetry in its
   Consequences so it is not read as intentional.

8. **`cancelOpenTasks` hands live engine state to the task store.**
   `engine/state.go:302` emits `UpdateTask{Task: s.Tasks[i]}` — a shallow copy
   sharing the `Claim`/`Completion` pointees, the `Vars` map and the
   candidate/eligibility slices with the record it commits as instance state.
   Latent only because both in-repo stores copy on ingest (`humantask/memory.go:35`;
   the SQL store marshals to JSON), but `TaskStore` is public API and a consumer
   store that retains the value verbatim would share mutable actor state.
   `HumanTask.Clone()` is the one-line fix — the same one ADR-0161's filter took
   after `/code-review` flagged it there. Pre-existing, long-standing; belongs to
   whichever delivery next touches that sweep.

⚠ `descendantScopeIDs` cannot be a straight extraction from `closeScope`:
`closeScope` opens with `if s.scopeByID(scopeID) == nil { return }`, and
`scopeByID("")` is **always** nil because the root scope is implicit. Carrying
that guard into the helper makes root-level teardown a silent no-op; removing it
from `closeScope` turns `closeScope("")` into an instance-wide scope wipe. The
helper must have no guard; `closeScope` must keep its own.

## After that — pre-v0.1.0 blockers

1. **Strict definition decoding** (`DisallowUnknownFields` / `KnownFields(true)`).
   Lenient decode plus a fail-open `AuthzSpec` means any future `eligible_*` tag
   drift silently degrades to allow-all. Harmless while untagged; a genuine
   security finding the moment v0.1.0 exists.
2. **A zero `next_run` cannot be armed on MySQL.** `runtime/timerops.go:156-159`
   arms with a zero `nextRun` when `TriggerSpec.Next` reports `ok == false`;
   `DATETIME(6) NOT NULL` rejects the `'0000-00-00'` the driver emits under strict
   mode, so the step fails. Postgres and SQLite store it fine. Needs a
   reject-vs-normalise decision, so it needs its own ADR.
3. `Upsert` can persist `State: Claimed, Claim: nil` — the read path upholds the
   invariant, the write path does not.
4. **ADR-0159 names two symbols that do not exist** (`0159:93` says
   `EncodeArmedCursor` / `DecodeArmedCursor`; the shipped names are
   `EncodeArmedTimerCursor` / `DecodeArmedTimerCursor`,
   `runtime/kernel/armed_timer_paging.go:64,81`). That ADR is merged and pushed,
   so it takes its own small `docs:` commit, not an amend.
5. **`TestPgxNotifierListenDrainsBeforePollInterval` is load-flaky.** Failed once
   during a full `go test -race ./...` on 2026-07-31, then passed in isolation and
   on two full re-runs. Pre-existing. Root cause is the assertion budget:
   `require.Eventually(..., 5*time.Second, 25ms)` at
   `internal/persistence/store/notifier_pgx_test.go:98` waits on a NOTIFY-driven
   relay drain while a dozen containers boot concurrently. Interacts with the
   suite-speed item; do not silence the assertion — it guards NOTIFY wakeup vs a
   30s poll.
6. **`processtest` cannot drive a boundary-arm-only park.** `Classify` derives
   `AwaitingSignals` from `Token.AwaitSignal` only (`processtest/park.go:107`),
   not from `state.SignalWaiters()`, so `Harness.PublishSignal`
   (`processtest/handlers.go:89-97`) passes forever on a definition parked purely
   on signal boundary arms. Same class as the bug ADR-0154 fixed in `runtime/`,
   still live in the **public** harness. Blocks consumers from testing
   delivery 3's headline scenario.

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's
  plan in `docs/plans/`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Designs** — `docs/specs/`.
- **Conventions and gates** — `CLAUDE.md`.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
