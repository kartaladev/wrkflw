# Input-Validation Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`. Execute on
> branch `feat/input-validation`. Steps use checkbox (`- [ ]`) syntax. Spec:
> `docs/specs/2026-07-09-input-validation-redesign.md`. Ledger: `.superpowers/sdd/progress.md`.
> Load the Go lens: `cc-skills-golang:golang-how-to` + custom `table-test`/`use-mockgen`/`use-testcontainers`.
> TDD strict (observe RED via a separate `go test` run before GREEN).

**Goal:** Reshape node-level input validation so the engine *decides* the scope-correct node (pure
`engine.TargetNode`) and the runtime *executes* the `Gate` before `Step` — fixing nested-subprocess
fail-open — then segregate the package into `definition/model/validate` + `runtime/validation` and
revert the boundary-injection smells.

**Architecture:** Validation runs pre-`Step` in the runtime, fed by one pure engine query. No
validator executes in the pure core; no emitted command (the effect stream is post-commit and can't
gate a commit). Package split keeps authoring (`definition/model/validate`) apart from execution
(`runtime/validation`).

**Tech Stack:** Go 1.25; `expr-lang/expr`, `santhosh-tekuri/jsonschema/v6`+`invopop/jsonschema`,
`linkedin/goavro/v2` (adapters, unchanged); testcontainers (Postgres/MySQL — Docker required).

## Global Constraints
- Module `github.com/zakyalvan/krtlwrkflw`; Go 1.25. `go build ./...`, `go test -race ./...`,
  `golangci-lint run ./...` clean per task.
- **TDD strict**, fail-closed: validation must NEVER be silently skipped.
- Error-sentinel prefix `workflow-<pkg>: ...`. `runtime/task` keeps `workflow-runtime: taskservice:`.
- `table-test` skill (`assert`-closure form, not `want`/`wantErr` fields); black-box `<package>_test`;
  `t.Context()`; testcontainers via `database.RunTestDatabase`.
- Two package clauses: `definition/model/validate` → `package validate`; `runtime/validation` →
  `package validation`. `definition/model/validate` imports nothing from `runtime`/`engine`.
- The branch is **unmerged**; reverts are forward-commit removals. After every revert task the tree
  must build and all remaining tests must pass (removed tests deleted alongside the code).

---

## PHASE A — Revert the boundary-injection smells

> After Phase A the feature temporarily performs NO boundary validation. That is expected; Phase C
> re-adds it through the new path. Keep the tree green throughout.

### Task 1: Revert completion validation from TaskService + the service.Engine wiring

**Files:**
- Modify: `runtime/task/service.go` — remove the `resolver` + `gate` fields, `DefinitionResolver`
  interface, `WithDefinitionResolver` option, and the validation block in `Complete`.
- Modify: `service/service.go:183-184` — drop `task.WithDefinitionResolver(c.reg)`.
- Modify/Delete tests: `runtime/task/*service*validation*_test.go`, and the
  `TestCompleteTaskValidatesOutputWhenResolverWired` case in `service/service_test.go`.

**Reference (authoritative "what to undo"):** `git show ac2bffa e0fa7e4 c3f6df8` — these added the
resolver, nil-def guard, and service wiring. Reverse exactly those additions.

- [ ] **Step 1:** Read `git show ac2bffa e0fa7e4 c3f6df8` to enumerate every added symbol.
- [ ] **Step 2:** In `runtime/task/service.go`, delete: the `resolver DefinitionResolver` and
  `gate *validation.Gate` struct fields; the `resolver`/`gate` init in `NewTaskService`; the
  `DefinitionResolver` interface + `WithDefinitionResolver` option; the entire `if s.resolver != nil { … }`
  block in `Complete` (currently lines ~209-226) and its now-unused imports (`model`, `strconv`,
  `activity`, `validation`). `Complete` returns after authz: `return engine.NewHumanCompleted(...)`.
- [ ] **Step 3:** In `service/service.go`, change the `task.NewTaskService(...)` call back to
  `task.NewTaskService(c.taskStore, c.authz, task.WithClock(c.clk))`.
- [ ] **Step 4:** Delete the completion-validation tests (the whole file if it only tests this) and
  the `TestCompleteTaskValidatesOutputWhenResolverWired` function in `service/service_test.go`.
- [ ] **Step 5:** Run `go build ./... && go test ./runtime/task/... ./service/... -race`. Expected: PASS.
- [ ] **Step 6:** `golangci-lint run ./runtime/task/... ./service/...` clean. Commit:
  `revert(validation): remove TaskService completion validation + service wiring (moves to engine-decides path)`

