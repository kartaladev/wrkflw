# 131. Arm-model unification via embedded `triggerMatch`

- Status: Accepted
- Date: 2026-07-13

Task B0/B1 of the engine-simplify Phase B backlog. Plan:
`docs/plans/2026-07-13-engine-simplify-phase-b.md` (Task B0/B1). Spec:
`docs/specs/2026-07-13-engine-simplification-program.md` (Phase B section).

## Context

The engine's `InstanceState` (`engine/state_arms.go`) tracks three near-identical
"arm" families — bookkeeping entries for something waiting to be triggered by a
timer, signal, or message:

- `armedEvent` — one arm per outgoing catch-event branch of an event-based
  gateway.
- `boundaryArm` — one arm per armed boundary event attached to a parked host
  activity token.
- `eventTriggeredSubprocessArm` — one arm per armed event sub-process waiting
  on its enclosing scope.

Each of the three struct types independently declares the same four
trigger-correlation fields — `TimerID`, `Signal`, `Message`, `MessageKey` — used
to match an incoming `TimerFired`/`SignalReceived`/`MessageReceived` trigger
against the arm. Each family additionally carries ~13 duplicated
`*ByTimer`/`*BySignal`/`*ByMessage`/`removeFor*` linear-scan accessor methods
that differ only in the slice they scan and the owner-key they filter on
(`GatewayToken`, `HostToken`, `EnclosingScopeID`). This is the deepest
structural duplication remaining in the engine core.

`InstanceState` — including `ArmedEvents`, `Boundaries`, and
`EventTriggeredSubprocesses` — is persisted via plain `json.Marshal`
(`internal/persistence/store/store_core.go:77,219`), with no custom
`MarshalJSON`/`UnmarshalJSON` and no wire-schema versioning for these arm
slices. Any change to field names or nesting on these three types is a wire
break requiring a migration.

Go's `encoding/json` promotes the fields of an **anonymous embedded struct**
into the parent object's JSON representation — a field `TimerID` declared
directly on `boundaryArm` and a field `TimerID` reached via an anonymously
embedded `triggerMatch{TimerID string; ...}` serialize identically: both
produce `{"TimerID": "..."}` at the top level of the enclosing object, with no
nesting. This makes it possible to deduplicate the four shared fields **without
moving them in the wire representation** — the collapse can be proven safe by a
round-trip parity test rather than treated as a breaking change needing a
migration.

We considered collapsing the three families into a single fat union type
carrying every owner field (`GatewayToken`, `HostToken`, `EnclosingScopeID`,
`CatchNode`, `HostNode`, `BoundaryNode`, `EventSubprocessNode`, `Flow`,
`NonInterrupting`, `Action`, ...) plus the shared trigger quartet. We rejected
this: it would put every family's owner-specific fields on every arm
(nonsensical zero-valued fields on the other two families' instances at all
times), read worse at every call site (which family is this, really?), and
still require a wire migration since the per-family slices
(`ArmedEvents`/`Boundaries`/`EventTriggeredSubprocesses`) would collapse into
one JSON shape.

## Decision

We introduce a shared, unexported struct

```go
type triggerMatch struct {
	TimerID    string
	Signal     string
	Message    string
	MessageKey string
}
```

and embed it **anonymously** (not as a named field) in each of the three arm
types. Each arm type loses its own four field declarations and keeps only its
own owner/routing fields:

- `armedEvent`: `GatewayToken`, `CatchNode`, `Flow`, plus the embedded
  `triggerMatch`.
- `boundaryArm`: `HostToken`, `HostNode`, `BoundaryNode`, `Flow`,
  `NonInterrupting`, `Action`, plus the embedded `triggerMatch`.
- `eventTriggeredSubprocessArm`: `EnclosingScopeID`, `EventSubprocessNode`,
  `NonInterrupting`, plus the embedded `triggerMatch`.

Field access (`arm.TimerID`, `arm.Signal`, `arm.Message`, `arm.MessageKey`) and
field assignment (`arm.TimerID = timerID`) continue to work unchanged via Go's
field-promotion rules — production call sites in `step_boundaries.go`,
`step_eventsubprocess.go`, and `step_nodes.go` build the owner fields via a
composite literal and then assign the trigger fields by promoted-field
assignment, so none of them needed to change. Composite literals that
previously keyed the trigger fields directly (test fixtures using
`boundaryArm{HostToken: ..., TimerID: ...}` style literals) do need a
mechanical edit to nest those four keys under `triggerMatch: triggerMatch{...}`
— Go requires keyed struct literals to name fields declared directly on the
type, not fields reached only through promotion.

