# Resilience (Retry / Backoff / DLQ / Idempotency) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement opt-in action retry with backoff/jitter, catch-flow→incident exhaustion handling, outbox-relay poison isolation/DLQ, and idempotency (stable action key + consumer dedup) for the `wrkflw` engine.

**Architecture:** A retry is modeled as an ordinary engine timer (ADR-0015): the runtime samples jitter and records it on `ActionFailed.JitterFraction`; the pure `Step` computes the (deterministic) backoff and emits `ScheduleTimer{TimerRetry}`; the existing `Scheduler` fires it; `TimerFired{TimerRetry}` re-invokes the action. On exhaustion the engine routes a catch-flow, else an error boundary, else raises an admin-resumable `Incident` (ADR-0016). The Postgres relay gains per-row isolation + a `dead` quarantine (ADR-0017). Idempotency is a stable `_idempotencyKey` on action input + a `wrkflw_processed_message` dedup table (ADR-0018).

**Tech Stack:** Go 1.25; `model`/`engine`/`runtime` pure-ish packages; `internal/persistence/postgres` (pgx + goose, testcontainers via `database.RunTestDatabase`); `math/rand/v2` for jitter (runtime only).

## Global Constraints

- **Module path:** `github.com/kartaladev/wrkflw` — import `.../model`, `.../engine`, `.../runtime`, `.../persistence`.
- **Pure core:** `engine` and `model` import only stdlib (+ `model`/`authz`/`humantask`/`expreval`). **No clock, no randomness, no transport/storage/bus/time-vendor** in `engine`/`model`. Jitter RNG and `time.Now` live ONLY in `runtime`/`internal/*`.
- **`Step` determinism:** identical `(state, trigger)` ⇒ identical `(state, commands)`. All IDs from `InstanceState` counters; jitter enters via `ActionFailed.JitterFraction` (recorded on the trigger), never sampled in `Step`.
- **`Step` purity:** never mutate input `InstanceState`; **extend `cloneState` for every new state field** (`Incidents`, `Token.RetryAttempts`, `Token.RetryStartedAt`).
- **`Step` signature is stable:** `Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)`.
- **Retry is opt-in:** absent an effective `RetryPolicy` (no `Node.RetryPolicy` and nil `StepOptions.DefaultRetryPolicy`), `ActionFailed` behaves **exactly as today** (`propagateError`). No existing engine test may change behaviour.
- **Tests:** black-box (`package <pkg>_test`); table-driven with an **`assert` closure per case** (project `table-test` skill, NOT `want`/`wantErr`); `t.Context()`; pair each `foo.go` with `foo_test.go`. Postgres tests use `database.RunTestDatabase(t)` (testcontainers `postgres:17`).
- **Lint:** `golangci-lint` v2 (`.golangci.yml`). Run `golangci-lint run ./...` clean before each commit.
- **Gate per touched package:** `go test -race ./<pkg>/...` green; ≥85% line coverage; lint clean.
- **Commits:** Conventional Commits scoped to the area; trailer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **Trust the code, not this plan's listings:** the engine has grown; ground every edit against the then-current source and observe the RED state. Test code here is authoritative for intent; implementation snippets are directional.

---

## File Structure

| File | Responsibility |
|---|---|
| `model/retry.go` (create) | `RetryPolicy` value type + `Backoff`/`IsNonRetryable`/`Normalize`/`DefaultRetryPolicy`. |
| `model/retry_test.go` (create) | Tests for the above. |
| `model/definition.go` (modify) | `Node.RetryPolicy *RetryPolicy`, `Node.RecoveryFlow string`. |
| `model/validate.go` (modify) | `ErrInvalidRetryPolicy`, `ErrInvalidRecoveryFlow` rules. (Find the actual validate file.) |
| `engine/trigger.go` (modify) | `ActionFailed.JitterFraction`; `NewActionFailedJittered`; `ResolveIncident` trigger. |
| `engine/command.go` (modify) | `TimerRetry` TimerKind + `String()`. |
| `engine/state.go` (modify) | `Token.RetryAttempts`/`RetryStartedAt`; `Incident`; `InstanceState.Incidents`/`IncidentSeq`; `TokenIncident`; `cloneState` extension. |
| `engine/step.go` (modify) | retry scheduling, `TimerRetry` re-invoke, exhaustion (catch/boundary/incident), `ResolveIncident`, `_idempotencyKey` stamping; `StepOptions.DefaultRetryPolicy`. |
| `engine/*_test.go` (modify/create) | retry/exhaustion/incident/determinism/clone tests. |
| `runtime/runner.go` (modify) | sample jitter in `perform`; `WithDefaultRetryPolicy`; `WithJitterSource`; `ResolveIncident`. |
| `runtime/jitter.go` (create) | `JitterSource` port + default `math/rand/v2` impl. |
| `runtime/ports.go` (modify) | `InstanceSummary.IncidentCount`. |
| `internal/persistence/postgres/trigger_codec.go` (modify) | encode/decode `JitterFraction` + `ResolveIncident`. |
| `internal/persistence/postgres/migrations/0002_resilience.sql` (create) | outbox DLQ columns + `wrkflw_processed_message`. |
| `internal/persistence/postgres/relay.go` (modify) | per-row isolation, backoff, `dead` quarantine, `ListDeadLettered`, `Redrive`. |
| `internal/persistence/postgres/dedup.go` (create) | `Deduper` Postgres impl. |
| `persistence/*.go` (modify/create) | façade for DLQ admin + `NewDeduper`. |
| `service/service.go` + `transport/*` (modify) | `ResolveIncident` pass-through + `IncidentCount` surfacing (thin). |

---

# Phase A — Pure model & engine retry core

### Task 1: `model.RetryPolicy` value type

**Files:**
- Create: `model/retry.go`
- Test: `model/retry_test.go`

**Interfaces:**
- Produces: `model.RetryPolicy{MaxAttempts int; InitialInterval time.Duration; BackoffCoef float64; MaxInterval time.Duration; MaxElapsed time.Duration; NonRetryableErrors []string}`; `model.DefaultRetryPolicy() RetryPolicy`; `(RetryPolicy).Backoff(attempt int) time.Duration`; `(RetryPolicy).IsNonRetryable(errMsg string) bool`; `(RetryPolicy).Normalize() RetryPolicy`.

- [ ] **Step 1: Write the failing test** (`model/retry_test.go`, `package model_test`)

```go
package model_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/model"
)

func TestDefaultRetryPolicy(t *testing.T) {
	p := model.DefaultRetryPolicy()
	if p.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", p.MaxAttempts)
	}
	if p.InitialInterval != time.Second {
		t.Fatalf("InitialInterval = %v, want 1s", p.InitialInterval)
	}
	if p.BackoffCoef != 2.0 {
		t.Fatalf("BackoffCoef = %v, want 2.0", p.BackoffCoef)
	}
	if p.MaxInterval != 100*time.Second {
		t.Fatalf("MaxInterval = %v, want 100s", p.MaxInterval)
	}
}

func TestRetryPolicyBackoff(t *testing.T) {
	p := model.RetryPolicy{InitialInterval: time.Second, BackoffCoef: 2.0, MaxInterval: 10 * time.Second}
	cases := []struct {
		name    string
		attempt int
		assert  func(t *testing.T, d time.Duration)
	}{
		{"attempt0", 0, func(t *testing.T, d time.Duration) {
			if d != time.Second {
				t.Fatalf("got %v, want 1s", d)
			}
		}},
		{"attempt1", 1, func(t *testing.T, d time.Duration) {
			if d != 2*time.Second {
				t.Fatalf("got %v, want 2s", d)
			}
		}},
		{"attempt2", 2, func(t *testing.T, d time.Duration) {
			if d != 4*time.Second {
				t.Fatalf("got %v, want 4s", d)
			}
		}},
		{"capped", 10, func(t *testing.T, d time.Duration) {
			if d != 10*time.Second {
				t.Fatalf("got %v, want capped 10s", d)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, p.Backoff(tc.attempt))
		})
	}
}

func TestRetryPolicyIsNonRetryable(t *testing.T) {
	p := model.RetryPolicy{NonRetryableErrors: []string{"validation", "not found"}}
	if !p.IsNonRetryable("input validation failed") {
		t.Fatal("expected substring match to be non-retryable")
	}
	if p.IsNonRetryable("timeout") {
		t.Fatal("unexpected non-retryable")
	}
}

func TestRetryPolicyNormalizeFillsZeros(t *testing.T) {
	got := model.RetryPolicy{MaxAttempts: 5}.Normalize()
	if got.MaxAttempts != 5 {
		t.Fatalf("MaxAttempts overwritten: %d", got.MaxAttempts)
	}
	if got.InitialInterval != time.Second || got.BackoffCoef != 2.0 {
		t.Fatalf("zero fields not filled from default: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./model/... -run TestRetryPolicy -v` → FAIL (`undefined: model.DefaultRetryPolicy`).