### Task 2: Revert HumanTask.DefID/DefVersion + persistence + migration

**Files:**
- Modify: `humantask/humantask.go` (remove `DefID`/`DefVersion` fields + doc), `humantask/memory.go`
  (remove the write-once preservation at lines ~34,49-50).
- Delete: `internal/persistence/store/migrations/postgres/0012_human_task_defref.sql`,
  `internal/persistence/store/migrations/mysql/0005_human_task_defref.sql`,
  `internal/persistence/store/migrations/sqlite/0004_human_task_defref.sql`.
- Modify: the humantask SQL store insert/scan (find via `grep -rn "def_id" internal/persistence/store`)
  to drop `def_id`/`def_version` columns; the runtime perform that builds `humantask.HumanTask` from
  `AwaitHuman` (find via `grep -rn "humantask.HumanTask{" runtime/`) to stop setting DefID/DefVersion.
- Modify tests: migration-count assertions (`internal/persistence/store/migrator_test.go`,
  `persistence/migrator_test.go`, and any dialect conformance test); the re-upsert-preservation
  lock-in test; the MemTaskStore write-once test.

**Reference:** `git show 234dd38 f08e2df c65ca6b` — reverse exactly these (fields, migrations, store
columns, population, write-once, and their tests). Note the migration count drops by 1 per dialect.

- [ ] **Step 1:** Read `git show 234dd38 f08e2df c65ca6b`.
- [ ] **Step 2:** Remove the three `*_human_task_defref.sql` migration files.
- [ ] **Step 3:** Remove `DefID`/`DefVersion` from `humantask.HumanTask` + the write-once block in
  `memory.go`; remove the SQL store column read/write; remove the population site in the runtime
  `AwaitHuman` perform.
- [ ] **Step 4:** Fix migration-count assertions (decrement expected counts) and delete the
  DefID-specific tests (write-once lock-in, re-upsert preservation).
- [ ] **Step 5:** Run `go build ./...`. Then, with Docker up,
  `go test ./internal/persistence/... ./humantask/... -race`. Expected: PASS (migration counts match).
- [ ] **Step 6:** `golangci-lint run ./...` clean on touched dirs. Commit:
  `revert(humantask): drop DefID/DefVersion + defref migration (engine resolves node scope at completion)`

### Task 3: Revert Drive + DeliverMessage boundary validation + MessageTargetNode

**Files:**
- Modify: `runtime/processdriver.go` — remove the `validationGate` field (line ~49), its init in
  `NewProcessDriver` (~145), and the start-var validation block in `Drive` (~323-333).
- Modify: `runtime/processdriver_message.go` — remove the validation block (~41-48) and the
  `payloadValidationStrategy` helper (~55-70); `DeliverMessage` reverts to load → `ApplyTrigger`.
- Modify: `engine/state.go` — remove `MessageTargetNode` (~876-890). Delete its test
  (`engine/message_target*_test.go`).
- Modify tests: `runtime/processdriver_validation_test.go`,
  `runtime/processdriver_message_validation_test.go` — delete (Phase C adds replacements).

**Reference:** `git show f226054 0a5634a f788884`.

- [ ] **Step 1:** Read `git show f226054 0a5634a f788884`.
- [ ] **Step 2:** Remove the `Drive` and `DeliverMessage` validation blocks + the driver
  `validationGate` field/init + `payloadValidationStrategy`.
- [ ] **Step 3:** Remove `MessageTargetNode` from `engine/state.go` and its test file.
- [ ] **Step 4:** Delete the two runtime `*_validation_test.go` boundary tests.
- [ ] **Step 5:** `go build ./... && go test ./runtime/... ./engine/... -race`. Expected: PASS.
- [ ] **Step 6:** `golangci-lint run ./runtime/... ./engine/...` clean. Commit:
  `revert(runtime): remove boundary start/message validation + MessageTargetNode (superseded by engine.TargetNode)`

---

## PHASE B — Package segregation (mechanical move, no behavior change)

### Task 4: Move validation → `definition/model/validate` + `runtime/validation`

**Files:**
- `git mv validation/validation.go definition/model/validate/validate.go`;
  `git mv validation/registry.go definition/model/validate/registry.go`;
  `git mv validation/{validation_test.go,registry_test.go} definition/model/validate/`.
