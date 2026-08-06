# Plan — structural terminal-trigger guard (ADR-0165)

Spec: `docs/specs/2026-08-05-structural-terminal-trigger-guard.md`
ADR: `docs/adr/0165-triggers-declare-their-terminal-policy.md`
Branch: `feat/terminal-trigger-guard`

## ▶ Progress

**✅ SHIPPED — merged `--no-ff` and PUSHED 2026-08-06.** `main` moved
`8832021` → **`ec25ffd`** (merge), carrying one squashed feature bundle
`d8854e5`. The ten pre-squash commits are preserved on
`backup/terminal-trigger-guard-presquash` (`a3aa889`) as provenance only.

The squash was verified lossless: squashed tree byte-identical to the
ten-commit branch tree, and the merge tree byte-identical to the squashed one
(`818d47f4…` on both sides), so the green suite run certifies exactly the
content now on `main`. Build, lint and the container-free packages were re-run
on the merged tree before pushing.

⚠ The commit count in this block was wrong twice before (it said six while
seven existed). Re-derive counts with `git log`, never from prose.

| Phase | State |
|---|---|
| 1 — RED tests | ✅ reviewed clean |
| 2 — sentinels + `terminalPolicy` on the sealed interface | ✅ inline (compile-breaking) |
| 3 — enforcement in `dispatch` | ✅ 2 fix rounds |
| 4 — remove the 8 guard sites | ✅ 2 fix rounds |
| 5 — task-lifetime guard | ✅ 3 fix rounds |
| 6.1 cross-layer pins · 6.2 godoc | ✅ (both pins mutation-verified; godoc nits fixed — see below) |
| 6.3 ADR amendments · 6.4 CHANGELOG | ✅ |
| **6.5 full suite** | ✅ **green** — owner-approved Docker run, see below |
| **6.6 gate** | ✅ **PASSED** — `/code-review` 5/5 fixed, `/security-review` 0 vulns |
| **6.7 squash + merge + push** | ✅ **DONE** — merge `ec25ffd`, pushed 2026-08-06 |

Verified continuously: `engine` coverage **91.9 %** (pre-delivery baseline 91.8 %,
floor held), `golangci-lint run ./...` 0 issues, and package-scoped runs of
`engine`, `runtime/calllink`, `runtime/signal`, `runtime/task`, `service`,
`processtest`, `transport/http` all EXIT=0.

⚠ **`./runtime/...` is NOT container-free** — `main_test.go`,
`rehydrate_durable_test.go`, `jobstore_rehydrate_durable_test.go` and
`timer_txflow_test.go` import `internal/dbtest`. The list above is the
container-free set; the full-repo run needs Docker and the owner's approval.

### Design corrections made during implementation

**Decision 5's predicate was inverted, and only execution found it.** It
prescribed refusing a plain full rollback on a terminal instance when
compensation records **survive**. Measured through `Step`, the opposite holds:
with no records there is *no walk at all*, but the status flips
`Failed`→`Terminated`, a surviving token is discarded and `EndedAt` is rewritten;
with records surviving it is a real walk that ADR-0164 protects. Implementing it
as written reddened **four** already-reviewed tests. The corrected guard could
not use `len(RootCompensations) == 0` either — `beginCompensation` consolidates
archived sub-process records *before* counting — so it asks
`hasCompensationRecordsToWalk`. ADR-0165 carries the full correction block.

Three further ADR corrections, all folded in: `MessageReceived` **reproduces**
(the ADR argued it only by analogy, so the count is six reproduced / none by
analogy); the `ErrTokenNotFound` → `ErrInstanceTerminal` sentinel change; and the
`RefreshTaskCandidates` 422 improvement being **latent** — that method has no
shipped HTTP route, so only a consumer who mounts one sees it.

### Phase 6.3 — the two owed items, discharged

**Both cross-layer pins are now mutation-verified** from an engine-owning
session. Snapshot `engine/trigger.go`, flip, run, restore, `diff` against
`HEAD` — both restores confirmed byte-identical.

| Mutation | Test | Result |
|---|---|---|
| `SubInstanceCompleted` + `SubInstanceFailed` → `rejectWithError` | `TestCallNotifierMarksLinkNotifiedWhenParentIsTerminal` | **RED**, EXIT=1 — both terminal-parent subtests fail on the nil-delivery-error, `notified` and `marked` assertions |
| `SignalReceived` → `rejectWithError` | `TestSignalBusPublishToleratesATerminalFanOutTarget` | **RED**, EXIT=1 — "one dead instance must not fail a broadcast for every live one" |

In both, the control subtest stayed **PASS** — `transient delivery failure
leaves the link claimable` and `a genuinely failing target does fail the
broadcast`. That is what proves the pins discriminate rather than passing for
any outcome. Both baselines were confirmed to have actually *run* with `-v`,
not inferred from a green exit.

**Phase 6.2's three godoc nits are fixed** (comment-only; no behavioural
change, so no new test — existing tests green before and after):

- `engine/errors.go` now uses the `[Symbol]` doc-link form consistently for
  prose references throughout the sentinel var block; bare `errors.Is(err, X)`
  code expressions are left bare deliberately.
