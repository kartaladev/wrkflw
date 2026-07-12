# Repeatable Non-Interrupting Events — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. TDD is mandatory (project CLAUDE.md): observe a RED `go test` before every GREEN. Design rationale: `docs/specs/2026-07-11-repeatable-noninterrupting-design.md`.

**Goal:** Make non-interrupting boundary events and non-interrupting event sub-processes repeatable (fire once per delivery, across deliveries, until the host/scope ends), and guard the runtime so terminal instances hold no correlation waiters.

**Architecture:** One-shot is caused solely by two single-arm deletions on the non-interrupting fire path. Remove them so the arm survives (existing host-end/scope-end/terminal sweeps still retire it). Add a runtime terminal-instance guard so a longer-lived root event-sub arm can't leak a stale waiter into a dead instance.

**Tech Stack:** Go 1.25; testify; clockwork fake clock; `engine`, `runtime`, `runtime/signal`.

## Global Constraints

- Go 1.25; module `github.com/kartaladev/wrkflw`. No new deps, no new wire/definition fields.
- `table-test` skill closure form for multi-case tests; `t.Context()`; black-box where feasible, white-box (`package engine`) where arms must be constructed.
- Coverage ≥ 85% on touched packages; `go test ./...` clean; `golangci-lint run ./...` clean.
- Interrupting paths, reverse/terminate/compensation sweeps, arm-time, and wire format MUST remain untouched.

## File Structure

- `engine/step_boundaries.go` — remove the `removeBoundaryArm` call in the non-interrupting branch (~line 171).
- `engine/step_eventsubprocess.go` — remove the `removeEventTriggeredSubprocessArm` call in the non-interrupting branch (~line 248).
- `runtime/processdriver_waiters.go` — terminal-instance guard in `syncMsgWaiters` + `syncSignalBus`.
- Tests: `engine/step_boundaries_test.go`, `engine/step_events_test.go`, `engine/step_subprocess_eventstart_test.go` (flip arm-gone → arm-present, add repeat-fire); `runtime/eventsub_correlation_e2e_test.go` (arm survives + second delivery re-fires); `runtime/*_test.go` (terminal-guard).

---

### Task 1: Repeatable non-interrupting BOUNDARY

**Files:** `engine/step_boundaries.go`; tests in `engine/step_boundaries_test.go` (message) + `engine/step_events_test.go` (signal).

- [ ] **Step 1 (RED):** In `engine/step_boundaries_test.go`, extend `TestNonInterruptingMessageBoundarySpawnsParallelToken`: after the first `MessageReceived("notify")` fire, change line ~299 from `assert.Empty(t, r2.State.Boundaries, ...)` to `assert.Len(t, r2.State.Boundaries, 1, "non-interrupting boundary stays armed (repeatable)")`. Then deliver `MessageReceived("notify")` a SECOND time and assert a THIRD token was spawned (2 notify-svc tokens + host) and the arm is still present.

- [ ] **Step 2 (RED verify):** `go test ./engine/ -run TestNonInterruptingMessageBoundarySpawnsParallelToken` → FAIL (arm was removed after first fire; second delivery no-ops; `assert.Len ... 1` fails / no second token).

- [ ] **Step 3 (GREEN):** In `engine/step_boundaries.go` non-interrupting branch, delete:

```go
		// Remove only THIS boundary arm (it fired once; no re-arm in scope).
		s.removeBoundaryArm(ba.HostToken, ba.BoundaryNode)
```

Replace the surrounding comment so the branch reads:

```go
	} else {
		// Non-interrupting: leave host parked, spawn an additional token. The arm
		// stays armed so it can fire again on the next delivery (BPMN non-
		// interrupting is repeatable, ADR-0124); it is retired when the host token
		// completes (removeBoundaryArmsForHost) or the instance ends.

		// Spawn a new Active token at the boundary's outgoing flow target, keeping
		// the host token's scope.
		s.placeTokenInScope(flowTarget, hostScopeID, at)
	}
```

- [ ] **Step 4 (GREEN verify):** `go test ./engine/ -run TestNonInterruptingMessageBoundarySpawnsParallelToken` → PASS.