- `git mv validation/expr definition/model/validate/expr` (and `callback`, `jsonschema`, `avro`).
- `git mv validation/gate.go runtime/validation/gate.go`;
  `git mv validation/gate_test.go runtime/validation/gate_test.go`.
- Set `package validate` in the definition-side files (and `_test` → `validate_test`), `package validation`
  stays in the runtime-side gate files.
- Rewrite every import: `krtlwrkflw/validation` → `krtlwrkflw/definition/model/validate` for the
  port/strategy/descriptor/registry; but `Gate`/`ErrInvalidInput` references → `krtlwrkflw/runtime/validation`.
  Adapters `krtlwrkflw/validation/{expr,…}` → `krtlwrkflw/definition/model/validate/{expr,…}`.

**No behavior change** — the gate on this task is the existing test suite passing before and after.

- [ ] **Step 1:** Move the port + registry + adapters to `definition/model/validate/…`, set
  `package validate`. Update intra-package references.
- [ ] **Step 2:** Move `gate.go` (+test) to `runtime/validation/`, `package validation`. `gate.go`
  imports `definition/model/validate` for `ValidationStrategy`; `ErrInvalidInput` stays with the gate.
- [ ] **Step 3:** Rewrite all importers (`grep -rln 'krtlwrkflw/validation' --include=*.go .`):
  definition/{model,activity,event,build}, transport/http/httpcore (ErrInvalidInput → runtime/validation),
  examples/scenarios/input_validation. Node-slot field types become `validate.ValidationStrategy`.
- [ ] **Step 4:** `go build ./...`. Fix every import until it compiles.
- [ ] **Step 5:** `go test ./... -race` (Docker up). Expected: PASS — identical behavior.
- [ ] **Step 6:** `golangci-lint run ./...` clean. Commit:
  `refactor(validation): segregate into definition/model/validate (authoring) + runtime/validation (execution)`

---

## PHASE C — Engine decides, runtime executes

### Task 5: `engine.TargetNode` — pure scope-aware node resolver

**Files:**
- Create: `engine/target_node.go`, `engine/target_node_test.go`.

**Interfaces:**
- Produces: `func TargetNode(def *model.ProcessDefinition, st InstanceState, trg Trigger) (model.Node, bool)`.
  Uses unexported `defForScope` (step_state.go:20), the 4-tier arm/token helpers, and
  `tokenAwaiting`. Returns `(nil,false)` for triggers other than `StartInstance`/`MessageReceived`/`HumanCompleted`.

- [ ] **Step 1: Failing test.** In `engine` (white-box `package engine`, mirroring
  `engine/step_triggers_test.go` setup), build a definition with a `UserTask` **nested in a
  SubProcess**, plus a top-level start node, and an `InstanceState` whose token is parked on the
  nested task in the sub-process scope. Assert `TargetNode(def, st, HumanCompleted{taskToken})`
  returns the **nested** UserTask node (the regression the redesign fixes). Add table rows for:
  message tier-4 nested ReceiveTask; `StartInstance` → top-level start node; a non-input trigger →
  `(nil,false)`. Use the `table-test` `assert`-closure form.
- [ ] **Step 2:** `go test ./engine/ -run TestTargetNode -race`. Expected: FAIL (`undefined: TargetNode`).
- [ ] **Step 3: Implement.**
```go
// TargetNode resolves the scope-correct node an external-input trigger targets, or (nil,false).
// It mirrors Step's own dispatch so the two never disagree on which node wins.
func TargetNode(def *model.ProcessDefinition, st InstanceState, trg Trigger) (model.Node, bool) {
    switch t := trg.(type) {
    case StartInstance:
        starts := def.StartNodes()
        if len(starts) != 1 { return nil, false }
        return starts[0], true
    case MessageReceived:
        nodeID, scopeID, ok := st.messageTargetNodeScoped(t.Name, t.CorrelationKey)
        if !ok { return nil, false }
        return nodeInScope(def, &st, scopeID, nodeID)
    case HumanCompleted:
        tok := st.tokenAwaiting(t.TaskToken)
        if tok == nil { return nil, false }
        return nodeInScope(def, &st, tok.ScopeID, tok.NodeID)
    default:
        return nil, false
    }
}
func nodeInScope(def *model.ProcessDefinition, st *InstanceState, scopeID, nodeID string) (model.Node, bool) {
    d, err := defForScope(def, st, scopeID)
    if err != nil { return nil, false }
    return d.Node(nodeID)
}
```
  Add `messageTargetNodeScoped(name, key) (nodeID, scopeID string, ok bool)` on `InstanceState` — the
  4-tier winner AND its scope: tier1 `armedEventByMessage`→`ae.CatchNode` + scope of `ae.GatewayToken`
  via `tokenByID(...).ScopeID`; tier2 `boundaryArmByMessage`→`ba.BoundaryNode` + scope of `ba.HostToken`;
  tier3 `eventSubprocessArmByMessage`→`ea.EventSubprocessNode` + `ea.EnclosingScopeID`; tier4
  `tokenAwaitingMessage`→`tok.NodeID` + `tok.ScopeID`. (Confirm exact field names against `engine/state.go`.)
