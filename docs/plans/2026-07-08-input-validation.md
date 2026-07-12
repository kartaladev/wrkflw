# Optional External-Input Validation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional, definition-declared validation of external `map[string]any` input at the three boundaries where a caller's payload is merged into instance variables (process start, human-task completion, message delivery), rejecting invalid input before any state mutation.

**Architecture:** A neutral `validation` port (`Validator`/`ValidationStrategy`/`DescribableStrategy`/`ValidationDescriptor`/`Registry`) plus a shared executor-side memoizing `Gate`. Four opt-in adapter packages (`expr`, `callback`, `jsonschema`, `avro`) implement strategies behind the port — the definition/engine core imports no schema library. Validation follows the **"input-owner validates"** rule: the driver validates start vars (`Drive`) and message payloads (`DeliverMessage`); the task service validates completion output (`Complete`, beside authz). Declarative strategies round-trip through wire/YAML via a `{kind, schema}` descriptor and a Loader-threaded `Registry`; a non-serializable callback strategy makes `MarshalJSON` fail-closed.

**Tech Stack:** Go 1.25; `github.com/expr-lang/expr` (existing); NEW deps behind adapters — `github.com/santhosh-tekuri/jsonschema/v6` + `github.com/invopop/jsonschema` (ADR-0111), `github.com/linkedin/goavro/v2` (ADR-0112). Persistence via the neutral `internal/persistence/store` + `dialect` (Postgres/MySQL/SQLite). goose migrations. testcontainers for DB tests.

## Global Constraints

- **Module path:** `github.com/kartaladev/wrkflw`. Public packages at repo root (no `pkg/` prefix, ADR-0004).
- **Go 1.25**; `go build ./...`, `go test -race ./...`, `golangci-lint run ./...` must all be clean before a task is done.
- **TDD strict (CLAUDE.md):** every new exported symbol gets a failing test FIRST, verified red via `go test`, before the implementation. The red state must be observable in the transcript as a separate `go test` run. No batching test+impl in one edit.
- **Error sentinels** use the prefix `workflow-<pkg>: ...` (e.g. `errors.New("workflow-validation: invalid input")`).
- **Test-file naming:** pair each `foo.go` with `foo_test.go`; prefer black-box `<package>_test` packages.
- **Table tests** use the project `table-test` skill form: an `assert func(t, got, err)` closure per case (NOT `want`/`wantErr` fields); a `ctx` modifier for context-sensitive components; `t.Context()` not `context.Background()`.
- **Mocks:** use the `use-mockgen` skill (`//go:generate mockgen --typed`, mocks alongside the interface in the producer package).
- **DB tests:** use `database.RunTestDatabase(t, opts...)` — never a hand-rolled container.
- **Never import** watermill/casbin/gocron/clockwork from engine/workflow code; go through the in-repo abstraction. `validation/expr` imports `expr-lang/expr` directly (it is an adapter boundary, like `action/httpcall`) — it MUST NOT import `internal/expreval` (public package cannot import `internal/`).
- **Type-safe per-kind options convention (ADR-0113):** a private option struct implements only the `applyX` methods for the kinds it is valid on; the `WithX` function returns a narrow interface embedding just those option interfaces (see `activity/options.go` `WithWaitReminder`/`reminderOpt`). Mis-applying an option is a compile error.
- **Compile-once/validate-hot-path:** `ValidationStrategy.NewValidator()` may compile a schema (non-trivial); the built `Validator` is cached by the `Gate` and reused. `Validate` is the hot path.

---

## File Structure

New:
- `validation/validation.go` — `Validator`, `ValidationStrategy`, `DescribableStrategy`, `ValidationDescriptor`, `ErrInvalidInput`.
- `validation/registry.go` — `Registry`, `StrategyFactory`.
- `validation/gate.go` — `Gate` (executor-side memoizer).
- `validation/expr/expr.go` — expr predicate-list strategy + `Factory` (imports `expr-lang/expr`).
- `validation/callback/callback.go` — code-only callback strategy (no `Descriptor`).
- `validation/jsonschema/jsonschema.go` — JSON Schema strategy: `New`/`NewFromValue`/`NewFromStruct` + `Factory` (imports `santhosh-tekuri/jsonschema/v6` + `invopop/jsonschema`).
- `validation/avro/avro.go` — Avro record strategy + `Factory` (imports `linkedin/goavro/v2`).
- `examples/scenarios/input_validation/main.go` — reference wiring (one rejected, one accepted path).
- `internal/persistence/store/migrations/{postgres/0012,mysql/0005,sqlite/0004}_human_task_defref.sql` — add `def_id`/`def_version` columns.
- `docs/adr/0110-input-validation-architecture.md`, `0111-jsonschema-adapter-libraries.md`, `0112-avro-adapter-library.md`.

Modified:
- `definition/event/{event.go,options.go}` — `StartEvent.InputValidation`, `IntermediateCatchEvent.PayloadValidation`, `WithInputValidation`, `WithPayloadValidation` (Catch); wire round-trip.
- `definition/activity/{activity.go,options.go}` — `UserTask.CompletionValidation`, `ReceiveTask.PayloadValidation`, `WithCompletionValidation`, `WithPayloadValidation` (Receive); wire round-trip.
- `definition/model/{node_wire.go,yaml.go}` — `Validation *ValidationDescriptor` field, per-kind round-trip helpers, `MarshalJSON` fail-closed.
- `definition/model/builder.go`, `definition/definition.go`, `definition/build/build.go` — `WithValidatorRegistry` loader option + descriptor→strategy reconstruction at `build()`.
- `engine/state.go` — exported `MessageTargetNode(name, key)` query.
- `humantask/humantask.go` — `HumanTask.DefID`/`DefVersion`.
- `internal/persistence/store/humantask_store.go` — read/write the two new columns.
- `runtime/processdriver_action.go:197` — populate `DefID`/`DefVersion` on task creation.
- `runtime/processdriver.go`, `runtime/processdriver_message.go` — `Gate` field + start/message injection.
- `runtime/task/service.go` — `WithDefinitionResolver` + completion injection.
- `transport/http/httpcore/errors.go` — map `validation.ErrInvalidInput` → 400.

---

## Task 1: `validation` core port + Registry + Gate

**Files:**
- Create: `validation/validation.go`, `validation/registry.go`, `validation/gate.go`
- Test: `validation/validation_test.go`, `validation/registry_test.go`, `validation/gate_test.go`

**Interfaces:**
- Produces:
  - `type Validator interface { Validate(ctx context.Context, input map[string]any) error }`
  - `type ValidationStrategy interface { NewValidator() (Validator, error) }`
  - `type DescribableStrategy interface { ValidationStrategy; Descriptor() ValidationDescriptor }`
  - `type ValidationDescriptor struct { Kind string; Schema string }`
  - `var ErrInvalidInput = errors.New("workflow-validation: invalid input")`
  - `type StrategyFactory func(schema string) (ValidationStrategy, error)`
  - `type Registry struct { ... }`; `func NewRegistry() *Registry`; `func (r *Registry) Register(kind string, f StrategyFactory)`; `func (r *Registry) Strategy(d ValidationDescriptor) (ValidationStrategy, error)`; `var ErrUnknownKind = errors.New("workflow-validation: unknown validation kind")`
  - `type Gate struct { ... }`; `func NewGate() *Gate`; `func (g *Gate) Validate(ctx context.Context, key string, s ValidationStrategy, input map[string]any) error`

- [ ] **Step 1: Write the failing test for `ErrInvalidInput` + a hand-written Validator/Strategy**

`validation/validation_test.go`:
```go
package validation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/validation"
)

// funcValidator adapts a func to the Validator port for tests.
type funcValidator func(ctx context.Context, input map[string]any) error

func (f funcValidator) Validate(ctx context.Context, input map[string]any) error { return f(ctx, input) }

// funcStrategy builds a fixed Validator.
type funcStrategy struct{ v validation.Validator }

func (s funcStrategy) NewValidator() (validation.Validator, error) { return s.v, nil }

func TestValidator_ReturnsError(t *testing.T) {
	t.Parallel()
	v := funcValidator(func(_ context.Context, in map[string]any) error {
		if in["amount"] == nil {
			return errors.New("amount required")
		}
		return nil
	})
	if err := v.Validate(t.Context(), map[string]any{}); err == nil {
		t.Fatal("expected error for missing amount")
	}
	if err := v.Validate(t.Context(), map[string]any{"amount": 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validation/...`
Expected: FAIL — `package .../validation` build error (`validation.Validator` undefined).

- [ ] **Step 3: Write `validation/validation.go`**