- [ ] **Step 3: Write minimal implementation** (`model/retry.go`)

```go
package model

import (
	"math"
	"strings"
	"time"
)

// RetryPolicy describes how a failed ServiceAction is retried. The zero value is
// not usable; use DefaultRetryPolicy or Normalize.
type RetryPolicy struct {
	MaxAttempts        int           // total attempts incl. first; default 3. 0 = unlimited.
	InitialInterval    time.Duration // wait before first retry; default 1s.
	BackoffCoef        float64       // per-attempt multiplier; default 2.0.
	MaxInterval        time.Duration // per-attempt cap; default 100×InitialInterval.
	MaxElapsed         time.Duration // total budget across attempts; 0 = no cap.
	NonRetryableErrors []string      // error substrings that abort retries.
}

// DefaultRetryPolicy returns Temporal-style defaults with a finite attempt cap.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: time.Second,
		BackoffCoef:     2.0,
		MaxInterval:     100 * time.Second,
	}
}

// Backoff returns the capped, un-jittered delay before the zero-based attempt.
func (p RetryPolicy) Backoff(attempt int) time.Duration {
	if p.InitialInterval <= 0 {
		return 0
	}
	d := float64(p.InitialInterval) * math.Pow(p.BackoffCoef, float64(attempt))
	if p.MaxInterval > 0 && d > float64(p.MaxInterval) {
		return p.MaxInterval
	}
	return time.Duration(d)
}

// IsNonRetryable reports whether errMsg contains any NonRetryableErrors substring.
func (p RetryPolicy) IsNonRetryable(errMsg string) bool {
	for _, s := range p.NonRetryableErrors {
		if s != "" && strings.Contains(errMsg, s) {
			return true
		}
	}
	return false
}

// Normalize fills zero-valued fields from DefaultRetryPolicy. MaxAttempts==0
// (unlimited) is preserved; only a negative value is treated as unset.
func (p RetryPolicy) Normalize() RetryPolicy {
	d := DefaultRetryPolicy()
	if p.MaxAttempts < 0 {
		p.MaxAttempts = d.MaxAttempts
	}
	if p.InitialInterval <= 0 {
		p.InitialInterval = d.InitialInterval
	}
	if p.BackoffCoef < 1.0 {
		p.BackoffCoef = d.BackoffCoef
	}
	if p.MaxInterval <= 0 {
		p.MaxInterval = d.MaxInterval
	}
	return p
}
```

- [ ] **Step 4: Run test to verify it passes** — `go test ./model/... -run TestRetryPolicy -v` → PASS.

- [ ] **Step 5: Commit** — `git add model/retry.go model/retry_test.go && git commit -m "feat(model): add RetryPolicy value type with backoff and normalize"` (+ trailer).

---

### Task 2: `model.Node` retry fields + `Validate`

**Files:**
- Modify: `model/definition.go` (add `Node.RetryPolicy`, `Node.RecoveryFlow`)
- Modify: the validation file (find it: `grep -rl "func Validate\|func.*Validate" model/`)
- Test: the existing model validate test file (same-named) + `model/definition_test.go` if present.

**Interfaces:**
- Consumes: `model.RetryPolicy` (Task 1).
- Produces: `Node.RetryPolicy *RetryPolicy`; `Node.RecoveryFlow string`; sentinels `model.ErrInvalidRetryPolicy`, `model.ErrInvalidRecoveryFlow`.

- [ ] **Step 1: Write the failing test** (add to the model validate test file, `package model_test`)

```go
func TestValidateRejectsBadRetryPolicy(t *testing.T) {
	bad := -1.0 // BackoffCoef below 1.0 with a positive interval is invalid
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "task", Kind: model.KindServiceTask, Action: "a",
				RetryPolicy: &model.RetryPolicy{InitialInterval: time.Second, BackoffCoef: bad}},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
	if err := def.Validate(); !errors.Is(err, model.ErrInvalidRetryPolicy) {
		t.Fatalf("got %v, want ErrInvalidRetryPolicy", err)
	}
}

func TestValidateRejectsRecoveryFlowNotFromNode(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "task", Kind: model.KindServiceTask, Action: "a", RecoveryFlow: "nope"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
	if err := def.Validate(); !errors.Is(err, model.ErrInvalidRecoveryFlow) {
		t.Fatalf("got %v, want ErrInvalidRecoveryFlow", err)
	}
}
```

(Adjust `Validate` call form — it may be `model.Validate(def)` rather than `def.Validate()`; check the existing tests and match them.)

- [ ] **Step 2: Run to verify it fails** — `go test ./model/... -run 'TestValidateRejects(BadRetryPolicy|RecoveryFlow)' -v` → FAIL (undefined sentinels / no rule).

- [ ] **Step 3: Implement** — add fields to `Node`:

```go
	// RetryPolicy is the optional per-node retry policy (nil ⇒ runtime default).
	RetryPolicy *RetryPolicy
	// RecoveryFlow is the sequence-flow ID taken when retries are exhausted
	// (Step-Functions "Catch"). Mirrors SLAFlow. Empty ⇒ no catch-flow.
	RecoveryFlow string
```

Add sentinels next to the existing `Validate` sentinels:

```go
var (
	ErrInvalidRetryPolicy  = errors.New("model: invalid retry policy")
	ErrInvalidRecoveryFlow = errors.New("model: invalid recovery flow")
)
```

In the per-node validation loop (mirror how `SLAFlow` / sentinels are checked), add:

```go
if n.RetryPolicy != nil {
	p := *n.RetryPolicy
	if p.MaxAttempts < 0 || p.InitialInterval < 0 || p.MaxInterval < 0 ||
		(p.InitialInterval > 0 && p.BackoffCoef < 1.0) {
		return fmt.Errorf("%w: node %q", ErrInvalidRetryPolicy, n.ID)
	}
}
if n.RecoveryFlow != "" {
	ok := false
	for _, f := range def.Flows { // use the same flow lookup the file already uses
		if f.ID == n.RecoveryFlow && f.Source == n.ID {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("%w: node %q flow %q", ErrInvalidRecoveryFlow, n.ID, n.RecoveryFlow)
	}
}
```

Ensure the recursion into `Subprocess` definitions covers these too (follow the existing recursive pattern).

- [ ] **Step 4: Run to verify it passes** — `go test ./model/... -v` → PASS (all, incl. existing).

- [ ] **Step 5: Commit** — `git commit -m "feat(model): validate Node RetryPolicy and RecoveryFlow"`.

---

### Task 3: engine sealed-set additions (`ActionFailed.JitterFraction`, `ResolveIncident`, `TimerRetry`)

**Files:**
- Modify: `engine/trigger.go`, `engine/command.go`
- Test: `engine/trigger_test.go`, `engine/command_test.go`

**Interfaces:**
- Produces: `ActionFailed.JitterFraction float64`; `engine.NewActionFailedJittered(at time.Time, commandID, errMsg string, retryable bool, jitter float64) ActionFailed`; `engine.ResolveIncident{IncidentID string; AddAttempts int}` (a `Trigger`) + `engine.NewResolveIncident(at time.Time, incidentID string, addAttempts int) ResolveIncident`; `engine.TimerRetry TimerKind`.

- [ ] **Step 1: Write failing tests** (add to `engine/trigger_test.go` and `engine/command_test.go`)

