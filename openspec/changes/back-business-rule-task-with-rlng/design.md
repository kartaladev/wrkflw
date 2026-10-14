# Design

## Context

See proposal.md (Why, and the re-measurement table). This section records only the state that shapes the approach. Everything below was measured on `main` at `eebe55ba` unless it says otherwise.

- **What the reservation consists of.** `RuleSpec` (`definition/model/rulespec.go`) is the wire carrier. Both codecs and `Validate` run one shape predicate, so `ErrInvalidRule` is raised the same way at decode and at validate. `Validate` refuses every well-shaped rule. If an action is also set it refuses with `ErrRuleAndAction`. Otherwise it refuses with `ErrRuleNotSupported` (`validate.go:1199-1222`). The engine maps `KindBusinessRuleTask` to `serviceTaskStrategy{}` (`engine/step_dispatch.go:44`), which emits one `InvokeAction` named by `mainActionName` (the action, or the node id).
- **The action path is the template.** A token parks on `InvokeAction`. After commit the runtime resolves the name (scoped catalog first, then global) and performs it. It feeds back `ActionCompleted` (output merged into variables) or `ActionFailed` (carrying a retryable flag, a message and an in-memory cause). Retry policy, error boundaries, incidents and the trigger journal all hang off those two triggers. An unknown action becomes a non-retryable `ActionFailed` with the message "unknown action: <name>" and no sentinel (`runtime/processdriver_action.go:374-389`).
- **No authoring-time catalog check exists.** `ProcessDefinition.CancelActions` documents that action-name existence is not validated, because "the catalog is not available at validate time". #118's "the service-task catalog check, reused" therefore has nothing to reuse (stale premise).
- **Precedent for a registry consulted at Build.** Declarative validation kinds are reconstructed at `Build` and at JSON decode from a `validate.Registry`. The registry is passed with `WithValidatorRegistry` or else defaults to the process-global one. `Build` fails with `validate.ErrUnknownKind` when no adapter is registered (`definition-validation` spec, *Declarative validation kinds reconstruct, unregistered kinds fail Build*).
- **rlng at `a6084234`** (untagged; pre-`v0.0.1`). It is pure Go on `expr-lang/expr v1.17.8`, the same version as wrkflw's `go.mod:9`. It also requires `shopspring/decimal v1.4.0` and `go-viper/mapstructure/v2 v2.5.0`. Pipelines are built from a YAML/JSON config or in Go, and evaluate to a context projected through a mapping. Decimal values carry exact precision. `Hash()` gives a deterministic content identity.
- **Immutability of published versions** (kartaladev/wrkflw#111, decided). A published `(id, version)` never changes content, so a compiled artifact keyed by `(id, version)` cannot go stale.

## Goals / Non-Goals

**Goals:**
- A rule-bearing businessRuleTask evaluates a rule with the same failure, retry, boundary, journal and recovery semantics an action has, and no new trigger type.
- The root module stays free of rlng. The rule engine reaches only consumers who import the adapter module.
- No rule-bearing node ever silently runs as a service task.

**Non-Goals:**
- Teaching wrkflw to read rlng documents. wrkflw hands `RuleSpec.Inline` to the registered compiler verbatim and never parses it, as it does today.
- A rule-set store or a rule versioning scheme. Rules are versioned by the definition that embeds them (inline), or by the consumer's catalog (named).
- Replaying rlng's explanation trail into wrkflw's journal.

## Decisions

### D1. The port lives in the root module; the adapter is a nested module (pending OQ1 for paths)
The root gains a dependency-free package (recommended name `rule`) with three parts. **`Catalog`** resolves a name to an evaluator, mirroring `action.Catalog`. **`Compiler`** compiles an inline document into an evaluator. **`Evaluator`** takes variables and a context and returns output and an error. The package also exports the sentinels `ErrUnknownRule` and `ErrRuleCompile`. The nested module implements `Compiler` and `Catalog` over rlng and offers `Action(pipeline) action.Action`.
- *Rejected: rlng-backed only through `action.Action` (#27's round-2 option a).* #27's round 3 decided a `rule` field. With an action-only adapter, the field would mean nothing.
- *Rejected: import rlng from the root.* That contradicts #30's acceptance test ("a consumer's non-test module graph contains exactly what their own imports name"). It would also put `shopspring/decimal` and `mapstructure` into every consumer's graph (measured, rlng `go.mod`).

### D2. Evaluation travels the action command path
The engine's businessRuleTask strategy grows one branch. A node with a rule still emits an `InvokeAction` and parks the token on its command id. The command gains one additive discriminator that marks it as a rule evaluation for that node. The runtime resolves the evaluator from the definition and node, never from a name carried on the command, and performs it where it would perform an action. It feeds back `ActionCompleted` or `ActionFailed`. So retry policy, boundaries, incidents, the trigger journal and restart recovery need no new code paths, and the spec scenarios for duplicate completion and restart hold by construction.
- *Rejected: evaluate synchronously inside `engine.Step`.* The engine is pure and has no catalog. Evaluating inside `Step` would also break the "commands are performed after commit" invariant that recovery relies on.
- *Rejected: a new `EvaluateRule` command and `RuleEvaluated` trigger.* That adds a trigger type to the journal codec, which is a persisted-format change, and duplicates every action-path behavior.
- *Persisted-format note.* If `persist-pending-commands` has landed, its pending-command payload gains the discriminator as an additive, optional field. A row written before this change lacks it and decodes as an action. That is correct, because before this change no rule-bearing node could exist (`ErrRuleNotSupported`).

### D3. Compile once per `(definition id, version)`
Inline rules are compiled when a definition is built or decoded with a compiler available (OQ2). The result is held beside the definition in memory, keyed by `(id, version)`, and is never serialized. Concurrent first use compiles once, using a per-key once. A different version compiles separately. #111 is what makes the key sound.
- *Rejected: compile per evaluation.* rlng's design is "compiled once, evaluated on the hot path".
- *Rejected: key by rlng's `Hash()`.* That saves work only when two versions embed byte-identical rules. It also couples the cache key to an untagged API.

### D4. Evaluation time is bounded by the action timeout policy
The runtime applies the effective action timeout to an evaluation's context, exactly as for an action. Whether rlng honors context cancellation mid-evaluation is unmeasured until `v0.0.1`. If it does not, the timeout bounds only the wait, not the CPU (see Risks).

### D5. Output merging is the action merge
Output enters through `ActionCompleted`, so it is merged by the same key-by-key replacement as action output, and it inherits whatever `reserve-engine-variable-keys` decides for engine-originated maps (its Q3).

## Risks / Trade-offs

- **[rlng API moves before `v0.0.1`]** → Gate task 1.1 re-measures the API, the dependency set and the config model at the tag, and stops the change if OQ4's premise has moved.
- **[The `RuleSpec` shape freezes into persisted rows]** → Gate task 1.2. OQ4 is answered before `ErrRuleNotSupported` is deleted.
- **[Untrusted definitions now carry executable logic]** → The module is opt-in. Document size is bounded by `bound-untrusted-input`, which this change depends on. expr expressions have no I/O unless the consumer registers functions. CPU time per evaluation is bounded only if rlng honors context (D4). This is recorded in the module's godoc as a limit, not hidden.
- **[A YAML inline rule loses integer precision]** (documented today on `RuleSpec.Inline`) → It matters now, because rlng evaluates money exactly. OQ4.
- **[The compile cache grows with versions]** → One entry per published version that has inline rules, the same growth as the definition registry itself. Bounding it is not attempted.

## Cross-change coupling

- **`cut-v0-release` (Depends on).** This is the maintainer's post-release decision. The new module also joins the lockstep tag script that change ships.
- **`bound-untrusted-input` (Depends on).** It owns the whole-document ceiling (#184), which is what bounds an inline rule. Inline rules must not become executable before that ceiling exists. Both changes touch `rulespec.go`, and that change lands first.
- **`improve-wire-gate-diagnostics` (Depends on).** It owns the nested non-string key walk in `RuleSpec`'s YAML arm (#235 half 1, its D7). OQ4 revisits the same arm, so it lands first and this change revisits the final shape once.
- **`persist-pending-commands` (coupling, no dependency of its own; it lands before the release).** D2 adds an optional discriminator to its pending-command payload.
- **`reserve-engine-variable-keys` (coupling).** It owns whether merged output may write reserved keys. D5 inherits its answer.
- **`split-persistence-modules` (coupling).** It owns `go.work`, `scripts/modules.sh` and the module-graph guard test. This change adds a module to them, and adds rlng to the guard's deny list for the root.

## Migration Plan

No data migration. Definitions published before this change cannot carry a rule. Consumers who never write `rule` see no change. A consumer adopting rules imports the adapter module and registers it (OQ2). Rollback before any rule-bearing definition is published is a plain revert. After one is published, a rollback would re-introduce `ErrRuleNotSupported` against stored rows. That is why the shape gate (task 1.2) comes first.

## Open Questions

None of these is recorded on kartaladev/wrkflw#118, and #27's decision does not cover them. The specs carry the recommended option, marked *Pending*. Gate task 1.3 rewrites the affected requirements if the maintainer chooses otherwise.

- **OQ1. Module path and port package.** (a) Root package `rule` and module `rule/rlng`. **Recommended.** The port is not an action, and the module serves the rule field first. (b) Module `action/rlng`, as #118 suggests, with the port in `action`. That mixes two catalogs in one package. (c) Module `rlng` at the root of the repo.
- **OQ2. Refusal when no rule engine is configured.** (a) A compiler registry, process-global by default and passable as a loader option. This mirrors `WithValidatorRegistry`. `Build`, registration and `PublishDefinition` refuse a rule-bearing definition with a new `model.ErrNoRuleEngine` when the registry is empty, and compile inline rules there (`rule.ErrRuleCompile`). **Recommended.** It keeps "silence is never correct" at authoring time, and it follows the precedent for declarative validation. (b) Accept at authoring, and fail the token at evaluation. A definition would then be publishable that no driver can run. (c) Keep `ErrRuleNotSupported`, and delete it only when the module is imported. That is an exported sentinel whose meaning depends on imports.
- **OQ3. Rule-name checking.** (a) Check names at `Build` only when the loader is given a rule catalog, and always refuse an unresolved name at evaluation with `rule.ErrUnknownRule` (non-retryable). **Recommended.** (b) Runtime only, mirroring actions today (no sentinel). That leaves the failure unnamed. (c) Require a catalog at `Build`. That breaks the DB decode path, which has none.
- **OQ4. `RuleSpec.Inline` at rlng `v0.0.1`.** (a) Keep the shape. Refuse a YAML inline rule containing an integer literal that `float64` cannot represent exactly, with `ErrInvalidRule`. **Recommended.** Money rules are rlng's target, and the loss is silent today. (b) Keep the shape and the documented loss. (c) Revise the shape, for example to hold the YAML source. This is the last moment a revision is free (proposal, Persisted formats).
- **OQ5. Decimal output in process variables.** (a) Store the exact decimal string. **Recommended.** It is lossless, and rlng's own JSON form is a string. (b) Store a JSON number through `json.Number`. That is lossy once any consumer decodes it into `float64`. (c) Refuse decimal output.
- **OQ6. Retry classification of a failed evaluation.** (a) Non-retryable. **Recommended.** Evaluation is pure, so identical variables fail identically. (b) Classify through `action.IsRetryable` on the error, as for actions.
