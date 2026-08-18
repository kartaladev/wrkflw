# RE-AUDIT LENS D — the pre-commit seam (ADR-0183)

Worktree: scratchpad/wt-lens-d, detached at b3cc2593 (== feat/human-task-claim-invariant head).
STEP 0: bundle present — spec 298 lines, ADR 166, plan 783, adjudication 162. PASS.

## D1 — CRITICAL (CONFIRMED). The empty-claimant clause has a SECOND live route the bundle never enumerated: the CLAIM verb.

Bundle claim (ADR Context, closing para): "no live engine path *authors* an inconsistent shape …
The exposure is the public API plus a corrupted snapshot replayed through `:612`." And it enumerates
exactly ONE empty-claimant route: `POST /tasks/{token}/reassign {"to":""}` (Context bullet 4, audit B2).

FALSE. `handleHumanClaimed` (engine/step_triggers.go:577-581) authors
`Claim = &Claim{Actor: t.Actor}` + `State = Claimed` from the trigger's actor, and NOTHING
validates `Actor.ID`:
- `validatedTriggerKinds["engine.HumanClaimed"]` validates **TaskID only** (engine/trigger_validate.go:64)
- `TaskService.Claim` (runtime/task/service.go:194-204) runs `authz.Authorize` and nothing else —
  an actor with an EMPTY ID but a MATCHING ROLE authorizes fine.

Executed (`go test -count=1 -v -run '^TestProbeD1EmptyActorClaim$' ./runtime/`, EXIT=0):
```
PROBE D1 svc.Claim(empty-id actor with role) err = <nil>
PROBE D1 ApplyTrigger err = workflow-runtime: reject step: workflow-humantask: invalid task: task "da1kai183g3ks1taq4d0": state claimed has an empty claimant
PROBE D1 errors.Is(ErrInvalidTask) = true
PROBE D1 PERSISTED version 1 -> 1 ; task state=unclaimed claim=<nil>
```
Consequences the bundle does not carry:
1. **A FOURTH breaking change.** `claim` with an empty actor id succeeds today and starts failing.
   ADR Consequences names three.
2. **The 422 rationale is refuted.** Decision 4 justifies 422 `conflict_state` as "an engine-authored
   shape the caller cannot fix by editing the request". Here the caller CAN fix it (send an actor id):
   this is a 400. The bundle minted a dedicated 400 sentinel for the *reassign* twin and left the
   *claim* twin to the 422 — the two identical defects get opposite classifications.
3. B2's own reasoning ("R1's empty-claimant clause is safe only behind the pre-commit hook") was
   applied to one of two routes.

FIX: mirror Decision 3 for the claim verb. Validate `HumanClaimed.Actor.ID != ""` at the same
`engine/step.go:156` pre-`cloneState` site, under the SAME new sentinel, so both routes are refused
with 400 before any state is touched; add the claim route to the CHANGELOG's breaking list; and
re-word Decision 4's rationale (or keep 422 only for shapes with no caller-facing cause).
Also correct the ADR Context sentence "no live engine path authors an inconsistent shape".

## D2 — NO WEDGE (CONFIRMED). The mandate's most important question answers in the bundle's favour.

4 corrupt shapes x 4 verbs, corruption written DIRECTLY to the durable snapshot via
`MemInstanceStore.Load`/`Commit` (fixture IS constructible — see D6). All 16 subtests PASS.
`claim`, `complete` and `compensate_terminate` succeed on EVERY corrupt shape (version 2->3):
handleHumanClaimed rewrites Claim+State together, handleHumanCompleted sets Completed (unconstrained),
cancelOpenTasks sets Cancelled (unconstrained) BEFORE Clone. Only `refresh_candidates` (the :612
pass-through) is refused. **The guard does not refuse the operation that clears the bad state.**
Record this in the ADR as an executed consequence — it is the strongest argument for the design and
the bundle currently asserts it nowhere.

## D3 — MAJOR (CONFIRMED). "A contradictory shape can no longer be committed" is FALSE: the hook validates COMMANDS, never the snapshot `st.Tasks`.

