# Tasks

Before writing any Go, start with the `cc-skills-golang:golang-how-to` skill. Tables follow the project `table-test` skill: `assert` closures and `t.Context()`. Run every command from the repo root. The specified work does not depend on Open Questions OQ1 to OQ4 in the design. If a decision on one of them arrives before this work starts, update the change with `/opsx:update` first. Do not fold it in here.

## 1. Reproduce the defects (red)

- [ ] 1.1 Declare `ErrInvalidEndBehavior`, `ErrInvalidTriggerKind`, `ErrKeyNotOnEndBehavior` and `ErrInvalidDeadlineFlow` in `definition/model/validate.go`. Give each only enough body to compile. Each godoc states the constraint, names the identifiers it governs, and cites its capability spec (`openspec/specs/definition-wire-format/spec.md`, or `openspec/specs/definition-validation/spec.md` for the deadline flow).
- [ ] 1.2 Add table tests in `definition/model/node_wire_vocabulary_test.go`, one row per Examples row:
  - `TestEndBehaviorVocabularyAtDecode` covers *An out-of-vocabulary end_behavior is refused* and *The refusal locates the node and quotes the value*.
  - `TestTriggerKindVocabularyAtDecode` covers *An out-of-vocabulary trigger kind is refused at every trigger site*.
  - `TestEndBehaviorKeysAtDecode` covers *A key its end_behavior does not read is refused*.

  Rows assert with `errors.Is`, and JSON and YAML rows both decode through the public entry points. Watch every refusal row go red with a nil error: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'Test(EndBehaviorVocabulary|TriggerKindVocabulary|EndBehaviorKeys)AtDecode' -count=1`.
- [ ] 1.3 Add `TestDeadlineFlowMustResolve` in `definition/model/validate_test.go` for *A deadline flow that resolves to no flow is refused*. Watch it go red: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TestDeadlineFlowMustResolve' -count=1`.
- [ ] 1.4 Add the negative controls in the same tables:
  - *The vocabulary values decode to their own meaning*;
  - *Every trigger kind name is accepted and round-trips*;
  - *A null or absent trigger is not refused*;
  - *Legal combinations and empty values are accepted*;
  - *error_code on a boundary event is not an endEvent key*;
  - *A deadline flow that resolves is accepted*.

  Confirm they are green before the fix and that the 1.2 and 1.3 rows are red: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -count=1 -run 'Test(EndBehavior|TriggerKind|DeadlineFlow)' -v`.

## 2. Vocabulary gates at fromWire (green)

- [ ] 2.1 Implement `checkEndBehavior` in `node_wire_vocabulary.go` (design D1, D4). Spell the vocabulary in `model` and guard it with the three-test pattern from design D2, mirroring the termination-outcome tests: set equality with `event.EndBehavior.String()`, every name through the decoder, and every name against the leaf spec. Makes *An out-of-vocabulary end_behavior is refused*, *The refusal locates the node and quotes the value*, *The vocabulary values decode to their own meaning* and *The refusal reaches a nested subprocess* (end_behavior) pass. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'EndBehavior' -count=1`.
- [ ] 2.2 Implement `checkTriggerKinds` over `timer_trigger`, `deadline_trigger` and `wait_trigger`. The accepted set is derived from `kindString` (design D2). Add a test that asserts set equality with `ReadTrigger`'s handled kinds, and a test that each name round-trips through `PutTrigger`. Makes *An out-of-vocabulary trigger kind is refused at every trigger site*, *Every trigger kind name is accepted and round-trips*, *A null or absent trigger is not refused*, *YAML has no trigger objects* and *The refusal reaches a nested subprocess* (trigger) pass. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TriggerKind' -count=1`.
- [ ] 2.3 Implement `checkEndBehaviorKeys` with the conditional-key table derived by probe (design D2). Add a test pinning the derived table against the `EndBehavior` prose in `node_wire.go`. Makes *A key its end_behavior does not read is refused*, *Legal combinations and empty values are accepted*, *error_code on a boundary event is not an endEvent key*, *The refusal reaches a nested subprocess* (endEvent keys) and *Every library-encoded endEvent decodes again* pass. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ ./definition/event/ -run 'EndBehaviorKeys|EndOption' -count=1`.
- [ ] 2.4 Wire the gates into `fromWire` in the design D3 order. Extend `TestTheKindGateRunsBeforeTheValueGate`, or add a sibling test, to pin every adjacent pair. Makes *The gates answer in a fixed order* and *The kind gate still answers first for a key the kind never reads* pass. Verify that swapping any two calls turns the test red, then restore the order: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'GateRunsBefore|GatesAnswer' -count=1`.
- [ ] 2.5 Rewrite `checkTerminationOutcome`'s message and godoc. Drop the "accepted and dropped" clause and keep the `end_behavior "terminate"` precondition. Flip `TestTheDiagnosticsStatedPreconditionIsTrue`: it now asserts that the bare `termination_outcome` case is refused with `ErrKeyNotOnEndBehavior`, and that the message no longer claims a drop. Update the `ErrInvalidTerminationOutcome` godoc and the `node_wire_vocabulary.go` file comment ("SCOPE, deliberately narrow"), which say the other vocabularies still collapse. Makes *The refusal locates the node and quotes the value* (termination_outcome, MODIFIED) pass. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TerminationOutcome|StatedPrecondition' -count=1`.
- [ ] 2.6 Extend the library round-trip guards to the new sentinels: the golden byte round-trip, "No library output is refused by the key gate", and the YAML store round-trip. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/... -count=1`.

## 3. Deadline flow reference (green)

- [ ] 3.1 Add the `ErrInvalidDeadlineFlow` rule to `validateStructure`, per definition level, mirroring the `ErrInvalidRecoveryFlow` loop (design D5). Makes *A deadline flow that resolves to no flow is refused*, *A deadline flow that resolves is accepted* and *Every authoring path reaches the rule* pass. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/... -run 'DeadlineFlow' -count=1`.
- [ ] 3.2 Update the comments in `engine/step_timers.go` and `engine/flow_identity.go` that say DeadlineFlow has no validator rule. Keep the blank-refusal comment: a blank flow ID is still legal until OQ1 is decided. Verify: `GOTOOLCHAIN=go1.26.9 go test ./engine/ -count=1 && scripts/check-doc-refs.sh`.
- [ ] 3.3 Add a `MemDefinitionRegistry.Register` row in `runtime/kernel` for *Publishing refuses before any I/O on every store*. Verify: `GOTOOLCHAIN=go1.26.9 go test ./runtime/kernel/ -run 'Register' -count=1`.

