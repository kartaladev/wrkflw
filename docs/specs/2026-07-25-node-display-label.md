# Spec: Optional human `label` on every node

- **Date:** 2026-07-25
- **Status:** Approved (design + bundle audit passed)
- **ADR:** 0139
- **Bundle:** spec (this) + ADR-0139 + plan `docs/plans/2026-07-25-node-display-label.md`

## Context

Every node embeds `model.Base{id, name}` and exposes `ID()` (technical identifier)
and `Name()`. `Name` is currently documented as the node's "display name", is
serialized (`NodeWire.Name`, `json:"name"`), and is set via a `WithName(...)` option
that exists in the `activity` and `event` leaf packages (gateways instead take a
trailing `name ...string` variadic and have no functional options).

Consumers want a dedicated **human-facing label** on each node — the string a UI
renders — kept distinct from the machine/reference name. We therefore adopt a
three-tier identity model (the usual BPMN `id`/`name` intent, extended):

- `ID` — technical identifier (unchanged).
- `Name` — **semantic/reference name**: stable, code-facing (expressions, logs,
  correlation). Re-documented from "display name" to this meaning.
- `Label` — the **human display string** (i18n-friendly), optional; **defaults to
  `Name` when unset**.

## Goals

- Add an optional `label` to every node kind, set via `WithLabel(string)`.
- Expose `Label() string` on the `Node` interface; it returns the explicit label,
  or falls back to `Name()` when empty.
- Serialize the **raw** label only (JSON + YAML, `omitempty`); resolve the
  name-fallback at read time. Round-trip faithful; old definitions decode with no
  explicit label.
- Give gateways a functional-options constructor so `WithLabel` (and `WithName`)
  work uniformly across all node kinds.
- Re-document `Name`; record the decision as ADR-0139.

## Non-Goals

- No i18n/translation machinery — `Label` is a single plain string.
- No change to runtime execution semantics; `Label` is authoring/serialization
  metadata only (nothing routes or gates on it).
- No migration of stored definitions required (additive, `omitempty`).

## Design

### 1. Model — `definition/model/node.go`

Add a raw `label` to `Base` and the accessor/mutator/carrier:

```go
type Base struct {
	id    string
	name  string
	label string // raw human label; empty means "fall back to name"
}

// Label returns the human display label: the explicitly-set label, or the
// semantic Name when no label was set.
func (b Base) Label() string {
	if b.label != "" {
		return b.label
	}
	return b.name
}

// SetLabel sets the raw human label. Used by the WithLabel options in the leaf
// packages, which mutate the embedded Base.
func (b *Base) SetLabel(label string) { b.label = label }

// rawLabel returns the explicitly-set label without the Name fallback. Used only
// by toWire so serialization records what was actually set (empty ⇒ omitted).
func (b Base) rawLabel() string { return b.label }
```

Extend the `Node` interface:

```go
type Node interface {
	Kind() NodeKind
	ID() string
	Name() string
	Label() string // human display label; falls back to Name when unset
}
```

Re-document `Name`: change the `Base.name` / `SetName` doc wording from "display
name" to "semantic/reference name (code-facing); the human-facing string is
`Label`".

**Node-interface extension** is source-compatible for all in-repo kinds (every
type embeds `Base`, which now provides `Label()`); it is technically breaking only
for a hypothetical *external* implementer of `model.Node` — none exist in the repo,
and the documented consumer path is the `New*` constructors. Recorded in the ADR.

### 2. Serialization — raw label via carrier

`definition/model/node_wire.go`:
- Add `Label string` to `NodeWire` with `json:"label,omitempty"`.
- `toWire` writes the **raw** label through the carrier so an unset label is
  omitted (no `label == name` duplication on every node):

```go
func toWire(n Node) NodeWire {
	w := NodeWire{ID: n.ID(), Kind: n.Kind(), Name: n.Name()}
	if lc, ok := n.(interface{ rawLabel() string }); ok {
		w.Label = lc.rawLabel()
	}
	if s, ok := specFor(n.Kind()); ok && s.ToWire != nil {
		s.ToWire(n, &w)
	}
	return w
}
```

- `fromWire` restores the raw label into `Base`:

```go
return s.FromWire(Base{id: w.ID, name: w.Name, label: w.Label}, w), nil
```

This is centralized: because `toWire`/`fromWire` handle `Base` for **all** kinds
before/around the per-kind spec, every one of the 19 node kinds (gateways included,
whose `ToWire` is a no-op) serializes `label` with zero per-kind work.

### 3. YAML authoring — `definition/model/yaml.go`

