# ADR-0183 Adversarial Audit — LENS A (EXECUTION)
Branch feat/human-task-claim-invariant, head a2aff201. STEP 0: bundle PRESENT (all 4 files).

## Findings (appended as observed)

### F1 — CONFIRMED TRUE (no defect): the inbox double-listing numbers are right
Attacked: "AssignedTo(alice)=1 AND ClaimableBy(alice)=1 for an Unclaimed row carrying a claim,
with Eligibility.Roles ["mgr"] + Candidates ["alice"], actor alice/mgr."
Ran (SQLite, dbtest.RunTestSQLite, EXIT=0):
```
PROBE A upsert_err=<nil> get_err=<nil> state=claimed claim=<nil>
PROBE B upsert_err=<nil> get_err=<nil> state=unclaimed claim=&{{alice [] map[]} 2026-08-17 10:00:00 UTC}
PROBE inbox AssignedTo(alice)=1 ClaimableBy(alice)=1
  ASSIGNED id=p-unclaimed-claim ... CLAIMABLE id=p-unclaimed-claim ...
PROBE C upsert_err=<nil> state=completed completion=<nil> claim=<nil>
```
The corrected number (1, not 0) is the right one. The `Claimed`+nil row (p-claimed-nil) appeared
in NEITHER inbox, also as claimed. All three round-trip rows reproduce. No finding against the
measurement itself.

### F2 — MAJOR (CONFIRMED by construction, execution below): the prescribed "double-listing
### regression" assertions in plan Task 2 Step 1 are VACUOUS — they can never execute in the
### only scenario that would make them fail.
The plan's conformance case is:
```go
assert: func(t *testing.T, err error) { require.ErrorIs(t, err, humantask.ErrInvalidTask, ...) }
...
tc.assert(t, ts.Upsert(t.Context(), tc.task))
_, err := ts.Get(...); require.ErrorIs(t, err, humantask.ErrTaskNotFound, ...)
assigned, _ := ts.AssignedTo(...); claimable, _ := ts.ClaimableBy(...)
for ... require.NotEqual(tc.task.TaskID, got.TaskID)   // "the consequence the rejection prevents"
```
`require.ErrorIs` is FailNow (runtime.Goexit). So:
- guard present  -> Upsert errors, row never exists, the Get/AssignedTo/ClaimableBy checks are
  TAUTOLOGIES (a row that was never written cannot be listed);
- guard absent/regressed -> `tc.assert` FailNows on the FIRST line and the subtest aborts, so the
  Get and the two inbox loops NEVER RUN.
=> The three assertions the plan calls "the consequence the rejection exists to prevent" and the
Verification Checklist calls "the double-listing consequence is pinned" have ZERO discriminating
power in either direction. This is the repo's recurring vacuous-assertion failure, one level up
from the vacuous-FIXTURE failure the bundle correctly guarded against.
FIX: in this group only, use `assert.ErrorIs` (non-fatal) in the closures and `assert.*` for the
persistence/inbox checks, so a regressed guard actually reports "rejected row reachable via
AssignedTo/ClaimableBy" instead of aborting at line 1. (Keep `require` for the query errors.)
Then mutation-verify: delete the store guard and confirm the FAILURE MESSAGE names the inbox
reachability, not just ErrorIs. The plan prescribes a mutation for Task 3 but NOT for Task 2 —
add one.

### F3 — MINOR/MAJOR (CONFIRMED): the evidence file's stated reason for not measuring the
### Postgres/MySQL legs is refuted by its own run.
Claim (premise-evidence l.96-100): "the store package's postgres and mysql subtests did NOT run
(no Docker probe)". But the same blast-radius run lists `./persistence/...` and `./runtime/...`
as passing (EXIT=0, 26 packages ok). Verified: `internal/dbtest.RunTestDatabase` /
`RunTestMySQL` use `require.NoError(t, err, "start postgres container")` (postgres.go:181) —
they FATAL, they do NOT `t.Skip`. And `persistence/{migrator,facade_mysql,persistence,
chaining_e2e,...}_test.go` + `runtime/{rehydrate_durable,timer_txflow}_test.go` all call those
helpers. So with Docker down those two package trees CANNOT have returned EXIT=0.
Observed now: `docker info` -> DOCKER=up.
=> Docker was available; the one package where the guard is actually wired into SQL is the only
one whose two container dialects were left unmeasured, and the stated cause is not the real one.
FIX: re-run the blast-radius measurement with the full `./internal/persistence/store/` (all three
dialects) and restate the scope sentence honestly, or delete the "no Docker probe" justification
and say the legs were not run. Also drop `./persistence/...` and `./runtime/...` from the
"container-free" framing.

