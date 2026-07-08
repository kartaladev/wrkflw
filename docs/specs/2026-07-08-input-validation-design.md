# Optional external-input validation — design

Date: 2026-07-08
Status: Approved (design) — **REVISED 2026-07-08 post-architecture-assessment** (see the Revision
section at the end); pending implementation plan
Scope: a new validation subsystem (neutral port + strategy + registry + 4 adapters) wired at three
external-input boundaries, plus definition/wire/YAML changes. New (ADR-gated, adapter-isolated)
dependencies.

> **⚠️ READ THE REVISION SECTION AT THE END FIRST.** An architecture assessment against `main`
> (`db12a21`) disproved several premises of the original body below (merge lives in the engine not
> the runtime hooks; `TaskService` has no definition; message node-resolution needs an engine query;
> `validation/expr` cannot import `internal/expreval`; `MarshalJSON` needs new fail-closed logic).
> The Revision section records the corrected design + the user's dependency/placement decisions. Where
> the original body and the Revision disagree, **the Revision wins.**

## Context

Three boundaries accept `map[string]any` arguments from external callers, and today the payload is
merged into instance variables **without any validation**:

| Boundary | Entry point | Merged by | Validate before |
|---|---|---|---|
| Process **start** | `ProcessDriver.Drive(ctx, def, id, vars)` (`processdriver.go:291`) | `handleStartInstance`→`mergeVars` | `engine.NewStartInstance` (`processdriver.go:308`) |
| Human-task **completion** | `TaskService.Complete(ctx, taskToken, actor, output)` (`runtime/task/service.go:147`) | `handleHumanCompleted`→`mergeVars` | trigger built (`service.go:158`), after authz |
| **Message** delivery | `ProcessDriver.DeliverMessage(ctx, def, name, key, payload)` (`processdriver_message.go:20`) | `handleMessageReceived`→`mergeVars` | `engine.NewMessageReceived` (`processdriver_message.go:25`) |

All three can reject **before any state mutation** — validation is a clean pre-check.

Requirements (user):
- Validation is **optional** and **resides in the definition** (the template declares the contract).
- **Flexible** mechanisms: JSON Schema, **Avro schema**, expr predicates, and a **custom Go callback**.
- The definition core must not lock the tech stack to a schema library (CLAUDE.md: deps are locked;
  new deps need an ADR). This mirrors the codebase's vendor-neutral seams (eventing→watermill,
  authz→casbin, cache→adapters, actions→catalog).

## Decision

A neutral **`Validator` port**, a definition-attached **`ValidationStrategy`** provider/factory
interface, and a **registry** that reconstructs declarative strategies from the serialized
definition. Concrete strategies live in **separate, opt-in adapter packages**; the engine/definition
core imports no schema library.

### The two interfaces

```go
// Validator is the runtime port: the executable check. A non-nil error rejects the
// operation before any state mutation. Lives in the module-root `validation` package.
type Validator interface {
	Validate(ctx context.Context, input map[string]any) error
}

// ValidationStrategy is attached to a node in the definition. It PROVIDES/CREATES the
// runtime Validator (a strategy may also implement Validator directly). Declarative
// strategies additionally describe themselves for serialization (see Descriptor).
type ValidationStrategy interface {
	// NewValidator builds the runtime validator (may compile a schema). Called once at
	// load / first use; the built Validator is cached and reused (Validate is the hot path).
	NewValidator() (Validator, error)
}

// DescribableStrategy is implemented by DECLARATIVE strategies (expr/json-schema/avro)
// so they round-trip through wire/YAML. The callback strategy does NOT implement it.
type DescribableStrategy interface {
	ValidationStrategy
	Descriptor() ValidationDescriptor // {Kind, Schema}
}

// ValidationDescriptor is the serialized form stored on a node's wire representation.
type ValidationDescriptor struct {
	Kind   string // "expr" | "json-schema" | "avro" (registry key)
	Schema string // the schema text / predicate list (adapter-interpreted)
}
```

### Node options (validation is NODE-LEVEL — see Design note on start)

```go
// StartEvent — validates the manually-provided start vars (Drive).
func event.WithInputValidation(s validation.ValidationStrategy) StartOption

// UserTask — validates the completion output.
func activity.WithCompletionValidation(s validation.ValidationStrategy) UserTaskOption

// ReceiveTask + IntermediateCatchEvent(message) — validates the message payload.
func activity.WithPayloadValidation(s validation.ValidationStrategy) ReceiveTaskOption
func event.WithPayloadValidation(s validation.ValidationStrategy) CatchOption
```

