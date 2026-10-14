# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`, and every test goes red before the code that makes it pass. Tables follow the `table-test` skill. Waits and deadlines follow `docs/agents/eventually-waits.md` and `docs/agents/test-deadlines.md`. All commands use `GOTOOLCHAIN=go1.26.9`. Run tests per module as CONTRIBUTING.md describes, because a bare `./...` covers only the root module.

Design Q1 must be ruled before task 3.3. Every other task is independent of the answer.

## 1. The envelope type

- [ ] 1.1 Write `humantask/call_test.go` first: a table over the kind names `completion`, `deadline`, `reminder` and the refused values `"Completion"`, `" completion"`, `"complétion"`, `""` and `0`. Watch it go red (the type does not exist). Scenario: "Kind round trip and refusal". Verify with `go test ./humantask/ -run TestCallKind`.
- [ ] 1.2 Add `humantask.Call[In]`, `CallKind`, its text marshalling and `ErrUnknownCallKind` (design D3), with the JSON names from the proposal and `omitempty` on `update` and `previous`. Make 1.1 green, and add a round-trip row proving `applied` encodes as `{}` and not `null`. Verify with `go test ./humantask/...`.

## 2. Completion action of a user task

- [ ] 2.1 Write the failing engine test: a user task with `completion_action`, completed by "alice" with output `{"approved": true}` over variables `{"order": 7}`. Assert every envelope field on the emitted `InvokeAction.Input`. Add the `{}`/absent-output rows. Watch it go red. Scenarios: "The completion action sees the task, the actor and the output" and "Completion input for empty and absent output". Verify with `go test ./engine/ -run TestCompletionAction_Envelope`.
- [ ] 2.2 Add the receive-task row to the same table as a negative control. It must stay green throughout. Scenario: "Receive task completion is instance data, not task context".
- [ ] 2.3 Add `taskCall` (design D4) and pass the input into `parkOnCompletionAction` from `handleHumanCompleted` (envelope) and `handleMessageReceived` (flat map) (design D1). Make 2.1 green with 2.2 still green. Verify with `go test -race ./engine/...`.

## 3. Deadline and reminder actions of a user task

- [ ] 3.1 Write the failing table for the deadline and reminder of a user task, plus the negative-control rows: a receive-task deadline, a catch-event reminder, a timer boundary's `boundary_action` and an error boundary's `boundary_action`. Watch the user-task rows go red and the control rows stay green. Scenarios: "The engine, not an actor, fires the action" and "Instance actions are not wrapped". Verify with `go test ./engine/ -run TestTimerTaskAction_Envelope`.
- [ ] 3.2 Give `emitFireOnceAction` an input parameter and build the envelope at the two user-task timer sites only (design D1). Make 3.1 green. Verify with `go test -race ./engine/...`.
- [ ] 3.3 After Q1 is ruled, write the key-format table (completion, deadline, 1st and 2nd reminder), the three-firings distinctness test, and the same-command-twice byte-equality test. Watch them go red, then implement the ruled key (for option (a), a firing counter on the reminder timer record that `cloneState` copies). Scenarios: "Key format per kind", "Repeated reminders are distinguishable" and "The same command re-sent carries the same envelope". Verify with `go test -race ./engine/...`.

## 4. Instance actions and failure handling stay put

- [ ] 4.1 Add a table pinning the service-task (with `_idempotencyKey`), cancel and compensation inputs as flat maps with no `kind` or `task_id` key. It is green before and after. Scenario: "Instance actions see instance data". Verify with `go test ./engine/...`.
- [ ] 4.2 Add a driver-level test per kind with an always-failing action: completion follows the retry policy and then raises an incident; deadline still moves the token to the deadline flow; reminder leaves the token on the task. No incident is raised for the timer kinds. Scenario: "The same failure chain as before the envelope". Verify with `go test -race ./runtime/...`.

## 5. Strict typed decoding, examples and docs

- [ ] 5.1 Write a `runtime` test that registers `action.Typed` over `humantask.Call[map[string]any]` with `WithStrictInput()` as each of the three actions and drives each event, asserting no `ErrDecodeInput`. Add the flat-struct strict action row, which must fail with `ErrDecodeInput` naming `kind` and must not be retried. Scenarios: "The envelope's key set is closed" and "An action still written against the flat map is refused, not silently fed zero values". Verify with `go test -race ./runtime/...`.
- [ ] 5.2 Update every example and test helper that registers a user-task completion, deadline or reminder action (`grep -rn 'completion_action\|deadline_action\|wait_action\|CompletionAction\|DeadlineAction\|WaitAction' examples processtest`). Verify by running the per-module test loop from CONTRIBUTING.md.
- [ ] 5.3 Write the `humantask` package doc section: the two input conventions and the reason ("task actions receive task context; instance actions receive instance data"), the strict typed example, and a citation of `openspec/specs/human-task-actions/spec.md`. Verify with `scripts/check-doc-refs.sh` and `go doc ./humantask Call`.

## Workflow follow-up

- `cut-v0-release` records the release-note line: user-task completion, deadline and reminder actions receive `humantask.Call`.
- The PR's last commit runs `openspec archive add-user-task-updates-call-envelope`.