### F4 — CONFIRMED, no defect: the "zero test churn" blast-radius claim holds (partially verified)
Patched the exact ADR-0183 `Validate` body into `MemTaskStore.Upsert` + `HumanTaskStore.Upsert`
(cp backups). `go build ./...` EXIT=0.
- `./humantask/... ./processtest/... ./service/... ./runtime/task/... ./engine/... ./transport/http/...` -> EXIT=0
- `./persistence/ -run 'TestCachingTaskStore|TestNewSQLiteTaskStore|...'` -> EXIT=0
- `./runtime/ -run 'TestProcessDriver'` -> EXIT=0
- `./internal/persistence/store/ -run '.*/sqlite'` -> EXIT=0, **281 subtests PASS, 0 FAIL**
POSITIVE CONTROL (both stores, both directions, mandatory):
```
PROBE A upsert_err=workflow-humantask: invalid task: task "p-claimed-nil": state claimed requires a claim   get_err=task not found
PROBE B upsert_err=workflow-humantask: invalid task: task "p-unclaimed-claim": state unclaimed must not carry a claim   get_err=task not found
PROBE inbox AssignedTo(alice)=0 ClaimableBy(alice)=0     <- double-listing gone
MEMCTL claimed_nil=<err> unclaimed_claim=<err> completed_nil=<nil> get_ctl1=task not found
```
So both guards fire, a rejected Upsert persists nothing, and `Completed`+nil still passes (the
deliberate deferral). UNVERIFIED (needs Docker, not permitted in this brief): the Postgres and
MySQL conformance legs and the container-backed tests in `persistence`/`runtime`.

### F5 — CONFIRMED, no defect: plan Task 3's counter reasoning is correct, and its mutation is real
Constructed the plan's caching case WITHOUT `upserts`, with `caching_task_store.go` UNMODIFIED and
a strict `MemTaskStore`: `--- PASS: TestCachingTaskStore/PROBE_no-counter_variant`, EXIT=0.
=> the no-counter case would indeed pass with no production change; the counter is load-bearing.
Added the counter, still no production change: EXIT=1,
`caching_task_store_test.go:96: rejected Upsert must not reach the backing store; upserts went 0 -> 1`
=> the prescribed RED and the prescribed Step-6 mutation message both reproduce exactly.
Also verified the plan's ordering is safe: `errors.Is(err, ErrInvalidTask)` is satisfied in the RED
state (inherited from MemTaskStore), so the counter check is reached.
Note the pointer receiver matters: the existing harness already builds `&countingTaskStore{...}`
(caching_task_store_test.go:85), so the increments are observable. Plan is right.

### F6 — MAJOR (CONFIRMED): plan Task 2 Step 2's guard against the `-run` trap DOES NOT WORK
Claim: "⚠ If you see no `---` lines at all, the `-run` path is wrong and nothing ran — that green
exit is the trap, not a pass." Ran the plan's EXACT command today, before the test exists:
```
$ go test -count=1 -v -run 'TestHumanTaskStoreConformance/sqlite/upsert_rejects_a_state_claim_contradiction' ./internal/persistence/store/
EXIT=0
    --- PASS: TestHumanTaskStoreConformance/sqlite (0.01s)
testing: warning: no tests to run
ok  ... [no tests to run]
```
There ARE `---` lines — TWO, both PASS — because the `sqlite` parent subtest's body runs and only
its children are filtered. An implementer following the plan's own
`grep -E '^\s*--- (PASS|FAIL)'` sees `--- PASS: .../sqlite` and concludes the path is right, while
NOTHING ran. Worse, that grep FILTERS OUT the only real discriminator,
`testing: warning: no tests to run` / `[no tests to run]`.
FIX: replace the heuristic in Step 2 (and the identical Step 4 pattern) with an explicit check for
the leaf name and for the no-tests-to-run marker, e.g.
`grep -q 'no tests to run' /tmp/red3.log && echo "TRAP: NOTHING RAN"` plus
`grep -c 'claimed_without_a_claim' /tmp/red3.log` (expect 2 leaf lines). Same fix applies to Task 1
Step 4, whose `grep -cE '^\s*--- PASS'`-equals-6 check is sound only because it asserts a COUNT —
say so, since Task 2's does not.

