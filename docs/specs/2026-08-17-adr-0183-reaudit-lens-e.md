# Re-audit Lens E (counting / cross-doc)

## CONFIRMED-CORRECT (re-derived, no defect) — recorded so nobody redoes it
- 8 non-test `UpdateTask{` emit sites, ALL EIGHT line numbers exact (grep output pasted below).
  step_timers.go:93, step_triggers.go:581/612/637/941, state.go:656, step_cancel.go:40,
  step_stale_commands.go:171.
- `:612` (handleHumanCandidatesResolved) IS the only one touching neither State nor Claim:
  the other seven set `.State` at 89 / 578 / 634 / 928 / 649 / 39 / 170 respectively.
- 5th State writer humantask_store.go:363 `t.State = htParseTaskState(stateStr)`; default→Unclaimed
  at 648-658. scanTask rebuilds Claim from claimed_at independent of state.
- TaskState is `int` (signed) and the four constants are Unclaimed(0)..Cancelled(3) → the plan's
  `TaskState(-1)` row compiles and the `< Unclaimed || > Cancelled` arm is correct.
- 3 non-test `.Upsert(` sites EXACT: processdriver_action.go:468, :483, caching_task_store.go:99.
- 3 TaskStore impls EXACT: memory.go:33, humantask_store.go:131, caching_task_store.go:98.
- exactly ONE mutating SQL statement in humantask_store.go, at :155 (INSERT), inside Upsert.
- humantask_store.go:129-130 IS the inherited false ADR-0148 sentence. 131 IS the Upsert func line.

## F1 (MAJOR/CRITICAL, CONFIRMED) "Zero fixture churn, measured" is FALSE for the REVISED guard
humantask/memory_test.go:121-122 seeds `State: Claimed, Claim: &Claim{Actor{ID:""}}` through
`require.NoError(t, store.Upsert(ctx, tsk))` (seed loop ~line 218). R1's NEW empty-claimant clause
rejects exactly that shape -> subtest
"an empty actor ID does not match a claim recording an empty actor ID" goes RED.
The zero-churn measurement was taken against the TWO-rule guard (pre-B2/pre-C1); the spec/ADR
restate it for the THREE-rule guard + empty-claimant clause without re-measuring.
Also: that fixture is the ONLY coverage of the AssignedTo("") disclosure footgun with a
claim-recording-nobody row; making it unrepresentable through Upsert DELETES a security
regression test unless the bundle says how to keep it.
### F1 EVIDENCE (own worktree at b3cc2593, plan's prescribed Validate + MemTaskStore guard)
go test -count=1 ./humantask/...  -> EXIT=1
--- FAIL: TestMemTaskStore_AssignedTo/an_empty_actor_ID_does_not_match_a_claim_recording_an_empty_actor_ID
    memory_test.go:221  Received unexpected error   (require.NoError on the seed Upsert)
### Item 8+9 CONFIRMED CORRECT
go test -count=1 -v -run '^TestValidate$' ./humantask/... -> EXIT=0 PASS=10 noTests=0.
All 9 prescribed rows pass the prescribed switch; TaskState is `int` so TaskState(-1) compiles;
`grep -cE '^\s*--- PASS'` works on both ugrep and /usr/bin/grep (BSD) on this box.

## F2 (CRITICAL, CONFIRMED by construction) R1's empty-claimant clause CONTRADICTS ADR-0148 amd 1 §4
internal/persistence/store/humantask_store_conformance_test.go:252-272, subtest
"claim with an empty actor id still reads back as a claim", seeds
State: Claimed, Claim: &Claim{Actor: authz.Actor{Roles: ["kiosk"]}, At: claimedAt}  (empty Actor.ID)
with the comment: "an empty claimant id is a LEGITIMATE value, and keying on it would resurrect
the fabricated/dropped-claim bug of amendment 1 §4."
=> second zero-churn violation, in a second package; and a 4th BREAKING change (the kiosk shape)
that no document acknowledges. ADR-0183 never mentions ADR-0148 amendment 1 §4.

## F3 (MAJOR, MEASURED) plan Task 4 Step 2's Docker warning is false, and its reason is false
Claim: "This command REQUIRES Docker ... ~15 top-level Postgres/MySQL tests in this package call
dbtest directly and are not filtered out by a subtest-level -run".
Measured (Docker up, but nothing booted): 2.8s wall, only sqlite RUN lines:
  go test -count=1 -v -run 'TestHumanTaskStoreConformance/sqlite/upsert_get_round_trip' ./internal/persistence/store/ -> EXIT=0
  === RUN TestHumanTaskStoreConformance / .../sqlite / .../sqlite/upsert_get_round_trip  (no postgres, no mysql)
Element 0 of a -run pattern DOES filter top-level names; forEachDialect puts each backend behind
t.Run(b.name) so /sqlite filters the container legs too. The TRUE inherited claim was about
-run '.*/sqlite' (element 0 = .* matches every top-level test). Restated for a command where it
is false. Also the count: 20 (not ~15) top-level tests call dbtest PG/MySQL directly.

## F4 (MINOR, MEASURED) "R3 must be the FIRST arm" is a false load-bearing claim
Moved R3 to the LAST arm in my worktree: go test -run '^TestValidate$' -> EXIT=0 PASS=10 0 FAIL.
An out-of-range State cannot satisfy any later arm (they test == Claimed / == Unclaimed), and the
Claimed+nil arm still precedes the Claim deref. Behaviour is identical; no test pins the order.