Observed, out-of-range shape + refresh_candidates:
```
PROBE D2 out_of_range_state/refresh_candidates corrupted-durable: state=unknown claim=<nil>
PROBE D2 out_of_range_state/refresh_candidates => err=<nil> invalidTask=false | version 2->3 | taskstate=unknown
```
`handleHumanCandidatesResolved` returns `Commands: nil` for a non-open task, so the hook sees nothing
and the **R3-violating record is re-committed at version 3**. Every subsequent step that does not
happen to emit an `UpdateTask` for that task re-persists the corruption, silently and forever.
Also observed: `out_of_range_state/compensate_terminate` terminates the instance with
`taskstate=unknown` — `cancelOpenTasks` skips it (`IsOpen()` is `State==Unclaimed||State==Claimed`),
so a terminated instance keeps an un-cancelled task and its store row is never reconciled.

FIX (docs, not code — the code fix would be a snapshot-wide validator and is a bigger decision):
reword the ADR's positive consequence to what was measured — "a contradictory shape can no longer be
AUTHORED by the engine, nor reach a TaskStore" — and add to the residual list that a durable
out-of-range record is (i) re-committed unchanged by every step that emits no UpdateTask for it, and
(ii) permanently unactionable: claim/complete are refused with `ErrTaskNotOpen` and the terminal
sweep skips it. Observed:
```
out_of_range_state/claim     => workflow-engine: human task is not open: … invalid state transition
out_of_range_state/complete  => workflow-engine: human task is not open: … invalid state transition
```

## D4 — CRITICAL (CONFIRMED). "Pre-commit" means "before THIS ITERATION's commit". `deliverLoop` commits once per queued trigger, and the precedent the ADR quotes as its justification DOES strand an instance.

Bundle claims attacked:
- plan Task 3: "a rejection aborts the step before anything is persisted"; test named
  `TestPreCommitRejectionCommitsNothing`; "the persisted snapshot is BYTE-FOR-BYTE what step 2 wrote".
- ADR Consequences: "a rejection can no longer strand an instance".
- ADR Decision 1 + adjudication B1 quote `resolveHumanCandidates`' comment VERBATIM as the precedent:
  *"a resolver outage can no longer leave a committed instance parked on a task that was never
  written to the task store."*

`runtime/processdriver.go:608` is a `for len(queue) > 0` loop; `perform` appends follow-up triggers
(`:864-865`). The hook's `return st, verr` exits the LOOP — earlier iterations are already durable.

Executed the precedent itself. Def `start -> serviceTask("a") -> userTask`, no ActorResolver, ONE
`Drive` call (`go test -count=1 -v -run '^TestProbeD4MultiCommitPerCall$' ./runtime/`, EXIT=0):
```
PROBE D4 Drive err=workflow-runtime: resolve candidates for task …: no ActorResolver configured status=running ; durable version after ONE Drive call = 1
PROBE D4 PERSISTED: status=running tokens=1 incidents=0 tasks=0 history=2 vars=map[]
PROBE D4   token[0] node="task" await="da1kc0h83g3ldior32e0"
PROBE D4 RETRY Drive err = workflow-runtime: commit: workflow-runtime: instance already exists
PROBE D4 journal entries = 1
```
That is **B1's harm shape verbatim** (`status=running incidents=0 tokens=1`, token parked on a
command nobody will answer) produced by the seam chosen to FIX B1. Worse than B1: the follow-up
trigger (`ActionCompleted`) is minted INSIDE the loop and lost, so the caller cannot replay it —
`Drive` now returns `instance already exists`. Permanently un-advanceable, no incident.

Is the NEW hook reachable in iteration >=2 today? **No — and by accident.** All 8 `UpdateTask` emit
sites (re-derived: step_timers.go:93, step_triggers.go:581/612/637/941, state.go:656,
step_cancel.go:40, step_stale_commands.go:171) either normalize `State` to `Cancelled`/`Completed`
(both unconstrained by R1/R2/R3) or are reached only from an EXTERNAL human trigger, i.e. iteration 1.
`state.go:656` and `step_stale_commands.go:171` ARE follow-up-reachable — they are safe only because
`Cancelled` happens to have no claim rule. Nothing in the bundle names that as the reason.

FIX (three parts, all cheap):
1. Reword every unqualified claim: the hook aborts before **this step's** commit; a `deliverLoop`
   call can commit several steps, so the property is "no *contradictory task* is committed", not
   "nothing is committed". Rename the prescribed test `TestPreCommitRejectionDoesNotCommitTheStep`.
2. Add the missing invariant to `validateTaskCommands`' doc comment: *the hook is only strand-free
   while every `UpdateTask` reachable from a perform-generated follow-up trigger normalizes `State`
   to `Completed` or `Cancelled`; a new emit site in a follow-up-reachable handler must be re-checked.*
   Enumerate the two follow-up-reachable sites by name.