- [ ] **Step 4:** `go test ./engine/ -run TestTargetNode -race`. Expected: PASS.
- [ ] **Step 5:** `go test ./engine/... -race`; lint clean. Commit:
  `feat(engine): TargetNode — pure scope-aware node resolution for external-input triggers`

### Task 6: `model.ValidationStrategyFor` + `model.PutValidation`

**Files:**
- Modify: `definition/model/validation_wire.go` (or a new `validation_resolve.go`) + test.

**Interfaces:**
- Produces: `func ValidationStrategyFor(n Node) validate.ValidationStrategy` (nil when the node has no
  slot); `func PutValidation(s validate.ValidationStrategy, w *NodeWire)` (sets `w.Validation` from a
  `DescribableStrategy`, no-op otherwise).

- [ ] **Step 1: Failing test.** Table over the 4 slot-bearing kinds (`StartEvent.InputValidation`,
  `IntermediateCatchEvent.PayloadValidation`, `UserTask.CompletionValidation`,
  `ReceiveTask.PayloadValidation`) each returning its strategy, and a plain node returning nil.
  For `PutValidation`: a describable strategy sets `w.Validation`; a callback strategy leaves it nil.
- [ ] **Step 2:** `go test ./definition/model/ -run 'TestValidationStrategyFor|TestPutValidation'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
```go
func ValidationStrategyFor(n Node) validate.ValidationStrategy {
    switch v := n.(type) {
    case event.StartEvent:            return v.InputValidation
    case event.IntermediateCatchEvent:return v.PayloadValidation
    case activity.UserTask:           return v.CompletionValidation
    case activity.ReceiveTask:        return v.PayloadValidation
    default:                          return nil
    }
}
func PutValidation(s validate.ValidationStrategy, w *NodeWire) {
    if ds, ok := s.(validate.DescribableStrategy); ok {
        d := ds.Descriptor(); w.Validation = &d
    }
}
```
  (If `definition/model` cannot import `definition/{event,activity}` without a cycle, keep the switch
  in the package that already has those imports — check where `ToWire` lives — and expose from there.
  Verify before implementing.)
- [ ] **Step 4:** `go test ./definition/model/... -race`. Expected: PASS. Refactor the 4 `ToWire`
  snippets (activity.go:221/246, event.go:224/266) to call `PutValidation` (R8).
- [ ] **Step 5:** lint clean. Commit:
  `refactor(definition): ValidationStrategyFor resolver + PutValidation wire helper (dedup)`

### Task 7: Runtime pre-Step validation — driver Gate + hook

**Files:**
- Modify: `runtime/processdriver.go` (add `gate *validation.Gate`, init in `NewProcessDriver`, a
  `validateInput` helper, and call it before `engine.Step` in `deliverLoop`).
- Create: `runtime/processdriver_validation_test.go` (start + message + completion, incl. nested).

**Interfaces:**
- Consumes: `engine.TargetNode`, `model.ValidationStrategyFor`, `validation.Gate.Validate(ctx, key,
  strat, input) error`, `validation.ErrInvalidInput`.
- Produces: `func (d *ProcessDriver) validateInput(ctx, def, st, trg) error`; `keyFor(def, node) string`
  (struct-key builder, no colon-join — R4); `inputOf(trg) map[string]any`.

- [ ] **Step 1: Failing tests.** Table (assert-closure): (a) start vars rejected when
  `StartEvent.InputValidation` fails → `errors.Is(_, validation.ErrInvalidInput)` and `store.Load`
  → `ErrInstanceNotFound` (no creation); (b) message payload to a ReceiveTask **nested in a
  subprocess** rejected + no advance (pre/post state equal); (c) completion output to a UserTask
  **nested in a subprocess** rejected + task stays open; (d) valid input in each case proceeds.
  Use `vexpr` (now `definition/model/validate/expr`).
- [ ] **Step 2:** Run the new test. Expected: FAIL (validation not wired yet).
- [ ] **Step 3: Implement.** In `NewProcessDriver`, `gate: validation.NewGate()`. Add:
```go
func (d *ProcessDriver) validateInput(ctx context.Context, def *model.ProcessDefinition, st engine.InstanceState, trg engine.Trigger) error {
    node, ok := engine.TargetNode(def, st, trg)
    if !ok { return nil }
    strat := model.ValidationStrategyFor(node)
    if strat == nil { return nil }
    return d.gate.Validate(ctx, keyFor(def, node), strat, inputOf(trg))
}
func keyFor(def *model.ProcessDefinition, n model.Node) string { /* struct key or hashed, NOT colon-join */ }
func inputOf(trg engine.Trigger) map[string]any {
    switch t := trg.(type) {
    case engine.StartInstance:  return t.Vars
    case engine.MessageReceived:return t.Payload
    case engine.HumanCompleted: return t.Output
    default:                    return nil
    }
}
```
  In `deliverLoop`, at the top of each iteration **before** `engine.Step` (processdriver.go:~419):
  `if err := d.validateInput(ctx, def, st, t); err != nil { return … fmt.Errorf("workflow-runtime: %w", err) }`.
  Because `TargetNode` returns false for re-entrant triggers (ActionCompleted, etc.), only the three
  external triggers validate, once, before commit. Confirm `keyFor` uses an unambiguous key (e.g. a
  `struct{DefID string; Version int; NodeID string}` map key inside the Gate, or `def.ID + "\x00" +
  … `) — coordinate with the Gate: if the Gate keys by string, hand it a collision-free encoding.
- [ ] **Step 4:** Run the tests → PASS. `go test ./runtime/... -race` (Docker up) — no regressions.
- [ ] **Step 5:** lint clean. Commit:
  `feat(runtime): validate external input pre-Step via engine.TargetNode (start/message/completion, nested-safe)`

### Task 8: Durable-reload registry reconciliation (former Task 7b) + fail-closed

**Files:**
- Modify: `definition/model/validate/registry.go` (`DefaultRegistry`, `Register`),
  `definition/model/definition.go` (`UnmarshalJSON` reconciliation), `definition/model/builder.go`
  (`build()` fallback) + tests.

**Interfaces:**
- Consumes: the pending-strategy reconstruction mechanism from Task 7 (`d1675ff`:
  `NodeSpec.ValidationGet/Set`, `pendingStrategy`, `reconcileNodeValidation`).
- Produces: `validate.DefaultRegistry() *Registry`, `validate.Register(kind string, f Factory)`;
  `UnmarshalJSON` reconciles pending descriptors against `DefaultRegistry()` **leniently**.

- [ ] **Step 1: Failing test.** Register the `expr` kind in `DefaultRegistry` (adapter `init` or test
  setup). Marshal a validation-bearing def to JSON, `json.Unmarshal` it back (the durable-store path),
  and assert the reconstructed node's strategy validates correctly (currently reloads as permanent
  pending → RED). Add a case: an **unregistered** kind stays pending and fail-closes at validation
  time (`errors.Is(_, ErrValidationNotReconstructed)`), not silently open.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3: Implement.** `UnmarshalJSON`, after populating pending descriptors, reconciles each
  against `DefaultRegistry()`: registered kind → real strategy; unregistered → stays pending. `build()`
  falls back to `DefaultRegistry()` when no explicit loader registry was set. Have each adapter register
  its kind via a package `init()` (or a documented `RegisterDefaults()`), so importing the adapter arms
  reload. Document the authoring=strict / reload=lenient asymmetry.
- [ ] **Step 4:** Run → PASS. `go test ./definition/... -race`.
- [ ] **Step 5:** lint clean. Commit:
  `feat(definition): reconcile validation descriptors against DefaultRegistry on durable reload (fail-closed)`

---

## PHASE D — Cleanups, guard, docs

### Task 9: R10 — reject `Version < 1`

**Files:** `definition/model/validate.go` (the definition validator, NOT the new package) + test.

- [ ] **Step 1: Failing test.** A def with `Version: 0` fails `Validate`/`Build` with a new
  `ErrInvalidVersion`; `Version: 1` passes. (Fix any existing test fixtures that use `Version: 0`.)
- [ ] **Step 2:** Run → FAIL (`undefined: ErrInvalidVersion` / 0 currently accepted).
- [ ] **Step 3: Implement.**
```go
var ErrInvalidVersion = errors.New("workflow-definition: version must be >= 1 (0 reserved as latest sentinel)")
// in validate():
if d.Version < 1 { errs = append(errs, fmt.Errorf("%w: got %d", ErrInvalidVersion, d.Version)) }
```
- [ ] **Step 4:** Run → PASS; `go test ./definition/... -race` (fix fixtures). Commit:
  `feat(definition): reject Version<1 in Build/Validate (0 reserved as latest sentinel)`

### Task 10: R9 adapter `Factory`→`New` + R7 testable Examples

**Files:** `definition/model/validate/jsonschema/jsonschema.go`,
`definition/model/validate/avro/avro.go`; new `Example*` tests beside the `With*Validation` options
(`definition/event/`, `definition/activity/`), mirroring `example_boundary_options_test.go`.

- [ ] **Step 1:** Change `Factory` in jsonschema + avro to `func Factory(schema string) (validate.ValidationStrategy, error) { return New(schema), nil }`. Existing round-trip tests cover it — run them (RED only if the bodies diverge; otherwise this is a dedup refactor, gate on existing tests green).
- [ ] **Step 2: Failing Examples.** Write `ExampleWithInputValidation`, `ExampleWithPayloadValidation`
  (event + activity), `ExampleWithCompletionValidation` with runnable `// Output:` blocks.