The wire safeguard is a golden-JSON round-trip parity test
(`engine/state_arms_wire_test.go`) written and run green **against the
pre-embed code first** (establishing the golden fixture from the actual
current marshaled shape), then re-run green **after** the embed — proving the
serialized field set and nesting are unchanged. The real persistence
round-trip suite (`go test ./persistence/... ./internal/persistence/...`)
is the second, end-to-end proof.

## Consequences

- Four field declarations collapse from three copies to one; the doc comment
  for the shared trigger-correlation contract now lives in one place
  (`triggerMatch`) instead of being repeated three times.
- No wire migration is required — the persisted JSON shape of `ArmedEvents`,
  `Boundaries`, and `EventTriggeredSubprocesses` is byte-identical before and
  after, verified by the parity test and the persistence round-trip suite.
- Production call sites are unaffected (field promotion covers both read and
  assignment); only test fixtures using keyed composite literals for the
  trigger fields needed mechanical nesting edits under `triggerMatch: {...}`.
- The fat-union alternative is explicitly rejected: it would carry
  irrelevant owner fields on every arm and force a wire migration, for no
  benefit over the embedded-match approach.
- `go doc ./engine` is unchanged — `triggerMatch` and all three arm types are
  unexported, so this is invisible to library consumers.

### Task B2 — generic scan/remove accessors (done)

The nine near-identical by-trigger scan loops
(`armedEvent`/`boundaryArm`/`eventTriggeredSubprocessArm` ×
`Timer`/`Signal`/`Message`) collapse into three generic helpers
(`armByTimer`/`armBySignal`/`armByMessage`) keyed on the now-shared
`triggerMatch` via an `armMatchable[T]` pointer-method constraint
(`matchPtr() *triggerMatch`, one trivial method per arm type; `PT` is inferred
from the slice element type at every call site). The three owner-keyed remove
filters collapse into one `removeArmsWhere` helper taking a per-family owner
predicate (`GatewayToken`/`HostToken`/`EnclosingScopeID` — the genuinely
per-family part). Thin per-family wrapper methods preserve every method name,
the pointer-return-for-mutation contract, slice order, and the non-nil
kept-slice semantics, so all call sites and the persisted JSON shape are
unchanged. `cancelAllArmsAndBoundaries` and `removeAllEventTriggeredSubprocessArms`
are left specialized — they clear to `nil` (not an allocated empty slice) and
operate over multiple slices / with ESP-survival semantics that the generic
filter does not model.

### Task B3 — arm→fire→cancel skeleton (nothing extracted, by design)

Task B3 speculatively aimed to unify the boundary and event-sub fire skeletons
(`fireBoundaryArm`, `fireEventTriggeredSubprocessArm`). On inspection there is
no clean common skeleton left to extract: the genuinely-shared sub-parts —
`emitFireOnceAction`, `cancelTokenWaits`, and the gateway→boundary→event-sub
dispatch cascade (`dispatchArmCascade`) — were already factored out in Phase A.
What remains differs structurally along the host-token-keyed (boundary) vs
scope-keyed (event-sub) axis on four of the ~six steps: the stale-fire guard
(host-token existence + defensive arm cleanup vs enclosing-scope/Status check),
target resolution (outgoing flow vs `eventSubprocessNested` inner-start node),
the interrupting cancel (one host token vs every token in the scope plus
sibling-arm removal), and placement (a token in the host's own scope vs a new
child scope). The only byte-identical fragment is the five-line `drive`-and-
append tail, which is a pervasive engine idiom appearing at five unrelated
sites (timers, compensation, gateways, boundary, event-sub) — not specific to
the fire skeleton; unifying it would exceed B3's scope and hide the explicit
resume-point `drive` call. A forced shared helper would parameterize over four
of six steps and read worse than the two explicit, documented functions. Per
the plan's sanctioned "do less / nothing and report" clause, B3 extracts
nothing; the arm-cascade unification the umbrella spec envisioned was already
delivered by Phase A's `dispatchArmCascade`.
