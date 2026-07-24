# Node Human `label` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional human `label` to every node kind — set via `WithLabel(string)`, serialized as `json:"label,omitempty"` / `yaml:"label"`, resolving to `Name` when unset.

**Architecture:** `label` lives on the shared `model.Base` embed, so serialization (`toWire`/`fromWire`) and the `Node` interface cover all 19 kinds centrally. `WithLabel` mirrors the existing `WithName` (`nameOpt`) pattern per leaf package; gateways convert from a `name ...string` variadic to functional options so `WithLabel`/`WithName` are uniform.

**Tech Stack:** Go 1.25; `definition/*` authoring packages; existing JSON/YAML wire.

## Global Constraints

- Module `github.com/kartaladev/wrkflw`; Go 1.25.
- `label` is authoring/serialization metadata only — no runtime routing/gating on it.
- Serialize the **raw** label (omit when unset); resolve the `Name` fallback only in the `Label()` accessor. Round-trip must be faithful (unset stays unset).
- `Name` is re-documented as the *semantic/reference* name — documentation-only, no value/wire change.
- Wire key is exactly `label` (JSON and YAML), not `displayLabel`.
- Error sentinels keep `workflow-<pkg>:` prefix; pair each `foo.go` with `foo_test.go`; black-box `_test` packages preferred.
- TDD red-first for every new symbol; ≥85% coverage on touched packages, hot paths (the wire round-trip) first; `go test -race ./...` and `golangci-lint run ./...` clean.
- ADR is **0139** (0138 is reserved by the concurrent clock-removal branch).

---

### Task 1: Model — `label` on `Base`, `Label()` on the `Node` interface

**Files:**
- Modify: `definition/model/node.go` (`Base` :20, `Node` interface :12, `Name`/`SetName` docs)
- Test: `definition/model/base_test.go`

**Interfaces:**
- Produces: `Base.Label() string` (raw label, else `Name`), `Base.SetLabel(string)`, unexported `Base.rawLabel() string`; `Node` interface gains `Label() string`.

- [ ] **Step 1: Write the failing `Label` fallback test**

Add to `definition/model/base_test.go`:

```go
func TestBaseLabelFallsBackToName(t *testing.T) {
	b := model.NewBase("id1", "SemanticName")
	if got := b.Label(); got != "SemanticName" {
		t.Fatalf("unset Label() = %q, want fallback to name", got)
	}
	b.SetLabel("Human Label")
	if got := b.Label(); got != "Human Label" {
		t.Fatalf("Label() = %q, want explicit label", got)
	}
}
```

Also assert `rawLabel` stays empty on fallback via a same-package test in `node.go`'s package if `rawLabel` is unexported — put a `package model` white-box test `node_rawlabel_test.go`:

```go
package model

import "testing"

func TestBaseRawLabelIsEmptyWhenUnset(t *testing.T) {
	b := NewBase("id1", "Name")
	if b.rawLabel() != "" {
		t.Fatalf("rawLabel() = %q, want empty when unset", b.rawLabel())
	}
	b.SetLabel("L")
	if b.rawLabel() != "L" {
		t.Fatalf("rawLabel() = %q, want L", b.rawLabel())
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`Label`/`SetLabel`/`rawLabel` undefined)

Run: `go test ./definition/model/ -run 'Label|RawLabel'`
Expected: build failure — undefined methods.

- [ ] **Step 3: Implement on `Base`**

In `node.go`, add `label string` to `Base`; add:

```go
// Label returns the human display label: the explicitly-set label, or the
// semantic Name when none was set.
func (b Base) Label() string {
	if b.label != "" {
		return b.label
	}
	return b.name
}

// SetLabel sets the raw human label (used by the WithLabel leaf options).
func (b *Base) SetLabel(label string) { b.label = label }

// rawLabel returns the explicitly-set label without the Name fallback; used only
// by toWire so an unset label is omitted from the wire.
func (b Base) rawLabel() string { return b.label }
```

Add `Label() string` to the `Node` interface. Update `Base.name`/`SetName` doc
comments: `name` is the *semantic/reference* name (code-facing); the human string
is `Label`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./definition/model/ -run 'Label|RawLabel'` → PASS.
Run: `go build ./...` → all 19 kinds still satisfy `Node` (they embed `Base`).

