# Proposal

## Why

A definition key can be known, legal on its node kind, and still carry a value the decoder never acts on. The decoder then collapses the value onto the zero value of the type it parses into, and that zero value already means something. A typo in a reminder's `wait_trigger.kind` produces no trigger. `end_behavior: "terminat"` produces a normal end. `error_code` on an endEvent without `end_behavior: "error"` is dropped. `Validate` then accepts every one of these definitions, and the re-encoded definition no longer contains the value that was wrong, so the stored record hides the cause. The project rule is that an unrecognized value is refused with a named error, and these paths break it.

Re-measured on this branch's base (`9c6dd2c1`, go1.26.9) with a scratch probe compiled into `definition/model` through `-overlay`; nothing was written to the tree:

- `wait_trigger: {"kind":"duratoin"}` (and `"Duration"`, and a trigger object with no kind) decodes, validates, and re-encodes without `wait_trigger`.
- `end_behavior: "terminat"`, `"Terminate"` and `"normal"` decode and validate; the re-encoded node is a plain `endEvent`.
- `error_code`, `termination_reason` and `termination_outcome: "abort"` on an endEvent whose `end_behavior` does not read them decode, validate, and are erased on re-encode. So are `error_code` beside `terminate` and `termination_reason` or `termination_outcome` beside `error`.
- The trigger-kind refusal that #206 called accidental is narrower than reported. A `deadline_trigger` typo is refused only when `deadline_action` is set; with only `deadline_flow` it is accepted and the deadline vanishes. An `intermediateCatchEvent` timer typo is accepted when the event also declares a signal. A `startEvent` timer typo is accepted and the start becomes untimed. A boundary timer typo is refused with `ErrBoundaryErrorHost`, which sends the author to the wrong problem.
- A `deadline_flow` that names no flow validates. The engine fails only when the deadline fires, with a message that names the missing flow.

## What Changes

