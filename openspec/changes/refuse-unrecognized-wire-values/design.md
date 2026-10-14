# Design

## Context

See proposal.md, *Why*, for the measured instances. Current state that shapes the approach, measured at `9c6dd2c1`:

- Both decoders reach one seam per node. The JSON path is `ProcessDefinition.UnmarshalJSON`. The YAML path is `ParseYAML`, which converts `nodeYAML` to `NodeWire` and calls the same function (`definition/model/yaml.go`). That seam is `fromWire` in `definition/model/node_wire.go`. It already runs two gates before it dispatches to the leaf node spec: `checkNodeKeys` (`ErrKeyNotOnKind`) and `checkTerminationOutcome` (`ErrInvalidTerminationOutcome`, in `node_wire_vocabulary.go`, from PR #226 / kartaladev/wrkflw#214).
- The leaf `NodeSpec.FromWire` returns no error (`func(Base, NodeWire) Node`), and neither does `ReadTrigger` (`definition/model/trigger_wire.go`). The `switch` in endEvent's `FromWire` (`definition/event/event.go`) and the outer `switch` in `ReadTrigger` both lack a `default` arm. An unrecognized value therefore leaves the zero value: `EndNormal`, or `schedule.TriggerSpec{}`.
- `model.Validate` receives nodes after this collapse, and a collapsed node is well formed. Validate cannot tell `"terminat"` from an absent key. The comments on kartaladev/wrkflw#193 and #194 record this, and the probe re-confirmed it: every collapsed case returns `Validate` = nil.
- `PutTrigger` writes only the nine names `kindString` returns, and returns nil for an unset trigger. endEvent's `ToWire` writes `termination_*` only for `EndTerminate` and `error_code` only for `EndError`. So library output never carries a value or key this change refuses. The one exception is a dangling `deadline_flow`, which the encoder writes back unchanged.
- The definition store decodes every load with `json.Unmarshal` (`GetDefinition`, `Lookup`, and republish normalisation in `internal/persistence/store/definitions.go`). It runs `kernel.ValidateDefinition` only on publish, before any I/O.
- `DeadlineFlow` has no validation rule. `engine/step_timers.go` refuses a blank one when the deadline fires and reports "DeadlineFlow %q not found" for a dangling one. `engine/flow_identity.go` documents both gaps.

## Goals / Non-Goals

**Goals:**
- One remedy for the closed-vocabulary members of the class, at the seam the maintainer chose, with no change to any leaf node-spec signature.
- Follow the precedent `ErrInvalidOutcome` / `ErrOutcomeRequired` (`engine/errors.go`; enforced in `engine/step_triggers.go` at `:982` and `:984` today). Refuse a value outside the vocabulary, and refuse an empty value only where absence is not legal.
- Derive each vocabulary from the code that writes it, not from a transcribed list.

**Non-Goals:**
- The blank flow ID policy, a trigger-less `wait_action` or `deadline_flow`, `def_ref` diagnostics, and whether a deadline flow must leave its node (Open Questions OQ1 to OQ4).
- Bounding the size of the quoted authored value in a diagnostic. `ErrInvalidTerminationOutcome` already quotes it unbounded. Input size ceilings belong to `bound-untrusted-input`, which adds a whole-document byte ceiling at the same decode boundary; that ceiling bounds the quoted value too.
- Nesting-path diagnostics (kartaladev/wrkflw#217, `improve-wire-gate-diagnostics`).

## Decisions

### D1. Refuse at `fromWire`, beside the existing gates

The maintainer recorded this as settled on kartaladev/wrkflw#219 (first manager note). Three gates join `checkTerminationOutcome` in `node_wire_vocabulary.go`: `checkEndBehavior`, `checkEndBehaviorKeys` and `checkTriggerKinds`. `fromWire` calls them in a fixed order (D3).

**Rejected:**
- *`NodeSpec.FromWire` returning `(Node, error)`*, which touches the registry and all leaf specs.
- *`ReadTrigger` returning `(TriggerSpec, error)`*, which touches its call sites.

  The note rules both out as unnecessary: `fromWire` already holds the authored strings and an error return.
- *A `Validate` rule.* It is structurally impossible, as Context shows: the authored string is gone before `Validate` runs.

### D2. Vocabularies are derived, not transcribed

- **Trigger kinds.** The accepted set is the image of `kindString` over every non-unset `schedule.Kind`. `trigger_wire.go` is in this package, so the gate reads that set directly. A test asserts that it equals the set of `case` labels `ReadTrigger` handles. A probe confirms that each name, decoded, yields a non-zero `TriggerSpec` whose `PutTrigger` re-encodes to the same name.
- **`end_behavior`.** `definition/event` imports `model`, so `model` cannot import `event`. This is the constraint `node_wire_vocabulary.go` already documents for termination outcomes. The set is spelled in `model`, and the same three-test pattern guards it:
  - set equality with the image of `event.EndBehavior.String()`, tested in the external test package;
  - each name carried through the decoder to the reconstructed node;
  - each accepted name pinned against the leaf spec, tested internally.
- **Conditional endEvent keys.** The rule "`termination_*` only for terminate, `error_code` only for error" is derived the way `node_wire_keys.go` derives read sets. A probe builds each `end_behavior` value with one conditional key set at a time, and observes whether the reconstructed node differs. Kartaladev/wrkflw#193 asks for exactly this: "derive the conditional set from the spec rather than transcribing the comment". A test pins the derived table against the three-row prose in `node_wire.go`'s `EndBehavior` comment, so either one drifting fails the test.

**Rejected:** *a hand-written table per gate.* It drifts, for the reason `node_wire_keys.go` records. The #183 probe that used one arbitrary value per field is a cautionary instance: it concluded that endEvent reads no fields (kartaladev/wrkflw#194), so the conditional-key probe iterates over the derived `end_behavior` vocabulary, never over an arbitrary value.

### D3. Gate order: kind, then discriminator, then combination, then value

`fromWire` runs the gates in this order: `checkNodeKeys` (kind), then `checkEndBehavior`, then `checkEndBehaviorKeys`, then `checkTerminationOutcome`, then `checkTriggerKinds`. The precedent is `TestTheKindGateRunsBeforeTheValueGate`. Its reasoning, recorded on `checkTerminationOutcome`, is that when two gates both have grounds, the one naming the key is the accurate diagnostic.

- An invalid discriminator makes the combination unjudgeable, so `end_behavior` runs before the keys that depend on it.
- A key that its `end_behavior` never reads is a key problem, not a value problem. So `termination_outcome: "Abort"` with no `end_behavior` now gets `ErrKeyNotOnEndBehavior`, not `ErrInvalidTerminationOutcome`. This narrows what `checkTerminationOutcome` sees, and its "NOT conditional on end_behavior" comment is rewritten to match.

A test pins every adjacent pair, as the existing test pins the first pair. The spec scenario *The gates answer in a fixed order* covers it.

### D4. Empty values

This follows the second half of the precedent: empty is refused only where absence is not legal.

- **`end_behavior: ""`** means a normal end and is accepted. `doc.go` documents absence as a normal end, and the wire cannot tell `""` from absent.
- **A trigger object that is present with an empty or missing `kind`** is refused. The object's presence asserts a trigger, and the zero `TriggerSpec` is exactly the silent "no trigger" collapse this change exists to remove. A JSON `null` decodes to a nil pointer and stays absent.
- **An empty string under a conditional endEvent key** is accepted as absent. This is the scalar-zero rule `ErrKeyNotOnKind` already uses, and the plain-string wire field cannot distinguish the two.

### D5. `ErrInvalidDeadlineFlow` is a `Validate` rule

A `deadline_flow` survives decode intact. kartaladev/wrkflw#219 §1a separates it from the collapse class as an unvalidated reference, with the remedy "resolve the reference". The rule mirrors `ErrInvalidRecoveryFlow` and runs per definition level. That matches `engine/step_timers.go`, which resolves the flow in the definition that holds the node.

This change specifies only that the reference resolves. Whether it must also leave its own node is OQ4. Once the rule exists, update the comments that say it does not: in `engine/step_timers.go`, and in `engine/flow_identity.go` ("DeadlineFlow has no validator RULE at all").

**Republish impact.** `PublishDefinition` and `MemDefinitionRegistry.Register` validate before any I/O, so a stored definition whose `deadline_flow` already dangles cannot be republished. Loads are unaffected. Its instances already fail when the deadline fires today, so no running behavior is lost.

### D6. Cross-change coupling

- **`refuse-case-variant-json-keys` and `export-refusal-sentinels`.** Both modify *Unknown keys are refused at decode* in `definition-wire-format`, and `export-refusal-sentinels` also modifies two other requirements there. This change MODIFIES only *termination_outcome on a terminate end is refused outside its vocabulary* and ADDS three requirements, so the delta blocks are disjoint. The file is shared, though. Whichever of the three archives later re-reads `openspec/specs/definition-wire-format/spec.md` after the earlier sync, and re-validates. If a later change ever modifies the termination_outcome block, it re-copies the block as synced by this change. No landing order is required.
- **`export-refusal-sentinels`** introduces an `internal/errmark` helper for marking existing errors. This change creates new errors with `%w` and does not need that helper. Neither change waits for the other.
- **`refuse-case-variant-json-keys`** adds a pre-decode key scan for JSON. Its error is about keys and this change's errors are about values, so their scenarios never compete for one input. A case-variant key holding a bad value gets `ErrCaseVariantKey` first, because that scan runs before `dec.Decode`.
- **`bound-untrusted-input`** ADDS a document-ceiling requirement to `definition-wire-format` and edits `definition/model/node_wire.go` and `yaml.go`. The requirement blocks are disjoint, but the code files are shared. Whichever change lands second rebases. Neither depends on the other.
- **`harden-definition-store-parity`** explicitly leaves trigger-kind refusal to this change. Its store conformance tests and the 4.x tasks here both edit `internal/persistence/store/definitions_conformance_test.go`. Whichever lands second rebases.
- **`improve-wire-gate-diagnostics`** (planned) owns kartaladev/wrkflw#225, the stale justification of the blank-flow-ID rule in `validate_test.go`. If OQ1 is decided as "refuse", that test comment is replaced here instead. The two changes must not both edit it.
- **`make-definition-registration-idempotent`** changes the outcome of re-registering identical content. Under D5, re-registering a definition with a dangling deadline flow fails validation before idempotency is considered. That is consistent with "validate before I/O" and needs no coordination beyond a test row there, if that change wants one.

## Risks / Trade-offs

- **[A definition stored out of band holds a refused value and stops loading]** → It fails by name, never silently. The release note gives the repair, the same recipe as `refuse-case-variant-json-keys` D3: on the previous release, decode and re-encode each row. The previous release's encoder erases exactly the values this change refuses, and that re-encoded form is what running instances already execute. The repair therefore preserves behavior.
- **[A stored definition with a dangling `deadline_flow` cannot be republished]** → Its instances already fail when the deadline fires. The refusal moves the failure earlier and names it.
- **[The derived vocabulary drifts from the encoder]** → The D2 tests, plus the existing golden round-trip and the "No library output is refused by the key gate" scenario, extended to the new sentinels.
- **[Hot-path cost on definition load]** → Each gate is a constant-time switch or set lookup per node. Run `BenchmarkGoldenDecode` before and after with `-count=10` and put the benchstat output in the PR.

## Migration Plan

Ship as a breaking v0.x minor release, with the D5 and Risks notes in the release notes. There is no schema change and no migration. To roll back, revert. Stored bytes are untouched, so the previous release reads every row this one accepts.

## Open Questions

Each question would ADD a refusal. None changes a requirement specified here, so the specified work can start once a human confirms Readiness. Each needs a maintainer decision before its own spec rows are written; none was recorded in the issue thread.

- **OQ1. Should a blank flow ID be refused?** Today nothing refuses a blank flow ID. The flow-ID loop skips blanks (`validate.go`, ErrDuplicateFlowID's loop), while the node-ID loop reports blank duplicates. The maintainer recorded a directional constraint on kartaladev/wrkflw#219: *tighten flows toward nodes, never loosen nodes toward flows*. Loosening nodes would reopen #212, because `fireBoundaryArm` relies on `ErrDuplicateNodeID` having no blank exemption. The constraint does not say whether to tighten at all. Options:
  - (a) keep blank flow IDs legal and document why;
  - (b) refuse two blank flow IDs in one definition, with `ErrDuplicateFlowID`, matching nodes;
  - (c) refuse any blank flow ID with a new sentinel.

  Both (b) and (c) are persisted-format impacts. The encoder writes `"id":""` explicitly, so library-written rows hold blank IDs. The rule must stay in `Validate` and not in decode, or those rows stop loading. Republishing them would then be refused. The remedy #219 forbids is uniqueness of the (source, target) endpoint pair. **Recommendation: (b)**, as a `Validate` rule. It is the direction the constraint names, it does not make stored rows unloadable, and it removes the "first blank-ID flow wins" hazard that `engine/flow_identity.go` documents.
- **OQ2. Should a trigger-dependent key without its trigger be refused?** `deadline_action` without a deadline timer is refused (`ErrDeadlineActionWithoutDeadline`). `wait_action` without a wait trigger is not, and neither is `deadline_flow` without a deadline timer; this probe measured both as accepted. #219 §2 records that "nothing in the design decides which". Options:
  - (a) refuse both in `Validate`, with a sentinel each or a shared one;
  - (b) leave both legal.

  Library rows can hold them, because both keys round-trip. **Recommendation: (a)**, in `Validate`, for symmetry with the deadline-action rule. Republishing an affected row would then be refused, so release-note it.
- **OQ3. Should a malformed `def_ref` be refused at decode with `ErrInvalidQualifier`?** Today `parseOrZero` (`definition/activity/activity.go`) discards `ParseQualifier`'s error. The author of `"my-def:0"` is told the reference is "missing", and the re-encoded node has no `def_ref`. The probe re-measured this. #219 §4 asks that this "Mode 3" be "a decision, not a default". Options:
  - (a) refuse it at `fromWire` with `ErrInvalidQualifier`, naming the node and the value;
  - (b) leave it, since it is not a fail-open.

  **Recommendation: (a).** It is the same seam and the same cause, it costs one more gate, and library output always writes a parseable qualifier.
- **OQ4. Must a deadline flow leave its own node?** `ErrInvalidRecoveryFlow` requires both `f.ID == rf` and `f.Source == node`. The engine's deadline path moves the token to the flow's target without checking the flow's source. Options:
  - (a) require the flow to leave the node, as for recovery flows;
  - (b) require only that it resolves, as specified now.

  **Recommendation: (a)**, if no shipped example routes a deadline elsewhere. Before deciding, grep the tests and examples for `WithWaitDeadline` and `deadline_flow`. If (a) is chosen, a row joins the specified requirement.
