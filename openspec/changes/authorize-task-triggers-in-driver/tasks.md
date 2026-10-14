# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`. Each test goes red before its fix. Waits and deadlines follow `docs/agents/eventually-waits.md` and `docs/agents/test-deadlines.md`. Tables follow the `table-test` skill. All commands use `GOTOOLCHAIN=go1.26.9`. Run tests per module, as CONTRIBUTING.md describes, because a bare `./...` covers only the root module.

Q1 and Q2 in design.md must be ruled before groups 5 and 6 start. Groups 1 to 4 do not depend on either answer.

## 1. Prove the back door (defect first)

- [ ] 1.1 Write `runtime/processdriver_authz_test.go`. It builds a driver with `authz.NewComposite()`, parks an instance on a user task whose eligibility "bob" fails, and then calls `ApplyTrigger` directly with a hand-built `HumanClaimed`, `HumanCompleted` and `HumanCandidatesResolved` naming "bob". Assert `authz.ErrNotAuthorized` and an unchanged instance version and journal. Run `go test ./runtime/ -run TestApplyTrigger_HumanTriggerAuthorization` and watch it go **red** (today `err == nil` and the version advances). Scenario: human-task-authorization "A hand-built trigger passed straight to ApplyTrigger is refused".
- [ ] 1.2 In the same table, add the eligible-"alice" `HumanCompleted` row as the negative control. It is green before and after the fix. Scenario: "Negative control - a hand-built trigger from an eligible actor is applied".

## 2. Gate the transition in the driver

- [ ] 2.1 Move the `humantask.HumanTask` → `authz.TaskView` projection into `runtime` (design D2). Unit-test the claimed and unclaimed rows. Scenario: "Unclaimed task projects an unclaimed view". Verify with `go test ./runtime/...`.
- [ ] 2.2 In `applyTrigger`, after `store.Load` and before `deliverLoop`, switch on the four human trigger types. Look up `st.TaskByID`. If the task is missing, skip authorization. Otherwise build the `authz.Request` (operation, the trigger's `Actor`/`By`, the snapshot task's `Eligibility`, `Vars` and `TaskView`), call the authorizer, and return its error wrapped with `%w`. Make 1.1 green. Using a recording spy authorizer (`processtest.NewSpyAuthorizer`), add rows for these scenarios: "The request the authorizer receives", "Any authorizer error stops the transition and writes nothing" (`ErrNotAuthorized`, `ErrSpecNotEvaluable`, `ErrNoDeciders`, `context.Canceled`; assert no event and no task-store write), "Negative control - an allowing authorizer lets the transition commit", "Non-human triggers are never put to the authorizer", "A trigger naming an unknown task reaches the engine's own refusal", "A closed task is authorized before the engine reports it closed", and "Each delivery is authorized afresh". Verify with `go test -race ./runtime/...`.
- [ ] 2.3 Add a test where the task store's copy and the snapshot disagree on eligibility, both directions. Scenario: "The instance snapshot decides, not the task store's copy". Verify with `go test ./runtime/...`.
- [ ] 2.4 Add a lost-update test. A store wrapper commits a competing version between the driver's `Load` and its `Commit`, and the test asserts `kernel.ErrConcurrentUpdate` and the competitor's state. Scenario: "A decision made on a stale snapshot cannot commit". Verify with `go test -race ./runtime/...`.
- [ ] 2.5 Replace the "Authorization contract ... MUST originate from TaskService" paragraph (`runtime/processdriver.go:545-550`) with one sentence saying the driver authorizes human triggers against the snapshot's task, and add a pointer to `openspec/specs/human-task-authorization/spec.md`. Correct the `az` line in `WithHumanTasks`'s doc (`runtime/processdriver_options.go:91-92`). Verify with `scripts/check-doc-refs.sh` and `go doc ./runtime ProcessDriver.ApplyTrigger`.

## 3. Fail closed on a nil authorizer

- [ ] 3.1 Write the failing table first. It covers a driver built with `WithHumanTasks(r, t, nil)`, with `authz.AllowAll{}`, and with `authz.NewComposite()`, using "alice" and "bob", and all four human trigger kinds against the nil driver. Watch it go red. Scenarios: "Authorizer configuration decides admission" and "Each human trigger kind is refused".
- [ ] 3.2 Add `runtime.ErrNoAuthorizer`. Its message names `authz.AllowAll{}`, and its godoc names the opt-out and the spec. Return it from the D1 gate when `driver.authz == nil`. Assert `errors.Is(err, authz.ErrNotAuthorized) == false`, and that a nil-authorizer driver constructs and applies a `TimerFired`. Scenarios: "The refusal names the opt-out and is not a denial" and "Negative control - a driver with no authorizer still runs non-human work". Verify with `go test -race ./runtime/...`.
- [ ] 3.3 Update every `WithHumanTasks(..., nil)` test site that delivers a human trigger to pass `authz.AllowAll{}` (22 sites measured; re-grep with `grep -rn 'WithHumanTasks([^)]*, nil)' --include='*_test.go' .`). Leave the rest on nil. Verify by running the per-module suite from CONTRIBUTING.md (`for pat in $(scripts/modules.sh); do go test -race "$pat"; done`, Docker up).
- [ ] 3.4 Add a parity test in `transport/http/parity` that mounts each adapter over a nil-authorizer driver and posts a claim. Assert 500, `internal_error`, and no "AllowAll" in the body. Scenario: "The task routes classify the missing authorizer as an internal error". Verify with `go test ./transport/http/...`.