Each target node gains one validation slot appropriate to its boundary:
`StartEvent.InputValidation`, `UserTask.CompletionValidation`, `ReceiveTask.PayloadValidation`,
`IntermediateCatchEvent.PayloadValidation`.

### Adapter strategies (separate packages, opt-in)

| Package | Kind | Dependency | Serializable |
|---|---|---|---|
| `validation/expr` | `expr` | none — reuses `expr-lang` (existing) | yes |
| `validation/callback` | — (code-only) | none | **no** |
| `validation/jsonschema` | `json-schema` | new lib (ADR) | yes |
| `validation/avro` | `avro` | new lib (ADR) | yes |

- `validation/expr` — a Validator that requires all of a list of boolean `expr-lang` predicates to
  hold against the input (e.g. `["decision in ['approve','reject']", "amount > 0"]`). Zero new
  dependency; reuses the existing evaluator (`internal/expreval`) precedent used for gateway
  conditions. `Schema` is the newline/`;`-separated predicate list.
- `validation/callback` — wraps `func(ctx, map[string]any) error`. Not declarative, has no
  `Descriptor`; code-authored definitions only.
- `validation/jsonschema` — compiles a JSON Schema string into a Validator (checks the input map
  against the schema). New dependency, isolated in this package behind the port.
- `validation/avro` — validates the input map conforms to an Avro **record** schema (field
  presence/types). New dependency, isolated here.

### Registry & serialization round-trip

```go
// StrategyFactory rebuilds a declarative strategy from its serialized schema text.
type StrategyFactory func(schema string) (ValidationStrategy, error)

// Registry maps a descriptor Kind → factory. The Loader uses it to reconstruct strategies
// from a serialized definition, so nodes always hold a LIVE ValidationStrategy at runtime.
type Registry struct{ /* kind → StrategyFactory */ }
func (r *Registry) Register(kind string, f StrategyFactory)
func (r *Registry) Strategy(d ValidationDescriptor) (ValidationStrategy, error)
```

- **Code authoring:** `WithInputValidation(jsonschema.New(schemaStr))` etc. — the node holds the live
  strategy directly.
- **Serialize (MarshalJSON / YAML):** each node writes `validation: {kind, schema}` via the
  strategy's `Descriptor()`. **A node carrying a non-serializable `callback` strategy makes
  `MarshalJSON` return a descriptive error** (fail-closed — you cannot accidentally persist a
  definition and silently lose its validation). Consumers who persist must use a declarative
  strategy. (Considered alternative — lint-warn + silently omit — was rejected because silently
  dropping validation on reload is unsafe.)
- **Deserialize (Loader):** `NewLoader(WithValidatorRegistry(reg))`; for each node's descriptor the
  Loader calls `reg.Strategy(d)` to rebuild the live strategy. Registration is **explicit** (no
  `init()` magic): the consumer registers the adapters they use —
  `reg.Register("json-schema", jsonschema.Factory)`, `reg.Register("avro", avro.Factory)`,
  `reg.Register("expr", expr.Factory)` — matching the action-catalog explicit-wiring pattern. `expr`
  may be registered by default (no dep); `json-schema`/`avro` only when the consumer opts in.

### Injection at the three boundaries

Each boundary, before building its trigger:
1. Resolves the target node (start → the start event `Drive` enters; completion → the task's
   `UserTask`; message → the node the delivered message wakes, via the existing waiter lookup).
2. If the node has a validation strategy, obtains its (cached) `Validator` and calls
   `Validate(ctx, input)`.
3. On error, returns a wrapped `validation.ErrInvalidInput` (sentinel) **before any state mutation**;
   the transport layer maps it to HTTP 400.

The built `Validator` is cached per node (compiled once — `NewValidator` may be non-trivial for
schema kinds; `Validate` is the hot path).

## Components / files

- `validation/` (new module-root core pkg) — `Validator`, `ValidationStrategy`,
  `DescribableStrategy`, `ValidationDescriptor`, `Registry`, `ErrInvalidInput`. No third-party dep;
  depends only on `context` + stdlib, so `definition` may import it with no cycle (the port deals in
  `map[string]any`, not definition types).