```go
// engine/trigger_test.go
func TestNewActionFailedJitteredCarriesFraction(t *testing.T) {
	at := time.Unix(0, 0)
	f := engine.NewActionFailedJittered(at, "c-1", "boom", true, 0.5)
	if f.JitterFraction != 0.5 || !f.Retryable || f.CommandID != "c-1" {
		t.Fatalf("bad ActionFailed: %+v", f)
	}
	var _ engine.Trigger = f
}

func TestResolveIncidentIsTrigger(t *testing.T) {
	at := time.Unix(0, 0)
	r := engine.NewResolveIncident(at, "p-in0", 2)
	if r.IncidentID != "p-in0" || r.AddAttempts != 2 {
		t.Fatalf("bad ResolveIncident: %+v", r)
	}
	var _ engine.Trigger = r
	if !r.OccurredAt().Equal(at) {
		t.Fatal("OccurredAt mismatch")
	}
}

// engine/command_test.go — extend the existing TimerKind distinctness/String tests
func TestTimerRetryDistinctAndStringable(t *testing.T) {
	if engine.TimerRetry == engine.TimerIntermediate || engine.TimerRetry == engine.TimerSLA || engine.TimerRetry == engine.TimerInWait {
		t.Fatal("TimerRetry not distinct")
	}
	if engine.TimerRetry.String() == "" || engine.TimerRetry.String() == "TimerKind(unknown)" {
		t.Fatalf("TimerRetry.String() = %q", engine.TimerRetry.String())
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run 'TestNewActionFailedJittered|TestResolveIncident|TestTimerRetry' -v` → FAIL.

- [ ] **Step 3: Implement** —
  - In `engine/trigger.go`: add `JitterFraction float64` to `ActionFailed`; add `NewActionFailedJittered` (and keep `NewActionFailed` delegating with `jitter=0`). Add `ResolveIncident` struct embedding `baseTrigger` with `IncidentID string; AddAttempts int` + `NewResolveIncident`.
  - In `engine/command.go`: add `TimerRetry` after `TimerInWait` in the const block and a `case TimerRetry: return "TimerRetry"` in `String()`.

```go
// trigger.go
type ActionFailed struct {
	baseTrigger
	CommandID      string
	Err            string
	Retryable      bool
	JitterFraction float64 // [0,1); sampled by the runtime, applied to the backoff in Step.
}

func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool) ActionFailed {
	return NewActionFailedJittered(at, commandID, errMsg, retryable, 0)
}

func NewActionFailedJittered(at time.Time, commandID, errMsg string, retryable bool, jitter float64) ActionFailed {
	return ActionFailed{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Err: errMsg, Retryable: retryable, JitterFraction: jitter}
}

// ResolveIncident asks the engine to clear an incident, grant AddAttempts more
// retries, and re-invoke the parked action.
type ResolveIncident struct {
	baseTrigger
	IncidentID  string
	AddAttempts int
}

func NewResolveIncident(at time.Time, incidentID string, addAttempts int) ResolveIncident {
	return ResolveIncident{baseTrigger: baseTrigger{at: at}, IncidentID: incidentID, AddAttempts: addAttempts}
}
```

- [ ] **Step 4: Run to verify it passes** — `go test ./engine/... -run 'TestNewActionFailedJittered|TestResolveIncident|TestTimerRetry' -v` → PASS. Also `go test ./engine/...` → PASS (no regressions).

- [ ] **Step 5: Commit** — `git commit -m "feat(engine): add ActionFailed.JitterFraction, ResolveIncident trigger, TimerRetry kind"`.

---

### Task 4: engine state additions + `cloneState`

**Files:**
- Modify: `engine/state.go`
- Test: `engine/state_test.go` (the cloneState/clone test lives here or in `engine/step_test.go` — find it: `grep -rln "cloneState\|func TestClone" engine/`)

**Interfaces:**
- Produces: `Token.RetryAttempts int`, `Token.RetryStartedAt time.Time`; `engine.Incident{ID, TokenID, NodeID, ScopeID, CommandID, Error string; Attempts int; CreatedAt time.Time}`; `InstanceState.Incidents []Incident`, `InstanceState.IncidentSeq int`; `engine.TokenIncident TokenState`; `StepOptions.DefaultRetryPolicy *model.RetryPolicy`.

- [ ] **Step 1: Write failing test** — extend the clone test to assert `Incidents` is deep-copied and the new token fields survive cloning. (Find the existing clone test name; it asserts no aliasing.)

```go
func TestCloneStateDeepCopiesIncidents(t *testing.T) {
	st := engine.InstanceState{
		Incidents: []engine.Incident{{ID: "p-in0", TokenID: "p-t1", NodeID: "task", Error: "boom", Attempts: 3}},
		Tokens:    []engine.Token{{ID: "p-t1", NodeID: "task", State: engine.TokenIncident, RetryAttempts: 3}},
	}
	clone := st.Clone() // or engine.cloneState via the exported Clone wrapper used by existing tests
	clone.Incidents[0].Error = "mutated"
	clone.Tokens[0].RetryAttempts = 99
	if st.Incidents[0].Error != "boom" {
		t.Fatal("Incidents aliased — not deep-copied")
	}
	if st.Tokens[0].RetryAttempts != 3 {
		t.Fatal("token RetryAttempts aliased")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run TestCloneStateDeepCopiesIncidents -v` → FAIL (undefined fields).

- [ ] **Step 3: Implement** —
  - Add `RetryAttempts int` and `RetryStartedAt time.Time` to `Token`.
  - Add `TokenIncident` to the `TokenState` const block (after `TokenAtJoin`).
  - Add the `Incident` struct.
  - Add `Incidents []Incident` and `IncidentSeq int` to `InstanceState`.
  - Add `DefaultRetryPolicy *model.RetryPolicy` to `StepOptions` (find `StepOptions` — likely in `step.go`).
  - In `cloneState`, append-copy `Incidents` (value structs, no nested maps):

```go
	if len(st.Incidents) > 0 {
		out.Incidents = append([]Incident(nil), st.Incidents...)
	}
```

  `Token.RetryAttempts`/`RetryStartedAt` are scalars copied by the existing per-token element copy — verify the token copy loop copies the whole struct (it does via `append`), so no extra code is needed; the test proves it.

- [ ] **Step 4: Run to verify it passes** — `go test ./engine/... -run TestCloneState -v` → PASS; `go test ./engine/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(engine): add retry/incident state fields and cloneState coverage"`.

---

### Task 5: engine `Step` — schedule a retry on retryable failure

**Files:**
- Modify: `engine/step.go` (the `ActionFailed` case + a helper `effectiveRetryPolicy(def, node, opt)`)
- Test: `engine/retry_test.go` (create)

**Interfaces:**
- Consumes: Tasks 1–4 types; existing `Step`, `ScheduleTimer`, `findTokenByCommand` helpers (find the real helper name).
- Produces: behaviour — a retryable `ActionFailed` with budget remaining and an effective policy emits exactly one `ScheduleTimer{Kind: TimerRetry, FireAt: OccurredAt + jitter×backoff}` and increments the token's `RetryAttempts`.

- [ ] **Step 1: Write failing test** (`engine/retry_test.go`, `package engine_test`)

```go
package engine_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/model"
)

// helper: a one-service-task definition with a node-level retry policy.
func retryDef(t *testing.T, p *model.RetryPolicy) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "task", Kind: model.KindServiceTask, Action: "a", RetryPolicy: p},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

func TestStepSchedulesRetryWithJitteredBackoff(t *testing.T) {
	def := retryDef(t, &model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2.0, MaxInterval: time.Minute})
	// Drive start → the engine emits InvokeAction for "task". Capture its CommandID.
	start := engine.NewStartInstance(time.Unix(0, 0), nil)
	r1, err := engine.Step(def, engine.NewInstanceState("p", "p", 1, time.Unix(0, 0)), start, engine.StepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cmdID := firstInvokeActionCommandID(t, r1.Commands) // small helper that scans r1.Commands
	// Now the action fails, retryable, with jitter 0.5 at t=10s.
	failAt := time.Unix(10, 0)
	fail := engine.NewActionFailedJittered(failAt, cmdID, "boom", true, 0.5)
	r2, err := engine.Step(def, r1.State, fail, engine.StepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st := firstScheduleTimer(t, r2.Commands) // helper returning the engine.ScheduleTimer
	if st.Kind != engine.TimerRetry {
		t.Fatalf("Kind = %v, want TimerRetry", st.Kind)
	}
	// attempt 0 backoff = 1s; jitter 0.5 ⇒ 500ms; FireAt = 10s + 500ms.
	wantFire := failAt.Add(500 * time.Millisecond)
	if !st.FireAt.Equal(wantFire) {
		t.Fatalf("FireAt = %v, want %v", st.FireAt, wantFire)
	}
	// token RetryAttempts incremented to 1.
	if got := tokenByNode(t, r2.State, "task").RetryAttempts; got != 1 {
		t.Fatalf("RetryAttempts = %d, want 1", got)
	}
}

func TestStepNoPolicyKeepsLegacyBehaviour(t *testing.T) {
	def := retryDef(t, nil) // no node policy
	start := engine.NewStartInstance(time.Unix(0, 0), nil)
	r1, _ := engine.Step(def, engine.NewInstanceState("p", "p", 1, time.Unix(0, 0)), start, engine.StepOptions{})
	cmdID := firstInvokeActionCommandID(t, r1.Commands)
	fail := engine.NewActionFailed(time.Unix(1, 0), cmdID, "boom", true)
	r2, err := engine.Step(def, r1.State, fail, engine.StepOptions{}) // nil DefaultRetryPolicy
	if err != nil {
		t.Fatal(err)
	}
	if hasScheduleTimerOfKind(r2.Commands, engine.TimerRetry) {
		t.Fatal("no-policy path must not schedule a retry (legacy propagateError expected)")
	}
}
```

