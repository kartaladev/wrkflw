# 139. Nodes carry an optional human `label`, distinct from the semantic `name`

- Status: Accepted
- Date: 2026-07-25

## Context

Nodes expose `ID()` (technical identifier) and `Name()`. `Name` has been documented
as the node's "display name", is serialized (`NodeWire.Name`), and is set via a
`WithName(...)` option present in the `activity` and `event` leaf packages; gateways
instead take a trailing `name ...string` variadic and carry no functional options.

Consumers need a dedicated human-facing label per node — the string a UI renders —
kept separate from the machine/reference name used in code, expressions, logs, and
correlation. Conflating the two in `Name` forces a choice between a
developer-friendly identifier and a user-friendly caption.

## Decision

We adopt a three-tier node identity and add an optional `label`:

- `ID` — technical identifier (unchanged).
- `Name` — **semantic/reference name** (code-facing). Re-documented from "display
  name" to this meaning; no value or behaviour change.
- `Label` — the optional **human display string**, set via `WithLabel(string)`.

Details:

- `model.Base` gains a raw `label` field, a `Label() string` accessor that **falls
  back to `Name()` when the label is empty**, a `SetLabel(string)` mutator, and an
  unexported `rawLabel()` carrier.
- The `Node` interface gains `Label() string`. Every in-repo kind satisfies it via
  the `Base` embed; this is breaking only for a hypothetical external `Node`
  implementer (none exist; consumers use the `New*` constructors).
- Serialization records the **raw** label only: `NodeWire.Label` with
  `json:"label,omitempty"` (and a matching YAML `label`), written from `rawLabel()`
  so an unset label is omitted and the name-fallback is resolved at read time. This
  keeps the wire clean (no `label == name` on every node), preserves round-trip
  fidelity, and requires no migration of stored definitions. `toWire`/`fromWire`
  handle this centrally, so all node kinds — including gateways whose per-kind
  `ToWire` is a no-op — get it with zero per-kind work.
- `WithLabel(string)` is added to `activity` and `event` (mirroring `WithName`,
  including the throw-event variants). **Gateways convert to a functional-options
  constructor** (`gateway.Option` + `WithName` + `WithLabel`), replacing the
  `name ...string` variadic so labels are set uniformly across every node kind.

## Consequences

- **Clean separation** of machine name vs human caption; UIs render `Label()`,
  which is never empty (it resolves to `Name`), while code keeps using `Name`.
- **Additive, non-breaking serialization.** `label` is `omitempty`; existing stored
  JSON/YAML definitions load unchanged with no explicit label, and `Label()` returns
  their `Name`. Round-trips are faithful (unset stays unset).
- **Breaking gateway constructors.** `gateway.NewExclusive("x", "Name")` becomes
  `gateway.NewExclusive("x", gateway.WithName("Name"))` (same for Parallel/Inclusive/
  EventBased). In-repo call sites are migrated in the same bundle; external callers
  migrate per the CHANGELOG. This is the price of uniform `WithLabel` across all kinds.
- **`Node` interface widened.** Source-compatible in-repo; the ADR notes the
  external-implementer edge case as accepted.
- **`Name` redefinition is documentation-only** — no field, value, or wire change;
  existing names keep working, now read as semantic names.
- **No runtime impact.** `Label` is authoring/serialization metadata; nothing in
  the engine routes, gates, or waits on it.
