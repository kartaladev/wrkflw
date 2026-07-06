# Server-Generated Instance ID Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the engine always generate a process-instance's `InstanceID` server-side via a pluggable `idgen.Generator` (xid default, UUID v7 alternative), replacing the caller-supplied ID.

**Architecture:** A new nested package `runtime/idgen` defines a `Generator` interface (`NewID() (string, error)`) with `XID()` (default), `UUIDv7()`, and a `Func(...)` test adapter. The generator is injected via `WithIDGenerator` on both `runtime.ProcessDriver` and `service.Engine` (nil-guarded, default `idgen.XID()`), exactly like the existing `WithClock` seam. `runtime.ProcessDriver.Run` generates when the passed `instanceID` is empty; `service.Engine.StartInstance` always mints one, and the caller-supplied `InstanceID` input is removed from the service request and the HTTP DTO.

**Tech Stack:** Go 1.25; `github.com/rs/xid` (new); `github.com/google/uuid v1.6.0` (already present); `clock.Clock` seam as the pattern to mirror.

## Global Constraints

- **Language:** Go 1.25 (hard requirement).
- **TDD strict:** No production code before a failing test. Every new exported symbol and behavioral change is preceded by a Bash `go test ./<package>/...` run showing red (a compile error like `undefined: idgen.XID` is a valid red state). Never create test + impl in one edit pass with no `go test` between them.
- **Package location:** the generator lives in the NEW nested package `runtime/idgen` (module path `github.com/zakyalvan/krtlwrkflw/runtime/idgen`) — NOT a new root package, NOT in `engine` (engine stays vendor-pure).
- **Generator interface:** `NewID() (string, error)` — the error lets a rare UUID v7 entropy failure surface as a clean `StartInstance` error. `xid` never errors (returns nil).
- **Default strategy:** `idgen.XID()`. Options are nil-guarded and preserve the default (mirror `WithClock`).
- **Derived IDs untouched:** command IDs, task tokens, timer IDs, outbox dedup keys, and child-instance IDs (`<parent>-sub-cN`) keep embedding the InstanceID verbatim — do NOT modify them.
- **Error sentinels:** message prefix `workflow-<package>:` (e.g. `workflow-idgen:`).
- **Module path:** `github.com/zakyalvan/krtlwrkflw`.
- **Coverage:** each touched package ≥ 85% line coverage (`go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`).
- **Lint:** `golangci-lint run ./...` clean before any task is "done".
- **Docs:** ADRs use the Nygard template under `docs/adr/NNNN-<slug>.md`; next free number is **0100**.
- **Commits:** Conventional Commits scoped to the area; commit per task. End commit messages with the two trailer lines the harness requires:
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf
  ```

---

### Task 1: `runtime/idgen` package (Generator + XID + UUIDv7 + Func)

**Files:**
- Create: `runtime/idgen/idgen.go`
- Test: `runtime/idgen/idgen_test.go`
- Modify: `go.mod`, `go.sum` (add `github.com/rs/xid`)

**Interfaces:**
- Produces:
  - `idgen.Generator` interface: `NewID() (string, error)`.
  - `idgen.XID() Generator` — backed by `github.com/rs/xid`; `NewID` never errors.
  - `idgen.UUIDv7() Generator` — backed by `github.com/google/uuid` `NewV7`; `NewID` propagates the entropy error.
  - `idgen.Func(fn func() (string, error)) Generator` — function adapter / deterministic test seam.

- [ ] **Step 1: Write the failing test**

`runtime/idgen/idgen_test.go`:
```go
package idgen_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
)

func TestXID(t *testing.T) {
	g := idgen.XID()
	id1, err := g.NewID()
	if err != nil {
		t.Fatalf("xid NewID error: %v", err)
	}
	if id1 == "" {
		t.Fatal("xid NewID returned empty string")
	}
	id2, _ := g.NewID()
	if id1 == id2 {
		t.Fatalf("xid NewID not unique: %q == %q", id1, id2)
	}
	// xid string form has no hyphens (matters for the child-suffix parsing).
	for _, c := range id1 {
		if c == '-' {
			t.Fatalf("xid contains a hyphen: %q", id1)
		}
	}
}

