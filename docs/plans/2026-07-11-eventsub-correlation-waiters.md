# Event Sub-process Correlation Waiters — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. TDD is mandatory (see project CLAUDE.md "TDD Operational Discipline"): observe a RED state via `go test` before every GREEN.

**Goal:** Make `DeliverMessage` and `BroadcastSignal` correctly wake message- and signal-triggered event sub-process arms, by centralizing "what is this instance awaiting?" in the engine.

**Architecture:** The engine already fires event-sub arms on `MessageReceived`/`SignalReceived`; the runtime just never registered those arms in its correlation tables. Add engine-authority accessors `InstanceState.MessageWaiters()` / `SignalWaiters()` (unions over all await sources, including event-subs) and have the runtime mirror exactly those. The runtime stops enumerating construct types.

**Tech Stack:** Go 1.25; `github.com/stretchr/testify`; `github.com/jonboulle/clockwork` (fake clock); in-repo `engine`, `runtime`, `runtime/signal`, `definition/*` packages.

## Global Constraints

- Go 1.25; module `github.com/zakyalvan/krtlwrkflw`.
- Error sentinels use the `workflow-<pkg>:` prefix (no new sentinels expected here).
- Black-box tests (`package X_test`) preferred; white-box (`package engine`) only where unexported types must be constructed — matches `engine/state_esp_test.go`.
- Table tests use the project `table-test` skill closure form (`assert` closure, `ctx` modifier where relevant, `t.Context()`).
- No new deps. Never import watermill/casbin/gocron/clockwork from engine code (tests may use clockwork).
- Determinism: waiter accessors iterate their backing slices in existing slice order.
- Coverage ≥ 85% on touched packages; `go test ./...` clean; `golangci-lint run ./...` clean.

## File Structure

- `engine/state.go` — add `MessageEventSubprocessWaiters`, `SignalEventSubprocessNames`, `MessageWaiters`, `SignalWaiters` (next to the existing `MessageBoundaryWaiters`/`MessageArmedEventWaiters`, ~line 850).
- `engine/state_waiters_test.go` — NEW, `package engine` (white-box): unit tests for the four accessors, constructing `InstanceState` directly.
- `runtime/processdriver_waiters.go` — refactor `syncMsgWaiters` and `syncSignalBus` to consume the unified accessors.
- `runtime/eventsub_correlation_e2e_test.go` — NEW, `package runtime_test`: DeliverMessage→message event-sub and BroadcastSignal→signal event-sub, interrupting + non-interrupting.
- `examples/scenarios/event_subprocess/main.go` — replace the `ApplyTrigger` "cancel" workaround with `DeliverMessage`.
- `docs/adr/0123-eventsub-correlation-waiters.md` — NEW ADR (Nygard).

---

### Task 1: Engine granular event-sub accessors

**Files:**
- Modify: `engine/state.go` (add two methods near line 850)
- Test: `engine/state_waiters_test.go` (new, `package engine`)

**Interfaces:**
- Consumes: `InstanceState.EventTriggeredSubprocesses []eventTriggeredSubprocessArm` (fields `Message`, `MessageKey`, `Signal`, `TimerID`); `MessageWaiter{Name, CorrelationKey string}`.
- Produces:
  - `func (s *InstanceState) MessageEventSubprocessWaiters() []MessageWaiter`
  - `func (s *InstanceState) SignalEventSubprocessNames() []string`

- [ ] **Step 1: Write the failing tests**

Create `engine/state_waiters_test.go`:

```go
package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageEventSubprocessWaiters(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		arms   []eventTriggeredSubprocessArm
		assert func(t *testing.T, got []MessageWaiter)
	}{
		"none": {
			arms:   nil,
			assert: func(t *testing.T, got []MessageWaiter) { assert.Nil(t, got) },
		},
		"only message arms, in slice order": {
			arms: []eventTriggeredSubprocessArm{
				{EventSubprocessNode: "esp-msg", Message: "cancel", MessageKey: "order-1"},
				{EventSubprocessNode: "esp-timer", TimerID: "t1"},
				{EventSubprocessNode: "esp-sig", Signal: "sig-a"},
				{EventSubprocessNode: "esp-msg2", Message: "amend", MessageKey: ""},
			},
			assert: func(t *testing.T, got []MessageWaiter) {
				assert.Equal(t, []MessageWaiter{
					{Name: "cancel", CorrelationKey: "order-1"},
					{Name: "amend", CorrelationKey: ""},
				}, got)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &InstanceState{EventTriggeredSubprocesses: tc.arms}
			tc.assert(t, s.MessageEventSubprocessWaiters())
		})
	}
}

func TestSignalEventSubprocessNames(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		arms   []eventTriggeredSubprocessArm
		assert func(t *testing.T, got []string)
	}{
		"none": {
			arms:   nil,
			assert: func(t *testing.T, got []string) { assert.Nil(t, got) },
		},
		"only signal arms, in slice order": {
			arms: []eventTriggeredSubprocessArm{
				{EventSubprocessNode: "esp-sig", Signal: "sig-a"},
				{EventSubprocessNode: "esp-msg", Message: "cancel"},
				{EventSubprocessNode: "esp-sig2", Signal: "sig-b"},
				{EventSubprocessNode: "esp-timer", TimerID: "t1"},
			},
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"sig-a", "sig-b"}, got)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &InstanceState{EventTriggeredSubprocesses: tc.arms}
			tc.assert(t, s.SignalEventSubprocessNames())
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./engine/ -run 'TestMessageEventSubprocessWaiters|TestSignalEventSubprocessNames'`
Expected: FAIL — build error `s.MessageEventSubprocessWaiters undefined` / `s.SignalEventSubprocessNames undefined`.

- [ ] **Step 3: Write minimal implementation**

In `engine/state.go`, immediately after `MessageArmedEventWaiters` (~line 850):

```go
// MessageEventSubprocessWaiters returns the (message name, correlation key)
// pairs for every armed MESSAGE-triggered event sub-process arm. A runtime
// registers these alongside message-catch tokens, message-boundary waiters, and
// event-based-gateway message arms so a delivered message can be correlated to a
// parked instance even though an event sub-process arm carries no token
// (ADR-0122/0123). Timer and signal arms contribute no entries. The result
// preserves s.EventTriggeredSubprocesses slice order (deterministic) and is nil
// when no message arm is armed.
func (s *InstanceState) MessageEventSubprocessWaiters() []MessageWaiter {
	var out []MessageWaiter
	for i := range s.EventTriggeredSubprocesses {
		ea := &s.EventTriggeredSubprocesses[i]
		if ea.Message != "" {
			out = append(out, MessageWaiter{Name: ea.Message, CorrelationKey: ea.MessageKey})
		}
	}
	return out
}

// SignalEventSubprocessNames returns the signal names of every armed
// SIGNAL-triggered event sub-process arm. A runtime subscribes these in its
// SignalBus alongside signal-catch tokens (Token.AwaitSignal) so a broadcast
// signal can wake an event sub-process arm, which carries no token (ADR-0123).
// Timer and message arms contribute no entries. The result preserves
// s.EventTriggeredSubprocesses slice order (deterministic) and is nil when no
// signal arm is armed.
func (s *InstanceState) SignalEventSubprocessNames() []string {
	var out []string
	for i := range s.EventTriggeredSubprocesses {
		ea := &s.EventTriggeredSubprocesses[i]
		if ea.Signal != "" {
			out = append(out, ea.Signal)
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./engine/ -run 'TestMessageEventSubprocessWaiters|TestSignalEventSubprocessNames'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/state.go engine/state_waiters_test.go
git commit -m "feat(engine): granular message/signal event-sub waiter accessors (ADR-0123)"
```

---

### Task 2: Engine unified `MessageWaiters` / `SignalWaiters`

**Files:**
- Modify: `engine/state.go` (add two methods after the granular ones from Task 1)
- Test: `engine/state_waiters_test.go` (extend)

**Interfaces:**
- Consumes: `s.Tokens` (`Token.AwaitMessage`, `Token.AwaitMessageKey`, `Token.AwaitSignal`); `MessageBoundaryWaiters()`, `MessageArmedEventWaiters()`, `MessageEventSubprocessWaiters()`, `SignalEventSubprocessNames()`.
- Produces:
  - `func (s *InstanceState) MessageWaiters() []MessageWaiter`
  - `func (s *InstanceState) SignalWaiters() []string`

- [ ] **Step 1: Write the failing tests**

Append to `engine/state_waiters_test.go`:

```go
func TestMessageWaiters_UnionInOrder(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		Tokens: []Token{
			{ID: "t1", AwaitMessage: "tok-msg", AwaitMessageKey: "k1"},
			{ID: "t2"}, // not awaiting a message
		},
		Boundaries: []boundaryArm{
			{HostToken: "h1", BoundaryNode: "bnd", Message: "bnd-msg", MessageKey: "k2"},
		},
		ArmedEvents: []armedEvent{
			{Message: "gw-msg", MessageKey: "k3"},
		},
		EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
			{EventSubprocessNode: "esp", Message: "esp-msg", MessageKey: "k4"},
			{EventSubprocessNode: "esp-timer", TimerID: "t1"}, // contributes nothing
		},
	}

	// Order: tokens, then boundaries, then gateway arms, then event-subs.
	assert.Equal(t, []MessageWaiter{
		{Name: "tok-msg", CorrelationKey: "k1"},
		{Name: "bnd-msg", CorrelationKey: "k2"},
		{Name: "gw-msg", CorrelationKey: "k3"},
		{Name: "esp-msg", CorrelationKey: "k4"},
	}, s.MessageWaiters())
}

func TestMessageWaiters_Empty(t *testing.T) {
	t.Parallel()
	assert.Nil(t, (&InstanceState{}).MessageWaiters())
}

func TestSignalWaiters_Union(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		Tokens: []Token{
			{ID: "t1", AwaitSignal: "tok-sig"},
			{ID: "t2"},
		},
		EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
			{EventSubprocessNode: "esp", Signal: "esp-sig"},
			{EventSubprocessNode: "esp-msg", Message: "m"}, // contributes nothing
		},
	}

	// Order: token signals, then event-sub signals.
	assert.Equal(t, []string{"tok-sig", "esp-sig"}, s.SignalWaiters())
}

func TestSignalWaiters_Empty(t *testing.T) {
	t.Parallel()
	assert.Nil(t, (&InstanceState{}).SignalWaiters())
}
```

> NOTE for the implementer: confirm the exact struct/field names of `boundaryArm` and `armedEvent` by reading `engine/state.go` (search `type boundaryArm`, `type armedEvent`). The message fields are `Message` and `MessageKey` on both (used by `MessageBoundaryWaiters`/`MessageArmedEventWaiters`). Adjust the literals in Step 1 to match the real fields before running — do not invent fields.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./engine/ -run 'TestMessageWaiters|TestSignalWaiters'`
Expected: FAIL — `s.MessageWaiters undefined` / `s.SignalWaiters undefined`.

- [ ] **Step 3: Write minimal implementation**

In `engine/state.go`, after the Task 1 methods:

```go
// MessageWaiters returns EVERY (message name, correlation key) pair the instance
// can currently be woken by: token message-catch awaits (Token.AwaitMessage),
// armed message boundaries, event-based-gateway message arms, and
// message-triggered event sub-process arms. It is the single authority a runtime
// mirrors into its correlation table — a future message construct extends only
// this method, not every runtime call site (ADR-0123). Order is deterministic:
// tokens (slice order), then boundaries, then gateway arms, then event-subs. The
// result is nil when the instance awaits no message.
func (s *InstanceState) MessageWaiters() []MessageWaiter {
	var out []MessageWaiter
	for i := range s.Tokens {
		tok := &s.Tokens[i]
		if tok.AwaitMessage != "" {
			out = append(out, MessageWaiter{Name: tok.AwaitMessage, CorrelationKey: tok.AwaitMessageKey})
		}
	}
	out = append(out, s.MessageBoundaryWaiters()...)
	out = append(out, s.MessageArmedEventWaiters()...)
	out = append(out, s.MessageEventSubprocessWaiters()...)
	return out
}

