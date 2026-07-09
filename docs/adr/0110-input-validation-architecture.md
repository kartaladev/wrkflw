# 110. Input validation architecture

- Status: Amended (2026-07-09) — see below and ADR-0115
- Date: 2026-07-08

## Context

The engine has three boundaries where a caller-supplied `map[string]any` is merged directly
into process-instance variables with no validation at all:

1. **Start vars** — `ProcessDriver.Drive` merges the caller's `vars` into the new instance's
   variables when it starts a process.
2. **Human-task completion output** — `TaskService.Complete` merges the acting user's `output`
   into the instance variables when a `UserTask` completes.
3. **Message payload** — `ProcessDriver.DeliverMessage` merges a delivered message's `payload`
   into the instance variables when it wakes a waiting `ReceiveTask` / `IntermediateCatchEvent`.

Consumers repeatedly asked for a way to reject malformed input at each of these boundaries
*before* it reaches instance state, declared on the process definition itself rather than
re-implemented ad hoc by every consumer. The validation need is heterogeneous across
consumers: some want a handful of boolean predicates (`amount > 0`), some want a full JSON
Schema, some want an Avro record schema (already the wire contract for an external message
producer), and some want to call out to arbitrary Go code (a lookup, a remote check) that
cannot be serialized at all. The engine core must not be locked to any one of these — per
`CLAUDE.md`'s "transports/vendors are library-provided, not baked into the core" principle,
core packages (`definition`, `engine`) must not import a schema library directly.

## Decision

We introduce a neutral `validation` port package, opt-in adapter subpackages, node-level
declaration slots, and validation at exactly the boundary that owns each kind of external input.

**Port (`validation` package):**

- `Validator` — the executable check: `Validate(ctx, map[string]any) error`.
- `ValidationStrategy` — attached to a definition node; `NewValidator()` builds (and may
  compile) the runtime `Validator` once.
- `DescribableStrategy` — implemented by *declarative* strategies (expr / JSON Schema / Avro)
  so they can round-trip through the wire/YAML form via `Descriptor() ValidationDescriptor`. The
  callback adapter (arbitrary Go closures) intentionally does **not** implement it — it is a
  code-only escape hatch that never persists.
- `ValidationDescriptor{Kind, Schema}` — the serialized `{kind, schema}` pair stored on a node's
  wire representation.
- `Registry` — maps a descriptor `Kind` string to a `StrategyFactory` that rebuilds a live
  strategy from its serialized schema text. Adapters register themselves explicitly (no `init()`
  magic), mirroring the existing action-catalog registration pattern.
- `Gate` — an executor-side, mutex-guarded memoizing cache (`map[key]Validator`) shared by the
  `ProcessDriver` and `TaskService`. `NewValidator()` runs once per `key` (typically
  `defID:defVersion:nodeID`); every `Gate.Validate` call wraps a validator failure in the
  sentinel `validation.ErrInvalidInput` so callers (and the transport 400 mapper) can test with
  `errors.Is`.

**Adapters** — each an isolated, opt-in subpackage the core never imports:

- `validation/expr` — `expr-lang/expr` boolean predicates (`New(predicates ...string)`), all
  must hold. Does not reuse `internal/expreval` (whose `EvalBool` treats a missing variable as
  `false`, the wrong semantics for validation, which must distinguish "absent" from "false").
- `validation/callback` — a Go closure wrapped as a `ValidationStrategy`; non-serializable by
  design (code-only authoring).
- `validation/jsonschema` — JSON Schema (draft 2020-12) via `santhosh-tekuri/jsonschema/v6`,
  with `invopop/jsonschema` struct-reflection so a consumer can derive a schema from an existing
  Go decode-target struct instead of hand-writing one. See ADR-0111 for the library choice.
- `validation/avro` — Avro record-schema validation via `linkedin/goavro`, useful when the same
  schema already governs an external message producer's wire format. See ADR-0112.

**Placement — "the input-owner validates":** validation runs in whichever component accepts
the external input, not inside the engine core (`engine.Step` stays pure, no I/O, no schema
libraries):

- `ProcessDriver.Drive` validates the caller's start vars against the sole start event's
  `InputValidation` strategy (when the process has exactly one start node) **before any
  instance is created** — a rejection leaves the store untouched.
