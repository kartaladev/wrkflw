# In-wait Reminders Generalization — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make in-wait reminders (`WithWaitReminder`) actually arm, fire, and cancel for `ReceiveTask` and `IntermediateCatchEvent` (today: `UserTask` only), surface unsupported-option usage as a non-fatal driver WARN, and rename `WithReminder`→`WithWaitReminder`.

**Architecture:** The engine fire path is already kind-agnostic (`model.ReminderOf`). Extract the `UserTask` arm block into a shared helper, call it from the two other parking strategies, generalize the staleness check to "token still parked", add a token-keyed cancel helper and call it at every resume/interrupt site. A pure `definition.Lint` reports set-but-ignored options; the driver logs them.

**Tech Stack:** Go 1.25, `expr-lang`, `clockwork`, `scheduling` (gocron). Strict TDD.

## Global Constraints

- Engine core (`engine/`) stays pure: no logger, no transport/storage imports.
- Error sentinel messages use the `workflow-<pkg>: ...` prefix.
- Black-box tests (`<pkg>_test`) preferred; table tests follow the project `table-test` skill (assert-closure form, `t.Context()`).
- Behaviour-preserving for `UserTask` reminders (existing tests must stay green).
- `go test -race ./...`, `golangci-lint run ./...` clean at the end; ≥85% coverage on touched packages.

---

### Task 1: Rename `WithReminder`→`WithWaitReminder`, `WithCatchReminder`→`WithCatchWaitReminder`

Mechanical, compiler-verified, behaviour-preserving.

**Files:**
- Modify: `definition/activity/options.go` (`WithReminder`), `definition/event/options.go` (`WithCatchReminder`), all call sites (tests, examples, README/docs snippets, godoc cross-refs).

- [ ] **Step 1: Rename the two functions** — `func WithReminder` → `func WithWaitReminder` and `func WithCatchReminder` → `func WithCatchWaitReminder` (update their godoc first lines).
- [ ] **Step 2: Update all call sites** compiler-driven:
```bash
grep -rl "WithReminder\b" --include="*.go" . | xargs sed -i '' 's/\bWithReminder\b/WithWaitReminder/g'
grep -rl "WithCatchReminder\b" --include="*.go" . | xargs sed -i '' 's/\bWithCatchReminder\b/WithCatchWaitReminder/g'
# README/docs snippets:
grep -rl "WithReminder\b" --include="*.md" . | xargs sed -i '' 's/\bWithReminder\b/WithWaitReminder/g'
grep -rl "WithCatchReminder\b" --include="*.md" . | xargs sed -i '' 's/\bWithCatchReminder\b/WithCatchWaitReminder/g'
```
- [ ] **Step 3: Build** — `go build ./...` clean.
- [ ] **Step 4: Test** — `go test ./definition/... ./engine/... ./runtime/...` green (no behaviour change).
- [ ] **Step 5: Commit** — `refactor(definition): rename WithReminder->WithWaitReminder, WithCatchReminder->WithCatchWaitReminder`.

---

### Task 2: Extract `armWaitReminder` helper; rewire `userTaskStrategy` (behaviour-preserving)

**Files:**
- Modify: `engine/step_nodes.go` (add helper; replace the inline block in `userTaskStrategy.enter` ~L543-566).

**Interfaces:**
- Produces: `func armWaitReminder(c *stepCtx, tok *Token, node model.Node, cancelKey string, cmds []Command) ([]Command, error)` — resolves the node's reminder via `model.ReminderOf`; if non-zero, appends `ScheduleTimer{Kind: TimerInWait}` and a `timerRecord{Kind: TimerInWait, Token: tok.ID, TaskToken: cancelKey, NodeID: node.ID(), ScopeID: tok.ScopeID}`. Returns the extended `cmds`.