- [ ] **Step 5:** Flip the SIGNAL analogue in `engine/step_events_test.go` `TestNonInterruptingBoundarySpawnsParallelToken` (line ~878 `assert.Empty(t, r2.State.Boundaries, ...)` → `assert.Len(..., 1, ...)`), add a second `SignalReceived("notify")` delivery asserting a second spawned token. Run `go test ./engine/ -run 'TestNonInterruptingBoundary'` → PASS. Confirm `TestNonInterruptingBoundarySignalNoSelfCascade` still PASSES (single delivery → one spawn).

- [ ] **Step 6 (regression):** `go test ./engine/...` → PASS. Commit:

```bash
git add engine/step_boundaries.go engine/step_boundaries_test.go engine/step_events_test.go
git commit -m "feat(engine): repeatable non-interrupting boundary events (ADR-0124)"
```

---

### Task 2: Repeatable non-interrupting EVENT SUB-PROCESS

**Files:** `engine/step_eventsubprocess.go`; tests in `engine/step_subprocess_eventstart_test.go`.

- [ ] **Step 1 (RED):** In `engine/step_subprocess_eventstart_test.go` `TestEventStartSubprocess_RootNonInterrupting_Signal`: change line ~226 `assert.Empty(t, r2.State.EventTriggeredSubprocesses, ...)` to `assert.Len(t, r2.State.EventTriggeredSubprocesses, 1, "non-interrupting event-sub stays armed (repeatable)")`. Deliver `SignalReceived("notify")` a SECOND time; assert a SECOND child scope was opened (`len(scopes)` grew) and the arm is still present.

- [ ] **Step 2 (RED verify):** `go test ./engine/ -run TestEventStartSubprocess_RootNonInterrupting_Signal` → FAIL.

- [ ] **Step 3 (GREEN):** In `engine/step_eventsubprocess.go` non-interrupting branch, delete:

```go
		// Remove only THIS arm (one-shot).
		s.removeEventTriggeredSubprocessArm(ea.EnclosingScopeID, ea.EventSubprocessNode)
```

and update the branch comment to note the arm stays armed (repeatable, ADR-0124), retired when the enclosing scope closes / instance ends.

- [ ] **Step 4 (GREEN verify):** `go test ./engine/ -run TestEventStartSubprocess_RootNonInterrupting_Signal` → PASS.

- [ ] **Step 5 (regression):** `go test ./engine/...` → PASS (watch `TestEventStartSubprocess_Nested_NonInterrupting` and reverse/terminate tests). Commit:

```bash
git add engine/step_eventsubprocess.go engine/step_subprocess_eventstart_test.go
git commit -m "feat(engine): repeatable non-interrupting event sub-processes (ADR-0124)"
```

---

### Task 3: Runtime terminal-instance waiter guard + e2e repeat-fire

**Files:** `runtime/processdriver_waiters.go`; `runtime/eventsub_correlation_e2e_test.go`.

- [ ] **Step 1 (RED):** In `runtime/eventsub_correlation_e2e_test.go`, update the non-interrupting cases of BOTH `TestDeliverMessageFiresMessageEventSubprocess` and `TestBroadcastSignalFiresSignalEventSubprocess`:
  - change `assert.Empty(t, final.EventTriggeredSubprocesses, ...)` → `assert.Len(t, final.EventTriggeredSubprocesses, 1, "repeatable event-sub stays armed")`;
  - add a SECOND `DeliverMessage`/`BroadcastSignal` and assert it fires again (the arm survives; a second child scope drained — assert `final.Status==Running` and the arm still present).

  Add a new test `TestCompletedInstanceHoldsNoEventSubWaiter`: build the message event-sub def, drive to park (root event-sub armed, NOT fired), then complete the instance by delivering `"DeliveryConfirmed"` (main path → end). After completion assert the instance is `StatusCompleted` AND that a subsequent `DeliverMessage(ctx, "cancel", "order-1", …)` does NOT resurrect/no-op against the dead instance — assert the driver reports no waiter (via a fresh delivery returning cleanly with the instance still terminal / arm not fired).

- [ ] **Step 2 (RED verify):** `go test ./runtime/ -run 'EventSubprocess|CompletedInstanceHoldsNoEventSubWaiter'` → FAIL (repeat cases: arm empty after first fire in old engine already fixed by Tasks 1-2, so the repeat-fire part should pass after Tasks 1-2; the terminal-guard test FAILS because the completed instance still registers the "cancel" waiter).

