# AUDIT LENS B — end-to-end reachability & consumer seam
STEP 0 PASS: bundle present at a2aff201 on feat/human-task-claim-invariant

## F1 (Major, CONFIRMED-by-grep) The bundle enumerated STATE-ASSIGNMENT sites, but the guard fires on UpdateTask EMIT sites — and there are 8, one of which authors no claim shape at all.
`grep -rn --include='*.go' "UpdateTask{" . | grep -v _test.go` → 8 sites:
engine/step_timers.go:93, step_triggers.go:581, :612, :637, :941, state.go:656, step_cancel.go:40, step_stale_commands.go:171.
`step_triggers.go:612` (`handleHumanCandidatesResolved`) emits UpdateTask WITHOUT touching State or Claim — it is a pass-through refresh that re-emits whatever shape instance state already holds. The spec's writer table (spec:78-85) enumerates only `State = ...` assignments, so it structurally cannot see pass-through emitters. Same ADR-0179 class: the gap lives in the seam.

## F2 (Major, CONFIRMED by execution) The guard closes `Claimed`+nil but ADMITS `Claimed`+empty-claimant — reachable from the HTTP reassign route, identical harm.
Probe 1 (engine, /tmp/lensb_probe1.log, EXIT=0):
  reassign to="" → err=<nil>; UpdateTask: state=claimed claim=&{Actor:{ID: ...}}; proposed-Validate-would-reject = false
Probe 2 (SQLite store, guard INSTALLED in tree by lens A — the real ADR body):
  Upsert(Claimed, Claim{ID:""}) err = <nil>
  AssignedTo("")=0  AssignedTo("alice")=0  ClaimableBy(alice)=0
`to` is never validated: dto.go:60-67 comment says "No fields carry explicit required-field
validation in the rest handler"; trigger_validate.go:65 checks only TaskID for HumanReassigned;
TaskService.Reassign (runtime/task/service.go:214-231) checks `from` against the claimant but
never `to`.
⇒ POST /tasks/{token}/reassign {"from":"alice","to":""} yields a Claimed row invisible to every
inbox query — the same "inert in both queries" profile the spec attributes to Claimed+nil.
FIX: extend R1 to `State == Claimed ⟹ Claim != nil && Claim.Actor.ID != ""`, and/or reject an
empty `To` in NewHumanReassigned/handleHumanReassigned. At minimum the spec must state that the
empty-claimant variant is NOT closed (today it silently reads as if it were).

## F3 (Major, CONFIRMED) `ErrInvalidTask` has no arm in `ClassifyError` → 500 with an EMPTY body.
transport/http/httpcore/errors.go:26-52 — default arm returns
`http.StatusInternalServerError, ErrorBody{Error: "internal_error"}` (Message dropped for 5xx).
The bundle never mentions the HTTP surface. Every consumer-facing task route (claim/complete/
reassign) funnels through `perform`→Upsert, so a validation failure becomes an opaque 500 with the
diagnostic text (which names the task and the contradiction) discarded.
FIX: decide and record. Either (a) add an `errors.Is(err, humantask.ErrInvalidTask)` arm — 500 is
arguably right for an engine-authored shape, but then say so in the ADR; or (b) map to 422
`conflict_state` when the shape came from a caller-supplied field. Also: the errors.go arm list is
the repo's own precedent for "a sentinel that reaches HTTP gets classified" (see the
ErrInvalidOutcome/ErrEmptyTriggerKey comments at :38-46) — silence here is a gap, not a decision.

## F4 (CRITICAL, CONFIRMED by execution) A rejected Upsert inside perform() PERMANENTLY STRANDS the instance: state committed, remaining commands DROPPED, ZERO incidents, and the trigger cannot be retried.
Probe 3 (/tmp/lensb_probe3.log, EXIT=0). Def: start → userTask(manual) → serviceTask(action) → end.
TaskStore rejects the Completed upsert with humantask.ErrInvalidTask (exactly what Validate will do).
  PROBE ApplyTrigger err = workflow-runtime: update task: workflow-humantask: invalid task
  PROBE returned state: status=running incidents=0 tokens=1 actionRan=0 rejectHits=1
  PROBE   token[0] node=act state=1 await="...kos0"
  PROBE PERSISTED state: status=running incidents=0 tokens=1
  PROBE   ptoken[0] node=act state=1 await="...kos0"   ptask[0] state=completed
  PROBE RETRY same trigger err = workflow-runtime: step: workflow-engine: human task is not open:
        workflow-engine: invalid state transition
