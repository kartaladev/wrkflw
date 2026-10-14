# Design

## Context

See proposal.md, *Why*. Two facts make the engine assertions vacuous. Both are measured on main.

1. **`Step` discards everything on error.** `engine.Step` has four `return StepResult{}, err` sites (`engine/step.go`). The handlers it reaches also return `StepResult{}` on refusal, for example `engine/step_compensation.go:169` and `:247`. Commands a refusing handler built are never returned, so commands are not an observable channel for "nothing was dispatched".
2. **`Step` deep-clones its input** (`cloneState`, `engine/step_state.go`). A replay against the caller's own state, or a read of it, cannot see whether the refusal consumed anything.

The measurements below were taken in a scratch copy of the repository, not the working tree, on go1.26.9.

- **#227 reproduced.** The guard in `fireBoundaryArm` was moved below `cancelTokenWaits` (the host consumption). `TestBoundaryFireFailsLoudlyWhenItsTargetNamesNothing` stayed green on both refused rows.
- **A post-call state probe discriminates.** The same mutation was applied, and the working state captured after the refusing call was compared with `armed.Clone()`. The comparison went from equal to unequal, both at `dispatch` level and at `fireBoundaryArm` level. The returned commands were empty in both cases, mutated or not, which confirms fact 1.
- **The level matters.** The `MessageReceived` was given a payload (`{"x": 1}`) on unmutated code. The `dispatch`-level probe was then unequal at HEAD, and the `fireBoundaryArm`-level probe stayed equal. `onMatch` merges the trigger payload before the fire (`engine/step_arm_dispatch.go`), so "refused before anything is mutated" is true of `fireBoundaryArm` and false of `dispatch`.
- **The terminal guard is clean at `dispatch` level.** A `StartInstance` was sent to a terminal instance (`terminalGuardDef`). The post-dispatch state equalled the input clone.
- **The zero-result contract is unpinned.** `return res, idErr` was substituted for `return StepResult{}, idErr` in `Step`. `TestStepIDGen` stayed green, and the result carried 4 tokens and status running.
- **Decoder parity of the asymmetry rows holds.** All six rows of `TestZeroValuedMisplacedKeyIsAsymmetric` were decoded through `model.ParseYAML` plus `Build`, and through `json.Unmarshal`. Both decoders gave the spec's outcome on every row: accepted ×3, `ErrKeyNotOnKind` ×3.

### Site inventory

Found by grepping `*_test.go` for `Empty|Nil|Zero|Len(t, X.Commands|X.State…)` and `range X.Commands|X.State.Tokens` within 20 lines after a required error, then reading each site. Grepping outside `engine/` found no `Step` error-path sites.

**Shape A: an emptiness assertion on the `StepResult` of a refused `Step`** (30 assertions)

