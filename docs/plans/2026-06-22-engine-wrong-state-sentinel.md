# Engine Wrong-State Sentinel + `workflow-` Prefix Sweep — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a typed engine-level wrong-state sentinel (`engine.ErrInvalidTransition`) that `ErrTokenNotFound` wraps, classify it up through service + transports to 422 / `FailedPrecondition`, and adopt the `workflow-` error-message prefix repo-wide.

**Architecture:** Pure error-wrapping — `ErrTokenNotFound` wraps a new parent sentinel so all seven wrong-state call sites stay untouched and `Step`'s `(state, commands)` output is byte-identical. `service` classifies a leaked engine wrong-state error into the existing `ErrConflict`; transports gain a direct fallback case. A mechanical, behavior-preserving sweep prefixes every production error message's package segment with `workflow-`.

**Tech Stack:** Go 1.25, stdlib `errors`/`fmt`, testify, project `table-test` skill.

## Global Constraints

- **TDD strict:** every new symbol/behavior gets a failing test with a visible RED (`go test ./<pkg>/...`) before the impl. (CLAUDE.md "TDD Operational Discipline".)
- **Engine/model purity:** no transport/storage/bus/time-vendor imports in `engine`/`model`; `Step` output unchanged. Verify: `engine`+`model` import only stdlib + `authz`/`humantask`/`expreval`/`model`.
- **Error sentinel prefix:** production error messages prefix the package segment with `workflow-` (e.g. `"engine: ..."` → `"workflow-engine: ..."`). Assert on sentinels with `errors.Is`, never string-matching.
- **Tests:** black-box (`package <pkg>_test`); table-driven with the **`assert` closure per case** (project `table-test` skill, not `want`/`wantErr`); `t.Context()`; pair each `foo.go` with `foo_test.go`.
- **Lint:** `golangci-lint` is v2 (`.golangci.yml`, `version: "2"`).
- **Verify on completion:** `go test -race ./...` green (Postgres pkg with `-p 1`); coverage ≥ 85% on touched packages; lint clean.
- **Commits:** Conventional Commits scoped to the area; end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **Branch:** `feat/engine-wrong-state-sentinel` (never implement on `main`).

---

### Task 1: `engine.ErrInvalidTransition` sentinel + relocate sentinels to `engine/errors.go`

**Files:**
- Create: `engine/errors.go`
- Create: `engine/errors_test.go`
- Modify: `engine/step.go:14-18` (remove the relocated `var (...)` block)
- Modify: `engine/step_errors_test.go` (extend the existing late-trigger assertion)

**Interfaces:**
- Produces: `engine.ErrInvalidTransition` (`error`), `engine.ErrUnknownTrigger`, `engine.ErrTokenNotFound`, `engine.ErrNoMatchingFlow` — all now defined in `engine/errors.go`. Invariant: `errors.Is(ErrTokenNotFound, ErrInvalidTransition) == true`; the other two do **not** wrap `ErrInvalidTransition`.

- [ ] **Step 1: Write the failing test** — `engine/errors_test.go`

```go
package engine_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

func TestSentinelWrappingGraph(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		assert func(t *testing.T)
	}{
		{
			name: "ErrTokenNotFound wraps ErrInvalidTransition",
			assert: func(t *testing.T) {
				assert.ErrorIs(t, engine.ErrTokenNotFound, engine.ErrInvalidTransition,
					"a token-not-awaiting error is a wrong-state transition")
			},
		},
		{
			name: "ErrNoMatchingFlow is not a wrong-state transition",
			assert: func(t *testing.T) {
				assert.NotErrorIs(t, engine.ErrNoMatchingFlow, engine.ErrInvalidTransition,
					"gateway routing failure is a definition error, not wrong-state")
			},
		},
		{
			name: "ErrUnknownTrigger is not a wrong-state transition",
			assert: func(t *testing.T) {
				assert.NotErrorIs(t, engine.ErrUnknownTrigger, engine.ErrInvalidTransition,
					"unsupported trigger type is an infrastructure error, not wrong-state")
			},
		},
		{
			name: "wrapped ErrTokenNotFound still satisfies both sentinels",
			assert: func(t *testing.T) {
				wrapped := fmt.Errorf("workflow-runtime: step: %w", engine.ErrTokenNotFound)
				assert.ErrorIs(t, wrapped, engine.ErrTokenNotFound)
				assert.ErrorIs(t, wrapped, engine.ErrInvalidTransition)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

var _ = errors.Is // keep errors import if unused after edits
```

