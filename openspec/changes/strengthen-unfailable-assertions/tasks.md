# Tasks

`skip_specs: true`. No behavior changes, so no task makes a new scenario pass. Each task names the existing baseline scenario whose evidence it strengthens, where one exists. Site lines and the D1 to D5 decisions are in design.md.

Each strengthening task follows one shape:
1. Apply the named mutation to the production code. Watch the current assertion stay green, which reproduces the vacuity.
2. Write the strengthened assertion. Watch it go red under the mutation.
3. Restore the production code. Watch it pass.

Record the mutation and the red test name in the PR description. Production code is unchanged at the end of every task. `git diff --stat -- ':!*_test.go'` must be empty. Run every command from the repository root with `GOTOOLCHAIN=go1.26.9`.

## 1. Probe shims and the zero-result pin (D1, D4)

- [ ] 1.1 Red: substitute `return res, idErr` for `return StepResult{}, idErr` in `engine.Step`. Watch `TestStepIDGen` stay green. Add `assert.Equal(t, engine.StepResult{}, res)` to its "generator failure surfaces as a step error" row, and watch the row go red under the mutation. Restore. Scenario: none (an internal contract). Verify: `go test ./engine -run 'TestStepIDGen' -count=1` passes.
- [ ] 1.2 Add thin delegating shims to `engine/export_test.go`, with no logic. One runs `dispatch` on a `cloneState` copy and returns the post-call copy and error. One runs `fireBoundaryArm` on a copy for a named boundary arm and returns the post-call copy, commands and error. Add a shared engine_test helper `assertRefusedUntouched(t, before, after)` that compares `after` with `before.Clone()`. Verify: `go vet ./engine` and `go test ./engine -count=1` pass.

## 2. kartaladev/wrkflw#227: `refusedLoudly` (D1, D3)

- [ ] 2.1 Red: in `fireBoundaryArm`, move the unresolved-target guard below `cancelTokenWaits` in the interrupting branch (the A3 ablation). Watch `TestBoundaryFireFailsLoudlyWhenItsTargetNamesNothing` stay green on both refused rows. Replace the returned-token, returned-command and `armed` assertions with the `fireBoundaryArm` probe and `assertRefusedUntouched`, asserting that the host token `work` is present in the post-call state. Watch both refused rows go red under the mutation. Restore. Scenario: token-routing-and-joins, *Unresolvable and blank-but-declared boundary targets*. Verify: `go test ./engine -run 'TestBoundaryFireFailsLoudlyWhenItsTargetNamesNothing' -count=1` passes, and the blank-ID control row still passes.
- [ ] 2.2 Add a probe row whose `MessageReceived` carries a payload. Show that the `fireBoundaryArm`-level probe stays untouched. Add a comment naming `onMatch` as the reason a `dispatch`-level probe would be red here. Verify: the same command passes.

## 3. Dispatch's terminal guard (D1)

- [ ] 3.1 Red: in `dispatch`, move the `sp.Status.IsTerminal()` refusal below the handler call for `rejectWithError` triggers. Watch these tests stay green: `TestStartInstanceRejectedOnTerminalInstance`, `assertAssignmentRejected` (claim and reassign), `TestHumanCompletedRejectedOnTerminalInstance`, `TestResolveIncidentRejectedOnTerminalInstance`, `TestOnlyAPlainFullRollbackIsAdmittedOnAHarvestedTerminalInstance`, `TestTerminalDispatchOutcomes`, `TestTerminalResumeGuard`, `TestResolveIncidentOnFailedInstanceWithSurvivingSiblingIsRejected`, and the "nothing left to compensate" row of `TestCompensateRequestedOnTerminalInstance`. Replace each "dispatch nothing" assertion with the `dispatch` probe and `assertRefusedUntouched`, and watch each go red. Restore. Scenarios: human-task-lifecycle, *Terminal instance*; operator-incidents, *A terminal instance answers with the terminal refusal*. Verify: `go test ./engine -run 'Terminal|Harvested|RejectedOn' -count=1` passes.

## 4. Handler refusals (D1)