```go
// Package validation is the neutral external-input validation port. A Validator
// is the executable check; a ValidationStrategy (attached to a definition node)
// provides the runtime Validator. Concrete strategies live in opt-in adapter
// subpackages (validation/expr, validation/callback, validation/jsonschema,
// validation/avro) so the definition/engine core imports no schema library.
package validation

import (
	"context"
	"errors"
)

// ErrInvalidInput is the sentinel wrapping every validation failure. The transport
// layer maps it to HTTP 400. Always wrapped with a detail (which field/predicate/schema).
var ErrInvalidInput = errors.New("workflow-validation: invalid input")

// Validator is the runtime port: the executable check. A non-nil error rejects the
// operation before any state mutation.
type Validator interface {
	Validate(ctx context.Context, input map[string]any) error
}

// ValidationStrategy is attached to a node in the definition and PROVIDES the runtime
// Validator (a strategy may also implement Validator directly). NewValidator is called
// once (may compile a schema); the built Validator is cached by the Gate and reused.
type ValidationStrategy interface {
	NewValidator() (Validator, error)
}

// DescribableStrategy is implemented by DECLARATIVE strategies (expr/json-schema/avro) so
// they round-trip through wire/YAML. The callback strategy does NOT implement it.
type DescribableStrategy interface {
	ValidationStrategy
	Descriptor() ValidationDescriptor
}

// ValidationDescriptor is the serialized form stored on a node's wire representation.
type ValidationDescriptor struct {
	Kind   string // "expr" | "json-schema" | "avro" (registry key)
	Schema string // schema text / predicate list (adapter-interpreted)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validation/...`
Expected: PASS.

- [ ] **Step 5: Write the failing test for `Registry`**

`validation/registry_test.go`:
```go
package validation_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/validation"
)

func TestRegistry_RegisterAndResolve(t *testing.T) {
	t.Parallel()
	r := validation.NewRegistry()
	r.Register("stub", func(schema string) (validation.ValidationStrategy, error) {
		return funcStrategy{v: funcValidator(func(_ context.Context, _ map[string]any) error { return nil })}, nil
	})

	tests := map[string]struct {
		desc   validation.ValidationDescriptor
		assert func(t *testing.T, s validation.ValidationStrategy, err error)
	}{
		"known kind resolves": {
			desc: validation.ValidationDescriptor{Kind: "stub", Schema: "x"},
			assert: func(t *testing.T, s validation.ValidationStrategy, err error) {
				if err != nil || s == nil {
					t.Fatalf("want strategy, got s=%v err=%v", s, err)
				}
			},
		},
		"unknown kind errors": {
			desc: validation.ValidationDescriptor{Kind: "nope"},
			assert: func(t *testing.T, s validation.ValidationStrategy, err error) {
				if !errors.Is(err, validation.ErrUnknownKind) {
					t.Fatalf("want ErrUnknownKind, got %v", err)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, err := r.Strategy(tc.desc)
			tc.assert(t, s, err)
		})
	}
}
```
(Add `import "context"` to the file.)

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./validation/...`
Expected: FAIL — `validation.NewRegistry` / `validation.ErrUnknownKind` undefined.

- [ ] **Step 7: Write `validation/registry.go`**

```go
package validation

import (
	"errors"
	"fmt"
	"sync"
)

// ErrUnknownKind is returned by Registry.Strategy for a descriptor Kind with no
// registered factory (the consumer did not opt into that adapter).
var ErrUnknownKind = errors.New("workflow-validation: unknown validation kind")

// StrategyFactory rebuilds a declarative strategy from its serialized schema text.
type StrategyFactory func(schema string) (ValidationStrategy, error)

// Registry maps a descriptor Kind -> factory. The Loader uses it to reconstruct
// strategies from a serialized definition. Registration is explicit (no init magic),
// matching the action-catalog wiring pattern.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]StrategyFactory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{factories: make(map[string]StrategyFactory)} }

// Register maps kind -> factory. A later registration for the same kind wins.
func (r *Registry) Register(kind string, f StrategyFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[kind] = f
}

// Strategy rebuilds the live strategy for descriptor d, or ErrUnknownKind if the kind
// is not registered.
func (r *Registry) Strategy(d ValidationDescriptor) (ValidationStrategy, error) {
	r.mu.RLock()
	f, ok := r.factories[d.Kind]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, d.Kind)
	}
	return f(d.Schema)
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./validation/...`
Expected: PASS.

- [ ] **Step 9: Write the failing test for `Gate` (build-once + wrap error)**

`validation/gate_test.go`:
```go
package validation_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/kartaladev/wrkflw/validation"
)

// countingStrategy counts how many times NewValidator is invoked.
type countingStrategy struct {
	builds *int32
	fail   bool
}

func (s countingStrategy) NewValidator() (validation.Validator, error) {
	atomic.AddInt32(s.builds, 1)
	return funcValidator(func(_ context.Context, in map[string]any) error {
		if s.fail {
			return errors.New("bad input detail")
		}
		return nil
	}), nil
}

func TestGate_BuildsOncePerKeyAndWrapsError(t *testing.T) {
	t.Parallel()
	g := validation.NewGate()
	var builds int32
	s := countingStrategy{builds: &builds, fail: true}

	err := g.Validate(t.Context(), "def:1:node", s, map[string]any{})
	if !errors.Is(err, validation.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
	// second call with same key must reuse the built validator (no re-build).
	_ = g.Validate(t.Context(), "def:1:node", s, map[string]any{})
	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("want 1 build, got %d", got)
	}
}
```

- [ ] **Step 10: Run test to verify it fails**

Run: `go test ./validation/...`
Expected: FAIL — `validation.NewGate` undefined.

- [ ] **Step 11: Write `validation/gate.go`**

```go
package validation

import (
	"context"
	"fmt"
	"sync"
)

// Gate is the executor-side memoizer shared by the driver and task service. It builds
// a strategy's Validator once per key (compile-once) and wraps any failure in
// ErrInvalidInput. Definitions stay immutable; the executor owns the compiled cache.
type Gate struct {
	mu    sync.RWMutex
	built map[string]Validator
}

// NewGate returns an empty Gate.
func NewGate() *Gate { return &Gate{built: make(map[string]Validator)} }

// Validate builds (once, cached under key) the Validator for s and runs it against input.
// A build error is returned as-is; a validation failure is wrapped in ErrInvalidInput.
func (g *Gate) Validate(ctx context.Context, key string, s ValidationStrategy, input map[string]any) error {
	v, err := g.validator(key, s)
	if err != nil {
		return err
	}
	if err := v.Validate(ctx, input); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
	}
	return nil
}