- [ ] **Step 3:** `go test ./definition/... -run Example`. Expected: PASS.
- [ ] **Step 4:** lint clean. Commit:
  `refactor(validation): Factory delegates to New; add testable Examples for With*Validation options`

### Task 11: Update the example to the new design + package paths

**Files:** `examples/scenarios/input_validation/main.go`.

- [ ] **Step 1:** Repoint imports to `definition/model/validate` / `runtime/validation`. Remove the
  `WithDefinitionResolver` wiring (completion now validates through the driver automatically). Keep
  the 4 checkpoints (rejected/accepted start; rejected/accepted completion). Add a nested-subprocess
  checkpoint if cheap.
- [ ] **Step 2:** `go run ./examples/scenarios/input_validation` → exit 0, expected log lines.
- [ ] **Step 3:** `go vet ./examples/...`; lint clean. Commit:
  `docs(examples): input_validation uses engine-decides path + new package layout`

### Task 12: ADRs

**Files:** `docs/adr/0110-*.md` (revise), `docs/adr/0115-validation-engine-decides.md` (new, Nygard),
`docs/adr/0111-*.md` + `docs/adr/0112-*.md` (import paths).

- [ ] **Step 1:** Revise ADR-0110: Status → superseded-in-part / amended; Decision now "engine decides
  (`TargetNode`), runtime executes (`Gate`)"; document the package split + fail-closed/durable-reload.
