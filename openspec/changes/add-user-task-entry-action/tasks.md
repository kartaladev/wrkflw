# Tasks

Every Go task starts with the `cc-skills-golang:golang-how-to` skill and then runs red, green, refactor. Waits and deadlines follow `docs/agents/eventually-waits.md` and `docs/agents/test-deadlines.md`. Every command sets `GOTOOLCHAIN=go1.26.9`. Store and adapter parity rows need Docker. A whole-workspace run uses the `scripts/modules.sh` loop in CONTRIBUTING.md, because a bare `./...` covers only the root module.

## 0. Gate

- [ ] 0.1 Confirm that `cut-v0-release`, `add-user-task-updates-call-envelope`, `add-user-task-updates` and `replace-casbin-with-composed-deciders` are archived (`openspec list --json`). This change is post-release by maintainer ruling. Record the maintainer's answers to Q1–Q3 (design, Open Questions) in design.md, and rewrite any PENDING requirement whose answer differs from its recommendation with `/opsx:update`. Verify with `openspec validate add-user-task-entry-action --strict` and `scripts/check-openspec.sh`.

## 1. Re-invoking the action that failed (retry-policy)

- [ ] 1.1 Write the failing engine test for *The re-invocation names the failed action*, completion-action rows only: a userTask with a completion action and a retry policy fails retryably, the retry timer fires, and the test asserts the re-emitted `InvokeAction` names the completion action. Watch it go red; this confirms or refutes the proposal's hypothesis. If it is green on the base, keep it as a regression pin and correct the proposal table. Verify with `GOTOOLCHAIN=go1.26.9 go test -run TestTaskActionReinvoke ./engine/...`.
- [ ] 1.2 Add the awaited-action marker to the token (design D1), set it where `parkOnCompletionAction` parks, clear it in `clearAwait`, copy it in the state clone, and encode it by name in the snapshot. Make `reinvokeServiceAction` re-emit the marked action under the original idempotency key. Make 1.1 green, along with *A service task's retry still re-invokes its own action* and *A duplicate retry fire dispatches nothing*. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./engine/...`.

## 2. Definition: fields, wire form and Build rules

- [ ] 2.1 Write the failing decode tests for *Both forms decode to a name and a policy*, *Out-of-vocabulary policies are refused*, *The two words of the vocabulary are accepted*, *Malformed references are refused*, *Absent references stay absent* and *entry_action on any other kind is refused*, for JSON and YAML. Verify with `GOTOOLCHAIN=go1.26.9 go test -run 'TestTaskActionRef|TestOnFailure' ./definition/...`, red before 2.2.
- [ ] 2.2 Add `EntryAction`, `EntryFailure` and `CompletionFailure` (`OnFailurePolicy`, name-encoded), plus `WithEntryAction` and `WithOnFailure`, to `definition/activity`. Add the string-or-object decoder (design D3) with `ErrInvalidOnFailure` and `ErrInvalidActionRef`, and register `entry_action` as a userTask-only key. Make 2.1 green. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./definition/...`.
- [ ] 2.3 Write the failing encode tests for *The encoder writes the shortest form that round-trips* and *A definition stored before this change re-encodes unchanged*. Implement the encoder, then regenerate the golden file once to add the object-form fixtures, and confirm that the pre-existing fixtures are byte-identical. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./definition/...`.
- [ ] 2.4 Write the failing Build tests for *The immediate manual task cannot carry an entry action*, *The refusal reaches a nested subprocess*, *ignore needs an action to ignore*, and *Only userTask completion actions take ignore* (the Q2 answer). Then add `ErrEntryActionOnImmediate`, `ErrOnFailureWithoutAction` and, per Q2, `ErrOnFailureUnsupportedKind` to `model.Validate`. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./definition/...`.

## 3. Engine: the entry action

- [ ] 3.1 Write the failing engine tests for *The output reaches the variables snapshot, the predicate and the candidates* (engine rows), *A userTask without an entry action is created on entry, as today*, *The entry action receives the entry call envelope* and *A loop runs the entry action once per visit with distinct keys*. Verify with `GOTOOLCHAIN=go1.26.9 go test -run TestEntryAction ./engine/...`, red before 3.2.
- [ ] 3.2 Split `userTaskStrategy.enter` (design D2): mint the pending id, emit the entry `InvokeAction` through the envelope builder of `add-user-task-updates-call-envelope` with kind `entry`, and park with the marker set to `entry`. On re-entry, take the pending id and clear it. In `handleActionCompleted`, an `entry` marker merges the output and re-enters the strategy. Add `entry` to the `humantask.CallKind` vocabulary, name-encoded. Make 3.1 green. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./engine/... ./humantask/...`.
- [ ] 3.3 Write and pass the tests for *Human triggers on the pending id are refused*, *A duplicate entry completion creates no second task*, *Paths that remove the token before the task exists* (cancel, terminate, interrupting event sub-process) and *The same entry completion on a token still parked creates the task*. Clear the pending id and the marker on every token-removal path the tests reach. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./engine/...`.
- [ ] 3.4 Write and pass the tests for *A slow entry action delays the deadline instead of consuming it*, *Without an entry action the deadline starts on entry, as today*, *Only the completion action records compensation* (Q1) and *Boundary arms before and after the entry action* (Q3). Guard the compensation record on the marker (design D4). Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./engine/...`.