- [ ] **Step 1: Confirm existing UserTask reminder tests pass** (the regression gate): `go test ./engine/ -run 'Reminder' -v`. Expected: PASS.
- [ ] **Step 2: Add the helper:**
```go
// armWaitReminder appends the ScheduleTimer + timer record for a node's in-wait
// reminder, if one is configured. cancelKey is the token whose resume/interrupt
// must cancel the reminder: the human-task token for UserTask, the parked token
// id for ReceiveTask / IntermediateCatchEvent.
func armWaitReminder(c *stepCtx, tok *Token, node model.Node, cancelKey string, cmds []Command) ([]Command, error) {
	reminderSpec, err := ResolveTrigger(c.eval, reminderTrigger(node), c.s.Variables)
	if err != nil {
		return cmds, fmt.Errorf("workflow-engine: reminder node %q: %w", node.ID(), err)
	}
	if reminderSpec.IsZero() {
		return cmds, nil
	}
	reminderTimerID := c.s.nextTimerID()
	cmds = append(cmds, ScheduleTimer{TimerID: reminderTimerID, Token: tok.ID, Trigger: reminderSpec, Kind: TimerInWait})
	c.s.Timers = append(c.s.Timers, timerRecord{
		TimerID: reminderTimerID, Kind: TimerInWait, Token: tok.ID,
		TaskToken: cancelKey, NodeID: node.ID(), ScopeID: tok.ScopeID,
	})
	return cmds, nil
}

// reminderTrigger returns the node's raw (unresolved) reminder TriggerSpec.
func reminderTrigger(node model.Node) schedule.TriggerSpec {
	spec, _ := model.ReminderOf(node)
	return spec
}
```
- [ ] **Step 3: Replace the inline block** in `userTaskStrategy.enter` (the `reminderSpec, err := ResolveTrigger(...)` block through the `timerRecord` append) with:
```go
cmds, err = armWaitReminder(c, tok, node, taskToken, cmds)
if err != nil {
	return cmds, false, err
}
```
- [ ] **Step 4: Test** — `go test ./engine/ -run 'Reminder' -v` still PASS (byte-identical behaviour). Also `go test ./engine/...`.
- [ ] **Step 5: Commit** — `refactor(engine): extract armWaitReminder helper (UserTask behaviour-preserving)`.

---

### Task 3: Add `cancelTimersForToken` (matches `rec.Token`)

**Files:**
- Modify: `engine/state.go` (next to `cancelTimersByTaskToken`).
- Test: `engine/state_test.go` (or a focused internal test).

**Interfaces:**
- Produces: `func (s *InstanceState) cancelTimersForToken(tokenID, excludeTimerID string) []string` — removes and returns TimerIDs of all records whose `Token == tokenID` (except excludeTimerID). Mirrors `cancelTimersByTaskToken` but keys on `Token`.

- [ ] **Step 1: Failing test** — build a state with two timer records for the same `Token` (one TimerInWait, one TimerIntermediate) plus one for another token; assert `cancelTimersForToken(tok, "")` returns exactly the two and leaves the third.
- [ ] **Step 2: Run — FAIL** (`cancelTimersForToken` undefined).
- [ ] **Step 3: Implement** (copy `cancelTimersByTaskToken`, match on `tr.Token`).
- [ ] **Step 4: Run — PASS.**
- [ ] **Step 5: Commit** — `feat(engine): cancelTimersForToken (token-keyed timer cancellation)`.

---

### Task 4: Arm reminders in `receiveTaskStrategy` + cancel on message

**Files:**
- Modify: `engine/step_nodes.go` (`receiveTaskStrategy.enter`), `engine/step_triggers.go` (`handleMessageReceived` resume).
- Test: `engine/reminder_receive_test.go` (`engine_test`).