- [ ] **Step 2:** Write ADR-0115 (Nygard): the `engine.TargetNode` pure-query seam + the
  `definition/model/validate` ÷ `runtime/validation` layout and dependency direction.
- [ ] **Step 3:** Update ADR-0111/0112 import paths to `definition/model/validate/{jsonschema,avro}`.
- [ ] **Step 4:** Commit: `docs(adr): 0115 validation engine-decides seam; revise 0110; repath 0111/0112`

---

## Final — whole-branch review → merge
- [ ] `go build ./...`, `go test -race ./...` (Docker up), `golangci-lint run ./...` all clean.
- [ ] Whole-branch `/code-review` (golang-how-to lens + custom skills); address Critical/Important.
- [ ] `superpowers:finishing-a-development-branch` → `--no-ff` merge `feat/input-validation` → `main`;
  update `docs/plans/HANDOVER.md` + memory. Next free ADR after this item: 0116.

## Self-review notes
- Spec coverage: D1→T5/T6/T7; D2 (no command) honored by T7 pre-Step hook; D3→T4; D4→T1/T2/T3; D5→T9;
  D6→T10; D7→T8. Transport 400 mapping stays (import-repathed in T4).
- Type consistency: `TargetNode`, `ValidationStrategyFor`, `PutValidation`, `validateInput`, `keyFor`,
  `inputOf`, `ErrInvalidVersion`, `DefaultRegistry`/`Register` used identically across tasks.
- Open verification points flagged inline for implementers (import-cycle check in T6; exact arm field
  names in T5; Gate key encoding in T7).