- `ErrInstanceTerminal`'s doc no longer names `httpcore.ClassifyError` by a
  bare package selector. It describes the classifier by role and states
  explicitly that those layers live outside `engine` and are not imported by it.
- `CancelRequested`'s first paragraph described only the immediate-termination
  branch. It now states both termination routes (compensation-first when
  records exist, immediate otherwise) and the already-in-flight-walk case,
  source-verified against `handleCancelRequested` (`engine/step_triggers.go:125`).

Doc-link resolution was **executed, not assumed**: a throwaway `go/doc` +
`go/doc/comment` parser over `./engine` reports **87 resolved links and zero
unresolved** bracketed refs across the package's var and type doc comments
(func docs were out of scope). It also settled the open question — cross-package
links such as `[humantask.ErrTaskNotFound]` resolve against the **package's**
imports, not the individual file's, so they are valid in `errors.go` even
though that file imports only `errors` and `fmt`.

### Phase 6.5 — full repo suite, GREEN

`go test -race -coverprofile=cover.out ./...` → **EXIT=0**, 64 packages `ok`,
**zero** failures, **zero** data races. `scripts/coverage.sh cover.out` → repo
total **73.6 %**.

This ran on the **merged tree**: `main` was verified unmoved at `8832021` both
locally and at `origin`, and the branch is a straight descendant
(`git merge-base --is-ancestor main HEAD` holds), so the branch tree and the
post-merge tree are the same. ⚠ It must be **re-run after any `/code-review`
fix** — 2b's first run certified a tree that no longer existed.

Coverage of the packages this delivery touched:

| Package | Coverage | Note |
|---|---|---|
| `engine` | **91.9 %** | production changes; pre-delivery baseline 91.8 %, floor held |
| `runtime/task` | **94.1 %** | production change (`service.go`) |
| `runtime/signal` | 98.1 % | test-only |
| `runtime/calllink` | 88.2 % | test-only |
| `processtest` | 88.0 % | test-only |
| `service` | **52.6 %** | ⚠ below the 85 % floor, but **test-only** change here — the branch adds `terminal_policy_crosslayer_test.go` and touches no `service` production file, so this delivery could only raise it. Pre-existing; backlog item 4 |

The repo total of 73.6 % matches the known pre-existing figure exactly — untested
`examples/` and transport adapters are the drag, and this is not a regression.

`TestPgxNotifierListenDrainsBeforePollInterval` (the known load-flaky NOTIFY
test) **passed** this run, with 18 containers up concurrently.

### Phase 6.6a — `/code-review`: 5 findings, 5 fixed

All five verified against source before acting; two rested on a shared false
premise that **execution** settled. Suite re-run on the fixed tree: **EXIT=0**,
64 packages, 0 failures, 0 races, repo **73.6 %**, `engine` **91.9 %**, lint 0.

| # | Finding | Resolution |
|---|---|---|
| 1 (Med) | `HumanCompleted` answered `humantask.ErrTaskNotFound` (**404**) for an id with no task record while its three siblings answered `ErrTokenNotFound` (**422**) | **Converged all four on 422** — see below |
| 2 (Low) | `CompensateRequested` logged a refusal with **no node id**, excused by "its `terminalPolicy` is never `rejectSilently`, so the guard never logs it" | Registered a `rollback_target` accessor; comment corrected |
| 3 (Low) | The third refusal (`stepCompensateRequested`, nothing left to compensate) returned 422 with **no log at all** | Emits the same Warn line with a `reason` attribute |
| 4 (Low) | CHANGELOG bullet claimed five triggers "previously returned `ErrTokenNotFound`", contradicting its own table 12 lines below | Rewritten to defer to the table; ADR corrected identically |
| 5 (Low) | Test comment claimed a mis-paired accessor was "latent only because `StartInstance` is `rejectWithError` and never logged" | Corrected — `rejectWithError` **is** logged |

**Findings 2 and 5 shared one false premise**, that `dispatch` does not log
`rejectWithError`. It does; both flavours log, told apart by `outcome`. Probed
rather than argued: a `StartInstance` on a terminal instance emits
`outcome=errored start_node_id=probe-node`, which is the accessor running. The
same probe showed `CompensateRequested` emitting `outcome=errored` and nothing
else — finding 2, reproduced.

**Finding 1 reversed twice, and implementation is what corrected it.** The first
resolution converged all four on **404**, following the shipped rationale that
the service layer answers an unknown id that way "so the engine and the layer
above it stop disagreeing". That reasoning is backwards.
`service.deliverTaskTrigger` reads the task store on its FIRST line, so a
genuinely unknown id **never reaches the engine**; the engine's branch fires only
for a **ghost** — an id the store holds and instance state does not — which is a
state conflict. 404 would deny a task that exists.

The evidence was a test failing, not an argument:
`TestErrConflict_EngineWrongStateClassified` must **seed a synthetic task into
the store** to reach the branch at all, and the 404 convergence turned it RED by
breaking the `ErrInvalidTransition` → `service.ErrConflict` chain. All four now
converge on `ErrTokenNotFound`; `HumanCompleted` keeps `humantask.ErrTaskNotFound`
for the one case only it can observe (a token still parked on a vanished record)
by re-checking `tokenAwaiting` inside the nil branch. ADR-0165 carries the
correction block.