- [ ] **Step 1: Failing test** — definition `start → receive[ReceiveTask "PaymentReceived", WithWaitReminder(Every 30m,"nudge")] → end`. Drive to park; simulate a reminder `TimerFired` (TimerInWait) → assert an `InvokeAction{Name:"nudge", FireAndForget:true}` command is emitted and the token stays parked. Then deliver `MessageReceived` → assert the token advances AND a `CancelTimer` for the reminder is emitted (no reminder record remains).
- [ ] **Step 2: Run — FAIL** (no reminder armed; no nudge; no cancel).
- [ ] **Step 3: Implement** — in `receiveTaskStrategy.enter`, after setting `AwaitMessage`, before `armBoundaries`, add:
```go
cmds, err := armWaitReminder(c, tok, node, tok.ID, nil)
if err != nil {
	return nil, false, err
}
// merge cmds with boundary cmds in the return
```
(adjust the function to accumulate `cmds` and append `bndCmds`). In `handleMessageReceived`, after `tok.AwaitMessage = ""; tok.AwaitMessageKey = ""`, add:
```go
for _, timerID := range s.cancelTimersForToken(tok.ID, "") {
	cmds = append(cmds, CancelTimer{TimerID: timerID})
}
```
- [ ] **Step 4: Run — PASS.** Also `go test ./engine/...` (no regressions).
- [ ] **Step 5: Commit** — `feat(engine): arm in-wait reminders for ReceiveTask (fire + cancel on message)`.

---

### Task 5: Arm reminders in `intermediateCatchEventStrategy` + cancel on signal/message/timer

**Files:**
- Modify: `engine/step_nodes.go` (`intermediateCatchEventStrategy.enter`), `engine/step_triggers.go` (`handleSignalReceived`, `handleTimerFired` intermediate branch; `handleMessageReceived` already covered by Task 4).
- Test: `engine/reminder_catch_test.go` (`engine_test`, table over signal/message/timer variants).

- [ ] **Step 1: Failing test (table)** — for each catch variant (signal, message, timer), definition `start → gw?...` no — `start → catch[IntermediateCatchEvent <variant>, WithCatchWaitReminder(Every 30m,"nudge")] → end`. Park; fire the reminder `TimerInWait` → assert `InvokeAction{"nudge"}` + token parked. Resolve the wait (signal/message/timer) → assert token advances + reminder `CancelTimer` emitted. For the timer-catch variant, ensure the reminder (a *different* TimerInWait) is cancelled when the intermediate timer fires.
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement** — in `intermediateCatchEventStrategy.enter`, after parking (each of the timer/signal/message/else branches), arm the reminder once:
```go
cmds, err = armWaitReminder(c, tok, node, tok.ID, cmds)
if err != nil {
	return cmds, false, err
}
```
In `handleSignalReceived` after `tok.AwaitSignal = ""`, and in `handleTimerFired` intermediate branch after `tok.AwaitCommand = ""`, add the `cancelTimersForToken(tok.ID, <firedTimerID or "">)` loop emitting `CancelTimer`. (In `handleTimerFired`, exclude the intermediate timer id that just fired if it is itself in s.Timers.)
- [ ] **Step 4: Run — PASS.** Also full `go test ./engine/...`.
- [ ] **Step 5: Commit** — `feat(engine): arm in-wait reminders for IntermediateCatchEvent (fire + cancel on resolve)`.

---

### Task 6: Generalize `handleReminderFired` staleness (token-parked, not just HumanTask)

**Files:**
- Modify: `engine/step_timers.go` (`handleReminderFired`).
- Test: `engine/reminder_catch_test.go` (add stale cases).