| File | Lines | Guard (sentinel) |
|---|---|---|
| step_boundaries_flow_resolution_test.go | 234-238, 249-252 (`refusedLoudly`, #227) | boundary target unresolved (`fireBoundaryArm`) |
| step_terminal_policy_test.go | 203, 251, 339, 479 | `ErrInstanceTerminal` (dispatch) |
| step_harvest_terminal_admission_test.go | 80, 81 | `ErrInstanceTerminal` |
| step_terminal_dispatch_test.go | 285, 286 | `ErrInstanceTerminal` |
| step_terminal_test.go | 385, 400, 1444 | `ErrInstanceTerminal` |
| step_compensation_terminal_test.go | 237, 254, 276, 296, 355 | trigger-shape errors in the compensation handler; `ErrInstanceTerminal` ("nothing left to compensate") |
| step_compensation_stall_verbs_test.go | 274 | `ErrNoCompensationWalk`, `ErrCompensationCommandMismatch`, `ErrStallIncidentNotFound` |
| step_compensation_failed_incident_lifecycle_test.go | 224 | `ErrIncidentNotResolvable` |
| baseline_operator_incidents_test.go | 74 | `ErrIncidentNotResolvable` |
| step_resolve_incident_stall_refusal_test.go | 57 | `ErrIncidentNotResolvable` |
| step_human_outcome_test.go | 61, 62 | `ErrInvalidOutcome`, `ErrOutcomeRequired`, `ErrManualTaskPayload` |
| step_cancel_dropped_test.go | 143 | `ErrCancelNotApplicable` |
| step_start_duplicate_test.go | 81, 108 | `ErrInstanceAlreadyStarted` |
| step_human_test.go | 857-860 | `ErrEmptyReassignTarget` (in `Step`, before `cloneState`) |

**Shape B: a replay or read of the caller's own input after a refusal** (4 assertions)

| File | Lines | Claim it cannot distinguish |
|---|---|---|
| step_boundaries_flow_resolution_test.go | 240-246 (`armed`, #227) | the host token survives |
| step_compensation_failed_incident_lifecycle_test.go | 228-233 (replay) | the refusal does not consume the incident |
| baseline_operator_incidents_test.go | 76-80 (replay) | the incident is still there to refuse again |
| step_resolve_incident_stall_refusal_test.go | 59-65 (replay) | the incident survives and is nameable |

**Non-engine** (3 tests): `transport/http/gin/gin_test.go` `TestInternalError_NoRawErrorLeak`; the "unevaluable authz predicate" row of `transport/http/httpcore/errors_rendering_test.go` `TestClassifyError_ChainRenderingPerArm`; and `definition/model/node_wire_keys_test.go` `TestZeroValuedMisplacedKeyIsAsymmetric`.

## Goals / Non-Goals

**Goals:**
- Every listed assertion either observes a channel that changes when its guard moves or is deleted, or is labelled a control that states why it cannot fail.
- Each strengthened assertion is proven by a recorded mutation that turns it red. That is the A3 check #227 asks for, generalized.

**Non-Goals:**
- Moving any production guard. Probes describe the guards as they are.
- New `Step` API to expose partial results. A test-only shim suffices.
- Sentinel exports. `export-refusal-sentinels` owns them.

## Decisions

### D1. Observe the working state right after the function that owns the guard

A test-only shim in `engine/export_test.go` (package `engine`, compiled only under `go test`) runs the refusing function on a `cloneState` copy and returns the post-call copy. Two entry points cover the inventory:
- `dispatch`, for the dispatch-level terminal guard and the handler guards;
- `fireBoundaryArm`, for #227.

The assertion compares the post-call copy with `input.Clone()`. Commands are not asserted, because they are unobservable (fact 1).

*Evidence:* the `fireBoundaryArm` probe and the payload measurement above. A `dispatch`-level probe is red at HEAD for a payload-carrying message, through no fault of the guard.

*Rejected:*
- **Assert `Step`'s result harder.** It is zero by construction.
- **A `dispatch`-level probe for everything.** `onMatch` defeats it for boundary fires (measured).
- **A side channel through public seams**, such as a counting `IDGenerator` or a captured slog handler. Neither moves on a refusal at most of these sites, so it would be vacuous in its own way.
- **Make `Step` return partial state on error.** That changes observable behavior, and the zero-result contract is the point.

### D2. A property unobservable even white-box becomes a labelled control

Two kinds of assertion are rewritten to the existing `step_task_lifetime_guard_test.go` form ("CONTROL: … cannot fail here") instead of being made to fail:
- the `Step`-level "returns the zero result" lines, once the contract is pinned by D4;
- `ErrEmptyReassignTarget`, whose guard runs before `cloneState`, so no state exists to observe.

Inventing an observation that does not exist would be the same defect in a new form.

*Rejected:* **Deleting them.** They document the contract at the point of use, and the label prevents the misreading.

### D3. Absorb kartaladev/wrkflw#227

It is the same class, and its own checklist asks to enumerate the helper, which the inventory does. The `armed` assertion (shape B) is retargeted to the D1 post-call state of `fireBoundaryArm`.

*Rejected:* **Leave #227 separate.** It would split one helper across two changes.

### D4. Pin the zero-result contract once, on the path where it is not automatic

The `TestStepIDGen` "generator failure" row asserts `assert.Equal(t, engine.StepResult{}, res)`. That is the only error path where `dispatch` has already produced a non-empty result (measured: 4 tokens, status running).

### D5. Non-engine sites

- **gin:** read the raw body before decoding and assert it does not contain the service error's text, as stdlib and fiber do. Mutation: make `writeErr` copy `err.Error()` into `body.Error` for status ≥ 500.
- **httpcore:** produce the row's error with `authz.RoleAuthorizer{}.Authorize` over a spec whose attribute predicate cannot evaluate. This reuses the `predicateSource` approach of `authz/predicate_failure_test.go`. Assert 500 and that `body.Message` lacks the predicate source. Mutation: wrap the predicate error in `ErrNotAuthorized` in `authz/decider.go`. The row goes to 403, while the fabricated row would stay 500.
- **asymmetry:** decode each row through `model.ParseYAML(...).Build()` and through `json.Unmarshal`, asserting the same outcome on both. Keep the direct gate rows as the unit-level table, or fold them in. Mutation: in `fromNodeYAML`, nil out an empty `Outcomes` before the gate. The YAML row flips to accepted, while the direct gate test stays green.

### Cross-change coupling

- **`export-refusal-sentinels`** owns the sentinels for the unresolvable boundary target and the unevaluable predicate. It edits the same `refusedLoudly` error check and the same httpcore row. Either may land first. The second to land rebases, and asserts `errors.Is` on the sentinel if it exists.
- **`refuse-case-variant-json-keys`** edits the JSON decode path that D5's asymmetry rows drive. No ordering is required. The rows use lower-case keys, so they are unaffected.

## Risks / Trade-offs

- [A probe is red at HEAD because a guard sits after a benign mutation, the way `onMatch` does at `dispatch` level] → Move the probe to the innermost function that owns the guard. If none is clean, see Open Questions.
- [The `export_test.go` shims couple tests to the unexported `dispatch` and `fireBoundaryArm` signatures] → Keep them thin delegations, as `export_test.go` already requires ("No logic lives here — only delegation"). A signature change then breaks the build loudly, never silently.
- [The mutation proofs are not kept in the tree] → Each task records the mutation and the red test name in its checkbox text, so a reviewer can re-run it.

## Open Questions

- **A guard that is not first in its function.** If a handler probe is red at HEAD and no inner function owns the guard cleanly, does this change move the guard (internal, not observable) or record the finding and seed a separate change? Only the two measured sites are known clean. Under baseline D4 a red test seeds a new change, which points to the second option, but that is a decision for the owner, not this plan.
