# True async call activity — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a call-activity parent park and resume durably/crash-safely when its child finishes independently (children that park on human tasks/timers/signals/nested calls now work), with `engine`/`model` untouched.

**Architecture:** A `wrkflw_call_links` correlation table + `CallLinkStore` port (Mem + Postgres) holds the parent↔child link and the durable notification queue. The link is written atomically with the child's `Create`/terminal `Commit` via additive `AppliedStep` fields. `perform(StartSubInstance)` goes non-blocking; a `deliverLoop` hook flips the link on terminal; a relay-shaped `CallNotifier` delivers `SubInstanceCompleted`/`SubInstanceFailed` to parents idempotently.

**Tech Stack:** Go 1.25, pgx v5 + pgxpool, goose v3, testcontainers Postgres 17. Reuses the existing relay/LISTEN-NOTIFY patterns.

## Global Constraints

- **Go 1.25**; module `github.com/kartaladev/wrkflw`.
- **engine/model UNTOUCHED** — do NOT edit any production `.go` under `engine/` or `model/`. The `SubInstanceCompleted`/`SubInstanceFailed` triggers, `StartSubInstance` command, parent park (`AwaitCommand`), and resume (`engine/step.go:514–539`) already exist and are used as-is. A guard test (Task 9) asserts this.
- **No parent fields on `engine.InstanceState`** — correlation lives in `wrkflw_call_links` only.
- **Crash-safety:** the call-link write MUST share the child's `Create`/`Commit` transaction (ADR-0025) via `AppliedStep.NewCallLink` / `AppliedStep.CallOutcome` (both nil for all existing callers — additive, behavior-preserving).
- **Opt-in:** `runtime.NewRunner(..., WithCallLinks(store))`. Absent it, `perform(StartSubInstance)` keeps today's synchronous behavior; existing tests/consumers unaffected.
- **Idempotent parent resume:** a duplicate `SubInstanceCompleted`/`SubInstanceFailed` ⇒ `engine.ErrTokenNotFound` ⇒ the notifier treats it as success.
- **Façade discipline (ADR-0008):** `persistence` constructors return stable interface types; Postgres impls stay in `internal/persistence/postgres`.
- **TDD STRICT:** failing test + visible RED before impl. **Tests:** black-box (`package <pkg>_test`); `assert`-closure table form for 2+ cases; `t.Context()`; pair `foo.go`/`foo_test.go`. Postgres tests use `database.RunTestDatabase(t)` and run with `-p 1`; Docker required.
- **Lint:** `golangci-lint run ./...` clean (v2). **Coverage:** ≥85% on touched `runtime`, `internal/persistence/postgres`, `persistence`.
- **Commits:** Conventional Commits scoped `feat(callactivity)`/`feat(runtime)`/`feat(persistence)`, ending with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Commit per task.

## Confirmed current shapes (extend these — do not redefine)

- `runtime.AppliedStep` (`runtime/ports.go`): `{State engine.InstanceState; Trigger engine.Trigger; Events []OutboxEvent}`.
- `runtime.MemStore` (`runtime/memstore.go`): `Create(ctx, AppliedStep)(Token,error)` (line 46), `Commit(ctx, expected Token, AppliedStep)(Token,error)` (line 70); `memInstance{state, version}`.
- `runtime.Option = func(*Runner)`; existing options at `runner.go:108–184` (`WithScheduler`, `WithDefinitions`, …) — mirror their style for `WithCallLinks`.
- `runner.perform` (`runner.go:467`); `case engine.StartSubInstance:` (`runner.go:631`); `runner.Run` (`runner.go:227`); `deliverLoop` (`runner.go:290`).
- `engine.ErrTokenNotFound` (`engine/step.go:16`).
- The Postgres `Store.Create`/`Commit` (`internal/persistence/postgres/store.go`) write snapshot+journal+outbox in one tx; mirror that to add the in-tx call-link write. The relay (`internal/persistence/postgres/relay.go`) is the template for the Postgres `CallNotifier`.

---

### Task 1: Call-link value types, `CallLinkStore` port, `MemCallLinkStore`, and `AppliedStep` fields

**Files:**
- Create: `runtime/calllink.go` (value types + port + `ErrNoCallLink`)
- Create: `runtime/mem_calllink.go` (`MemCallLinkStore`)
- Modify: `runtime/ports.go` (add `AppliedStep.NewCallLink` + `CallOutcome`)
- Modify: `runtime/memstore.go` (honor the new `AppliedStep` fields against an injected `MemCallLinkStore`)
- Test: `runtime/mem_calllink_test.go`

**Interfaces:**
- Produces: `runtime.CallLink`, `runtime.CallOutcome`, `runtime.PendingNotify`, `runtime.CallLinkStore` (`ClaimPending`/`MarkNotified`/`LookupChild`), `runtime.ErrNoCallLink`, `runtime.MemCallLinkStore` + `runtime.NewMemCallLinkStore()`. `AppliedStep.NewCallLink *CallLink`, `AppliedStep.CallOutcome *CallOutcome`. `MemStore` gains an optional `*MemCallLinkStore` it writes links into (via a new `runtime.NewMemStoreWithCallLinks(cl *MemCallLinkStore) *MemStore`, plus `NewMemStore()` keeps a nil call-link store = no-op).