3. Delete the inherited-and-false quotation from ADR Decision 1 / the adjudication, or hedge it with
   the measurement above. Premise Discipline: it was restated as plain fact without execution, and it
   is false for any definition whose user task is not the first node.

## D5 — MAJOR (CONFIRMED). The plan's ONE ordering warning names the wrong arm, and hides the arm that really is load-bearing (a nil-deref PANIC).

Plan Task 1 Step 3: "⚠ R3 must be the **first** arm: the later arms compare against
`Claimed`/`Unclaimed`, and an out-of-range value must not fall through to them."

FALSE, and mutation-verified. R1/R2's arms test `State == Claimed` / `State == Unclaimed`; an
out-of-range value matches NEITHER, so it always falls through to R3 wherever R3 sits. The rule sets
are disjoint by construction (R1/R2 only fire on in-range states), so the order is irrelevant.

MUTATION 1 — R3 moved to the LAST arm. All 10 shapes byte-identical to the baseline:
```
state99+claim    err=…: unknown state 99
state-1+nil      err=…: unknown state -1
state99+nil      err=…: unknown state 99
```
(baseline and mutant printed the same line for every one of the 10 shapes.)

MUTATION 2 — arms 2 and 3 swapped (`empty claimant` before `requires a claim`):
```
PROBE D5 claimed+nil      PANIC: runtime error: invalid memory address or nil pointer dereference
```
**That** is the real constraint: the `t.Claim == nil` arm must precede the `t.Claim.Actor.ID` arm or
`Validate` panics on the single most likely corrupt shape — inside a pre-commit hook on the hot path,
i.e. a panic that takes the step down. The plan does not mention it, and its 9-row table WOULD catch
it (`t-2`) only because the row happens to exist — nothing in the plan says that row is guarding a
nil deref, so a future reorder-and-trim of the table disarms it silently.

FIX: delete the R3-first warning (it is a false claim in a design document — Premise Discipline).
Replace it with: *"the `Claim == nil` arm MUST precede the `Claim.Actor.ID` arm — swapping them makes
`Validate` panic on `Claimed`+nil (mutation-verified)."* Add a comment on that arm in
`validate.go` itself, and mark the `t-2` table row as the guard for it. Optionally restructure as
`if t.Claim == nil { … } else { … }` so the dependency is structural rather than positional.

## D6 — CONFIRMED, no defect. The plan's Task 3 Step 2 fixture IS constructible; the plan may stop hedging.

`kernel.AppliedStep{State, Trigger}` is sufficient (`runtime/kernel/ports.go:51-61`: Events,
NewCallLink, CallOutcome are all optional). `MemInstanceStore.Load` returns `state.Clone()` and
`HumanTask.Clone` guards `if t.Claim != nil`, so a nil `Claim` survives the round-trip — observed
directly:
```
PROBE D2 claimed_nil_claim/refresh_candidates corrupted-durable: state=claimed claim=<nil>
```
and the prescribed discriminating assertion holds: `version 2->2` on rejection, vs `2->3` on every
path that commits. **The test can fail today** (today the step commits and the version advances).
One un-hedged wrinkle to add: the fabricated `AppliedStep.Trigger` lands in the JOURNAL, so any
`store.Entries()` length assertion in the same test must account for it.

## D7 — MINOR (CONFIRMED by source). Ordering vs `resolveHumanCandidates` is safe, but the mandate's premise about it is wrong.

`resolveHumanCandidates` (`runtime/processdriver_action.go:236-265`) writes exactly one field:
`task.Candidates = authz.CloneActors(actors)` on `st.TaskByID(cmd.TaskID)`. It does **not** mutate the
commands (and cannot: `engine.AwaitHuman` is `{TaskID, Eligibility}` — `engine/command.go:148-151`,
no Candidates field). It never touches `State` or `Claim`. So validating first cannot be invalidated
afterwards. `engine.UpdateTask` is also the ONLY command type carrying a `humantask.HumanTask`
(sole `humantask.HumanTask` field in `engine/command.go`), so "UpdateTask only" is structurally
complete for commands.

Residual worth one sentence in the ADR: on a hook rejection the step span is `End()`ed WITHOUT
`wrkflw.command_count` / `wrkflw.status`, because `span.SetAttributes` runs after
`resolveHumanCandidates`. Same as an existing resolver failure, so it is a pre-existing shape — but an
operator reading traces for these rejections gets a span with neither attribute.

## D1 (reinforcement, CONFIRMED). The claim route is a DOCUMENTED contract the bundle breaks silently.