// SignalWaiters returns EVERY signal name the instance can currently be woken by:
// token signal-catch awaits (Token.AwaitSignal) and signal-triggered event
// sub-process arms. It is the single authority a runtime mirrors into its
// SignalBus subscription set (ADR-0123). Order is deterministic: token signals
// (slice order), then event-sub signals. The list may contain duplicates when a
// token and an event-sub await the same signal; a set-based SignalBus.Sync
// collapses them. The result is nil when the instance awaits no signal.
func (s *InstanceState) SignalWaiters() []string {
	var out []string
	for i := range s.Tokens {
		if s.Tokens[i].AwaitSignal != "" {
			out = append(out, s.Tokens[i].AwaitSignal)
		}
	}
	out = append(out, s.SignalEventSubprocessNames()...)
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./engine/ -run 'TestMessageWaiters|TestSignalWaiters'`
Expected: PASS.

- [ ] **Step 5: Full engine package + commit**

Run: `go test ./engine/...`
Expected: PASS (no regression).

```bash
git add engine/state.go engine/state_waiters_test.go
git commit -m "feat(engine): unified MessageWaiters/SignalWaiters authority (ADR-0123)"
```

---

### Task 3: Runtime reconciliation mirrors the unified authority + e2e

**Files:**
- Modify: `runtime/processdriver_waiters.go` (`syncMsgWaiters`, `syncSignalBus`)
- Test: `runtime/eventsub_correlation_e2e_test.go` (new, `package runtime_test`)

**Interfaces:**
- Consumes: `engine.InstanceState.MessageWaiters()`, `engine.InstanceState.SignalWaiters()`; `ProcessDriver.DeliverMessage`, `ProcessDriver.BroadcastSignal`, `ProcessDriver.Drive`; `signal.NewSignalBus`, `runtime.WithSignalBus`.
- Produces: no new exported runtime symbols; behavioral change only.

- [ ] **Step 1: Write the failing e2e tests**

Create `runtime/eventsub_correlation_e2e_test.go`. This mirrors the def-construction of `examples/scenarios/event_subprocess` and the SignalBus wiring of `examples/scenarios/signal_broadcast`.

```go
package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
)

// eventSubMsgDef: main path parks on a ReceiveTask; a non-interrupting
// message-triggered event-sub "handleCancel" arms at start. Delivering "cancel"
// must fire the event-sub alongside the main path.
func eventSubMsgDef(t *testing.T, nonInterrupting bool) *definitionPD {
	t.Helper()
	startOpts := []event.StartOption{event.WithMessageCorrelator("cancel", "orderId")}
	if nonInterrupting {
		startOpts = append(startOpts, event.WithNonInterrupting())
	}
	inner, err := definition.NewBuilder("handle-cancel", 1).
		Add(event.NewStart("onCancel", startOpts...)).
		Add(activity.NewServiceTask("notify-cancel", activity.WithTaskAction("noop"))).
		Add(event.NewEnd("inner-end")).
		Connect("onCancel", "notify-cancel").
		Connect("notify-cancel", "inner-end").
		Build()
	require.NoError(t, err)

	def, err := definition.NewBuilder("order", 1).
		Add(event.NewStart("start")).
		Add(activity.NewReceiveTask("await", "DeliveryConfirmed", activity.WithCorrelationKey("orderId"))).
		Add(event.NewEnd("end")).
		AddSubProcess("handleCancel", inner).
		Connect("start", "await").
		Connect("await", "end").
		Build()
	require.NoError(t, err)
	return &definitionPD{def: def}
}

type definitionPD struct{ def *definitionModel }

// NOTE for implementer: definition.NewBuilder(...).Build() returns
// (*model.ProcessDefinition, error). Replace definitionModel with the real
// return type (import definition/model) and drop the definitionPD wrapper if it
// adds no value — it is only here to keep this snippet self-contained. Prefer
// returning *model.ProcessDefinition directly.

func newNoopDriver(t *testing.T) (*runtime.ProcessDriver, func(string) map[string]any) {
	t.Helper()
	cat := action.NewCatalog(map[string]action.Action{
		"noop": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return nil, nil
		}),
	})
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	d, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store))
	require.NoError(t, err)
	load := func(id string) map[string]any { return nil } // placeholder; use store.Load in real test
	return d, load
}