Mechanism (source): runtime/processdriver.go:846-860 — perform() runs AFTER the commit
(processdriver_action.go:282-286 says so), and `if err != nil { return st, err }` aborts the
REMAINING command queue. engine/step_triggers.go:941 puts `UpdateTask` FIRST and appends the drive
commands after it, so the InvokeAction for the next service task is never dispatched. No incident
is recorded (incidents=0) and the token is parked on a command id that will never be answered.
⇒ The bundle designs the guard and never traces this. It also makes the spec's own "benefit"
claim (spec:220-222 / ADR:134-141 — "converts backlog 32's silent corruption into a loud error")
WRONG as stated: it converts it into a stranded instance with no incident.
FIX (pick one, record it): (a) classify a Validate failure as a permanent error and raise an
incident / fail the instance instead of returning bare — so an operator can see it; (b) validate
the emitted UpdateTask/AwaitHuman commands PRE-COMMIT (the precedent is already in this file:
resolveHumanCandidates at processdriver.go:667-673 runs pre-commit for exactly this reason and its
comment says "a resolver outage can no longer leave a committed instance parked on a task that was
never written to the task store"); (c) at minimum, add the ADR Consequence stating this residual.
Option (b) is the repo's own established answer to this precise failure shape.

## F5 (Major, CONFIRMED) `Validate` is documented as a general-purpose public predicate, but the engine DELIBERATELY mints an inconsistent HumanTask into instance state — the deferred completion axis will collide with it.
Probe 4 (/tmp/lensb_probe4.log, EXIT=0) upgrades the spec's ASSUMPTION to a measurement:
  PROBE immediate: driveErr=<nil> status=completed tasks=1 upsertsSeen=0
  PROBE   task[0] state=completed claim=<nil> completion=<nil>
  PROBE   store.Get(...) err=workflow-humantask: task not found
⇒ The carve-out DOES hold for STORE writes. But the `Completed`+nil+nil record is live in
instance state and is reachable by consumers via `service.ProcessInstance.ActiveTasks()` /
the history DTO (`service/instance.go:72-95`, `:238-239`). ADR sub-decision 3 says Validate is
"named generally … so the deferred completion axis extends it without a rename" — the moment it
does, `humantask.Validate` (the rule the interface doc tells consumers to call) will reject a
record the engine authored on purpose.
FIX: state in the ADR that `Validate` is a **TaskStore-write** contract, not a whole-model
invariant, AND record that the deferred completion axis must carve out ManualImmediate (or that
path must record a synthetic Completion) — the spec's Deferred item 1 half-says this; the ADR's
Consequences do not, and the godoc in the plan (validate.go doc block) says only "an immediate
manual task completes without one", which reads as permanent permission rather than a deferral.

## F6 (Major, PLAUSIBLE→CONFIRMED-partially) `processtest` — the PUBLIC consumer test harness — hands consumers a raw `*humantask.MemTaskStore` they seed directly, and the CHANGELOG entry does not mention it.
`processtest/harness.go:345`: `func (h *Harness) Tasks() *humantask.MemTaskStore { return h.tasks }`.
Any consumer test that seeds a fixture task by hand through `h.Tasks().Upsert(...)` breaks. The
spec's "zero test churn, measured" (spec:161-182) is a measurement over THIS repo only; it says
nothing about consumer suites, and `processtest` is exactly the surface ADR-0179's CHANGELOG entry
had to call out ("`processtest` consumers, even with retry OFF"). This bundle's entry (plan Task 4
Step 3) does not name `processtest` at all.
FIX: add `processtest` to the CHANGELOG breaking entry, and state that the break is SILENT for a
consumer's own `TaskStore` implementation — the interface signature does not change, so nothing
compiles differently; a non-conforming store simply keeps accepting bad rows.

## F7 (Minor→Major, CONFIRMED) A consumer implementing `TaskStore` is told to uphold a contract they have NO WAY TO TEST, and the bundle defers the only thing that would fix that.
The ADR makes the invariant a documented interface contract (`humantask/humantask.go:186`), yet
spec "Deferred" item 2 defers a cross-implementation conformance suite. There is no exported
`humantask.TestTaskStoreConformance(t, factory)` helper. Consumers get a MUST with no gate. The
repo already has the shape for it — `internal/persistence/store/humantask_store_conformance_test.go`
is a dialect suite locked inside `internal/`, unusable by a consumer.
FIX: either export a minimal conformance helper in this bundle (it is small: the two directions
plus "a rejected Upsert persists nothing"), or state in the ADR that the contract is advisory
until that suite exists — do not present it as "a canonical rule consumers get".

## F8 (Minor, CONFIRMED) The choke-point enumeration for Q1 is CLEAN — recorded so it is not re-derived.
`grep -rn --include='*.go' "\.Upsert(" . | grep -v _test.go` → exactly 3 sites:
  runtime/processdriver_action.go:468 (AwaitHuman create), :483 (UpdateTask), persistence/caching_task_store.go:99.
`grep -rn --include='*.go' "wrkflw_human_task" .` → exactly ONE mutating statement,
`internal/persistence/store/humantask_store.go:155` (INSERT ... upsert), inside `Upsert`. No other
store method mutates claim state; `runtime/task/service.go` never writes (it returns Triggers —
its own doc at :257 says "this method never writes to the store itself"). TaskStore
implementations: 3 (`MemTaskStore`, `store.HumanTaskStore`, `persistence.CachingTaskStore`);
`persistence/humantask.go:60` is a re-export assertion, not a fourth; there is NO generated
MockTaskStore. ⇒ the bundle's "all three implementations" claim is correct.