### F2 — EXECUTED CONFIRMATION + verified fix
Wrote the plan's conformance group VERBATIM, store guard REMOVED (= today). Exact prescribed
command, EXIT=1. The ONLY assertions that fired:
```
Error: Expected error with "workflow-humantask: invalid task" in chain but got nil.
Test:  .../claimed_without_a_claim
Error: Expected error with "workflow-humantask: invalid task" in chain but got nil.
Test:  .../unclaimed_carrying_a_claim
```
The "must not persist the row" and BOTH "reachable via AssignedTo/ClaimableBy" assertions produced
NO output — they never executed (require.ErrorIs FailNow'd first). The double-listing is NOT pinned.
Applied my proposed fix (assert.* in this group) and re-ran the same RED:
```
Messages: sqlite: a rejected Upsert must not persist the row
Messages: sqlite: rejected row reachable via AssignedTo      Error: Should not be: "tok-unclaimed-claim-sqlite"
Messages: sqlite: rejected row reachable via ClaimableBy     Error: Should not be: "tok-unclaimed-claim-sqlite"
```
=> the fix makes the named consequence actually observable. FIX IS VERIFIED, not merely proposed.

### F7 — MAJOR/CRITICAL (CONFIRMED by execution): the bundle's backlog-32 interaction names the
### ONE path that IMMUNIZES the bad shape, and misses the one path that actually trips the guard.
Claim (spec l.208-223 AND ADR Consequences l.134-141): "`cancelOpenTasks` (`engine/state.go:649`)
re-emits `UpdateTask` for every *open* task — which includes `Claimed` ones — and a `Claimed` task
whose `Claim` was lost would then reach `Upsert`" ... "the guard converts backlog 32's silent
corruption into a loud error on that path — a benefit".
REFUTED. `cancelOpenTasks` sets `s.Tasks[i].State = humantask.Cancelled` BEFORE `Clone()`
(state.go:648-656), and ADR-0183 sub-decision 4 gives `Cancelled` NO claim rule. Executed:
```
CANCELPATH  id=t-lost2 state=cancelled claim=<nil> -> Validate=<nil>       <- guard does NOT fire
CANDRESOLVED id=t-lost state=claimed  claim=<nil> -> Validate=workflow-humantask: invalid task:
                                       task "t-lost": state claimed requires a claim  <- fires
```
The state-preserving re-emitter is `handleHumanCandidatesResolved` (`engine/step_triggers.go:612`):
it guards on `task.IsOpen()` and emits `UpdateTask{task.Clone()}` WITHOUT touching State or Claim.
Enumerated all EIGHT `UpdateTask{` sites in `engine/` (grep, non-test):
  state.go:656 Cancelled · step_cancel.go:40 Cancelled · step_stale_commands.go:171 Cancelled ·
  step_timers.go:93 Cancelled · step_triggers.go:581 sets Claim · :637 sets Claim ·
  :941 Completed+Completion · **:612 preserves State and Claim verbatim** — the only one.
Also CONFIRMED the first half of the assumption: a snapshot missing the `Claim` key decodes
silently to exactly the blocker-3 shape —
`LOSTCLAIM err=<nil> state=claimed claim=<nil>` (json.Unmarshal of `{"TaskID":"t-lost","State":1}`).
And the round trip is lossless when the key is present: `WIRE task={..."State":1,"Claim":{"actor":
{"id":"alice","roles":["mgr"]},"timestamp":"2026-08-17T10:00:00Z"}...}`, decoding back to
`state=claimed claim=&{{alice [mgr] map[]} ...}`.
FIX: rewrite the paragraph in BOTH spec and ADR to name `handleHumanCandidatesResolved`
(`engine/step_triggers.go:612`) as the path, delete the `cancelOpenTasks` citation, and DELETE the
`ASSUMPTION (unverified)` label — it is now executed, and it was half wrong. Do NOT keep the
"benefit on that path" sentence as written: the terminal-sweep path is immunized precisely because
`Cancelled` is unconstrained, which is a real (and acceptable) LIMIT of the chosen rule set and
belongs under "Accepted residuals". Recommend the plan add a lens/test for the :612 path.

### F8 — CONFIRMED, assumption is TRUE (settle the spec's open question): `ManualImmediate` does
### NOT reach a task store.
Claim (spec l.90-93, evidence l.46-50, `ASSUMPTION (unverified)`).
Closed enumeration + execution:
- `engine/step_nodes.go:733-755`: the `ut.Manual && ut.ManualImmediate` branch appends the
  `Completed` task to `c.s.Tasks` and `return nil, false, nil` — ZERO commands, so neither
  `AwaitHuman` nor `UpdateTask` is emitted at entry. (Read + the strategy's own return.)
- Exactly TWO `Upsert` call sites exist outside the stores themselves (re-derived by grep):
  `runtime/processdriver_action.go:468` (`performAwaitHuman`, which HARDCODES
  `State: humantask.Unclaimed` and never copies `t.Claim`, so it cannot emit a bad shape) and
  `:483` (`performUpdateTask`, verbatim `cmd.Task`). The evidence file's enumeration is correct.
- A `Completed` task is not `IsOpen()`, and all four Cancelled-emitters plus
  `handleHumanCandidatesResolved` guard on `IsOpen()`; `handleHumanCompleted` requires a token
  parked on the task. So no emitter can ever re-project a ManualImmediate task.
=> The assumption holds. Upgrade it from `ASSUMPTION (unverified)` to a verified statement.
⚠ ADJACENT GAP (Minor): grep shows `ManualImmediate` appears in NO engine or runtime test —
only `definition/activity/{options,activity}_test.go`, `definition/model/yaml_test.go` and
`service/instance_test.go:323`, and that last one is a **DTO-rendering** table row over a
hand-built `InstanceState`, not an execution of the branch. The branch the bundle is correcting
the comment on (step_nodes.go:755) therefore has no behavioural test at all. Plan Task 4 Step 2's
"a comment-only edit must not move a single test" is true but vacuous here.
FIX: state in the spec that the path is source-verified unreached, not test-covered, and add
"an execution test for the ManualImmediate branch" to the deferred/backlog list.

### F9 — MINOR (CONFIRMED): "inert" understates the filed direction's consequence.
Claim (spec l.65-67): "`Claimed`+nil is inert in both queries (`AssignedTo=0`, `ClaimableBy=0`)".
The numbers are right (my probe: p-claimed-nil appeared in neither inbox). But "inert" reads as
"harmless", and it is used to argue the *inverse* direction "carries the concrete consequence".
Measured, the filed direction produces a task that NO inbox query can surface: `AssignedTo` misses
it because `claimed_by` is the empty string, `ClaimableBy` misses it because the SQL restricts to
`state = 'unclaimed'`. That is a human task lost from every inbox — reachable only by task ID —
i.e. a stalled instance from any consumer driving work off the two inbox queries. It is a
different consequence, not a smaller one.
FIX: replace "inert" with the measured statement and its two causes, and stop using it to rank
the two directions.

### F10 — MINOR (CONFIRMED): `Unclaimed` is the ZERO VALUE of `TaskState`, which widens the
### breaking change beyond what the ADR describes.
`humantask/humantask.go:28`: `Unclaimed TaskState = iota`. Observed: a zero-valued `HumanTask`
prints `state=unclaimed`. So R2 (`Unclaimed ⟹ Claim == nil`) fires on any task whose `State` was
never set but which carries a `Claim` — including a decode that dropped only the `State` key
(the same backlog-32 surface as F7). The error a caller sees,
`state unclaimed must not carry a claim`, will read as wrong to someone who simply forgot `State`.
FIX: say so explicitly in the `Validate` doc comment and in the CHANGELOG entry — R2 doubles as a
"forgot to set State" detector, and that is deliberate. One sentence; no code change.

### F11 — CONFIRMED TRUE, no defect: plan Task 1 Step 5's inherited import claim.
`head -20 humantask/memory_test.go` shows `context, errors, testing, time, authz, humantask,
assert, require` all already imported, and `ErrTaskNotFound` exists
(`humantask/humantask.go:21`). The prescribed test compiles with no new imports. Claim verified.

### F12 — MINOR (PLAUSIBLE, not executed): duplicate package comment in `humantask`.
The plan's new `humantask/validate_test.go` opens with
`// Package humantask_test verifies the claim invariant enforced by Validate.` while
`humantask/memory_test.go:1` already carries `// Package humantask_test exercises MemTaskStore and
StaticActorResolver.` Two package doc comments in one package concatenate in `go doc` and are
flagged by revive/stylecheck ST1000-family rules in some configurations. UNVERIFIED whether this
repo's `.golangci.yml` enables them, but the repo-wide lint run is a Verification gate.
FIX: drop the `Package ` prefix in the new file (make it a plain comment, or place it below the
package clause).

### F13 — MINOR (PLAUSIBLE): plan Task 2 Step 3 makes ONE error in `HumanTaskStore.Upsert`
### deliberately unprefixed, unlike the other six.
The instruction "Return the validation error **unwrapped** by the store's own
`fmt.Errorf("workflow-store: upsert task %s: %w", …)` prefix" is a reasonable call (the message
already names the task), but every other failure in that method — including the closely-related
`errZeroAuditTime("claim")` path, which the neighbouring conformance group tests — DOES carry
`workflow-store: upsert task <id>: `. A future reader will read the bare return as an oversight.
FIX: keep the decision, but put the reason in the code as a one-line comment, and record it in the
ADR's API-surface section so it is a decision rather than an inconsistency.

## Restoration
`cp` backups restored; probe files deleted. `git diff --stat` EMPTY, `git status --porcelain` shows
only ANOTHER lens's untracked `zz_probe_lensb_*` files (not mine). `go build ./...` EXIT=0.
