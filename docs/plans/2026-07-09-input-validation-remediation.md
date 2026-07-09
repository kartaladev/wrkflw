# Input-Validation Remediation Plan (post whole-branch review) — NEXT SESSION

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`. Execute on branch
> `feat/input-validation` (already contains Item-3 Tasks 1–14, HEAD `c6043d0`). These tasks fold in the
> whole-branch `/code-review` findings (2026-07-09). **Order is fixed: R1→R10 (review fixes) BEFORE R11 (Task 7b),
> per user instruction.** Then whole-branch re-review → merge to `main`. Ledger: `.superpowers/sdd/progress.md`
> (Item-3 review block has the full findings). Load the Go lens via `cc-skills-golang:golang-how-to` +
> custom `table-test`/`use-mockgen`/`use-testcontainers`. TDD strict throughout.

**Goal:** Close the correctness/quality gaps the whole-branch review found so node-level validation works through EVERY path (batteries-included `service.Engine`, nested subprocesses, YAML+nested, durable reload), then merge.

**Context recap:** the feature validates external input at three boundaries (start `Drive`, completion `TaskService.Complete`, message `DeliverMessage`) via a neutral `validation` port + memoizing `Gate` + adapters. The review confirmed three paths where it silently no-ops or fails, plus cleanups. Branch is green (build/lint/`-race`); these are logic/design fixes.

## Global Constraints
- Module `github.com/zakyalvan/krtlwrkflw`; Go 1.25; `go build ./...`, `go test -race ./...`, `golangci-lint run ./...` clean per task.
- TDD strict: failing test first, observed RED via a separate `go test` run, then GREEN. Fail-closed principle: validation must NEVER be silently skipped.
- Error-sentinel prefix `workflow-<pkg>: ...` (note: `runtime/task` uses `workflow-runtime: taskservice:` file-consistently — keep it).
- `table-test` custom skill (`assert`-closure form); black-box `<package>_test`; `t.Context()`; testcontainers via `database.RunTestDatabase` (Docker required for PG/MySQL).

---

## R1 — [HIGH] Wire the DefinitionResolver into `service.Engine` (1-line fix + test)

**Finding #2 (CONFIRMED).** `service/service.go:183` builds `TaskService` with only `WithClock`; a zero-config consumer's `WithCompletionValidation` silently no-ops because the resolver is nil.

**Files:** `service/service.go` (~line 183); test in `service/*_test.go`.
- [ ] Write a failing test: build a `service.Engine` (zero-config `service.New`/`NewEngine`) with a definition whose UserTask has `activity.WithCompletionValidation(vexpr.New("decision in ['approve','reject']"))`; complete the task via the engine's `CompleteTask` path with invalid output; assert it's rejected with `errors.Is(_, validation.ErrInvalidInput)`. Run → RED (currently accepted).
- [ ] Fix: pass `task.WithDefinitionResolver(c.reg)` alongside `task.WithClock(c.clk)` at the `task.NewTaskService(...)` call. `c.reg` (`kernel.DefinitionRegistry`) is already in scope and validated non-nil.
- [ ] GREEN; `go test ./service/... -race`, build, lint clean. Commit `fix(service): wire DefinitionResolver into TaskService so completion validation works on the zero-config path`.

## R2 — [HIGH] Scope-aware node resolution for nested-subprocess validation

**Finding #1 (CONFIRMED, fail-OPEN silent skip).** `(*ProcessDefinition).Node(id)` (definition/model/definition.go:76) is flat; `CompletionValidation` (runtime/task/service.go:218) and `PayloadValidation` (runtime/processdriver_message.go:59) on a node nested inside a Sub/EventSubProcess are never found → silently skipped. Same class as the fixed inline-action bug I-1.

**DECISION POINT (resolve first, ideally with a short opus assessment):** two approaches —
- (a) **Recursive definition lookup** `model.NodeDeep(id) (Node, bool)` walking into `SubProcess`/`EventSubProcess` `.Subprocess` trees. Simplest; requires node IDs to be unique across the whole definition tree (verify this invariant — check the engine/validate.go uniqueness rules).
- (b) **Scope-aware** reuse of the engine's `defForScope(top, state, scopeID)` (engine/step_state.go:20) — threads `ScopeID` through `humantask.HumanTask` (add a `ScopeID` field, persist it — another migration) and through `engine.InstanceState.MessageTargetNode`'s return. Heavier but matches how the engine resolves scoped nodes everywhere else.
Recommend **(a) `NodeDeep`** if tree-wide node-ID uniqueness holds (far cheaper — no schema change, no ScopeID plumbing); fall back to (b) only if IDs can collide across scopes. **Confirm the uniqueness invariant before choosing.**

**Files:** `definition/model/definition.go` (+ test) for NodeDeep; `runtime/task/service.go:218` + `runtime/processdriver_message.go:59` to use it; regression tests reproducing the nested case at both call sites.
- [ ] Write failing tests FIRST at BOTH sites: (completion) outer→SubProcess(inner UserTask "approve" with `WithCompletionValidation`)→end; create the task, `Complete` with invalid output; assert `ErrInvalidInput` (currently silently accepted → RED). (message) a ReceiveTask with `WithPayloadValidation` nested in a subprocess; `DeliverMessage` invalid payload; assert `ErrInvalidInput` + no advance (currently silently applied → RED).
- [ ] Implement the chosen resolver; both call sites use it. GREEN. Verify existing top-level tests still pass.
- [ ] `go test ./definition/... ./runtime/... -race`, build, lint clean. Commit `fix(validation): resolve validation nodes scope-aware so nested-subprocess nodes are validated (fail-closed)`.

## R3 — [MED-HIGH] Thread the validator registry into nested-subprocess YAML build

**Finding #3 (CONFIRMED, load fails).** `definition/model/yaml.go` `coreFromYAML` (:126) builds a nested subprocess core with `validators == nil`, and `fromNodeYAML` calls nested `core.build()` (:83) BEFORE `ParseYAML` applies `WithValidatorRegistry` (opts loop ~:161) → a `validation:` descriptor on a nested node makes `NewLoader` always error.

**Files:** `definition/model/yaml.go`, `definition/model/builder.go` (+ test).
- [ ] Failing test FIRST: YAML with an `eventSubProcess`/`subProcess` whose nested node carries `validation: {kind: expr, schema: ...}`; `definition.NewLoader(r, WithValidatorRegistry(reg-with-expr))` → currently errors during load → RED.
- [ ] Fix: defer nested-core validation reconciliation until after LoaderOptions apply (e.g. thread the registry into `coreFromYAML`/nested `build()`, or hold nested cores unreconciled and reconcile the whole tree once the outer registry is set — mirror how the outer core defers to `Build()`). Ensure NO double-reconcile and that top-level YAML validation still works.
- [ ] GREEN; regression: nested + registry reconstructs and validates. Commit `fix(definition): thread validator registry into nested-subprocess YAML build`.

## R4 — [MED] Move the Gate cache key into `validation.Gate` (fix colon-collision + dedup)

**Findings #4/#5 merged.** `defID:version:nodeID` is hand-built at 3 sites (processdriver.go:326, processdriver_message.go:43, task/service.go:220) with an unescaped `:` join → collision risk (`defID="a:1",v=2,node="x"` == `defID="a",v=1,node="2:x"`).

**Files:** `validation/gate.go` (+ test); the 3 injection sites.
- [ ] Failing test FIRST: a collision test proving two distinct (defID,version,nodeID) triples currently map to the same key / reuse the wrong cached Validator (or assert the new API's keys are distinct). RED against the current string-join.
- [ ] Add `Gate.ValidateNode(ctx, defID string, version int, nodeID string, s ValidationStrategy, input map[string]any) error` using an unambiguous internal struct key `{defID,version,nodeID}` (not a joined string). Replace the 3 hand-built key sites with it. GREEN.
- [ ] Commit `refactor(validation): key Gate cache by struct not colon-joined string (dedup 3 sites, fix collision)`.

## R5 — [MED] Export a single node→strategy resolver from `definition/model`

**Finding #6.** The 3 runtime type-switches (Drive `starts[0].(event.StartEvent)`, `payloadValidationStrategy`, Complete `node.(activity.UserTask)`) duplicate the generic `NodeSpec.ValidationGet`/unexported `nodeValidationStrategy`. A 5th validation-bearing kind would be silently skipped at these sites.

**Files:** `definition/model/validation_wire.go` (export), the 3 runtime sites.
- [ ] Export `func model.ValidationStrategyFor(n Node) validation.ValidationStrategy` delegating to the registered `NodeSpec.ValidationGet`. Test it returns the strategy for each of the 4 kinds and nil otherwise.
- [ ] Use it at the 3 runtime sites in place of the hand-rolled type switches (note: at Complete/DeliverMessage the node comes from R2's scope-aware lookup; at Drive from `def.StartNodes()[0]`). GREEN.
- [ ] Commit `refactor(validation): resolve node validation strategy via one exported model helper`.

## R6 — [MED] Resolve the phantom `WithValidationGate`

**Finding #7.** `runtime/task/service.go` gate doc-comment (and the original plan) reference a `WithValidationGate` option to share a Gate between driver and TaskService, but it was never implemented.
- [ ] DECISION: implement it (recommended — lets `service.Engine` share ONE gate so a strategy compiled once serves both driver and task service; small: add `WithValidationGate(*validation.Gate) TaskServiceOption` + a matching `runtime.WithValidationGate` driver option + wire a shared gate in `service.Engine`) OR remove the misleading doc-comment. If implementing, add a test that a shared gate compiles a strategy once across both. If removing, just fix the doc.
- [ ] Commit accordingly.

## R7 — [LOW-MED] Testable `Example` functions for the public options

**Finding #8 (CLAUDE.md Go rule #6).** Add `ExampleWithInputValidation`, `ExampleWithPayloadValidation` (event + activity), `ExampleWithCompletionValidation` — mirror `definition/event/example_boundary_options_test.go`. Runnable `// Output:` examples. Commit `docs(definition): testable Examples for With*Validation options`.

## R8 — [LOW] `model.PutValidation` ToWire helper

**Finding #9.** The `if ds, ok := v.XxxValidation.(validation.DescribableStrategy); ok { d := ds.Descriptor(); w.Validation = &d }` snippet is copy-pasted in 4 ToWire closures (activity.go:221/246, event.go:224/266). Add `model.PutValidation(s validation.ValidationStrategy, w *NodeWire)` (mirror `PutTrigger`) and call it from all 4. Test the non-describable branch. Commit `refactor(definition): PutValidation wire helper (dedup 4 ToWire sites)`.

## R9 — [LOW] Collapse adapter `Factory` onto `New`

**Finding #10.** `validation/jsonschema/jsonschema.go:58` and `validation/avro/avro.go:26` duplicate `New`'s body. Change to `func Factory(schema string) (validation.ValidationStrategy, error) { return New(schema), nil }` (New returns `DescribableStrategy` which satisfies `ValidationStrategy`). Existing round-trip tests cover it. Commit `refactor(validation): Factory delegates to New (jsonschema, avro)`.

## R10 — [LOW, optional/pre-existing] Reject `Version < 1` in Build/Validate

**Finding (PLAUSIBLE, pre-existing, system-wide).** `Qualifier{Version:0}` = "latest", so a def authored with `Version:0` + later re-registered can misresolve. NOT feature-specific (affects all instance-resume paths). Optional defensive fix: reject `Version < 1` in `definition/model` `Build`/`Validate` (0 is reserved as the latest sentinel). If done, add a test + note it's a behavior change (a def authored with Version 0 now fails Build). **Flag to the user before doing — it's a pre-existing engine semantic, arguably out of this feature's scope.**

*(Optional efficiency, defer unless cheap: precompute per-definition "has any PayloadValidation" to skip the DeliverMessage tier-scan; cache the "node has no CompletionValidation" outcome so Complete skips the resolver Lookup. Low priority.)*

---

## R11 — Task 7b: durable-reload reconstruction (LAST, after R1–R10)

**The deferred durable-path fix.** Process-global `validation.DefaultRegistry()` + `validation.Register(kind, f)`; `ProcessDefinition.UnmarshalJSON` reconciles pending descriptors against it LENIENTLY (unregistered kind → pending remains → runtime fail-closed), fixing `DefinitionStore.GetDefinition`/`Lookup` (json.Unmarshal path) with ZERO persistence changes; `build()` falls back to `DefaultRegistry()` when no explicit loader registry (authoring=strict / reload=lenient). Full brief in `.superpowers/sdd/progress.md` (Item-3 "Task 7b" line). NOTE: R3 (nested-YAML registry threading) may interact — reconcile the two registry-threading mechanisms coherently. Update ADR-0110's "durable reload" section from "pending" to "implemented" once done.

## Final — whole-branch re-review → merge
- [ ] `go build ./...`, `go test -race ./...` (Docker up for PG/MySQL), `golangci-lint run ./...` all clean.
- [ ] Whole-branch `/code-review` (golang-how-to lens) over the updated branch; address any Critical/Important.
- [ ] `superpowers:finishing-a-development-branch` → `--no-ff` merge `feat/input-validation` → `main`; update `docs/plans/HANDOVER.md` + memory. Next free ADR after this item: 0115.
