# Tasks

Every Go task starts with the `cc-skills-golang:golang-how-to` skill, then runs red, green, refactor. Waits and deadlines follow `docs/agents/eventually-waits.md` and `docs/agents/test-deadlines.md`. Every command sets `GOTOOLCHAIN=go1.26.9`. Store rows (Postgres, MySQL, SQLite) and parity rows need Docker. A whole-workspace run uses the `scripts/modules.sh` loop in CONTRIBUTING.md, because a bare `./...` covers only the root module.

## 1. Gate

- [ ] 1.1 Record the maintainer's answers to Q1–Q4 (design, Open Questions) in design.md. Rewrite any pending requirement whose answer differs from the recommendation with `/opsx:update` before starting the group that depends on it. Verify with `openspec validate replace-casbin-with-composed-deciders --strict` and `scripts/check-openspec.sh`. Before starting group 2, confirm `remove-casbin` and `authorize-task-triggers-in-driver` are archived (`openspec list --json`).

## 2. The actor model (kartaladev/wrkflw#119 amendment on #115; D1, D6)

- [ ] 2.1 Write the failing `authz` tests for `Actor.Validate()` covering every row of *An actor that fails validation is refused before authorization*, plus `Principal()` and `PrincipalID()` on the with- and without-principal cases. Verify with `go test -run 'TestActor' ./authz/...`, red before 2.2.
- [ ] 2.2 Make `authz.Actor` `{ID, Role, Privileges, Attributes, OnBehalf *Actor}` and add `Principal`, `PrincipalID`, `Validate` and `ErrInvalidActor`. Extend `Clone` to deep-copy `OnBehalf`. Remove `Roles`. Make 2.1 green. Verify with `go test -race ./authz/...`.
- [ ] 2.3 Write the failing decider tests for *Role eligibility tests the principal's single active role*, *The deciders evaluate the principal, not the caller* and *Attribute predicates bind the principal as actor and the caller as caller*. Each decider gets direct table tests on the exact `Decision` constant (allow, deny, `NotApplicable`), as #115 ruling 6 requires, besides `Composite` rows. Then make `RoleDecider`, `PrivilegeDecider` and `AttributeDecider` read `Principal()` and bind `actor` and `caller`. Verify with `go test -race ./authz/...`.
- [ ] 2.4 Write the failing tests for *Recorded actors keep the caller whole and carry no privileges*: the engine snapshot and journal round trip, plus a raw-row assertion per dialect on `claim_actor` and `completion_actor`. Then add the recorded-copy helper (D6). Apply it in the engine's claim, completion and reassign handlers, rewrite the SQL actor remainder codec (`internal/persistence/store/humantask_store.go`) for `role` and `on_behalf`, and update the memory store. Verify with `go test -race ./engine/... ./humantask/... ./internal/persistence/store/...` (Docker).
- [ ] 2.5 Write the failing tests for *The claimable list matches the principal* on memory, Postgres, MySQL and SQLite, through `processtest/taskstoreconformance.go`. Then switch `ClaimableBy`, `AssignedTo` callers and the `claimed_by` and `completed_by` writes to `PrincipalID()` and `Principal().Role`. Verify with `go test -race ./humantask/... ./internal/persistence/store/... ./processtest/...` (Docker).
- [ ] 2.6 Write the failing tests for *Resolved candidates are principals* (Q3), then strip privileges and apply the Q3 answer in candidate resolution (`runtime/processdriver_action.go` `resolveHumanCandidates` and `TaskService.RefreshCandidates`). Verify with `go test -race ./runtime/...`.
- [ ] 2.7 Write the failing tests for *An actor recorded before the single-role model is refused on read* (Q4) for the snapshot, journal and task-row decoders, including the negative-control row. Then implement the Q4 answer. If it is refused, record the missing sentinel as a finding in design.md. Verify with `go test -race ./engine/... ./internal/persistence/store/...` (Docker).
- [ ] 2.8 Add the `PrincipalID()` guard test (D6). It is grep-shaped over `authz`, `engine`, `runtime/task`, `humantask` and `internal/persistence/store`, and fails on any direct actor `.ID` comparison. Switch `TaskService.Reassign`'s `from == claimant` check to it. Watch the guard go red on a planted comparison before removing the plant. Verify with `go test -run TestPrincipalIdentity ./...`.
- [ ] 2.9 Write the failing tests for *The on-behalf principal and the ownership rule follow their categories*, then classify `OnBehalf` under actors and the ownership fields under policy in `runtime/view`. Keep `TestClassification_IsTotal` green. Verify with `go test -race ./runtime/view/...`.
- [ ] 2.10 Migrate every remaining `Roles:` construction to `Role:`: `internal/transporttest/harness.go`, `processtest`, `transport/http/httpcore` (`resolve_actor.go`, `seam.go`, `disclosure.go` comments) and the 15 example files. Verify with the `scripts/modules.sh` build-and-test loop.

## 3. Actor validation at the driver seam (D2, D7; Q1)