(The small command-scanning helpers `firstInvokeActionCommandID`, `firstScheduleTimer`, `tokenByNode`, `hasScheduleTimerOfKind`, `firstScheduleTimer` go in a shared `engine/helpers_test.go` if not already present — check existing tests; many such scanners likely already exist, reuse them and DROP duplicates. `engine.NewInstanceState` may have a different constructor — match the existing test setup.)

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run TestStepSchedulesRetry -v` → FAIL (retry not scheduled).

- [ ] **Step 3: Implement** — in `step.go`, add the helper and intercept the `ActionFailed` case **before** `propagateError`:

```go
func effectiveRetryPolicy(node model.Node, opt StepOptions) (model.RetryPolicy, bool) {
	switch {
	case node.RetryPolicy != nil:
		return node.RetryPolicy.Normalize(), true
	case opt.DefaultRetryPolicy != nil:
		return opt.DefaultRetryPolicy.Normalize(), true
	default:
		return model.RetryPolicy{}, false
	}
}
```

In the `ActionFailed` handler (after locating the token + its node), insert before the existing boundary-cancel/propagateError logic:

```go
eff, hasPolicy := effectiveRetryPolicy(node, opt)
if hasPolicy {
	attempt := tok.RetryAttempts
	terminal := !v.Retryable || eff.IsNonRetryable(v.Err) ||
		(eff.MaxAttempts != 0 && attempt+1 >= eff.MaxAttempts) ||
		(eff.MaxElapsed > 0 && !tok.RetryStartedAt.IsZero() && v.OccurredAt().Sub(tok.RetryStartedAt) > eff.MaxElapsed)
	if !terminal {
		delay := time.Duration(v.JitterFraction * float64(eff.Backoff(attempt)))
		timerID := next.timerID() // use the existing TimerSeq id generator
		cmds = append(cmds, ScheduleTimer{TimerID: timerID, Token: tok.ID, FireAt: v.OccurredAt().Add(delay), Kind: TimerRetry})
		// record the timer + park the token waiting on it (mirror timer-intermediate parking)
		next.addTimer(timerID, tok.ID, TimerRetry /*, fireAt if the record stores it */)
		tokRef.RetryAttempts++
		if tokRef.RetryStartedAt.IsZero() {
			tokRef.RetryStartedAt = v.OccurredAt()
		}
		tokRef.State = TokenWaitingCommand
		tokRef.AwaitCommand = timerID // park on the retry timer id
		return StepResult{State: next, Commands: cmds}, nil
	}
	// terminal → exhaustion path (Task 7). For THIS task, fall through to legacy
	// propagateError so the test for scheduling passes; Task 7 replaces this.
}
// ... existing boundary-cancel + propagateError ...
```

(Names like `next`, `next.timerID()`, `addTimer`, `tokRef` are placeholders — use the real mutation idioms in `step.go`. The engine clones state once at the top of `Step`; mutate the clone. Match how the timer-intermediate case schedules + parks.)

- [ ] **Step 4: Run to verify it passes** — `go test ./engine/... -run TestStep -v` → PASS; `go test ./engine/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(engine): schedule jittered retry timer on retryable action failure"`.

---

### Task 6: engine `Step` — re-invoke on `TimerFired{TimerRetry}`

**Files:**
- Modify: `engine/step.go` (the `TimerFired` case)
- Test: `engine/retry_test.go`

**Interfaces:**
- Produces: a `TimerFired` whose timer record `Kind == TimerRetry` re-emits an `InvokeAction` for the parked token's node and parks the token on the new command id.

- [ ] **Step 1: Write failing test**

```go
func TestStepRetryTimerReinvokesAction(t *testing.T) {
	def := retryDef(t, &model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2.0, MaxInterval: time.Minute})
	r1, _ := engine.Step(def, engine.NewInstanceState("p", "p", 1, time.Unix(0, 0)), engine.NewStartInstance(time.Unix(0, 0), nil), engine.StepOptions{})
	cmdID := firstInvokeActionCommandID(t, r1.Commands)
	r2, _ := engine.Step(def, r1.State, engine.NewActionFailedJittered(time.Unix(10, 0), cmdID, "boom", true, 0.5), engine.StepOptions{})
	timerID := firstScheduleTimer(t, r2.Commands).TimerID
	// Fire the retry timer.
	r3, err := engine.Step(def, r2.State, engine.NewTimerFired(time.Unix(11, 0), timerID), engine.StepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasInvokeActionForNode(t, r3, def, "task") { // a fresh InvokeAction for "task"
		t.Fatal("expected re-invocation of the action after retry timer fired")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run TestStepRetryTimerReinvokes -v` → FAIL.

- [ ] **Step 3: Implement** — in the `TimerFired` case, branch on the consumed timer record's `Kind`:

```go
case TimerRetry:
	tok := /* token by record.Token */
	cmdID := next.commandID()
	in := actionInput(def, node, next) // re-evaluate the node's action input; stamp _idempotencyKey (Task 9)
	cmds = append(cmds, InvokeAction{CommandID: cmdID, Name: node.Action, Input: in})
	tokRef.State = TokenWaitingCommand
	tokRef.AwaitCommand = cmdID
	// remove the consumed timer record (same as other timer kinds)
	return StepResult{State: next, Commands: cmds}, nil
```

(Match the existing `TimerIntermediate`/`TimerSLA` dispatch structure in the `TimerFired` handler.)

- [ ] **Step 4: Run to verify it passes** — `go test ./engine/... -run TestStepRetry -v` → PASS; `go test ./engine/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(engine): re-invoke action when retry timer fires"`.

---

### Task 7: engine `Step` — exhaustion (catch-flow → boundary → incident)

**Files:**
- Modify: `engine/step.go` (replace the Task-5 "fall through to legacy" with the exhaustion precedence)
- Test: `engine/retry_test.go`

**Interfaces:**
- Produces: on terminal failure — (a) route down `Node.RecoveryFlow` injecting `_error`/`_errorMessage`/`_errorAttempts`; else (b) existing `propagateError`; else (c) append `Incident`, set token `TokenIncident`, instance stays `StatusRunning`.

- [ ] **Step 1: Write failing tests**