Note: add `"fmt"` to the import block (the last case uses `fmt.Errorf`); drop the `var _ = errors.Is` line and the `errors` import if `errors` ends up unused.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/...`
Expected: FAIL — `undefined: engine.ErrInvalidTransition`.

- [ ] **Step 3: Write minimal implementation** — create `engine/errors.go`

```go
package engine

import (
	"errors"
	"fmt"
)

// ErrInvalidTransition classifies a trigger that cannot be applied because the
// targeted instance/token is not in a state that accepts it. The instance exists —
// this is a conflict, not a "not found". Consumers classify wrong-state transitions
// with errors.Is(err, ErrInvalidTransition); the service layer maps it to ErrConflict
// and transports map it to HTTP 422 / gRPC FailedPrecondition.
var ErrInvalidTransition = errors.New("workflow-engine: invalid state transition")

var (
	// ErrUnknownTrigger is returned when a trigger type has no handler. It is an
	// infrastructure/programming error, not a wrong-state transition.
	ErrUnknownTrigger = errors.New("workflow-engine: unknown trigger")

	// ErrTokenNotFound is returned when a trigger targets a command/task token that
	// is not awaiting. It is one kind of invalid transition and wraps
	// ErrInvalidTransition so errors.Is holds for both sentinels.
	ErrTokenNotFound = fmt.Errorf("workflow-engine: no token awaiting command: %w", ErrInvalidTransition)

	// ErrNoMatchingFlow is returned when a gateway has no matching/default outgoing
	// flow. It is a definition/data error, not a wrong-state transition.
	ErrNoMatchingFlow = errors.New("workflow-engine: no matching outgoing flow")
)
```

Then **remove** the old `var (...)` block from `engine/step.go:14-18`. Leave the `engine/step.go` imports (`errors`, `fmt`, ...) as-is — they are still used elsewhere in the file. If `go vet`/lint reports `errors` newly unused in `step.go`, remove only that import.

- [ ] **Step 4: Extend the existing late-trigger behavioral test** — `engine/step_errors_test.go`

Find the existing assertion `assert.ErrorIs(t, lateErr, engine.ErrTokenNotFound, ...)` (≈ line 830) and add immediately after it:

```go
	assert.ErrorIs(t, lateErr, engine.ErrInvalidTransition,
		"a late/wrong-state trigger is classifiable as an invalid transition")
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./engine/...`
Expected: PASS (all engine tests, including the existing suite, stay green — proof the relocation is behavior-preserving).

- [ ] **Step 6: Verify engine purity unchanged**

Run: `go list -deps ./engine | grep -E 'transport|persistence|watermill|gocron|clockwork' || echo "PURE"`
Expected: `PURE`.

- [ ] **Step 7: Commit**

```bash
git add engine/errors.go engine/errors_test.go engine/step.go engine/step_errors_test.go
git commit -m "feat(engine): add ErrInvalidTransition wrong-state sentinel

ErrTokenNotFound now wraps ErrInvalidTransition so direct engine consumers
can classify wrong-state transitions with errors.Is. Sentinels relocated to
engine/errors.go. Step output unchanged (pure reclassification via wrapping).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Service classifies leaked `engine.ErrInvalidTransition` as `ErrConflict`

**Files:**
- Modify: `service/service.go:146-160` (`DeliverSignal`), `:164-173` (`DeliverMessage`), `:253-273` (`deliverTaskTrigger`)
- Modify: `service/errors.go:14` (apply `workflow-` prefix to the `ErrConflict` message)
- Modify: `service/errors_test.go` (add the race-gap case)

**Interfaces:**
- Consumes: `engine.ErrInvalidTransition` (Task 1).
- Produces: `DeliverSignal`/`DeliverMessage`/`deliverTaskTrigger` now return `service.ErrConflict` (wrapping the engine cause) when the runner reports a wrong-state transition. `errors.Is(err, service.ErrConflict)` holds; the engine cause stays inspectable via `errors.Is(err, engine.ErrInvalidTransition)`.

- [ ] **Step 1: Write the failing test** — append to `service/errors_test.go`