## 4. Engine: on_failure on entry and completion

- [ ] 4.1 Write the failing tests for *Terminal failure under each policy*, *Ignore offers the task on the unchanged variables and journals the failure once*, *Retries come first under both policies*, *Terminal completion-action failure under each policy* and *A succeeding completion action is unaffected by the policy*. Verify with `GOTOOLCHAIN=go1.26.9 go test -run TestOnFailure ./engine/...`, red before 4.2.
- [ ] 4.2 Add the `ignore` short-circuit at the terminal-exhaustion point of `handleActionFailed` (design D5): a WARN carrying instance, node, command and error; entry re-enters the strategy unmerged; completion advances the token. Leave the `stop` path untouched. Make 4.1 green, along with the entry and completion rows of *The re-invocation names the failed action*, ResolveIncident included. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./engine/...`.

## 5. Runtime, stores and adapters

- [ ] 5.1 Write and pass the runtime tests for the candidate-resolution row of *The output reaches the variables snapshot, the predicate and the candidates*, and for *An unregistered entry action fails like any unknown action*. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./runtime/...`.
- [ ] 5.2 Write and pass *No task row exists on any dialect* through `processtest` on memory, Postgres, MySQL and SQLite. Verify with the `scripts/modules.sh` loop on the store modules (Docker).
- [ ] 5.3 Write and pass *Task routes answer 404 on every adapter* through `internal/transporttest` on stdlib, gin and fiber. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./internal/transporttest/... ./transport/...`.
- [ ] 5.4 Write and pass *A crash after dispatch re-drives the entry action under the same key* on the recovery path that `persist-pending-commands` built. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./runtime/...` (Docker for the SQL store rows).

## 6. Docs and integration

- [ ] 6.1 Update the `humantask` package doc, the `WithEntryAction` and `WithOnFailure` godoc, and the `UserTask` field docs. Cover the failure rule (*an awaited task action stops the instance on terminal failure unless the author says to ignore it; an update action fails to its actor*), the deadline starting at task creation, and the post-release note. Cite `openspec/specs/human-task-lifecycle/spec.md`, never a change path. Verify with `scripts/check-doc-refs.sh` and `go doc ./humantask`.
- [ ] 6.2 Add the release-note lines for the release after v0: `completion_action` object form; `on_failure: ignore`; the blank `completion_action` refusal. Verify the lines are present in the release-notes file `cut-v0-release` established.
- [ ] 6.3 Measure the rollback hazard in design, *Risks*: decode a snapshot with a pending entry action using the base commit's engine, record what happens, and state the rollback guidance in design.md *Migration Plan*. Verify by running the probe test at the base commit.
- [ ] 6.4 Run the full suite, lint and the repo checks. Verify with the `scripts/modules.sh` loop (Docker), `golangci-lint run`, `scripts/check-openspec.sh`, `scripts/check-doc-refs.sh` and `scripts/check-test-timeout.sh`.

## Workflow follow-up

- The implementing PR names this change and ends with `openspec archive add-user-task-entry-action`.
- After merge, comment on and close kartaladev/wrkflw#121 with the change name.
