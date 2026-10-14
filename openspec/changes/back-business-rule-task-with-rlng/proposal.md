# Proposal

## Why

`businessRuleTask` is a `serviceTask` under another name. It invokes a catalog action and never evaluates a decision. kartaladev/wrkflw#27 (decided) kept the kind and reserved a `rule` field on it, so that the rule engine [kartaladev/rlng](https://github.com/kartaladev/rlng) could back the kind after the first release without a persisted-format change. kartaladev/wrkflw#118 records that follow-up, and this change absorbs it. Until it lands, `model.Validate` refuses every well-shaped rule with `ErrRuleNotSupported`, so the field round-trips through JSON, YAML and the golden file but no definition can be published with it.

**Post-release.** By maintainer decision, this change is planned in full now and applied only after the first tag, so it depends on `cut-v0-release`. It also has an external blocker that no change owns: rlng must cut `v0.0.1`. Measured 2026-10-14 with `gh api repos/kartaladev/rlng/tags`: rlng has no tags and no releases, its last push was 2026-07-14 (`a6084234`), and its README still says "pin to a commit until `v0.0.1` is cut; APIs may still change".

## What Changes

Premises from #118, re-measured on `main` (`eebe55ba`):

| #118 premise | Re-measured |
|---|---|
| `rule` decodes as `RuleSpec{Name string; Inline json.RawMessage}` from a string or an object | **Holds.** `definition/model/rulespec.go`. Both codecs and `Validate` share one shape predicate. `ErrInvalidRule` refuses any other JSON or YAML type. |
| Build refuses any non-empty `rule` with `ErrRuleNotSupported` | **Holds, with a nuance.** The refusal is in `model.Validate` (`validate.go:1221`), not in Build alone, so the DB publish path refuses too. The baseline specs deliberately do not record it, because it exists only until this change. |
| `ErrRuleAndAction` exists and should stay | **Holds.** `validate.go:486`, and the baseline `definition-validation` spec pins it. |
| "Build checks a named rule resolves when a rule catalog is present (**the service-task catalog check, reused**)" | **Stale.** No service-task catalog check exists. `ProcessDefinition.CancelActions` says "Action-name existence is not validated here (the catalog is not available at validate time)". An unknown action is discovered only when it is invoked: `runtime/processdriver_action.go:374-389` fails the token with a non-retryable `ActionFailed` whose message is "unknown action: <name>", and exports no sentinel. Whether Build checks rule names is therefore a new decision (OQ3). |
| The engine dispatch already shares `serviceTaskStrategy` | **Holds.** `engine/step_dispatch.go:44`. The duplicate strategy was collapsed by #27's implementation. |
| `doc.go` and `CONTEXT.md` describe `rule` as reserved | **Holds.** `doc.go:47-52` and `CONTEXT.md:40-45`. `ErrRuleNotSupported` is referenced in 11 files. |
| rlng adds only rlng itself, because `expr-lang/expr v1.17.8` is already in wrkflw's `go.mod` (#27 audit) | **Half stale.** expr is shared at the same version. But rlng's `go.mod` at `a6084234` also requires `github.com/shopspring/decimal v1.4.0` and `github.com/go-viper/mapstructure/v2 v2.5.0`. Neither is in wrkflw's `go.mod` today. |

Changes:

- **A business rule task with a `rule` evaluates the rule** (#118, absorbed). It invokes no action. Variables go in, and the decision output is merged into process variables exactly as an action's output is. A failed evaluation is an action failure, so retry policy, error boundaries and incidents apply unchanged. A business rule task without a `rule` keeps today's behavior: it invokes `action`, which defaults to the node id. Under the recommended option (D2), evaluation travels the action command path, so the journal gains no trigger type.
- **`ErrRuleNotSupported` is deleted** (#118). `Validate` accepts a well-shaped rule on a node with no action. `ErrRuleAndAction` and `ErrInvalidRule` stay unchanged. **BREAKING (small):** an exported sentinel is removed. A consumer that matched it gets a compile error, which is the intent.
- **The rule engine is an opt-in module** (#118, and the intent of kartaladev/wrkflw#30 (decided): "a consumer imports only what they use or need"). The root module gains only a dependency-free rule port: a rule catalog for `rule: <name>` that mirrors `action.Catalog`, plus a compiler for inline documents. A new nested module adapts rlng to that port. It also offers an rlng pipeline as an `action.Action` that works with `Typed` (kartaladev/wrkflw#117, shipped). The module path is OQ1.
- **A rule-bearing node is never silently a service task.** A definition whose rule cannot be compiled or resolved is refused with a named error. The point of refusal, and the sentinels, are decided in OQ2 and OQ3. The specs carry the recommended options, marked pending.
- **Inline documents are compiled once per definition version** and reused across instances. kartaladev/wrkflw#111 (decided) makes a published `(id, version)` immutable, which is what makes the cache key sound.
- **Docs.** The `doc.go` BPMN table row for `businessRuleTask` changes from "reserved" to what the kind does. So do `CONTEXT.md`'s **Rule** entry and the godoc on `RuleSpec`, `activity.WithRule`, `activity.WithInlineRule` and the dispatch comment. `doc_bpmn_test.go` keeps pinning that the kind is classified.

**Impact flags**

- **Persisted formats (flagged).** The wire shape of `rule` does not change. But this change is the moment the shape freezes. Today no published definition can carry a rule. Once this lands, stored `wrkflw_definitions.definition` rows will, on Postgres, MySQL and SQLite alike. Any later revision of `RuleSpec` would then need a migration. That is why #118 requires revisiting `RuleSpec.Inline` against rlng's `v0.0.1` config model before the refusal is deleted (gate task 1.2, OQ4). Rule output also lands in snapshots as process variables, so how a decimal is represented there is persisted (OQ5). No column or migration changes.
- **Default security posture (flagged): unchanged by default, widened on opt-in.** Without the module, nothing evaluates a rule, and a rule-bearing definition is refused rather than run (pending OQ2). With the module, a definition document carries logic that is evaluated in-process. No library HTTP route accepts a definition, but a consumer who publishes definitions from untrusted bytes now executes untrusted expressions. Bounding the document is `bound-untrusted-input`'s job, and this change depends on it. Bounding one evaluation's time is covered by the action timeout policy (design D4).
- **Consumer go.mod (flagged).** The root module's `go.mod` gains nothing. That is pinned by a module-graph requirement in the new capability. A consumer who imports the rlng module gains rlng, `shopspring/decimal` and `go-viper/mapstructure/v2` (as of `a6084234`; re-measured at rlng `v0.0.1`, task 1.1). The new module is versioned in lockstep with the others, per #30 (decided), call 2.

**Decisions this change cannot be fully specified without** (design, Open Questions; no comment on #118 records them; Readiness: human):
- **OQ1.** The module path and where the port lives. Recommended: a root package `rule` holding the port, and a nested module `rule/rlng`. #118 says "`action/rlng` or similar".
- **OQ2.** How a rule-bearing definition is refused when no rule engine is configured. Recommended: a registered compiler, as with declarative validation kinds, and `Build`/`Validate` refuse with a new `model.ErrNoRuleEngine`.
- **OQ3.** Whether a rule *name* is checked at Build. Recommended: when the loader is given a rule catalog, Build refuses an unresolved name with a new `rule.ErrUnknownRule`. At evaluation, an unresolved name always fails the token, non-retryably, with the same sentinel.
- **OQ4.** Whether `RuleSpec.Inline` keeps its shape and its documented YAML lossiness at rlng `v0.0.1`. Wide integers lose precision through the YAML arm, and rlng's value model is exact decimal. Recommended: keep the shape, and refuse a YAML inline rule whose integer literal does not survive `float64`.
- **OQ5.** How a decimal in rule output enters process variables. Recommended: as its exact decimal string.
- **OQ6.** Whether a failed evaluation is retryable. Recommended: no. Evaluation is pure, so identical variables fail identically, and retrying only spends attempts.

**Out of scope**
- Size and depth ceilings on a definition document, and so on an inline rule: `bound-untrusted-input` (kartaladev/wrkflw#184).
- Nested non-string keys in a YAML inline rule, and the Go 1.27 codec behavior: `improve-wire-gate-diagnostics` (kartaladev/wrkflw#235 half 1).
- Whether rule output may write engine-reserved variables (`_errorMessage` and others): `reserve-engine-variable-keys` (kartaladev/wrkflw#187, its Q3 on engine-originated maps). Rule output is merged through the same path as action output, so it inherits that change's answer.
- Module topology, `go.work`, the module-graph guard and lockstep tags: `split-persistence-modules` and `cut-v0-release`. This change adds one module to the mechanism they build.
- A DMN import, a rule-authoring UI, or rule persistence in wrkflw's stores. rlng is an engine only, and wrkflw never stores a rule set apart from the definition that embeds or names it.
- An idempotency key per rule evaluation. Rule evaluation is pure, so the per-visit key wrinkle recorded on #117 does not apply.

**Coordination**
- Size: L
- Depends on: cut-v0-release, bound-untrusted-input, improve-wire-gate-diagnostics
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

- `business-rule-evaluation`: what a businessRuleTask with a `rule` does. That covers evaluation in place of an action, how output is merged, how failures are classified, compile-once per definition version, refusal when no engine is configured or a name is unknown, and the promise that the root module's dependency graph never contains the rule engine.

### Modified Capabilities

- `definition-validation`: *A businessRuleTask cannot carry both a rule and an action* now also accepts a rule with no action. Before this change that combination was refused by the unspecified reservation.

## Impact

- **Code (root module):** `definition/model/validate.go` (delete `ErrRuleNotSupported` and its branch, add `ErrNoRuleEngine` under OQ2), `rulespec.go` (godoc, plus OQ4), a new `rule` port package (OQ1), `engine/step_dispatch.go` and `engine/step_nodes.go` (the rule branch), `runtime` (resolving and evaluating a rule on the action command path, and a driver option for the rule catalog), the loader options (rule catalog and compiler registry), `definition/activity` godoc, `doc.go`, `CONTEXT.md`.
- **New module:** the rlng adapter, with its own `go.mod` and a `go.work` entry. `scripts/modules.sh` `MIN_MODULES` is raised.
- **Tests:** `validate_test.go`, `rulespec_test.go` and `strict_decoding_test.go` rows that pin `ErrRuleNotSupported` become acceptance rows. New engine, runtime and module tests.
- **Coupling:** `bound-untrusted-input` and `improve-wire-gate-diagnostics` both edit `rulespec.go` and land first. `reserve-engine-variable-keys` owns the output-merge filter. See design, Cross-change coupling.