- `validation/expr/`, `validation/callback/`, `validation/jsonschema/`, `validation/avro/` —
  adapter subpackages (each imports the `validation` port + its own lib).
- `definition/event/` — `StartEvent.InputValidation`, `IntermediateCatchEvent.PayloadValidation`,
  `WithInputValidation`, `WithPayloadValidation`.
- `definition/activity/` — `UserTask.CompletionValidation`, `ReceiveTask.PayloadValidation`,
  `WithCompletionValidation`, `WithPayloadValidation`.
- `definition/model/node_wire.go`, `yaml.go` — a `validation` descriptor field + each kind's
  `ToWire`/`FromWire`; `MarshalJSON` fail-closed on callback.
- `definition/` loader — `WithValidatorRegistry`, strategy reconstruction.
- `runtime/processdriver.go` (Drive), `runtime/task/service.go` (Complete),
  `runtime/processdriver_message.go` (DeliverMessage) — the three injection hooks.
- `transport/http/httpcore` — map `ErrInvalidInput` → 400.
- `examples/scenarios/input_validation/` — a def with json-schema start validation + a callback
  completion validation, showing a rejected and an accepted call.

## Error handling

- `validation.ErrInvalidInput` sentinel, wrapped with the failing detail (which field / which
  predicate / schema message). Returned before any state change.
- Both a callback strategy on a persisted (marshaled) definition → `MarshalJSON` error.
- Unknown descriptor `Kind` at load (adapter not registered) → Loader error naming the missing kind.

## Testing (TDD)

- **Core:** `Registry` register/resolve; descriptor round-trip; `MarshalJSON` fail-closed on callback.
- **Each adapter:** valid input passes, invalid rejects with a useful message; declarative adapters
  round-trip through YAML+wire (build → marshal → load via registry → validate identically).
- **Injection (all three points):** valid input proceeds; invalid input rejects with `ErrInvalidInput`
  and leaves **no state mutation** (assert the instance was not created / trigger not applied).
- **Message injection** resolves the waking node's payload strategy correctly.
- **Example** runs (one rejected, one accepted path).

## Non-goals

- Not validating internal state transitions or gateway routing — only the three external-input
  boundaries.
- Does not replace structural definition validation (`definition/model/validate.go`) or the lint
  advisories — this is runtime *data* validation, complementary.
- No cross-field validation beyond what a chosen schema/predicate expresses.

## Design note — why start validation is on the StartEvent node (not the definition)

Start validation is node-level for forward-compatibility with **multiple start triggers**:

| Start trigger | Input source | Validation |
|---|---|---|
| Manual (`Drive` vars) | caller `vars` | `WithInputValidation` on the "none" start event |
| Message start event | message payload | reuses `WithPayloadValidation` (identical to a ReceiveTask) |
| Timer start event | no external input | none |

A definition-level slot would conflate these different input contracts. Node-level keeps the rule
"the node that receives external input owns its input contract," matches the other two boundaries,
and is ready for the future start-by-timer / start-by-event work without rework. `Drive` resolves
the start event it enters and validates against that node's strategy.

## Dependencies & ADRs

- **ADR-0110** — the validation architecture (port + strategy + registry + adapter model).
- **ADR-0111** — adopt the JSON Schema library (behind `validation/jsonschema`).
- **ADR-0112** — adopt the Avro library (behind `validation/avro`).

(ADR numbers pre-allocated; `ReverseInstance` holds 0109. `expr`/`callback` adapters add no dep.)

## Parallelism note

Independent of the `ReverseInstance` feature (`docs/specs/2026-07-08-reverse-instance-design.md`) —
different code paths, no shared files of consequence. The two can be built as parallel
spec → plan → implementation cycles in separate sessions.

---

## Revision 2026-07-08 (post-architecture-assessment) — AUTHORITATIVE

An architecture assessment against `main` (`db12a21`) corrected the original body. This section is
authoritative where it conflicts.

### Deliberated placement principle — "the input-owner validates" (+ one shared Gate)

Validation placement follows one rule: **each external input is validated by the component that is
its external entry point.**