**Mutation-verified**, because the convergence test's final assertions were
rewritten after their RED was observed and so never had one of their own:

| Mutation | Result |
|---|---|
| `handleHumanClaimed` → the 404 sentinel | RED, isolated to the `HumanClaimed` row |
| Drop the `tokenAwaiting` disambiguation → always 404 | RED on the `HumanCompleted` row; corruption test still PASS |
| Collapse it the other way → always `ErrTokenNotFound` | RED on the corruption test; convergence test still PASS |

The last two fail **different** tests, which is what proves both halves of the
disambiguation are load-bearing rather than one being decorative.
`engine/step_triggers.go` restored identical to its snapshot after each.

### Phase 6.6b — `/security-review`: 0 vulnerabilities

Nothing to fold, so the tree is unchanged since the post-`/code-review` suite
re-run and no third suite run is owed.

The review traced every removed guard, every new classification, every changed
sentinel, and the authz ordering across `service/` → `runtime/task/` → `engine/`.
Four conclusions worth keeping:

- **No security check was dropped.** Every deleted production line is a pure
  `IsTerminal()` + `slog.Warn` + `return`; all other deleted lines are in
  `_test.go`. The one compound guard removed from `stepCompensateRequested` is
  reproduced with an identical predicate by `CompensateRequested.terminalPolicy()`.
- **No trigger became more permissive.** The only `allowOnTerminal` outcome was
  already allowed on `main`. Several previously *unguarded* triggers are now
  refused — including `SignalReceived`/`MessageReceived`, which used to merge an
  attacker-supplied `Payload` into a dead instance's `Variables`.
- **Authz runs strictly BEFORE every new guard** (`runtime/task/service.go:199,
  226, 247, 298`; `service/service.go:549-558`), so `ErrTaskNotOpen` and
  `ErrInstanceTerminal` are unreachable by an unauthorized caller and **no task
  enumeration is added**.
- **The new log lines carry no PII or secrets** — only opaque or
  definition-declared identifiers. No variables, payloads, actor IDs, claims or
  completion notes.

Net assessment: a security **tightening**.

### Still owed, beyond the gate

- The `forceTerminate` → `endInstance` boundary sweep is entirely uncovered;
  `s.Boundaries = nil` has no semantic cover anywhere.

Task-by-task detail — every mutation, adjudication and false claim — is in
`.superpowers/sdd/2026-08-05-structural-terminal-trigger-guard/progress.md`.

**Audit outcome (2026-08-05):** three Opus auditors, separate lenses, 42
findings, 41 accepted. It changed two design decisions (§3.2 and §4 of the spec),
corrected a false justification, and caught **three prescribed tests that could
not have failed**. This plan is the post-audit revision — the pre-audit version's
Phase 4 and Phase 5 were both inoperative as written. Full record: spec §9.
⚠ The audit was effective by every measure and still passed Decision 5's inverted
predicate, because it read rather than ran it. That is the origin of CLAUDE.md's
**Premise Discipline** section.

## Execution constraints — read before dispatching anything

- ⚠ **Implementation cannot fan out.** Every engine file is `package engine`, so
  concurrent subagents in one worktree break each other's `go test` compile even
  on disjoint files. **One subagent at a time**, controller reviews each returned
  diff before dispatching the next.
- ⚠ **Phase 2 is a compile-breaking, repo-wide change** (adding an undefaulted
  method to an exported interface). Per CLAUDE.md rule #11 it stays **inline in
  the controller**, not delegated.
- ⚠ **`engine/` mixes `package engine` and `package engine_test`.** Run
  `head -1 <file>` before writing into any existing test file. The two new test
  files have fixed packages — see Phases 1 and 2.
- ⚠ **`go test -run` on a name that does not exist exits 0** ("no tests to run").
  A name-filtered run can never certify "this test is unreached". Verify with
  exit codes on the whole package, never a `| grep | head` pipeline.
- ⚠ **Symbols are authoritative, line numbers are informative.** This bundle was
  written and audited against `main` @ `8832021`; if the base moves, re-verify by
  symbol.
- `engine` is provably container-free, so engine-only runs need no Docker
  permission. The full repo suite does — **ask the owner first**.

## What this delivers

1. `terminalPolicy` declared per trigger on the sealed `Trigger` interface, with
   **no `baseTrigger` default**, so a new trigger cannot compile without deciding.
2. One enforcement point in `dispatch`; **all eight** scattered guard sites
   removed (seven in `step_triggers.go`, one in `stepCompensateRequested`).
3. One narrow **state-dependent** guard retained in `stepCompensateRequested`
   (surviving compensation records) — the policy method reads the trigger, not
   state.
4. Two sentinels, both wrapping `ErrInvalidTransition`, so existing `service/`
   and `transport/` mapping picks them up unchanged. `runtime/task`'s existing
   `ErrTaskNotOpen` becomes an alias of the engine's.
5. **Six** resurrection routes closed — all six reproduced by execution in
   Phase 1 (`MessageReceived`, which the ADR first argued only by analogy,
   reproduced identically to `SignalReceived`), plus a seventh the audit found
   (`CancelRequested`).
6. The task-lifetime companion guard on the three human-task handlers.

---

## Phase 1 — RED: pin the current wrong behaviour