func (g *Gate) validator(key string, s ValidationStrategy) (Validator, error) {
	g.mu.RLock()
	v, ok := g.built[key]
	g.mu.RUnlock()
	if ok {
		return v, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if v, ok = g.built[key]; ok { // re-check under write lock
		return v, nil
	}
	v, err := s.NewValidator()
	if err != nil {
		return nil, err
	}
	g.built[key] = v
	return v, nil
}
```

- [ ] **Step 12: Run test to verify it passes**

Run: `go test ./validation/... -race`
Expected: PASS, no races.

- [ ] **Step 13: Commit**

```bash
git add validation/
git commit -m "feat(validation): neutral port + Registry + memoizing Gate"
```

---

## Task 2: `validation/expr` adapter

**Files:**
- Create: `validation/expr/expr.go`
- Test: `validation/expr/expr_test.go`

**Interfaces:**
- Consumes: `validation.Validator`, `validation.ValidationStrategy`, `validation.DescribableStrategy`, `validation.ValidationDescriptor`, `validation.StrategyFactory`.
- Produces:
  - `const Kind = "expr"`
  - `func New(predicates ...string) validation.DescribableStrategy` — all predicates must hold.
  - `func Factory(schema string) (validation.ValidationStrategy, error)` — schema is newline-separated predicates.
  - `Descriptor()` returns `{Kind: "expr", Schema: strings.Join(predicates, "\n")}`.

**Semantics note:** unlike `internal/expreval.EvalBool` (which maps a missing variable to `false`, gateway semantics), a predicate that errors or references a missing field here is a **validation failure** (return the error), NOT silently false.

- [ ] **Step 1: Write the failing test**

`validation/expr/expr_test.go`:
```go
package expr_test

import (
	"strings"
	"testing"

	"github.com/kartaladev/wrkflw/validation"
	vexpr "github.com/kartaladev/wrkflw/validation/expr"
)

func TestExpr_ValidateAndRoundTrip(t *testing.T) {
	t.Parallel()
	s := vexpr.New(`decision in ['approve','reject']`, `amount > 0`)

	v, err := s.NewValidator()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := v.Validate(t.Context(), map[string]any{"decision": "approve", "amount": 5}); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if err := v.Validate(t.Context(), map[string]any{"decision": "maybe", "amount": 5}); err == nil {
		t.Fatal("expected rejection for bad decision")
	}
	// missing field => failure, not silent-false-pass.
	if err := v.Validate(t.Context(), map[string]any{"decision": "approve"}); err == nil {
		t.Fatal("expected failure for missing amount")
	}

	d := s.(validation.DescribableStrategy).Descriptor()
	if d.Kind != vexpr.Kind {
		t.Fatalf("kind = %q", d.Kind)
	}
	rebuilt, err := vexpr.Factory(d.Schema)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if got := strings.Split(d.Schema, "\n"); len(got) != 2 {
		t.Fatalf("schema predicates = %d", len(got))
	}
	rv, _ := rebuilt.NewValidator()
	if err := rv.Validate(t.Context(), map[string]any{"decision": "reject", "amount": 1}); err != nil {
		t.Fatalf("rebuilt rejected valid input: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validation/expr/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Add the module dep is NOT needed** (expr-lang already in `go.mod`). Write `validation/expr/expr.go`:

```go
// Package expr is a validation adapter over github.com/expr-lang/expr. A strategy holds
// a list of boolean predicates; ALL must evaluate true against the input map. It imports
// expr-lang directly (an allowed adapter boundary) and does NOT reuse internal/expreval,
// whose EvalBool maps missing vars to false (gateway semantics) — undesirable for validation.
package expr

import (
	"context"
	"fmt"
	"strings"

	exprlang "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/kartaladev/wrkflw/validation"
)

// Kind is the registry key for expr strategies.
const Kind = "expr"

type strategy struct{ predicates []string }

// New returns a strategy requiring all predicates to hold against the input.
func New(predicates ...string) validation.DescribableStrategy { return strategy{predicates: predicates} }

// Factory rebuilds a strategy from newline-separated predicate text.
func Factory(schema string) (validation.ValidationStrategy, error) {
	var preds []string
	for _, line := range strings.Split(schema, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			preds = append(preds, s)
		}
	}
	return strategy{predicates: preds}, nil
}

func (s strategy) Descriptor() validation.ValidationDescriptor {
	return validation.ValidationDescriptor{Kind: Kind, Schema: strings.Join(s.predicates, "\n")}
}

func (s strategy) NewValidator() (validation.Validator, error) {
	programs := make([]*vm.Program, 0, len(s.predicates))
	for _, p := range s.predicates {
		prog, err := exprlang.Compile(p, exprlang.AsBool())
		if err != nil {
			return nil, fmt.Errorf("workflow-validation/expr: compile %q: %w", p, err)
		}
		programs = append(programs, prog)
	}
	return &validator{source: s.predicates, programs: programs}, nil
}

type validator struct {
	source   []string
	programs []*vm.Program
}

func (v *validator) Validate(_ context.Context, input map[string]any) error {
	for i, prog := range v.programs {
		out, err := exprlang.Run(prog, input)
		if err != nil {
			return fmt.Errorf("predicate %q: %w", v.source[i], err)
		}
		ok, _ := out.(bool)
		if !ok {
			return fmt.Errorf("predicate %q not satisfied", v.source[i])
		}
	}
	return nil
}
```
Note: `exprlang.Compile(p, exprlang.AsBool())` does NOT use `AllowUndefinedVariables`, so a missing field raises a run error → validation failure (the desired semantics).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validation/expr/... -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validation/expr/ go.mod go.sum
git commit -m "feat(validation): expr predicate-list adapter (no new dep)"
```

---

## Task 3: `validation/callback` adapter

**Files:**
- Create: `validation/callback/callback.go`
- Test: `validation/callback/callback_test.go`

**Interfaces:**
- Produces:
  - `func New(fn func(ctx context.Context, input map[string]any) error) validation.ValidationStrategy` — wraps fn; implements `ValidationStrategy` + `Validator` but NOT `DescribableStrategy` (non-serializable).

- [ ] **Step 1: Write the failing test**

`validation/callback/callback_test.go`:
```go
package callback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/validation"
	"github.com/kartaladev/wrkflw/validation/callback"
)

func TestCallback_ValidatesAndIsNotDescribable(t *testing.T) {
	t.Parallel()
	s := callback.New(func(_ context.Context, in map[string]any) error {
		if in["ok"] != true {
			return errors.New("not ok")
		}
		return nil
	})
	if _, isDesc := s.(validation.DescribableStrategy); isDesc {
		t.Fatal("callback strategy must NOT implement DescribableStrategy")
	}
	v, _ := s.NewValidator()
	if err := v.Validate(t.Context(), map[string]any{"ok": true}); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
	if err := v.Validate(t.Context(), map[string]any{}); err == nil {
		t.Fatal("expected rejection")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validation/callback/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `validation/callback/callback.go`**

```go
// Package callback is a code-only validation adapter wrapping a Go func. It is NOT
// declarative: it has no Descriptor and cannot be serialized. A definition carrying a
// callback strategy fails MarshalJSON (fail-closed) — use a declarative strategy to persist.
package callback

import (
	"context"

	"github.com/kartaladev/wrkflw/validation"
)

type strategy struct {
	fn func(ctx context.Context, input map[string]any) error
}

// New wraps fn as a (non-serializable) validation strategy.
func New(fn func(ctx context.Context, input map[string]any) error) validation.ValidationStrategy {
	return strategy{fn: fn}
}

func (s strategy) NewValidator() (validation.Validator, error) { return s, nil }

func (s strategy) Validate(ctx context.Context, input map[string]any) error {
	return s.fn(ctx, input)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validation/callback/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validation/callback/
git commit -m "feat(validation): code-only callback adapter (non-serializable)"
```

---

## Task 4: `validation/jsonschema` adapter (ADR-0111)

**Files:**
- Create: `validation/jsonschema/jsonschema.go`
- Test: `validation/jsonschema/jsonschema_test.go`
- Modify: `go.mod`, `go.sum`
- Create: `docs/adr/0111-jsonschema-adapter-libraries.md`

**Interfaces:**
- Produces:
  - `const Kind = "json-schema"`
  - `func New(schemaJSON string) validation.DescribableStrategy` — schema as JSON text.
  - `func NewFromValue(schema map[string]any) (validation.DescribableStrategy, error)` — schema assembled as a Go map (marshaled to canonical JSON internally).
  - `func NewFromStruct(v any) (validation.DescribableStrategy, error)` — derive a JSON Schema from a Go type via `invopop/jsonschema` reflection.
  - `func Factory(schema string) (validation.ValidationStrategy, error)`.
  - All strategies `Descriptor()` returns `{Kind: "json-schema", Schema: <canonical JSON>}`.

- [ ] **Step 1: Add the dependencies**

Run:
```bash
go get github.com/santhosh-tekuri/jsonschema/v6@latest
go get github.com/invopop/jsonschema@latest
```
Expected: `go.mod`/`go.sum` updated.

- [ ] **Step 2: Write the failing test**

`validation/jsonschema/jsonschema_test.go`:
```go
package jsonschema_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/validation"
	vjs "github.com/kartaladev/wrkflw/validation/jsonschema"
)

const schemaJSON = `{
  "type": "object",
  "required": ["amount"],
  "properties": { "amount": { "type": "number", "minimum": 0 } }
}`

func TestJSONSchema_Validate(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		strategy func(t *testing.T) validation.DescribableStrategy
	}{
		"from text":  {strategy: func(t *testing.T) validation.DescribableStrategy { return vjs.New(schemaJSON) }},
		"from value": {strategy: func(t *testing.T) validation.DescribableStrategy {
			s, err := vjs.NewFromValue(map[string]any{
				"type":     "object",
				"required": []any{"amount"},
				"properties": map[string]any{
					"amount": map[string]any{"type": "number", "minimum": 0},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			return s
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := tc.strategy(t)
			v, err := s.NewValidator()
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if err := v.Validate(t.Context(), map[string]any{"amount": 10.0}); err != nil {
				t.Fatalf("valid rejected: %v", err)
			}
			if err := v.Validate(t.Context(), map[string]any{"amount": -1.0}); err == nil {
				t.Fatal("expected rejection for negative amount")
			}
			if err := v.Validate(t.Context(), map[string]any{}); err == nil {
				t.Fatal("expected rejection for missing amount")
			}
			// round-trip through Factory
			rebuilt, err := vjs.Factory(s.Descriptor().Schema)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			rv, _ := rebuilt.NewValidator()
			if err := rv.Validate(t.Context(), map[string]any{"amount": 3.0}); err != nil {
				t.Fatalf("rebuilt rejected valid: %v", err)
			}
		})
	}
}

type startInput struct {
	Amount float64 `json:"amount" jsonschema:"minimum=0"`
}

func TestJSONSchema_FromStruct(t *testing.T) {
	t.Parallel()
	s, err := vjs.NewFromStruct(&startInput{})
	if err != nil {
		t.Fatalf("from struct: %v", err)
	}
	v, _ := s.NewValidator()
	if err := v.Validate(t.Context(), map[string]any{"amount": 5.0}); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
	if err := v.Validate(t.Context(), map[string]any{"amount": -2.0}); err == nil {
		t.Fatal("expected rejection")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./validation/jsonschema/...`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Write `validation/jsonschema/jsonschema.go`**

```go
// Package jsonschema is a JSON Schema validation adapter. It validates the input map
// against a compiled schema using github.com/santhosh-tekuri/jsonschema/v6, and can also
// DERIVE a schema from a Go type via github.com/invopop/jsonschema (NewFromStruct). Both
// third-party deps are isolated in this package (ADR-0111); the definition/engine core
// never imports them. The serialized descriptor always carries canonical JSON text.
package jsonschema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	invopop "github.com/invopop/jsonschema"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kartaladev/wrkflw/validation"
)

// Kind is the registry key for JSON Schema strategies.
const Kind = "json-schema"

type strategy struct{ schema string } // canonical JSON text

// New builds a strategy from JSON Schema text.
func New(schemaJSON string) validation.DescribableStrategy { return strategy{schema: schemaJSON} }

// NewFromValue builds a strategy from a schema assembled as a Go map.
func NewFromValue(schema map[string]any) (validation.DescribableStrategy, error) {
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: marshal schema value: %w", err)
	}
	return strategy{schema: string(b)}, nil
}

// NewFromStruct derives a JSON Schema from v's type (invopop reflection) and returns a strategy.
func NewFromStruct(v any) (validation.DescribableStrategy, error) {
	r := &invopop.Reflector{DoNotReference: true}
	sch := r.Reflect(v)
	b, err := json.Marshal(sch)
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: marshal reflected schema: %w", err)
	}
	return strategy{schema: string(b)}, nil
}

// Factory rebuilds a strategy from serialized JSON schema text.
func Factory(schema string) (validation.ValidationStrategy, error) { return strategy{schema: schema}, nil }

func (s strategy) Descriptor() validation.ValidationDescriptor {
	return validation.ValidationDescriptor{Kind: Kind, Schema: s.schema}
}

func (s strategy) NewValidator() (validation.Validator, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(s.schema)))
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: parse schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	const resource = "mem://schema.json"
	if err := c.AddResource(resource, doc); err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: add resource: %w", err)
	}
	compiled, err := c.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: compile: %w", err)
	}
	return &validator{schema: compiled}, nil
}

type validator struct{ schema *jsonschema.Schema }

func (v *validator) Validate(_ context.Context, input map[string]any) error {
	return v.schema.Validate(any(input))
}
```
> NOTE for the implementer: verify the exact v6 API against the pulled version — the surface used is `jsonschema.UnmarshalJSON(io.Reader) (any, error)`, `NewCompiler()`, `(*Compiler).AddResource(url string, doc any) error`, `(*Compiler).Compile(url) (*Schema, error)`, `(*Schema).Validate(any) error`. If a symbol differs, adapt (this is the only place the lib is used). For `invopop`, `(*Reflector).Reflect(any) *Schema` with `DoNotReference: true` inlines definitions so the derived schema validates a plain map.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./validation/jsonschema/... -race`
Expected: PASS.

- [ ] **Step 6: Write ADR-0111**

`docs/adr/0111-jsonschema-adapter-libraries.md` (Nygard template): Status Accepted / 2026-07-08; Context (need a JSON Schema validator + a programmatic/struct-reflection authoring path, both isolated behind `validation/jsonschema`, per ADR-0110); Decision (adopt `santhosh-tekuri/jsonschema/v6` as the validator and `invopop/jsonschema` for struct-reflection generation; both confined to `validation/jsonschema`; core imports neither); Consequences (two new deps behind one adapter; draft 2020-12 support; canonical-JSON descriptor round-trip; struct authoring available via `jsonschema:"..."` tags).

- [ ] **Step 7: Commit**

```bash
git add validation/jsonschema/ go.mod go.sum docs/adr/0111-jsonschema-adapter-libraries.md
git commit -m "feat(validation): json-schema adapter (santhosh-tekuri + invopop), ADR-0111"
```

---

## Task 5: `validation/avro` adapter (ADR-0112)

**Files:**
- Create: `validation/avro/avro.go`
- Test: `validation/avro/avro_test.go`
- Modify: `go.mod`, `go.sum`
- Create: `docs/adr/0112-avro-adapter-library.md`

**Interfaces:**
- Produces:
  - `const Kind = "avro"`
  - `func New(avsc string) validation.DescribableStrategy`
  - `func Factory(schema string) (validation.ValidationStrategy, error)`
  - `Descriptor()` returns `{Kind: "avro", Schema: avsc}`.

Validation: parse the `.avsc` record schema with goavro; attempt `codec.BinaryFromNative(nil, input)`; a non-nil error means the input map does not conform (missing field / wrong type).

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/linkedin/goavro/v2@latest`
Expected: `go.mod`/`go.sum` updated.

- [ ] **Step 2: Write the failing test**

`validation/avro/avro_test.go`:
```go
package avro_test

import (
	"testing"

	vavro "github.com/kartaladev/wrkflw/validation/avro"
)

const avsc = `{
  "type": "record",
  "name": "StartInput",
  "fields": [
    {"name": "amount", "type": "double"},
    {"name": "decision", "type": "string"}
  ]
}`

func TestAvro_Validate(t *testing.T) {
	t.Parallel()
	s := vavro.New(avsc)
	v, err := s.NewValidator()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := v.Validate(t.Context(), map[string]any{"amount": 10.0, "decision": "approve"}); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
	// missing required field 'decision' => reject.
	if err := v.Validate(t.Context(), map[string]any{"amount": 10.0}); err == nil {
		t.Fatal("expected rejection for missing field")
	}
	// wrong type for amount => reject.
	if err := v.Validate(t.Context(), map[string]any{"amount": "nan", "decision": "x"}); err == nil {
		t.Fatal("expected rejection for wrong type")
	}
	rebuilt, err := vavro.Factory(s.Descriptor().Schema)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	rv, _ := rebuilt.NewValidator()
	if err := rv.Validate(t.Context(), map[string]any{"amount": 1.0, "decision": "reject"}); err != nil {
		t.Fatalf("rebuilt rejected valid: %v", err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./validation/avro/...`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Write `validation/avro/avro.go`**

```go
// Package avro is an Avro-record validation adapter over github.com/linkedin/goavro/v2.
// It checks that the input map conforms to an Avro record schema by attempting to encode
// the map against the parsed codec; an encode error means the input does not conform. The
// goavro dep is isolated in this package (ADR-0112).
package avro

import (
	"context"
	"fmt"

	"github.com/linkedin/goavro/v2"

	"github.com/kartaladev/wrkflw/validation"
)

// Kind is the registry key for Avro strategies.
const Kind = "avro"

type strategy struct{ avsc string }

// New builds a strategy from an Avro record schema (.avsc text).
func New(avsc string) validation.DescribableStrategy { return strategy{avsc: avsc} }

// Factory rebuilds a strategy from serialized .avsc text.
func Factory(schema string) (validation.ValidationStrategy, error) { return strategy{avsc: schema}, nil }

func (s strategy) Descriptor() validation.ValidationDescriptor {
	return validation.ValidationDescriptor{Kind: Kind, Schema: s.avsc}
}

func (s strategy) NewValidator() (validation.Validator, error) {
	codec, err := goavro.NewCodec(s.avsc)
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/avro: parse schema: %w", err)
	}
	return &validator{codec: codec}, nil
}

type validator struct{ codec *goavro.Codec }

func (v *validator) Validate(_ context.Context, input map[string]any) error {
	if _, err := v.codec.BinaryFromNative(nil, input); err != nil {
		return fmt.Errorf("does not conform to avro schema: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./validation/avro/... -race`
Expected: PASS.

- [ ] **Step 6: Write ADR-0112**

`docs/adr/0112-avro-adapter-library.md` (Nygard): Status Accepted / 2026-07-08; Context (Avro-record validation isolated behind `validation/avro`, per ADR-0110); Decision (adopt `github.com/linkedin/goavro/v2`; validate via native→binary encode against the parsed record codec; confined to this package); Consequences (one new dep; encode-based conformance check; descriptor carries raw `.avsc`).

- [ ] **Step 7: Commit**

```bash
git add validation/avro/ go.mod go.sum docs/adr/0112-avro-adapter-library.md
git commit -m "feat(validation): avro record adapter (linkedin/goavro), ADR-0112"
```

---

## Task 6: Definition node validation slots + type-safe options

**Files:**
- Modify: `definition/event/event.go` (struct fields), `definition/event/options.go` (options)
- Modify: `definition/activity/activity.go` (struct fields), `definition/activity/options.go` (options)
- Test: `definition/event/options_test.go`, `definition/activity/options_test.go`

**Interfaces:**
- Consumes: `validation.ValidationStrategy`.
- Produces:
  - Struct fields (all `validation.ValidationStrategy`, nil when absent):
    - `event.StartEvent.InputValidation`
    - `event.IntermediateCatchEvent.PayloadValidation`
    - `activity.UserTask.CompletionValidation`
    - `activity.ReceiveTask.PayloadValidation`
  - Options:
    - `func event.WithInputValidation(s validation.ValidationStrategy) StartOption`
    - `func event.WithPayloadValidation(s validation.ValidationStrategy) CatchOption`
    - `func activity.WithCompletionValidation(s validation.ValidationStrategy) UserTaskOption`
    - `func activity.WithPayloadValidation(s validation.ValidationStrategy) ReceiveTaskOption`

Model each option on the single-kind precedents (`startFuncOpt`/`catchFuncOpt` in `event/options.go`; `eligibilityExprOpt` in `activity/options.go`). Each `WithX` returns the concrete single-kind option interface (NOT a spanning interface — `ReceiveTask` and `IntermediateCatchEvent` are in different packages, so their `WithPayloadValidation` are separate functions per the spec).

- [ ] **Step 1: Write the failing test (activity side)**

`definition/activity/options_test.go` (add cases):
```go
func TestWithCompletionValidation_SetsSlot(t *testing.T) {
	t.Parallel()
	strat := stubStrategy{} // implements validation.ValidationStrategy
	u := activity.NewUserTask("approve", activity.WithCompletionValidation(strat))
	ut, ok := u.(activity.UserTask) // adjust to the actual constructor return shape
	if !ok {
		t.Fatalf("node kind = %T", u)
	}
	if ut.CompletionValidation == nil {
		t.Fatal("CompletionValidation not set")
	}
}

func TestWithPayloadValidation_Receive_SetsSlot(t *testing.T) {
	t.Parallel()
	r := activity.NewReceiveTask("await", "OrderPlaced", activity.WithPayloadValidation(stubStrategy{}))
	// assert r's PayloadValidation is set (mirror the accessor used elsewhere in this test file)
	_ = r
}
```
> The implementer must match `NewUserTask`/`NewReceiveTask`'s ACTUAL signatures and node-return shape (see existing tests in `definition/activity/`). Add a local `stubStrategy` implementing `validation.ValidationStrategy` (`NewValidator() (validation.Validator, error)` returning a no-op validator).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/activity/...`
Expected: FAIL — `activity.WithCompletionValidation` undefined + `CompletionValidation` field undefined.

- [ ] **Step 3: Add struct fields + options (activity)**

In `definition/activity/activity.go`, add to `UserTask`: `CompletionValidation validation.ValidationStrategy` and to `ReceiveTask`: `PayloadValidation validation.ValidationStrategy` (import `github.com/kartaladev/wrkflw/validation`).

In `definition/activity/options.go`:
```go
type completionValidationOpt struct{ s validation.ValidationStrategy }

func (o completionValidationOpt) applyUserTask(u *UserTask) { u.CompletionValidation = o.s }

// WithCompletionValidation validates a UserTask's completion output before it is applied.
func WithCompletionValidation(s validation.ValidationStrategy) UserTaskOption {
	return completionValidationOpt{s: s}
}

type receivePayloadValidationOpt struct{ s validation.ValidationStrategy }

func (o receivePayloadValidationOpt) applyReceiveTask(r *ReceiveTask) { r.PayloadValidation = o.s }

// WithPayloadValidation validates a ReceiveTask's message payload before it is applied.
func WithPayloadValidation(s validation.ValidationStrategy) ReceiveTaskOption {
	return receivePayloadValidationOpt{s: s}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/activity/...`
Expected: PASS.

- [ ] **Step 5: Write the failing test (event side)**

`definition/event/options_test.go` (add cases) analogous to Step 1 for `event.WithInputValidation` on `NewStartEvent`/`NewStart` and `event.WithPayloadValidation` on `NewIntermediateCatch` — assert `StartEvent.InputValidation` and `IntermediateCatchEvent.PayloadValidation` are set. Match the actual constructor names/signatures from existing `definition/event` tests.

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./definition/event/...`
Expected: FAIL — `event.WithInputValidation`/`WithPayloadValidation` undefined.

- [ ] **Step 7: Add struct fields + options (event)**

In `definition/event/event.go`: add `StartEvent.InputValidation validation.ValidationStrategy` and `IntermediateCatchEvent.PayloadValidation validation.ValidationStrategy`.

In `definition/event/options.go`:
```go
type inputValidationOpt struct{ s validation.ValidationStrategy }

func (o inputValidationOpt) applyStart(n *StartEvent) { n.InputValidation = o.s }

// WithInputValidation validates the manually-provided start vars (Drive) against the
// start event's contract before the instance is created.
func WithInputValidation(s validation.ValidationStrategy) StartOption { return inputValidationOpt{s: s} }

type catchPayloadValidationOpt struct{ s validation.ValidationStrategy }

func (o catchPayloadValidationOpt) applyCatch(n *IntermediateCatchEvent) { n.PayloadValidation = o.s }

// WithPayloadValidation validates a message IntermediateCatchEvent's payload before it is applied.
func WithPayloadValidation(s validation.ValidationStrategy) CatchOption { return catchPayloadValidationOpt{s: s} }
```

- [ ] **Step 8: Run test to verify it passes; build all**

Run: `go test ./definition/... && go build ./...`
Expected: PASS / clean.

- [ ] **Step 9: Commit**

```bash
git add definition/event/ definition/activity/
git commit -m "feat(definition): node validation slots + type-safe With*Validation options"
```

---

## Task 7: Wire/YAML round-trip + MarshalJSON fail-closed + Loader registry

**Files:**
- Modify: `definition/model/node_wire.go`, `definition/model/yaml.go`
- Modify: `definition/event/event.go`, `definition/activity/activity.go` (the per-kind `ToWire`/`FromWire` closures registered in `init()`)
- Modify: `definition/model/builder.go`, `definition/definition.go`, `definition/build/build.go` (Loader option + reconstruction)
- Test: `definition/model/node_wire_test.go` (or a new `validation_wire_test.go`), `definition/definition_test.go`

**Interfaces:**
- Consumes: `validation.ValidationStrategy`, `validation.DescribableStrategy`, `validation.ValidationDescriptor`, `validation.Registry`.
- Produces:
  - `NodeWire.Validation *validation.ValidationDescriptor` (json `validation,omitempty`) and the YAML mirror in `nodeYAML`.
  - `func definition.WithValidatorRegistry(reg *validation.Registry) LoaderOption` (new option type on `NewLoader`).
  - Descriptor written from a node's `DescribableStrategy.Descriptor()`; reconstructed at `build()` via `reg.Strategy(desc)`.
  - `ProcessDefinition.MarshalJSON` returns an error when any node carries a non-`DescribableStrategy` (callback) strategy.
  - `var ErrUnserializableValidation = errors.New("workflow-model: validation strategy is not serializable")` (in `definition/model`).

**Design decisions for the implementer:**
- Add the descriptor field to `NodeWire` (`node_wire.go`) AND `nodeYAML` (`yaml.go`), copied in the `fromNodeYAML` field block. Model the `omitempty` pointer on the existing `TimerTrigger *TriggerWire` precedent.
- Each kind's `ToWire` writes `w.Validation` from its strategy field IF the strategy implements `DescribableStrategy`; the fail-closed hard-error is enforced centrally in `MarshalJSON` (see Step 5), NOT in each `ToWire` (which has no error return).
- Each kind's `FromWire` stores the RAW descriptor onto the node's strategy field wrapped as a "pending descriptor" so `build()` can reconstruct it. Simplest approach: give `NodeWire.Validation` reconstruction happen in `build()` by walking nodes; but nodes are built eagerly in `fromNodeYAML`/`fromWire`. **Chosen approach:** `FromWire` sets the node's strategy field to a small unexported `pendingStrategy{desc}` (implements `ValidationStrategy` by erroring "not reconstructed") and records `desc` so `build()`/loader reconstruction replaces it via the registry. Cleaner alternative if it fits: store the descriptor in a side map on `definitionCore` keyed by nodeID, resolve at `build()`. Pick whichever keeps `fromWire` signature stable; document the choice in the ADR.

- [ ] **Step 1: Write the failing round-trip test**

`definition/model/validation_wire_test.go`:
```go
package model_test

import (
	"encoding/json"
	"testing"

	"github.com/kartaladev/wrkflw/definition"
	vexpr "github.com/kartaladev/wrkflw/validation/expr"
)

func TestStartValidation_WireRoundTrip(t *testing.T) {
	t.Parallel()
	// Build a definition with a start event carrying an expr input-validation strategy,
	// marshal to JSON, reload via a Loader with an expr-registered registry, and assert
	// the reconstructed strategy validates identically.
	// (Use the actual builder API: NewBuilder(...).AddStartEvent("s", event.WithInputValidation(vexpr.New("amount > 0"))) ... .Build())
	_ = vexpr.New
	_ = json.Marshal
	_ = definition.NewBuilder
}
```
> Flesh this out against the real builder/loader API. The assertion chain: build → `json.Marshal(def)` → `definition.NewLoaderFromJSON`-or-equivalent with `WithValidatorRegistry(reg)` where `reg` has `reg.Register(vexpr.Kind, vexpr.Factory)` → `Build()` → drive/validate and confirm the reconstructed strategy rejects `amount = -1`. Note: YAML load is the primary Loader path; if JSON reload isn't a public entry, assert the wire round-trip via `ProcessDefinition.MarshalJSON` + the model's `fromWire` directly, and add a separate YAML test using `definition.NewLoader(reader, WithValidatorRegistry(reg))`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/...`
Expected: FAIL — `definition.WithValidatorRegistry` undefined / round-trip drops validation.

- [ ] **Step 3: Add the wire descriptor field**

`definition/model/node_wire.go`: add to `NodeWire`:
```go
Validation *validation.ValidationDescriptor `json:"validation,omitempty"`
```
`definition/model/yaml.go`: add to `nodeYAML`:
```go
Validation *validation.ValidationDescriptor `yaml:"validation,omitempty"`
```
and copy it in the `fromNodeYAML` field-by-field block: `w.Validation = y.Validation`.

- [ ] **Step 4: Wire per-kind `ToWire`/`FromWire`**

In `definition/event/event.go` and `definition/activity/activity.go`, in each relevant kind's `ToWire`, write the descriptor when the strategy is describable:
```go
if v, ok := node.InputValidation.(validation.DescribableStrategy); ok {
	d := v.Descriptor()
	w.Validation = &d
}
```
In `FromWire`, set a pending strategy (chosen approach from the Design note) so `build()` reconstructs it:
```go
if w.Validation != nil {
	node.InputValidation = model.PendingValidation(*w.Validation)
}
```
Add `model.PendingValidation(desc validation.ValidationDescriptor) validation.ValidationStrategy` returning an unexported `pendingStrategy{desc}` whose `NewValidator()` returns an error `"validation strategy not reconstructed (missing registry)"` and which is also introspectable by `build()` (add `model.DescriptorOf(s validation.ValidationStrategy) (validation.ValidationDescriptor, bool)` to detect a pending or describable strategy).

- [ ] **Step 5: MarshalJSON fail-closed**

In `ProcessDefinition.MarshalJSON` (`node_wire.go`), in the per-node loop, after obtaining the node's strategy fields, if any non-nil validation strategy is NEITHER a `DescribableStrategy` NOR a pending descriptor, return `fmt.Errorf("%w: node %q", ErrUnserializableValidation, n.ID())`. Add the sentinel `ErrUnserializableValidation`. Provide a helper `nodeValidationStrategies(n Node) []validation.ValidationStrategy` that returns the (0–1) validation strategy for each kind so the check is centralized.

- [ ] **Step 6: Loader registry threading**

`definition/model/builder.go`: add a `validators *validation.Registry` field to `definitionCore`; in `build()`, walk nodes and for each pending/describable validation strategy call `core.validators.Strategy(desc)` to replace it with the live strategy (error if `validators == nil` but a descriptor is present, or if `Strategy` errors — surface the unknown kind).
`definition/definition.go` + `definition/build/build.go`: add `type LoaderOption` + `func WithValidatorRegistry(reg *validation.Registry) LoaderOption`; thread it into `NewLoader(r, opts...)` → `ParseYAML`/core so `core.validators` is set before `build()`.

- [ ] **Step 7: Write the failing test for MarshalJSON fail-closed**

`definition/model/validation_wire_test.go` (add):
```go
func TestMarshalJSON_FailsClosedOnCallbackStrategy(t *testing.T) {
	t.Parallel()
	// Build a def with a UserTask carrying a callback (non-describable) CompletionValidation,
	// then json.Marshal(def) and assert it returns model.ErrUnserializableValidation.
}
```

- [ ] **Step 8: Run tests to verify pass**

Run: `go test ./definition/... ./validation/... -race && go build ./...`
Expected: PASS / clean.

- [ ] **Step 9: Commit**

```bash
git add definition/
git commit -m "feat(definition): validation descriptor wire/YAML round-trip + fail-closed MarshalJSON + WithValidatorRegistry"
```

---

## Task 8: Engine `MessageTargetNode` query

**Files:**
- Modify: `engine/state.go`
- Test: `engine/state_test.go` (or a focused `message_target_test.go` in `package engine`)

**Interfaces:**
- Produces: `func (s *InstanceState) MessageTargetNode(name, correlationKey string) (nodeID string, ok bool)` — returns the node ID the message `(name, key)` WOULD wake, mirroring `handleMessageReceived`'s 4-tier priority (event-gateway arm → message boundary → event-subprocess arm → standalone parked message token). `ok == false` when nothing would match.

**Design note:** This query MUST use the SAME priority order and matching predicates as `handleMessageReceived` (`engine/step_triggers.go:654`) so runtime validation targets exactly the node the engine will wake. Reuse the existing unexported helpers (`armedEventByMessage`, `boundaryArmByMessage`, `eventSubprocessArmByMessage`, `tokenAwaitingMessage`) — return the winning node's ID. For tiers that resolve to a token/arm without a distinct node id, return the node id the arm/token sits on.

- [ ] **Step 1: Write the failing test**

`engine/message_target_test.go`:
```go
package engine

import "testing"

func TestMessageTargetNode_ResolvesStandaloneReceiveTask(t *testing.T) {
	t.Parallel()
	// Construct an InstanceState with a token parked awaiting message "OrderPlaced" on node "recv".
	// Assert MessageTargetNode("OrderPlaced", key) == ("recv", true).
	// Use the same in-package fixtures other engine tests use to build parked-message state.
}

func TestMessageTargetNode_NoMatch(t *testing.T) {
	t.Parallel()
	// Empty/unrelated state => ("", false).
}
```
> Build the fixtures the way existing `engine` message tests do (there are tests around `handleMessageReceived`); reuse their state-construction helpers.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/ -run MessageTargetNode`
Expected: FAIL — `MessageTargetNode` undefined.

- [ ] **Step 3: Implement `MessageTargetNode` in `engine/state.go`**

Mirror the tier priority of `handleMessageReceived`, returning the winning node ID. (Read `handleMessageReceived` first and replicate its match predicates exactly; do not change `handleMessageReceived`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./engine/ -run MessageTargetNode -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/
git commit -m "feat(engine): MessageTargetNode query mirroring message dispatch priority"
```

---

## Task 9: `HumanTask` Qualifier + durable persistence + parity

**Files:**
- Modify: `humantask/humantask.go` (add fields)
- Modify: `internal/persistence/store/humantask_store.go` (read/write columns)
- Create: `internal/persistence/store/migrations/postgres/0012_human_task_defref.sql`, `mysql/0005_human_task_defref.sql`, `sqlite/0004_human_task_defref.sql`
- Modify: `runtime/processdriver_action.go:197` (populate on creation)
- Test: `internal/persistence/store/humantask_store_conformance_test.go` (extend), `internal/persistence/store/migration_parity_test.go` (already asserts parity)

**Interfaces:**
- Produces: `humantask.HumanTask.DefID string`, `humantask.HumanTask.DefVersion int`.

- [ ] **Step 1: Write the failing test (record field + store round-trip)**

Extend the humantask store conformance test to set `DefID: "approvals", DefVersion: 2` on a created task and assert `Get` returns them. (Follow the existing conformance-test table form and the `RunTestDatabase` helper.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persistence/store/ -run HumanTask`
Expected: FAIL — `DefID`/`DefVersion` undefined (and, once added, the columns don't exist yet).

- [ ] **Step 3: Add the fields**

`humantask/humantask.go` — add to `HumanTask`:
```go
// DefID / DefVersion reference the process definition that generated this task, so the
// task service can resolve the UserTask node's CompletionValidation via a DefinitionResolver.
// DefVersion == 0 means "latest".
DefID      string
DefVersion int
```

- [ ] **Step 4: Add the migrations**

`internal/persistence/store/migrations/postgres/0012_human_task_defref.sql`:
```sql
-- +goose Up
ALTER TABLE wrkflw_human_task ADD COLUMN def_id      TEXT NOT NULL DEFAULT '';
ALTER TABLE wrkflw_human_task ADD COLUMN def_version INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE wrkflw_human_task DROP COLUMN def_version;
ALTER TABLE wrkflw_human_task DROP COLUMN def_id;
```
`internal/persistence/store/migrations/mysql/0005_human_task_defref.sql`:
```sql
-- +goose Up
ALTER TABLE wrkflw_human_task ADD COLUMN def_id      VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE wrkflw_human_task ADD COLUMN def_version BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE wrkflw_human_task DROP COLUMN def_version;
ALTER TABLE wrkflw_human_task DROP COLUMN def_id;
```
`internal/persistence/store/migrations/sqlite/0004_human_task_defref.sql`:
```sql
-- +goose Up
ALTER TABLE wrkflw_human_task ADD COLUMN def_id      TEXT NOT NULL DEFAULT '';
ALTER TABLE wrkflw_human_task ADD COLUMN def_version INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE wrkflw_human_task DROP COLUMN def_version;
ALTER TABLE wrkflw_human_task DROP COLUMN def_id;
```
> NOTE: MySQL `VARCHAR(255)` for a `NOT NULL DEFAULT ''` indexed-agnostic column matches the store's other text columns; if the existing table uses `TEXT` for similar id columns, match that instead (check `mysql/0003_human_task.sql`).

- [ ] **Step 5: Wire the columns in `humantask_store.go`**

Add `def_id`, `def_version` to the INSERT/UPSERT column list, the placeholders, and the SELECT/scan in `Get`/`List` for the human-task store. Follow the dialect-neutral pattern already used for `node_id`/`claimed_by`.

- [ ] **Step 6: Populate on task creation**

`runtime/processdriver_action.go` around line 197, set `DefID`/`DefVersion` on the `humantask.HumanTask{...}` literal from the driving definition (`def.ID()`, `def.Version()` — use the actual accessors). The driver has `def` in scope at the `AwaitHuman` case.

- [ ] **Step 7: Run tests (all three dialects via conformance + parity)**

Run: `go test ./internal/persistence/store/ -run 'HumanTask|Parity' -race`
Expected: PASS (parity guardrail sees the same logical column added to all three dialects).

- [ ] **Step 8: Commit**

```bash
git add humantask/ internal/persistence/store/ runtime/processdriver_action.go
git commit -m "feat(humantask): carry def Qualifier on tasks (+3-dialect migration) for completion validation"
```

---

## Task 10: Injection — Boundary 1 (start vars in `Drive`)

**Files:**
- Modify: `runtime/processdriver.go` (add `*validation.Gate` field + default; validate in `Drive`)
- Modify: `runtime/processdriver_options.go` (optional `WithValidationGate` if a custom gate is ever needed — otherwise default-construct)
- Test: `runtime/processdriver_validation_test.go`

**Interfaces:**
- Consumes: `validation.Gate`, `event.StartEvent.InputValidation`, `validation.ErrInvalidInput`.
- Produces: `Drive` rejects invalid start vars with `validation.ErrInvalidInput` before any instance is created.

- [ ] **Step 1: Write the failing test**

`runtime/processdriver_validation_test.go`:
```go
package runtime_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/validation"
	vexpr "github.com/kartaladev/wrkflw/validation/expr"
	// + definition + runtime imports as used by existing driver tests
)

func TestDrive_RejectsInvalidStartVars_NoInstanceCreated(t *testing.T) {
	t.Parallel()
	// Build a def whose start event has WithInputValidation(vexpr.New("amount > 0")).
	// Drive with vars {"amount": -1}. Assert:
	//   - returned err Is validation.ErrInvalidInput
	//   - no instance was created (store has zero instances / Load(id) is not-found)
	_ = errors.Is
	_ = validation.ErrInvalidInput
	_ = vexpr.New
}
```
> Use the same in-memory driver construction the existing `runtime` driver tests use (`NewProcessDriver(...)` with a MemStore). Assert non-creation via the store (`driver`-exposed) or via `processtest` if simpler.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run RejectsInvalidStartVars`
Expected: FAIL — no validation happens; instance IS created.

- [ ] **Step 3: Add the Gate + validate in `Drive`**

In `runtime/processdriver.go`: add field `validationGate *validation.Gate`; default-construct in `NewProcessDriver` (`driver.validationGate = validation.NewGate()`). In `Drive`, between the `InstanceState` build (`:308`) and `deliverLoop` (`:309`):
```go
starts := def.StartNodes()
if len(starts) == 1 {
	if se, ok := starts[0].(event.StartEvent); ok && se.InputValidation != nil {
		key := def.ID() + ":" + strconv.Itoa(def.Version()) + ":" + se.ID()
		if err := driver.validationGate.Validate(ctx, key, se.InputValidation, vars); err != nil {
			return engine.InstanceState{}, err
		}
	}
}
```
(Use the actual `StartNodes`/`ID`/`Version` accessors; import `event`, `strconv`, `validation`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./runtime/ -run RejectsInvalidStartVars -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/processdriver.go runtime/processdriver_options.go runtime/processdriver_validation_test.go
git commit -m "feat(runtime): validate start vars in Drive before instance creation"
```

---

## Task 11: Injection — Boundary 2 (completion output in `TaskService.Complete`)

**Files:**
- Modify: `runtime/task/service.go` (add resolver + gate fields, `WithDefinitionResolver` option, validate in `Complete`)
- Test: `runtime/task/service_validation_test.go`

**Interfaces:**
- Consumes: `kernel.DefinitionRegistry` (as the `DefinitionResolver`), `activity.UserTask.CompletionValidation`, `validation.Gate`, `validation.ErrInvalidInput`, `humantask.HumanTask.DefID/DefVersion`.
- Produces:
  - `type DefinitionResolver interface { Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error) }` (narrow, consumer-defined; `kernel.DefinitionRegistry` satisfies it structurally).
  - `func WithDefinitionResolver(r DefinitionResolver) TaskServiceOption`
  - `func WithValidationGate(g *validation.Gate) TaskServiceOption` (optional; default a fresh gate) — so the driver and task service can SHARE one gate if the consumer wires it.
  - `Complete` rejects invalid output with `validation.ErrInvalidInput` after authz, before returning the trigger.

- [ ] **Step 1: Write the failing test**

`runtime/task/service_validation_test.go`:
```go
package task_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/validation"
	vexpr "github.com/kartaladev/wrkflw/validation/expr"
	// + task, humantask, authz, definition, kernel imports
)

func TestComplete_RejectsInvalidOutput(t *testing.T) {
	t.Parallel()
	// Arrange: a def registered in a kernel.MemDefinitionRegistry whose UserTask "approve"
	// has WithCompletionValidation(vexpr.New("decision in ['approve','reject']")).
	// A HumanTask stored with NodeID "approve", DefID/DefVersion pointing at that def, state Claimed.
	// TaskService built with WithDefinitionResolver(reg) and an allow-all authorizer.
	// Act: Complete(ctx, token, actor, {"decision": "maybe"}).
	// Assert: err Is validation.ErrInvalidInput; no trigger usable.
	_ = errors.Is
	_ = validation.ErrInvalidInput
	_ = vexpr.New
}

func TestComplete_AcceptsValidOutput(t *testing.T) {
	t.Parallel()
	// Same setup; Complete with {"decision":"approve"} returns a HumanCompleted trigger, no error.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/task/ -run Complete_Rejects`
Expected: FAIL — `WithDefinitionResolver` undefined; no validation.

- [ ] **Step 3: Implement**

In `runtime/task/service.go`:
- Add the narrow `DefinitionResolver` interface (as above) and struct fields `resolver DefinitionResolver`, `gate *validation.Gate` (default `validation.NewGate()` in `NewTaskService`).
- Add options `WithDefinitionResolver` and `WithValidationGate`.
- In `Complete`, after authz and after `s.store.Get` yields the task, before building the trigger:
```go
if s.resolver != nil {
	def, err := s.resolver.Lookup(ctx, model.Qualifier{ID: task.DefID, Version: task.DefVersion})
	if err != nil {
		return engine.Trigger{}, fmt.Errorf("workflow-task: resolve definition for validation: %w", err)
	}
	if node, ok := def.Node(task.NodeID).(activity.UserTask); ok && node.CompletionValidation != nil {
		key := task.DefID + ":" + strconv.Itoa(task.DefVersion) + ":" + task.NodeID
		if err := s.gate.Validate(ctx, key, node.CompletionValidation, output); err != nil {
			return engine.Trigger{}, err
		}
	}
}
```
(Adjust `engine.Trigger{}` to the actual zero-return; import `model`, `activity`, `validation`, `strconv`, `fmt`. `runtime/task` may import `runtime/kernel`, `definition/model`, `definition/activity` per the decomposition import graph.)
> Fail-closed nuance: if `node.CompletionValidation != nil` but `s.resolver == nil`, the outer `if s.resolver != nil` skips — validation is simply not enforced when no resolver is wired (opt-in). Document this in the option's godoc. If stricter behavior is wanted later, a `WithStrictValidation` flag can error when a slot exists but no resolver is configured — OUT OF SCOPE here.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./runtime/task/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/task/
git commit -m "feat(runtime/task): validate completion output in Complete via DefinitionResolver"
```

---

## Task 12: Injection — Boundary 3 (message payload in `DeliverMessage`)

**Files:**
- Modify: `runtime/processdriver_message.go` (load state, resolve node, validate)
- Test: `runtime/processdriver_message_validation_test.go`

**Interfaces:**
- Consumes: `engine.InstanceState.MessageTargetNode` (Task 8), `activity.ReceiveTask.PayloadValidation`, `event.IntermediateCatchEvent.PayloadValidation`, `validation.Gate`, `validation.ErrInvalidInput`.
- Produces: `DeliverMessage` rejects an invalid payload with `validation.ErrInvalidInput` before `ApplyTrigger`, when the woken node (tier 4) carries a `PayloadValidation` slot; skips for tier-1..3 wakes.

- [ ] **Step 1: Write the failing test**

`runtime/processdriver_message_validation_test.go`:
```go
package runtime_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/validation"
	vexpr "github.com/kartaladev/wrkflw/validation/expr"
)

func TestDeliverMessage_RejectsInvalidPayload(t *testing.T) {
	t.Parallel()
	// Build a def with a ReceiveTask awaiting "OrderPlaced" carrying
	// WithPayloadValidation(vexpr.New("orderID != nil")). Start an instance so the token parks.
	// DeliverMessage(ctx, def, "OrderPlaced", key, {}) -> err Is ErrInvalidInput; token still parked
	// (no state advance). Then DeliverMessage with {"orderID":"o1"} -> nil, token resumes.
	_ = errors.Is
	_ = validation.ErrInvalidInput
	_ = vexpr.New
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run DeliverMessage_RejectsInvalidPayload`
Expected: FAIL — no payload validation.

- [ ] **Step 3: Implement in `runtime/processdriver_message.go`**

Between `NewMessageReceived` (`:25`) and `ApplyTrigger` (`:26`):
```go
st, err := driver.store.Load(ctx, instanceID) // read state to resolve the woken node
if err != nil {
	return err
}
if nodeID, ok := st.State.MessageTargetNode(name, correlationKey); ok {
	if strat := payloadValidationStrategy(def, nodeID); strat != nil {
		key := def.ID() + ":" + strconv.Itoa(def.Version()) + ":" + nodeID
		if err := driver.validationGate.Validate(ctx, key, strat, payload); err != nil {
			return err
		}
	}
}
```
Add a small helper `payloadValidationStrategy(def, nodeID)`:
```go
func payloadValidationStrategy(def *model.ProcessDefinition, nodeID string) validation.ValidationStrategy {
	switch n := def.Node(nodeID).(type) {
	case activity.ReceiveTask:
		return n.PayloadValidation
	case event.IntermediateCatchEvent:
		return n.PayloadValidation
	default:
		return nil
	}
}
```
> Adjust `driver.store.Load`/`st.State` to the ACTUAL types returned (check how `ApplyTrigger` at `processdriver.go:333` loads state — reuse the same accessor so you get the `engine.InstanceState`). If `Load` returns a wrapper, get the `engine.InstanceState` from it. Import `activity`, `event`, `model`, `strconv`, `validation`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./runtime/ -run DeliverMessage -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/processdriver_message.go runtime/processdriver_message_validation_test.go
git commit -m "feat(runtime): validate message payload in DeliverMessage before apply"
```

---

## Task 13: Transport — map `ErrInvalidInput` → HTTP 400

**Files:**
- Modify: `transport/http/httpcore/errors.go`
- Test: `transport/http/httpcore/errors_test.go`

**Interfaces:**
- Consumes: `validation.ErrInvalidInput`.
- Produces: `ClassifyError(validation.ErrInvalidInput-wrapped err)` → `(400, {error:"bad_request", ...})`.

- [ ] **Step 1: Write the failing test**

Extend `transport/http/httpcore/errors_test.go`'s table with a case:
```go
"validation invalid input -> 400": {
	err:  fmt.Errorf("wrap: %w", validation.ErrInvalidInput),
	code: http.StatusBadRequest,
},
```
(Match the existing table's field names / assertion closure.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./transport/http/httpcore/ -run ClassifyError`
Expected: FAIL — validation error classified as 500.

- [ ] **Step 3: Add the case in `ClassifyError`**

In `transport/http/httpcore/errors.go`, extend the 400 case:
```go
case errors.Is(err, kernel.ErrBadCursor), errors.Is(err, ErrBadInput), errors.Is(err, validation.ErrInvalidInput):
	return http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()}
```
Add `import "github.com/kartaladev/wrkflw/validation"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./transport/http/httpcore/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add transport/http/httpcore/
git commit -m "feat(transport): map validation.ErrInvalidInput to HTTP 400"
```

---

## Task 14: Example + ADR-0110

**Files:**
- Create: `examples/scenarios/input_validation/main.go`
- Create: `docs/adr/0110-input-validation-architecture.md`
- Test: (example is a runnable `main`; no unit test — verify it runs)

**Interfaces:**
- Consumes: the full public surface (`validation` + adapters + node options + driver/task wiring).

- [ ] **Step 1: Write ADR-0110**

`docs/adr/0110-input-validation-architecture.md` (Nygard):
- **Status:** Accepted — 2026-07-08.
- **Context:** three external-input boundaries merge caller `map[string]any` into instance vars with no validation; user wants optional, definition-declared, flexible (expr/callback/json-schema/avro/custom) validation without locking the core to a schema lib.
- **Decision:** neutral `validation` port (`Validator`/`ValidationStrategy`/`DescribableStrategy`/`ValidationDescriptor`) + `Registry` + executor-side memoizing `Gate`; opt-in adapter packages; the "input-owner validates" placement (driver validates start/message; task service validates completion beside authz, via a `DefinitionResolver` resolving by `Qualifier`); node-level slots with type-safe options; `{kind,schema}` wire/YAML descriptor round-trip reconstructed by a Loader-threaded registry; `MarshalJSON` fail-closed on a non-serializable callback strategy; a new engine `MessageTargetNode` query resolves the woken node for message payload validation; `HumanTask` gains the def Qualifier (+3-dialect migration).
- **Consequences:** core imports no schema lib; consumers explicitly register the adapters they use; adds `HumanTask.DefID/DefVersion` columns; message validation covers only tier-4 standalone waiters (gateway/boundary/subprocess wakes are unvalidated by design); ADR-0111/0112 record the adapter deps.

- [ ] **Step 2: Write the example**

`examples/scenarios/input_validation/main.go` — build a def with (a) a start event with `event.WithInputValidation(vexpr.New("amount > 0"))` and (b) a UserTask with `activity.WithCompletionValidation(...)`; wire a `ProcessDriver` + `TaskService` sharing a `validation.Gate` and a `DefinitionResolver`; show one REJECTED `Drive` (amount ≤ 0 → `ErrInvalidInput`) and one ACCEPTED `Drive`, then a rejected + accepted `Complete`. Follow the `examples/scenarios/*` house style (per [[examples-dir-purpose]] — examples show engine mechanics, NOT test helpers; do not import `processtest`). Log the outcomes with `slog`.

- [ ] **Step 3: Verify the example builds and runs**

Run: `go build ./examples/... && go run ./examples/scenarios/input_validation`
Expected: prints the rejected + accepted outcomes; exit 0.

- [ ] **Step 4: Commit**

```bash
git add examples/scenarios/input_validation/ docs/adr/0110-input-validation-architecture.md
git commit -m "docs(validation): input_validation example + ADR-0110 architecture"
```

---

## Final verification (before requesting review / merge)

- [ ] `go build ./...` clean.
- [ ] `go test -race ./...` from the repo root — 0 failures. New packages ≥ 85% line coverage:
  `go test -race -coverprofile=cover.out ./validation/... && go tool cover -func=cover.out | tail -1`
- [ ] `golangci-lint run ./...` clean.
- [ ] Migration parity guardrail passes (`internal/persistence/store` parity test) with the new `def_id`/`def_version` columns present in all three dialects.
- [ ] `engine/` behavior unchanged except the additive `MessageTargetNode` (no diff to `handleMessageReceived`).
- [ ] Self-audit: every new exported symbol has an observable red→green in the commit history.
- [ ] Run `/code-review` over the whole branch; address Critical/Important; then `superpowers:finishing-a-development-branch` → `--no-ff` merge to `main`.

## Spec-coverage self-check

- Port + strategy + registry → Task 1. Gate (executor cache) → Task 1.
- expr/callback/jsonschema/avro adapters → Tasks 2/3/4/5. JSON programmatic+struct authoring → Task 4.
- Node slots + type-safe options → Task 6.
- Wire/YAML round-trip + fail-closed MarshalJSON + Loader registry → Task 7.
- Message node resolution (engine query) → Task 8.
- Completion resolver + HumanTask Qualifier + persistence → Tasks 9 & 11.
- Three injection points → Tasks 10 (start), 11 (completion), 12 (message).
- Transport 400 → Task 13.
- Example + ADR-0110/0111/0112 → Tasks 14 / 4 / 5.
