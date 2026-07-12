# Transactional SendTask Outbox Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a BPMN SendTask's outbound message with true transactional-outbox semantics — written atomically with the state commit into the existing `wrkflw_outbox` as a `message.<Name>` event, relayed at-least-once — replacing ADR-0060's best-effort `MessageSink` port.

**Architecture:** A `SendMessage` engine command is turned into an `OutboxEvent` in `AppliedStep.Events` at the deliverLoop edge (mirroring `terminalOutboxEvent`), so the existing `writeOutbox`/`Relay`/`Publisher` machinery carries it with no new table. The post-commit `MessageSink` port is retired (Replace decision); consumers customize delivery via a `message.*` subscriber, for which a reference `eventing.NewMessageHandler` + `Runner.DeliverMessage` is provided.

**Tech Stack:** Go 1.25; `runtime` (engine command performer), `eventing` (watermill adapter, kept out of `runtime`/`engine`), the existing `wrkflw_outbox` + `persistence.Relay` + `eventing.NewPublisher`.

## Global Constraints

- Go 1.25; module `github.com/kartaladev/wrkflw`.
- TDD strict (CLAUDE.md): every new symbol/behavior change gets a failing test (visible RED — a compile error counts) before implementation; the implementer's report must show RED and GREEN command output.
- Black-box tests preferred (`<package>_test`); `t.Context()` over `context.Background()`; testify assert/require; project `table-test` `assert`-closure form when 2+ cases share one SUT call.
- Never import `watermill` from `engine`/`runtime`/workflow code — the `message.*` subscriber adapter lives in `eventing/` (watermill is confined there and `internal/eventing/watermill`).
- `engine/` and `model/` stay **zero-diff** (`engine.SendMessage` and `sendTaskStrategy` already exist and are not changed). Confirm in the final task.
- Public message contract (use verbatim): topic `message.<MessageName>`; payload JSON `{"messageName": <name>, "correlationKey": <key>, "variables": <sender vars copy>}`; `instance_id` + `definition_ref` carried as `OutboxEvent` fields (the existing publisher maps them to watermill metadata).
- Conventional Commits scoped to the area; commit per task; end commit messages with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- Spec: `docs/specs/transactional-sendtask-outbox.md`. ADR to record: **0067** (supersedes ADR-0060).
- Next free ADR is 0067 (the clock refactor took 0066).

---

### Task 1: Record ADR-0067 (supersede ADR-0060)

**Files:**
- Create: `docs/adr/0067-transactional-sendtask-outbox.md`
- Modify: `docs/adr/0060-sendtask-message-sink.md` (Status line → Superseded)

**Interfaces:**
- Produces: the decision record every later commit references.

- [ ] **Step 1: Write ADR-0067 (Nygard template)**

Create `docs/adr/0067-transactional-sendtask-outbox.md`:

```markdown
# 67. Transactional SendTask delivery via the event outbox (supersedes ADR-0060)

- Status: Accepted
- Date: 2026-06-26
- Supersedes: ADR-0060

## Context

ADR-0060 routed a SendTask's outbound message through a consumer-supplied `MessageSink`
port whose `Send` runs AFTER the state commit (commit-before-perform). Because `Send` is in
a separate transaction from the state commit, a crash between the commit and `Send` strands
the message: the instance has durably advanced past the SendTask but the message is never
sent and never retried. ADR-0060 documented this as best-effort. True atomicity is
unreachable through the `Send` port itself, since `Send` is post-commit by construction.

## Decision

Carry the outbound message as an `OutboxEvent` in `AppliedStep.Events`, derived from the
`engine.SendMessage` command at the deliverLoop edge (mirroring `terminalOutboxEvent`). The
existing `Store.writeOutbox` persists it inside the state-commit transaction; the existing
`Relay` drains it at-least-once; the existing watermill `Publisher` publishes it on topic
`message.<Name>` with payload `{messageName, correlationKey, variables}`. The `perform`
handler for `SendMessage` becomes a no-op. The `MessageSink`/`OutboundMessage` port and
`WithMessageSink` are RETIRED (Replace). Consumers customize delivery via a `message.*`
subscriber; a reference `eventing.NewMessageHandler` routes to `Runner.DeliverMessage` for
intra-engine resume of a parked ReceiveTask. `engine/` and `model/` are unchanged.

## Consequences

- SendTask delivery is atomic with state and at-least-once (retry/DLQ via the existing
  relay); the ADR-0060 stranding window is eliminated.
- The synchronous in-process `MessageSink` hook is gone; consumer customization moves to an
  async `message.*` subscriber (more durable, fan-out-capable, but broker-coupled).
- `Runner.DeliverMessage`'s waiter index is in-memory per Runner, so intra-engine
  correlation works within one process; cross-process correlation is the consumer's
  responsibility (an external subscriber / correlation store).
- Messages and domain events share `wrkflw_outbox`, namespaced by topic (`message.*` vs
  `instance.*`). Intentional reuse, no new table/migration.
```

