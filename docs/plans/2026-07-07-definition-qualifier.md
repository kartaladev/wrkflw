# Definition Qualifier Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the string `DefRef` (format `"id"`=latest / `"id:version"`=pinned) with a typed `definition.Qualifier{ID string; Version int}` (`Version==0`=latest) everywhere a process definition is referenced.

**Architecture:** A new value type `model.Qualifier` (re-exported as `definition.Qualifier`) carries the id+version. The wire stays byte-identical — HTTP DTO fields serialize through `Qualifier`'s JSON/YAML marshalers; node-wire/YAML structs keep a `string` field converted in ToWire/FromWire. Persisted TEXT columns keep the joined string via `Qualifier.String()`/`ParseQualifier`. `DefinitionRegistry.Lookup` becomes `Lookup(ctx, Qualifier)`; registries branch on `IsLatest()` instead of double-indexing / string-parsing. Behavior-preserving; no schema migration.

**Tech Stack:** Go 1.25; `expr-lang/expr` unaffected; `modernc.org/sqlite`/pgx/database-sql via the neutral store+dialect; testcontainers for the SQL registry test.

## Global Constraints

- **Language:** Go 1.25 (hard requirement).
- **TDD strict:** No production code before a failing test. Every new exported symbol / behavioral change is preceded by a Bash `go test ./<package>/...` showing RED (a compile error like `undefined: model.Qualifier` is a valid red state). Never create test+impl in one edit pass with no `go test` between.
- **Type:** `Qualifier{ID string; Version int}`; `Version==0` means latest, `>=1` pinned. `Version 0` is a reserved sentinel — real definitions are `>=1`; `ParseQualifier` rejects `"id:0"`.
- **Wire is byte-identical** (D1): HTTP `def_ref`, node `defRef`, YAML `defRef` all keep the `"id"`/`"id:version"` string form. No HTTP body, YAML fixture, or wire-round-trip test changes its serialized bytes.
- **No schema migration** (D2): the `definition_ref` TEXT columns and watermill `definition_ref` metadata keep the joined string via `Qualifier.String()`/`ParseQualifier`.
- **Field names kept:** the `…DefRef` / `…DefinitionRef` field names are unchanged; only their types change.
- **Error sentinels:** message prefix `workflow-<package>:` (e.g. `workflow-model:` for `ParseQualifier`).
- **Module path:** `github.com/kartaladev/wrkflw`.
- **Coverage:** each touched package ≥ 85% line coverage. **Lint:** `golangci-lint run ./...` clean before any task is "done".
- **Docs:** ADRs use the Nygard template under `docs/adr/NNNN-<slug>.md`; next free number is **0101**.
- **Table tests:** use the project `table-test` skill's `assert` closure form. **DB tests:** use `database.RunTestDatabase(t, …)` (never a hand-rolled container).
- **Commits:** Conventional Commits scoped to area; commit per task. End every commit message with the two trailer lines:
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf
  ```

## File Structure

- `definition/model/qualifier.go` (**new**) — the `Qualifier` type, `Latest`/`Version`/`ParseQualifier`, `String`/`IsLatest`, JSON+YAML marshalers.
- `definition/model/qualifier_test.go` (**new**) — unit tests.
- `definition/model/definition.go` — add `(*ProcessDefinition).Qualifier()` helper.
- `definition/definition.go` — re-export `Qualifier`, `Latest`, `Version`, `ParseQualifier`.
- `runtime/kernel/definition_registry.go`, `mem_definition_registry.go`, `caching_definition_registry.go` — `Lookup(ctx, Qualifier)`; registries re-typed.
- `internal/persistence/store/definitions.go` — `Lookup(ctx, Qualifier)` branches on `IsLatest()`.
- Field/param swaps: `definition/activity/activity.go`, `definition/build/build.go`, `service/request.go`, `engine/command.go`, `runtime/kernel/{ports,chainlink,opsstats}.go`, `runtime/chain/chainer.go`.
- Boundary conversions: `runtime/outbox.go`, `runtime/chain/chainer.go`, `internal/persistence/store/*`, `internal/eventing/watermill/publisher.go`, `transport/http/httpcore/{dto,endpoints,admin_endpoints}.go`, `definition/model/{node_wire,yaml}.go`.
- `docs/adr/0101-definition-qualifier.md` (**new**), `CHANGELOG.md`.

---

### Task 1: `Qualifier` type + constructors + parse/format + JSON/YAML + `ProcessDefinition.Qualifier()`

**Files:**
- Create: `definition/model/qualifier.go`, `definition/model/qualifier_test.go`
- Modify: `definition/model/definition.go` (add `Qualifier()` method), `definition/definition.go` (re-exports)

**Interfaces:**
- Produces:
  - `model.Qualifier struct { ID string; Version int }`
  - `model.Latest(id string) Qualifier`; `model.Version(id string, v int) Qualifier`
  - `(Qualifier) IsLatest() bool`; `(Qualifier) String() string`
  - `model.ParseQualifier(s string) (Qualifier, error)`; `model.ErrInvalidQualifier` sentinel
  - `(Qualifier) MarshalJSON`/`(*Qualifier) UnmarshalJSON`; `(Qualifier) MarshalYAML`/`(*Qualifier) UnmarshalYAML`
  - `(*ProcessDefinition) Qualifier() Qualifier`
  - Re-exports: `definition.Qualifier`, `definition.Latest`, `definition.Version`, `definition.ParseQualifier`

- [ ] **Step 1: Write the failing test**

`definition/model/qualifier_test.go`:
```go
package model_test

import (
	"encoding/json"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kartaladev/wrkflw/definition/model"
)

func TestQualifierConstructorsAndString(t *testing.T) {
	cases := []struct {
		name     string
		q        model.Qualifier
		isLatest bool
		str      string
	}{
		{"latest", model.Latest("order"), true, "order"},
		{"pinned", model.Version("order", 3), false, "order:3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert := func(cond bool, msg string) {
				t.Helper()
				if !cond {
					t.Fatalf("%s (q=%+v)", msg, tc.q)
				}
			}
			assert(tc.q.IsLatest() == tc.isLatest, "IsLatest mismatch")
			assert(tc.q.String() == tc.str, "String mismatch: got "+tc.q.String())
		})
	}
}

func TestParseQualifier(t *testing.T) {
	cases := []struct {
		in      string
		want    model.Qualifier
		wantErr bool
	}{
		{"order", model.Latest("order"), false},
		{"order:3", model.Version("order", 3), false},
		{"", model.Qualifier{}, true},
		{":3", model.Qualifier{}, true},
		{"order:", model.Qualifier{}, true},
		{"order:x", model.Qualifier{}, true},
		{"order:-1", model.Qualifier{}, true},
		{"order:0", model.Qualifier{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert := func(cond bool, msg string) {
				t.Helper()
				if !cond {
					t.Fatalf("%s (in=%q)", msg, tc.in)
				}
			}
			got, err := model.ParseQualifier(tc.in)
			if tc.wantErr {
				assert(err != nil, "expected error")
				assert(errors.Is(err, model.ErrInvalidQualifier), "expected ErrInvalidQualifier")
				return
			}
			assert(err == nil, "unexpected error")
			assert(got == tc.want, "parse mismatch")
		})
	}
}

func TestQualifierJSONRoundTrip(t *testing.T) {
	for _, q := range []model.Qualifier{model.Latest("order"), model.Version("order", 3)} {
		b, err := json.Marshal(q)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if want := `"` + q.String() + `"`; string(b) != want {
			t.Fatalf("json = %s, want %s", b, want)
		}
		var back model.Qualifier
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back != q {
			t.Fatalf("round-trip: got %+v want %+v", back, q)
		}
	}
}

func TestQualifierYAMLRoundTrip(t *testing.T) {
	type holder struct {
		Ref model.Qualifier `yaml:"ref"`
	}
	for _, q := range []model.Qualifier{model.Latest("order"), model.Version("order", 3)} {
		b, err := yaml.Marshal(holder{Ref: q})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var back holder
		if err := yaml.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back.Ref != q {
			t.Fatalf("yaml round-trip: got %+v want %+v", back.Ref, q)
		}
	}
}

func TestProcessDefinitionQualifier(t *testing.T) {
	def := &model.ProcessDefinition{ID: "order", Version: 3}
	if got := def.Qualifier(); got != model.Version("order", 3) {
		t.Fatalf("def.Qualifier() = %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/model/ -run 'Qualifier'`
Expected: FAIL — `undefined: model.Qualifier` / `model.Latest` / `model.ParseQualifier` / `ErrInvalidQualifier`.

- [ ] **Step 3: Write minimal implementation**

`definition/model/qualifier.go`:
```go
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidQualifier is returned by ParseQualifier for a malformed reference.
var ErrInvalidQualifier = errors.New("workflow-model: invalid qualifier")

// Qualifier references a process definition: a specific version when Version >= 1,
// or the latest registered version when Version == 0.
type Qualifier struct {
	ID      string
	Version int
}

// Latest returns a Qualifier that resolves the newest registered version of id.
func Latest(id string) Qualifier { return Qualifier{ID: id} }

// Version returns a Qualifier pinned to (id, v).
func Version(id string, v int) Qualifier { return Qualifier{ID: id, Version: v} }

// IsLatest reports whether q resolves the newest version (Version == 0).
func (q Qualifier) IsLatest() bool { return q.Version == 0 }

// String renders q as "id" (latest) or "id:version" (pinned). It is the inverse
// of ParseQualifier for all valid qualifiers.
func (q Qualifier) String() string {
	if q.IsLatest() {
		return q.ID
	}
	return q.ID + ":" + strconv.Itoa(q.Version)
}

// ParseQualifier is the inverse of String. "id" -> latest; "id:version" -> pinned.
// It rejects an empty id, an empty/non-numeric/negative version, and ":0"
// (Version 0 is the reserved latest sentinel; express latest as bare "id").
func ParseQualifier(s string) (Qualifier, error) {
	id, verStr, hasColon := strings.Cut(s, ":")
	if id == "" {
		return Qualifier{}, fmt.Errorf("%w: empty id in %q", ErrInvalidQualifier, s)
	}
	if !hasColon {
		return Qualifier{ID: id}, nil
	}
	v, err := strconv.Atoi(verStr)
	if err != nil {
		return Qualifier{}, fmt.Errorf("%w: bad version in %q: %v", ErrInvalidQualifier, s, err)
	}
	if v < 1 {
		return Qualifier{}, fmt.Errorf("%w: version must be >= 1 in %q", ErrInvalidQualifier, s)
	}
	return Qualifier{ID: id, Version: v}, nil
}

// MarshalJSON emits the String form (a JSON string), keeping the wire byte-identical.
func (q Qualifier) MarshalJSON() ([]byte, error) { return json.Marshal(q.String()) }

// UnmarshalJSON parses a JSON string via ParseQualifier.
func (q *Qualifier) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := ParseQualifier(s)
	if err != nil {
		return err
	}
	*q = parsed
	return nil
}

// MarshalYAML emits the String form.
func (q Qualifier) MarshalYAML() (any, error) { return q.String(), nil }

// UnmarshalYAML parses a YAML scalar string via ParseQualifier.
func (q *Qualifier) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	parsed, err := ParseQualifier(s)
	if err != nil {
		return err
	}
	*q = parsed
	return nil
}
```

In `definition/model/definition.go`, add (near the `ProcessDefinition` type):
```go
// Qualifier returns a Qualifier pinned to this definition's exact ID and Version.
func (d *ProcessDefinition) Qualifier() Qualifier { return Qualifier{ID: d.ID, Version: d.Version} }
```

In `definition/definition.go`, add re-exports (matching the existing `NewBuilder` forwarding style):
```go
// Qualifier references a process definition by id and version (0 == latest).
type Qualifier = model.Qualifier

// Latest returns a Qualifier resolving the newest registered version of id.
func Latest(id string) Qualifier { return model.Latest(id) }

// Version returns a Qualifier pinned to (id, v).
func Version(id string, v int) Qualifier { return model.Version(id, v) }

// ParseQualifier parses "id" or "id:version" into a Qualifier.
func ParseQualifier(s string) (Qualifier, error) { return model.ParseQualifier(s) }
```
(Confirm `definition/definition.go` already imports `definition/model`; it does for `NewBuilder`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/model/ -run 'Qualifier' && go build ./definition/...`
Expected: PASS, build clean.

- [ ] **Step 5: Commit**

```bash
git add definition/model/qualifier.go definition/model/qualifier_test.go definition/model/definition.go definition/definition.go
git commit -m "feat(definition): Qualifier type (typed DefRef) with string wire form"
```

---

### Task 2: `DefinitionRegistry.Lookup(ctx, Qualifier)` — both interfaces + 4 impls

**Files:**
- Modify: `runtime/kernel/definition_registry.go` (interface + `MapDefinitionRegistry`), `runtime/kernel/mem_definition_registry.go` (`MemDefinitionRegistry`), `runtime/kernel/caching_definition_registry.go` (`CachingDefinitionRegistry`), `internal/persistence/store/definitions.go` (`DefinitionStore`), `persistence/persistence.go` (the second `Lookup` port declaration)
- Test: `runtime/kernel/definition_registry_test.go` (extend), `internal/persistence/store/definitions_conformance_test.go` (extend — grep for the existing definition test file name first)

**Interfaces:**
- Consumes: `model.Qualifier`, `def.Qualifier()`, `model.Latest` (Task 1).
- Produces: `DefinitionRegistry.Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error)` on all impls.

**Design notes (apply to the impls below):**
- `MapDefinitionRegistry` / `MemDefinitionRegistry`: change the map to `map[model.Qualifier]*model.ProcessDefinition` (Qualifier is comparable). Index each def under BOTH its pinned key `def.Qualifier()` and the latest key `model.Latest(def.ID)` (the latest key overwritten to the newest-registered def, preserving today's bare-id semantics). `Lookup(q)` is a single `m[q]`.
- `CachingDefinitionRegistry`: keep the internal `map[string]cacheEntry` and `singleflight.Group` (singleflight needs a string key) but derive the key from `q.String()`; delegate `backing.Lookup(ctx, q)`.
- `DefinitionStore`: branch on `q.IsLatest()` → `ORDER BY version DESC LIMIT 1` (using `q.ID`), else `GetDefinition(ctx, q.ID, q.Version)`.

- [ ] **Step 1: Write the failing test**

Extend `runtime/kernel/definition_registry_test.go` (grep the file for existing helpers; add):
```go
func TestMemRegistryLookupByQualifier(t *testing.T) {
	reg := kernel.NewMemDefinitionRegistry()
	v1 := &model.ProcessDefinition{ID: "order", Version: 1}
	v2 := &model.ProcessDefinition{ID: "order", Version: 2}
	if err := reg.Register(v1); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(v2); err != nil {
		t.Fatal(err)
	}

	assert := func(q model.Qualifier, want *model.ProcessDefinition) {
		t.Helper()
		got, err := reg.Lookup(t.Context(), q)
		if err != nil {
			t.Fatalf("lookup %s: %v", q, err)
		}
		if got != want {
			t.Fatalf("lookup %s = %+v, want %+v", q, got, want)
		}
	}
	assert(model.Version("order", 1), v1)
	assert(model.Version("order", 2), v2)
	assert(model.Latest("order"), v2) // latest == newest registered

	if _, err := reg.Lookup(t.Context(), model.Version("order", 9)); !errors.Is(err, kernel.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/kernel/ -run 'LookupByQualifier'`
Expected: FAIL — `Lookup` still takes a string (`cannot use q (model.Qualifier) as string`).

- [ ] **Step 3: Write minimal implementation**

`runtime/kernel/definition_registry.go` — interface + `MapDefinitionRegistry`:
```go
type DefinitionRegistry interface {
	Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error)
}

type MapDefinitionRegistry struct {
	m map[model.Qualifier]*model.ProcessDefinition
}

// NewMapDefinitionRegistry indexes each non-nil definition under both its pinned
// Qualifier (def.Qualifier()) and its latest Qualifier (Latest(def.ID)); the
// latest key resolves to the highest version seen.
func NewMapDefinitionRegistry(defs ...*model.ProcessDefinition) *MapDefinitionRegistry {
	m := make(map[model.Qualifier]*model.ProcessDefinition, len(defs)*2)
	for _, d := range defs {
		if d == nil {
			continue
		}
		m[d.Qualifier()] = d
		latest := model.Latest(d.ID)
		if cur, ok := m[latest]; !ok || d.Version >= cur.Version {
			m[latest] = d
		}
	}
	return &MapDefinitionRegistry{m: m}
}

func (r *MapDefinitionRegistry) Lookup(_ context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	def, ok := r.m[q]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDefinitionNotFound, q)
	}
	return def, nil
}
```
> **Constructor signature change:** `NewMapDefinitionRegistry` moves from `map[string]*ProcessDefinition` to `...*ProcessDefinition` (variadic). This removes caller-side double-indexing. Fix every caller in the compile sweep (the harness at `internal/transporttest/harness.go:86` and any test). If a caller genuinely needs the map form, keep a `map[model.Qualifier]*…` overload — but prefer the variadic; grep `NewMapDefinitionRegistry(` to enumerate callers.

`runtime/kernel/mem_definition_registry.go` — `Register` + `Lookup`:
```go
type MemDefinitionRegistry struct {
	mu sync.RWMutex
	m  map[model.Qualifier]*model.ProcessDefinition
}

func NewMemDefinitionRegistry() *MemDefinitionRegistry {
	return &MemDefinitionRegistry{m: make(map[model.Qualifier]*model.ProcessDefinition)}
}

func (r *MemDefinitionRegistry) Register(def *model.ProcessDefinition) error {
	if def == nil {
		return ErrNilDefinition
	}
	if def.ID == "" {
		return ErrEmptyDefinitionID
	}
	pinned := def.Qualifier()
	latest := model.Latest(def.ID)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.m[pinned]; exists {
		return fmt.Errorf("%w: %q", ErrDefinitionExists, pinned)
	}
	r.m[pinned] = def
	r.m[latest] = def // overwrite latest key to newest registered
	return nil
}

func (r *MemDefinitionRegistry) Lookup(_ context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.m[q]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDefinitionNotFound, q)
	}
	return def, nil
}
```
> Update the `ErrDefinitionExists` doc comment (it references `"<ID>:<Version>"` — still accurate via `Qualifier.String()`).

`runtime/kernel/caching_definition_registry.go` — signature + string-keyed internals:
```go
func (c *CachingDefinitionRegistry) Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	key := q.String()
	now := c.clk.Now()

	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Before(e.expiresAt) {
		c.mu.Unlock()
		return e.def, nil
	}
	c.mu.Unlock()

	v, err, _ := c.group.Do(key, func() (any, error) {
		now := c.clk.Now()
		c.mu.Lock()
		if e, ok := c.entries[key]; ok && now.Before(e.expiresAt) {
			c.mu.Unlock()
			return e.def, nil
		}
		c.mu.Unlock()

		def, err := c.backing.Lookup(ctx, q)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.entries[key] = cacheEntry{def: def, expiresAt: c.clk.Now().Add(c.ttl)}
		c.mu.Unlock()
		return def, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*model.ProcessDefinition), nil
}
```

`internal/persistence/store/definitions.go` — `Lookup` (drop `strings`/`strconv` if now unused):
```go
func (ds *DefinitionStore) Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	if !q.IsLatest() {
		return ds.GetDefinition(ctx, q.ID, q.Version)
	}

	dbq := ds.querier(ctx)
	var data []byte
	err := dbq.QueryRow(ctx, ds.dialect.Rebind(
		`SELECT definition FROM wrkflw_definitions
		 WHERE def_id = ?
		 ORDER BY version DESC
		 LIMIT 1`),
		q.ID,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", kernel.ErrDefinitionNotFound, q)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-store: lookup %q: %w", q, err)
	}
	var def model.ProcessDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("workflow-store: lookup %q: unmarshal: %w", q, err)
	}
	return &def, nil
}
```

`persistence/persistence.go:61` — change the port declaration's `Lookup` signature to `Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error)` (import `definition/model` if not already). Update the doc mentions at `persistence/mysql.go:334`, `persistence/sqlite.go:274` if they show the old signature.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./runtime/kernel/ -run 'Lookup' && go build ./runtime/... ./internal/persistence/... ./persistence/...`
Expected: PASS; note the driver/service callers still won't build until Task 3 — restrict the build to the packages above, OR proceed if they already compile (Lookup callers move in Task 3). If cross-package build breaks here, that is expected; the commit for this task stages only the registry files and their tests.

- [ ] **Step 5: SQL registry conformance (3 dialects)**

Add a `Lookup`-by-Qualifier case (latest + pinned + not-found) to the existing definition-store conformance test (grep `internal/persistence/store` for the file that tests `GetDefinition`/`DefinitionStore`; reuse its `forEachDialect`/`RunTestDatabase` harness). Run: `go test ./internal/persistence/store/ -run 'Definition'`.
Expected: PASS on sqlite (+ pg/mysql when Docker is up).

- [ ] **Step 6: Commit**

```bash
git add runtime/kernel/definition_registry.go runtime/kernel/mem_definition_registry.go runtime/kernel/caching_definition_registry.go runtime/kernel/definition_registry_test.go internal/persistence/store/definitions.go internal/persistence/store/*definition*_test.go persistence/persistence.go persistence/mysql.go persistence/sqlite.go
git commit -m "refactor(kernel): DefinitionRegistry.Lookup takes Qualifier; drop string parse/double-index"
```

---

### Task 3: Field / parameter / producer swaps (compile-driven sweep of domain + call sites)

**Files (modify):**
- Domain fields: `definition/activity/activity.go:96` (`CallActivity.DefRef`), `engine/command.go:156` (`StartSubInstance.DefRef`), `service/request.go:14,37` (`StartInstanceRequest.DefRef`, `DeliverMessageRequest.DefRef`), `runtime/kernel/ports.go:43` (`OutboxEvent.DefinitionRef`), `runtime/kernel/chainlink.go:34,37`, `runtime/kernel/opsstats.go:66` (`ChainLinkRef.DefinitionRef`), `runtime/chain/chainer.go:33` (`ChainEvent.PredecessorDefinitionRef`)
- Constructors: `definition/activity/activity.go:160` (`NewCallActivity`), `definition/build/build.go:116` (`AddCallActivity`)
- `fmt.Sprintf` producers → typed: `runtime/outbox.go:14`, `runtime/chain/chainer.go:228`, `runtime/calllink/notifier.go:189`, `runtime/processdriver_cancel.go:63`, `runtime/timerops.go:97`, `service/service.go:296,422`
- `.Lookup(...)` callers pass a Qualifier: `runtime/calllink/notifier.go:190`, `runtime/processdriver_action.go:273`, `runtime/processdriver_cancel.go:64`, `runtime/timerops.go:98`, `service/service.go:272,296,326,423`

**Interfaces:**
- Consumes: `model.Qualifier`, constructors, `def.Qualifier()` (Task 1); `Lookup(ctx, Qualifier)` (Task 2).
- Produces: all domain def-ref fields typed `Qualifier`; `NewCallActivity(id string, ref Qualifier, …)`; `AddCallActivity(id string, ref Qualifier, …)`.

> This task is **compile-driven**. Change the field/param/producer types (below), then `go build ./... 2>&1` and fix each error. The wire/store CONVERSIONS (marshalers, `String()`/`ParseQualifier` at the DB/wire boundary) are Task 4 — but some conversions are unavoidable here to compile (e.g. a producer that fed a string field). Where a string is still required at a boundary you haven't reached, use `q.String()`; where a string must become a Qualifier, use the split source (`def.Qualifier()`, `Version(id, v)`, `Qualifier{ParentDefID, ParentDefVersion}`) or `model.ParseQualifier` (handle its error).

- [ ] **Step 1: Write/adjust the failing test (representative behavior)**

Pick the call-activity constructor as the red anchor. In `definition/activity/activity_test.go` (grep for the existing CallActivity test; if none, add one), assert the typed constructor:
```go
func TestNewCallActivityQualifier(t *testing.T) {
	n := activity.NewCallActivity("call", definitionModelVersion("order", 2)) // helper -> model.Version
	ca, ok := n.(activity.CallActivity)
	if !ok {
		t.Fatalf("want CallActivity, got %T", n)
	}
	if ca.DefRef != model.Version("order", 2) {
		t.Fatalf("DefRef = %+v", ca.DefRef)
	}
}
```
(Use `model.Version(...)` directly; drop the helper note. Import `definition/model`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/activity/ -run 'CallActivityQualifier'`
Expected: FAIL — `NewCallActivity` still takes a string; `ca.DefRef` is a string.

- [ ] **Step 3: Change the domain types + constructors**

- `definition/activity/activity.go`: `CallActivity.DefRef model.Qualifier`; `NewCallActivity(id string, ref model.Qualifier, opts ...ActivityOption) model.Node { n := CallActivity{Base: model.NewBase(id, ""), DefRef: ref}; … }`.
- `definition/build/build.go:116` `AddCallActivity(id string, ref model.Qualifier, opts …)` forwarding `activity.NewCallActivity(id, ref, opts...)`.
- `service/request.go`: `StartInstanceRequest.DefRef model.Qualifier`; `DeliverMessageRequest.DefRef model.Qualifier` (import `definition/model`). Update the field doc comments to say "process-definition reference (id or id:version)".
- `engine/command.go:156`: `StartSubInstance.DefRef model.Qualifier`.
- `runtime/kernel/ports.go:43`, `chainlink.go:34,37`, `opsstats.go:66`, `runtime/chain/chainer.go:33`: the `…DefinitionRef` fields become `model.Qualifier` (names kept).

- [ ] **Step 4: Compile-drive the call sites**

Run `go build ./... 2>&1 | head -80` and fix each error:
- Producers building `"%s:%d"` for a now-`Qualifier` field → `def.Qualifier()` (from a `*ProcessDefinition`), `model.Version(st.DefID, st.DefVersion)` (from split state fields), or `model.Qualifier{ID: p.Link.ParentDefID, Version: p.Link.ParentDefVersion}` (notifier). Concretely: `runtime/outbox.go:14`, `runtime/chain/chainer.go:228`, `runtime/calllink/notifier.go:189`, `runtime/processdriver_cancel.go:63`, `runtime/timerops.go:97`, `service/service.go:296,422`.
- `.Lookup(ctx, someString)` callers → pass the Qualifier directly (the same value the producer built). e.g. `service/service.go:272` (`req.DefRef` is now a Qualifier), `:296`/`:422` (`model.Version(st.DefID, st.DefVersion)`), `:326` (`req.DefRef` for message), `runtime/processdriver_action.go:273`, `runtime/processdriver_cancel.go:64`, `runtime/timerops.go:98`, `runtime/calllink/notifier.go:190`.
- Where a field written to a TEXT column / wire is still a string in this task (store/wire not yet converted), assign `q.String()`; Task 4 relocates the conversion to the boundary if cleaner. Prefer leaving the domain field as `Qualifier` and converting only at the store/wire write.

- [ ] **Step 5: Run build + touched unit tests**

Run: `go build ./... && go test ./definition/... ./engine/... ./runtime/... ./service/... 2>&1 | tail -30`
Expected: build clean; failures at this point are wire/store boundary conversions handled in Task 4 and test-literal churn — if a package fails ONLY on test files, note it and defer those test edits to Task 4's sweep; production code must build.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(definition): def-ref fields, constructors, and producers use Qualifier"
```
(The scratch ledger under `.superpowers/` is gitignored; `git status` first and confirm only source/test files are staged.)

---

### Task 4: Boundary conversions (wire + store + metadata) and the test-literal sweep

**Files (modify):**
- Wire: `transport/http/httpcore/dto.go:13,27` (`StartInput`/`MessageInput.DefRef` → `Qualifier`), `endpoints.go:29,98` (mapping — now identity), `admin_endpoints.go:372,414,422` (lineage `definition_ref` via `String()`), `definition/model/node_wire.go:39` (stays `string`) + `definition/model/yaml.go:41,113` (stays `string`) + CallActivity `ToWire`/`FromWire` in `definition/activity/activity.go:245,249`
- Store: `runtime/outbox.go` (write `q.String()`), `internal/persistence/store/*` chain/outbox read+write (`ParseQualifier`/`String`), `internal/eventing/watermill/publisher.go:106`, `eventing/chaining.go:61`
- Validate: `definition/model/validate.go` (call-activity def-ref required check — verify a zero `Qualifier` marshals to `""` and still fails required)
- Tests: the ~41 files referencing `def_ref`/`DefRef` (construction-site string literals → `definition.Version(...)`/`definition.Latest(...)`; wire-body strings unchanged)

**Interfaces:**
- Consumes: `Qualifier` marshalers, `String()`, `ParseQualifier` (Task 1); typed fields (Task 3).
- Produces: byte-identical wire + unchanged DB rows; all tests green.

**HTTP DTO fields** (`StartInput.DefRef`, `MessageInput.DefRef`) become `model.Qualifier`. The `json:"def_ref" validate:"required"` tag is unchanged; `Qualifier`'s JSON marshalers keep the wire a string. `endpoints.go` mapping `service.StartInstanceRequest{DefRef: in.DefRef, …}` becomes an identity assignment (both are `Qualifier` now). **Validation:** `validate:"required"` on a `Qualifier` — go-playground validates the struct field; a zero `Qualifier{}` is not "required-empty" by default (it's a struct). Add a small check: either keep `def_ref` required by validating the un/marshaled string is non-empty in `endpoints.go` before calling the service, or add a `Validate()` on the DTO that rejects `in.DefRef.ID == ""`. Implement the DTO-level `ID==""` guard and keep the existing `ErrMissingDefRef` 400 behavior (verify with the existing "missing def_ref → 400" transport tests).

**Node wire / YAML** keep a `string` field (`node_wire.go:39`, `yaml.go:41`). CallActivity `ToWire` sets `w.DefRef = v.DefRef.String()`; `FromWire` sets `DefRef` by parsing: since `FromWire` has no error return, use `ref, _ := model.ParseQualifier(w.DefRef)` and rely on `validate.go` to reject an invalid/empty call-activity ref. YAML hydration at `yaml.go:113` (`DefRef: ny.DefRef`) similarly parses the string into the `Qualifier` field.

- [ ] **Step 1: Write the failing wire round-trip guard**

Add to `transport/http/httpcore/dto_test.go` (or the wire test) a case asserting the DTO still (de)serializes `def_ref` as the string form:
```go
func TestStartInputDefRefWireString(t *testing.T) {
	body := []byte(`{"def_ref":"order:3","vars":{}}`)
	var in httpcore.StartInput
	if err := json.Unmarshal(body, &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if in.DefRef != model.Version("order", 3) {
		t.Fatalf("DefRef = %+v", in.DefRef)
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"def_ref":"order:3"`) {
		t.Fatalf("wire not string-form: %s", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./transport/http/httpcore/ -run 'DefRefWireString'`
Expected: FAIL — `StartInput.DefRef` is still a string / undefined field type mismatch.

- [ ] **Step 3: Convert the boundaries**

Apply the DTO/type/ToWire/FromWire/YAML/store changes above. For the store: at every site that writes `OutboxEvent.DefinitionRef` / `ChainLink.*DefinitionRef` to SQL, write `q.String()`; at every hydration/scan site, `ParseQualifier(scanned)` (handle the error → wrap `workflow-store:`). Watermill metadata `Set("definition_ref", q.String())`; the read side `ParseQualifier`. Grep `definition_ref` and `DefinitionRef` in `internal/persistence/store` and `internal/eventing` to enumerate the exact write/read sites.

- [ ] **Step 4: Sweep the test literals**

Run `go build ./... && go vet ./... 2>&1 | head` then `go test ./... 2>&1 | grep -E "FAIL|cannot use" | head -40`. For each failing test literal:
- `service.StartInstanceRequest{DefRef: "order:1", …}` → `DefRef: definition.Version("order", 1)` (or `definition.Latest("order")` for the bare form). Same for `DeliverMessageRequest`, `StartSubInstance`, `NewCallActivity`, `AddCallActivity`, and the `…DefinitionRef` struct literals in kernel/chain conformance tests.
- HTTP request BODY strings (`map[string]any{"def_ref": "order:1"}` and JSON `"def_ref":"order:1"`) — **unchanged** (wire is still a string).
- `NewMapDefinitionRegistry(map[string]…)` callers → variadic `NewMapDefinitionRegistry(def1, def2)`; the harness at `internal/transporttest/harness.go:86` drops its `fmt.Sprintf` double-index.
The high-traffic test files (expect churn): `transport/http/httpcore/endpoints_test.go`, `service/service_test.go`, `internal/persistence/store/chainlink_conformance_test.go`, `runtime/kernel/*_test.go`, `runtime/chain/chainer_test.go`, `runtime/outbox_test.go`, `runtime/calllink/notifier_test.go`, plus the transport `*_test.go` (mostly wire-body strings = unchanged).

- [ ] **Step 5: Full build + race + boundary verification**

Run: `go build ./... && go test ./... 2>&1 | grep -E "FAIL|^ok" | tail -40`
Expected: all `ok`. Then confirm no stray string def-ref parsing remains in domain code: `grep -rn --include='*.go' 'strings.Cut(.*":"' runtime/ service/ internal/ | grep -i def` should be empty (only `ParseQualifier` parses now).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(transport,store): convert def-ref at wire/store boundary via Qualifier; sweep tests"
```

---

### Task 5: ADR-0101 + CHANGELOG

**Files:**
- Create: `docs/adr/0101-definition-qualifier.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Write the ADR (Nygard, match `docs/adr/0100-server-generated-instance-id.md` shape)**

`docs/adr/0101-definition-qualifier.md`: `# 0101. Definition Qualifier: typed DefRef`, `Status: **Accepted — 2026-07-07.**`, `## Context`, `## Decision` (D0 type; D1 string wire via Marshal; D2 TEXT storage via String(); D3 Lookup(ctx,Qualifier) + registry re-typing; D4 field/param/producer swaps, names kept; D5 boundary-only conversion), `## Consequences` (positive: typed, single latest-vs-pinned locus, no fmt.Sprintf joins, wire+schema unchanged; negative: breaking Go API — Lookup signature + field/param types, pre-v0.1.0 acceptable; Version-0-as-latest sentinel; ~41 test files swept). Verify each claim against the shipped code before writing.

- [ ] **Step 2: Update CHANGELOG**

Read `CHANGELOG.md`; under **Breaking changes**: `DefinitionRegistry.Lookup(ctx, defRef string)` → `Lookup(ctx, Qualifier)`; def-ref fields/params/constructors (`StartInstanceRequest.DefRef`, `DeliverMessageRequest.DefRef`, `CallActivity.DefRef`, `NewCallActivity`, `AddCallActivity`, `StartSubInstance.DefRef`, the `…DefinitionRef` chain/outbox fields) now typed `definition.Qualifier`; wire (`def_ref`) and DB columns unchanged. Under **Added**: `definition.Qualifier`/`Latest`/`Version`/`ParseQualifier`. Reference ADR-0101.

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0101-definition-qualifier.md CHANGELOG.md
git commit -m "docs(adr): 0101 definition Qualifier (typed DefRef)"
```

---

### Task 6: Final verification

- [ ] **Step 1: Full race + coverage**

Run: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`
Expected: PASS, no data races. Confirm `definition/model` ≥ 85% and no regression in `runtime/kernel`, `internal/persistence/store`, `service`, `transport/...`.

- [ ] **Step 2: Lint + vet**

Run: `golangci-lint run ./... && go vet ./...`
Expected: no findings.

- [ ] **Step 3: Wire/schema-invariance guards**

Run:
```bash
grep -rn --include='*.go' 'strconv.Atoi' internal/persistence/store/definitions.go || echo "no manual version parse in store"
grep -rn --include='*.go' 'fmt.Sprintf("%s:%d"' runtime/ service/ internal/ || echo "no id:version fmt.Sprintf producers remain"
```
Expected: both "clean" lines (all joins now go through `Qualifier.String()`; all parses through `ParseQualifier`).

- [ ] **Step 4: Commit any fixes**

```bash
git add -A
git commit -m "test(definition): close verification gaps for Qualifier refactor"
```

---

## Self-Review

**Spec coverage:**
- `Qualifier` type + constructors + `String`/`ParseQualifier` + JSON/YAML + `ProcessDefinition.Qualifier()` → Task 1. ✓
- D1 string wire (byte-identical) → Task 1 (marshalers) + Task 4 (DTO/node-wire conversions). ✓
- D2 TEXT storage via `String()`/`ParseQualifier` → Task 4. ✓
- D3 `Lookup(ctx, Qualifier)` across both interface decls + 4 impls (drop double-index/string-parse) → Task 2. ✓
- D4 field/param/producer swaps, names kept → Task 3. ✓
- D5 boundary-only conversion (wire/store/metadata/admin) → Task 4. ✓
- Testing (unit Qualifier, per-registry latest/pinned, 3-dialect SQL, behavior preservation) → Tasks 1,2,4,6. ✓
- ADR-0101 + CHANGELOG → Task 5. ✓

**Placeholder scan:** No "TBD"/"add validation"/"similar to Task N". The compile-driven sweeps (Tasks 3–4) give the exact transformation recipe + exhaustive site lists from the 2026-07-07 recon; implementers re-grep exact line numbers (idgen renumbered some) — this is grounding, not a placeholder.

**Type consistency:** `model.Qualifier`/`Latest`/`Version`/`ParseQualifier`/`IsLatest`/`String` stable across all tasks. `Lookup(ctx, model.Qualifier) (*model.ProcessDefinition, error)` identical on the interface and all four impls. `NewMapDefinitionRegistry(...*ProcessDefinition)` variadic form consistent between Task 2 (definition) and Task 4 (harness sweep). Field names unchanged everywhere (type-only), per the resolved micro-decision.