- `TaskService.Complete` validates completion output against the completing `UserTask`'s
  `CompletionValidation` strategy, **after authorization succeeds but before the completion
  trigger is issued**. This requires resolving the process definition that generated the task,
  so `TaskService` accepts an optional `DefinitionResolver` (a narrow, TaskService-local
  interface — `kernel.DefinitionRegistry` satisfies it structurally, no explicit adapter
  needed) via `WithDefinitionResolver`. Validation is opt-in: without a resolver wired,
  `Complete` never validates, even if the node carries a `CompletionValidation` slot.
  `TaskService` resolves the definition by `model.Qualifier{ID: task.DefID, Version:
  task.DefVersion}` — see the `HumanTask` Qualifier decision below.
- `ProcessDriver.DeliverMessage` validates a delivered message's payload against the woken
  node's `PayloadValidation` strategy (`ReceiveTask` / `IntermediateCatchEvent`) before applying
  the trigger. Resolving *which* node a message would wake required a new read-only engine
  query, `InstanceState.MessageTargetNode(name, correlationKey) (nodeID string, ok bool)`
  (`engine/state.go`), which mirrors — tier-for-tier and predicate-for-predicate — the same
  dispatch priority `handleMessageReceived` already uses (event-based-gateway arm → message
  boundary arm → event-subprocess arm → standalone parked token), so the node picked for
  validation is guaranteed to be the same node `ApplyTrigger` actually wakes.

**Node-level slots and type-safe options:** each node kind that owns one of the three input
boundaries gets a typed field plus a matching functional option: `StartEvent.InputValidation`
(`event.WithInputValidation`), `UserTask.CompletionValidation`
(`activity.WithCompletionValidation`), and `ReceiveTask`/`IntermediateCatchEvent`
`PayloadValidation` (`activity.WithPayloadValidation`, `event` catch-event equivalent). Each
option accepts a `validation.ValidationStrategy` directly — no untyped `any`, no reflection at
the option layer.