| Input | Enters through | Validated in | Rationale |
|---|---|---|---|
| Start `vars` | `ProcessDriver` | `Drive` | driver owns `def` + vars |
| Message `payload` | `ProcessDriver` | `DeliverMessage` | driver owns `def` + payload |
| Completion `output` | `TaskService` | `Complete` | **beside authz** — both are admission control on the human's submission |

Completion validation lives in `TaskService.Complete` (NOT the generic `ProcessDriver.ApplyTrigger`)
because `Complete` is already the human-task policy boundary: it runs authz there, and "is this actor
allowed?" + "is this output valid?" are the same kind of gate on the same submission — cohesive, and
fail-fast (rejects at `Complete` before a trigger exists, rather than succeeding then failing later
at apply).

**Shared mechanism (DRY, uniform, idiomatic):** all three sites delegate to ONE memoizing
`validation.Gate` that builds+caches the compiled `Validator` per node and wraps failures in
`validation.ErrInvalidInput`. Definitions stay immutable value types — the *executor* owns the
compiled-artifact cache (mirrors `internal/expreval` caching compiled programs, not the definition).

```go
// package validation — the executor-side memoizer shared by driver + task service.
type Gate struct{ /* mu; built map[string]Validator */ }
func NewGate() *Gate
// key uniquely identifies the node's strategy (e.g. "defID:version:nodeID"); s built once per key.
func (g *Gate) Validate(ctx context.Context, key string, s ValidationStrategy, input map[string]any) error
```

**Flexibility knobs:** (1) a narrow consumer-defined `DefinitionResolver` interface
(`Lookup(ctx, Qualifier)`), structurally satisfied by `kernel.DefinitionRegistry`, injected via
`WithDefinitionResolver` (accept-interfaces); (2) any custom `ValidationStrategy`/`Validator` plugs in
— the four adapters are batteries-included, not the only path; (3) every slot optional, but
**fail-closed when a slot is declared** so validation is never silently skipped.

### Where the merge/validation actually happens

All three `mergeVars` calls are in the **engine** (`engine/step_triggers.go`: start `:20`, completion
`:450`, message `:674/686/696/711`), reached only after the runtime calls `engine.Step`. So each
injection is a **pre-`engine.Step` gate in the runtime that resolves the target node itself** — the
engine performs no per-hook validation.

### Corrected injection points

1. **Start — `runtime/processdriver.go` `Drive`** (current `:293`; gate between the `InstanceState`
   build `:308` and `deliverLoop`/`NewStartInstance` `:309`). `handleStartInstance` enforces exactly
   one start, so resolve the node as `def.StartNodes()[0]` and type-assert to `event.StartEvent` to
   read `InputValidation`. Clean; matches the original design.
2. **Completion — validate inside `TaskService.Complete`** (`runtime/task/service.go:149`), via an
   **injected `DefinitionResolver`** (user decision). Reuse the existing
   `kernel.DefinitionRegistry` interface (`Lookup(ctx, model.Qualifier) (*model.ProcessDefinition,
   error)`) — do NOT invent a new interface. Wiring:
   - Add the definition **Qualifier** to the `humantask.HumanTask` record: `DefID string` +
     `DefVersion int` (resolve via `model.Version(DefID, DefVersion)` / `model.Latest(DefID)` when
     version 0). Populate it in the runtime when the `AwaitHuman` command creates the task (the
     runtime is driving `def` at that point). **Persist it** — the durable SQL `humantask.TaskStore`
     (3-dialect, ADR-0098) needs two new columns → a new migration for Postgres/MySQL/SQLite; the
     schema-parity guardrail must still pass.
   - Add `WithDefinitionResolver(kernel.DefinitionRegistry) TaskServiceOption` (optional). In
     `Complete`, after authz, resolve `def := resolver.Lookup(ctx, q)`, `node := def.Node(task.NodeID)`,
     type-assert `activity.UserTask`, and `Validate(ctx, output)` **before** returning the trigger.
   - Backward-compat: if no resolver is wired, completion validation is not enforced (opt-in). But if
     a node HAS a `CompletionValidation` and the resolver is present, a resolve/assert failure is an
     error (fail-closed), not a silent skip.
