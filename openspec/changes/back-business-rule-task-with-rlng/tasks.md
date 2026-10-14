# Tasks

Every Go task starts with the `cc-skills-golang:golang-how-to` skill, then runs red, green, refactor. Waits and deadlines follow `docs/agents/eventually-waits.md` and `docs/agents/test-deadlines.md`. `GOTOOLCHAIN=go1.26.9` is set on every command. Commands run per module, as CONTRIBUTING.md describes. Store parity rows need Docker.

This change is post-release. Do not start it until `cut-v0-release`, `bound-untrusted-input` and `improve-wire-gate-diagnostics` are archived **and** rlng has tagged `v0.0.1`.

## 1. Gate

- [ ] 1.1 Re-measure rlng at `v0.0.1`, and record the results in design.md Context: its tag and commit, its `go.mod` requirements (compare with the three in proposal, Consumer go.mod), whether a compiled pipeline evaluates with a `context.Context` and honors cancellation (D4), how a decimal marshals to JSON (OQ5), and the config model an inline document follows. If any of these contradicts a proposal premise, update the proposal with `/opsx:update` before continuing. Verify that design.md names the tag and that `openspec validate back-business-rule-task-with-rlng --strict` passes.
- [ ] 1.2 Revisit `RuleSpec.Inline` against rlng `v0.0.1`'s config model (OQ4), **before** `ErrRuleNotSupported` is deleted. This is the last point a shape revision costs no migration. Record the result in design.md. Verify with `openspec validate back-business-rule-task-with-rlng --strict`.
- [ ] 1.3 Record the maintainer's answers to OQ1 through OQ6 in design.md. For any answer that differs from the recommendation, rewrite the requirements marked *Pending* with `/opsx:update` before starting the group that depends on them. Verify with `openspec validate back-business-rule-task-with-rlng --strict` and `scripts/check-openspec.sh`.

## 2. Rule port in the root module (D1, OQ1)

- [ ] 2.1 Add the dependency-free port package with `Catalog`, `Compiler` and `Evaluator`, the sentinels `ErrUnknownRule` and `ErrRuleCompile`, and godoc that cites `openspec/specs/business-rule-evaluation`. Add an in-memory catalog and a counting test compiler under test-only files for later groups. Verify with `go test -race ./rule/...` and `go vet ./rule/...`.
- [ ] 2.2 Write the guard test for *The engine arrives only through the adapter module*, rows 1 and 2. It extends the module-graph guard owned by `split-persistence-modules`, so that the root's non-test graph denies `github.com/kartaladev/rlng`, `github.com/shopspring/decimal` and `github.com/go-viper/mapstructure/v2`. Verify with the guard test's `go test` command, and by temporarily adding an rlng import to a root package to watch it go red.

## 3. Authoring: accept rules, refuse what cannot run (OQ2, OQ3)

- [ ] 3.1 Write the failing tests first. In `definition/model/validate_test.go` and the builder and loader tests, cover *A businessRuleTask with a rule and no action validates* (every row, with a registered stub compiler) and *Rule and action together are refused* (both rows). The rule-only rows fail today with `ErrRuleNotSupported`. Verify with `go test -run 'TestValidate.*Rule' ./definition/model/...`, red before 3.2.
- [ ] 3.2 Delete `ErrRuleNotSupported` and its `Validate` branch. Add the compiler registry and its loader option, mirroring `WithValidatorRegistry`, and `model.ErrNoRuleEngine`. Compile inline rules at `Build` and at decode when a compiler is present. Make 3.1 green, plus *A malformed rule is reported as malformed, not as a conflict*, *A businessRuleTask without a rule validates*, *No engine, no rule-bearing definition* (Build and Register rows), *Without a rule, no engine is needed* and *A broken inline document is refused, a sound one builds*. Convert the `ErrRuleNotSupported` rows in `validate_test.go`, `rulespec_test.go` and `strict_decoding_test.go`, and keep `TestValidate_RuleShapeIsTheCodecDomain` asserting the shape invariant, now unmasked. Verify with `go test -race ./definition/...`.
- [ ] 3.3 Make *No engine, no rule-bearing definition* pass for the three `PublishDefinition` rows: nothing is written on Postgres, MySQL and SQLite. Verify with `go test -race -run TestPublishDefinition ./persistence/...` (Docker), per the module layout `split-persistence-modules` leaves.
- [ ] 3.4 Write and then pass *A given catalog makes Build check names* (all three rows). Verify with `go test -race -run TestBuildRuleCatalog ./definition/model/...`.
- [ ] 3.5 Apply OQ4's answer. Under the recommended option, write the failing YAML row (an integer literal wider than float64 in an inline rule fails with `ErrInvalidRule`), then a passing JSON control row, then the fix in `rulespec.go`. Rewrite the `RuleSpec`, `WithRule` and `WithInlineRule` godoc from "reserved" to what they do. Verify with `go test -race -run TestRuleSpec ./definition/model/...` and `scripts/check-doc-refs.sh`.

