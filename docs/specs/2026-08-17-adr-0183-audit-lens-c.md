# Audit Lens C — RE-COUNTING — ADR-0183 bundle
STEP 0: all four files present. Branch feat/human-task-claim-invariant.

## F1 CRITICAL — the writer enumeration is INCOMPLETE: a 5th production writer of HumanTask.State exists (scanTask), and it MANUFACTURES an R2 violation
grep -rn "\.State = " --include="*.go" . | grep -v _test.go
=> internal/persistence/store/humantask_store.go:363  t.State = htParseTaskState(stateStr)
htParseTaskState default: returns humantask.Unclaimed for ANY unrecognized string.
scanTask then rebuilds t.Claim whenever claimed_at is non-NULL, INDEPENDENTLY of state.
=> read path can produce State: Unclaimed + Claim != nil  == R2 violation, from production code.
Bundle enumerated only sites naming the 4 constants; never grepped `.State =` generically.

## MEASUREMENT CAVEAT (my own)
The shared primary tree is CURRENTLY PATCHED by concurrent lenses A/B:
 M humantask/memory.go, M internal/persistence/store/humantask_store.go (Validate guard added)
 ?? humantask/zzprobe_validate.go (throwaway Validate+ErrInvalidTask)
 ?? 4 zzprobe test files (engine, humantask, store x2)
So my store-package runs are against a STRICT tree. Noted per measurement.

## F2 CONFIRMED — "Both `Upsert` call sites" is 2, real is 3
grep -rn "\.Upsert(" --include="*.go" . | grep -v _test.go
 runtime/processdriver_action.go:468, :483, persistence/caching_task_store.go:99
Evidence file "Convention notes": "Both `Upsert` call sites live in runtime/processdriver_action.go:468 and :483".
Third site is the very one Task 3 modifies.

## F3 CONFIRMED — "three TaskStore impls plus A test double" undercounts doubles
grep -rn "^func (.*) Upsert(" => MemTaskStore, HumanTaskStore, CachingTaskStore, capturingTaskStore(test).
BUT countingTaskStore (persistence/caching_task_store_test.go:21) embeds *MemTaskStore and
therefore ALSO satisfies humantask.TaskStore => TWO test doubles, not one. The plan uses it.

## F4 CONFIRMED — 26 packages: correct
go list -f '...' <the 7 patterns> | awk '$2+$3>0' | wc -l  => 26  (27 pkgs matched, 1 has no tests)

## F5 — 280 subtests: real number on the current tree is 283
go test -count=1 -v -run '.*/sqlite' ./internal/persistence/store/ => EXIT=0 PASS=283 FAIL=0 SKIP=0
Delta of exactly 3 == the 3 concurrently-added ZZProbe top-level tests => 280 CORROBORATED.
BUT the run executes ~17 Docker-requiring TOP-LEVEL tests (TestOwnership_Postgres_* x6,
TestOwnership_MySQL_* x3, TestPgxNotifier* x5, TestMigrationParity_* x2,
TestPrunerReclaimNeverDueTimersPostgres). So "no Docker" + "0 FAIL" needs checking.

## F6 CONFIRMED — seven packages whose tests call .Upsert(: correct
grep -rln "\.Upsert(" --include="*_test.go" . | xargs -n1 dirname | sort -u
 => humantask, internal/persistence/store, persistence, processtest, runtime, runtime/task, service
BUT "all seven ran": internal/persistence/store was run ONLY under -run '.*/sqlite' (a FILTER),
not in the 26-package run. See F7.

## F7 CONFIRMED — "every case assigns task.Claim before upserting" is FALSE
git show HEAD:...conformance_test.go | sed -n '532,578p'
Group upsert_rejects_an_audit_record_with_no_timestamp has 2 cases:
 1 "claim without a timestamp": base()=Claimed, then task.Claim = &Claim{u-jane} -> assigns Claim ✓
 2 "completion without a timestamp": base()=Claimed, then task.State = Completed,
   task.Completion = ... -> NEVER assigns task.Claim.
It survives only because it REASSIGNS State to Completed (deliberately unconstrained),
not because it assigns a Claim. The stated reason is refuted by the fixture.

## F8 CONFIRMED-OK (counts that hold)
- 35 State: Claimed composite literals: git grep HEAD => 35 ✓ (all in _test.go)
- zero `.Claim = nil` / `Claim: nil` anywhere ✓ (grep empty)
- humantask sentinels: exactly one committed, ErrTaskNotFound (humantask.go:21) ✓
- three TaskStore Upsert impls in production ✓
- seven packages whose tests call .Upsert( ✓
- 26 packages ✓
- Unclaimed 2 / Completed 2 / Cancelled 4 production sites ✓ (both forms checked)
- step_triggers.go:578 and :634 both set task.Claim on the IMMEDIATELY preceding line ✓
- ALL file:line citations resolve on the clean tree (incl. conformance :531/:539/:542,
  humantask.go:104/:186, store_core.go:78/:216, CHANGELOG.md:18) ✓
- TestValidate rows in the plan = 6 ✓

## F9 CONFIRMED — the ADR sentence is self-contradictory about the two forms
"Re-derived uniformly across assignment *and* composite-literal forms: State = Claimed has 2 sites"
Composite-literal form has 35 sites; assignment form has 6 (4 in tests). "2" is
production-assignment-only. Also spec table column labelled "assignment" but the two
Unclaimed sites are COMPOSITE LITERALS (processdriver_action.go:439, step_nodes.go:733).

## F10/F12 — absolutes refuted by execution (out-of-range TaskState)
Probe (SQLite, deleted): Upsert{State: TaskState(99), Claim: alice@nonzero} => err=<nil>
 read back: state=unclaimed claim=&{alice ...}  R2 violated on read = true
 AssignedTo(alice)=1 ClaimableBy(alice)=0   (column stores "unknown")
=> "Write-side is the only seam" over-generalises from direction 1 to R2.
=> Validate cannot see an out-of-range state; the record-level invariant is NOT unrepresentable.

## PROCESS NOTE for the controller
Shared primary tree is DIRTY right now from concurrent lenses A/B (patched memory.go +
humantask_store.go, 5 untracked zzprobe files). Must be cleaned before implementation.