`transport/http/httpcore/dto.go:40-44`, verbatim:
```go
// ClaimInput is the request body for POST /tasks/{token}/claim.
// No fields are required — an empty actor is allowed.
type ClaimInput struct { Actor Actor `json:"actor"` }
```
and `endpoints.go:116-120` passes `authz.Actor{ID: in.Actor.ID, …}` straight through. So the empty-ID
claim is reachable end-to-end over HTTP exactly like the reassign twin — with a doc comment that
promises it works. `ClassifyError`'s `default:` arm is confirmed at
`transport/http/httpcore/errors.go:50-52`: `500 / {"error":"internal_error"}` — so the bundle's
"empty body" wording is slightly off (there IS a body, just no message). Raise D1 to CRITICAL and
correct that doc comment in-bundle (Delivery-Gate item 2).

## D8 — MAJOR (CONFIRMED code path, PLAUSIBLE reachability). The bundle classifies the rejection for ONE of five entry points.

Decision 4 designs only the HTTP classification. `applyTrigger`/`deliverLoop` have four other
entries, and each swallows the rejection differently:
- **timer fire** (`runtime/timerops.go:344-353`): a non-`ErrConcurrentUpdate` error is **logged at
  ERROR and DROPPED**; the job's `fn` returns nil regardless (`timerops.go:313`), so the scheduler
  believes the fire succeeded, no incident is raised, and the durable timer row is NOT deleted
  (the delete lives in `commitFn`, which never ran) — so it re-fires and re-fails on every boot.
  Deadline breaches and reminders silently stop happening.
- **child cancel propagation** (`processdriver_cancel.go:95`): logged WARN, loop continues.
- **message delivery** (`processdriver_message.go:71`): returned to the publisher.
- **`Drive`/child create** (`processdriver_child.go:60`, `processdriver.go:465`).
Only the HTTP one produces the 422 the ADR designs.

FIX: add one paragraph to ADR-0183 Consequences enumerating the five entries and what a rejection
does at each, and state explicitly that at the timer seam the rejection is a **silent drop with no
incident** — so it is NOT a design goal to reach the hook from a timer fire. That is the same class
of finding `/code-review` raised on ADR-0177/0180 ("ask what a guard must STILL DO").

## D9 — MINOR (CONFIRMED by source). The abort skips no cleanup, but it does leak `instActive` on a late iteration.

Verified at `runtime/processdriver.go:664-685`: the early `return st, verr` sits before the metrics
block, before `commitFn`, before scheduler activate/deactivate, before `syncWaiters(st)` and before
the `perform` loop — all correct to skip, since this iteration committed nothing. It calls
`span.End()` explicitly (no leak) and `ApplyTrigger`'s `defer release()` still runs, so the shutdown
semaphore is released. The `if create` at `:686` is the METRICS block, not a creation branch — the
real `store.Create` is inside `commitFn`, so a first-iteration abort leaves NO row.

The leak: on an iteration >= 2 abort (D4's shape) `create` was already consumed, so `instStarted` and
`instActive` were incremented in iteration 1 and `instActive.Add(-1)` only ever runs on a terminal
transition — which the stranded instance can never reach. Live today for the resolver path (D4's
measurement is exactly that instance).

Also observed (D1): the returned `st` on rejection is the **contradictory, uncommitted** state —
`PROBE D1 returned st.Tasks[0]: state=claimed claim=&{Actor:{ID: …}}` while the durable snapshot reads
`state=unclaimed claim=<nil>`. Every current caller returns early on error, so this is latent, not a
live defect; the ADR should say the returned state on a rejection is the REJECTED projection, not the
committed one.

## D10 — MINOR (CONFIRMED). R3's range form is exact today but silently coupled to iota contiguity.

`t.State < Unclaimed || t.State > Cancelled` with `Unclaimed = iota` == 0 and Cancelled == 3 covers
the declared set exactly — verified for 99, -1 and all four in-range values (D5 baseline). It is
correct only while the four constants stay a contiguous iota block: a fifth constant declared inside
the block silently becomes "declared", and one declared outside silently becomes "unknown", with no
test failing either way.

FIX: use an explicit `switch t.State { case Unclaimed, Claimed, Completed, Cancelled: default: … }`,
or add a comment naming the contiguity dependency and a test that pins `Cancelled == 3`. Note
`TaskState.String()` already enumerates the four explicitly (`humantask/humantask.go:41-53`), so the
enumerated form is the house style for this type.