func TestDeliverMessageFiresMessageEventSubprocess(t *testing.T) {
	ctx := t.Context()
	// Build a non-interrupting message event-sub def (see eventSubMsgDef).
	// Drive("order-1", {orderId: "order-1"}) → main token parks on "await",
	// event-sub arms. Assert len(state.EventTriggeredSubprocesses) == 1.
	// DeliverMessage(ctx, "cancel", "order-1", {...}) → must NOT be a no-op:
	// reload state, assert the event-sub fired (arm consumed / notify-cancel ran).
	// Then DeliverMessage(ctx, "DeliveryConfirmed", "order-1", {...}) completes.
	_ = ctx
	t.Skip("replace with concrete assertions once helper return types are finalized")
}
```

> **Implementer:** the snippet above is a scaffold showing the def + driver wiring. Before Step 2, finalize it into a concrete, non-skipped test: return `*model.ProcessDefinition` from the helper, use `store.Load(ctx, id)` to reload, and assert on observable state. Concretely assert BOTH directions:
> - **Before the fix (RED):** `DeliverMessage(ctx, "cancel", "order-1", …)` leaves `state.EventTriggeredSubprocesses` length unchanged (arm still armed) and `notify-cancel` never ran — proving the silent no-op.
> - **After the fix (GREEN):** the message-event-sub arm fires (for non-interrupting: a new child scope drains and the main token still parks on `await`; assert the "cancel" arm is gone and, if you thread a flag through the `noop` action, that it was invoked).
>
> Add the signal counterpart `TestBroadcastSignalFiresSignalEventSubprocess` in the same file: build `eventSubMsgDef` variant whose inner start uses `event.WithSignalName("cancel-sig")` instead of `WithMessageCorrelator`; wire a `signal.NewSignalBus` closing over the driver (forward-reference pattern from `examples/scenarios/signal_broadcast/main.go:77-85`) and pass `runtime.WithSignalBus(bus)`; `Drive` the instance, then `driver.BroadcastSignal(ctx, "cancel-sig", …)` and assert the signal event-sub fired. Cover interrupting and non-interrupting.

- [ ] **Step 2: Run the e2e tests to verify they FAIL against current runtime**

First write the concrete (non-skipped) assertions per the note above, then:

Run: `go test ./runtime/ -run 'TestDeliverMessageFiresMessageEventSubprocess|TestBroadcastSignalFiresSignalEventSubprocess' -v`
Expected: FAIL — the event-sub arm is never woken (still armed after delivery / action never ran). THIS RED STATE IS THE PROOF OF THE BUG. Do not proceed until you see it.

- [ ] **Step 3: Refactor the runtime reconciliation**

In `runtime/processdriver_waiters.go`, replace the bodies of `syncMsgWaiters` and `syncSignalBus`:

```go
// syncSignalBus reconciles st's signal awaits (token AwaitSignal + signal
// event-sub arms, via st.SignalWaiters) with the SignalBus, if one is
// configured. This is a no-op when driver.sigbus is nil.
func (driver *ProcessDriver) syncSignalBus(st engine.InstanceState) {
	if driver.sigbus == nil {
		return
	}
	driver.sigbus.Sync(st.InstanceID, st.SignalWaiters())
}

// syncMsgWaiters reconciles the runner's internal message-waiter table with the
// current state of st. It removes stale entries for the instance and re-registers
// every (name, key) the instance awaits, as reported by the engine's single
// authority st.MessageWaiters() (token catches, message boundaries, event-gateway
// message arms, and message-triggered event sub-processes — ADR-0123).
func (driver *ProcessDriver) syncMsgWaiters(st engine.InstanceState) {
	driver.msgMu.Lock()
	defer driver.msgMu.Unlock()

	// Remove all existing entries for this instance.
	for k, id := range driver.msgWaiters {
		if id == st.InstanceID {
			delete(driver.msgWaiters, k)
		}
	}

	// Re-register from the engine's authoritative union.
	for _, w := range st.MessageWaiters() {
		driver.msgWaiters[msgKey{Name: w.Name, CorrelationKey: w.CorrelationKey}] = st.InstanceID
	}
}
```

Remove the now-unused per-construct enumeration (the token loop, `MessageBoundaryWaiters()` loop, and `MessageArmedEventWaiters()` loop) from `syncMsgWaiters` — they are subsumed by `st.MessageWaiters()`.

- [ ] **Step 4: Run the e2e tests to verify they PASS**

Run: `go test ./runtime/ -run 'TestDeliverMessageFiresMessageEventSubprocess|TestBroadcastSignalFiresSignalEventSubprocess' -v`
Expected: PASS.

- [ ] **Step 5: Full runtime package (regression) + commit**

Run: `go test ./runtime/...`
Expected: PASS — the existing message-boundary and event-gateway correlation e2e tests must stay green through the single-source refactor.

```bash
git add runtime/processdriver_waiters.go runtime/eventsub_correlation_e2e_test.go
git commit -m "fix(runtime): correlate message/signal delivery to event-sub arms (ADR-0123)"
```

---

### Task 4: Update example + write ADR-0123

**Files:**
- Modify: `examples/scenarios/event_subprocess/main.go` (~lines 131-146)
- Create: `docs/adr/0123-eventsub-correlation-waiters.md`

- [ ] **Step 1: Replace the `ApplyTrigger` workaround with `DeliverMessage`**

In `examples/scenarios/event_subprocess/main.go`, replace the "cancel" delivery block:

```go
	// A "cancel" message arrives mid-run, correlated to this order. Because
	// onCancel is non-interrupting, this spawns the event-sub ALONGSIDE the
	// main path — await-delivery is left untouched. driver.DeliverMessage now
	// correlates a delivered message to an event-sub's own message arm
	// (ADR-0123), so no ApplyTrigger workaround is needed.
	fmt.Println(`delivering message "cancel" (orderId=order-1)...`)
	if err := driver.DeliverMessage(ctx, "cancel", "order-1", map[string]any{"orderId": "order-1"}); err != nil {
		log.Fatal("deliver cancel:", err)
	}
