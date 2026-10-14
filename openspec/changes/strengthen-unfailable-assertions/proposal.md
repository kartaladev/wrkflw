# Proposal

## Why

The baseline (design D7, group 5) found test assertions that cannot fail for the reason they state. They pass by construction, so a future edit can remove the behavior they appear to protect and the suite stays green. The behavior they describe holds today. This is a coverage defect, not a false promise, and it is the same class as kartaladev/wrkflw#227.

The main shape, measured on main: `engine.Step` returns the zero `StepResult` on every error (`engine/step.go`, four `return StepResult{}, err` sites), and every handler refusal it reaches also returns `StepResult{}`. So `assert.Empty(t, r.Commands, "a refused verb must dispatch nothing")` on an error path compares nil with nil. It fails only when the sentinel assertion beside it fails already. A second shape replays a refusal against the caller's own input state, which `Step` never modifies because it deep-clones its input. That replay cannot see whether the refusal consumed anything.

## What Changes

- **Absorbs kartaladev/wrkflw#227** (folded in): the three `refusedLoudly` no-mutation assertions in the boundary-target test. The fix is the white-box call to `fireBoundaryArm` that the issue prescribes. Re-measured on main: they are still vacuous. The issue asked to enumerate the whole helper; it has five assertions. The `err` check and the boundary-name check are live. The other three are the vacuous ones (returned tokens, returned commands, the caller's `armed` copy).
- **Engine no-mutation assertions on a refusal path.** Enumerated by grep over `engine/*_test.go`, then classified by reading each site. 30 assertions in 14 files compare an empty `StepResult` field after a required error, and 4 replay a refusal or read a refusal's effect from the caller's input. The full list is in design.md. Each one is replaced by an observation that can fail: the working state after the refusing function, captured white-box through a test-only shim in `engine/export_test.go`. Or it is relabelled as a control, where the property it states is unobservable even white-box.
- **One discriminating pin of the zero-result-on-error contract.** The generator-failure row of `TestStepIDGen` gains a zero-`StepResult` assertion. On that path `dispatch` has already produced commands and state when the ID error discards them, so it is the only error path where the contract is not true by construction.
- **gin 5xx test** (`TestInternalError_NoRawErrorLeak`): it checks only that `message` is empty, so a raw error leaked into any other field passes. It gains the raw-body text check its stdlib and fiber siblings already make, as the `http-transport` scenario *An unclassified failure never reaches the client* states.
- **Unevaluable-predicate 500 row** (`TestClassifyError_ChainRenderingPerArm`): it classifies a hand-built `fmt.Errorf` that wraps no sentinel, so it reaches the 500 default whatever the authorizer does. It will classify an error produced by a real `authz.RoleAuthorizer` call with an unevaluable predicate.
- **`TestZeroValuedMisplacedKeyIsAsymmetric`** calls the key gate directly. It will decode each row through the JSON and YAML definition decoders, which is where the `definition-wire-format` scenario places the behavior.
- **Excluded after re-measurement** (stale): the `TestChainerHandle` transient row (it asserts `starts = 1`, `runtime/chain/chainer_test.go`) and `TestXID` (it checks the second call's error, `runtime/idgen/idgen_test.go`). The baseline already tightened both.
- **Proof per site.** Each strengthened assertion is shown to go red under a deliberate mutation of the code it guards, and is then restored. The mutation is recorded in the task.
- Not BREAKING. No impact on persisted formats, on the default security posture, or on a consumer's go.mod. Test files and `engine/export_test.go` only, so this sets `skip_specs`.

Out of scope:
- Exporting sentinels for the unresolvable boundary target and the unevaluable predicate. `export-refusal-sentinels` owns that. This change asserts whatever that change has exported when it lands, and the error text otherwise.
- Changing any guard's position in production code. If a probe is red at HEAD, see the open decision in design.md.
- Input-purity checks that compare the caller's state before and after a refusal (`reverse_instance_test.go`, the `st.Incidents` check in `baseline_operator_incidents_test.go`). They pin `Step`'s clone-on-entry contract, which is what they claim.
- `committedAfter` post-conditions. They fail when the guard is deleted, and their claim (what the caller commits) is exactly what they measure.
- The `step_task_lifetime_guard_test.go` controls. They are already labelled "CANNOT fail" beside a live whole-state check.

Decisions needed before tasks are worked (resolved in design.md): D1 at what level each probe observes, D2 what happens to a property that is unobservable even white-box, D3 the disposition of #227.

**Coordination**
- Size: L
- Depends on: establish-baseline-specs
- Blocks: nothing
- Readiness: agent

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. No externally observable behavior changes, so `skip_specs: true`.

## Impact

- Test files only. Engine (14 files), `engine/idgen_test.go`, `engine/export_test.go` (test-only shims, compiled only under `go test`), `transport/http/gin/gin_test.go`, `transport/http/httpcore/errors_rendering_test.go`, `definition/model/node_wire_keys_test.go`.
- The test files are shared with `export-refusal-sentinels` (the httpcore row, the boundary-target error) and `refuse-case-variant-json-keys` (the definition decoders). Either can land first. See design.md.
- When archived, comment on and close kartaladev/wrkflw#227.