- Add `Label string` with `yaml:"label,omitempty"` to the YAML node struct.
- Map it into the `NodeWire` it builds (`Label: ny.Label`).

### 4. Authoring options — `WithLabel`

Mirror the existing `WithName` (`nameOpt`) pattern with a `labelOpt` that calls
`SetLabel`:

- `definition/activity/options.go`: `WithLabel(string)` with the full `nameOpt`
  method set so it reaches **all seven** activity constructors — the five task kinds
  (ServiceTask, UserTask, ReceiveTask, SendTask, BusinessRuleTask) via their per-kind
  option interfaces, **and SubProcess + CallActivity** via `ActivityOption`'s
  `applyName` (their sole label channel; `applyName` must call `SetLabel`, not be a
  no-op).
- `definition/event/options.go`: `WithLabel(string)` with per-event-kind apply
  (Start, Catch, End, Boundary) **and** the throw-event func setters
  (IntermediateThrowEvent, CompensationThrowEvent), matching how `WithName` is
  wired there.

### 5. Gateways — convert to functional options (breaking)

`definition/gateway/gateway.go`: introduce a gateway option type and constructors
that accept it, replacing the `name ...string` variadic:

```go
type Option func(*model.Base)

func WithName(name string) Option  { return func(b *model.Base) { b.SetName(name) } }
func WithLabel(label string) Option { return func(b *model.Base) { b.SetLabel(label) } }

func NewExclusive(id string, opts ...Option) model.Node {
	b := model.NewBase(id, "")
	for _, o := range opts { o(&b) }
	return ExclusiveGateway{b}
}
// … NewParallel, NewInclusive, NewEventBased identically.
```

**Breaking:** callers using `gateway.NewExclusive("x", "My Gateway")` migrate to
`gateway.NewExclusive("x", gateway.WithName("My Gateway"))`. In-repo call sites
(examples, tests, builder) are updated in the same bundle; recorded in CHANGELOG.

### 6. Builder / fluent layer

`definition/model/builder.go`, `definition/build/build.go`, and the fluent `AddX`
methods pass options through to the `New*` constructors, so `WithLabel` flows
without new builder surface. Verify during planning that no builder signature
hard-codes the gateway `name ...string` form; update any that do to the option form.

## Testing strategy (TDD, red-first)

Hot-path-first for the serialization round-trip (the path production exercises):

1. **`Base.Label` fallback** — explicit label returns it; empty returns `name`.
2. **Wire round-trip** — a node with an explicit label serializes `label` and
   reloads it; a node with no label omits `label` from JSON and `Label()` still
   resolves to `name` after reload (raw-not-baked assertion).
3. **YAML authoring** — a `label:` in YAML decodes onto the node.
4. **`WithLabel` per package** — activity, event (incl. a throw variant), and
   gateway each set the label via the option.
5. **Gateway option migration** — `gateway.NewExclusive(id, WithName(...), WithLabel(...))`
   sets both; a table test across the four gateway kinds.
6. **Node-interface conformance** — every kind returns a sensible `Label()`.

## Verification checklist

- [ ] `Base.label` + `Label()` (name fallback) + `SetLabel` + `rawLabel` carrier.
- [ ] `Node` interface has `Label()`; all kinds satisfy it (compile-time).
- [ ] `NodeWire.Label` (`json:"label,omitempty"`); `toWire` writes raw; `fromWire` restores.
- [ ] YAML `label` field + mapping.
- [ ] `WithLabel` in activity, event (+ throw), gateway; gateways on functional options.
- [ ] `Name` re-documented (semantic/reference name).
- [ ] In-repo gateway call sites migrated to the option form; build green.
- [ ] New tests (round-trip raw-not-baked is mandatory), each red before green.
- [ ] `go test -race ./...` green; ≥85% on touched packages.
- [ ] `golangci-lint run ./...` clean.
- [ ] ADR-0139 written; CHANGELOG breaking entry (gateway constructors).
- [ ] Delivery Gate: `/code-review` + `/security-review`, findings folded via `--amend`.
- [ ] One feature-bundle commit (impl + tests + ADR + spec + plan + CHANGELOG).

## Risks & open questions

- **Gateway constructor break** — the only breaking surface; blast radius is
  in-repo examples/tests/builder plus external callers, mitigated by CHANGELOG +
  ADR. Confirm the full in-repo call-site list during planning (`grep -rn
  "gateway.New"`).
- **`Name` semantic redefinition** is documentation-only (no code/behaviour
  change); existing `Name` values keep working, now interpreted as semantic names.
- **Carrier assertion** in `toWire` always succeeds (every node embeds `Base`);
  the `ok` guard is defensive and keeps `toWire` total.