- [ ] **Step 2: Mark ADR-0060 superseded**

In `docs/adr/0060-sendtask-message-sink.md`, change the `- Status: Accepted` line to:

```markdown
- Status: Superseded by ADR-0067
```

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0067-transactional-sendtask-outbox.md docs/adr/0060-sendtask-message-sink.md
git commit -m "docs(adr): transactional SendTask outbox supersedes the MessageSink port

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `outboundMessageEvents` derivation

**Files:**
- Modify: `runtime/outbox.go` (add the function beside `terminalOutboxEvent`/`instanceDefRef`)
- Test: `runtime/outbox_test.go` (black-box `runtime_test`)

**Interfaces:**
- Consumes: `engine.SendMessage{Name, CorrelationKey, Payload}`, `engine.InstanceState`, the existing unexported `instanceDefRef(st) string` and `OutboxEvent` (in package `runtime`).
- Produces: `func outboundMessageEvents(st engine.InstanceState, cmds []engine.Command) []OutboxEvent` (unexported; Task 3 calls it from the same package).

- [ ] **Step 1: Write the failing table test**

Add to `runtime/outbox_test.go` (create the file if absent; package `runtime_test`). Because `outboundMessageEvents` is unexported, this black-box test reaches it through the package's existing test export shim if one exists; if `runtime` has no export-shim file for unexported helpers, place THIS test in an internal `package runtime` test file `runtime/outbox_internal_test.go` instead (the function is an internal helper, so a white-box test is appropriate here — note this in your report).

```go
func TestOutboundMessageEvents(t *testing.T) {
	st := engine.InstanceState{InstanceID: "i-1", DefID: "shipping", DefVersion: 2}
	cases := []struct {
		name   string
		cmds   []engine.Command
		assert func(t *testing.T, got []OutboxEvent)
	}{
		{
			name: "no send commands yields nil",
			cmds: []engine.Command{engine.CompleteInstance{}},
			assert: func(t *testing.T, got []OutboxEvent) {
				assert.Nil(t, got)
			},
		},
		{
			name: "one send command yields one message event",
			cmds: []engine.Command{engine.SendMessage{Name: "OrderPlaced", CorrelationKey: "ord-7", Payload: map[string]any{"amount": 10}}},
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Len(t, got, 1)
				assert.Equal(t, "message.OrderPlaced", got[0].Topic)
				assert.Equal(t, "i-1", got[0].InstanceID)
				assert.Equal(t, "shipping:2", got[0].DefinitionRef)
				assert.Equal(t, "OrderPlaced", got[0].Payload["messageName"])
				assert.Equal(t, "ord-7", got[0].Payload["correlationKey"])
				assert.Equal(t, map[string]any{"amount": 10}, got[0].Payload["variables"])
			},
		},
		{
			name: "multiple send commands preserve order",
			cmds: []engine.Command{
				engine.SendMessage{Name: "A"},
				engine.InvokeAction{},
				engine.SendMessage{Name: "B"},
			},
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Len(t, got, 2)
				assert.Equal(t, "message.A", got[0].Topic)
				assert.Equal(t, "message.B", got[1].Topic)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, outboundMessageEvents(st, tc.cmds))
		})
	}
}
```