**Goal: every test in this phase FAILS against `main`.**

**New file `engine/step_terminal_policy_test.go` — `package engine_test`.**

- [ ] **1.1** Two fixtures. ⚠ **The audit's #1 catch: one fixture is not enough.**
  - `terminalGuardDef()` — `Start → fork ⇉ { approve(UserTask), svc(ServiceTask) }`,
    flows `f1..f4`, `approve → end-a`. Constructors: `event.NewStart`,
    `gateway.NewParallel`, `activity.NewUserTask("approve")`,
    `activity.NewServiceTask("svc", activity.WithTaskAction("svc-action"))`,
    `event.NewEnd("end-a")`.
  - `terminalGuardSignalDef()` — **identical but the human branch is a signal
    catch**: `event.NewIntermediateCatch("await", event.WithSignalName("approved"))`.
    Required because on `terminalGuardDef` **no token awaits a signal**, so
    `handleSignalReceived` matches nothing, never reaches its deferred
    `mergeVars`, and is *already* a clean no-op on `main`. A signal test built on
    the first fixture **passes before any implementation exists.**
  - `terminalWithSurvivingToken(t, def)` — `StartInstance`, capture the
    `InvokeAction` `CommandID` and (for the human fixture) the task id
    (`humantask.HumanTask.TaskID` — there is **no** `.ID` field), then
    `engine.NewActionFailed(at.Add(time.Minute), cmdID, "boom", false)`.
    Assert preconditions: `StatusFailed`, `len(Tokens) == 2`, task `Cancelled`.
  - ⚠ Signatures: `NewActionFailed(at time.Time, commandID, errMsg string, retryable bool, opts ...ActionFailedOption)`;
    `NewHumanCompleted(at, taskID string, c CompletionInput, actor authz.Actor)`
    takes a struct, not a map.
- [ ] **1.2 RED** — one table test per route (`table-test` skill: `assert`
      closures, **not** `want`/`wantErr` fields; `t.Context()`):
  - `TestStartInstanceRejectedOnTerminalInstance` — `errors.Is(err, engine.ErrInstanceTerminal)`.
    Today: no error; `Status` → `Running` with `EndedAt` still set. (Also
    observed: tokens 2→4, tasks 1→2, history 4→8.)
  - `TestHumanClaimedRejectedOnTerminalInstance` /
    `TestHumanReassignedRejectedOnTerminalInstance` — the error, **and** task
    still `Cancelled`, **and** `Commands` empty.
  - `TestHumanCompletedRejectedOnTerminalInstance` — the error, **and
    `len(State.History)` unchanged**. The history assertion is the load-bearing
    one: it proves no post-mortem visit was appended.
  - `TestSignalReceivedIsNoOpOnTerminalInstance` — **on `terminalGuardSignalDef`**.
    `err == nil`, `Commands` empty, `State.Variables` unchanged, `len(Tokens)`
    unchanged. Today on that fixture: tokens 2→1, `vars=map[x:1]`, history 4→5.
  - `TestMessageReceivedIsNoOpOnTerminalInstance` — message-catch variant.
    ⚠ The option is **`event.WithMessageCorrelator(msg, key)`**
    (`definition/event/options.go:87`). There is no `event.WithMessageName`.
  - `TestResolveIncidentRejectedOnTerminalInstance` — ⚠ **the audit found this
    missing entirely.** `ResolveIncident` is the one behaviour change that is a
    pure owner decision rather than a reproduced defect, and without this it has
    **no engine-level test at all** — only a cross-layer one outside `engine/`
    that may need containers.
  - `TestCancelRequestedIsNoOpOnTerminalInstance` — the route the audit found.
    Drive to terminal **with surviving compensation records** (see 4.2's fixture
    note), deliver `NewCancelRequested`. Today: `Status` → `Compensating`,
    compensation `InvokeAction`s re-emitted. After: clean no-op.
- [ ] **1.3** `go test ./engine/... 2>&1; echo "EXIT=$?"`. Expect a build failure
      first (`undefined: engine.ErrInstanceTerminal` — a valid red state), then
      genuine assertion failures once Phase 2 defines the sentinels. Record both
      outputs in Progress.

## Phase 2 — the mechanism (INLINE, compile-breaking)

**Files:** `engine/errors.go`, `engine/trigger.go`, `runtime/task/service.go`.

- [ ] **2.1** Add both sentinels to the existing `var (...)` block in
      `engine/errors.go`, wrapping `ErrInvalidTransition` as `ErrTokenNotFound`
      does. ⚠ **Both must wrap** — an unwrapped sentinel falls through
      `httpcore.ClassifyError` to a **500 with an empty body**:

```go
	// ErrInstanceTerminal is returned when a trigger that requires a live
	// instance is delivered to one that has already reached a terminal status
	// (Completed, Failed, Terminated). Wrapped in ErrInvalidTransition so
	// errors.Is holds for both sentinels, which is what makes the service layer
	// classify it ErrConflict and transports map it to 422 with no change to
	// either layer. See ADR-0165.
	ErrInstanceTerminal = fmt.Errorf("workflow-engine: instance is terminal: %w", ErrInvalidTransition)

	// ErrTaskNotOpen is returned when a trigger that requires an open human task
	// is delivered to one already Completed or Cancelled. Deliberately distinct
	// from humantask.ErrTaskNotFound, which means the record is absent: a closed
	// task is present, and a caller must be able to tell "no such task" from
	// "too late". runtime/task aliases this. See ADR-0165.
	ErrTaskNotOpen = fmt.Errorf("workflow-engine: human task is not open: %w", ErrInvalidTransition)
```