- [ ] 3.1 Write the failing tests:
  - *Validation outranks the eligibility refusal*;
  - *The driver refuses a hand-built trigger from an invalid actor*, every row, asserting the snapshot version and the journal length are unchanged;
  - every row of *An actor that fails validation is refused before authorization*, through `TaskService` plus `ApplyTrigger`.

  Verify with `go test -run 'TestValidateActor' ./runtime/...`, red before 3.2.
- [ ] 3.2 Call `Validate()` in `ProcessDriver.applyTrigger` immediately before the authorization call that `authorize-task-triggers-in-driver` added, on `HumanClaimed.Actor`, `HumanCompleted.Actor`, `HumanReassigned.By` and `HumanCandidatesResolved.By`. Make 3.1 green. Verify with `go test -race ./runtime/...`.
- [ ] 3.3 Write the failing tests for *An invalid actor classifies as a bad request*, and for the MODIFIED *Task routes fail closed when no identity can be established* rows (kiosk and self-principal), with the adapter parity scenario. Then add the classifier arm. Verify with `go test -race ./transport/http/...`.
- [ ] 3.4 Rewrite the kiosk prose to the Q1 answer: `humantask/validate.go:24`, `httpcore/resolve_actor.go:47,213`, `seam.go:145`, `disclosure.go:21-29` and `doc.go:151-156`. Add the #119 rule sentence to the `authz` package doc and `doc.go`, citing `openspec/specs/human-task-authorization`. Verify with `scripts/check-doc-refs.sh` and `go doc ./authz Actor`.

## 4. Claimant-only tasks (kartaladev/wrkflw#115 115c; D3, D4, D5; Q2)

- [ ] 4.1 Write the failing decode tests for *A user task carries claimant_only and reassign_eligibility on the wire*, every outline in JSON and YAML. Then add the keys to `definition/activity.UserTask` and its options, `definition/model/node_wire.go` and `node_wire_keys.go`, mapping them onto `AuthzSpec.ClaimantOnly` and `ReassignEligibility`. Verify with `go test -race ./definition/...`.
- [ ] 4.2 Write the failing tests for *Reassign eligibility is validated like eligibility and only beside claimant_only* (Q2), then add `model.ErrInvalidReassignEligibility` and the rules in `model.Validate`. Verify with `go test -race ./definition/model/...`.
- [ ] 4.3 Write the failing tests for *Registering a claimant-only task without reassign eligibility warns*, capturing `slog.Default`, then add the WARN beside `warnForceTermination` in `runtime/definition_registry.go`. Verify with `go test -run TestRegisterDefinition ./runtime/...`.
- [ ] 4.4 Write direct decider tests for `OwnershipDecider` and `ReassignDecider` on the exact `Decision` constant: refuse, accept, and `NotApplicable` on claim and refresh, and with the rule off. Then implement both. Add the `claimant_only` and `reassign_eligibility` `SpecField`s and coverage, and put the deciders in `NewComposite`. Verify with `go test -race ./authz/...`.
- [ ] 4.5 Write the failing service-level tests for *Only the claimant may complete a claimant-only task* and *Reassignment of a claimant-only task needs the claimant or reassign eligibility*, every Examples row, through `TaskService` and `ApplyTrigger`. Make them green: the ownership fields reach the driver's request through the snapshot's eligibility (D3). Verify with `go test -race ./runtime/task/... ./runtime/...`.
- [ ] 4.6 Write the failing engine tests for *Completing an unclaimed claimant-only task claims it in the same step*: memory and three dialects, the replay scenario and the counter scenario. Then record the implicit claim in the `HumanCompleted` handler (`engine/step_triggers.go`) and count it in the `claimed` metric. Verify with `go test -race ./engine/... ./runtime/... ./internal/persistence/store/...` (Docker).
- [ ] 4.7 Write the race test for *Racing completions of one unclaimed task admit exactly one*, on memory and three dialects, with waits per `docs/agents/eventually-waits.md`. Record in design D5 which error the losing call surfaces. Verify with `go test -race -count=20 -run TestImplicitClaimRace ./runtime/...` (Docker).
- [ ] 4.8 Document `claimant_only`, implicit claim and `reassign_eligibility` in the `humantask` and `definition/activity` godoc, citing `openspec/specs/human-task-authorization`. Verify with `scripts/check-doc-refs.sh`.

## 5. Integration

- [ ] 5.1 Run the full workspace suite with the `scripts/modules.sh` loop (Docker), `scripts/lint.sh`, `scripts/check-module-drift.sh`, `scripts/check-doc-refs.sh` and `scripts/check-test-timeout.sh`. Verify that all exit 0.
- [ ] 5.2 Add the release-note lines from design's Migration Plan to the `cut-v0-release` change's notes input, or record them in the PR body for it. Verify the PR body names `replace-casbin-with-composed-deciders`.

## Workflow follow-up

- The PR's last commit runs `openspec archive replace-casbin-with-composed-deciders`.
- After merge, comment on and close kartaladev/wrkflw#115, naming this change and `remove-casbin` (which closed #179).