- [ ] **Step 1: Failing test** — a catch reminder that fires AFTER the wait resolved (token advanced) must be a clean no-op with the record removed; a catch reminder that fires WHILE parked must emit the nudge. (Task 5's happy path already covers "while parked"; add the "after resolve" stale case for a non-UserTask token — currently `TaskByToken` is nil so it's treated stale, which happens to be correct, but assert it explicitly so the generalization is pinned.)
- [ ] **Step 2: Run — FAIL or PASS-by-accident?** If it passes because `TaskByToken==nil` already returns stale, still add the explicit "live while parked" assertion: a catch reminder fires while the token is still parked → nudge emitted. That path currently returns stale (no HumanTask) → **FAIL**. This is the real gap.
- [ ] **Step 3: Implement** — change the staleness check so a reminder is live when its parked token is still awaiting, regardless of a HumanTask:
```go
tok := s.tokenAwaiting(rec.TaskToken)
if tok == nil {
	tok = s.tokenAwaiting(rec.Token) // ReceiveTask/catch: keyed on the parked token
}
if tok == nil {
	s.removeTimer(rec.TimerID)
	return StepResult{State: *s, Commands: nil}, nil
}
// If a HumanTask exists for this token, honour its open state (UserTask path);
// otherwise the parked token being present is sufficient (ReceiveTask/catch).
if task := s.TaskByToken(rec.TaskToken); task != nil && !task.IsOpen() {
	s.removeTimer(rec.TimerID)
	return StepResult{State: *s, Commands: nil}, nil
}
```
(keep the rest — resolve node, emit `InvokeAction(reminderAction)`, no reschedule.)
- [ ] **Step 4: Run — PASS.** Full `go test ./engine/...` including UserTask reminder regression.
- [ ] **Step 5: Commit** — `feat(engine): reminder staleness by parked-token (generalized beyond HumanTask)`.

---

### Task 7: Interrupt/consume-site cancel audit (no reminder leak on interrupt)

**Files:**
- Modify: `engine/step_errors.go` (~L211), `engine/step_eventsubprocess.go` (~L164), `engine/step_compensation.go` (~L87), `engine/step_boundaries.go` (~L133) — each already cancels via `cancelTimersByTaskToken(...)`; add an additive `cancelTimersForToken(<consumed parked token id>, "")` so token-keyed reminders on catch/receive tokens are also removed.
- Test: `engine/reminder_interrupt_test.go` (`engine_test`).

- [ ] **Step 1: Failing test** — a catch event WITH a reminder inside a sub-process that has an interrupting error/timer boundary; interrupt it → assert the reminder's `CancelTimer` is emitted and no reminder record survives (no stale re-fire after interrupt).
- [ ] **Step 2: Run — FAIL** (interrupt sites key on `AwaitCommand`/task token, miss the token-keyed reminder → leak).
- [ ] **Step 3: Implement** — at each of the four interrupt sites, immediately after the existing `cancelTimersByTaskToken(...)` loop, add for the consumed parked token(s):
```go
for _, timerID := range s.cancelTimersForToken(tokPtr.ID, "") {
	cmds = append(cmds, CancelTimer{TimerID: timerID})
}
```
(use the correct token variable at each site: `hostTok`, `tokPtr`, etc. — verify each by reading its surrounding loop.)
- [ ] **Step 4: Run — PASS.** Full `go test ./engine/...`.
- [ ] **Step 5: Commit** — `fix(engine): cancel token-keyed in-wait reminders on interrupt/consume paths`.

---

### Task 8: `definition.Lint(def) []Warning`

**Files:**
- Create: `definition/lint.go`, `definition/lint_test.go` (`definition_test`).

**Interfaces:**
- Produces: `type Warning struct { NodeID, Rule, Detail string }`; `func Lint(def *model.ProcessDefinition) []Warning`.

- [ ] **Step 1: Failing test (table)** — reminder on `ServiceTask`/`SendTask`/`BusinessRuleTask` → one `Warning{Rule:"reminder-ignored"}` per node; reminder on `UserTask`/`ReceiveTask`/`IntermediateCatchEvent` → none; nil/empty def → nil.
- [ ] **Step 2: Run — FAIL** (`Lint` undefined).
- [ ] **Step 3: Implement** — iterate `def.Nodes`; for each, if `model.ReminderOf(node)` is non-zero AND `node.Kind()` is not in `{KindUserTask, KindReceiveTask, KindIntermediateCatchEvent}`, append `Warning{NodeID: node.ID(), Rule: "reminder-ignored", Detail: "in-wait reminder set on a node kind that does not arm reminders (only UserTask, ReceiveTask, IntermediateCatchEvent do)"}`.
- [ ] **Step 4: Run — PASS.**
- [ ] **Step 5: Commit** — `feat(definition): Lint reports set-but-ignored options (reminder-ignored)`.