**Wire/YAML round-trip:** a node's validation slot serializes to the flat `NodeWire.Validation
*validation.ValidationDescriptor` field. `ProcessDefinition.MarshalJSON` **fails closed**: if any
node's strategy is neither a `DescribableStrategy` nor the wire-reconstruction placeholder
(`pendingStrategy`, see below), it returns `ErrUnserializableValidation` rather than silently
dropping the strategy or serializing a lossy stand-in — a callback strategy can only be
authored in Go and must never be persisted as if it were re-creatable. On decode,
`ProcessDefinition.UnmarshalJSON` reconstructs each node via its kind's `FromWire` spec; a node
whose wire form carries a `validation` descriptor gets a `PendingValidation` placeholder — a
strategy that is still `Describable` (so it round-trips again byte-for-byte even before
reconstruction) but whose `NewValidator()` always errors (`ErrValidationNotReconstructed`) until
resolved. `definition.NewBuilder(...).Build()` / `definition.NewLoader(...)`, when configured
with `definition.WithValidatorRegistry(reg *validation.Registry)` (a `LoaderOption`), walks every
node and replaces each pending placeholder with the live strategy the registry's factory
produces from the descriptor (`reconcileNodeValidation`, `definition/model/validation_wire.go`).
Without a configured registry, `Build` returns `ErrValidatorRegistryRequired` for any definition
still carrying a pending descriptor.

> **IMPORTANT — durable-reload reconstruction status.** The mechanism above (a
> `*validation.Registry` threaded through `WithValidatorRegistry` into `Build`) is the
> **implemented and committed** reconstruction path on this branch: it covers any caller that
> goes through `definition.NewBuilder`/`definition.NewLoader`. It does **not** cover a caller that
> calls `json.Unmarshal` directly against a `*model.ProcessDefinition` (e.g. a durable store
> reading a JSONB column outside the Loader) — that path currently reconstructs every validated
> node into an unusable `pendingStrategy` with no way to resolve it, because `UnmarshalJSON`
> itself has no registry to consult. The decided design for that gap is a process-global
> `validation.DefaultRegistry()` (a package-level singleton `*validation.Registry`, populated by
> adapter `init()`/explicit registration at process start) that `ProcessDefinition.UnmarshalJSON`
> consults directly, so a bare `json.Unmarshal` reload also reconstructs live strategies without
> requiring every caller to route through `Build`. **This piece is Task 7b and is PENDING as of
> this ADR's authoring — not yet implemented or committed on this branch.** Until it lands,
> any consumer that decodes a `*model.ProcessDefinition` via raw `json.Unmarshal` (bypassing the
> Loader) will get non-functional pending placeholders for any validated node; the safe path
> today is always to route decode through `definition.NewLoader(...)` +
> `WithValidatorRegistry`.

**`HumanTask` Qualifier:** `humantask.HumanTask` gains two write-once fields, `DefID` and
`DefVersion` (+ a 3-dialect migration for the SQL-backed `HumanTaskStore`), populated at task
creation from the definition that produced the task. This is what lets `TaskService.Complete`
resolve the completing node's `CompletionValidation` strategy via a `DefinitionResolver` without
requiring the caller to separately pass the definition on every `Complete` call. The two columns
are write-once by construction: the engine's own task-update lifecycle path (claim / reassign /
complete, `engine/step_nodes.go`) rebuilds an `UpdateTask` command from a task skeleton that does
not carry `DefID`/`DefVersion`, so both the SQL dialects' `UpsertTask()` conflict-update SET
clause and the in-memory `MemTaskStore.Upsert` deliberately preserve the original values across
every re-upsert rather than let a later zeroed re-upsert clobber them.

## Consequences

- The `definition`/engine core imports no schema or validation library; `validation` itself
  imports only stdlib. Consumers explicitly opt in to whichever adapter(s) they need
  (`validation/expr`, `validation/callback`, `validation/jsonschema`, `validation/avro`) and
  register declarative ones with a `*validation.Registry` for wire/YAML reconstruction.
- `humantask.HumanTaskStore` gains two new persisted columns (`def_id`, `def_version`) across
  all three SQL dialects (Postgres/MySQL/SQLite), plus the equivalent write-once preservation
  logic in the in-memory `MemTaskStore` fake used by tests and reference examples.
- Message-payload validation covers only "tier-4" standalone waiters — a `ReceiveTask` or
  `IntermediateCatchEvent` token parked directly on that node. Gateway-arm, boundary-arm, and
  event-subprocess-arm message wakes (tiers 1–3) are unvalidated **by design**: those nodes have
  no `PayloadValidation` slot today, since the semantics of "which node's schema applies" are
  less obvious when a gateway or boundary event is what actually consumes the message.
- `ProcessDriver.DeliverMessage` now performs two `driver.store.Load` calls for a validated
  message delivery: one to resolve `MessageTargetNode` for validation, and a second (inside
  `ApplyTrigger`) to actually apply the trigger. This is a known, accepted inefficiency — a
  future hot-path-caching item (candidate: thread the already-loaded state into `ApplyTrigger`,
  or cache `InstanceState` briefly) rather than a correctness problem, since instance state
  between the two loads is not expected to change within one `DeliverMessage` call.
  `validation.Gate`'s own compiled-validator cache is keyed `defID:defVersion:nodeID`, so
  repeated deliveries against the same node never recompile the strategy regardless of the
  double-load.
- Task 7b (a process-global `validation.DefaultRegistry()` consulted directly by
  `ProcessDefinition.UnmarshalJSON`) remains open work; until it lands, decoding a definition via
  raw `json.Unmarshal` outside `definition.NewLoader`/`WithValidatorRegistry` yields
  non-functional `pendingStrategy` placeholders for any validated node. This ADR records the
  decided design for that gap so it is not re-litigated when Task 7b is picked up.
- See ADR-0111 (JSON Schema adapter library choice: `santhosh-tekuri/jsonschema/v6` +
  `invopop/jsonschema`) and ADR-0112 (Avro adapter library choice: `linkedin/goavro`) for the
  per-adapter third-party dependency decisions this ADR delegates to.

## Revision 2026-07-09 — engine decides, runtime executes

The "input-owner validates" placement above shipped, then a whole-branch review plus a user
design review found it structurally wrong, on two counts:

- **Fail-open on nested nodes.** Each boundary resolved its target node with the flat
  `(*ProcessDefinition).Node(id)` lookup — `def.Node`, or the boundary-local
  `MessageTargetNode`/completion-token lookups built on it — which scans only the top-level
  `Nodes` slice. A `UserTask` / `ReceiveTask` / `IntermediateCatchEvent` nested inside a
  sub-process was never found, so its validation was silently skipped: the opposite of the
  fail-closed guarantee this feature exists to provide.
- **Scope-derivation smells forced onto the wrong types.** Scope-correct node resolution
  already lives in the engine (`defForScope`, reading `InstanceState`'s private scope/token/arm
  bookkeeping); the boundary components (`ProcessDriver`, `TaskService`) had no clean way to get
  at it. The two remedies both smelled: growing `InstanceState` an ad-hoc
  `MessageValidationNode` method, or persisting a scope path onto `humantask.HumanTask` (meant
  to stay a clean task bucket) — this branch had in fact already done the latter, adding
  `HumanTask.DefID`/`DefVersion` plus a 3-dialect migration solely so `TaskService.Complete`
  could resolve the completing node's definition.

**New design.** The engine exposes a pure, validation-agnostic scope-aware resolver,
`engine.TargetNode(def *model.ProcessDefinition, st InstanceState, trg Trigger) (model.Node,
bool)` (`engine/target_node.go`). It mirrors `Step`'s own trigger dispatch tier-for-tier —
`StartInstance` → the sole start node; `MessageReceived` → the same 4-tier priority
`handleMessageReceived` uses (event-gateway arm → boundary arm → event-subprocess arm →
standalone parked token), each resolved against the *scope-owning* `ProcessDefinition` via
`defForScope`; `HumanCompleted` → the parked task token's node, likewise resolved in its own
scope — so the query and `Step` dispatch can never disagree on which node wins, including one
nested arbitrarily deep in a sub-process.

The runtime composes the resolver with strategy extraction and execution, before `Step` ever
runs:

```go
func (driver *ProcessDriver) validateInput(ctx context.Context, def *model.ProcessDefinition, st engine.InstanceState, trg engine.Trigger) error {
	node, ok := engine.TargetNode(def, st, trg)
	if !ok {
		return nil
	}
	strat := model.ValidationStrategyFor(node)
	if strat == nil {
		return nil
	}
	return driver.gate.Validate(ctx, keyFor(def, node), strat, inputOf(trg))
}
```

`deliverLoop` (`runtime/processdriver.go`) calls `validateInput` at the top of its per-trigger
loop, before the store `Commit`/`Create` — so a rejection returns before any state is
persisted, on all three input-bearing trigger kinds it processes: `Drive` builds the
`StartInstance` trigger and enters `deliverLoop` directly (start vars validate here); message
payload and completion output validate when their trigger (`MessageReceived` /
`HumanCompleted`) reaches `deliverLoop` via `ApplyTrigger`. `model.ValidationStrategyFor`
(`definition/model/validation_wire.go`) extracts the strategy through the registered
`NodeSpec.ValidationGet` rather than a type switch, so `model` never needs to know about the
leaf node packages' concrete types.

Validator *execution* stays entirely out of the pure `Step`: no `Gate` is threaded into
`StepOptions`, and no validator runs in the engine core. This preserves `Step`'s determinism —
a validated external trigger is validated once, before it is ever handed to `Step`, and is
never replayed — the property the original design's boundary-injection placement was, in
effect, also trying to preserve, just from the wrong components and with a lookup that could
silently miss a nested node.

This reverts, rather than layers onto, several pieces of the original decision:
`TaskService.Complete` no longer validates — it goes back to authz-then-emit-trigger only, and
`WithDefinitionResolver` is gone. `humantask.HumanTask.DefID`/`DefVersion` and their migration
are reverted: the token's own live scope (available to the engine at completion time) supplies
node resolution, so the task bucket no longer needs to carry definition identity for this
feature's sake. `MessageTargetNode` (the bare-nodeID, scope-blind precursor) is superseded by
`engine.TargetNode`.

**Package segregation.** The validation port, adapters, and reconstruction registry moved from
the original single `validation` package into `definition/model/validate` (package `validate`)
— colocated with the node model + wire descriptors it decorates, importing nothing from
`runtime`/`engine`. The executor — the `Gate` and `ErrInvalidInput` — moved into
`runtime/validation` (package `validation`), which imports `definition/model/validate` to
consume its `ValidationStrategy`/`Validator` types. See ADR-0115 for the full dependency-
direction rationale.

**Durable reload stays as designed, now correctly located.** `ProcessDefinition.UnmarshalJSON`
still reconciles any pending validation descriptor against a process-global
`validate.DefaultRegistry()` (`definition/model/validate/registry.go`) — now **leniently**: an
unregistered kind leaves the node's slot pending rather than erroring the whole decode, so it
fails closed at validation time (`ErrValidationNotReconstructed`) instead of failing the reload.
`definitionCore.build()` (`definition/model/builder.go`) falls back to the same
`DefaultRegistry()` when no explicit `WithValidatorRegistry` is configured, but **strictly** — an
unregistered kind fails `Build` with `ErrUnknownKind`, giving early authoring-time feedback
instead of a silently pending node. Adapters (`validate/expr`, `validate/jsonschema`,
`validate/avro`) self-register into `DefaultRegistry()` via `init()`, so importing an adapter
package is what arms both paths for its kind.

See `docs/specs/2026-07-09-input-validation-redesign.md` for the full design record (including
the revert list with commit references) and ADR-0115 for the `TargetNode`/package-layout
decision this revision delegates to.