- [ ] **Step 1: Write the failing test**

Create `runtime/mem_calllink_test.go`:

```go
package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
)

func runningChild(id string) engine.InstanceState {
	return engine.InstanceState{InstanceID: id, DefID: "child", DefVersion: 1, Status: engine.StatusRunning}
}

func TestMemStoreRecordsCallLinkOnCreate(t *testing.T) {
	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	link := &runtime.CallLink{
		ChildInstanceID:  "p-sub-c1",
		ParentInstanceID: "p",
		ParentCommandID:  "p-c1",
		ParentDefID:      "parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State:       runningChild("p-sub-c1"),
		Trigger:     startTrg(), // existing helper: engine.NewStartInstance(...)
		NewCallLink: link,
	})
	require.NoError(t, err)

	got, ok, err := cl.LookupChild(t.Context(), "p-sub-c1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "p", got.ParentInstanceID)
	assert.Equal(t, "p-c1", got.ParentCommandID)
}

func TestMemStoreFlipsCallLinkOnTerminalCommit(t *testing.T) {
	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	tok, err := store.Create(t.Context(), runtime.AppliedStep{
		State:       runningChild("p-sub-c1"),
		Trigger:     startTrg(),
		NewCallLink: &runtime.CallLink{ChildInstanceID: "p-sub-c1", ParentInstanceID: "p", ParentCommandID: "p-c1", ParentDefID: "parent", ParentDefVersion: 1, Depth: 1},
	})
	require.NoError(t, err)

	done := runningChild("p-sub-c1")
	done.Status = engine.StatusCompleted
	done.Variables = map[string]any{"result": 42}
	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{
		State:       done,
		Trigger:     startTrg(),
		CallOutcome: &runtime.CallOutcome{Completed: true, Output: map[string]any{"result": 42}},
	})
	require.NoError(t, err)

	pending, err := cl.ClaimPending(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "p-sub-c1", pending[0].Link.ChildInstanceID)
	assert.True(t, pending[0].Outcome.Completed)
	assert.Equal(t, 42, pending[0].Outcome.Output["result"])
}
```

> `startTrg()` is the existing no-arg helper in the `runtime_test` package (`engine.NewStartInstance(time.Unix(0,0).UTC(), nil)`). If it isn't in scope, define it locally — do NOT use the nonexistent `engine.StartProcess`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./runtime/... -run 'TestMemStore(Records|Flips)CallLink'`
Expected: FAIL — `undefined: runtime.NewMemCallLinkStore` / `NewMemStoreWithCallLinks` / `AppliedStep.NewCallLink`.

- [ ] **Step 3: Add the value types + port**

Create `runtime/calllink.go`:

```go
package runtime

import (
	"context"
	"errors"
)

// ErrNoCallLink is returned by CallLinkStore.LookupChild when the instance has no
// parent (it is a root instance, not a child of any call activity).
var ErrNoCallLink = errors.New("runtime: no call link for instance")

// CallLink is the durable parent↔child correlation for one async call activity
// (ADR-0024). It is recorded atomically with the child's Create (ADR-0025).
type CallLink struct {
	ChildInstanceID  string
	ParentInstanceID string
	ParentCommandID  string // the parent token's AwaitCommand (StartSubInstance.CommandID)
	ParentDefID      string
	ParentDefVersion int
	Depth            int // call-chain depth (runaway/cycle guard)
}

// CallOutcome is a child's terminal result, recorded for the parent notification.
type CallOutcome struct {
	Completed bool           // true => SubInstanceCompleted; false => SubInstanceFailed
	Output    map[string]any // child terminal variables (when Completed)
	Err       string         // child error (when !Completed)
}

// PendingNotify is a claimed terminal link awaiting parent delivery.
type PendingNotify struct {
	Link    CallLink
	Outcome CallOutcome
}

// CallLinkStore persists parent↔child call-activity correlation and the durable
// parent-notification queue. The write side is fused into the transactional Store
// (AppliedStep.NewCallLink / CallOutcome, ADR-0025); this port is the read/claim
// side the CallNotifier uses.
type CallLinkStore interface {
	// ClaimPending returns up to limit terminal-but-unnotified links.
	ClaimPending(ctx context.Context, limit int) ([]PendingNotify, error)
	// MarkNotified records that the parent for childInstanceID has been resumed.
	MarkNotified(ctx context.Context, childInstanceID string) error
	// LookupChild returns the link for a child instance; ok=false (ErrNoCallLink)
	// when the instance is a root (no parent).
	LookupChild(ctx context.Context, childInstanceID string) (CallLink, bool, error)
}
```

