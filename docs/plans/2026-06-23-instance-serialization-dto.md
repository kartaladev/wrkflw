# Instance Serialization DTO (FOLLOWUPS ③) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide a stable, public, JSON-serializable view of a process instance — a **full snapshot** (status, variables, tokens, history, tasks, incidents) plus a **curated "actionable" view** (status + open human tasks with their allowed next actions) — mapped from the internal `engine.InstanceState`, never by marshalling the engine type directly.

**Architecture:** A transport-agnostic DTO layer in the `runtime` package (alongside the existing `InstanceSummary`/`InstancePage`/`InstanceLister` read-side types). Pure mapper functions take `engine.InstanceState` (+ the `model.ProcessDefinition` for the curated view's next-action derivation) and return DTOs with stable `json` tags. Enum→string conversion lives in the mapper, so the `engine` package stays untouched. A final task exposes the snapshot through the existing REST transport following its established handler pattern.

**Tech Stack:** Go 1.25, standard `encoding/json`. No new dependencies; no testcontainers.

## Global Constraints

- Go 1.25; single `go.mod`; module path `github.com/kartaladev/wrkflw`; flat root layout (ADR-0004).
- **TDD strict:** every new exported symbol gets a failing test first.
- The `engine` package MUST stay zero-diff (no `String()`/`MarshalJSON` added to `engine.Status`/`TokenState`). All enum→string mapping lives in the new `runtime` DTO code.
- Never expose engine bookkeeping in the wire contract: the snapshot includes only consumer-relevant fields (InstanceID, DefID, DefVersion, Status, Variables, Tokens, StartedAt, EndedAt, History, Tasks, Incidents). It MUST NOT include `Timers`, `ArmedEvents`, `Boundaries`, `Scopes`, `RootCompensations`, `ArchivedCompensations`, `EventSubprocesses`, `Compensating`, `PendingCancel`, or any `*Seq` counter.
- Error sentinels (none expected) use the `workflow-runtime:` prefix.
- Conventional Commits `feat(runtime)` / `feat(transport)`. Ask before committing.
- New ADR: **ADR-0043** (instance DTO/view contract), authored in Task 5.

## Source-of-truth field reference (from engine/state.go & humantask)

- `engine.InstanceState`: `InstanceID string`, `DefID string`, `DefVersion int`, `Status Status`, `Variables map[string]any`, `Tokens []Token`, `StartedAt time.Time`, `EndedAt *time.Time`, `History []NodeVisit`, `Tasks []humantask.HumanTask`, `Incidents []Incident`.
- `engine.Status` (int iota): `StatusRunning`=0→"running", `StatusCompleted`=1→"completed", `StatusFailed`=2→"failed", `StatusCompensating`=3→"compensating", `StatusTerminated`=4→"terminated". (No `String()` today; `transport/rest` has a private `statusString` we mirror.)
- `engine.Token`: `ID, NodeID, ScopeID string`, `State TokenState`, `AwaitCommand, AwaitSignal, AwaitMessage, AwaitMessageKey string`, `Payload map[string]any`, `EnteredAt time.Time`, `RetryAttempts int`.
- `engine.TokenState` (int iota): `TokenActive`=0→"active", `TokenWaitingCommand`=1→"waitingCommand", `TokenAtJoin`=2→"atJoin", `TokenIncident`=3→"incident".
- `engine.NodeVisit`: `NodeID, TokenID string`, `EnteredAt time.Time`, `LeftAt *time.Time`, `ActorID *string`.
- `engine.Incident`: `ID, TokenID, NodeID, ScopeID, CommandID, Error string`, `Attempts int`, `CreatedAt time.Time`.
- `humantask.HumanTask`: `TaskToken, InstanceID, NodeID string`, `Eligibility authz.AuthzSpec`, `Candidates []string`, `State TaskState`, `ClaimedBy string`, `CreatedAt time.Time`, `DueAt *time.Time`, `Vars map[string]any`. Method `IsOpen() bool` ⇔ `State == Unclaimed || State == Claimed`.
- `humantask.TaskState` (int iota): `Unclaimed`=0→"unclaimed", `Claimed`=1→"claimed", `Completed`=2→"completed", `Cancelled`=3→"cancelled".
- `model.ProcessDefinition.Outgoing(nodeID string) []SequenceFlow`; `SequenceFlow{ID, Source, Target, Condition string, IsDefault bool}`.

## File map

| Path | Action | Responsibility |
|---|---|---|
| `runtime/instance_snapshot.go` | Create | Full `InstanceSnapshot` DTO + `NewInstanceSnapshot` mapper + enum→string helpers. |
| `runtime/instance_actionable.go` | Create | Curated `ActionableView` DTO + `NewActionableView` mapper. |
| `runtime/instance_snapshot_test.go`, `runtime/instance_actionable_test.go` | Create | TDD coverage incl. a JSON-shape assertion and the "no engine bookkeeping leaks" check. |
| `transport/rest/snapshot.go` (+ test) | Create | REST handler exposing the snapshot + actionable view (follows `transport/rest/view.go`/`handler.go`). |
| `docs/adr/0043-instance-view-contract.md` | Create | ADR for the public DTO contract. |

---

### Task 1: Enum→string helpers + `InstanceSnapshot` token/visit/incident sub-views

**Files:** Create `runtime/instance_snapshot.go`, `runtime/instance_snapshot_test.go`.

**Interfaces:**
- Produces: `func StatusString(s engine.Status) string`, `func tokenStateString(engine.TokenState) string`, `func taskStateString(humantask.TaskState) string`; DTO types `TokenView`, `NodeVisitView`, `IncidentView`.

- [ ] **Step 1: Failing test for the enum mappers**

```go
package runtime_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
)

func TestStatusString(t *testing.T) {
	cases := map[engine.Status]string{
		engine.StatusRunning: "running", engine.StatusCompleted: "completed",
		engine.StatusFailed: "failed", engine.StatusCompensating: "compensating",
		engine.StatusTerminated: "terminated",
	}
	for in, want := range cases {
		if got := runtime.StatusString(in); got != want {
			t.Errorf("StatusString(%v) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2:** Run red: `go test ./runtime/ -run TestStatusString` → `undefined: runtime.StatusString`.
- [ ] **Step 3:** Implement `StatusString` (exported, mirrors `transport/rest` mapping with a `default:"unknown"`), `tokenStateString`, `taskStateString`, and the `TokenView`/`NodeVisitView`/`IncidentView` structs with `json` tags (snake_case to match existing `InstanceSummary` tags, e.g. `node_id`, `entered_at`, `left_at`, `created_at`).
- [ ] **Step 4:** Run green.
- [ ] **Step 5:** Commit `feat(runtime): enum string mappers and token/visit/incident views`.

---

### Task 2: `InstanceSnapshot` full DTO + mapper

**Files:** Modify `runtime/instance_snapshot.go`; extend `runtime/instance_snapshot_test.go`.

**Interfaces:**
- Produces: `type InstanceSnapshot struct {...}`; `func NewInstanceSnapshot(st engine.InstanceState, def *model.ProcessDefinition) InstanceSnapshot`.

- [ ] **Step 1: Failing test (incl. no-bookkeeping-leak guard)**

```go
func TestNewInstanceSnapshot(t *testing.T) {
	end := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "i1", DefID: "d1", DefVersion: 2, Status: engine.StatusCompleted,
		Variables: map[string]any{"amount": 10},
		Tokens:    []engine.Token{{ID: "t1", NodeID: "n1", State: engine.TokenActive}},
		History:   []engine.NodeVisit{{NodeID: "n1", TokenID: "t1"}},
		EndedAt:   &end,
	}
	snap := runtime.NewInstanceSnapshot(st, nil)
	if snap.InstanceID != "i1" || snap.Status != "completed" || snap.DefVersion != 2 {
		t.Fatalf("snap = %+v", snap)
	}
	if len(snap.Tokens) != 1 || snap.Tokens[0].State != "active" {
		t.Fatalf("tokens = %+v", snap.Tokens)
	}
	// no-leak guard: serialized JSON must not contain engine bookkeeping keys.
	b, _ := json.Marshal(snap)
	for _, banned := range []string{"armed", "boundaries", "scopes", "compensat", "Seq", "pendingCancel"} {
		if bytes.Contains(bytes.ToLower(b), bytes.ToLower([]byte(banned))) {
			t.Errorf("snapshot JSON leaks bookkeeping key %q: %s", banned, b)
		}
	}
}
```

- [ ] **Step 2:** Run red.
- [ ] **Step 3:** Implement `InstanceSnapshot` (fields: `InstanceID, DefID string`, `DefVersion int`, `Status string`, `Variables map[string]any`, `Tokens []TokenView`, `History []NodeVisitView`, `Tasks []TaskView`, `Incidents []IncidentView`, `StartedAt time.Time`, `EndedAt *time.Time` with `omitempty`) and `NewInstanceSnapshot` mapping each field, converting enums via the Task 1 helpers. `TaskView` here carries the task fields (TaskToken, NodeID, State string, ClaimedBy, Candidates, CreatedAt, DueAt). The `def` param is unused for the full snapshot (pass-through for signature symmetry with the actionable mapper) — accept `nil`.
- [ ] **Step 4:** Run green.
- [ ] **Step 5:** Commit `feat(runtime): InstanceSnapshot full DTO and mapper`.

---

### Task 3: `ActionableView` curated DTO + mapper

**Files:** Create `runtime/instance_actionable.go`, `runtime/instance_actionable_test.go`.

**Interfaces:**
- Produces: `type NextAction struct{ FlowID, Target, Condition string; IsDefault bool }`; `type ActionableTask struct{...}`; `type ActionableView struct{...}`; `func NewActionableView(st engine.InstanceState, def *model.ProcessDefinition) ActionableView`.

- [ ] **Step 1: Failing test**

```go
func TestNewActionableView(t *testing.T) {
	def, _ := model.NewDefinition("d1", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewUserTask("approve", []string{"manager"})).
		Add(model.NewEndEvent("e")).
		Connect("s", "approve").
		Connect("approve", "e", model.WithFlowID("go-e"), model.WithCondition("vars.ok")).
		Build()
	st := engine.InstanceState{
		InstanceID: "i1", Status: engine.StatusRunning,
		Tasks: []humantask.HumanTask{{TaskToken: "tk", NodeID: "approve", State: humantask.Unclaimed}},
	}
	v := runtime.NewActionableView(st, def)
	if v.Status != "running" || len(v.OpenTasks) != 1 {
		t.Fatalf("view = %+v", v)
	}
	ot := v.OpenTasks[0]
	if ot.NodeID != "approve" || ot.State != "unclaimed" {
		t.Fatalf("open task = %+v", ot)
	}
	if len(ot.AllowedActions) != 1 || ot.AllowedActions[0].FlowID != "go-e" || ot.AllowedActions[0].Condition != "vars.ok" {
		t.Fatalf("allowed actions = %+v", ot.AllowedActions)
	}
}
```

- [ ] **Step 2:** Run red (depends on `model.NewDefinition`/`NewUserTask`/`WithFlowID`/`WithCondition` from sub-project 2 — this plan executes AFTER sub-project 2 merges).
- [ ] **Step 3:** Implement: `ActionableView{ InstanceID string; Status string; OpenTasks []ActionableTask }`; `ActionableTask{ TaskToken, NodeID, State, ClaimedBy string; Candidates []string; AllowedActions []NextAction }`. `NewActionableView` iterates `st.Tasks`, keeps `t.IsOpen()`, and for each derives `AllowedActions` from `def.Outgoing(t.NodeID)` (map each `SequenceFlow`→`NextAction`). If `def == nil`, `AllowedActions` is empty (document this).
- [ ] **Step 4:** Run green.
- [ ] **Step 5:** Commit `feat(runtime): curated ActionableView with allowed next actions`.

---

### Task 4: Expose the snapshot through the REST transport

Make the contract reachable. Follow the existing pattern in `transport/rest/view.go` (DTO conversion) and `transport/rest/handler.go` (instance fetch via the service facade).

**Files:** Create `transport/rest/snapshot.go` + `transport/rest/snapshot_test.go`.

**Interfaces:**
- Consumes: the service facade's existing "get instance" method that returns `engine.InstanceState`, the `DefinitionRegistry`/store to fetch the `*model.ProcessDefinition`, and `runtime.NewInstanceSnapshot`/`NewActionableView`.

- [ ] **Step 1: Confirm handler wiring**

Read `transport/rest/handler.go` and `transport/rest/view.go` to confirm: (a) the constructor/router that registers instance routes, (b) how an existing handler obtains `engine.InstanceState` for an instance ID, (c) how a definition is resolved. Match those exact signatures.

- [ ] **Step 2: Failing handler test**

Write `transport/rest/snapshot_test.go` using `httptest` to hit `GET /instances/{id}/snapshot` and assert the JSON body has `instance_id` and `status` and does NOT contain `scopes`/`armed`. Mirror the test style already in `transport/rest`.

- [ ] **Step 3:** Run red.
- [ ] **Step 4:** Implement `snapshot.go`: two handlers — `/instances/{id}/snapshot` → `json.NewEncoder(w).Encode(runtime.NewInstanceSnapshot(state, def))` and `/instances/{id}/actionable` → `runtime.NewActionableView(state, def)`. Register them on the existing router constructor. Reuse the existing instance-fetch + definition-resolve helpers found in Step 1.
- [ ] **Step 5:** Run green: `go test ./transport/rest/ -run Snapshot -count=1`.
- [ ] **Step 6:** Commit `feat(transport): REST endpoints for instance snapshot and actionable view`.

> Note: gRPC exposure is intentionally deferred — the proto surface lives in `transport/grpc/proto`; adding a `GetInstanceSnapshot` RPC is a thin follow-up that mirrors this REST task. Logged here so the gap is explicit, not silent.

---

### Task 5: ADR-0043 + verification

**Files:** Create `docs/adr/0043-instance-view-contract.md`.

- [ ] **Step 1:** Write ADR-0043 (Nygard): Context (no `ProcessInstance` type; `engine.InstanceState` mixes consumer data with bookkeeping and unexported types; FOLLOWUPS ③; "complete information" must not leak engine internals into a wire contract). Decision (transport-agnostic DTOs in `runtime`; full `InstanceSnapshot` + curated `ActionableView`; pure mappers from `InstanceState` (+ definition); enums stringified in the mapper, engine left zero-diff; reachable via REST, gRPC deferred). Consequences (FE contract stable across engine refactors; mild duplication of the status-string mapping with `transport/rest` — flagged for later consolidation; `def` required for the actionable view's next-actions).
- [ ] **Step 2: Verify**

```bash
go test -race -coverprofile=cover.out ./runtime/... ./transport/... && go tool cover -func=cover.out | tail -1
go test ./... -count=1
golangci-lint run ./runtime/... ./transport/...
```
Expected: coverage ≥ 85% on touched packages; full suite green; lint clean.

- [ ] **Step 3:** Commit `docs(adr): instance view serialization contract (ADR-0043)`.

---

## Verification checklist (whole sub-project)

- [ ] `runtime.InstanceSnapshot` serializes all consumer-relevant instance data and **no** engine bookkeeping (the no-leak guard test passes).
- [ ] `runtime.ActionableView` lists only open tasks and, for each, the allowed next actions derived from the definition's outgoing flows.
- [ ] `engine` package is unchanged (no `String()`/`MarshalJSON` added there).
- [ ] REST endpoints `/instances/{id}/snapshot` and `/instances/{id}/actionable` return the DTOs.
- [ ] ADR-0043 recorded; gRPC deferral noted.
- [ ] `go test ./... -count=1` green; lint clean; touched-package coverage ≥ 85%.

## Dependency note

This sub-project consumes `model.NewDefinition`/constructors/flow-options from sub-project 2 (Node redesign) in its tests and the actionable mapper. Execute it AFTER sub-project 2 merges.

## Out of scope

- gRPC snapshot RPC (deferred, noted in Task 4).
- Consolidating the duplicated status-string mapping with `transport/rest` (flagged in ADR-0043).
- Layout hygiene (1), Node redesign (2), docs (4).