- [ ] **Step 5: Stub-implementer sweep**

Run: `grep -rn ") Kind() \(model\.\)\?NodeKind" --include=*.go | grep -v definition/`
Expected: no non-embedding `Node` implementer that now lacks `Label()`. If any test stub implements `Node` by hand, add a `Label()` method to it. (Repo scan at plan time found none outside `definition/`.)

- [ ] **Step 6: Commit**

```bash
git add definition/model/node.go definition/model/base_test.go definition/model/node_rawlabel_test.go
git commit -m "feat(model): node human label with name fallback (ADR-0139)"
```

---

### Task 2: Serialization — raw `label` through JSON wire

**Files:**
- Modify: `definition/model/node_wire.go` (`NodeWire` :13, `toWire` :74, `fromWire` :120)
- Test: `definition/model/node_wire_test.go` (or `node_test.go`)

**Interfaces:**
- Consumes: `Base.rawLabel()` (Task 1).
- Produces: `NodeWire.Label string` (`json:"label,omitempty"`).

- [ ] **Step 1: Write the failing round-trip test (raw-not-baked)**

Authored at the **wire level** so it depends only on Task 1 + Task 2 (no leaf
`WithLabel` option — those come later; `model` can't import the leaves anyway). The
test package is `model_test` and must import a leaf package so the `userTask` kind
is registered (`import _ "github.com/kartaladev/wrkflw/definition/activity"`).

```go
func TestNodeWire_LabelRawRoundTrip(t *testing.T) {
	// Explicit label: MUST be restored by fromWire, then written raw by toWire.
	const withLabel = `{"id":"d1","version":1,"nodes":[{"id":"u","kind":"userTask","name":"sem","label":"Human"}]}`
	var def1 model.ProcessDefinition
	require.NoError(t, json.Unmarshal([]byte(withLabel), &def1))
	require.Equal(t, "Human", def1.Nodes[0].Label()) // guards the fromWire label restore

	data1, err := json.Marshal(def1)
	require.NoError(t, err)
	require.Contains(t, string(data1), `"label":"Human"`) // guards the toWire raw write

	// Unset label: omitted from JSON, Label() resolves to name (fallback).
	const noLabel = `{"id":"d2","version":1,"nodes":[{"id":"u2","kind":"userTask","name":"sem2"}]}`
	var def2 model.ProcessDefinition
	require.NoError(t, json.Unmarshal([]byte(noLabel), &def2))
	require.Equal(t, "sem2", def2.Nodes[0].Label()) // fallback to name after reload

	data2, err := json.Marshal(def2)
	require.NoError(t, err)
	require.NotContains(t, string(data2), `"label"`) // raw-not-baked: unset stays omitted
}
```

Why wire-level: the `require.Equal("Human", def1.Nodes[0].Label())` after unmarshal
is what guards the `fromWire` `label: w.Label` line — an implementer who drops it
fails here (the unset case alone would pass via the `Label()` fallback regardless).

- [ ] **Step 2: Run — expect FAIL** (`NodeWire.Label` undefined / label not emitted)

Run: `go test ./definition/model/ -run TestNodeWire_LabelRawRoundTrip`
Expected: FAIL.

- [ ] **Step 3: Implement wire plumbing**

Add to `NodeWire`:
```go
Label string `json:"label,omitempty"`
```
In `toWire`, after constructing `w` and before the per-kind spec call:
```go
if lc, ok := n.(interface{ rawLabel() string }); ok {
	w.Label = lc.rawLabel()
}
```
In `fromWire`, thread the label into `Base`:
```go
return s.FromWire(Base{id: w.ID, name: w.Name, label: w.Label}, w), nil
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./definition/model/ -run TestNodeWire_LabelRawRoundTrip` → PASS.
Run: `go test ./definition/model/... -race` → PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/node_wire.go definition/model/node_wire_test.go
git commit -m "feat(model): serialize raw node label (json label, ADR-0139)"
```

---

### Task 3: YAML authoring — `label` key

**Files:**
- Modify: `definition/model/yaml.go` (node YAML struct :19-area, mapping into `NodeWire` :108-area)
- Test: `definition/model/yaml_test.go`

- [ ] **Step 1: Write the failing YAML test**

Add a test decoding a YAML process whose node has `label: "Human Caption"`, asserting the built node's `Label()` returns it, and a node without `label` resolves to its `name`.

- [ ] **Step 2: Run — expect FAIL** (`label` not decoded → empty/fallback mismatch)

Run: `go test ./definition/model/ -run TestYAML.*Label`
Expected: FAIL.

- [ ] **Step 3: Implement**

Add `Label string `yaml:"label,omitempty"`` to the YAML node struct; set `Label: ny.Label` where the struct is projected into `NodeWire`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./definition/model/ -run TestYAML.*Label` → PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/yaml.go definition/model/yaml_test.go
git commit -m "feat(model): YAML label authoring key (ADR-0139)"
```

---

### Task 4: `activity.WithLabel`

**Files:**
- Modify: `definition/activity/options.go` (after the `nameOpt` block :52-63)
- Test: `definition/activity/options_test.go`

- [ ] **Step 1: Write the failing test**

Assert `WithLabel("L").Label() == "L"` across **all seven** activity constructors (table test per the `table-test` skill): `NewServiceTask`, `NewUserTask`, `NewReceiveTask`, `NewSendTask`, `NewBusinessRuleTask`, **and `NewSubProcess`, `NewCallActivity`**. The last two take `...ActivityOption` and route label through `labelOpt.applyName` (a *different* path from the five task kinds) — they MUST be in the table, because that path is the sole label channel for them and is where a silent-drop bug would hide. (`NewReceiveTask`/`NewSendTask` also need a `messageName` arg; `NewSubProcess` a `*model.ProcessDefinition`; `NewCallActivity` a `model.Qualifier` — supply minimal valid values.)

- [ ] **Step 2: Run — expect FAIL** (`WithLabel` undefined)

Run: `go test ./definition/activity/ -run Label`
Expected: FAIL.

- [ ] **Step 3: Implement — mirror `nameOpt`**

```go
type labelOpt struct{ label string }

func (o labelOpt) applyActivity(_ *model.ActivityFields) {}
func (o labelOpt) applyName(b *model.Base)               { b.SetLabel(o.label) }
func (o labelOpt) applyServiceTask(s *ServiceTask)       { s.SetLabel(o.label) }
func (o labelOpt) applyUserTask(u *UserTask)             { u.SetLabel(o.label) }
func (o labelOpt) applyReceiveTask(r *ReceiveTask)       { r.SetLabel(o.label) }
func (o labelOpt) applySendTask(s *SendTask)             { s.SetLabel(o.label) }
func (o labelOpt) applyBusinessRule(b *BusinessRuleTask) { b.SetLabel(o.label) }

// WithLabel sets the human display label on any activity node.
func WithLabel(label string) labelOpt { return labelOpt{label} }
```

**Critical:** `labelOpt.applyName` MUST call `b.SetLabel(o.label)` — model it on `nameOpt` (`options.go:54-60`), NOT on `activityOnlyOption` whose `applyName` is a deliberate no-op (`options.go:89`). `applyName` is the *only* label channel for `NewSubProcess`/`NewCallActivity` (which apply via `applyActivityOpts` → `applyActivity` + `applyName`); a no-op here silently drops their labels while the five task kinds still pass. `labelOpt` must implement all seven apply-methods so it satisfies every per-kind option interface AND `ActivityOption` (which requires `applyActivity` + `applyName`).

- [ ] **Step 4: Run — expect PASS**; **Step 5: Commit**

Run: `go test ./definition/activity/... -race` → PASS.
```bash
git add definition/activity/
git commit -m "feat(activity): WithLabel option (ADR-0139)"
```

---

### Task 5: `event.WithLabel` (+ throw variants)

**Files:**
- Modify: `definition/event/options.go` (`nameOpt` :32-46; throw setters :183-210)
- Test: `definition/event/options_test.go`

- [ ] **Step 1: Write the failing test**

Assert `WithLabel` sets the label on Start/Catch/End/Boundary, `WithThrowLabel` on `IntermediateThrowEvent`, and `WithCompensateThrowLabel` on `CompensationThrowEvent` — via `.Label()`.

- [ ] **Step 2: Run — expect FAIL**; Run: `go test ./definition/event/ -run Label` → FAIL.

- [ ] **Step 3: Implement — mirror `WithName`/`WithThrowName`**

```go
type labelOpt struct{ label string }

func (o labelOpt) applyStart(n *StartEvent)             { n.SetLabel(o.label) }
func (o labelOpt) applyCatch(n *IntermediateCatchEvent) { n.SetLabel(o.label) }
func (o labelOpt) applyEnd(n *EndEvent)                 { n.SetLabel(o.label) }
func (o labelOpt) applyBoundary(n *BoundaryEvent)       { n.SetLabel(o.label) }

// WithLabel sets the human display label on a start, end, catch, or boundary node.
func WithLabel(label string) interface {
	StartOption
	EndOption
	CatchOption
	BoundaryOption
} {
	return labelOpt{label}
}

// WithThrowLabel sets the label on an IntermediateThrowEvent.
func WithThrowLabel(label string) ThrowOption {
	return func(n *IntermediateThrowEvent) { n.SetLabel(label) }
}

// WithCompensateThrowLabel sets the label on a CompensationThrowEvent.
func WithCompensateThrowLabel(label string) CompensateThrowOption {
	return func(n *CompensationThrowEvent) { n.SetLabel(label) }
}
```

- [ ] **Step 4: Run — expect PASS**; **Step 5: Commit**

Run: `go test ./definition/event/... -race` → PASS.
```bash
git add definition/event/
git commit -m "feat(event): WithLabel + throw label options (ADR-0139)"
```

---

### Task 6: Gateways — functional options (breaking) + migrate call sites

**Files:**
- Modify: `definition/gateway/gateway.go` (constructors :47-67, drop `optName` :38-43, **update the stale package doc :6-8** — "carry no options beyond an optional name" → describe the `WithName`/`WithLabel` functional-options constructors)
- Modify: `definition/build/build.go` (`AddXGateway` :99-109)
- Migrate name-passing call sites: `definition/gateway/gateway_test.go:16,26`, `definition/model/accessors_test.go:172`, `internal/persistence/store/definitions_conformance_test.go:49`
- Test: `definition/gateway/gateway_test.go`

**Interfaces:**
- Produces: `gateway.Option`, `gateway.WithName(string) Option`, `gateway.WithLabel(string) Option`; `New{Exclusive,Parallel,Inclusive,EventBased}(id string, opts ...Option) model.Node`.

- [ ] **Step 1: Write the failing option test**

```go
func TestGatewayOptions(t *testing.T) {
	n := gateway.NewExclusive("x", gateway.WithName("Decision"), gateway.WithLabel("Approve?"))
	require.Equal(t, "Decision", n.Name())
	require.Equal(t, "Approve?", n.Label())

	// Unset label falls back to name.
	n2 := gateway.NewParallel("fork", gateway.WithName("Fork"))
	require.Equal(t, "Fork", n2.Label())

	// Bare id still valid (source-compatible for the 100+ id-only call sites).
	require.Equal(t, "", gateway.NewInclusive("i").Name())
}
```

- [ ] **Step 2: Run — expect FAIL** (`gateway.Option`/`WithName`/`WithLabel` undefined; and the old `"x","XOR"` two-string calls in gateway_test.go break the build)

Run: `go test ./definition/gateway/`
Expected: FAIL / build error.

- [ ] **Step 3: Implement the option constructors**

Replace `optName` + the four `New*` in `gateway.go`:

```go
// Option configures a gateway at construction.
type Option func(*model.Base)

// WithName sets the semantic name on a gateway.
func WithName(name string) Option { return func(b *model.Base) { b.SetName(name) } }

// WithLabel sets the human display label on a gateway.
func WithLabel(label string) Option { return func(b *model.Base) { b.SetLabel(label) } }

func newGateway(id string, opts ...Option) model.Base {
	b := model.NewBase(id, "")
	for _, o := range opts {
		o(&b)
	}
	return b
}

func NewExclusive(id string, opts ...Option) model.Node { return ExclusiveGateway{newGateway(id, opts...)} }
func NewParallel(id string, opts ...Option) model.Node  { return ParallelGateway{newGateway(id, opts...)} }
func NewInclusive(id string, opts ...Option) model.Node { return InclusiveGateway{newGateway(id, opts...)} }
func NewEventBased(id string, opts ...Option) model.Node { return EventBasedGateway{newGateway(id, opts...)} }
```

- [ ] **Step 4: Migrate the breaking call sites**

- `gateway_test.go:16,26`: `gateway.NewExclusive("x", "XOR")` → `gateway.NewExclusive("x", gateway.WithName("XOR"))`.
- `accessors_test.go:172`: `gateway.NewExclusive("xor", "Decision")` → `…, gateway.WithName("Decision")`.
- `definitions_conformance_test.go:49`: `gateway.NewExclusive("approve", "Approved?")` → `…, gateway.WithName("Approved?")`.
- `build.go` `AddXGateway`: change signature `(id string, name ...string)` → `(id string, opts ...gateway.Option)` and pass through: `return b.Add(gateway.NewExclusive(id, opts...))` (etc.). **This is itself a public Builder API break — CHANGELOG it.**
- Migrate `AddXGateway(id, "Name")` callers: `definition/build/build_test.go:46` `AddExclusiveGateway("gw", "Approved?")` → `AddExclusiveGateway("gw", gateway.WithName("Approved?"))`. (Confirmed the only name-passing `AddXGateway` caller; `runtime/definition_registry_test.go:530` and `build_test.go:75-77` are id-only and compile unchanged. Re-run the grep to be sure.)

- [ ] **Step 5: Run — expect PASS**

Run: `go test ./definition/... ./internal/persistence/store/... -race` → PASS.
Run: `go build ./...` → PASS (id-only gateway calls unaffected).

- [ ] **Step 6: Commit**

```bash
git add definition/gateway/ definition/build/ definition/model/accessors_test.go internal/persistence/store/definitions_conformance_test.go
git commit -m "feat(gateway): functional-options constructors with WithName/WithLabel (ADR-0139, breaking)"
```

---

### Task 7: Governance + full verification + Delivery Gate

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: CHANGELOG**

Unreleased/Breaking: gateway constructors moved from `New*(id, name ...string)` to `New*(id, ...gateway.Option)`, **and `Builder.AddXGateway(id, name ...string)` → `(id, ...gateway.Option)`**; use `gateway.WithName(...)`. Added: optional node `label` (`WithLabel`, `json:"label"`, YAML `label`) resolving to `name` when unset. `Name` re-documented as the semantic name.

- [ ] **Step 2: Full verification**

Run: `go test -race ./... && go tool cover -func=cover.out | tail -1` (≥85% touched pkgs).
Run: `golangci-lint run ./...` → clean.

- [ ] **Step 3: Fold into one feature bundle**

Squash Task-1–7 commits into a single bundle (impl + tests + ADR-0139 + spec + this plan + CHANGELOG):
```
feat(node): optional human label on every node (ADR-0139)
```

- [ ] **Step 4: Delivery Gate**

`/code-review` then `/security-review`; fold findings via `git commit --amend`; merge `--no-ff` to `main`.

---

## Self-Review

- **Spec coverage:** Base/Label/interface → Task 1; JSON wire → Task 2; YAML → Task 3; activity/event/gateway options → Tasks 4/5/6; `Name` re-doc → Task 1; governance → ADR (written) + Task 7. All mapped.
- **Type consistency:** `Label()`/`SetLabel()`/`rawLabel()`/`WithLabel`/`gateway.Option` names are consistent across tasks; wire key is `label` everywhere.
- **Placeholders:** none — every code step shows code. `mustDef`/existing-helper reuse is called out, not invented divergently.
- **Blast radius confirmed:** only ~4 name-passing gateway sites + `build.go` break; 100+ id-only sites compile unchanged. Stub-implementer sweep (Task 1 Step 5) guards the interface widening.