- [ ] **2.2** Alias the runtime sentinel — `runtime/task` already imports
      `engine`. This unifies two sentinels for one condition **and fixes a live
      defect**: the runtime one is unwrapped today, so `RefreshCandidates` on a
      closed task returns 500 instead of 422.

```go
// runtime/task/service.go — replaces the local errors.New definition.
// Aliased so errors.Is(err, task.ErrTaskNotOpen) and
// errors.Is(err, engine.ErrTaskNotOpen) both hold (ADR-0165).
var ErrTaskNotOpen = engine.ErrTaskNotOpen
```

  ⚠ Its godoc currently says "[TaskService.RefreshCandidates]"; keep that, and
  add a RED test asserting `RefreshCandidates` on a closed task now maps to 422.

- [ ] **2.3** In `engine/trigger.go`, add the policy type and extend the sealed
      interface. **Do not give `baseTrigger` a default** — that omission is the
      mechanism, so it gets a comment saying so:

```go
// terminalPolicy reports how Step treats a trigger delivered to an instance that
// has already reached a terminal status.
type terminalPolicy int

const (
	// rejectSilently logs and returns the state unchanged. For triggers delivered
	// asynchronously by the engine's own machinery, whose caller cannot tell a
	// no-op from success and must not retry — see ADR-0165 for the two shipped
	// call sites (calllink.CallNotifier, signalbus.Publish) that require it.
	rejectSilently terminalPolicy = iota
	// rejectWithError returns ErrInstanceTerminal. For triggers originating in a
	// synchronous external API call, whose caller must not believe it succeeded.
	rejectWithError
	// allowOnTerminal runs the handler. For triggers that deliberately operate on
	// a terminal instance.
	allowOnTerminal
)

// Trigger is the sealed set of inputs Step accepts.
type Trigger interface {
	isTrigger()
	OccurredAt() time.Time
	// terminalPolicy states this trigger's behaviour on an already-terminal
	// instance. It is deliberately NOT implemented on baseTrigger: every trigger
	// type must declare its own, so a new trigger fails to compile until its
	// author has made the decision. Do not add a default. See ADR-0165.
	terminalPolicy() terminalPolicy
}
```

- [ ] **2.4** One method per trigger type, next to its struct, per ADR §4.
      **`CancelRequested` is `rejectSilently`**, not `allowOnTerminal` — the
      audit reclassified it. Fourteen are one-liners; `CompensateRequested` reads
      its receiver:

```go
// CompensateRequested is the one payload-dependent policy: a rollback that would
// RESUME the instance is refused, while a plain full rollback must still work on
// a terminal instance (ADR-0164 carve-out, preserved). The surviving-records
// condition is state-dependent and stays in the handler — see Phase 4.2.
func (t CompensateRequested) terminalPolicy() terminalPolicy {
	if t.ReverseNode != "" || t.ToNode != "" {
		return rejectWithError
	}
	return allowOnTerminal
}
```

- [ ] **2.5** **New file `engine/trigger_terminal_policy_test.go` —
      `package engine`** (white box). A table naming all 15 concrete types and
      their expected policy.
      ⚠ **The audit refuted the claim that this table self-enforces**: a 16th
      trigger simply omitted compiles and passes green. **Length-assert it
      against the `all []Trigger` slice in `TestValidateTriggerKindsAreExhaustive`**
      (`engine/trigger_validate_test.go:82-125`, same package), so one list drives
      both. Otherwise this becomes the *third* hand-maintained 15-item trigger
      list, alongside `store.AllTriggerKinds`.
- [ ] **2.6** `go test ./engine/... 2>&1; echo "EXIT=$?"` — policy table passes;
      Phase 1's tests still fail on assertions (nothing enforces yet).

## Phase 3 — enforcement in `dispatch`

**File:** `engine/step.go`, function `dispatch`.

- [ ] **3.1** Insert ahead of the type switch, and **only** there — not in `Step`,
      so `validateTriggerKey` and `cloneState` keep running first and a malformed
      trigger (ADR-0152) still loses to the terminal check:

```go
	// One structural guard replacing the per-handler copies (ADR-0165). Placed
	// after Step's validateTriggerKey/cloneState so trigger-shape rejection keeps
	// precedence over this state-dependent one.
	//
	// StatusCompensating is NOT terminal, so in-flight compensation walks are
	// unaffected.
	if sp.Status.IsTerminal() {
		switch trg.terminalPolicy() {
		case rejectSilently:
			slog.WarnContext(ctx, "trigger rejected on terminal instance",
				"instance_id", sp.InstanceID,
				"trigger", fmt.Sprintf("%T", trg),
				"status", sp.Status.String(),
			)
			return StepResult{State: *sp, Commands: nil}, nil
		case rejectWithError:
			return StepResult{}, fmt.Errorf("%w (status %v)", ErrInstanceTerminal, sp.Status)
		case allowOnTerminal:
			// fall through to the handler
		}
	}
```

  ⚠ `step.go` already imports `fmt` and `log/slog`; `Status.String()` exists.