## 4. Engine and runtime: evaluate on the action path (D2, D3, D4, D5)

- [ ] 4.1 Write the failing engine tests for *A named or inline rule is evaluated and no action runs*. Assert that a rule-bearing businessRuleTask emits a command marked as a rule evaluation for its node, and that a rule-less one emits today's `InvokeAction`. Today both emit a plain action. Verify with `go test -run TestBusinessRuleTask ./engine/...`, red before 4.2.
- [ ] 4.2 Add the rule branch to the businessRuleTask strategy and the additive discriminator on the command (D2). Make 4.1 green, and *The pre-rule behavior is unchanged* (all four rows) at engine level. Rewrite the dispatch-table comment in `engine/step_dispatch.go`. Verify with `go test -race ./engine/...`.
- [ ] 4.3 In the runtime, resolve the evaluator from the definition and node (catalog for a name, compiled artifact for an inline rule), apply the action timeout (D4), and feed back `ActionCompleted` or `ActionFailed`. Add the driver option for the rule catalog. Make these pass end to end through the ProcessDriver: *A named or inline rule is evaluated and no action runs*, *Output keys replace, other variables stay*, *Unknown and near-miss names fail*, *An error boundary catches a failed evaluation*, *A successful evaluation does not route through the boundary* and *The pre-rule behavior is unchanged*. Verify with `go test -race -run 'TestBusinessRule|TestRule' ./runtime/...`.
- [ ] 4.4 Make *A duplicate completion of an evaluation is refused like an action's* and *A restart before the result is committed re-evaluates once* pass. If `persist-pending-commands` has landed, add a row proving that a pending row written before this change, with no discriminator, still re-drives as an action. Verify with `go test -race -run 'TestRuleRecovery|TestRuleDuplicate' ./runtime/... ./persistence/...` (Docker).
- [ ] 4.5 Make *Instances of one version share one compilation* (all three rows, the concurrent one under `-race`) and *A recovered instance evaluates its own version's rule* pass. Verify with `go test -race -run TestRuleCompileOnce ./runtime/...`.
- [ ] 4.6 Make *Merged output survives a snapshot round trip on every store* pass on Postgres, MySQL and SQLite. Verify with `go test -race -run TestRuleOutputSnapshot ./persistence/...` (Docker).

## 5. The rlng adapter module (D1, OQ1, OQ5)

- [ ] 5.1 Create the nested module at the OQ1 path, with its `go.mod` requiring rlng at `v0.0.1` and root at a pseudo-version of a pushed commit (#30's rule, as `split-persistence-modules` applies it). Add it to `go.work` and raise `MIN_MODULES` in `scripts/modules.sh`. Verify with `scripts/check-module-drift.sh` and with the `scripts/modules.sh` test loop.
- [ ] 5.2 Implement `Compiler` and `Catalog` over rlng, using test-first tables against real rlng pipelines. Make *A broken inline document is refused, a sound one builds* pass with the real engine (parse error and dependency-cycle rows), and the row 3 of *The engine arrives only through the adapter module*. Verify with `go test -race ./...` inside the module.
- [ ] 5.3 Apply OQ5's answer. Under the recommendation, write the failing table for *Decimal values keep every digit* (all five rows, reloaded through a SQL store), then convert decimal output to its exact string. Verify with `go test -race -run TestDecimalOutput ./...` inside the module (Docker for the store reload).
- [ ] 5.4 Add the action adapter and make *A pipeline runs as a catalog action* pass for both kinds, including through `action.Typed`. Verify with `go test -race -run TestAction ./...` inside the module.
- [ ] 5.5 Write the module's package doc and a runnable `Example`. It covers registration, a named rule, an inline rule, and the limits: CPU time is bounded only as D4 records, and YAML inline fidelity is as OQ4 settles it. Verify with `go test -run Example ./...` inside the module and `go doc` on the package.

## 6. Docs and integration

- [ ] 6.1 Rewrite the `businessRuleTask` row of the `# BPMN 2.0` table in `doc.go`, from "reserved" to what the kind does, and `CONTEXT.md`'s **Rule** entry. Verify with `go test -run TestBPMN ./` (`doc_bpmn_test.go`) and `grep -rn ErrRuleNotSupported --include='*.go' --include='*.md' . | grep -v '^./openspec/'`, which prints nothing.
- [ ] 6.2 Run the full suite on every module with the `scripts/modules.sh` loop, plus lint and `scripts/check-doc-refs.sh`. Verify that all exit 0 (Docker for store rows).

## Workflow follow-up

- The implementing PR names this change, and its last commit runs `openspec archive back-business-rule-task-with-rlng`.
- After merge, comment on and close kartaladev/wrkflw#118 with the change name.
- The release notes for the tag that ships this change name the new module, its go.mod footprint, and the deletion of `ErrRuleNotSupported`.