```go
func TestStepExhaustionRaisesIncident(t *testing.T) {
	def := retryDef(t, &model.RetryPolicy{MaxAttempts: 1}) // 1 attempt ⇒ first failure is terminal
	r1, _ := engine.Step(def, engine.NewInstanceState("p", "p", 1, time.Unix(0, 0)), engine.NewStartInstance(time.Unix(0, 0), nil), engine.StepOptions{})
	cmdID := firstInvokeActionCommandID(t, r1.Commands)
	r2, err := engine.Step(def, r1.State, engine.NewActionFailed(time.Unix(1, 0), cmdID, "boom", true), engine.StepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r2.State.Status != engine.StatusRunning {
		t.Fatalf("instance status = %v, want StatusRunning (incident, not failed)", r2.State.Status)
	}
	if len(r2.State.Incidents) != 1 {
		t.Fatalf("incidents = %d, want 1", len(r2.State.Incidents))
	}
	inc := r2.State.Incidents[0]
	if inc.NodeID != "task" || inc.Error != "boom" || inc.Attempts != 1 {
		t.Fatalf("bad incident: %+v", inc)
	}
	if tokenByNode(t, r2.State, "task").State != engine.TokenIncident {
		t.Fatal("token not parked in TokenIncident")
	}
}

func TestStepExhaustionTakesRecoveryFlow(t *testing.T) {
	// task has RecoveryFlow "rf" → "recover" node.
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "task", Kind: model.KindServiceTask, Action: "a", RecoveryFlow: "rf",
				RetryPolicy: &model.RetryPolicy{MaxAttempts: 1}},
			{ID: "recover", Kind: model.KindServiceTask, Action: "compensate"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
			{ID: "rf", Source: "task", Target: "recover"},
		},
	}
	r1, _ := engine.Step(def, engine.NewInstanceState("p", "p", 1, time.Unix(0, 0)), engine.NewStartInstance(time.Unix(0, 0), nil), engine.StepOptions{})
	cmdID := firstInvokeActionCommandID(t, r1.Commands)
	r2, err := engine.Step(def, r1.State, engine.NewActionFailed(time.Unix(1, 0), cmdID, "boom", true), engine.StepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.State.Incidents) != 0 {
		t.Fatal("recovery flow should pre-empt incident")
	}
	if !hasInvokeActionForNode(t, r2, def, "recover") {
		t.Fatal("expected token routed to recover node")
	}
	if r2.State.Variables["_errorMessage"] != "boom" {
		t.Fatalf("_errorMessage = %v, want boom", r2.State.Variables["_errorMessage"])
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run TestStepExhaustion -v` → FAIL.

- [ ] **Step 3: Implement** — replace the Task-5 terminal fall-through with:

```go
// terminal exhaustion
if node.RecoveryFlow != "" {
	next.Variables = setVar(next.Variables, "_errorMessage", v.Err)
	next.Variables = setVar(next.Variables, "_errorAttempts", tok.RetryAttempts)
	if node.ErrorCode != "" {
		next.Variables = setVar(next.Variables, "_error", node.ErrorCode)
	}
	tokRef.RetryAttempts = 0
	tokRef.RetryStartedAt = time.Time{}
	// route token down RecoveryFlow (drive from flow target) — reuse the normal
	// flow-following routine the engine uses for an outgoing flow by ID.
	return driveFlow(def, next, tokRef, node.RecoveryFlow, opt)
}
// else: try existing error-boundary propagation
if handled, res, err := tryPropagateError(def, next, tok, v, opt); handled {
	return res, err
}
// else: raise an incident
incID := next.incidentID() // "<instanceID>-in<IncidentSeq++>"
next.Incidents = append(next.Incidents, Incident{
	ID: incID, TokenID: tok.ID, NodeID: node.ID, ScopeID: tok.ScopeID,
	CommandID: v.CommandID, Error: v.Err, Attempts: tok.RetryAttempts, CreatedAt: v.OccurredAt(),
})
tokRef.State = TokenIncident
return StepResult{State: next, Commands: cmds}, nil
```

(`setVar`, `driveFlow`, `incidentID`, and the propagate-error refactor `tryPropagateError` are directional — adapt to the real helpers. `propagateError` today probably both detects a boundary AND falls back to `StatusFailed`; split it so the fallback becomes "incident" only when `hasPolicy`. Keep the **no-policy** path calling the original `propagateError` verbatim.)

- [ ] **Step 4: Run to verify it passes** — `go test ./engine/... -run TestStep -v` → PASS; `go test ./engine/...` → PASS (legacy failure tests still green).

- [ ] **Step 5: Commit** — `git commit -m "feat(engine): exhaustion precedence catch-flow then boundary then incident"`.

---

### Task 8: engine `Step` — `ResolveIncident`

**Files:**
- Modify: `engine/step.go` (new `ResolveIncident` case in the trigger switch)
- Test: `engine/retry_test.go`

**Interfaces:**
- Produces: a `ResolveIncident{IncidentID, AddAttempts}` removes the matching incident, lowers the token's `RetryAttempts` by up to `AddAttempts` (granting budget), moves it out of `TokenIncident`, and re-emits `InvokeAction`. Unknown id ⇒ no-op.

- [ ] **Step 1: Write failing test**

```go
func TestStepResolveIncidentReinvokes(t *testing.T) {
	def := retryDef(t, &model.RetryPolicy{MaxAttempts: 1})
	r1, _ := engine.Step(def, engine.NewInstanceState("p", "p", 1, time.Unix(0, 0)), engine.NewStartInstance(time.Unix(0, 0), nil), engine.StepOptions{})
	cmdID := firstInvokeActionCommandID(t, r1.Commands)
	r2, _ := engine.Step(def, r1.State, engine.NewActionFailed(time.Unix(1, 0), cmdID, "boom", true), engine.StepOptions{})
	incID := r2.State.Incidents[0].ID
	r3, err := engine.Step(def, r2.State, engine.NewResolveIncident(time.Unix(2, 0), incID, 2), engine.StepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r3.State.Incidents) != 0 {
		t.Fatal("incident not cleared")
	}
	if !hasInvokeActionForNode(t, r3, def, "task") {
		t.Fatal("action not re-invoked after resolve")
	}
}

func TestStepResolveUnknownIncidentNoop(t *testing.T) {
	def := retryDef(t, &model.RetryPolicy{MaxAttempts: 1})
	base := engine.NewInstanceState("p", "p", 1, time.Unix(0, 0))
	r, err := engine.Step(def, base, engine.NewResolveIncident(time.Unix(0, 0), "nope", 1), engine.StepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Commands) != 0 {
		t.Fatal("unknown incident must be a no-op")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run TestStepResolve -v` → FAIL.

- [ ] **Step 3: Implement** — add a case to the top-level trigger type switch in `Step`:

```go
case ResolveIncident:
	idx := -1
	for i := range next.Incidents {
		if next.Incidents[i].ID == v.IncidentID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return StepResult{State: next}, nil // no-op
	}
	inc := next.Incidents[idx]
	next.Incidents = append(next.Incidents[:idx], next.Incidents[idx+1:]...)
	tok := /* token by inc.TokenID, as a *Token into next.Tokens */
	if tok != nil {
		tok.RetryAttempts = max(0, tok.RetryAttempts-v.AddAttempts)
		tok.State = TokenActive
		node := /* node by tok.NodeID in the right scope def */
		cmdID := next.commandID()
		cmds = append(cmds, InvokeAction{CommandID: cmdID, Name: node.Action, Input: actionInput(def, node, next)})
		tok.State = TokenWaitingCommand
		tok.AwaitCommand = cmdID
	}
	return StepResult{State: next, Commands: cmds}, nil
```

- [ ] **Step 4: Run to verify it passes** — `go test ./engine/... -run TestStepResolve -v` → PASS; `go test ./engine/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(engine): handle ResolveIncident to re-invoke parked action"`.

---

### Task 9: engine — stamp `_idempotencyKey` on every `InvokeAction`

**Files:**
- Modify: `engine/step.go` (`actionInput` helper / wherever `InvokeAction.Input` is built)
- Test: `engine/retry_test.go`

**Interfaces:**
- Produces: every `InvokeAction.Input` carries `"_idempotencyKey": instanceID + ":" + nodeID`, identical across retries.

- [ ] **Step 1: Write failing test**

```go
func TestInvokeActionCarriesStableIdempotencyKey(t *testing.T) {
	def := retryDef(t, &model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2})
	r1, _ := engine.Step(def, engine.NewInstanceState("p", "p", 1, time.Unix(0, 0)), engine.NewStartInstance(time.Unix(0, 0), nil), engine.StepOptions{})
	inv1 := firstInvokeAction(t, r1.Commands)
	if inv1.Input["_idempotencyKey"] != "p:task" {
		t.Fatalf("key = %v, want p:task", inv1.Input["_idempotencyKey"])
	}
	// after a retry, the re-invocation carries the SAME key.
	r2, _ := engine.Step(def, r1.State, engine.NewActionFailedJittered(time.Unix(10, 0), inv1.CommandID, "boom", true, 0.5), engine.StepOptions{})
	timerID := firstScheduleTimer(t, r2.Commands).TimerID
	r3, _ := engine.Step(def, r2.State, engine.NewTimerFired(time.Unix(11, 0), timerID), engine.StepOptions{})
	inv2 := firstInvokeAction(t, r3.Commands)
	if inv2.Input["_idempotencyKey"] != "p:task" {
		t.Fatalf("retry key = %v, want stable p:task", inv2.Input["_idempotencyKey"])
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run TestInvokeActionCarries -v` → FAIL.