---

### Task 9: ProcessDriver logs Lint warnings once per definition

**Files:**
- Modify: `runtime/processdriver.go` (add a deduped lint-log call in `Drive`/`Deliver` entry), field for dedup.
- Test: `runtime/processdriver_lint_test.go` (`runtime_test`).

- [ ] **Step 1: Failing test** — a driver with a JSON slog handler; `Drive` a def containing a reminder on a `ServiceTask` → exactly one WARN record `msg="definition lint warning"` with the node id + rule; driving the SAME def again logs nothing more; a clean def logs nothing. Non-fatal (Drive still succeeds/parks/completes).
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement** — add `lintedDefs map[string]struct{}` (guarded by a mutex; key `def.ID + "\x00" + strconv.Itoa(def.Version)`); at the top of `Drive` (and `Deliver`), if not yet linted, call `definition.Lint(def)` and log each warning at WARN via `driver.obs.tel.Logger`, then mark linted.
- [ ] **Step 4: Run — PASS.** Full `go test ./runtime/...`.
- [ ] **Step 5: Commit** — `feat(runtime): log definition.Lint warnings once per definition (non-fatal)`.

---

### Task 10: Example scenario `catch_event_reminder`

**Files:**
- Create: `examples/scenarios/catch_event_reminder/main.go`.
- Modify: `README.md` (scenario entry #14).

- [ ] **Step 1: Write the example** — `start → await[IntermediateCatchEvent WithCatchSignal("approved"), WithCatchWaitReminder(schedule.Every(30*time.Minute),"nudge")] → end`. Wire real `scheduling.NewScheduler(scheduling.WithClock(clk))` + fake clock + SignalBus (forward-ref). Park; advance the clock 3×30m firing 3 nudges (count via a channel/counter, done-signalled); then `bus.Publish("approved")` resumes to completion; advance again and assert NO further nudge (reminder cancelled). Print a clear summary. Follow `usertask_deadline`/`inwait_reminder` wiring patterns.
- [ ] **Step 2: Build + run** — `go run ./examples/scenarios/catch_event_reminder/` prints the expected nudges-then-stop.
- [ ] **Step 3: README** — add scenario #14 with the flow diagram + link, mirroring #12/#13 format.
- [ ] **Step 4: Lint** — `golangci-lint run ./examples/scenarios/catch_event_reminder/` clean.
- [ ] **Step 5: Commit** — `docs(examples): add catch_event_reminder scenario`.

---

### Task 11: Final verification

- [ ] `go build ./...` clean; `go vet ./...` clean.
- [ ] `go test -race ./...` — 0 failures, 0 races (PG/MySQL/SQLite testcontainers).
- [ ] `golangci-lint run ./...` — 0 issues.
- [ ] Coverage on `engine`, `definition`, `runtime` ≥85%.
- [ ] `/code-review` on the branch; fold findings.
- [ ] Merge `--no-ff` to main + push (message via file).

## Self-Review

- **Spec coverage:** renames (T1), arm generalization (T2/T4/T5), staleness (T6), cancel resume+interrupt (T4/T5/T7), Lint (T8), driver logging (T9), example (T10). All spec sections mapped.
- **Placeholder scan:** none — code shown per step; the interrupt-site token variable is noted as "verify at each site" (concrete method, not a placeholder).
- **Type consistency:** `armWaitReminder(c, tok, node, cancelKey, cmds)`, `cancelTimersForToken(tokenID, exclude)`, `Warning{NodeID,Rule,Detail}`, `Lint(def)` used consistently across tasks.
- **Risk (from spec):** cancel coverage — T4/T5 cover resume, T7 covers interrupt, each with a "no fire after wait ends" assertion.