func TestUUIDv7(t *testing.T) {
	g := idgen.UUIDv7()
	id, err := g.NewID()
	if err != nil {
		t.Fatalf("uuidv7 NewID error: %v", err)
	}
	u, perr := uuid.Parse(id)
	if perr != nil {
		t.Fatalf("uuidv7 produced unparseable UUID %q: %v", id, perr)
	}
	if got := u.Version(); got != 7 {
		t.Fatalf("expected UUID version 7, got %d", got)
	}
}

func TestFunc(t *testing.T) {
	t.Run("returns wrapped value", func(t *testing.T) {
		g := idgen.Func(func() (string, error) { return "fixed-1", nil })
		id, err := g.NewID()
		if err != nil || id != "fixed-1" {
			t.Fatalf("Func NewID = %q, %v", id, err)
		}
	})
	t.Run("propagates wrapped error", func(t *testing.T) {
		sentinel := errors.New("boom")
		g := idgen.Func(func() (string, error) { return "", sentinel })
		_, err := g.NewID()
		if !errors.Is(err, sentinel) {
			t.Fatalf("Func did not propagate error: %v", err)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go get github.com/rs/xid && go test ./runtime/idgen/...`
Expected: FAIL — `undefined: idgen.XID` / package has no non-test files.

- [ ] **Step 3: Write minimal implementation**

`runtime/idgen/idgen.go`:
```go
// Package idgen mints process-instance identifiers behind a pluggable strategy.
// It is the ID-generation counterpart to the clock package: a small, injectable
// seam with a sensible default (xid) that consumers override via
// runtime.WithIDGenerator / service.WithIDGenerator.
package idgen

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/xid"
)

// Generator mints a unique process-instance identifier. NewID returns an error
// so a rare entropy failure (e.g. from UUID v7) surfaces as a clean caller error
// rather than a panic; the xid generator never errors.
type Generator interface {
	NewID() (string, error)
}

// XID returns the default generator, backed by github.com/rs/xid. The returned
// IDs are ~20-char lowercase base32hex with no hyphens, k-sortable, and need no
// external coordination. NewID always returns a nil error.
func XID() Generator { return xidGen{} }

type xidGen struct{}

func (xidGen) NewID() (string, error) { return xid.New().String(), nil }

// UUIDv7 returns a generator backed by github.com/google/uuid NewV7
// (chronologically sortable, RFC 9562). NewID propagates the rare entropy error.
func UUIDv7() Generator { return uuidV7Gen{} }

type uuidV7Gen struct{}

func (uuidV7Gen) NewID() (string, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("workflow-idgen: uuidv7: %w", err)
	}
	return u.String(), nil
}

// Func adapts a plain function into a Generator. Use it in tests to inject a
// deterministic sequence via WithIDGenerator.
func Func(fn func() (string, error)) Generator { return funcGen(fn) }

type funcGen func() (string, error)

func (f funcGen) NewID() (string, error) { return f() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go mod tidy && go test ./runtime/idgen/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum runtime/idgen/
git commit -m "feat(idgen): pluggable instance-ID generator (xid default, uuidv7)"
```

---

### Task 2: `runtime.WithIDGenerator` + generate-on-empty in `Run`

**Files:**
- Modify: `runtime/processdriver.go` (add `idgen` field ~line 37; default in `NewProcessDriver` ~line 111; generate-on-empty in `Run` ~line 190)
- Modify: `runtime/processdriver_options.go` (add `WithIDGenerator` near `WithClock` ~line 195)
- Test: `runtime/processdriver_idgen_test.go`

**Interfaces:**
- Consumes: `idgen.Generator`, `idgen.XID`, `idgen.Func` (Task 1).
- Produces: `runtime.WithIDGenerator(gen idgen.Generator) Option`; `ProcessDriver.Run` generates when `instanceID == ""`.

- [ ] **Step 1: Write the failing test**

`runtime/processdriver_idgen_test.go`:
```go
package runtime_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
)

// minimalDef builds a trivial single-node definition that starts and ends.
func minimalDef(t *testing.T) *modelProcessDefinition { /* see note */ }

func TestRunGeneratesWhenInstanceIDEmpty(t *testing.T) {
	def := buildStartEndDefinition(t) // helper: a def whose start immediately completes
	r, err := runtime.NewProcessDriver(
		runtime.WithIDGenerator(idgen.Func(func() (string, error) { return "gen-123", nil })),
	)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	st, err := r.Run(t.Context(), def, "", map[string]any{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.InstanceID != "gen-123" {
		t.Fatalf("expected generated id gen-123, got %q", st.InstanceID)
	}
}

func TestRunUsesExplicitInstanceID(t *testing.T) {
	def := buildStartEndDefinition(t)
	r, err := runtime.NewProcessDriver(
		runtime.WithIDGenerator(idgen.Func(func() (string, error) { return "SHOULD-NOT-BE-USED", nil })),
	)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	st, err := r.Run(t.Context(), def, "explicit-1", map[string]any{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.InstanceID != "explicit-1" {
		t.Fatalf("expected explicit-1, got %q", st.InstanceID)
	}
}

func TestRunPropagatesGeneratorError(t *testing.T) {
	def := buildStartEndDefinition(t)
	boom := errors.New("no entropy")
	r, _ := runtime.NewProcessDriver(
		runtime.WithIDGenerator(idgen.Func(func() (string, error) { return "", boom })),
	)
	_, err := r.Run(t.Context(), def, "", map[string]any{})
	if !errors.Is(err, boom) {
		t.Fatalf("expected generator error to propagate, got %v", err)
	}
}
```

> **Definition helper:** reuse the repo's existing test-definition builder. Grep an existing `runtime/*_test.go` for how a minimal start→end definition is built (e.g. via `definition.NewBuilder("d", 1)....Build()` with a start event flowing to an end event) and factor a local `buildStartEndDefinition(t)` in this test file. Do NOT invent a new builder API — copy the shape from a neighboring runtime test. Remove the `minimalDef`/`modelProcessDefinition` stub above; it is only a signature placeholder to delete.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run 'TestRun.*InstanceID|TestRunPropagatesGeneratorError'`
Expected: FAIL — `undefined: runtime.WithIDGenerator`.

- [ ] **Step 3: Write minimal implementation**

In `runtime/processdriver.go`, add the field to the `ProcessDriver` struct (after `clk clock.Clock`):
```go
	idgen      idgen.Generator
```
Add the import `"github.com/zakyalvan/krtlwrkflw/runtime/idgen"`.

In `NewProcessDriver`, add the default to the struct literal (alongside `clk: clock.System(),`):
```go
		idgen:         idgen.XID(),
```

In `Run`, generate when empty — change the body start from:
```go
	defer span.End()
	st := engine.InstanceState{InstanceID: instanceID}
```
to:
```go
	defer span.End()
	if instanceID == "" {
		id, gerr := r.idgen.NewID()
		if gerr != nil {
			span.RecordError(gerr)
			return engine.InstanceState{}, fmt.Errorf("workflow-runtime: run: generate id: %w", gerr)
		}
		instanceID = id
	}
	st := engine.InstanceState{InstanceID: instanceID}
```
(Note: the tracing span attribute `wrkflw.instance_id` is set at span-start with the possibly-empty value; that is acceptable — the generated id is on the returned state. If you prefer, set an additional `span.SetAttributes(attribute.String("wrkflw.instance_id", instanceID))` after generation.)

In `runtime/processdriver_options.go`, add after `WithClock`:
```go
// WithIDGenerator sets the strategy used to mint a process-instance ID when
// ProcessDriver.Run is called with an empty instanceID. Default: idgen.XID().
// A nil generator is ignored. Inject idgen.Func in tests for determinism.
func WithIDGenerator(gen idgen.Generator) Option {
	return func(r *ProcessDriver) {
		if gen != nil {
			r.idgen = gen
		}
	}
}
```
Add the `idgen` import to the options file if not already present.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./runtime/ -run 'TestRun.*InstanceID|TestRunPropagatesGeneratorError'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/processdriver.go runtime/processdriver_options.go runtime/processdriver_idgen_test.go
git commit -m "feat(runtime): WithIDGenerator; ProcessDriver.Run generates on empty id"
```

---

### Task 3: Service always-generates; remove caller-supplied InstanceID (service + HTTP + sweep)

**Files:**
- Modify: `service/service.go` (engineConfig `idgen` field ~line 115 area; default ~line 153; wire into driver opts ~line 184; `Engine.idgen` field ~line 209; `StartInstance` ~line 263)
- Modify: `service/options.go` (add `WithIDGenerator` near `WithClock` ~line 82)
- Modify: `service/request.go` (remove `StartInstanceRequest.InstanceID`, lines 15-16)
- Modify: `transport/http/httpcore/dto.go` (remove `StartInput.InstanceID`, line 13)
- Modify: `transport/http/httpcore/endpoints.go` (remove `InstanceID: in.InstanceID,` mapping, ~line 30)
- Sweep (compile-driven): every construction site of `service.StartInstanceRequest{... InstanceID ...}` and `httpcore.StartInput{... InstanceID ...}` and any JSON body with `"instance_id"` in tests/examples.
- Test: `service/service_idgen_test.go`

**Interfaces:**
- Consumes: `idgen.Generator`, `idgen.XID`, `idgen.Func` (Task 1); `runtime.WithIDGenerator` (Task 2).
- Produces: `service.WithIDGenerator(gen idgen.Generator) Option`; `service.Engine.StartInstance` always generates the InstanceID; `StartInstanceRequest.InstanceID` removed; `StartInput.instance_id` removed.

- [ ] **Step 1: Write the failing test**

`service/service_idgen_test.go`:
```go
package service_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
	"github.com/zakyalvan/krtlwrkflw/service"
)

func TestStartInstanceGeneratesID(t *testing.T) {
	eng := buildTestEngine(t, // helper mirroring existing service tests' engine setup
		service.WithIDGenerator(idgen.Func(func() (string, error) { return "svc-gen-1", nil })),
	)
	registerStartEndDefinition(t, eng) // helper: register a trivial def "d:1"
	pi, err := eng.StartInstance(t.Context(), service.StartInstanceRequest{DefRef: "d", Vars: map[string]any{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if pi.State().InstanceID != "svc-gen-1" {
		t.Fatalf("expected svc-gen-1, got %q", pi.State().InstanceID)
	}
}

func TestStartInstancePropagatesGeneratorError(t *testing.T) {
	boom := errors.New("no entropy")
	eng := buildTestEngine(t,
		service.WithIDGenerator(idgen.Func(func() (string, error) { return "", boom })),
	)
	registerStartEndDefinition(t, eng)
	_, err := eng.StartInstance(t.Context(), service.StartInstanceRequest{DefRef: "d", Vars: map[string]any{}})
	if !errors.Is(err, boom) {
		t.Fatalf("expected generator error, got %v", err)
	}
}
```

> **Test helpers:** reuse the existing service-test scaffolding. Grep `service/*_test.go` for how an in-memory `*service.Engine` is built (e.g. `service.NewEngine(...)` with a mem definition registry) and for how a trivial definition is registered, and factor `buildTestEngine`/`registerStartEndDefinition` from that existing shape — do not invent new setup APIs.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./service/ -run 'TestStartInstanceGeneratesID|TestStartInstancePropagatesGeneratorError'`
Expected: FAIL — `undefined: service.WithIDGenerator` (and `StartInstanceRequest` still has `InstanceID`).

- [ ] **Step 3: Write minimal implementation — service layer**

In `service/service.go`:
- Add to `engineConfig` (near `clk clock.Clock`, line 115): `idgen idgen.Generator`.
- Add to `Engine` struct (near `clk clock.Clock`): `idgen idgen.Generator`.
- Add the default after the `clk` default (line 153-155):
  ```go
  if c.idgen == nil {
      c.idgen = idgen.XID()
  }
  ```
- Wire into the default driver opts (in the `dopts := []runtime.Option{...}` block, ~line 184):
  ```go
  runtime.WithIDGenerator(c.idgen),
  ```
- Set it on the `Engine` literal (~line 209, alongside `clk: c.clk,`): `idgen: c.idgen,`.
- Rewrite `StartInstance` (line 263) to mint and pass the id:
  ```go
  func (e *Engine) StartInstance(ctx context.Context, req StartInstanceRequest) (ProcessInstance, error) {
      def, err := e.reg.Lookup(ctx, req.DefRef)
      if err != nil {
          return nil, fmt.Errorf("workflow-service: start instance: %w", err)
      }
      id, err := e.idgen.NewID()
      if err != nil {
          return nil, fmt.Errorf("workflow-service: start instance: generate id: %w", err)
      }
      st, err := e.runner.Run(ctx, def, id, req.Vars)
      if err != nil {
          return nil, fmt.Errorf("workflow-service: start instance: run: %w", err)
      }
      return NewProcessInstance(def, st), nil
  }
  ```
- Add the import `"github.com/zakyalvan/krtlwrkflw/runtime/idgen"`.

In `service/options.go`, add after `WithClock`:
```go
// WithIDGenerator sets the strategy used to mint every new process-instance ID.
// Default: idgen.XID(). A nil generator is ignored. It is also threaded into the
// default driver, so runtime and service agree on the strategy.
func WithIDGenerator(gen idgen.Generator) Option {
	return func(c *engineConfig) {
		if gen != nil {
			c.idgen = gen
		}
	}
}
```
Add the `idgen` import to `service/options.go`.

- [ ] **Step 4: Run test to verify service tests pass (request field still present)**

Run: `go test ./service/ -run 'TestStartInstanceGeneratesID|TestStartInstancePropagatesGeneratorError'`
Expected: PASS.

- [ ] **Step 5: Remove the caller-supplied `InstanceID` field (compile-driven sweep)**

Delete the `InstanceID` field from `service/request.go` (`StartInstanceRequest`, the doc+field at lines 14-16) and from `transport/http/httpcore/dto.go` (`StartInput`, line 13), and remove the `InstanceID: in.InstanceID,` mapping in `transport/http/httpcore/endpoints.go` (~line 30). Update the `StartInput` doc comment ("Both DefRef and InstanceID are required" → "DefRef is required; the instance ID is server-generated").

Then run the build to surface every remaining construction site:
```bash
go build ./... 2>&1 | head -40
```
Fix each dangling reference:
- Test/example literals `service.StartInstanceRequest{DefRef: ..., InstanceID: "x", Vars: ...}` → drop `InstanceID:`.
- Test/example literals `httpcore.StartInput{DefRef: ..., InstanceID: "x", ...}` → drop `InstanceID:`.
- JSON request bodies in HTTP tests containing `"instance_id": "..."` → remove that key.
- Where a test asserted a *specific* instance ID from a service/HTTP start, either (a) inject `service.WithIDGenerator(idgen.Func(func() (string, error) { return "fixed", nil }))` when building the engine and assert `"fixed"`, or (b) read the returned ID from the response/`ProcessInstance` and use it for subsequent calls (signal/get/cancel by that ID). Prefer (b) for round-trip tests, (a) when a literal assertion is clearer.
- The transport parity suite (`transport/http/parity`, `stdlib`, `gin`, `fiber`) and `internal/transporttest` fakes: drop `instance_id` from request bodies; the `service.Service` interface method signature is unchanged, so fakes only change where they build a `StartInstanceRequest` literal (if at all).

- [ ] **Step 6: Run the full touched-area suite**

Run: `go build ./... && go test ./service/... ./transport/... ./runtime/... ./examples/...`
Expected: PASS, build clean.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(service): always server-generate InstanceID; remove caller-supplied id"
```

---

### Task 4: ADR-0100 + CHANGELOG

**Files:**
- Create: `docs/adr/0100-server-generated-instance-id.md`
- Modify: `CHANGELOG.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: Write the ADR (Nygard template)**

`docs/adr/0100-server-generated-instance-id.md` with **Status** (Accepted, 2026-07-06), **Context**, **Decision**, **Consequences**. Content (verify against the shipped code before writing):
- Context: InstanceID was caller-supplied and `validate:"required"`; no server minting existed; we want an engine-owned, configurable strategy.
- Decision: new nested package `runtime/idgen` with `Generator` (`NewID() (string, error)`), `XID()` default (rs/xid), `UUIDv7()` (google/uuid), `Func` adapter; `WithIDGenerator` on `runtime` + `service` (default `idgen.XID()`, nil-guarded, WithClock-style); `runtime.ProcessDriver.Run` generates on empty id; `service.Engine.StartInstance` always generates; `InstanceID` removed from `StartInstanceRequest` and the HTTP `StartInput` (`instance_id` gone from the wire). Derived IDs and child `<parent>-sub-cN` IDs unchanged. `NewID` returns an error so uuidv7 entropy failures surface cleanly.
- Consequences: positive (engine-owned consistent IDs, pluggable, sortable, WithClock-parity ergonomics); negative (idempotency loss on start retries — accepted, future idempotency-key follow-up; breaking wire/API removal of `instance_id`, pre-v0.1.0; one new tiny dep rs/xid).

- [ ] **Step 2: Update CHANGELOG**

Read `CHANGELOG.md` to match its format. Add: under Breaking — `instance_id` removed from the start-instance request/DTO; `StartInstanceRequest.InstanceID` removed; instance IDs are now server-generated. Under Added — `runtime/idgen` package + `WithIDGenerator` (service + runtime), xid default / uuidv7 option.

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0100-server-generated-instance-id.md CHANGELOG.md
git commit -m "docs(adr): 0100 server-generated instance id"
```

---

### Task 5: Final verification

**Files:** none (verification only; fix-forward any failures with a follow-up commit).

- [ ] **Step 1: Full race + coverage**

Run: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`
Expected: PASS, no data races. Confirm `runtime/idgen` ≥ 85% (`go tool cover -func=cover.out | grep runtime/idgen`) and no regression in `runtime`/`service`/`transport`.

- [ ] **Step 2: Lint + vet**

Run: `golangci-lint run ./... && go vet ./...`
Expected: no findings.

- [ ] **Step 3: Confirm no lingering `instance_id` caller input**

Run: `grep -rn "instance_id\"\|InstanceID:" --include=*.go transport/ service/ examples/ | grep -i "startinput\|startinstancerequest\|\"instance_id\"" || echo "clean"`
Expected: no start-instance *input* sites remain (response DTOs that expose the generated `instance_id` are fine and expected).

- [ ] **Step 4: Examples build**

Run: `go build ./examples/...`
Expected: clean.

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "test(idgen): close verification gaps for server-generated instance id"
```

---

## Self-Review

**Spec coverage:**
- `runtime/idgen` package (Generator/XID/UUIDv7/Func) → Task 1. ✓
- `NewID() (string, error)` → Task 1 (interface) + Task 2/3 (error propagation). ✓
- `WithIDGenerator` at runtime + service, default `idgen.XID()`, nil-guarded → Tasks 2, 3. ✓
- Runtime generate-on-empty; explicit id preserved; children untouched → Task 2. ✓
- Service always-generate; `InstanceID` removed from `StartInstanceRequest` + HTTP `StartInput`; generated id returned → Task 3. ✓
- Derived IDs untouched (no task modifies them) ✓.
- New dep `rs/xid`; uuid already present → Task 1. ✓
- ADR-0100 + idempotency tradeoff note → Task 4. ✓
- TDD-strict, coverage/lint gates → every task + Task 5. ✓

**Placeholder scan:** No "TBD"/"add error handling"/"similar to Task N". The two helper-builder notes (Task 2 `buildStartEndDefinition`, Task 3 `buildTestEngine`) point at concrete existing test scaffolding to copy rather than inventing APIs — they are grounding instructions, not placeholders, because the exact builder API differs per existing test and must be matched, not guessed.

**Type consistency:** `idgen.Generator`/`XID`/`UUIDv7`/`Func` names stable across Tasks 1-3. `WithIDGenerator(gen idgen.Generator)` identical signature at runtime (Task 2) and service (Task 3). `NewID() (string, error)` consistent everywhere. `StartInstanceRequest`/`StartInput` field removal consistent between Task 3 steps and Task 5's grep guard.