3. **Message — `runtime/processdriver_message.go` `DeliverMessage`** (`:20`; gate between
   `NewMessageReceived` `:25` and `ApplyTrigger` `:26`). The runtime `msgWaiters` map is
   `(name,key)→instanceID` with **no node identity**, and the winning node is chosen by the engine's
   private 4-tier dispatch priority in `handleMessageReceived`. Only **tier 4** (standalone
   `ReceiveTask` / message `IntermediateCatchEvent`) carries a `PayloadValidation` slot. Resolution:
   add an **exported engine query** `func (s *engine.InstanceState) MessageTargetNode(name, key
   string) (nodeID string, ok bool)` mirroring the tier priority; `DeliverMessage` loads the instance
   state (one read; delivery is not ultra-hot), resolves the node, and if it is a
   ReceiveTask/IntermediateCatchEvent with `PayloadValidation`, validates the payload. Messages that
   wake tiers 1–3 (event-gateway arm / boundary / event-subprocess) have no validation slot →
   **skip** (in-scope non-goal). A delivery wakes at most one node in one instance (no broadcast).

### `validation/expr` adapter

Cannot import `internal/expreval` (internal-package rule; `validation/` is a public root package). It
**imports `github.com/expr-lang/expr` directly** (allowed adapter boundary, as `action/httpcall`
does) and follows expreval's *pattern* (compile-cache, `AllowUndefinedVariables`). NOTE: unlike
`expreval.EvalBool` (which maps missing-vars → `false`, gateway semantics), the validation adapter
must treat a predicate that errors/references a missing field as a **validation failure**, not
silently `false`.

### `MarshalJSON` fail-closed

`ProcessDefinition.MarshalJSON` (`definition/model/node_wire.go:115`) loops nodes calling `toWire`
(no error return today). Implement the callback fail-closed **inside that loop**: inspect each node's
strategy; if a node carries a non-`DescribableStrategy` (the callback strategy), return a descriptive
`MarshalJSON` error. Do NOT thread an error through every kind's `ToWire`. (The existing
`BoundaryEvent.ErrorCheck` closure is *silently omitted* on marshal — that precedent is the WRONG
behaviour here; validation must hard-fail.)

### Dependency decisions (user)

- **JSON Schema (ADR-0111)** — validator: **`github.com/santhosh-tekuri/jsonschema/v6`**. Authoring:
  the adapter exposes `New(jsonText string)`, `NewFromValue(map[string]any)` (assemble the schema as a
  Go map programmatically), **and** a struct-reflection path using
  **`github.com/invopop/jsonschema`** to derive a schema from a Go type
  (`NewFromStruct`/reflector). Both deps are recorded in ADR-0111 (json-schema adapter stack) and are
  isolated inside `validation/jsonschema`. The descriptor serializes to canonical JSON text for
  wire/YAML round-trip regardless of how the schema was authored.
- **Avro (ADR-0112)** — **`github.com/linkedin/goavro/v2`**. Validate by parsing the `.avsc` record
  schema and attempting a native→binary encode of the input map; a non-nil error means the input does
  not conform. Isolated inside `validation/avro`.
- **Scope:** all four adapters (`expr`, `callback`, `jsonschema`, `avro`) in this item.

### ADR allocation for this item

- **ADR-0110** — validation architecture (port + strategy + registry + adapter model + 3 injection
  points + fail-closed marshal). Also records the `HumanTask` Qualifier + `MessageTargetNode` engine
  additions as consequences.
- **ADR-0111** — JSON Schema adapter stack: `santhosh-tekuri/jsonschema/v6` (validator) +
  `invopop/jsonschema` (struct-reflection schema generation).
- **ADR-0112** — Avro adapter: `linkedin/goavro/v2`.

(No extra ADR number consumed for `invopop` — it is part of the ADR-0111 json-schema decision. Next
free ADR after this item remains 0115, per the roadmap.)

### Files added/changed vs. the original list (deltas)

- `humantask/humantask.go` — `HumanTask.DefID`/`DefVersion` (+ the durable SQL TaskStore columns &
  3-dialect migration & parity guardrail).
- `runtime/task/service.go` — `WithDefinitionResolver`; resolver-based completion validation.
- `engine/state.go` — exported `MessageTargetNode(name, key)` query.
- `runtime/processdriver_message.go` — load state + resolve node + validate.
- `runtime` `AwaitHuman` task-creation site — populate the Qualifier on the created `HumanTask`.
- `validation/jsonschema` — depends on BOTH `santhosh-tekuri/jsonschema/v6` and `invopop/jsonschema`.