## 4. Store parity (all dialects, Docker)

- [ ] 4.1 In `internal/persistence/store/definitions_conformance_test.go`, add a `forEachDialect` test. It inserts, with raw SQL, out-of-band rows carrying `end_behavior: "terminat"` and `wait_trigger.kind: "duratoin"`. It asserts that `GetDefinition` and `Lookup` fail wrapping `ErrInvalidEndBehavior` and `ErrInvalidTriggerKind`, and that a library-published `terminate` row still loads. These are the two *A stored row written out of band fails to load by name* scenarios. No store code is expected to change; if it must, stop and revisit the design. Verify with Docker running: `GOTOOLCHAIN=go1.26.9 go test ./internal/persistence/store/ -run 'TestDefinitionStoreRefusesUnrecognisedWireValues' -count=1`.
- [ ] 4.2 In the same file, add a `forEachDialect` row for *Publishing refuses before any I/O on every store*. It asserts `errors.Is(err, model.ErrInvalidDeadlineFlow)` and that no row exists afterwards. Verify with Docker: `GOTOOLCHAIN=go1.26.9 go test ./internal/persistence/store/ -run 'TestDefinitionStorePublishRefusesDanglingDeadlineFlow' -count=1`.

## 5. Docs, cost and integration

- [ ] 5.1 Update the user-facing docs that describe these keys: the `NodeWire` field comments in `node_wire.go`, the BPMN divergence table in `doc.go` if it names `end_behavior` or trigger kinds, and any example YAML or JSON that authored a now-refused combination. Verify: `grep -rn 'end_behavior\|wait_trigger' doc.go docs examples` shows only legal combinations, and `GOTOOLCHAIN=go1.26.9 go test ./... -run Example -count=1` passes.
- [ ] 5.2 Run `BenchmarkGoldenDecode` on main and on the branch with `-count=10`, and put the `benchstat` comparison in the PR body (design Risks): `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run '^$' -bench 'BenchmarkGoldenDecode' -benchmem -count=10`.
- [ ] 5.3 Run the full suite per module with Docker (the CONTRIBUTING.md form), and the OpenSpec guard: `mods="$(scripts/modules.sh)"; for pat in $(printf '%s\n' "${mods}"); do GOTOOLCHAIN=go1.26.9 go test -race "${pat}"; done && scripts/check-openspec.sh`.
- [ ] 5.4 Check the scenario-to-test map: the PR body names, for every scenario in this change's two delta specs, the test that covers it. Each named test ran green under 5.3.

## Workflow follow-up

- Before 1.1, a human confirms Readiness: the four Open Questions stay deferred, or are decided and folded in with `/opsx:update`.
- The PR body names this change, marks it **BREAKING**, flags the persisted-format and republish impacts, and carries the repair recipe from design Risks.
- The PR's last commit runs `openspec archive refuse-unrecognized-wire-values`. Before it does, re-read `openspec/specs/definition-wire-format/spec.md`, in case `refuse-case-variant-json-keys` or `export-refusal-sentinels` archived first (design D6).
- After merge, comment on and close kartaladev/wrkflw#219, #206, #194 and #193, naming this change.