- [ ] **Step 4: Add `MemCallLinkStore`**

Create `runtime/mem_calllink.go`:

```go
package runtime

import (
	"context"
	"sync"
)

// memLink is the in-memory record for one call link + its terminal outcome.
type memLink struct {
	link     CallLink
	terminal bool
	outcome  CallOutcome
	notified bool
}

// MemCallLinkStore is the in-memory CallLinkStore for the pure-runtime/test path.
type MemCallLinkStore struct {
	mu    sync.Mutex
	links map[string]*memLink // keyed by ChildInstanceID
}

var _ CallLinkStore = (*MemCallLinkStore)(nil)

func NewMemCallLinkStore() *MemCallLinkStore {
	return &MemCallLinkStore{links: make(map[string]*memLink)}
}

// record inserts a new link (called by MemStore on Create with NewCallLink).
func (m *MemCallLinkStore) record(link CallLink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[link.ChildInstanceID] = &memLink{link: link}
}

// markTerminal flips a child's link to terminal (called by MemStore on Commit with
// CallOutcome). No-op when the instance has no link (root instance).
func (m *MemCallLinkStore) markTerminal(childID string, out CallOutcome) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.links[childID]; ok {
		l.terminal = true
		l.outcome = out
	}
}

func (m *MemCallLinkStore) ClaimPending(_ context.Context, limit int) ([]PendingNotify, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PendingNotify
	for _, l := range m.links {
		if l.terminal && !l.notified {
			out = append(out, PendingNotify{Link: l.link, Outcome: l.outcome})
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *MemCallLinkStore) MarkNotified(_ context.Context, childID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.links[childID]; ok {
		l.notified = true
	}
	return nil
}

func (m *MemCallLinkStore) LookupChild(_ context.Context, childID string) (CallLink, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.links[childID]; ok {
		return l.link, true, nil
	}
	return CallLink{}, false, nil
}
```

> Note: `ClaimPending` iterates a map — for the **Mem** store this is acceptable (test path); determinism is not required here (the engine core, which must be deterministic, is untouched). The Postgres impl (Task 6) uses `ORDER BY` for stable claim order.

- [ ] **Step 5: Extend `AppliedStep` + `MemStore`**

In `runtime/ports.go`, extend `AppliedStep`:

```go
type AppliedStep struct {
	State   engine.InstanceState
	Trigger engine.Trigger
	Events  []OutboxEvent
	// NewCallLink, when non-nil, records a parent↔child call link atomically with
	// this step (set on the child's first Create). ADR-0025.
	NewCallLink *CallLink
	// CallOutcome, when non-nil, flips THIS instance's call link to terminal
	// atomically with this step (set on the child's terminal Commit). ADR-0025.
	CallOutcome *CallOutcome
}
```