- [ ] **Step 3: Implement** — centralize input construction in `actionInput(def, node, st)` and have it copy the node's static input (if any) and set the key:

```go
func actionInput(st InstanceState, node model.Node) map[string]any {
	in := map[string]any{}
	// (copy any existing per-node input the engine already passes, then:)
	in["_idempotencyKey"] = st.InstanceID + ":" + node.ID
	return in
}
```

Route ALL `InvokeAction{...Input}` constructions (first invoke, retry re-invoke, resolve re-invoke, and — if applicable — SLA/reminder/compensation actions) through this helper, OR scope the key to service-task action invocations only. **Do not** break compensation: check existing compensation `InvokeAction` construction and decide whether the key applies (it should — same node identity). Keep the change minimal and grounded in the real input-building code.

- [ ] **Step 4: Run to verify it passes** — `go test ./engine/... -run TestInvokeAction -v` → PASS; `go test ./engine/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(engine): stamp stable _idempotencyKey on action invocations"`.

---

# Phase B — Runtime

### Task 10: runtime `JitterSource` + sampling in `perform`

**Files:**
- Create: `runtime/jitter.go`, `runtime/jitter_test.go`
- Modify: `runtime/runner.go` (the `engine.InvokeAction` branch of `perform` that builds `ActionFailed`)

**Interfaces:**
- Produces: `runtime.JitterSource interface { Fraction() float64 }`; default `runtime.NewJitterSource() JitterSource` (`math/rand/v2`); `runtime.WithJitterSource(JitterSource) Option`. `perform` builds `NewActionFailedJittered(..., src.Fraction())` on action error.

- [ ] **Step 1: Write failing test** (`runtime/jitter_test.go`, `package runtime_test`)

```go
func TestJitterSourceInRange(t *testing.T) {
	s := runtime.NewJitterSource()
	for i := 0; i < 1000; i++ {
		f := s.Fraction()
		if f < 0 || f >= 1 {
			t.Fatalf("Fraction out of [0,1): %v", f)
		}
	}
}
```

Plus a runner test that a failing action produces an `ActionFailed` carrying the injected fixed fraction (use a fake `JitterSource` returning 0.25 and a fake catalog whose action errors; assert the journalled/delivered trigger — or expose via a seam). If a direct assertion on the trigger is hard, assert behaviour: with a fixed fraction and known policy, the scheduled retry `FireAt` matches the expected jittered delay (covered more fully in Task 13).

- [ ] **Step 2: Run to verify it fails** — `go test ./runtime/... -run TestJitterSource -v` → FAIL.

- [ ] **Step 3: Implement** —

```go
// runtime/jitter.go
package runtime

import "math/rand/v2"

type JitterSource interface{ Fraction() float64 }

type randJitter struct{ r *rand.Rand }

func NewJitterSource() JitterSource {
	return randJitter{r: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}
}
func (j randJitter) Fraction() float64 { return j.r.Float64() }
```

In `runner.go`: add a `jitter JitterSource` field (default `NewJitterSource()`), a `WithJitterSource` option, and in `perform`'s action-error branch replace `engine.NewActionFailed(r.clk.Now(), cmd.CommandID, err.Error(), true)` with `engine.NewActionFailedJittered(r.clk.Now(), cmd.CommandID, err.Error(), true, r.jitter.Fraction())`.

- [ ] **Step 4: Run to verify it passes** — `go test ./runtime/... -run TestJitter -v` → PASS; `go test ./runtime/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(runtime): JitterSource port and recorded jitter on action failure"`.

---

### Task 11: runtime `WithDefaultRetryPolicy` → `StepOptions`

**Files:**
- Modify: `runtime/runner.go` (option + thread into every `Step` call's `StepOptions`)
- Test: `runtime/runner_test.go` (or a new `runtime/retry_test.go`)

**Interfaces:**
- Produces: `runtime.WithDefaultRetryPolicy(model.RetryPolicy) Option`; the runner passes `&policy` as `StepOptions.DefaultRetryPolicy` on every `Step`.

- [ ] **Step 1: Write failing test** — with `WithDefaultRetryPolicy`, an action that errors schedules a retry (no node-level policy needed):

```go
func TestRunnerDefaultPolicyEnablesRetry(t *testing.T) {
	// def: one service task "task" with NO node policy; action errors once.
	// runner built WithDefaultRetryPolicy(...) + a MemScheduler + fake clock.
	// After Run, assert a retry timer is pending (instance not failed).
	// (Construct via the existing runner test harness; see runner_test.go helpers.)
}
```

(Write the concrete body against the existing runner test harness — reuse its `newTestRunner`/`MemScheduler`/fake-clock helpers. Assert `state.Status == engine.StatusRunning` and a `TimerRetry` timer exists in `state.Timers`.)

- [ ] **Step 2: Run to verify it fails** — `go test ./runtime/... -run TestRunnerDefaultPolicy -v` → FAIL.

- [ ] **Step 3: Implement** — add `defaultRetryPolicy *model.RetryPolicy` to `Runner`, the `WithDefaultRetryPolicy` option, and set `opt.DefaultRetryPolicy = r.defaultRetryPolicy` wherever the runner constructs `engine.StepOptions` (search `StepOptions{` in `runner.go`).

- [ ] **Step 4: Run to verify it passes** — `go test ./runtime/... -run TestRunnerDefaultPolicy -v` → PASS; `go test ./runtime/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(runtime): WithDefaultRetryPolicy threaded into StepOptions"`.

---

### Task 12: runtime `ResolveIncident` API + `InstanceSummary.IncidentCount`

**Files:**
- Modify: `runtime/runner.go` (new `ResolveIncident` method), `runtime/ports.go` (+ `IncidentCount` on `InstanceSummary`), `runtime/memstore.go` + `internal/persistence/postgres` lister (populate `IncidentCount`).
- Test: `runtime/runner_test.go`

**Interfaces:**
- Produces: `Runner.ResolveIncident(ctx context.Context, instanceID, incidentID string, addAttempts int) error`; `InstanceSummary.IncidentCount int`.

- [ ] **Step 1: Write failing test** — drive an instance to an incident (default policy MaxAttempts 1 + always-failing action), then `ResolveIncident` and assert the incident cleared / action re-invoked.

```go
func TestRunnerResolveIncident(t *testing.T) {
	// Build runner with WithDefaultRetryPolicy{MaxAttempts:1}; action that fails
	// the first time and succeeds after resolve (a counter action).
	// Run → instance StatusRunning with 1 incident. Capture incidentID from loaded state.
	// runner.ResolveIncident(ctx, id, incidentID, 2) → reload → 0 incidents, StatusCompleted.
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./runtime/... -run TestRunnerResolveIncident -v` → FAIL.

- [ ] **Step 3: Implement** — `ResolveIncident` loads the instance via the `Store`, builds `engine.NewResolveIncident(r.clk.Now(), incidentID, addAttempts)`, and runs it through the same `Deliver`/`deliverLoop` path used by other admin triggers (so it journals + persists + performs follow-on commands). Add `IncidentCount` to `InstanceSummary` and set it from `len(state.Incidents)` in both `MemStore`'s lister and the Postgres lister projection.

- [ ] **Step 4: Run to verify it passes** — `go test ./runtime/... -run TestRunnerResolveIncident -v` → PASS; `go test ./runtime/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(runtime): ResolveIncident API and IncidentCount summary"`.

---

### Task 13: runtime — retry-then-succeed e2e on `MemStore` + fake clock

**Files:**
- Test: `runtime/retry_e2e_test.go` (create)

**Interfaces:**
- Consumes: everything in Phase A/B.

- [ ] **Step 1: Write failing test** — a deterministic capstone: an action that fails twice then succeeds, a node policy `{MaxAttempts:5, InitialInterval:1s, BackoffCoef:2}`, fixed `JitterSource` returning 1.0 (so delay == full backoff), one shared fake clock driving runner + `MemScheduler`. Drive: `Run` → parks on retry timer (FireAt = +1s) → advance clock 1s → fires → re-invokes → fails → parks (+2s) → advance 2s → fires → succeeds → `StatusCompleted`.

```go
func TestRetryThenSucceedDrivesToCompletion(t *testing.T) {
	// Use clockwork.NewFakeClock (test-only import), runtime.NewMemStore(),
	// runtime.MemScheduler bound to the SAME fake clock, WithJitterSource(fixed{1.0}).
	// failingThenOK action: errors on attempts 1-2, returns ok on attempt 3.
	// Assert final loaded state.Status == engine.StatusCompleted and the action ran 3×.
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./runtime/... -run TestRetryThenSucceed -v` → FAIL.

- [ ] **Step 3: Implement** — none (behaviour already built); make the test pass by wiring the harness correctly. If it surfaces a real bug (e.g. the retry timer not parking/firing through `MemScheduler`), fix the minimal engine/runtime code and note it.

- [ ] **Step 4: Run to verify it passes** — `go test ./runtime/... -run TestRetryThenSucceed -race -v` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "test(runtime): retry-then-succeed e2e on MemStore with fake clock"`.

---

# Phase C — Persistence (codec, relay DLQ, dedup)

### Task 14: trigger codec — `JitterFraction` + `ResolveIncident`

**Files:**
- Modify: `internal/persistence/postgres/trigger_codec.go`
- Test: `internal/persistence/postgres/trigger_codec_test.go`

**Interfaces:**
- Produces: `MarshalTrigger`/`UnmarshalTrigger` round-trip `ActionFailed.JitterFraction` and the new `ResolveIncident` variant (kind constant `kindResolveIncident`).

- [ ] **Step 1: Write failing test**

```go
func TestTriggerCodecRoundTripsResolveIncidentAndJitter(t *testing.T) {
	at := time.Unix(7, 0).UTC()
	cases := []engine.Trigger{
		engine.NewActionFailedJittered(at, "c-1", "boom", true, 0.375),
		engine.NewResolveIncident(at, "p-in0", 3),
	}
	for _, trg := range cases {
		b, err := postgres.MarshalTrigger(trg) // adjust to the real exported/unexported entry point used by existing tests
		if err != nil {
			t.Fatal(err)
		}
		got, err := postgres.UnmarshalTrigger(b)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, trg) {
			t.Fatalf("round-trip mismatch:\n got %#v\nwant %#v", got, trg)
		}
	}
}
```

(Match the existing codec test's call convention — it may be a package-internal `_test.go` using unexported `marshalTrigger`.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/persistence/postgres/... -run TestTriggerCodecRoundTripsResolveIncident -v` → FAIL.