(If you use the white-box `package runtime` file, refer to `outboundMessageEvents`, `OutboxEvent` directly without the `runtime.` qualifier and drop the package import of runtime.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run 'TestOutboundMessageEvents' -v`
Expected: FAIL — `undefined: outboundMessageEvents`.

- [ ] **Step 3: Implement the derivation**

In `runtime/outbox.go`, add after `terminalEventErr`:

```go
// outboundMessageEvents turns each engine.SendMessage command into a message.<Name>
// outbox event so a SendTask message is written atomically in the state-commit tx and
// relayed at-least-once, exactly like a domain event (ADR-0067). The payload carries the
// message name, the resolved correlation key, and a copy of the sender's variables.
func outboundMessageEvents(st engine.InstanceState, cmds []engine.Command) []OutboxEvent {
	var out []OutboxEvent
	for _, c := range cmds {
		m, ok := c.(engine.SendMessage)
		if !ok {
			continue
		}
		out = append(out, OutboxEvent{
			Topic:         "message." + m.Name,
			Payload:       map[string]any{"messageName": m.Name, "correlationKey": m.CorrelationKey, "variables": m.Payload},
			InstanceID:    st.InstanceID,
			DefinitionRef: instanceDefRef(st),
		})
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./runtime/ -run 'TestOutboundMessageEvents' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/outbox.go runtime/outbox_test.go
git commit -m "feat(runtime): derive message.<Name> outbox events from SendMessage (ADR-0067)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Wire derivation into deliverLoop + retire MessageSink

**Files:**
- Modify: `runtime/runner.go` (append message events in deliverLoop ~line 455; `SendMessage` perform case ~line 919 → no-op; remove `msgSink` field + `WithMessageSink`)
- Delete: `runtime/message_sink.go`, `runtime/message_sink_test.go`
- Test: `runtime/sendtask_outbox_test.go` (new integration test; black-box `runtime_test`)

**Interfaces:**
- Consumes: `outboundMessageEvents` (Task 2); the existing in-deliverLoop `events := terminalOutboxEvent(...)` assembly and `AppliedStep{... Events: events ...}`.
- Produces: a SendTask step now commits a `message.<Name>` event in `AppliedStep.Events`; `Run`/`Deliver` no longer require a `MessageSink`. Removes exported symbols `MessageSink`, `OutboundMessage`, `WithMessageSink` from package `runtime`.

- [ ] **Step 1: Write the failing integration test**

Add `runtime/sendtask_outbox_test.go` (package `runtime_test`). Adapt the SendTask process-definition setup from the soon-to-be-deleted `runtime/message_sink_test.go` (read it first — copy its `model.NewSendTask(...)` definition wiring and the `NewRunner` construction), but assert on committed outbox events instead of a sink call. Use a recording `Store` that captures committed `AppliedStep`s (if the package already has one, reuse it; otherwise define a small wrapper around the in-memory store in this test file):

```go
// recordingStore wraps an in-memory Store and captures every committed AppliedStep.
type recordingStore struct {
	runtime.Store
	steps []runtime.AppliedStep
}

func (s *recordingStore) Create(ctx context.Context, step runtime.AppliedStep) (runtime.Token, error) {
	s.steps = append(s.steps, step)
	return s.Store.Create(ctx, step)
}
func (s *recordingStore) Commit(ctx context.Context, expected runtime.Token, step runtime.AppliedStep) (runtime.Token, error) {
	s.steps = append(s.steps, step)
	return s.Store.Commit(ctx, expected, step)
}

func TestSendTaskCommitsMessageOutboxEvent(t *testing.T) {
	// A definition: start -> sendTask("OrderPlaced") -> end. (Copy the exact builder
	// calls from the deleted message_sink_test.go; a SendTask auto-advances.)
	def := buildSendTaskDef(t) // adapt from message_sink_test.go
	store := &recordingStore{Store: runtime.NewMemStore()}
	r := runtime.NewRunner(action.NewCatalog(), store) // NO MessageSink — must not error
	_, err := r.Run(t.Context(), def, "i-1", map[string]any{"k": "v"})
	require.NoError(t, err)

	// Exactly one message.OrderPlaced event was committed in an AppliedStep.
	var msgEvents []runtime.OutboxEvent
	for _, step := range store.steps {
		for _, ev := range step.Events {
			if ev.Topic == "message.OrderPlaced" {
				msgEvents = append(msgEvents, ev)
			}
		}
	}
	require.Len(t, msgEvents, 1)
	assert.Equal(t, "i-1", msgEvents[0].InstanceID)
	assert.Equal(t, "OrderPlaced", msgEvents[0].Payload["messageName"])
}
```

Replace `buildSendTaskDef`, `action.NewCatalog()`, and `runtime.NewMemStore()` with the exact constructors the existing runtime tests use (read `message_sink_test.go` and a neighbor test for the catalog/store/def idioms).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run 'TestSendTaskCommitsMessageOutboxEvent' -v`
Expected: FAIL — currently `Run` errors with `no MessageSink configured` (the perform case still requires the sink), so no message event is committed.

- [ ] **Step 3: Wire the derivation into deliverLoop**

In `runtime/runner.go`, change the events-assembly line (currently `events := terminalOutboxEvent(prevStatus, st, res.Commands)`, ~line 455) to:

```go
events := terminalOutboxEvent(prevStatus, st, res.Commands)
events = append(events, outboundMessageEvents(st, res.Commands)...)
```

- [ ] **Step 4: Make the SendMessage perform case a no-op and remove the sink**

In `runtime/runner.go`, replace the entire `case engine.SendMessage:` block (~lines 919-931) with:

```go
	case engine.SendMessage:
		// Delivered transactionally as a message.<Name> outbox event in this step's
		// AppliedStep.Events (ADR-0067). Nothing to perform post-commit.
		return nil, nil
```

Remove the `msgSink MessageSink` field from the `Runner` struct, and delete the `WithMessageSink` option function. Then delete the two files:

```bash
git rm runtime/message_sink.go runtime/message_sink_test.go
```

- [ ] **Step 5: Sweep callers of the removed symbols**

Run: `go build ./...` then `go vet ./...`. Find every remaining use of `WithMessageSink`, `MessageSink`, or `OutboundMessage`:

```bash
grep -rn "WithMessageSink\|MessageSink\|OutboundMessage" --include="*.go" .
```

For each (examples, other tests, README example code compiled via Example tests): remove the `WithMessageSink(...)` option from the `NewRunner` call (delivery now flows through the outbox). If an example demonstrated SendTask via a sink, point it at the Task 4 `eventing.NewMessageHandler` pattern instead (or simply drop the sink wiring if the example does not assert delivery). Iterate `go build ./...` until clean.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./runtime/ -run 'TestSendTaskCommitsMessageOutboxEvent|TestOutboundMessageEvents' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(runtime): SendTask emits a transactional outbox event; retire MessageSink (ADR-0067)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `eventing.NewMessageHandler` reference subscriber + Example

**Files:**
- Create: `eventing/message.go`
- Test: `eventing/message_test.go`, `eventing/message_example_test.go`

**Interfaces:**
- Consumes: `github.com/ThreeDotsLabs/watermill/message` (`message.NoPublishHandlerFunc`, `*message.Message`); `Runner.DeliverMessage(ctx, def, name, correlationKey, payload)` (for the Example's closure).
- Produces: `type MessageDeliverFunc func(ctx context.Context, name, correlationKey string, payload map[string]any) error`; `func NewMessageHandler(deliver MessageDeliverFunc) message.NoPublishHandlerFunc`.

**Note (design):** the handler takes a `MessageDeliverFunc` closure (matching the existing `runtime.DeliverFunc`/SignalBus idiom) rather than a `Runner`+`DefinitionResolver`. The consumer's closure captures the receiver `*model.ProcessDefinition` and calls `Runner.DeliverMessage`. This keeps `eventing` free of definition-resolution policy and mirrors `NewChainHandler`.

- [ ] **Step 1: Write the failing unit test**

Add `eventing/message_test.go` (package `eventing_test`):

```go
func TestNewMessageHandlerRoutesToDeliver(t *testing.T) {
	var gotName, gotKey string
	var gotPayload map[string]any
	deliver := func(_ context.Context, name, key string, payload map[string]any) error {
		gotName, gotKey, gotPayload = name, key, payload
		return nil
	}
	h := eventing.NewMessageHandler(deliver)

	body, _ := json.Marshal(map[string]any{
		"messageName":    "OrderPlaced",
		"correlationKey": "ord-7",
		"variables":      map[string]any{"amount": float64(10)},
	})
	msg := message.NewMessage("dedup-1", body)
	msg.Metadata.Set("topic", "message.OrderPlaced")

	require.NoError(t, h(msg))
	assert.Equal(t, "OrderPlaced", gotName)
	assert.Equal(t, "ord-7", gotKey)
	assert.Equal(t, map[string]any{"amount": float64(10)}, gotPayload)
}

func TestNewMessageHandlerAcksMalformedPayload(t *testing.T) {
	called := false
	h := eventing.NewMessageHandler(func(context.Context, string, string, map[string]any) error {
		called = true
		return nil
	})
	msg := message.NewMessage("dedup-2", []byte("{not json"))
	msg.Metadata.Set("topic", "message.X")
	require.NoError(t, h(msg)) // malformed → ack, no loop
	assert.False(t, called, "deliver must not be called for a malformed payload")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./eventing/ -run 'TestNewMessageHandler' -v`
Expected: FAIL — `undefined: eventing.NewMessageHandler`.

- [ ] **Step 3: Implement the handler**

Create `eventing/message.go`:

```go
package eventing

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
)

// MessageDeliverFunc routes a decoded outbound SendTask message to its receiver. A
// consumer typically wraps [runtime.Runner.DeliverMessage] with the receiver definition
// pre-captured in the closure.
type MessageDeliverFunc func(ctx context.Context, name, correlationKey string, payload map[string]any) error

// messageBody is the wire shape of a message.<Name> outbox event payload (ADR-0067).
type messageBody struct {
	MessageName    string         `json:"messageName"`
	CorrelationKey string         `json:"correlationKey"`
	Variables      map[string]any `json:"variables"`
}

// NewMessageHandler adapts a message.* outbox subscription to a MessageDeliverFunc. A
// consumer mounts it on their own message.Router for the message topics they care about
// (their retry/poison/DLQ middleware wraps it). It decodes the payload and routes the
// message to deliver.
//
// Ack/Nack discipline (a returned error nacks for re-delivery):
//   - delivered (or no-op: no waiter) → nil (ack)
//   - malformed JSON / empty message name → nil (ack + log; never loop on poison)
//   - transient deliver failure → error (nack → re-delivered)
func NewMessageHandler(deliver MessageDeliverFunc) message.NoPublishHandlerFunc {
	logger := slog.Default()
	return func(msg *message.Message) error {
		var body messageBody
		if len(msg.Payload) > 0 {
			if err := json.Unmarshal(msg.Payload, &body); err != nil {
				logger.WarnContext(msg.Context(), "message: malformed payload; acking",
					slog.String("topic", msg.Metadata.Get("topic")),
					slog.String("instance_id", msg.Metadata.Get("instance_id")),
					slog.Any("error", err))
				return nil
			}
		}
		if body.MessageName == "" {
			return nil // not a decodable message event; ack and ignore
		}
		return deliver(msg.Context(), body.MessageName, body.CorrelationKey, body.Variables)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./eventing/ -run 'TestNewMessageHandler' -v`
Expected: PASS.

- [ ] **Step 5: Write a godoc Example (wiring demonstration)**

Add `eventing/message_example_test.go` (package `eventing_test`). This compiles and documents the end-to-end wiring; it need not assert via `// Output:` (the relay/subscribe loop is async). Show the closure capturing the receiver definition and runner:

```go
// ExampleNewMessageHandler shows wiring a message.* subscription to intra-engine delivery.
func ExampleNewMessageHandler() {
	// Given a runner and the receiver definition (the process that has a ReceiveTask):
	var runner *runtime.Runner
	var receiverDef *model.ProcessDefinition

	handler := eventing.NewMessageHandler(func(ctx context.Context, name, key string, payload map[string]any) error {
		// Route intra-engine: wake the instance parked on (name, key) in receiverDef.
		return runner.DeliverMessage(ctx, receiverDef, name, key, payload)
	})

	// Mount handler on your message.Router for the "message.<Name>" topics you consume,
	// subscribing the same broker the persistence.Relay publishes to.
	_ = handler
	// Output:
}
```

Adjust imports (`runtime`, `model`, `context`, `eventing`) so the file compiles. If `// Output:` with no output causes a failure, remove the `// Output:` line to make it a non-executed compile-only example.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./eventing/ -run 'Message' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 7: Commit**

```bash
git add eventing/message.go eventing/message_test.go eventing/message_example_test.go
git commit -m "feat(eventing): NewMessageHandler routes message.* outbox events to DeliverMessage (ADR-0067)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Docs — README + HANDOVER

**Files:**
- Modify: `README.md` (the SendTask / MessageSink section)
- Modify: `docs/plans/HANDOVER.md` (resume point)

**Interfaces:** none (documentation only).

- [ ] **Step 1: Update the README SendTask section**

In `README.md`, locate the SendTask / `MessageSink` / `WithMessageSink` documentation (search: `grep -n "MessageSink\|SendTask\|WithMessageSink" README.md`). Replace it with the transactional model. Use this content (adapt headings to the file's style):

```markdown
### SendTask delivery (transactional outbox)

A BPMN `SendTask` emits its outbound message as a `message.<MessageName>` event written
into the same `wrkflw_outbox` (and the same transaction) as the state commit, then relayed
at-least-once by the outbox relay — no `MessageSink` wiring, no stranding window (ADR-0067).

The event payload is `{"messageName", "correlationKey", "variables"}`, with `instance_id`
and `definition_ref` as message metadata. Consume it like any other outbox topic. To deliver
a message intra-engine (resume a parked `ReceiveTask`), mount `eventing.NewMessageHandler`
on your message router and route to `Runner.DeliverMessage`:

    handler := eventing.NewMessageHandler(func(ctx context.Context, name, key string, vars map[string]any) error {
        return runner.DeliverMessage(ctx, receiverDef, name, key, vars)
    })

`DeliverMessage`'s waiter index is in-memory per `Runner`, so intra-engine correlation works
within one process; for cross-process correlation, subscribe `message.*` in your own consumer.
```

If the README contained a `WithMessageSink(...)` code snippet, delete it (the symbol no longer exists).

- [ ] **Step 2: Refresh the HANDOVER resume point**

In `docs/plans/HANDOVER.md`, update the top `## ⏩ CURRENT RESUME POINT` block: note the transactional SendTask outbox is merged (ADR-0067, supersedes ADR-0060), `MessageSink`/`OutboundMessage`/`WithMessageSink` are GONE (advise against them), delivery is via `message.*` events + `eventing.NewMessageHandler`, and set the next free ADR to **0068**. Keep the existing block's structure; demote the prior (clock-refactor) block to "PRIOR RESUME POINT" as that block did to its predecessor.

- [ ] **Step 3: Commit**

```bash
git add README.md docs/plans/HANDOVER.md
git commit -m "docs: transactional SendTask outbox replaces MessageSink (ADR-0067)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Verification sweep

**Files:** none (verification only).

- [ ] **Step 1: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 2: Full race suite**

Run: `go test -race ./...`
Expected: all green (Docker daemon up for testcontainers-backed packages).

- [ ] **Step 3: Coverage on touched packages**

Run: `go test -coverprofile=cover.out ./runtime/... ./eventing/... && go tool cover -func=cover.out | tail -1`
Expected: ≥ 85% line coverage on `runtime` (the package carrying the new logic). `eventing` may be lower if its watermill wiring is integration-tested elsewhere; note the number.

- [ ] **Step 4: Lint + format**

Run: `golangci-lint run ./... && gofmt -l runtime eventing`
Expected: lint 0 issues; `gofmt -l` prints nothing.

- [ ] **Step 5: Confirm engine/model untouched and the sink is gone**

Run:
```bash
git diff --name-only <merge-base>..HEAD -- engine/ model/   # expect EMPTY
grep -rn "WithMessageSink\|\bMessageSink\b\|OutboundMessage" --include="*.go" . # expect EMPTY
```
Expected: no engine/model files; no remaining references to the retired symbols.

- [ ] **Step 6: Final commit (only if Step 4 reformatted anything)**

```bash
git add -A
git commit -m "chore(runtime,eventing): gofmt after transactional-outbox change

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-review notes

- **Spec coverage:** core mechanism (§2 of spec) → Tasks 2+3; Replace/removal (§3) → Task 3; reference delivery side (§4, `NewMessageHandler` + `DeliverMessage`) → Task 4; docs/ADR (§5) → Tasks 1+5; testing & verification (§5–6) → per-task tests + Task 6; scope boundaries (§6, engine/model zero-diff) → Task 6 Step 5. The spec's `NewMessageHandler(deliver, resolve)` is refined to a single `MessageDeliverFunc` closure (def captured by the consumer), documented in Task 4's Note — meets the spec intent (reachable intra-engine delivery) with the codebase's established closure idiom.
- **Type consistency:** payload keys `messageName`/`correlationKey`/`variables` and topic `message.<Name>` are identical in Task 2 (derivation) and Task 4 (decode). `outboundMessageEvents` signature is the same where defined (Task 2) and consumed (Task 3).
- **Placeholder scan:** the `buildSendTaskDef`/catalog/store identifiers in Task 3 Step 1 are explicitly flagged to copy from the existing (deleted) `message_sink_test.go` and neighbor tests — they are real existing idioms, not invented APIs. No TBD/TODO left.