In `runtime/memstore.go`: add a `callLinks *MemCallLinkStore` field to `MemStore`, a `NewMemStoreWithCallLinks(cl *MemCallLinkStore) *MemStore` constructor (and keep `NewMemStore()` setting it nil). In `Create`, after the successful insert, `if m.callLinks != nil && step.NewCallLink != nil { m.callLinks.record(*step.NewCallLink) }`. In `Commit`, after the successful version bump, `if m.callLinks != nil && step.CallOutcome != nil { m.callLinks.markTerminal(step.State.InstanceID, *step.CallOutcome) }`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./runtime/... -run 'TestMemStore(Records|Flips)CallLink'`
Run: `go test ./runtime/...` (no regression — existing `NewMemStore` callers still compile and behave identically)
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add runtime/calllink.go runtime/mem_calllink.go runtime/ports.go runtime/memstore.go runtime/mem_calllink_test.go
git commit -m "$(printf 'feat(callactivity): CallLinkStore port + MemCallLinkStore + atomic AppliedStep link fields (ADR-0024/0025)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 2: `perform(StartSubInstance)` non-blocking refactor + child kick-off with link

**Files:**
- Modify: `runtime/runner.go` (`perform` `StartSubInstance` case; add `callLinks CallLinkStore` field + `WithCallLinks` option; an internal `runChild` that injects the link into the child's first Create; `maxCallDepth` rename)
- Test: `runtime/async_callactivity_test.go`

**Interfaces:**
- Consumes: `CallLinkStore`, `CallLink` (Task 1); existing `Runner`, `DefinitionRegistry`, `perform`.
- Produces: `runtime.WithCallLinks(store CallLinkStore) Option`; refactored `perform(StartSubInstance)` that records the link, starts the child non-blocking, returns `nil`; `maxCallDepth` const (renamed from `maxCallActivityDepth`).

- [ ] **Step 1: Write the failing test**

Create `runtime/async_callactivity_test.go` — a parent whose child PARKS on a human task no longer errors; the parent stays parked and the child link is recorded. Use a `DefinitionRegistry` returning a parent def (single call-activity node → end) and a child def (single human-task node). Build the runner with `WithCallLinks(NewMemCallLinkStore())`, `WithDefinitions(reg)`, `WithHumanTasks(...)`, and a `MemStore` created via `NewMemStoreWithCallLinks(cl)` sharing the same `cl`. Assert: `runner.Run(parent)` returns with the parent `StatusRunning` (parked, not errored); the child instance exists in the store and is `StatusRunning`; `cl.LookupChild(childID)` returns the link with `ParentCommandID` == the parent's call command id.

> Model the parent/child `*model.ProcessDefinition` construction on the existing call-activity tests — search `runtime/*_test.go` and `engine/*_test.go` for `KindCallActivity` and `KindUserTask`/`KindHumanTask` to copy the exact node/flow shapes and the `DefRef` format. Ground the human-task wiring in an existing human-task runtime test.

- [ ] **Step 2: Run the test to verify it fails (RED)**

Run: `go test ./runtime/... -run TestAsyncCallActivityParentParks`
Expected: FAIL — `undefined: runtime.WithCallLinks`, and (once that compiles) the current synchronous `perform` returns the "synchronous runner does not support parked children" error instead of leaving the parent parked.

- [ ] **Step 3: Implement the refactor**

Add to `Runner`: `callLinks CallLinkStore` field; `WithCallLinks(store CallLinkStore) Option { return func(r *Runner){ r.callLinks = store } }` (mirror `WithDefinitions`). Rename `maxCallActivityDepth` → `maxCallDepth` (keep value 64). Refactor the `case engine.StartSubInstance:` block (`runner.go:631`):

- If `r.callLinks == nil`: keep the EXISTING synchronous behavior verbatim (opt-out preserved).
- Else (async):
  1. Resolve the child def (existing registry lookup).
  2. Derive `childInstanceID` (existing `<parent>-sub-<suffix>` scheme).
  3. Compute `depth`: look up the PARENT's own link via `r.callLinks.LookupChild(ctx, st.InstanceID)` — found ⇒ `parentLink.Depth + 1`, not found ⇒ `1`. If `depth > maxCallDepth`, return `engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID, "<runaway depth message>")` synchronously (the one remaining synchronous failure path).
  4. Build the `CallLink{ChildInstanceID: childInstanceID, ParentInstanceID: st.InstanceID, ParentCommandID: cmd.CommandID, ParentDefID: def.ID, ParentDefVersion: def.Version, Depth: depth}`.
  5. Start the child non-blocking: call a new internal `r.runChild(ctx, childDef, childInstanceID, cmd.Input, &link)` which drives the child's first burst (a `deliverLoop` with `create=true`, `StartInstance` trigger) AND threads `&link` so the child's first `Create` `AppliedStep` carries `NewCallLink: &link`. (Refactor `deliverLoop`/`Run` minimally to accept an optional first-step call-link, or have `runChild` build the first `AppliedStep` with the link — see Step 3a.)
  6. Return `nil, nil` — no synchronous resume trigger.

- [ ] **Step 3a: Thread the link into the child's first Create**

`deliverLoop` builds `AppliedStep{State, Trigger, Events}` (`runner.go` ~around the create/commit). Add an optional `firstCallLink *CallLink` parameter (or a small struct field) that is attached to the FIRST `AppliedStep` only (the `create` branch), then cleared. Keep all existing callers passing `nil` (no behavior change). `runChild` passes the link; `Run`/`Deliver` pass `nil`.

- [ ] **Step 4: Run the test to verify it passes (GREEN)**

Run: `go test ./runtime/... -run TestAsyncCallActivityParentParks`
Run: `go test ./runtime/...` (existing call-activity tests: the SYNCHRONOUS ones — without `WithCallLinks` — must still pass unchanged)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/runner.go runtime/async_callactivity_test.go
git commit -m "$(printf 'feat(callactivity): non-blocking perform(StartSubInstance) + WithCallLinks (ADR-0024)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 3: `deliverLoop` child-terminal hook (flip the link on terminal commit)

**Files:**
- Modify: `runtime/runner.go` (`deliverLoop`: set `AppliedStep.CallOutcome` on a transition into terminal)
- Test: `runtime/async_callactivity_test.go` (extend)

**Interfaces:**
- Consumes: `AppliedStep.CallOutcome`, `CallOutcome` (Task 1); existing terminal detection (`isTerminal(st.Status) && !isTerminal(prevStatus)` in `deliverLoop`).
- Produces: a `deliverLoop` that, when `r.callLinks != nil` and the step transitions into a terminal status, sets `AppliedStep.CallOutcome` (Completed ⇒ `st.Variables`; Failed ⇒ the failure error) so the link flips atomically.

- [ ] **Step 1: Write the failing test**

Extend `runtime/async_callactivity_test.go`: drive a CHILD instance directly to completion (a child def that completes immediately, run with `WithCallLinks(cl)` + a `MemStore` sharing `cl`, started via `runChild` or by simulating the parent call). Assert `cl.ClaimPending(10)` returns one pending notify for the child with `Outcome.Completed == true` and the child's output. Add a failure variant: a child that fails → `Outcome.Completed == false`, `Outcome.Err` set.

- [ ] **Step 2: Run the test to verify it fails (RED)**

Run: `go test ./runtime/... -run TestAsyncCallActivityChildTerminalFlipsLink`
Expected: FAIL — `ClaimPending` returns empty (the link is never flipped; `CallOutcome` not set).

- [ ] **Step 3: Implement the hook**

In `deliverLoop`, where the terminal transition is already detected (the block that does `r.obs.instCompleted.Add(...)`), when `r.callLinks != nil` set the `AppliedStep.CallOutcome` for the CURRENT step's commit:

```go
var outcome *CallOutcome
if r.callLinks != nil && isTerminal(st.Status) && !isTerminal(prevStatus) {
	switch st.Status {
	case engine.StatusCompleted:
		outcome = &CallOutcome{Completed: true, Output: copyVarsForOutcome(st.Variables)}
	default: // StatusFailed / cancelled / etc.
		outcome = &CallOutcome{Completed: false, Err: terminalErr(st)}
	}
}
appliedStep := AppliedStep{State: st, Trigger: t, Events: events, CallOutcome: outcome}
```

`terminalErr(st)` derives a short message from the terminal state (e.g. the failure recorded on the instance, or a generic "instance failed"/"instance cancelled" by status). `copyVarsForOutcome` is a shallow copy of `st.Variables` (avoid aliasing). For a root instance with no link the Postgres/Mem `markTerminal` is a no-op (zero rows / map miss), so setting `CallOutcome` unconditionally on terminal is safe.

- [ ] **Step 4: Run the tests to verify they pass (GREEN)**

Run: `go test ./runtime/... -run TestAsyncCallActivityChildTerminalFlipsLink`
Run: `go test ./runtime/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/runner.go runtime/async_callactivity_test.go
git commit -m "$(printf 'feat(callactivity): flip call link on child terminal commit (ADR-0024/0025)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 4: `CallNotifier` (runtime) + idempotent parent delivery + Mem e2e

**Files:**
- Create: `runtime/call_notifier.go` (`CallNotifier` driver over `CallLinkStore` + `DeliverFunc` + `DefinitionRegistry`)
- Test: `runtime/call_notifier_test.go` (the headline Mem e2e)

**Interfaces:**
- Consumes: `CallLinkStore`, `PendingNotify` (Task 1); `engine.NewSubInstanceCompleted`/`NewSubInstanceFailed`; `engine.ErrTokenNotFound`; `DefinitionRegistry`.
- Produces: `runtime.DeliverFunc = func(ctx, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error`; `runtime.CallNotifier` with `NewCallNotifier(cl CallLinkStore, deliver DeliverFunc, reg DefinitionRegistry, ...CallNotifierOption)` and `DrainOnce(ctx) (int, error)` + `Run(ctx) error`. `DrainOnce` claims pending, delivers to each parent (resolving the parent def via `reg`), marks notified; treats `ErrTokenNotFound` as success.

- [ ] **Step 1: Write the failing test (the headline e2e)**

Create `runtime/call_notifier_test.go`: parent calls a child that PARKS on a human task; the parent stays parked (Task 2). Complete the human task on the child (`runner.Deliver(childDef, childID, HumanCompleted{...})`) → the child completes and its link flips (Task 3). Build a `CallNotifier` with `cl`, a `DeliverFunc` wrapping `runner.Deliver` (resolving the parent def from `reg`), and `reg`; call `notifier.DrainOnce(ctx)`. Assert the PARENT is now `StatusCompleted` (resumed via `SubInstanceCompleted` and ran to its end), the child's output merged into the parent, and `cl.ClaimPending` is now empty (link marked notified).

- [ ] **Step 2: Run the test to verify it fails (RED)**

Run: `go test ./runtime/... -run TestCallNotifierResumesParkedParent`
Expected: FAIL — `undefined: runtime.NewCallNotifier`.

- [ ] **Step 3: Implement `CallNotifier`**

Create `runtime/call_notifier.go`:

```go
package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/model"
)

// DeliverFunc delivers a trigger to an instance (typically wraps Runner.Deliver
// after resolving the instance's definition). Mirrors the SignalBus DeliverFunc.
type DeliverFunc func(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error

// CallNotifier drains terminal call links and resumes the parked parent token with
// SubInstanceCompleted / SubInstanceFailed (ADR-0024). Delivery is idempotent: a
// parent whose token was already resumed (engine.ErrTokenNotFound) is treated as
// successfully notified.
type CallNotifier struct {
	cl      CallLinkStore
	deliver DeliverFunc
	reg     DefinitionRegistry
	batch   int
	clk     clock.Clock // for SubInstance* trigger timestamps
}

func NewCallNotifier(cl CallLinkStore, deliver DeliverFunc, reg DefinitionRegistry, clk clock.Clock, opts ...CallNotifierOption) *CallNotifier {
	n := &CallNotifier{cl: cl, deliver: deliver, reg: reg, batch: 100, clk: clk}
	for _, o := range opts {
		o(n)
	}
	return n
}

// DrainOnce claims one batch of terminal links and resumes their parents. Returns
// the number successfully notified.
func (n *CallNotifier) DrainOnce(ctx context.Context) (int, error) {
	pending, err := n.cl.ClaimPending(ctx, n.batch)
	if err != nil {
		return 0, fmt.Errorf("runtime: call notifier: claim: %w", err)
	}
	notified := 0
	for _, p := range pending {
		parentDef, err := n.reg.Lookup(fmt.Sprintf("%s:%d", p.Link.ParentDefID, p.Link.ParentDefVersion))
		if err != nil {
			// Cannot resolve the parent def — skip this one; a later drain retries.
			continue
		}
		var trg engine.Trigger
		if p.Outcome.Completed {
			trg = engine.NewSubInstanceCompleted(n.clk.Now(), p.Link.ParentCommandID, p.Outcome.Output)
		} else {
			trg = engine.NewSubInstanceFailed(n.clk.Now(), p.Link.ParentCommandID, p.Outcome.Err)
		}
		derr := n.deliver(ctx, parentDef, p.Link.ParentInstanceID, trg)
		if derr != nil && !errors.Is(derr, engine.ErrTokenNotFound) {
			// Transient/real failure — leave the link claimable for a later drain.
			continue
		}
		// Success OR duplicate (ErrTokenNotFound = parent already resumed): mark notified.
		if err := n.cl.MarkNotified(ctx, p.Link.ChildInstanceID); err != nil {
			return notified, fmt.Errorf("runtime: call notifier: mark notified: %w", err)
		}
		notified++
	}
	return notified, nil
}
```

Add `Run(ctx) error` (a poll loop calling `DrainOnce` on a ticker until ctx cancel — mirror `relay.Run`'s structure) and `CallNotifierOption` (`WithCallNotifierBatchSize`). Add the `clock` import.

> Confirm-point: the exact `engine.NewSubInstanceCompleted`/`NewSubInstanceFailed` constructor signatures (the explore map shows `NewSubInstanceCompleted(at, commandID, output)` / `NewSubInstanceFailed(at, commandID, err)` — verify against `engine/trigger.go`). `DefinitionRegistry.Lookup(defRef)` — confirm the `"id:version"` ref format against the existing registry.

- [ ] **Step 4: Run the test to verify it passes (GREEN)**

Run: `go test ./runtime/... -run TestCallNotifierResumesParkedParent`
Run: `go test -race ./runtime/...`
Expected: PASS — the parent resumes and completes; idempotent on a second `DrainOnce` (no pending).

- [ ] **Step 5: Commit**

```bash
git add runtime/call_notifier.go runtime/call_notifier_test.go
git commit -m "$(printf 'feat(callactivity): CallNotifier resumes parked parents idempotently (ADR-0024)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 5: Postgres `wrkflw_call_links` migration + atomic Store side-effects

**Files:**
- Create: `internal/persistence/postgres/migrations/0004_call_links.sql`
- Modify: `internal/persistence/postgres/store.go` (`Create` honors `NewCallLink`; `Commit` honors `CallOutcome` — both in the existing tx)
- Test: `internal/persistence/postgres/call_links_store_test.go`

**Interfaces:**
- Consumes: `AppliedStep.NewCallLink`/`CallOutcome` (Task 1); the existing `Store.Create`/`Commit` tx.
- Produces: the `wrkflw_call_links` table; `Store.Create`/`Commit` write/flip the link in-tx.

- [ ] **Step 1: Migration SQL** — `0004_call_links.sql` with `-- +goose Up`/`Down`, the `wrkflw_call_links` table + partial index from spec §3.

- [ ] **Step 2: Write the failing test** (testcontainers): `Create` with `NewCallLink` → the row exists with `status='running'`; `Commit` with `CallOutcome{Completed:true, Output}` → the row is `status='completed'` with the output JSONB. Use `database.RunTestDatabase`, `t.Context()`, `-p 1`.

- [ ] **Step 3: Implement** — in `Store.Create`, after the instance INSERT and before `tx.Commit`, `if step.NewCallLink != nil { insert into wrkflw_call_links … }` using `tx` (same tx). In `Store.Commit`, after the snapshot UPDATE and before `tx.Commit`, `if step.CallOutcome != nil { update wrkflw_call_links set status=$, output=$, error=$ where child_instance_id=$state.InstanceID }` using `tx` (zero rows for a root instance = clean no-op). Marshal `Output` to JSONB. Wrap errors `casbin`-style (`fmt.Errorf("postgres: create: call link: %w", err)`); a unique-violation on the PK maps via the existing `mapConflict`.

- [ ] **Step 4: Run** `go test -p 1 ./internal/persistence/postgres/... -run TestStoreCallLink` → PASS. Run the full postgres package `-p 1` to confirm no regression (existing Create/Commit callers pass `nil` link fields).

- [ ] **Step 5: Commit** (`feat(persistence): wrkflw_call_links migration + atomic Store link side-effects (ADR-0025)`).

---

### Task 6: Postgres `CallLinkStore` + façade

**Files:**
- Create: `internal/persistence/postgres/call_links.go` (`CallLinkStore` impl)
- Modify: `persistence/persistence.go` (`NewCallLinkStore`)
- Test: `internal/persistence/postgres/call_links_store_test.go` (extend)

**Interfaces:**
- Produces: a Postgres `CallLinkStore` (`ClaimPending` via `SELECT … WHERE status IN ('completed','failed') AND notified_at IS NULL ORDER BY child_instance_id FOR UPDATE SKIP LOCKED LIMIT $1`; `MarkNotified` via `UPDATE … SET status='notified', notified_at=$now WHERE child_instance_id=$1`; `LookupChild` via `SELECT …`); `persistence.NewCallLinkStore(pool) runtime.CallLinkStore`.

- [ ] **Step 1–4:** TDD with testcontainers — `ClaimPending` returns terminal-unnotified links in stable order and respects the limit; `MarkNotified` removes a link from the claim set (a second `ClaimPending` excludes it); `LookupChild` returns the link or `(_, false, nil)`; `ClaimPending` uses `FOR UPDATE SKIP LOCKED` (two concurrent claims don't double-return — assert via two txns if feasible, else document). Façade `NewCallLinkStore` returns the `runtime.CallLinkStore` interface (compile-time `var _ runtime.CallLinkStore`). Mirror the relay's row-scan + telemetry idioms.

- [ ] **Step 5:** Commit (`feat(persistence): Postgres CallLinkStore + facade (ADR-0024)`).

---

### Task 7: Postgres `CallNotifier` driver (relay-shaped) + façade

**Files:**
- Create: `internal/persistence/postgres/call_notifier.go` (the durable driver) — **mirror `internal/persistence/postgres/relay.go`** for structure: poll `Run` loop, `DrainOnce`, per-row isolation + capped backoff, telemetry (`observability.Telemetry`, `wrkflw.callnotifier.batch` span), optional `WithListenNotify` on a `wrkflw_call_links` channel.
- Modify: `persistence/persistence.go` (`NewCallNotifier`)
- Test: `internal/persistence/postgres/call_notifier_test.go`

**Interfaces:**
- Consumes: the Postgres `CallLinkStore` (Task 6), `runtime.DeliverFunc`, `DefinitionRegistry`, the relay patterns.
- Produces: a Postgres-backed notifier with the same delivery+idempotency logic as `runtime.CallNotifier` (Task 4) but driven by the SKIP-LOCKED claim; `persistence.NewCallNotifier(pool, deliver runtime.DeliverFunc, reg runtime.DefinitionRegistry, ...Option)`.

> Implementation note: the delivery/idempotency LOGIC (resolve parent def → build `SubInstanceCompleted`/`Failed` → `Deliver` → `ErrTokenNotFound`-as-success → mark notified) is identical to Task 4's `runtime.CallNotifier.DrainOnce`. Prefer to REUSE `runtime.CallNotifier` by giving it the Postgres `CallLinkStore` (whose `ClaimPending` does the SKIP-LOCKED claim + `MarkNotified` the durable mark) rather than duplicating the logic. If the durable claim needs to hold a tx across the deliver (so a crash mid-deliver re-claims), implement a thin Postgres `DrainOnce` that claims in a tx, delivers, marks, commits — mirroring `relay.DrainOnce`'s per-row isolation. Choose reuse where the contract allows; duplicate only the tx-holding claim if required. Document the choice in the report.

- [ ] **Step 1–4:** TDD with testcontainers — the **crash-safety e2e**: park a parent + commit the child terminal (link `completed`), build a **fresh** notifier over a **new pool**, `DrainOnce`, assert the parent resumes (proves durability across process restart). Plus: idempotent duplicate `DrainOnce`; a parent whose token is already consumed is marked notified (no error). `-p 1`, Docker.
- [ ] **Step 5:** Commit (`feat(persistence): durable Postgres CallNotifier (ADR-0024)`).

---

### Task 8: Cross-cutting e2e — nested async, failure path, runaway guard, opt-out

**Files:**
- Create: `runtime/async_callactivity_e2e_test.go` (Mem) and/or `internal/persistence/postgres/async_callactivity_e2e_test.go` (Postgres)

- [ ] Tests (each a focused `t.Run` or function):
  - **Nested async:** parent → child → grandchild, each parking; completing the grandchild cascades up (run the notifier repeatedly / `Run`) until the parent completes; assert `depth` increments per level.
  - **Failure path:** a child that fails → notifier delivers `SubInstanceFailed` → the parent's error boundary / propagation fires (assert the parent's terminal status / error flow matches the synchronous failure semantics).
  - **Runaway guard:** a self-calling definition (parent DefRef == its own) is rejected at `depth > maxCallDepth`; assert the parent gets `SubInstanceFailed` and no unbounded child spawning.
  - **Opt-out preserved:** a runner WITHOUT `WithCallLinks` whose child parks still returns today's synchronous "does not support parked children" error (behavior-preserving default).
- [ ] Commit (`test(callactivity): nested/failure/runaway/opt-out e2e`).

---

### Task 9: Verification gate + engine-unchanged guard + HANDOVER

**Files:**
- Create: an engine-unchanged guard (a test or a documented `git diff --stat <merge-base> HEAD -- engine/ model/` check showing zero production changes), e.g. assert in CI notes; pragmatically, Task 9 verifies `git diff <merge-base>..HEAD -- 'engine/*.go' 'model/*.go' | grep -v _test` is empty.
- Modify: `docs/plans/HANDOVER.md`

- [ ] **Step 1: engine/model unchanged proof** — run `git diff <merge-base>..HEAD -- engine model | grep -vE '_test\.go|^\+\+\+|^---|^diff|^index|@@' | grep -E '^[+-]'` and confirm only `_test.go` lines (if any) appear; production engine/model is untouched. Record the output in the report.
- [ ] **Step 2: full gate** —
  ```
  go test -race $(go list ./... | grep -v 'internal/persistence/postgres')
  go test -race -p 1 ./internal/persistence/postgres/...
  go test -coverprofile=cover.out ./runtime/... ./persistence/... ./internal/persistence/postgres/... && go tool cover -func=cover.out | tail -1
  golangci-lint run ./...
  ```
  ≥85% on `runtime`, `persistence`, `internal/persistence/postgres`; lint 0. Don't commit `cover.out`.
- [ ] **Step 3: HANDOVER** — add a "## True async call activity — ✅ COMPLETE" section (what shipped by layer; ADRs 0024–0025; real gate numbers; deferred follow-ups: cancellation propagation parent→child, cross-machine child execution, per-definition maxCallDepth, strict per-parent notify ordering). Flip the resume-point "Next focus" — async call activity ✅ DONE; the deferred-backlog "Also outstanding" list is now empty.
- [ ] **Step 4: Commit** (`docs(callactivity): mark true async call activity complete + engine-unchanged proof`).

---

## Verification checklist (whole track)

- [ ] Task 1 — `CallLinkStore`/`MemCallLinkStore`; `MemStore` records link on Create, flips on terminal Commit.
- [ ] Task 2 — parent calling a parking child no longer errors (with `WithCallLinks`); parent parked, link recorded; opt-out (no `WithCallLinks`) unchanged.
- [ ] Task 3 — child terminal commit flips its link (completed/failed) via `AppliedStep.CallOutcome`.
- [ ] Task 4 — `CallNotifier` resumes the parked parent (`SubInstanceCompleted`), merges output, idempotent on duplicate (`ErrTokenNotFound`).
- [ ] Task 5 — Postgres `wrkflw_call_links` migration; link written/flipped IN the child's Create/Commit tx (atomic).
- [ ] Task 6 — Postgres `CallLinkStore` (SKIP-LOCKED claim, mark, lookup) + façade.
- [ ] Task 7 — durable Postgres notifier; crash-safety e2e (fresh notifier/new pool resumes the parent).
- [ ] Task 8 — nested async, failure path, runaway guard, opt-out-preserved.
- [ ] Task 9 — full gate green (race, ≥85% touched pkgs, lint 0); **engine/model production code unchanged** (proof); HANDOVER updated.

## Self-review notes (plan author)

- **Spec coverage:** §3 table → Task 5; §4 port → Task 1; §5 atomicity → Tasks 1 (Mem) + 5 (PG); §6.1 perform → Task 2; §6.2 hook → Task 3; §6.3 notifier → Tasks 4 (Mem) + 7 (PG); §6.4 wiring → Tasks 2/6/7; §9 testing → Tasks 4/7/8; §2 invariants (engine-untouched, opt-in, crash-safe, idempotent) → Tasks 2/5/7/9. Covered.
- **Type consistency:** `CallLink`/`CallOutcome`/`PendingNotify`/`CallLinkStore`/`MemCallLinkStore`/`NewMemStoreWithCallLinks`/`AppliedStep.NewCallLink`/`CallOutcome`/`WithCallLinks`/`maxCallDepth`/`DeliverFunc`/`CallNotifier`/`NewCallNotifier`/`NewCallLinkStore` are named identically across tasks.
- **Confirm-points flagged:** `engine.NewSubInstanceCompleted`/`NewSubInstanceFailed` constructor signatures; `DefinitionRegistry.Lookup` ref format; the existing call-activity + human-task test node/flow shapes (for Tasks 2/4/8 fixtures); whether to reuse `runtime.CallNotifier` for the Postgres driver or write a tx-holding `DrainOnce` (Task 7). Implementers verify against current code — trust the test/red state over the listing.