- [ ] **Step 3: Implement** — add `Jitter float64 \`json:"jitter,omitempty"\`` to the envelope and set/read it for `ActionFailed`. Add a `kindResolveIncident` constant + marshal/unmarshal arms (`IncidentID`, `AddAttempts`). Keep the envelope exhaustive over the now-14 variants.

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/persistence/postgres/... -run TestTriggerCodec -v` → PASS (needs Docker only if the test touches a DB; the codec test should be pure).

- [ ] **Step 5: Commit** — `git commit -m "feat(persistence): codec for ActionFailed jitter and ResolveIncident"`.

---

### Task 15: outbox DLQ + processed_message migration

**Files:**
- Create: `internal/persistence/postgres/migrations/0002_resilience.sql` (goose; match the existing migration's `-- +goose Up/Down` format and numbering)
- Test: `internal/persistence/postgres/migrate_test.go` (extend the existing migration test, or add one)

**Interfaces:**
- Produces: `wrkflw_outbox` columns `status`/`retry_count`/`next_attempt_at`/`last_error`; indexes `wrkflw_outbox_claim_idx`, `wrkflw_outbox_dead_idx`; table `wrkflw_processed_message`.

- [ ] **Step 1: Write failing test** — bring up a DB via `database.RunTestDatabase(t)`, run `Migrate`, assert the new columns + table exist (query `information_schema`):

```go
func TestMigration0002AddsDLQAndDedup(t *testing.T) {
	pool := database.RunTestDatabase(t)
	if err := postgres.Migrate(t.Context(), pool); err != nil { // match the real Migrate signature
		t.Fatal(err)
	}
	assertColumn(t, pool, "wrkflw_outbox", "status")
	assertColumn(t, pool, "wrkflw_outbox", "retry_count")
	assertColumn(t, pool, "wrkflw_outbox", "next_attempt_at")
	assertTable(t, pool, "wrkflw_processed_message")
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/persistence/postgres/... -run TestMigration0002 -v` (Docker) → FAIL.

- [ ] **Step 3: Implement** — the migration (§7.1/§6.2 of the spec):

```sql
-- +goose Up
ALTER TABLE wrkflw_outbox ADD COLUMN status          TEXT        NOT NULL DEFAULT 'pending';
ALTER TABLE wrkflw_outbox ADD COLUMN retry_count     INT         NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_outbox ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE wrkflw_outbox ADD COLUMN last_error      TEXT;
UPDATE wrkflw_outbox SET status = 'published' WHERE published_at IS NOT NULL;
DROP INDEX IF EXISTS wrkflw_outbox_unpublished_idx;
CREATE INDEX wrkflw_outbox_claim_idx ON wrkflw_outbox (next_attempt_at) WHERE status = 'pending';
CREATE INDEX wrkflw_outbox_dead_idx  ON wrkflw_outbox (id) WHERE status = 'dead';
CREATE TABLE wrkflw_processed_message (
    subscriber   TEXT        NOT NULL,
    message_id   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber, message_id)
);

-- +goose Down
DROP TABLE IF EXISTS wrkflw_processed_message;
DROP INDEX IF EXISTS wrkflw_outbox_dead_idx;
DROP INDEX IF EXISTS wrkflw_outbox_claim_idx;
CREATE INDEX wrkflw_outbox_unpublished_idx ON wrkflw_outbox (id) WHERE published_at IS NULL;
ALTER TABLE wrkflw_outbox DROP COLUMN last_error;
ALTER TABLE wrkflw_outbox DROP COLUMN next_attempt_at;
ALTER TABLE wrkflw_outbox DROP COLUMN retry_count;
ALTER TABLE wrkflw_outbox DROP COLUMN status;
```

Ensure the migration is embedded (the package uses goose `embed.FS` — add the file to the same dir; verify it's picked up).

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/persistence/postgres/... -run TestMigration0002 -v` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(persistence): migration for outbox DLQ columns and processed_message"`.

---

### Task 16: relay per-row isolation + backoff + dead quarantine

**Files:**
- Create: `internal/persistence/postgres/relay_backoff.go` (pure `relayBackoff`), `internal/persistence/postgres/relay_backoff_test.go`
- Modify: `internal/persistence/postgres/relay.go` (claim predicate + per-row update + quarantine)
- Test: `internal/persistence/postgres/relay_test.go` (extend)

**Interfaces:**
- Produces: a relay where a poison row is retried with backoff and quarantined to `dead` after `MaxDeliveryAttempts`, while healthy rows in the same batch are delivered. `relayBackoff(retryCount int, base, cap time.Duration) time.Duration`.

- [ ] **Step 1: Write failing test (pure backoff first)**

```go
func TestRelayBackoff(t *testing.T) {
	base, cap := time.Second, time.Minute
	if got := postgres.RelayBackoff(0, base, cap); got != time.Second {
		t.Fatalf("rc0 = %v, want 1s", got)
	}
	if got := postgres.RelayBackoff(3, base, cap); got != 8*time.Second {
		t.Fatalf("rc3 = %v, want 8s", got)
	}
	if got := postgres.RelayBackoff(100, base, cap); got != time.Minute {
		t.Fatalf("rc100 = %v, want capped 1m", got)
	}
}
```

Then the integration test (Docker): seed two unpublished outbox rows; a publisher that errors for row A (poison) but succeeds for row B. Drain repeatedly while advancing a fake clock past each backoff; assert B becomes `published` immediately, A's `retry_count` climbs and eventually `status='dead'`, and B is never blocked by A.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/persistence/postgres/... -run 'TestRelayBackoff|TestRelayPoisonIsolation' -v` → FAIL.

- [ ] **Step 3: Implement** — `relayBackoff` (pure capped-exponential). In `relay.go`: change the claim SQL to `WHERE status='pending' AND next_attempt_at <= $now ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $n`; publish each row; on success `UPDATE ... SET status='published', published_at=$now`; on error `UPDATE ... SET retry_count=retry_count+1, next_attempt_at=$now+backoff, last_error=$e, status=CASE WHEN retry_count+1 >= $max THEN 'dead' ELSE 'pending' END`. **Do not** roll back the whole batch on one row's error — commit each row's state. Drive "now" off the injected `clock.Clock`. Add a `MaxDeliveryAttempts` config field (default 10) + base/cap.

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/persistence/postgres/... -run 'TestRelay' -race -v` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(persistence): relay per-row isolation, backoff, and dead-letter quarantine"`.

