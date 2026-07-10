# ADR-0122 — Remove `EventSubProcess`; model it as an event-triggered `SubProcess`

**Date:** 2026-07-10
**Status:** Design (approved for planning)
**Feature:** #4 (final) of the BPMN2-alignment set — see `docs/specs/2026-07-10-bpmn2-alignment-design.md`.
**Depends on:** ADR-0121 event-based start (shipped, main `58985e2`) — provides `eventTriggeredStart(def)` and the
lifted single-start invariant.

## Problem

The engine ships two node kinds that model the *same* BPMN concept two different ways:

- `activity.SubProcess` — a nested process definition executed as a scope, entered by a token flowing to it,
  driving its *none* start inline.
- `event.EventSubProcess` — a nested process definition rooted at an **event-triggered** start (message/signal/
  timer), *not* on any sequence flow, latent until its event fires, then either interrupting (cancels the enclosing
  scope) or non-interrupting (runs alongside).

Since ADR-0121 gave the engine first-class event-triggered start events *and* generalized the ESP arm/fire machinery
to select the inner start via `eventTriggeredStart(def)` (rather than `StartNodes()[0]`), the `EventSubProcess` kind
is now redundant: **an event sub-process is exactly a `SubProcess` whose inner start event is event-triggered.** The
BPMN2-alignment theme is to fold bespoke kinds into their general form; this is the last fold.

This is the **highest-regression-risk** item in the set: it retires machinery hardened very recently (ESP-arm timer
sweeps, interrupting/non-interrupting scope-drain edge cases, reverse-instance ESP handling). The design therefore
centers on a **parity-first** migration: prove the `SubProcess`-with-event-start form behaves identically to the
`EventSubProcess` kind *before* deleting the kind.

## Goals / non-goals

**Goals**
- Delete the `EventSubProcess` node kind and its entire public surface; the runtime concept ("event sub-process")
  survives, its *modeling* changes to `SubProcess` + an event-triggered inner start.
- Preserve behavior exactly: interrupting vs non-interrupting, root-scope and nested-scope arming, timer/signal/
  message triggers, scope-drain edge cases, reverse-instance handling.
- Clean break — this is an unreleased library (ADR-0004 line: no back-compat burden). No wire aliases, no migrators.

**Non-goals**
- No change to *instance-level* event-based start (ADR-0121). This ADR is purely about the *sub-process* form.
- No new BPMN capability. Pure consolidation.
- No renaming of the BPMN concept in docs ("event sub-process" stays the term of art).

## Decisions

### D1 — Interrupting marker lives on the `StartEvent` (BPMN-faithful)

Add `StartEvent.NonInterrupting bool`, set via a new bare `WithNonInterrupting()` StartOption. Default `false` =
interrupting (same as the old `EventSubProcess.NonInterrupting`).

Rationale: BPMN2 places `isInterrupting` on the event sub-process's **start event**, not on the sub-process. It sits
next to the trigger fields (`SignalName`/`MessageName`/`CorrelationKey`/`Timer`) that are *already* only meaningful
for an event-triggered start, and the engine's `eventTriggeredStart(def)` already returns that start event, so
reading the flag off it is a one-liner. A plain none-start `SubProcess` carries no interrupting field at all (cleaner
than hanging a meaningless flag on the wrapper — the same "semantic blast-radius" reasoning that made ADR-0120 a
dedicated kind rather than a fold).

Wart (accepted): `NonInterrupting` is meaningless on a *root* / manual start. Guarded by validation (see D5).

Authoring before → after:
```go
// before
event.NewEventSubProcess("handleCancel", cancelDef,
    event.WithEventSubProcessNonInterrupting())

// after
activity.NewSubProcess("handleCancel", cancelDef)   // cancelDef's start carries WithNonInterrupting()
// where cancelDef =
model.NewDefinition("handleCancel",
    event.NewStart("onCancel",
        event.WithMessageCorrelator("cancel", "orderId"),
        event.WithNonInterrupting()),
    activity.NewServiceTask("notify"), event.NewEnd("e"))
```

### D2 — Engine keys off `activity.SubProcess` + an `isEventTriggeredStart` predicate

All ESP machinery switches its type assertion from `event.EventSubProcess` → `activity.SubProcess`, gated by whether
the inner start is event-triggered:

- **Arm** (`armEventTriggeredSubprocesses`, formerly `armEventSubprocesses`, `engine/step_eventsubprocess.go`) scans
  a def's nodes for `activity.SubProcess` whose `eventTriggeredStart(sub.Subprocess) != nil`, and builds one arm per
  such sub-process. A `SubProcess` with only a *none* start is **not** armed (it stays token-driven inline).
- **Fire** (`fireEventTriggeredSubprocessArm`) type-asserts `activity.SubProcess`; reads `NonInterrupting` off the
  inner start event (D1), not the wrapper node.
- **Scope-drain** in `endEventStrategy` (`engine/step_nodes.go:253-500`, incl. the root-ESP "Fix 1/Fix 2" edge
  cases) switches its assertion to `activity.SubProcess` + predicate.
- **`defForScope`** (`engine/step_state.go:42`) merges the `case event.EventSubProcess` into the existing
  `case activity.SubProcess` (both resolve `.Subprocess`).

The arm-vs-inline discriminator is unambiguous: an event sub-process has an event-triggered inner start and **no
incoming sequence flow** (it is a reachability root, armed latent); an embedded sub-process has a *none* inner start
and is reached by a token. `subProcessStrategy.enter` (`engine/step_nodes.go:506-553`) already both inline-drives the
none start (`resolveManualStart`) and arms nested event-triggered sub-processes declared inside the scope — that dual
behavior is preserved, now expressed uniformly over `activity.SubProcess`.

### D3 — Rename the internal ESP identifiers

The user asked to rename (not keep) the `eventSubprocess*` identifiers so nothing carries the retired kind's name.
New scheme, disambiguated from ADR-0121's *instance*-level event-start:

| before | after |
|---|---|
| `eventSubprocessArm` (type) | `eventTriggeredSubprocessArm` |
| `armEventSubprocesses` | `armEventTriggeredSubprocesses` |
| `fireEventSubprocessArm` | `fireEventTriggeredSubprocessArm` |
| `eventSubprocessArmBySignal/Timer/Message` | `eventTriggeredSubprocessArmBySignal/Timer/Message` |
| `removeEventSubprocessArm` | `removeEventTriggeredSubprocessArm` |
| `removeEventSubprocessArmsForScope` | `removeEventTriggeredSubprocessArmsForScope` |
| `removeAllEventSubprocessArms` | `removeAllEventTriggeredSubprocessArms` |
| `InstanceState.EventSubprocesses` (field) | `InstanceState.EventTriggeredSubprocesses` |

These are engine-internal: the inventory confirmed **no** `persistence/`, `runtime/`, `service/`, or `transport/`
references to `eventSubprocessArm` or `InstanceState.EventSubprocesses` — arms live only inside the engine's
`InstanceState` snapshot/clone path, so the rename has no external DTO or wire impact. `eventTriggeredStart` (the
inner-start selector added by ADR-0121) keeps its name — it is already correctly generic.

### D4 — Clean break on the wire; no version bump

- Delete the `eventSubProcess` discriminator name (the `model.RegisterKind(model.KindEventSubProcess, ...)` call in
  `event.go:398-407`) along with `KindEventSubProcess`.