- [ ] **3.2** Add the **behavioural exhaustiveness table** the spec's §5.2
      prescribes and the pre-audit plan omitted: one case per trigger, all 15,
      driven against a terminal instance, asserting the *outcome* (not the policy
      value) per class.
- [ ] **3.3** `go test ./engine/... 2>&1; echo "EXIT=$?"` — Phase 1 goes GREEN.
      **Two existing tests are EXPECTED to break here.** Both must be rewritten,
      not deleted:
  - `TestTerminalResumeGuard` (`engine/step_terminal_test.go:350`) — its
    `partial_rollback_rejected` and `targeted_reverse_rejected` cases assert
    `assert.ErrorContains(t, err, "cannot resume a terminal instance")`, a string
    that disappears. Move both to
    `assert.ErrorIs(t, err, engine.ErrInstanceTerminal)`. They are carve-out #1's
    regression cover and ADR-0109's only pin. **Also read the third case at
    `:385` (`plain_full_rollback_allowed`)** — it was not read when this plan was
    written.
  - `TestResolveIncidentOnFailedInstanceWithSurvivingSiblingIsNoOp`
    (`engine/step_terminal_test.go:1330`) — pins the **silent no-op** that
    `ResolveIncident` no longer has. Rewrite to assert the error and rename off
    "IsNoOp". It is ADR-0164 Decision 3's regression cover.

  Any *other* failure is a behaviour change to adjudicate, not to paper over.

## Phase 4 — remove the eight guard sites, each mutation-verified

**This is the load-bearing risk of the delivery.**

⚠⚠ **The pre-audit procedure here was inoperative.** It disabled the `dispatch`
check while each handler's own guard was still present — which restores exactly
`main`'s behaviour, so the run stayed GREEN and the "mutation" proved nothing.
Seven identical no-op runs would have been read as "seven unpinned handlers".
**The order below is inverted from that; do not revert it.**

- [ ] **4.1** For **each** of the seven guards — identified by **enclosing
      function**, not line: `handleActionCompleted`, `handleActionFailed`,
      `handleHumanCandidatesResolved`, `handleTimerFired`,
      `handleSubInstanceCompleted`, `handleSubInstanceFailed`,
      `handleResolveIncident` — in this order:
  1. Snapshot the file to `/tmp`.
  2. **Delete that handler's own `if s.Status.IsTerminal()` body**, `dispatch`
     check still in place. Run → **must be GREEN** (the structural guard covers
     it).
  3. **Now comment out the `dispatch` check.** Run → **must be RED, naming a test
     attributable to this handler.** If it is green, this handler's behaviour is
     unpinned: **write the missing test before going further.**
  4. Restore the `dispatch` check; `diff` against the snapshot to confirm only
     the intended deletion remains. Re-run → GREEN.
- [ ] **4.2** Remove the compensation resume guard in `stepCompensateRequested`
      (the `if (t.ReverseNode != "" || t.ToNode != "") && s.Status.IsTerminal()`
      block) **and add the new state-dependent guard** rejecting a walk when the
      instance is terminal *and* compensation records survive (ADR §Decision 5).
      Pin **all five** cases:
  - `{ResetVars: true, ReverseNode: ""}` on terminal → still
    `"ResetVars requires ReverseNode"`.
  - `{RestoreTargetVars: true, ToNode: ""}` on terminal → still
    `"RestoreTargetVars requires ToNode"`.
  - ⚠ **Non-minimal shapes now report terminal FIRST** — the audit's correction:
    `{ResetVars: true, ToNode: "svc"}` and
    `{RestoreTargetVars: true, ReverseNode: "start"}` today give the shape error,
    after they give `ErrInstanceTerminal`. Pin both; it is a real change (and
    arguably an improvement — shape errors carry no sentinel and classify 500).
  - ⚠ **These next two were prescribed BACKWARDS and were corrected during
    implementation** — see ADR-0165 Decision 5's correction block. Measured
    through `Step`: with no records there is **no walk at all**, but the status
    flips (`Failed` → `Terminated`), a surviving token is discarded and `EndedAt`
    is rewritten; with records surviving it is a real walk emitting compensation
    `InvokeAction`s, which is ADR-0164 carve-out #1. The correct pins are:
  - `NewCompensateRequested(at, "")` (plain full rollback) on a terminal instance
    **with** surviving records → **still walks**. Carve-out #1, preserved.
  - Plain full rollback on a terminal instance with **no** surviving records →
    rejected by the new guard with `ErrInstanceTerminal`.
  - ⚠ Fixture: `driveToForceTerminatedWithBothRecords`
    (`engine/step_terminal_test.go`) produces a terminal instance with
    `len(RootCompensations) == 2`. A `NewCancelRequested`-based setup does **not**
    reproduce — that path's own walk consumes the records first.