---

### Task 17: DLQ admin API + façade

**Files:**
- Modify: `internal/persistence/postgres/relay.go` (`ListDeadLettered`, `Redrive`), `persistence/relay.go` (façade types + methods)
- Test: `internal/persistence/postgres/relay_test.go`

**Interfaces:**
- Produces: `Relay.ListDeadLettered(ctx, limit int) ([]DeadLetter, error)`; `Relay.Redrive(ctx, ids ...int64) (int, error)`; `persistence.DeadLetter` value type. `DeadLetter{ID int64; InstanceID, Topic, LastError string; RetryCount int; CreatedAt time.Time}`.

- [ ] **Step 1: Write failing test** (Docker) — seed a `dead` row; `ListDeadLettered` returns it; `Redrive(id)` flips it to `pending` with `retry_count=0`; a subsequent drain (publisher now healthy) publishes it.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/persistence/postgres/... -run TestRelayDLQAdmin -v` → FAIL.

- [ ] **Step 3: Implement** — the two methods (straight SQL) + the `DeadLetter` type on the façade; re-export through `persistence` returning the façade type (ADR-0008 — no internal leak). Add `var _` interface guards if the façade defines a `Relay` interface.

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/persistence/postgres/... -run TestRelayDLQAdmin -v` → PASS; `go test ./persistence/...` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(persistence): ListDeadLettered and Redrive DLQ admin API"`.

---

### Task 18: `Deduper` consumer dedup

**Files:**
- Create: `internal/persistence/postgres/dedup.go`, `internal/persistence/postgres/dedup_test.go`
- Modify: `persistence/dedup.go` (façade `NewDeduper` + `Deduper` interface)
- Test: façade test if the façade has its own tests.

**Interfaces:**
- Produces: `persistence.Deduper interface { Seen(ctx context.Context, tx pgx.Tx, subscriber, messageID string) (firstTime bool, err error) }`; `persistence.NewDeduper(pool *pgxpool.Pool) Deduper`.

- [ ] **Step 1: Write failing test** (Docker)

```go
func TestDeduperSeen(t *testing.T) {
	pool := database.RunTestDatabase(t)
	if err := postgres.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	d := postgres.NewDeduper(pool)
	tx, _ := pool.Begin(t.Context())
	first, err := d.Seen(t.Context(), tx, "sub", "m-1")
	if err != nil || !first {
		t.Fatalf("first Seen: first=%v err=%v", first, err)
	}
	again, err := d.Seen(t.Context(), tx, "sub", "m-1")
	if err != nil || again {
		t.Fatalf("duplicate Seen: again=%v err=%v", again, err)
	}
	_ = tx.Commit(t.Context())
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/persistence/postgres/... -run TestDeduper -v` → FAIL.

- [ ] **Step 3: Implement** — `Seen` does `INSERT INTO wrkflw_processed_message (subscriber, message_id) VALUES ($1,$2) ON CONFLICT DO NOTHING` and reports `firstTime` from the command tag's `RowsAffected() == 1`. Façade `NewDeduper` returns the interface.

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/persistence/postgres/... -run TestDeduper -v` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(persistence): idempotent-consumer Deduper over processed_message"`.

---

### Task 19: parked-retry Postgres resume e2e

**Files:**
- Test: `internal/persistence/postgres/resume_test.go` (extend the existing file)

**Interfaces:**
- Consumes: Phase A/B + the Postgres `Store`.

- [ ] **Step 1: Write failing test** — mirror the existing `TestPostgresParkedTimerResumesAfterReload`: start an instance whose action fails once (node policy `{MaxAttempts:3, InitialInterval:1s}`), park on the retry timer, **reload via a brand-new `Store`**, advance the fake clock 1s, deliver the `TimerFired`, action now succeeds → `StatusCompleted`. Proves `Token.RetryAttempts`/`RetryStartedAt` + the retry timer round-trip through JSONB.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/persistence/postgres/... -run TestPostgresParkedRetry -v` → FAIL (until wired; may pass immediately if everything round-trips — then it's a guard).

- [ ] **Step 3: Implement** — none expected; if the snapshot JSONB drops a new field, fix the codec/snapshot mapping.

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/persistence/postgres/... -run TestPostgresParkedRetry -race -v` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "test(persistence): parked-retry resume after reload e2e"`.

---

# Phase D — Service / transport (thin pass-through)

### Task 20: `service.ResolveIncident` + `IncidentCount` + REST/gRPC surfacing

**Files:**
- Modify: `service/service.go` (+ `ResolveIncident`, surface `IncidentCount`)
- Modify: `transport/rest` (admin route `POST /admin/instances/{id}/incidents/{incidentID}/resolve`), `transport/grpc` (RPC + proto if regenerated; otherwise REST-only and note gRPC as follow-up)
- Test: `service/service_test.go`, `transport/rest/*_test.go`

**Interfaces:**
- Produces: `service.Service.ResolveIncident(ctx, instanceID, incidentID string, addAttempts int) error` delegating to `Runner.ResolveIncident`; REST admin route returning 204 on success, mapped sentinel errors otherwise.

- [ ] **Step 1: Write failing test** — `service` test: a fake runner records the `ResolveIncident` call; assert delegation + error mapping (not-found → `ErrInstanceNotFound`). REST test: `POST /admin/instances/p/incidents/p-in0/resolve` with admin middleware allowed → 204; without admin → 403 (reuse the existing admin-gate test pattern).

- [ ] **Step 2: Run to verify it fails** — `go test ./service/... ./transport/rest/... -run Resolve -v` → FAIL.

- [ ] **Step 3: Implement** — `service.ResolveIncident` thin delegate; REST handler parsing `{id}`/`{incidentID}` + optional `{"add_attempts": N}` body (default 1), gated by the existing admin middleware; surface `IncidentCount` in the instance response mapper. (gRPC: add the RPC if the proto is regenerated easily; otherwise document as a follow-up in HANDOVER and keep REST-only — do not block the merge on protoc.)

- [ ] **Step 4: Run to verify it passes** — `go test ./service/... ./transport/rest/... -v` → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(service,transport): ResolveIncident pass-through and IncidentCount surfacing"`.

---

## Verification Checklist (run before the whole-branch review)

- [ ] `go build ./...` clean.
- [ ] `go test -race ./...` green (Docker running for Postgres testcontainers).
- [ ] `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` — ≥85% on every touched package (`model`, `engine`, `runtime`, `internal/persistence/postgres`, `persistence`, `service`, `transport/rest`).
- [ ] `golangci-lint run ./...` clean (0 issues).
- [ ] **Purity guards:** no clock / `math/rand` / transport / storage / bus import in `engine` or `model` (the existing import-guard tests must still pass). Jitter RNG only in `runtime`; `time.Now`/clock only in `runtime`/`internal/*`.
- [ ] **Opt-in proof:** the no-policy legacy `ActionFailed` path is unchanged (existing failure/error-boundary tests green).
- [ ] **Determinism proof:** a test asserts identical `(state, commands)` (incl. retry `FireAt`) for identical `(state, ActionFailed{jitter})`.
- [ ] `cloneState` test covers `Incidents` + token retry fields.
- [ ] Spec §-coverage: RetryPolicy (T1–2), retry scheduling (T5), re-invoke (T6), exhaustion catch+incident (T7), resolve (T8), idempotency key (T9), runtime jitter/default/resolve (T10–13), codec (T14), DLQ migration+relay+admin (T15–17), Deduper (T18), resume e2e (T19), transport (T20).

## Notes for the executor

- **Ground every snippet against the current source.** Helper names (`next`, `addTimer`, `commandID`, `propagateError`, `driveFlow`, `actionInput`, `NewInstanceState`, the command-scanning test helpers) are directional — find and reuse the real ones; observe the RED state each time.
- The biggest risk is Tasks 5–9 in `step.go` (a large file). Read the whole `ActionFailed` + `TimerFired` handlers and the trigger switch before editing; keep the no-policy path byte-for-byte equivalent.
- If `protoc` regeneration for gRPC (Task 20) is not readily available, ship REST-only and record gRPC `ResolveIncident` + DLQ admin RPCs as a deferred follow-up — the runtime/persistence APIs are the load-bearing deliverable.