- [ ] 4.1 Red: move each guard below the first mutation in its handler:
  - the `ErrIncidentNotResolvable` guard below the `s.Incidents` removal;
  - the `ErrInstanceAlreadyStarted` guard below token placement;
  - the `ErrCancelNotApplicable` guard below its first state write;
  - the compensation trigger-shape checks, the stall-verb cursor checks and the human-outcome checks below the first state write.

  Watch these tests stay green: `TestCompensationFailedIncidentIsNotResolvable`, `TestBaselineResolveIncidentRefusesADefinitionDefect`, `TestResolveIncidentRefusesACompensationStall`, `TestSecondStartInstanceIsRefused`, `TestCancelOnAnInFlightWalkIsTruthful`, the shape rows of `TestCompensateRequestedOnTerminalInstance`, `TestEscapeVerbsRefuseAMismatchedCursor` and `TestHumanCompletedOutcomeValidation`. Replace each "dispatch nothing" assertion with the `dispatch` probe, and watch each go red. Restore. Scenarios: operator-incidents, *Non-action incidents are refused and kept*; instance-lifecycle, *Starting an instance twice*. Verify: `go test ./engine -run 'Incident|SecondStart|InFlightWalk|CompensateRequested|EscapeVerbs|OutcomeValidation' -count=1` passes.
- [ ] 4.2 Red (shape B): with the `ErrIncidentNotResolvable` guard moved below the removal, watch the three replay assertions stay green. Retarget each replay, and the "still there to refuse a second time" read, at the post-call state from the probe instead of the caller's input. Watch them go red. Restore. Scenario: operator-incidents, *Non-action incidents are refused and kept*. Verify: the 4.1 command passes.
- [ ] 4.3 If any probe in 3.1 or 4.1 is red at HEAD before mutation, stop. Record the site and resolve the Open Question in design.md with the owner before going on. Verify: every probe was green at HEAD before its mutation, as recorded in the PR description.

## 5. Labelled controls (D2)

- [ ] 5.1 Rewrite each remaining `Step`-level zero-result line as a "CONTROL: … cannot fail here, pinned by TestStepIDGen" comment plus assertion, in the form `step_task_lifetime_guard_test.go` uses. These are `step_harvest_terminal_admission_test.go:81`, `step_terminal_dispatch_test.go:286`, `step_human_outcome_test.go:62` and the three `TestStepRejectsAReassignmentWithNoTarget` lines. That last guard runs before `cloneState`. Verify: `go test ./engine -count=1` passes, and `grep -rn 'must dispatch nothing\|must emit no commands\|still does nothing' engine/*_test.go` returns no line that is not labelled CONTROL.

## 6. Non-engine sites (D5)

- [ ] 6.1 Red: make gin's `writeErr` copy `err.Error()` into `body.Error` for status ≥ 500. Watch `TestInternalError_NoRawErrorLeak` stay green. Add a raw-body check that the body does not contain the `errSvc` error text, and watch it go red. Restore. Scenario: http-transport, *An unclassified failure never reaches the client* (gin row). Verify: `go test ./transport/http/gin -run TestInternalError_NoRawErrorLeak -count=1` passes.
- [ ] 6.2 Red: in `authz/decider.go`, wrap the attribute-predicate error in `ErrNotAuthorized`. Watch the "unevaluable authz predicate" row of `TestClassifyError_ChainRenderingPerArm` stay green. Build the row's error from `authz.RoleAuthorizer{}.Authorize` with an unevaluable predicate, and watch it go red (403, and the message carries the predicate source). Restore. Scenario: human-task-authorization, *The HTTP response hides the predicate*. Verify: `go test ./transport/http/httpcore ./authz -count=1` passes.
- [ ] 6.3 Red: in `fromNodeYAML`, set an empty `Outcomes` to nil before the gate. Watch `TestZeroValuedMisplacedKeyIsAsymmetric` stay green. Drive all six rows through `model.ParseYAML(...).Build()` and `json.Unmarshal`, asserting identical outcomes on both. Watch the YAML "empty slice" row go red. Restore. Scenario: definition-wire-format, *A zero value under a misplaced key is asymmetric*. Verify: `go test ./definition/model -run TestZeroValuedMisplacedKeyIsAsymmetric -count=1` passes.

## 7. Integration

- [ ] 7.1 Run the whole suite per module, as CONTRIBUTING.md describes, plus the repo guards. Verify: `go test ./... -count=1` passes in the root module and in `examples/`; `bash scripts/check-doc-refs.sh`, `bash scripts/check-openspec.sh` and `openspec validate strengthen-unfailable-assertions --strict` pass; `git diff --stat main -- ':!*_test.go' ':!openspec/**'` is empty.

## Workflow follow-up

- Open the PR naming `strengthen-unfailable-assertions`, with the mutation table from tasks 1-6 in its body. Its last commit runs `openspec archive strengthen-unfailable-assertions`.
- After merge, comment on kartaladev/wrkflw#227 with the change name and close it.