## F9 (Minor, CONFIRMED) Terminal sweep: a failed UpdateTask drops the REMAINING tasks' reconciliation.
`engine/state.go:645-659` `cancelOpenTasks` emits one UpdateTask per open task; `state.go:623-627`
appends the terminal command and `cancelAllScheduledWork()` AFTER them. Cancel actions are safe —
`step_triggers.go:320,337` PREPEND them — and ScheduleTimer/CancelTimer never reach `perform`
(`processdriver.go:846-852`). But with N open tasks, a rejection on task 1 drops tasks 2..N, so a
terminated instance keeps advertising them in `ClaimableBy`/`AssignedTo` — the exact harm
`cancelOpenTasks`' own doc comment (state.go:640-644) exists to prevent. Same root cause as F4.
FIX: covered by F4's option (b) (pre-commit validation) or by making perform's loop collect errors
instead of returning on the first one for the terminal sweep.

## F10 (Major, CONFIRMED by reading the prescribed test's control flow) The bundle's headline regression test — the double-listing guard — CANNOT FAIL on the axis it is named for.
Plan Task 2 Step 1 (plan:458-483) does, per case:
  tc.assert(t, ts.Upsert(...))          // require.ErrorIs → t.FailNow on a broken guard
  _, err := ts.Get(...)                  // require.ErrorIs ErrTaskNotFound
  assigned := ts.AssignedTo(...); claimable := ts.ClaimableBy(...)
  for ... require.NotEqual(tc.task.TaskID, got.TaskID)
- If the guard WORKS: the row was never inserted, so the two loops are trivially satisfied for
  any implementation. They assert nothing.
- If the guard is BROKEN: `require.ErrorIs` FailNows inside `tc.assert`, and the inbox assertions
  are never reached.
⇒ In both branches the double-listing assertions are unreachable-as-evidence. The spec elevates
this row to the load-bearing regression (spec:198) and attaches a ⚠ about the fixture declaring
`Candidates`/`Eligibility.Roles` — but that fixture detail only matters for a row that is
PERSISTED, which this test structure guarantees never happens. The ⚠ guards an unreachable case.
This is the CLAUDE.md "check the FIXTURE, not the line" failure inverted: here the fixture is
right and the CONTROL FLOW is what makes the assertion vacuous.
FIX: add a POSITIVE CONTROL to the same group — upsert the same fixture with a LEGAL shape
(`State: Claimed` + that claim, and `State: Unclaimed` + no claim) and assert `AssignedTo("alice")`
/ `ClaimableBy(alice)` DO return it. That proves the eligibility fixture discriminates and that the
inbox queries would have listed the row, which is the only way the "never double-listed" claim
earns its evidence. Alternatively drop the inbox loops and say plainly that non-persistence is the
whole assertion.

## F11 (Minor, CONFIRMED) Cross-document check — the enumerations and counts HOLD; two wording gaps.
Re-derived independently (not inherited):
  State=Claimed 2 (step_triggers.go:578,:634 — both preceded by a Claim assignment at :577,:633) ✓
  State=Unclaimed 2 (processdriver_action.go:439, step_nodes.go:733) ✓
  State=Completed 2 (step_triggers.go:928, step_nodes.go:755) ✓
  State=Cancelled 4 (step_timers.go:89, step_cancel.go:39, state.go:649, step_stale_commands.go:170) ✓
  `.Claim = nil` sites: 0 ✓   TaskStore impls: 3 ✓   `.Upsert(` non-test sites: 3 ✓
  CHANGELOG.md:18 is indeed `### Breaking changes (pre-v0.1.0 — no stability promise)` ✓
  memory_test.go imports (context/errors/testing/time/authz/humantask/assert/require) ✓
  conformance callback has `ts` and `b.name` in scope; the cited sibling group is at :532 (plan says ~531) ✓
Gaps: (1) the spec/ADR both frame the writer enumeration as covering the risk surface, but the
guard fires on EMIT sites (F1) — the tables are labelled as if they were the same set;
(2) spec:90-93 and ADR:141 still carry `ASSUMPTION (unverified)` for the ManualImmediate claim,
which is now EXECUTED (F5) — Premise Discipline requires replacing the label with the measurement.

## PROCESS NOTE (for the controller)
Another audit lens (A) has PATCHED THE SHARED WORKING TREE: `git status` shows
`M humantask/memory.go`, `M internal/persistence/store/humantask_store.go`, plus
`humantask/zzprobe_validate.go` and two `zzprobe_lensa_test.go` files. My probes 1-4 therefore ran
against a tree where the ADR-0183 guard was ALREADY INSTALLED (verified: the patch body is
character-for-character the ADR's Validate). That is what made F2 measurable, but two lenses
mutating the same packages concurrently violates the repo's own "fan out BY GO PACKAGE" rule and
risks one lens restoring over the other's patch. I removed only my own probe files.