```

Remove the now-unused `engine` and `time` imports if they become unused (the `engine.NewMessageReceived`/`time.Now()` call is gone; check the rest of the file — `engine.StatusCompleted` is still used, so keep `engine`; `time` may now be unused — remove it if so).

- [ ] **Step 2: Verify the example builds and runs**

Run: `go build ./examples/scenarios/event_subprocess/ && go run ./examples/scenarios/event_subprocess/`
Expected: builds; output shows the non-interrupting event-sub firing and both paths completing (`OK: ...`).

- [ ] **Step 3: Write ADR-0123 (Nygard template)**

Create `docs/adr/0123-eventsub-correlation-waiters.md` with Status/Date, Context (the systemic non-token reconciliation gap; message + signal silent no-ops; timer unaffected), Decision (engine-authority `MessageWaiters`/`SignalWaiters` + granular event-sub accessors; runtime mirrors one source; reject re-arm and message fan-out), Consequences (two bugs closed, defect class removed, follow-ups A/B deferred). Reference the spec at `docs/specs/2026-07-11-eventsub-correlation-waiters-design.md`.

- [ ] **Step 4: Commit**

```bash
git add examples/scenarios/event_subprocess/main.go docs/adr/0123-eventsub-correlation-waiters.md
git commit -m "docs(adr,examples): ADR-0123 + event_subprocess uses DeliverMessage"
```

---

### Task 5: Verify, review, secure, merge, push

- [ ] **Step 1: Full workspace verification**

```bash
go build ./...
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
```
Expected: build clean; all tests pass under race; touched-package coverage ≥ 85%; lint clean.

- [ ] **Step 2: `/code-review`** — run on the branch diff; adjudicate findings (fix real ones with a RED-first regression test where behavioral; record any deliberate non-fixes with rationale). Re-run tests + lint after fixes.

- [ ] **Step 3: `/security-review`** — run on the branch; fix any real findings. (Expected surface is low: no new I/O, no new inputs — accessors are pure projections; the runtime change only reorders how already-trusted internal state populates in-memory tables.)

- [ ] **Step 4: Merge + push**

```bash
git checkout main
git merge --no-ff fix/eventsub-correlation-waiters -m "Merge fix/eventsub-correlation-waiters: correlate message/signal delivery to event-sub arms (ADR-0123)"
git push origin main
```

- [ ] **Step 5: Update MEMORY.md** with an ADR-0123-shipped pointer and mark follow-up (A)/(B).

---

## Self-Review

**Spec coverage:**
- Engine granular accessors → Task 1. ✅
- Engine unified authority accessors → Task 2. ✅
- Runtime mirrors one source (both syncMsgWaiters + syncSignalBus) → Task 3. ✅
- Message event-sub e2e + signal event-sub e2e (interrupting + non-interrupting) → Task 3. ✅
- Example workaround removed → Task 4. ✅
- ADR-0123 → Task 4. ✅
- Reject re-arm / fan-out; file follow-ups A/B → captured in ADR (Task 4) + MEMORY (Task 5). ✅
- Verify/review/security/merge/push → Task 5. ✅

**Placeholder scan:** The e2e test in Task 3 Step 1 is an intentional scaffold with an explicit "finalize before running" instruction and concrete RED/GREEN assertion criteria — the implementer must confirm real return types (`*model.ProcessDefinition`) and `boundaryArm`/`armedEvent` field names against source. This is flagged, not hidden. All engine-side code (Tasks 1-2) is complete and runnable.

**Type consistency:** `MessageWaiter{Name, CorrelationKey}`, `msgKey{Name, CorrelationKey}`, arm fields `Message`/`MessageKey`/`Signal`/`TimerID`, token fields `AwaitMessage`/`AwaitMessageKey`/`AwaitSignal` — used consistently across tasks and matched to existing `state.go` usage. `MessageWaiters()`/`SignalWaiters()` names identical in engine (Task 2) and runtime (Task 3).
