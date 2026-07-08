# Optional external-input validation — design

Date: 2026-07-08
Status: Approved (design) — pending implementation plan
Scope: a new validation subsystem (neutral port + strategy + registry + 4 adapters) wired at three
external-input boundaries, plus definition/wire/YAML changes. Two new (ADR-gated, adapter-isolated)
dependencies.

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