- [ ] **4.3** **Migrate the rationale, don't delete it.** Each removed guard
      carries 10–25 lines of load-bearing comment that is the only record of why
      that route matters — notably `handleResolveIncident`'s, which documents
      ADR-0164 Decision 3's write-consumer lesson. Decide per guard whether the
      block moves to the `terminalPolicy()` method or to the `dispatch` guard, and
      **rewrite these three now-stale in-body comments**:
      `handleActionCompleted:117` ("the guard **at the top** already returned"),
      `handleActionFailed:302-303`, `handleTimerFired:512-515`.
- [ ] **4.4** `grep -n "Status.IsTerminal()" engine/*.go`. Expected **production**
      hits after this phase:
  - `engine/step.go` — `dispatch` (the new guard);
  - `engine/step_compensation.go` — the new surviving-records guard;
  - ⚠ **`engine/step_stale_commands.go:59` — `liveAwaiters`' terminal
    short-circuit. DO NOT DELETE IT.** It is a *command-filter* guard, not a
    dispatch guard, and it is load-bearing for ADR-0161/0164. The pre-audit
    version of this checklist omitted it; an implementer following that literally
    would have re-opened the entire stale-command class.
  - `engine/state.go:54` is the `IsTerminal` **definition** and does not match
    this pattern anyway. (The pre-audit text also named "`endInstance`'s
    callers" — no `endInstance` caller uses `IsTerminal`.)

## Phase 5 — the task-lifetime companion guard

**File:** `engine/step_triggers.go`. Per ADR §Decision 6: **all three error**, and
`handleHumanCompleted` is **reordered**.

- [ ] **5.1 RED** — drive a **running** instance whose task was closed by an
      interrupting boundary (ADR-0163's path; `engine/step_cancel_tasks_test.go`
      is `package engine_test` and has the closest fixture). Assert
      `StatusRunning` as a precondition, or Phase 3's guard fires first and the
      test passes for the wrong reason.
  - `TestHumanClaimedOnClosedTaskErrors` / `TestHumanReassignedOnClosedTaskErrors`
    — `errors.Is(err, engine.ErrTaskNotOpen)`, `Commands` empty, task unchanged.
    Today both succeed and re-open the task (`taskState=claimed`, one
    `UpdateTask`).
  - `TestHumanCompletedOnClosedTaskErrors` — `errors.Is(err, engine.ErrTaskNotOpen)`.
    ⚠ **This test could NOT have passed before the reorder in 5.2.** Today the
    handler errors with `ErrTokenNotFound` because the interrupting boundary
    consumes the host token and `tokenAwaiting` runs first.
- [ ] **5.2 GREEN** — add to `handleHumanClaimed` and `handleHumanReassigned`,
      after their existing `task == nil` check:

```go
	if !task.IsOpen() {
		return StepResult{}, fmt.Errorf("%w: task %q", ErrTaskNotOpen, t.TaskID)
	}
```

  and **reorder `handleHumanCompleted`** so `TaskByID` runs *before*
  `tokenAwaiting`, with the same guard between them. Without the reorder the
  guard is unreachable dead code — every path that closes a task on a live
  instance also detaches its token (`cancelTokenWaits` consumes it;
  deadline expiry clears `AwaitCommand` first; `dropStaleTokenCommands` only
  closes tasks whose awaiter is already gone). The reorder is a deliberate error
  change: the deadline-breach path upgrades from `ErrTokenNotFound` to
  `ErrTaskNotOpen`. Pin it.
- [ ] **5.3** Mutation-verify all three: delete each guard, confirm RED, restore
      from the snapshot and `diff`.

## Phase 6 — cross-layer pins, docs, Delivery Gate

- [ ] **6.1** Cross-layer tests (outside `engine/`; may need containers — ask):
  - `runtime/calllink` — the notifier still marks a link notified when the parent
    is terminal. **Pin `errors.Is(engine.ErrInstanceTerminal, engine.ErrTokenNotFound) == false`**:
    both wrap `ErrInvalidTransition` as siblings, and the notifier's idempotency
    branch keys on `ErrTokenNotFound`.
  - `runtime/signal` — `signalbus.Publish` returns `nil` when one fan-out target
    is terminal.
  - `service` + `transport/http/httpcore` — `service.ResolveIncident` on a
    terminal instance yields an error satisfying
    `errors.Is(err, engine.ErrInvalidTransition)`, mapped to **422**. ⚠ Note
    `service/` does **not** re-classify on this route (`:561-562` is
    `deliverTaskTrigger` only) — it relies solely on `httpcore`'s arm, so a single
    `%v` on the path silently downgrades it to 500.
  - `service.RefreshTaskCandidates` on a closed task → **422**, not 500 (the alias
    fix from 2.2).
  - `processtest` — `DriveToCompletion` on a flow that terminates mid-drive still
    returns cleanly. Its `drive` loop checks `IsTerminal` before every deliver
    (`processtest/drive.go:151,183`), so it *should* be shielded from the new
    errors — pin that rather than assume it, since it is the public harness
    consumers test through.
- [ ] **6.2** Godoc — a **public behaviour change in a library-first project**.
      The pre-audit list of seven was under-scoped. Cover:
  - **All 15 trigger types and their `New*` constructors**, plus the `Trigger`
    interface doc itself. The eight `rejectSilently` triggers document their
    silent-no-op contract today only in unexported handler comments, so the ADR's
    "policy readable as one table" is untrue for a consumer reading godoc.
  - ⚠ **`CancelRequested` (`engine/trigger.go:396-401`) is currently FALSE** —
    "no harmful side effects occur since there are no live tokens or timers to
    cancel". Three terminal paths keep `s.Tokens`, and `InvokeCancelAction` is
    emitted unconditionally before any token inspection. Rewrite it.
  - `CompensateRequested`, `NewCompensateRequested`, `NewReverseToStart`,
    `NewReverseToNode` — existing terminal paragraphs that this change alters.
  - `engine/state.go`'s `IsTerminal` note ("Used by `stepCompensateRequested` to
    reject…").
  - Both new sentinels, with the ADR-0165 reference.
- [ ] **6.3** **Amend the two shipped ADRs.** ADR-0165 replaces the mechanism
      ADR-0164 describes and deletes the guard ADR-0109's correction documents.
      Add an `Amended by ADR-0165` note to each. Skipping this is exactly how
      ADR-0162's zombie-scope sentence went stale.
- [ ] **6.4** **`CHANGELOG.md`** — add to the live
      `[Unreleased] → Breaking changes (pre-v0.1.0)` section: the `Trigger`
      interface method, both sentinels, and the eight behaviour changes with
      before/after. ADR-0160 wrote into this section; 0161–0164 skipped it, which
      is a reason to fix rather than a precedent.
- [ ] **6.5** Verification, by exit code, never a pipeline:
  ```bash
  go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out
  go test ./... ; echo "EXIT=$?"
  golangci-lint run ./... ; echo "EXIT=$?"
  ```
  Floors: `engine` ≥ 85% (baseline **91.8%** — do not regress), repo ~73.6%.
  ⚠ Run on the **merged** tree (ADR-0160 lesson) and **re-run after any
  `/code-review` fix** — 2b's first run certified a tree that no longer existed.
- [ ] **6.6** Delivery Gate: `/code-review`, then `/security-review`. Both are
      owner-invoked. Fold every fix with `--amend`; adjudicate explicitly any
      finding dismissed.
- [ ] **6.7** Merge `--no-ff` to `main`, push, rewrite `docs/plans/HANDOVER.md`
      in place, update auto-memory.

## Verification checklist

- [ ] All 15 triggers declare a policy; the white-box table length-asserts against
      `TestValidateTriggerKindsAreExhaustive`'s `all` slice.
- [ ] The behavioural table (3.2) covers all 15 triggers, including
      `ResolveIncident`.
- [ ] For each of the 8 removed guards: handler guard deleted → GREEN, then
      `dispatch` disabled → **RED with an attributable test**.
- [ ] `liveAwaiters`' terminal guard (`step_stale_commands.go:59`) is **still
      present**.
- [ ] Carve-out #1 (⚠ predicate corrected during implementation): plain full
      rollback still walks on a terminal instance **with** surviving records;
      rejected when **none** survive.
- [ ] Route #7 closed: `CancelRequested` on a force-terminated instance with
      compensation records emits nothing and leaves status terminal.
- [ ] Carve-out #3: `HumanCompleted` errors on a terminal instance **with a
      surviving token** — the case that today silently succeeds.
- [ ] `errors.Is(ErrInstanceTerminal, ErrTokenNotFound)` is **false**;
      `errors.Is(task.ErrTaskNotOpen, engine.ErrTaskNotOpen)` is **true**.
- [ ] `service.ResolveIncident` and `service.RefreshTaskCandidates` on the
      relevant states → 422, not 500.
- [ ] ADR-0164 and ADR-0109 carry amendment notes; `CHANGELOG.md` updated.
- [ ] `engine` coverage ≥ 91.8%; lint EXIT=0; full suite EXIT=0 on the merged tree.

## Commit message template

```
fix(engine)!: triggers declare their terminal policy, enforced in dispatch (ADR-0165)

Replaces eight hand-copied terminal guard sites -- seven per-handler and
the compensation resume guard -- with a policy declared on the sealed
Trigger interface and enforced once in Step's dispatch. baseTrigger
supplies no default, so a new trigger type does not compile until its
author has decided; that compile error is the point.

Closes six resurrection routes, every one reproduced by execution:
StartInstance (Failed -> Running with EndedAt still set),
HumanClaimed/HumanReassigned (re-open a cancelled task), HumanCompleted
(post-mortem completion and a falsified history visit), and
SignalReceived/MessageReceived (payload merged into a dead instance's
Variables). A seventh, found by the rule-#9 audit,
closes with it: CancelRequested on a force-terminated instance restarted
the compensation walk, re-firing every InvokeAction against a dead
instance and publishing a second terminal event.

Also adds the task-lifetime guard the three human-task handlers were
missing -- a second key the status guard cannot see, since ADR-0163
closes a task while the instance keeps running -- and unifies
runtime/task.ErrTaskNotOpen with the engine's, which incidentally fixes
that sentinel's fall-through to HTTP 500.

BREAKING: engine.Trigger gains an unexported method. No external
implementation can exist -- isTrigger() seals it. Eight trigger
behaviours change on terminal instances; ErrInstanceTerminal and
ErrTaskNotOpen wrap ErrInvalidTransition, so service/ and transport/
mapping is unchanged.

Discharges the first of the three follow-up ADRs owed by ADR-0164.
```