- [ ] **Step 3 (GREEN):** In `runtime/processdriver_waiters.go`:

```go
func (driver *ProcessDriver) syncSignalBus(st engine.InstanceState) {
	if driver.sigbus == nil {
		return
	}
	var awaiting []string
	if !isTerminal(st.Status) {
		// A terminal instance awaits nothing; leaving a subscription for it would
		// misroute a later broadcast to a dead instance (ADR-0124).
		awaiting = st.SignalWaiters()
	}
	driver.sigbus.Sync(st.InstanceID, awaiting)
}

func (driver *ProcessDriver) syncMsgWaiters(st engine.InstanceState) {
	driver.msgMu.Lock()
	defer driver.msgMu.Unlock()

	for k, id := range driver.msgWaiters {
		if id == st.InstanceID {
			delete(driver.msgWaiters, k)
		}
	}

	// A terminal instance awaits nothing: registering a waiter for it would
	// misroute a later delivery (e.g. swallow a message that should start a fresh
	// message-start instance). A repeatable root event-sub arm can still be present
	// in the terminal snapshot, so this guard is required (ADR-0124).
	if isTerminal(st.Status) {
		return
	}

	for _, w := range st.MessageWaiters() {
		driver.msgWaiters[msgKey{Name: w.Name, CorrelationKey: w.CorrelationKey}] = st.InstanceID
	}
}
```

- [ ] **Step 4 (GREEN verify):** `go test ./runtime/ -run 'EventSubprocess|CompletedInstanceHoldsNoEventSubWaiter'` → PASS.

- [ ] **Step 5 (regression):** `go test ./runtime/...` → PASS. Commit:

```bash
git add runtime/processdriver_waiters.go runtime/eventsub_correlation_e2e_test.go
git commit -m "fix(runtime): terminal instances hold no correlation waiters (ADR-0124)"
```

---

### Task 4: ADR + verify + review + secure + merge + push

- [ ] **Step 1:** Write `docs/adr/0124-repeatable-noninterrupting.md` (Nygard): Context (one-shot = two arm deletions truncating BPMN-repeatable non-interrupting; terminal-waiter leak), Decision (remove the two deletions, no new API, per-delivery-once preserved; runtime terminal guard), Consequences (BPMN-correct repeatable firing, recurring-timer leak fixed, ADR-0123 terminal-waiter gap fixed retroactively, one-shot tests flipped, opt-in flag rejected). Reference the spec.

- [ ] **Step 2:** Full verify:

```bash
go build ./...
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
```

- [ ] **Step 3:** `/code-review high` — adjudicate + fix findings (RED-first regression tests for behavioral fixes). Re-verify.
- [ ] **Step 4:** `/security-review` — fix real findings (expected none; internal firing lifecycle + a defensive terminal guard).
- [ ] **Step 5:** Commit ADR; merge `--no-ff` to `main`; `git push origin main`; delete branch; update MEMORY.

## Verification checklist

- [ ] Non-interrupting boundary (message + signal) fires on each of two deliveries; arm survives.
- [ ] Non-interrupting event-sub (signal + message) fires on each of two deliveries; arm survives; each fire opens a fresh child scope.
- [ ] `TestNonInterruptingBoundarySignalNoSelfCascade` green (single delivery → one spawn).
- [ ] Interrupting boundary/event-sub behavior unchanged (still sweeps + cancels scope).
- [ ] Completed instance with a still-armed root event-sub registers no message/signal waiter.
- [ ] Recurring-timer non-interrupting boundary is cancelled at host completion (arm retained → sweep cancels).
- [ ] `go test ./...` clean; touched-pkg coverage ≥ 85%; lint clean; reviews clean.

## Self-Review

- **Spec coverage:** §1 boundary → Task 1; §2 event-sub → Task 2; §3 runtime terminal guard → Task 3; §4 per-delivery-once → Tasks 1-2 (no-self-cascade check); §5 no-wire-change → inherent (no wire touched); tests incl. leak + terminal-guard → Tasks 1-3; ADR → Task 4. ✅
- **Placeholders:** none — every edit is exact code.
- **Type consistency:** `isTerminal` (runtime/observability.go), `st.MessageWaiters()`/`st.SignalWaiters()` (ADR-0123), `msgKey{Name,CorrelationKey}`, arm fields — all match existing source.
