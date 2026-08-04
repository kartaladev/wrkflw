# Terminal transitions are one path — implementation plan (delivery 2b)

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development`.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every terminal transition one code path, and make a terminal
instance impossible to resume — closing two independent resurrection vectors and
one stranded-command path.

**Architecture:** `endInstance` becomes the single terminal transition (status,
`EndedAt`, cleared compensation cursor, orphaned-incident sweep, task and
scheduled-work sweeps) at all eight sites. The compensation guard widens from
reverse intent to any **resuming** intent. `handleActionCompleted` gains the
terminal tolerance `handleResolveIncident` already has.

**Tech Stack:** Go 1.25, `engine/` only. No new dependency, no storage, no
transport, no public API **signature** change — but two exported godoc comments
change, and one public trigger behaviour becomes stricter.

## ▶ Progress

| | |
|---|---|
| Branch | `parked/terminal-transitions`, branched off `main` @ `85fbb38` — **one squashed feature bundle**, 21 files |
| Status | ✅ **DELIVERY GATE PASSED, 3 of 3.** Built 2026-08-04 with `superpowers:subagent-driven-development`: 4 tasks, 4 task reviews, 4 fix rounds, 1 final whole-branch review, 3 gate fix waves, every round scope-re-reviewed. **Merged-tree suite green (run twice) · `/code-review` 4 findings → 3 fixed + 1 owner-adjudicated · `/security-review` 0 vulnerabilities.** |

**`/security-review`: 0 vulnerabilities.** Assessed as a state-machine tightening
in which every new path is strictly more restrictive than the one it replaced.
Specifically cleared: the `UpdateTask` deep-copy across the consumer boundary is
intact at **all eight** non-test construction sites (delivery 2a's shallow-copy
class is not reintroduced); no new guard sits on an authz-evaluating path, so no
claim/complete/reassign bypasses a check that previously applied; the four new
`slog.WarnContext` calls log only identifiers and a status enum, never variables,
task payloads or actor identities; and `handleResolveIncident`'s new refusal
*denies* an operation whose pre-guard behaviour was the dangerous one.

**MERGED-TREE gate run (2026-08-04, owner-authorised Docker) — run TWICE**, the
second time after `/code-review`'s fixes, because the first run certified a tree
that no longer existed. Both on a local `--no-ff` merge into `main` @ `85fbb38`
(origin/main verified identical each time, no drift), per the ADR-0160 lesson
that the branch alone is not the tree that ships:

| run | tree | result |
|---|---|---|
| 1 | pre-`/code-review` | `-race ./...` **EXIT=0**, 64 ok, 0 FAIL, 0 skips · lint **0** · repo **73.5%** · engine 91.7% |
| 2 | post-`/code-review` fixes (`5ad0083`) | `-race ./...` **EXIT=0**, 64 ok, 0 FAIL, 0 skips · lint **0** · repo **73.6%** · engine **91.8%** |

The known load-flake `TestPgxNotifierListenDrainsBeforePollInterval` did **not**
trip in either run.

**The final review's blast-radius prediction held exactly: zero non-engine
failures.** Its structural argument — `engine.Step` has one non-engine caller
and nothing outside `engine/` reads `StepResult.Commands` — is now confirmed by
execution, not just by reading.

**Verification (engine, Docker-free — measured independently by reviewers, not just self-reported):**
`go test -race ./engine/...` EXIT=0 · `golangci-lint run ./engine/... ./runtime/...` 0 issues ·
`go build ./...` EXIT=0 · engine coverage **91.7%** (floor 85, 2a baseline 91.6%) ·
98 container-free packages EXIT=0 · **13 of 13 mutations CAUGHT**, six of them re-run
independently by a reviewer.

### ⚠⚠ The headline: FIVE resurrection routes, found by three successive reviews

The count went **1 → 2 → 5**, and each increase came *after* the ADR had claimed
the class was closed:

| review pass | routes found | handlers |
|---|---|---|
| rule-#9 audit + premise sweep (design only) | **0** | — |
| final whole-branch review | **2** | `handleActionCompleted`, `handleActionFailed` |
| `/code-review` (owner-run gate) | **3** | `handleSubInstanceCompleted`, `handleSubInstanceFailed`, `handleResolveIncident` |

All five are the same shape: a **surviving token** whose `AwaitCommand` still
matches, on an instance already terminal, resumed because `tokenAwaiting` has no
status check and `drive` has no status guard. The damage is a silent
Failed→Completed flip whose second terminal event is suppressed by
`terminalOutboxEvent`'s `prevStatus.IsTerminal()` edge guard — a status/event
disagreement that cannot be reconstructed after the fact.

⚠ **Route 3 was opened by this delivery's own Decision 3.** Keeping incidents
whose token survived is exactly the state that lets an admin `ResolveIncident`
re-invoke a side-effecting action against a dead instance, then have the
resulting `ActionCompleted` swallowed by the guard added in Phase 3 — stranding
the token permanently `TokenActive` with its incident already deleted, so it can
be neither re-raised nor re-resolved. Decision 3's rationale enumerated only
**read** consumers of `s.Incidents`; `handleResolveIncident` is a **write**
consumer nobody considered. *When a decision changes a data structure's lifetime,
enumerate its writers, not just its readers.*

**Five of fifteen handlers now carry the guard. The class is NOT closed** — the
rest are protected by convention only. The structural fix (classifying every
trigger as resumption-shaped vs deliberately terminal-tolerant and enforcing it
in `Step`'s dispatch) is **deferred to its own ADR by owner decision**, because
it must thread three shipped carve-outs: a plain full rollback must still work on
a terminal instance, cancel re-delivery is tolerated deliberately, and
`HumanCompleted` must keep erroring because it has an external caller via
`service.CompleteTask`.

**Cross-layer contract, verified not assumed:** `runtime/calllink/notifier.go`
keys idempotency off `ErrTokenNotFound`; its retry branch is
`derr != nil && !errors.Is(derr, ErrTokenNotFound)`, so a `nil` return falls
through to `MarkNotified` on the same branch. Pinned by
`TestCallNotifierRetiresLinkWhenParentIsTerminal` — drain twice, `notified` goes
`1` then `0`, no redelivery loop, parent stays Failed.

### What else shipped beyond the plan

Four further things the design bundle did not contain:

1. ⚠⚠ **The first two resurrection routes, proven by execution at the gate.**
   `handleActionCompleted` and `handleActionFailed` both looked up
   `tokenAwaiting` with no status check, and `drive` has no status guard either.
   A **surviving sibling token** plus an in-flight `ActionCompleted`/`ActionFailed`
   flipped an already **Failed** instance to **Completed** and suppressed its
   terminal event (`err=nil, status=completed, cmds=[CompleteInstance{}]`).
   Pre-existing defects; what was *new* was this ADR claiming they were closed.
   Both fixed by hoisting the guard to the top of the handler, matching
   `handleTimerFired`. **Owner-decided twice** (the second decision overrode an
   earlier controller ruling that deferred `handleActionFailed` — that ruling was
   wrong). Each has a RED-first regression test, mutation-verified.
2. **`cancelAllScheduledWork`'s godoc forbade what this delivery does** — *"Do
   not wire this into the normal-completion path"*, citing ADR-0124. The premise
   sweep missed it. ADR-0164 now records the withdrawal of that corollary, and
   all **three** in-code statements of it are rewritten (the third,
   `runtime/terminal_waiter_test.go`, lives outside `engine/`).
3. **ADR-0164 documented neither its incident decision nor its O1 tolerance.**
   Both were implemented by the plan while shipped, pushed code forward-referenced
   the ADR for them. Now Decisions 3 and 4.
4. **The incident sweep crosses the instance boundary** — `terminalErr` feeds
   `CallOutcome.Err` → `SubInstanceFailed` → the *parent's* failure text. Pinned
   by `runtime/terminal_incident_events_test.go`, a discriminating pair: dropping
   the sweep fails only the cancel case, `s.Incidents = nil` fails only the
   surviving-token case.

### Adjudications made during implementation

- **The premise sweep contradicted itself on incident ordering** (§4 I-3). Its
  "must run before `s.Tokens = nil`" bullet and its "the two dropping sites clear
  theirs" bullet are mutually exclusive. Resolved: `endInstance` sits at each
  site's existing terminal position; narrow and wholesale coincide at the two
  dropping sites *by construction* and differ only at the two that keep tokens,
  which is exactly where the pinning pair lives.
- **The hoist does not swallow legitimate traffic** — proven, not asserted: a
  positive-control mutation widening the guard to `StatusCompensating` fails
  `TestBestEffortCompActionFailure`, so compensation walks demonstrably traverse
  these handlers and stay reachable under the shipped narrow guard.
- **The site-2 tail is unreachable-but-kept** (see correction 2 above). Its two
  lines show 0 coverage deliberately.
- **A1b injects an open human task** rather than driving one. Accepted: suite-wide
  instrumentation showed only `forceTerminate`/`OutcomeComplete` can carry one to
  a completion site, `cancelOpenTasks` is pinned by four other genuinely-driven
  tests, and the injection is disclosed in the test's own doc comment.

### Deferred, deliberately — carry to the backlog

- ⚠ **`tokenAwaiting` has no status check and `drive` no status guard.** Five
  handlers are now guarded individually; ten are not, and a handler added later
  gets no protection by construction. **Owner-decided: its own ADR.** It should
  also decide whether `service/` surfaces "instance is terminal" for an admin
  `ResolveIncident` that the engine now silently refuses — correct at the engine
  layer, but the admin currently gets success back.
- **Incident history on the two token-dropping paths.** Even the narrow sweep
  erases every incident there, so `service/`'s audit view renders empty,
  `incident_count` drops to 0, and `terminalEventErr` degrades to the generic
  `"cancelled"`. **Owner-decided 2026-08-04 to REVISIT this** rather than accept
  it as final: an incident is history, not only a live pointer, and the loss is
  unreconstructable. Token linkage still ships in 2b (it discharges the forward
  reference shipped in ADR-0163); the retention design gets its own ADR, most
  likely carrying the incident error into the terminal event payload — which is
  the right layer and also fixes the "reports `cancelled` instead of the real
  failure" case that motivated the original narrowing.
- **`processtest/park.go`'s `Park.Incidents`** is the one unpinned consumer of the
  incident change — its own test uses a synthetic `StatusRunning` state and passes
  either way.
- Two test-hygiene minors triaged CARRY by the final review (an injected task
  reusing `NodeID:"gate"`; two conditional `CancelTimer` assertions gated by a
  preceding exact-slice assertion).

### Process lessons this delivery produced

- **`go test -run` exits 0 when the pattern matches nothing** ("no tests to run"),
  so a mutation-verify against a renamed test **cannot fail**. Phase 2 renamed
  three tests into subtests; the mutation table had to be corrected in the same
  breath, and every row now requires `-v` plus a visible `=== RUN` and `--- FAIL`.
- **A name-filtered mutation run cannot certify "unreached".** A positive control
  filtered with `-run 'Compensat|Reverse|Rollback'` found zero failures and read
  as "compensation never reaches this handler" — the test is named
  `TestBestEffortComp…`. Only the full suite under mutation exposed it.

### What the fold changed, and the adjudications made

The bundle survived its rule-#9 audit against `main` @ `17e148b`; delivery 2a
(merge `168fb06`) then rewrote `engine/step_nodes.go`, `step_triggers.go`,
`step_errors.go`, `state.go` and `step_cancel.go`. Every correction below is
source-verified against `main` @ `85fbb38`.

1. **The `:318` deletion is removed from the plan** (sweep D-1). At the current
   HEAD `engine/step_nodes.go:318` is ADR-0162's `hasChildScopeWithTokens` drain
   check, shipped in 2a; the arm retirement moved to `:324`. Phase 1.4 now names
   symbols and **restructures** rather than deletes.
2. **A second site with the identical ordering problem is now covered** (sweep
   D-2/D-3): `exitNestedEventSubprocessScope` retires arms before its completion
   block too, and there the call **must still run** on the `resumeInParentScope`
   success path, so deleting it would leave the enclosing scope's ESP arms armed
   after `closeScope`. Both sites get the same hoist-into-the-non-completing-
   branch treatment.
   ⚠ **Correction, found by Phase 1's task review:** the sweep's premise that
   the retirement "also runs on non-completing returns" is true at
   `exitNestedEventSubprocessScope` but **false** at
   `exitRootEventSubprocessScope`. At the root site the two guards above it
   (`tokensInScope("") > 0`, then the subtree `hasChildScopeWithTokens`) plus a
   current scope that is already drained and closed leave no token anywhere, so
   `len(c.s.Tokens) == 0` is necessarily true and the hoisted tail is
   unreachable. It is **kept anyway, as defensive code with a comment saying
   so** — deleting it would rest on an unreachability argument holding forever,
   and `endInstance`'s `cancelAllScheduledWork` is what retires those arms on
   the live path. Expect those two lines to show 0 coverage; that is intended,
   not a gap to chase.
3. **"Four sites drop tokens" is corrected to two** (sweep F-2), in this plan and
   in ADR-0164. `grep 's.Tokens = nil' engine/*.go` → `step_nodes.go:531`,
   `step_triggers.go:233`, and nothing else. This is now **load-bearing**, not
   cosmetic, because of the incident decision.
4. **ADR-0164 gained the incident decision** (sweep F-1, non-negotiable). It
   previously never mentioned incidents while shipped, pushed code
   (`engine/step_cancel.go:53-55`, ADR-0163:188-191) forward-referenced it for
   exactly that. ADR-0164 Decision 3 now records the **narrowing**: clear only
   incidents whose token is gone, never `s.Incidents = nil`.
5. **Phase 4's arm-retirement test is re-pointed** (sweep F-4). Routed through
   `exitRootEventSubprocessScope` the test would pass **with its own mutation
   applied**, because that path already retires root arms today. It is pinned to
   the `exitRootScope` path instead.

**Adjudication A — the sweep's incident "ordering" bullet is superseded.** Sweep
§4 I-3 says the incident sweep "must run **before** `s.Tokens = nil` at the two
dropping sites, or every incident looks orphaned and the narrowing collapses back
into a wholesale clear." That contradicts the same section's decision bullet,
which says those two sites **should** clear their incidents. Running the sweep
before the token drop would *retain* them there — the inverse failure. Resolution:
`endInstance` sits at each site's existing terminal position, which at the two
dropping sites is after the token drop; the orphan sweep therefore clears
everything there, exactly as decided. The narrow and wholesale implementations
coincide at those two sites *by construction* and differ **only** at the two that
keep tokens — which is precisely where Phase 1.1a pins them apart. The sweep's
underlying worry is real and is preserved as that test.

**Adjudication B — new finding, not in the sweep: `cancelAllScheduledWork`'s
godoc forbids what this delivery does.** `engine/state_arms.go:171-176` states
*"Normal completion does NOT run it … a repeatable non-interrupting root
event-sub arm is deliberately allowed to survive into a terminal snapshot
(ADR-0124). **Do not wire this into the normal-completion path.**"* Routing the
three completion sites through `endInstance` does exactly that. The reversal is
intended and ADR-0164 already argues for it, but it withdraws a **shipped
ADR-0124 corollary**, so: ADR-0164 now records the withdrawal explicitly, and
Phase 1.4 updates that godoc plus the matching comment at
`engine/step_eventsubprocess.go:221-228`. Shipping code that contradicts its own
doc comment is not acceptable, and ADR-0124's actual decision (repeatable, not
one-shot) is untouched.

**Pre-adjudicated churn** (sweep I-1, I-6), so Phase 1.5 does not re-litigate it:

- `endInstance`'s `cancelOpenTasks` does **not** double-emit `UpdateTask` against
  2a's `cancelTokenWaits`: the latter sets `Cancelled` on a pointer into
  `s.Tasks`, and `cancelOpenTasks` only emits for `IsOpen()`. 2a already
  re-baselined `engine/step_fail_tasks_test.go` to assert this.
- 2a renamed and deleted **no** test (`git diff 17e148b 168fb06 -- engine/ |
  grep -E '^-func Test'` → empty); it added 27. If command counts shift on the
  compensation-start path, expect `requireCompensationStart`'s
  `require.Len(cmds, 2)` in `engine/step_compensation_test.go` to move first.
- `engine/step_terminal_test.go` does not exist — no collision. 2a's
  `engine/export_test.go` is the established shim pattern if a white-box unit
  test is wanted.

⚠ **The spec is stale and cannot be amended.**
`docs/specs/2026-08-02-scope-lifecycle-correctness.md` §1.7–1.9, §3.3 carries the
pre-2a line numbers and the same incident omission. It shipped with 2a and is
pushed. **This plan and ADR-0164 are authoritative over the spec.**

| | |
|---|---|
| ADRs | `0164-terminal-transitions-are-one-path.md`, plus a correction note folded into the shipped `0109-reverse-instance.md` |
| Spec | `docs/specs/2026-08-02-scope-lifecycle-correctness.md` §1.7–1.9, §3.3 — on `main` since 2a merged. Stale where it conflicts with this plan |
| Audit | Rule-#9 round 2 complete (31 findings, table in `docs/plans/2026-08-02-scope-lifecycle-correctness.md`), plus the 2026-08-04 premise sweep |

## Why this is a separate delivery

Split out of the original three-ADR bundle on the round-2 audit's
recommendation, adjudicated by the owner on 2026-08-02.

ADR-0164 shares **zero symbols** with ADR-0162 — no `descendantScopeIDs`, no
`cancelScopeSubtree`, no `tokensInScopeSubtree`, no drain check — and touches a
disjoint file set.

⚠ Its dependency on delivery 2a is **no longer merely nominal**: 2a shipped a
*documentation* dependency (a forward reference to ADR-0164 for the incident
clear, in `engine/step_cancel.go` and ADR-0163) that 2b must discharge, and 2a's
per-token `removeIncidentsForToken` is the model the incident decision narrows
to.

The real reason to split: ADR-0163 and ADR-0164 each introduce an **independent
source of command-stream churn**, and both insist every moved test expectation be
adjudicated individually rather than mechanically re-baselined. In one combined
diff, a broken assertion in `engine/step_compensation_*_test.go` could come from
either, and the `/code-review` reviewer cannot separate them.

Defects 6 and 9 are reachable today and stay reachable one delivery longer. They
have no interaction with subtree teardown, so nothing compounds.

## What this delivers

| # | defect | evidence |
|---|---|---|
| 6 | No terminal transition clears `s.Compensating`, so a stale `ResumeNode` resurrects a force-terminated instance | spec §1.7 |
| 9 | A **partial rollback** resurrects a terminal instance with a *zero* cursor — so defect 6's fix does not cover it. Also open via the documented `ReverseInstance(WithTargetNode(…))`, which makes shipped **ADR-0109's defense-in-depth claim untrue** for half that API | spec §1.9, REPRODUCED |
| C1 | Incidents outlive the token on the terminal paths — **two** sites drop every token without retiring incidents, falsifying ADR-0163's Consequence | audit round 2, narrowed by the premise sweep |
| O1 | Completion during an in-flight compensation walk returns `ErrTokenNotFound`. A `CompensateThrow` consumes only its own token, so a sibling can complete the instance while an `InvokeAction` is in flight; the trailing `ActionCompleted` matches nothing (`handleActionCompleted`). Pre-existing; owner elected to fix it here | audit round 2 |

## Phases

Every file is in package `engine` — **dispatch one subagent at a time.**
Concurrent agents in one working tree break each other's `go test` compile
(ADR-0159 Stage 2 lesson).

**Locate every edit site by symbol.** Line numbers below are informative only,
as of `main` @ `85fbb38`. Note there are **four** `ended := c.at` occurrences in
`engine/step_nodes.go` (`:218`, `:328`, `:409`, `:524`) — disambiguate by
enclosing function, never by line.

### Phase 1 — `endInstance`, at all eight sites

**Files:** create `endInstance` and `removeOrphanedIncidents` in
`engine/state.go`; modify `exitRootScope`, `exitRootEventSubprocessScope`,
`exitNestedEventSubprocessScope`, `forceTerminate` (all
`engine/step_nodes.go`); `handleUnhandledError`'s immediate branch
(`engine/step_errors.go`); `handleCancelRequested`'s immediate branch and
`handleSubInstanceFailed`'s tail (`engine/step_triggers.go`); `applyTerminate`
(`engine/step_compensation.go`); the godocs on `cancelAllScheduledWork`
(`engine/state_arms.go`) and the non-interrupting comment in
`engine/step_eventsubprocess.go`. Test: `engine/step_terminal_test.go`
(**new**, `package engine_test`).

- [ ] **1.1 RED** — `TestForceTerminationClearsCompensationCursor`: a fork whose
      branch 1 reaches a `CompensateThrow` and branch 2 reaches
      `End(WithForceTermination)`. Assert `r.State.Compensating` is the zero
      value. `Compensating` is an **exported field** (`engine/state.go:239`) of
      the unexported type `compensationCursor`, so `assert.Zero(t,
      r.State.Compensating)` compiles from `engine_test` with no shim.
- [ ] **1.1a RED** — `TestTerminalFailureKeepsIncidentOfSurvivingToken` and
      `TestForceTerminationClearsOrphanedIncident`, the pair that pins the
      narrowing (ADR-0164 Decision 3). This pair is the **only** thing separating
      the correct implementation from `s.Incidents = nil`:
  - Surviving-token case: drive an instance to a raised incident
    (`handleUnhandledError` with the `raiseIncident` policy leaves it Running),
    then fail it terminally through `handleUnhandledError`'s **immediate**
    branch, which leaves `s.Tokens` in place. Assert the incident is **still
    present** on the terminal state. A wholesale clear must fail here.
  - Orphaned case: same incident, then `End(WithForceTermination)` — which drops
    every token. Assert `State.Incidents` is empty.
- [ ] **1.2** Run both; observe the non-zero cursor and the surviving-token
      assertion behaving as the *pre-fix* code does.
      `go test -run '^TestForceTerminationClearsCompensationCursor$|^TestTerminalFailureKeepsIncidentOfSurvivingToken$|^TestForceTerminationClearsOrphanedIncident$' ./engine/ ; echo "EXIT=$?"`
- [ ] **1.3 GREEN** — implement both helpers in `engine/state.go`:

```go
// removeOrphanedIncidents drops every incident whose TokenID names a token that
// is no longer present, and keeps the rest in slice order so command output
// stays deterministic. It is the terminal-site counterpart of
// removeIncidentsForToken (ADR-0163): the two terminal paths that drop every
// token — forceTerminate and handleCancelRequested's immediate branch — never
// route through cancelTokenWaits, so without this an incident outlives the token
// it describes.
//
// Deliberately NOT s.Incidents = nil. A terminal instance whose tokens survive
// (handleUnhandledError's immediate branch, handleSubInstanceFailed's tail)
// keeps its incidents, because runtime/outbox.go's terminalEventErr, the
// service/ audit view and incident_count all read them after the instance is
// terminal (ADR-0164 Decision 3).
func (s *InstanceState) removeOrphanedIncidents() {
	s.Incidents = slices.DeleteFunc(s.Incidents, func(inc Incident) bool {
		return s.tokenByID(inc.TokenID) == nil
	})
}

// endInstance performs the terminal transition: status, EndedAt, a cleared
// compensation cursor, the orphaned-incident sweep, and the projection sweeps
// every terminal path owes.
//
// Clearing s.Compensating makes beginCompensation's documented invariant
// ("s.Compensating is the zero cursor here") true by construction.
//
// Call it at the site's existing terminal position. The two sites that drop
// tokens do so BEFORE this call, which is what lets removeOrphanedIncidents
// retire their incidents; hoisting it above the token drop would silently
// retain them.
//
// The terminal command is threaded through rather than appended by the caller so
// the emitted order stays [task cancels…, terminal, scheduled-work cancels…] —
// exactly what applyTerminate, handleUnhandledError, forceTerminate and
// handleCancelRequested emit today. Pass nil where a site emits no terminal
// command of its own.
func (s *InstanceState) endInstance(status Status, at time.Time, terminal Command) []Command {
	s.Status = status
	ended := at
	s.EndedAt = &ended
	s.Compensating = compensationCursor{}
	s.removeOrphanedIncidents()
	cmds := s.cancelOpenTasks()
	if terminal != nil {
		cmds = append(cmds, terminal)
	}
	return append(cmds, s.cancelAllScheduledWork()...)
}
```

- [ ] **1.4** Route the eight sites **one at a time**, running `go test ./engine/`
      after each so a failure is attributable. Notes, by symbol:
  - `forceTerminate`: delete its own `ended := c.at; c.s.EndedAt = &ended` — it
    moves into `endInstance`. Leaving it yields `declared and not used: ended`,
    so it self-corrects at compile time. Keep the `closeVisitAs` loop and
    `c.s.Tokens = nil` exactly where they are, **before** the `endInstance`
    call that replaces the `cancelOpenTasks` / status / terminal-command /
    `cancelAllScheduledWork` tail.
  - `handleSubInstanceFailed`: currently emits `FailInstance` **first**; it moves
    to the canonical position after the task cancels.
  - `handleCancelRequested` immediate branch: keeps its `cancelActionCmds` /
    `nodeCancelCmds` prefix (2a deleted a call from the **compensation** branch
    only, not this one); append `endInstance(...)` after it. ⚠ Do not "simplify"
    the task sweep away here by analogy with 2a's deletion: this branch drops
    tokens via `closeVisitAs` + `s.Tokens = nil` and **never** goes through
    `cancelTokenWaits`, so `endInstance`'s `cancelOpenTasks` is the only task
    sweep on the path.
  - ⚠ **`exitRootEventSubprocessScope` — restructure, do not delete.** It calls
    `appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(""))`
    **before** its completion block, so splicing `endInstance` into the
    completion block as-is would put `CancelTimer`s on **both sides** of the
    terminal command. The arm retirement also runs on non-completing returns, so
    it cannot simply be dropped. Hoist it into the non-completing branch and let
    `endInstance` own the completing one:

```go
	if len(c.s.Tokens) == 0 {
		return append(cmds, c.s.endInstance(StatusCompleted, c.at,
			CompleteInstance{Result: copyVars(c.s.Variables)})...), true, nil
	}
	cmds = appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(""))
	return cmds, true, nil
```

  - ⚠ **`exitNestedEventSubprocessScope` — same problem, and here the naive fix
    is unsafe.** It retires the enclosing scope's arms
    (`removeEventTriggeredSubprocessArmsForScope(parentScopeID)`) before
    `archiveCompensations` / `closeScope` / the resume, and that call **must
    still run** on the non-terminal `resumeInParentScope` success path — deleting
    it would leave the enclosing scope's ESP arms armed after its scope is
    closed. Apply the same restructure, but hoist the call into **both**
    non-completing returns rather than removing it; only the
    `grandparentScopeID == "" && len(c.s.Tokens) == 0` completion branch defers
    to `endInstance`.
  - `applyTerminate`: keep `applyPlanRecordClearing(s, plan)` **before**
    `endInstance`. It reads only `finishPlan` fields and never inspects
    `s.Status`, so hoisting it above the status assignment that moved into
    `endInstance` is behaviour-preserving. The cursor clear is **redundant but
    intentional** here — `stepCompensationFinish` already clears it. Do **not**
    split `endInstance` into two variants to avoid the double assignment; one
    unconditional terminal path is the entire point.
  - **Godoc, same commit** (Adjudication B): rewrite `cancelAllScheduledWork`'s
    comment in `engine/state_arms.go` — the sentences *"Normal completion does
    NOT run it"* and *"Do not wire this into the normal-completion path"* become
    false — and the trailing claim in the non-interrupting branch comment at
    `engine/step_eventsubprocess.go:221-228` that a root arm "may therefore be
    present in a terminal snapshot … so that is harmless". Both must state that
    every terminal path now sweeps, citing ADR-0164, and note that ADR-0124's
    repeatability decision is unaffected.
- [ ] **1.5 GREEN + triage.** `go test ./engine/ 2>&1 | tail -40`. Expect
      completion-path churn: the three completion sites now emit sweeps they
      never did. For each moved expectation decide *expectation moved* vs *real
      regression* and record the adjudication here. A mechanical re-baseline
      defeats the tests that catch this class. The pre-adjudicated items are in
      the Progress block — do not re-derive them.
- [ ] **1.6** Commit (see the message template at the end).

### Phase 2 — the resume guard

**Files:** the terminal guard in `stepCompensateRequested`
(`engine/step_compensation.go:130-133`); `engine/trigger.go` godoc.

- [ ] **2.1 RED** — three tests in `engine/step_terminal_test.go`:
  - `TestPartialRollbackCannotResumeTerminalInstance` — the reproduced §1.9
    fixture: `start → svc(charge/refund) → after(ship/unship) →
    End(WithForceTermination("abort", OutcomeAbort))`, driven to terminal with
    both records intact, then `engine.NewCompensateRequested(at, "svc")`.
    Assert `require.Error` + `Contains(err, "cannot resume a terminal instance")`.
    ⚠ A `NewCancelRequested`-based setup does **not** reproduce — that path's own
    walk consumes the records first. `forceTerminate` is correct precisely
    because it skips compensation (`engine/step_nodes.go:521-522`).
  - `TestTargetedReverseCannotResumeTerminalInstance` — same fixture, delivered
    `engine.NewReverseToNode(at, "svc")`. **This is the test that makes the
    ADR-0109 correction honest**: `NewReverseToNode` leaves `ReverseNode` empty
    (`engine/trigger.go:373-374`), so the pre-fix guard never fired for
    `ReverseInstance(WithTargetNode(…))` and the facade's `Load`ed-snapshot
    pre-check (`runtime/processdriver_reverse.go:99-101`) was the only defence.
  - `TestPlainFullRollbackStillAllowedOnTerminalInstance` — same fixture,
    `NewCompensateRequested(at, "")`. Must still walk: internal cancel and error
    paths deliberately re-deliver a plain full rollback against an
    already-terminal instance (`engine/step_compensation.go:120-129`).
- [ ] **2.2** Run; observe the first two returning `nil` error.
- [ ] **2.3 GREEN** —

```go
	// Reject a compensation trigger that would RESUME an already-terminal
	// instance instead of silently resurrecting it. ToNode joins ReverseNode
	// because applyFinish's shared resume block (engine/step_compensation.go:719,
	// :724, :733) — which walkPartial reaches via stepCompensationFinish's plan —
	// sets StatusRunning, clears EndedAt and places a token, exactly as a full
	// reverse would.
	//
	// A PLAIN full rollback (both fields empty) is deliberately still allowed.
	// Rejecting here, before beginCompensation, is load-bearing: guarding at the
	// resume site instead would let the rollback's InvokeActions fire first.
	if (t.ReverseNode != "" || t.ToNode != "") && s.Status.IsTerminal() {
		return StepResult{}, fmt.Errorf("workflow-engine: cannot resume a terminal instance (status %v)", s.Status)
	}
```

  Testing `ToNode` rather than `RestoreTargetVars` is deliberate: it covers the
  targeted reverse (which sets both), the raw partial rollback (which sets only
  `ToNode`), and any hand-built trigger carrying a resume target.
- [ ] **2.4** Update the exported godoc — this is a **public behaviour change**
      in a library-first project:
  - `engine/trigger.go` — append to the `CompensateRequested`,
    `NewCompensateRequested` (`:352`) and `NewReverseToNode` (`:373`) doc
    comments: *"Delivering this trigger with a non-empty ToNode against an
    already-terminal instance is rejected with a `workflow-engine:` error rather
    than resurrecting it (ADR-0164). A plain full rollback (ToNode and
    ReverseNode both empty) is still accepted."* `NewReverseToStart` (`:364`,
    doc `:359-363`) already documents terminal rejection; the other three now
    match.
- [ ] **2.5** GREEN + triage; commit by `--amend`.

### Phase 3 — terminal tolerance for a stranded compensation command (O1)

**Files:** the `tokenAwaiting` nil branch in `handleActionCompleted`
(`engine/step_triggers.go:88-91`). ⚠ There are **three** `tokenAwaiting(t.CommandID)`
call sites (`:88`, `:267`, `:785`) — identify this one by enclosing function.

- [ ] **3.1 RED** — `TestActionCompletedOnTerminalInstanceIsNoOp`: fork ⇒
      {branch 1 → `CompensateThrow`, branch 2 → root `End`} — a plain end, **not**
      force-termination — driven across **two** steps so the walk is in flight
      when the instance completes. Deliver the walk's `ActionCompleted` in a third
      step. Assert no error and unchanged state.
- [ ] **3.2** Run; observe `ErrTokenNotFound`.
- [ ] **3.3 GREEN** —

```go
	tok := s.tokenAwaiting(t.CommandID)
	if tok == nil {
		// A terminal instance can never consume a resumption trigger, so a
		// command id that matches nothing is a stale straggler, not an error: a
		// CompensateThrow consumes only its own token (startCompensationWalk),
		// so a sibling branch can complete the instance while the walk's
		// InvokeAction is still in flight. Same tolerance handleResolveIncident
		// already applies to a missing token. ADR-0161's terminal exclusion
		// covers only the SAME-step case.
		if s.Status.IsTerminal() {
			return StepResult{State: *s}, nil
		}
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
	}
```

- [ ] **3.4** GREEN + full package; `--amend`.

### Phase 4 — normalization tests, verification, Delivery Gate

- [ ] **4.1** Two tests pinning ADR-0164's declared normalizations, which
      otherwise ship asserted-but-unverified:
  - `TestCompletionRetiresNonInterruptingRootEventSubprocessArm` — ⚠ **must be
    pinned to the `exitRootScope` completion path** (premise sweep F-4).
    Routed through `exitRootEventSubprocessScope` the test would pass **with its
    own mutation applied**, because that path already retires root arms today —
    a dead test of exactly the class this project keeps finding. The scenario to
    build is the one `engine/step_eventsubprocess.go:221-228` describes: a root
    **non-interrupting** ESP fires (its arm stays armed), its child scope drains
    while the root scope still holds a token (so `exitRootEventSubprocessScope`
    returns early), and the instance later completes when the root token reaches
    its End via `exitRootScope` — which sweeps nothing today. Assert
    `len(State.EventTriggeredSubprocesses) == 0` and a `CancelTimer` for the
    arm's timer.
  - `TestSubInstanceFailedEmitsFailInstanceAfterTaskCancels` — assert the order
    is `[UpdateTask…, FailInstance, CancelTimer…]`.
- [ ] **4.2 Mutation-verify** each load-bearing test: break the impl on purpose,
      confirm the test fails, restore from a `/tmp` snapshot and `diff`.
      ADR-0159's review found five tests that could not fail; delivery 2a found
      six more, four of them originating in a plan's own test text.

| test | mutation |
|---|---|
| `TestForceTerminationClearsCompensationCursor` | drop the cursor clear from `endInstance` |
| `TestTerminalFailureKeepsIncidentOfSurvivingToken` | replace `removeOrphanedIncidents()` with `s.Incidents = nil` |
| `TestForceTerminationClearsOrphanedIncident` | drop the `removeOrphanedIncidents()` call |
| `TestPartialRollbackCannotResumeTerminalInstance` | narrow the guard back to `t.ReverseNode != ""` |
| `TestTargetedReverseCannotResumeTerminalInstance` | same |
| `TestPlainFullRollbackStillAllowedOnTerminalInstance` | widen the guard to fire unconditionally on a terminal instance |
| `TestActionCompletedOnTerminalInstanceIsNoOp` | drop the `IsTerminal()` branch — after the final review's C1 hoist this targets the guard at the **top** of `handleActionCompleted`; re-run 2026-08-04, `--- FAIL` confirmed, restored and `diff`-clean |
| `TestActionCompletedOnFailedInstanceWithSurvivingSiblingIsNoOp` (final-review C1) | same mutation — this is the half a guard inside `tok == nil` cannot reach; it went RED **before** the hoist (`Status` was `StatusCompleted`, want `StatusFailed`) |
| `TestActionFailedOnFailedInstanceWithSurvivingSiblingIsNoOp` (scoped re-review, item 1) | drop the hoisted guard from `handleActionFailed`. Positive control for the same guard: widening it to `\|\| s.Status == StatusCompensating` fails `TestBestEffortCompActionFailure`, proving the walk path below it stays reachable |
| `TestSubInstanceCompletedOnFailedInstanceWithSurvivingSiblingIsNoOp` (`/code-review`, handler 1) | drop the hoisted guard from `handleSubInstanceCompleted` — ALSO fails `TestCallNotifierRetiresLinkWhenParentIsTerminal` in `runtime/calllink`, so the cross-layer pin is load-bearing too |
| `TestSubInstanceFailedOnFailedInstanceWithSurvivingSiblingIsNoOp` (`/code-review`, handler 2) | drop the hoisted guard from `handleSubInstanceFailed` |
| `TestResolveIncidentOnFailedInstanceWithSurvivingSiblingIsNoOp` (`/code-review`, handler 3) | drop the hoisted guard from `handleResolveIncident` |
| `TestCancelOfIncidentInstanceReportsCancelledNotIncident` (`runtime/`, final-review I2) | drop the `removeOrphanedIncidents()` call from `endInstance` |
| `TestUnhandledErrorKeepsIncidentOfSurvivingToken` (`runtime/`, final-review I2/I3) | replace `removeOrphanedIncidents()` with `s.Incidents = nil` — the `runtime/` mirror of the engine's incident pair; neither mutation can satisfy both |
| `TestCompletionRetiresNonInterruptingRootEventSubprocessArm` | drop `cancelAllScheduledWork` from `endInstance` |
| `TestSubInstanceFailedEmitsFailInstanceAfterTaskCancels` | move the terminal command back to the front |

- [ ] **4.3** Coverage and lint, Docker-free (`engine` is provably
      container-free — `go list -deps -test ./engine/... | grep -c testcontainers`
      → `0`):

```bash
go test -race -coverprofile=cover.out ./engine/... ; echo "EXIT=$?"
go tool cover -func=cover.out | tail -1
go tool cover -func=cover.out | grep -E 'endInstance|removeOrphanedIncidents'
golangci-lint run ./engine/... ; echo "LINT_EXIT=$?"
```

      `engine` must stay ≥ 85% and should not regress from 2a's **91.6%**.

- [ ] **4.4 Full suite — ASK THE OWNER FIRST.** Standing instruction since
      2026-07-31: other sessions saturate the Docker daemon. Verify by **exit
      code**, not a piped tail. Watch for the known load-flake
      `TestPgxNotifierListenDrainsBeforePollInterval`
      (`internal/persistence/store/notifier_pgx_test.go:98`) — re-run in
      isolation before calling it a regression. The repo-wide
      `scripts/coverage.sh` run CLAUDE.md Verification §1 requires happens here.
      ⚠ Watch `runtime/` and `service/` specifically: `runtime/outbox_test.go`
      and `runtime/processdriver_action.go`'s error path read `st.Incidents` on
      terminal states, and the incident narrowing changes what survives there.
- [ ] **4.5 Handover** — rewrite `docs/plans/HANDOVER.md` in place, fill this
      Progress block, update `MEMORY.md` and its topic file.
- [ ] **4.6 Delivery Gate** — Verification, then `/code-review` (owner-run;
      `disable-model-invocation`), then `/security-review`. Fold all findings via
      `--amend`. Adjudicate any false-positive explicitly with the reason —
      silence is not an adjudication. Then merge `--no-ff` and **push**.

## Commit message template

```
fix(engine)!: terminal transitions are one path, and a terminal instance is never resumed (ADR-0164)

endInstance becomes the single terminal transition at all eight sites — status,
EndedAt, cleared compensation cursor, orphaned-incident sweep, task and
scheduled-work sweeps — closing a resurrection vector that let a stale ResumeNode
revive a force-terminated instance.

Incidents are retired by TOKEN LINKAGE, not wholesale: the two terminal paths that
drop every token never route through cancelTokenWaits, so an incident outlived the
token it described. A terminal instance whose tokens survive KEEPS its incidents,
because runtime/outbox.go's terminalEventErr and the service/ audit view read them
after the instance is terminal. This discharges the forward reference shipped in
ADR-0163 and engine/step_cancel.go.

The compensation guard widens from reverse intent to any RESUMING intent, closing
a second, independent vector: a partial rollback resurrects a terminal instance
with a ZERO cursor, so the cursor fix alone does not cover it. The same hole was
open in the documented ReverseInstance(WithTargetNode(…)) path, which makes
shipped ADR-0109's defense-in-depth claim untrue for half that API; a correction
note is folded into it here.

handleActionCompleted gains the terminal tolerance handleResolveIncident already
has, so a compensation command stranded by a sibling completing the instance is a
clean no-op rather than ErrTokenNotFound.

Normal completion now sweeps scheduled work, which withdraws ADR-0124's
terminal-snapshot corollary; cancelAllScheduledWork's godoc and the
step_eventsubprocess.go comment that stated it are updated with it.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01EXUWVDMJfFfy6eVhiUL7qW
```