## 4. Requesting actor on candidate refresh

- [ ] 4.1 Write the codec tests first in `internal/persistence/store/trigger_codec_test.go`: a `HumanCandidatesResolved` with `By` round-trips, and a literal pre-change JSON payload with no `by` key decodes to the zero actor with its candidates intact. Watch them go red (the field does not exist). Scenarios: instance-store "Requesting actor survives the journal on each dialect" (codec half) and "A row written before the requesting actor existed still decodes".
- [ ] 4.2 Add `By authz.Actor` to `engine.HumanCandidatesResolved` and a `by` parameter to `engine.NewHumanCandidatesResolved`. Write and read `env.By` for the candidates kind in the codec (design D5). Fix every constructor call site (`go build` across modules). Make 4.1 green.
- [ ] 4.3 Add a dialect test for `Entries` on Postgres, MySQL and SQLite (testcontainers via `database.RunTestDatabase`) for the round trip, the legacy row inserted as raw JSON, and the neighbouring-kind negative control. Scenarios: all three instance-store scenarios. Verify with `go test -race ./internal/persistence/...` (Docker).
- [ ] 4.4 Have `TaskService.RefreshCandidates` put `by` on the trigger. Add driver rows for the manager, viewer, zero-actor and empty-spec cases. Scenarios: "The requesting actor rides on the trigger" and "The requester decides the refresh". Verify with `go test ./runtime/...`.

## 5. TaskService becomes trigger production

- [ ] 5.1 Write the failing test first. `TaskService.Claim`, `Complete` and `RefreshCandidates` with an ineligible actor return a trigger and no error, and applying that trigger fails with `authz.ErrNotAuthorized`. Watch it go red (today the service refuses). Scenario: "The task service produces the trigger and the refusal comes from applying it". Then remove the `authorize` call from all four methods. Keep `store.Get`, the empty-target and `from == claimant` checks, `ErrNoActorResolver` and the open-task check (design D6). Update `runtime/task/authz_request_test.go`, which pins the request the service used to build. Its assertions move to the 2.2 driver test. Verify with `go test ./runtime/task/...`.
- [ ] 5.2 (Shape depends on Q1.) Under Q1(a), remove the authorizer parameter from `task.NewTaskService` and `service.WithHumanTasks`, drop the facade's `AllowAll` default and its DEBUG label, and fix the callers (`processtest/harness.go`, `internal/transporttest/harness.go`, the examples). Under another ruling, apply that ruling instead. Verify by building every module and running the `service` and `processtest` tests.
- [ ] 5.3 Rewrite the godoc of `TaskService`, its four methods, `service.ProcessEngine.ClaimTask/CompleteTask/ReassignTask/RefreshTaskCandidates` and `service.WithHumanTasks` to say they produce triggers and that the driver authorizes. State the resolver-before-authorization trade-off on `RefreshCandidates` (design Risks). Verify with `scripts/check-doc-refs.sh`.

## 6. Counter in the driver

- [ ] 6.1 Write the table test first, against a manual-reader meter provider. Admitted and committed rows for each event count 1; denied, nil-authorizer and produced-but-not-applied rows count 0. Watch it go red (the "produced but not applied" row counts 1 today). Scenario: "Counting by outcome". Under Q2(a), add rows asserting that an authorized trigger refused with `engine.ErrTaskNotOpen` counts 0.
- [ ] 6.2 Remove the `humanTasks` counter from `TaskService` and increment `wrkflw_human_tasks_total{event}` in the driver once the human trigger's step has committed (design D7). Make 6.1 green with `go test -race ./runtime/...`.

## 7. Transport parity and the trust-boundary docs

- [ ] 7.1 Extend `transport/http/parity` with the 403 rows (claim and complete, ineligible "bob") and the 200 control (eligible "alice") on stdlib, gin and fiber, over a driver built with `authz.NewComposite()`. Re-run the existing 401 and forged-actor parity scenarios unchanged. Scenario: "The task routes answer the driver's refusal identically on every adapter". Verify with `go test ./transport/http/...`.
- [ ] 7.2 In `doc.go`, change the location sentence in `# Trust boundary` ("consulted by ... TaskService in Claim, Complete, Reassign and RefreshCandidates") and the `# Authorization` `authz` bullet to name the driver, at the transition, for the four human triggers, including direct `ApplyTrigger`. State the nil-authorizer refusal and the `AllowAll` opt-out once. Every sentence must check against a file:line (#43's rule). Verify with `go test ./transport/http/parity -run SecurityNote` and `scripts/check-doc-refs.sh`.

## 8. Integration

- [ ] 8.1 Run the full per-module race suite with Docker up, `scripts/check-openspec.sh`, `openspec validate authorize-task-triggers-in-driver --strict`, and per-module golangci-lint from inside each module. All of them must pass.

## Workflow follow-up

- The PR body names `authorize-task-triggers-in-driver`. Its last commit runs `openspec archive authorize-task-triggers-in-driver`.
- After merge, comment on kartaladev/wrkflw#106 with the change name and close it.
- Hand `cut-v0-release` the BREAKING lines: nil authorizer refused, `NewHumanCandidatesResolved` signature, `TaskService` no longer authorizes, and the Q1 outcome.
- Tell `define-access-and-disclosure` to re-read its Q1 against design D4.