Add a case proving a wrong-state error that escapes the pre-flight guard (the task is still Open and the instance is non-terminal at check time, but the engine rejects the trigger because the token is not awaiting) is classified as `ErrConflict`. Use the existing test harness in `service/errors_test.go` to build a service whose runner returns an `engine.ErrInvalidTransition`-wrapped error; if the existing tests construct a real engine + store, drive the instance so the token is no longer awaiting (e.g. complete the task once, then deliver a second `HumanCompleted` for the same token through a path that bypasses pre-flight — or inject a stub runner). Minimal form using a stub runner:

```go
func TestErrConflict_EngineWrongStateClassified(t *testing.T) {
	ctx := t.Context()
	// svc built with a runner stub whose Deliver returns a wrapped engine wrong-state error.
	// (Reuse the package's existing service-construction helper; substitute the runner.)
	_, err := svc.DeliverSignal(ctx, service.DeliverSignalRequest{
		InstanceID: "inst-race",
		Signal:     "go",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, service.ErrConflict,
		"an engine wrong-state error leaking past pre-flight must classify as ErrConflict")
	require.ErrorIs(t, err, engine.ErrInvalidTransition,
		"the engine cause stays inspectable through the wrap")
}
```

If the package lacks an easily-substitutable runner, prefer the real-engine path: start an instance with a human task, complete it, then call `CompleteTask` again — the task store now reports the task closed, so to reach the engine you must drive a *signal* race instead. The implementer should pick whichever of the two the existing harness supports and keep the assertion identical. (Document the chosen mechanism in a test comment.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./service/...`
Expected: FAIL — the wrong-state error currently propagates as `service: deliver signal: ...` without `ErrConflict`.

- [ ] **Step 3: Write minimal implementation** — `service/service.go`

In `DeliverSignal`, replace the runner-error return (≈ line 156-158):

```go
	newSt, err := e.runner.Deliver(ctx, def, st.InstanceID, trg)
	if err != nil {
		if errors.Is(err, engine.ErrInvalidTransition) {
			return engine.InstanceState{}, fmt.Errorf("%w: %v", ErrConflict, err)
		}
		return engine.InstanceState{}, fmt.Errorf("workflow-service: deliver signal: %w", err)
	}
	return newSt, nil
```

In `deliverTaskTrigger`, replace the runner-error return (≈ line 268-271):

```go
	newSt, err := e.runner.Deliver(ctx, def, task.InstanceID, trg)
	if err != nil {
		if errors.Is(err, engine.ErrInvalidTransition) {
			return engine.InstanceState{}, fmt.Errorf("%w: %v", ErrConflict, err)
		}
		return engine.InstanceState{}, fmt.Errorf("workflow-service: deliver task trigger: deliver: %w", err)
	}
	return newSt, nil
```

In `DeliverMessage`, replace the runner-error return (≈ line 169-171):

```go
	if err := e.runner.DeliverMessage(ctx, def, req.Name, req.CorrelationKey, req.Payload); err != nil {
		if errors.Is(err, engine.ErrInvalidTransition) {
			return fmt.Errorf("%w: %v", ErrConflict, err)
		}
		return fmt.Errorf("workflow-service: deliver message: %w", err)
	}
	return nil
```

Ensure `errors` is imported in `service/service.go` (add to the import block if absent). Update `service/errors.go:14`:

```go
var ErrConflict = errors.New("workflow-service: conflicting state")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./service/...`
Expected: PASS — the new case and all existing `ErrConflict` pre-flight tests stay green.

- [ ] **Step 5: Commit**

```bash
git add service/service.go service/errors.go service/errors_test.go
git commit -m "feat(service): classify engine ErrInvalidTransition as ErrConflict

Closes the race gap where a wrong-state trigger escaping the pre-flight
isTerminal/IsOpen checks surfaced as a generic 500. The runner-error paths in
DeliverSignal/DeliverMessage/deliverTaskTrigger now wrap engine wrong-state
errors as service.ErrConflict (422 / FailedPrecondition).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Transport fallback mapping for `engine.ErrInvalidTransition`

**Files:**
- Modify: `transport/rest/errors.go:30-48` (`classifyError`)
- Modify: `transport/rest/errors_test.go` (mapping-table row)
- Modify: `transport/grpc/errors.go:33-50` (`mapToGRPCStatus`)
- Modify: `transport/grpc/errors_test.go` (mapping-table row)

**Interfaces:**
- Consumes: `engine.ErrInvalidTransition` (Task 1), `service.ErrConflict` (Task 2).
- Produces: a bare `engine.ErrInvalidTransition` (no `service` wrap) maps to HTTP 422 `conflict_state` / gRPC `codes.FailedPrecondition`.

- [ ] **Step 1: Write the failing tests**

`transport/rest/errors_test.go` — add a row to the existing mapping table:

```go
{
	name:          "engine invalid transition (bare runner)",
	err:           fmt.Errorf("wrap: %w", engine.ErrInvalidTransition),
	wantStatus:    http.StatusUnprocessableEntity,
	wantErrorCode: "conflict_state",
},
```

`transport/grpc/errors_test.go` — add a row to the existing mapping table:

```go
{
	name:     "engine invalid transition (bare runner)",
	err:      fmt.Errorf("wrap: %w", engine.ErrInvalidTransition),
	wantCode: codes.FailedPrecondition,
},
```

Add the `engine` import to both test files if absent.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./transport/rest/... ./transport/grpc/...`
Expected: FAIL — bare `engine.ErrInvalidTransition` currently falls through to 500 / `codes.Internal`.

- [ ] **Step 3: Write minimal implementation**

`transport/rest/errors.go` — extend the `service.ErrConflict` case (add `engine` import):

```go
	case errors.Is(err, service.ErrConflict),
		errors.Is(err, engine.ErrInvalidTransition):
		return http.StatusUnprocessableEntity, "conflict_state"
```

`transport/grpc/errors.go` — extend the `service.ErrConflict` case (add `engine` import) and update the doc comment line:

```go
	case errors.Is(err, service.ErrConflict),
		errors.Is(err, engine.ErrInvalidTransition):
		return status.Error(codes.FailedPrecondition, err.Error())
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./transport/rest/... ./transport/grpc/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add transport/rest/errors.go transport/rest/errors_test.go transport/grpc/errors.go transport/grpc/errors_test.go
git commit -m "feat(transport): map engine.ErrInvalidTransition to 422/FailedPrecondition

Direct fallback so a transport mounted over a bare runner (no service facade)
still returns the correct wrong-state code.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: ADR-0026

**Files:**
- Create: `docs/adr/0026-engine-wrong-state-sentinel.md`

- [ ] **Step 1: Write the ADR** (Nygard template — Status/Date, Context, Decision, Consequences)

Content must cover: the wrong-state-via-`ErrTokenNotFound` status quo and its two gaps; the decision to add `ErrInvalidTransition` as a parent sentinel that `ErrTokenNotFound` wraps (so `Step` output is unchanged); the explicit scope boundary (excludes `ErrNoMatchingFlow` and `ErrUnknownTrigger`); the service-layer classification into `ErrConflict` and the transport fallback; and the `workflow-` error-prefix convention. Consequences: direct engine consumers gain typed classification; the service race-gap closes; one extra link in the error chain; the prefix sweep is a one-time mechanical cost.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0026-engine-wrong-state-sentinel.md
git commit -m "docs(adr): 0026 engine-level wrong-state sentinel

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Repo-wide `workflow-` error-prefix sweep

**Files (production code, package-prefixed error literals):** `engine/`, `runtime/`, `model/`, `service/`, `internal/persistence/postgres/`, `persistence/`, `internal/eventing/watermill/`, `eventing/`, `scheduling/`, `internal/scheduling/gocron/`, `authz/`, `casbinauthz/`, `internal/authz/casbin/`, `humantask/` — plus any string-matching `*_test.go` files in those packages. (Engine sentinels in `engine/errors.go` and `service.ErrConflict` are already done in Tasks 1–2; do **not** re-touch them.)

**Mechanical rule:** every production `errors.New("<pkg>: ...")` / `fmt.Errorf("<pkg>: ...")` whose leading segment is a **package name** gets that segment prefixed with `workflow-` (e.g. `"runtime: deliver: load: %w"` → `"workflow-runtime: deliver: load: %w"`). Do **not** prefix non-package wrapping labels (`wrap:`, `outer:`, `inner:`, `poison:`, `deliver:`, `broker:`, `x:`) — those are mid-chain wrap text or test fixtures, not package sentinels.

- [ ] **Step 1: Inventory the remaining sites per package**

Run for each package (example for `runtime`):

```bash
grep -rnE '(errors\.New|fmt\.Errorf)\("runtime: ' --include='*.go' ./runtime
```

Repeat with the package's own prefix for each package in the Files list (`postgres:` for `internal/persistence/postgres`, `model:`, `service:`, `expreval:`, `casbin:`, `casbinauthz:`, `eventing:`, `humantask:`, `authz:`, etc.). Expected magnitude (production, non-test): postgres 57, engine 41, runtime 32, model 20, service 17, casbin 9, expreval 7, casbinauthz 3, eventing 2, humantask 1, authz 1.

- [ ] **Step 2: Rewrite each package's production literals**

For each package, edit every matched literal to insert `workflow-` before the package segment. Work one package at a time. Do not touch `_test.go` fixtures yet except where a test *defines* a production-style sentinel.

- [ ] **Step 3: Find and fix string-matching tests**

Run:

```bash
grep -rnE '\.Error\(\)|ErrorContains|Contains\(' --include='*_test.go' . | grep -iE 'engine:|runtime:|model:|service:|postgres:|expreval:|casbin:|eventing:|humantask:|authz:'
```

For each hit that asserts on a changed prefix, update the expected substring to the `workflow-`-prefixed form, **or** (preferred) convert the assertion to `errors.Is` against the relevant sentinel. There are ≈22 test files referencing `.Error()`/`Contains`; only those matching a changed prefix need edits.

- [ ] **Step 4: Run the full suite**

Run: `go test -race -p 1 ./...`
Expected: PASS (no behavior change; only message text differs).

- [ ] **Step 5: Verify no stray un-prefixed package literals remain**

Run:

```bash
grep -rnE '(errors\.New|fmt\.Errorf)\("(engine|runtime|model|service|postgres|expreval|casbin|casbinauthz|eventing|humantask|authz): ' --include='*.go' . | grep -v 'workflow-'
```

Expected: no output (every package-prefixed literal now carries `workflow-`).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: adopt workflow- prefix on all error messages

Mechanical, behavior-preserving sweep of ~188 package-prefixed error literals
across engine/runtime/model/service/persistence/eventing/scheduling/authz.
Assertions use errors.Is, so message-text changes are safe.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Update HANDOVER + final gate

**Files:**
- Modify: `docs/plans/HANDOVER.md` (mark the wrong-state sentinel done; remove it from the backlog top picks; note the prefix-sweep convention)

- [ ] **Step 1: Run the full verification gate**

```bash
go test -race -p 1 -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
go list -deps ./engine ./model | grep -E 'transport|persistence|watermill|gocron|clockwork' || echo "PURE"
```

Expected: all tests green; touched packages ≥ 85%; lint 0 issues; `PURE`.

- [ ] **Step 2: Update HANDOVER.md**

Add an "Engine wrong-state sentinel sub-project — ✅ COMPLETE" section (mirroring the existing track sections): branch, ADR-0026, what shipped (sentinel + service classification + transport fallback + `workflow-` sweep), the gate numbers, and deferred follow-ups (engine terminal-instance guard intentionally out of scope; `ErrNoMatchingFlow`/`ErrUnknownTrigger` deliberately excluded). Remove "engine wrong-state sentinel" from the START-HERE top picks; promote the next pick (timer rehydration).

- [ ] **Step 3: Commit**

```bash
git add docs/plans/HANDOVER.md
git commit -m "docs: mark engine wrong-state sentinel complete in HANDOVER

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- §1 core sentinel + wrapping → Task 1. ✅
- §2 file placement (`engine/errors.go` + test) → Task 1. ✅
- §3 service classification → Task 2. ✅
- §4 transport fallback → Task 3. ✅
- §5 `workflow-` sweep → Tasks 1–2 (touched sentinels) + Task 5 (repo-wide). ✅
- §6 ADR-0026 → Task 4. ✅
- Testing strategy (engine wrapping graph + behavioral; service race; transport rows) → Tasks 1–3. ✅
- Verification gate → Task 6. ✅

**Placeholder scan:** Task 2's test names the harness choice as implementer judgment (stub runner vs real-engine race) because the existing `service/errors_test.go` construction is not reproduced here; both forms carry the exact identical assertions, so this is a bounded, explicit choice, not a TODO. Task 5 cannot enumerate 188 identical mechanical edits; it gives the exact grep/edit/verify procedure and guardrails instead. All code steps that introduce new symbols show complete code.

**Type consistency:** `engine.ErrInvalidTransition` is the single name used in Tasks 1–3. `service.ErrConflict` reused unchanged (message text only). Transport cases extend existing `errors.Is` switches. No signature drift.