- **BREAKING** Both decoders refuse an `end_behavior` other than `terminate`, `error` or empty, compared exactly, with a new sentinel `model.ErrInvalidEndBehavior`. Absorbs kartaladev/wrkflw#194.
- **BREAKING** The JSON decoder refuses a trigger object (`timer_trigger`, `deadline_trigger`, `wait_trigger`) whose `kind` is not one of the nine trigger kind names, compared exactly. This includes an empty or missing `kind` on a present object. The sentinel is a new `model.ErrInvalidTriggerKind`, and the rule applies on every node kind that reads a trigger object. A null trigger stays absent. YAML has no trigger objects and already refuses the keys as unknown. Absorbs kartaladev/wrkflw#206.
- **BREAKING** Both decoders refuse, on an endEvent, `termination_reason` or `termination_outcome` unless `end_behavior` is `terminate`, and `error_code` unless it is `error`. The sentinel is a new `model.ErrKeyNotOnEndBehavior`. A scalar zero (empty string) stays accepted as absent, the same rule `ErrKeyNotOnKind` uses. Absorbs kartaladev/wrkflw#193.
- `termination_outcome` keeps `ErrInvalidTerminationOutcome` on a terminate end. Its message drops the clause that says the key "is accepted and dropped" on any other end, because that stops being true (kartaladev/wrkflw#219, second maintainer comment). A bare `termination_outcome` on a non-terminate end is now refused by `ErrKeyNotOnEndBehavior`, which runs first.
- **BREAKING** `model.Validate` refuses a non-empty `deadline_flow` that names no flow of the node's definition. An empty value means no deadline flow, as today. The sentinel is a new `model.ErrInvalidDeadlineFlow`, mirroring `ErrInvalidRecoveryFlow`. This is the reference-shaped member of kartaladev/wrkflw#219.
- Every refusal happens at decode, beside the existing kind-key and termination-outcome gates. The maintainer settled this in kartaladev/wrkflw#219: `Validate` cannot see a collapsed value, and no leaf node-spec signature changes. The exception is `ErrInvalidDeadlineFlow`, a reference that survives decode intact.
- **Issue dispositions.**
  - kartaladev/wrkflw#219 is absorbed as the class.
  - kartaladev/wrkflw#206, #194 and #193 are superseded by it and absorbed as its measured instances. Their reproductions still hold.
  - Stale in #219:
    - the `attached_to` "Mode 3" misdirection: a dangling host now also reports `ErrBoundaryAttachment` naming the host;
    - "`outcome_variable` without `expose_outcome` silently accepted": an explicit `outcome_variable` is itself the opt-in to exposure, by `WithOutcomeVariable`'s godoc and `engine/step_triggers.go`, and the value round-trips;
    - the `engine/step_triggers.go:965/:967` citation, which is now `:982/:984`.
  - Stale in #193 and #194: "`Validate` can see this". The issue comments already corrected it, and this re-measurement confirms the correction.
- **Persisted-format impact (flagged).** Stored bytes do not change and no migration ships. The decode gates also run on every definition-store load (`GetDefinition`, `Lookup`, republish comparison), identically on Postgres, MySQL and SQLite. A row the library wrote cannot hold any refused value, because the encoder erases them. That erasure is the defect. A row written out of band can hold one, and it stops loading after the upgrade, by name. `ErrInvalidDeadlineFlow` is a `Validate` rule, so it does not affect loads. A stored definition with a dangling `deadline_flow` does survive the encoder, though, so republishing it is refused (design D5).
- **Default security posture:** tightened; silent fail-open configurations become loud. **go.mod:** no change.

**Out of scope**
- Diagnostic quality of decode refusals (nesting path, error order across formats, one key at a time), and the test comment justifying the blank-flow-ID rule (kartaladev/wrkflw#225). Both belong to `improve-wire-gate-diagnostics` (planned, not yet created).
- Case-variant JSON keys (`refuse-case-variant-json-keys`) and sentinels for the unknown-key and unknown-kind refusals (`export-refusal-sentinels`).
- Value ranges inside a recognized trigger (weekdays, intervals, durations), and the flat expression forms `timer_duration`, `deadline_duration` and `wait_every`. They are not closed vocabularies.
- `validate.ValidationDescriptor.Kind`, `signal_name`, `message_name` and `correlation_key`. #219 §8 left them unprobed or excluded by reasoning, and this change does not claim the enumeration complete.

**Decisions this change cannot fully specify without (Open Questions in design, Readiness: human)**
- OQ1: should a blank flow ID be refused? The maintainer constraint is "tighten flows toward nodes, never loosen nodes toward flows".
- OQ2: should `wait_action` without a wait trigger, and `deadline_flow` without a deadline trigger, be refused like `deadline_action` is?
- OQ3: should a malformed `def_ref` be refused with `ErrInvalidQualifier` at decode instead of reporting "missing definition reference"?
- OQ4: must a `deadline_flow` also leave its own node, as a recovery flow must?

The specs cover only behavior that is independent of these four.

**Coordination**
- Size: M
- Depends on: establish-baseline-specs
- Blocks: `improve-wire-gate-diagnostics`
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `definition-wire-format`:
  - ADDED: *end_behavior is refused outside its vocabulary*, *A trigger object's kind is refused outside its vocabulary*, and *An endEvent key is refused unless its end_behavior reads it*.
  - MODIFIED: *termination_outcome on a terminate end is refused outside its vocabulary*. Its diagnostic no longer claims a silent drop.
- `definition-validation`: ADDED *A deadline flow must name a flow of its definition*.

## Impact

- **Code:**
  - `definition/model`: `node_wire_vocabulary.go` gains the end_behavior, end-behavior-key and trigger-kind gates beside `checkTerminationOutcome`, called from `fromWire`.
  - `definition/model/validate.go`: four new sentinels and the deadline-flow rule. `trigger_wire.go` exports or shares the trigger kind vocabulary with `kindString`.
  - `engine/step_timers.go` and `engine/flow_identity.go`: comments that say "DeadlineFlow has no validator rule" must be updated.
- **Consumers:** a hand-authored JSON or YAML definition carrying any refused value now fails to decode or validate, and must be corrected. Library-encoded definitions are unaffected, except a dangling `deadline_flow` on republish.
- **Coupling:** `bound-untrusted-input` adds a requirement to `definition-wire-format` and edits `node_wire.go`, and `harden-definition-store-parity` shares the store conformance test file (design D6). `refuse-case-variant-json-keys` and `export-refusal-sentinels` also modify `definition-wire-format`, and both change *Unknown keys are refused at decode*. This change touches a different requirement, so no block is shared. It still lands in the same spec file, so whichever change archives later rebases onto the earlier sync. Design D6 has the details.