- **No wire-format-version bump** — there is no such constant. The only version is the business
  `ProcessDefinition.Version int` (`node_wire.go:132`, `yaml.go:68`); it is per-definition data, not a schema
  version. (This corrects the handover's "bump def wire version" premise — verified against source.)
- Old JSON/YAML carrying `"kind":"eventSubProcess"` now fails to unmarshal (unknown kind) — acceptable for an
  unreleased library, no consumers.
- `NodeWire.NonInterrupting` (shared field, already present, used by `BoundaryEvent`) is now *also* written/read by
  `StartEvent`'s `ToWire`/`FromWire`. Confirm it survives ESP-kind removal (it does — `BoundaryEvent` keeps it).
- Regenerate `definition/testdata/golden_definition.json` (currently pins one `eventSubProcess` node) to the
  `SubProcess`-with-event-start form.

### D5 — Validation collapses to `KindSubProcess`

- Reachability (`validate.go:346-352`): a `SubProcess` whose inner start is event-triggered becomes a reachability
  root (was `KindEventSubProcess`). `definition/model` **cannot import `event`** (import cycle), so the predicate is
  evaluated via the existing **wire projection**: project the inner start node to `NodeWire` and check
  `SignalName != "" || MessageName != "" || !Timer.IsZero()`.
- `ErrMissingSubprocess` (`:74-77`) and the nested-def validation loop (`:441-455`) collapse to `KindSubProcess`
  only.
- New lightweight guard (optional, low-risk): a `SubProcess`'s inner start carries `NonInterrupting=true` only if it
  is event-triggered; a `NonInterrupting` *root/none* start is a definition error (or WARN). Keeps D1's wart benign.

## Migration order (parity-first — the risk control)

The sequence proves parity **before** deletion by keeping both forms alive transiently:

1. **T1 — Add `StartEvent.NonInterrupting` + `WithNonInterrupting()` + wire projection.** Purely additive; ESP kind
   untouched. RED (option/field test) → GREEN.
2. **T2 — Generalize the engine (transitional dual-recognition).** Arm/fire/scope-drain/`defForScope` recognize
   `activity.SubProcess`-with-event-start **in addition to** the still-present `event.EventSubProcess`, via a shared
   helper (e.g. `isEventTriggeredSubprocessScope(node)` matching either). Apply the D3 renames here. Old ESP tests
   stay green. **Full opus review** (engine behaviour + concurrency).
3. **T3 — Generalize validation/reachability** (D5) for the `SubProcess`-with-event-start form, dual-recognition.
4. **T4 — Port the ESP test suite to the `SubProcess` form** (`state_esp_test.go`,
   `step_eventsubprocess_multistart_test.go`, ESP cases in `step_subprocess_test.go`, `reverse_instance_test.go`,
   `step_nodes_test.go`). **Both** the ported (SubProcess-form) tests **and** the original ESP-kind tests pass
   simultaneously ⇒ behavior parity demonstrated.
5. **T5 — Delete the `EventSubProcess` kind** (struct, `KindEventSubProcess`, `NewEventSubProcess`,
   `WithEventSubProcessNonInterrupting`, `EventSubProcessOption`, `Builder.AddEventSubProcess`, the `eventSubProcess`
   `RegisterKind`) **and the transitional dual-recognition branch**; convert/remove the original ESP-kind tests;
   collapse validation to `KindSubProcess`; regenerate the golden fixture. **Full opus review** (deletion + the
   migrate/remove refactor).
6. **T6 — Example** `examples/scenarios/event_subprocess`: a `SubProcess` with a message-triggered non-interrupting
   inner start acting as an event sub-process. Keep `subprocess_embedded` as the none-start inline case.
7. **T7 — ADR-0122** (`docs/adr/0122-remove-event-subprocess.md`, Nygard) + sweep ESP references in `README.md`,
   `definition/README.md`, `engine/README.md`, `INTERACTIONS.md` and any live plan/spec cross-refs.

## Testing strategy

- **Parity** is the core assertion (T2–T4): the same scenarios (root interrupting arm, root non-interrupting arm,
  nested-scope arm, timer/signal/message triggers, scope-drain on enclosing-scope completion, reverse-instance over
  an armed sub-process) must pass in the `SubProcess` form identically to the ESP-kind form.
- `table-test` project skill for multi-case tests (assert-closure form, `t.Context()`); black-box `_test` packages.
- Per the risk-scaled SDD cadence: T2/T5 get full opus reviewers; T1/T3/T6/T7 controller-verified; whole-branch
  `/code-review` (multi-finder + opus composition) before merge, pointed specifically at interrupting/
  non-interrupting parity and the scope-drain edge cases.
- Verify: `go test -race ./...` green, ≥85% coverage on touched packages, `golangci-lint run ./...` clean.

## Consequences

**Positive** — one fewer node kind; the "event sub-process" concept is now a natural composition (`SubProcess` +
event-triggered start) instead of a bespoke type; the interrupting marker sits where BPMN puts it; internal names no
longer reference a retired kind.

**Negative / risk** — retires freshly-hardened machinery; the interrupting/non-interrupting scope-drain is the
subtlest surface. Mitigated by the parity-first order (both forms green before deletion) and opus review on T2/T5.
Old serialized definitions with `eventSubProcess` stop loading (acceptable — unreleased).

**Mooted ticket** — normal success completion not sweeping outstanding root-ESP arms + timers (tracked separately in
the handover) becomes moot for the ESP-specific case once this machinery is retired; the general question ("does
`TimerFired` no-op on a terminal-status instance?") remains tracked separately.
