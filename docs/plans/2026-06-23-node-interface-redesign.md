# Node Interface Redesign + Builder + YAML Loader (FOLLOWUPS ②) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the flat 35-field `model.Node` struct with a `Node` **interface** plus one concrete type per `NodeKind`, each with a constructor; add a fluent `DefinitionBuilder` and a YAML loader; preserve JSONB persistence round-trips via an unexported flat wire form.

**Architecture:** `Node` becomes an interface (`Kind() NodeKind`, `ID() string`, `Name() string`). Each kind is a concrete struct embedding a shared `baseNode` (id/name) and carrying **the same exported field names the flat struct used**, so the engine migration is a type-switch wrap rather than a field rename. `ProcessDefinition.Nodes` becomes `[]Node`. JSON (un)marshalling is centralized through an unexported flat `nodeWire` struct (identical shape to today's `Node`), so previously stored JSONB definitions still deserialize unchanged. All ~108 node-access sites are confined to `engine/step.go`.

**Tech Stack:** Go 1.25, `github.com/expr-lang/expr` (via the `expreval` wrapper), `gopkg.in/yaml.v3` for the loader (new dependency — see Global Constraints). Tests are standard `go test`; no testcontainers needed for this sub-project.

## Global Constraints

- Go 1.25; single `go.mod`; module path `github.com/kartaladev/wrkflw`.
- No `pkg/` prefix; `model`, `engine` stay at the module root (ADR-0004).
- **TDD strict** (CLAUDE.md): for every new exported symbol, write the failing test, run it red, implement, run it green, commit. The model-layer tasks (1, 3, 4, 5, 6) are pure TDD. The engine-migration task (Task 2) is a behavior-preserving refactor whose gate is the **existing** `engine` test suite staying green; no new behavior is added there.
- **Red window disclosure:** an interface replacement is an atomic type change. After Task 1 lands, `model` compiles and its tests pass, but `engine/step.go` will NOT compile until Task 2 completes. Tasks 1 and 2 therefore form one continuous red window for the repo-wide build — do them back-to-back and do not merge to `main` between them. Every OTHER task boundary is fully green (`go build ./...` + `go test ./...`).
- Error sentinels use the `workflow-<package>:` prefix (e.g. `workflow-model:`). Reuse the existing sentinels in `model/validate.go`; do not rename them.
- `NodeKind` already marshals to lowerCamelCase strings (`model/nodekind_json.go`): `startEvent`, `serviceTask`, `userTask`, `receiveTask`, `sendTask`, `businessRuleTask`, `subProcess`, `callActivity`, `eventSubProcess`, `intermediateCatchEvent`, `intermediateThrowEvent`, `boundaryEvent`, `exclusiveGateway`, `parallelGateway`, `inclusiveGateway`, `eventBasedGateway`, `startEvent`, `endEvent`, `terminateEndEvent`, `errorEndEvent`. Reuse this as the JSON `kind` discriminator.
- Conventional Commits scoped `feat(model)`, `refactor(engine)`, `feat(model)` for builder/yaml. Ask before committing (executor handles approval).
- New ADR required: **ADR-0042** (Node-as-interface model). Author it in Task 7.
- A new third-party dependency (`gopkg.in/yaml.v3`) is introduced in Task 6. Per CLAUDE.md the tech stack is locked, but YAML authoring is an explicit project goal; record the dependency choice in ADR-0042's consequences (or a short note) — `yaml.v3` is the de-facto standard and already transitively present in most Go trees.

## The per-kind field map (authoritative for Tasks 1 & 3)

Each concrete type embeds `baseNode` (providing `ID()`/`Name()`) and declares `Kind()` returning its constant. Exported fields below keep the **exact names** from today's flat `Node` so `engine/step.go` reads are unchanged after the type-switch. Field set derived from the field doc comments in `model/definition.go` and the rules in `model/validate.go`.

| Concrete type | Kind constant | Exported kind-specific fields (same names as flat struct) |
|---|---|---|
| `StartEvent` | `KindStartEvent` | — |
| `EndEvent` | `KindEndEvent` | — |
| `TerminateEndEvent` | `KindTerminateEndEvent` | — |
| `ErrorEndEvent` | `KindErrorEndEvent` | `ErrorCode string` |
| `ServiceTask` | `KindServiceTask` | `Action string`; `RetryPolicy *RetryPolicy`; `RecoveryFlow string`; `CompensationAction string`; `CancelHandler string`; `SLADuration, SLAFlow, SLAAction string`; `ReminderEvery, ReminderAction string` |
| `UserTask` | `KindUserTask` | `CandidateRoles []string`; `EligibilityExpr string`; `RetryPolicy *RetryPolicy`; `RecoveryFlow string`; `CompensationAction string`; `CancelHandler string`; `SLADuration, SLAFlow, SLAAction string`; `ReminderEvery, ReminderAction string` |
| `ReceiveTask` | `KindReceiveTask` | `MessageName, CorrelationKey string`; plus the activity set (`RetryPolicy`, `RecoveryFlow`, `CompensationAction`, `CancelHandler`, `SLA*`, `Reminder*`) |
| `SendTask` | `KindSendTask` | `MessageName string`; plus the activity set |
| `BusinessRuleTask` | `KindBusinessRuleTask` | `Action string`; plus the activity set |
| `SubProcess` | `KindSubProcess` | `Subprocess *ProcessDefinition`; plus the activity set |
| `CallActivity` | `KindCallActivity` | `DefRef string`; plus the activity set |
| `EventSubProcess` | `KindEventSubProcess` | `Subprocess *ProcessDefinition` |
| `IntermediateCatchEvent` | `KindIntermediateCatchEvent` | `TimerDuration, SignalName, MessageName, CorrelationKey string`; `SLADuration, SLAFlow, SLAAction string`; `ReminderEvery, ReminderAction string` |
| `IntermediateThrowEvent` | `KindIntermediateThrowEvent` | `SignalName, CompensateRef string` |
| `BoundaryEvent` | `KindBoundaryEvent` | `AttachedTo string`; `NonInterrupting bool`; `ErrorCode string`; `SignalName, MessageName, CorrelationKey string`; `TimerDuration string` |
| `ExclusiveGateway` | `KindExclusiveGateway` | — |
| `ParallelGateway` | `KindParallelGateway` | — |
| `InclusiveGateway` | `KindInclusiveGateway` | — |
| `EventBasedGateway` | `KindEventBasedGateway` | — |

"The activity set" = `RetryPolicy *RetryPolicy`, `RecoveryFlow string`, `CompensationAction string`, `CancelHandler string`, `SLADuration/SLAFlow/SLAAction string`, `ReminderEvery/ReminderAction string`.

## File map

| Path | Action | Responsibility |
|---|---|---|
| `model/node.go` | Create | `Node` interface, `baseNode`, the 19 concrete types, `Kind()` methods. |
| `model/node_constructors.go` | Create | One constructor per kind + functional-option types. |
| `model/node_wire.go` | Create | Unexported flat `nodeWire`; `toWire`/`fromWire`; `ProcessDefinition` (Un)MarshalJSON. |
| `model/definition.go` | Modify | `ProcessDefinition.Nodes` → `[]Node`; `Node(id)`/`StartNodes()` return `Node`; drop the flat `Node` struct. |
| `model/validate.go` | Modify | Field accesses → type-asserts; per-kind rules become `validate()` methods or typed asserts. |
| `model/*_test.go` | Modify | Replace `Node{...}` literals with constructors (~75 literals across 4 files). |
| `model/builder.go` | Create | `DefinitionBuilder` fluent API. |
| `model/yaml.go` | Create | YAML → `ProcessDefinition` loader. |
| `engine/step.go` | Modify | ~108 node-access sites → type-switch/assert; 7 helper signatures → `Node` interface. |
| `docs/adr/0042-node-interface-model.md` | Create | ADR for the interface model. |

---

### Task 1: The `Node` interface, concrete types, constructors, validation, and discriminated JSON (model layer)

This task replaces the flat struct. It is large but self-contained to `model`; its gate is `go test ./model/...` green. The repo-wide build is red until Task 2.

**Files:**
- Create: `model/node.go`, `model/node_constructors.go`, `model/node_wire.go`
- Modify: `model/definition.go`, `model/validate.go`, and the 4 model test files.

**Interfaces:**
- Produces:
  - `type Node interface { Kind() NodeKind; ID() string; Name() string }`
  - 19 concrete value types (see field map) each with value-receiver methods.
  - Constructors returning `Node`: `NewStartEvent(id string, opts ...StartEventOption) Node`, …, `NewServiceTask(id, action string, opts ...ServiceTaskOption) Node`, etc.
  - `ProcessDefinition.Nodes []Node`; `(*ProcessDefinition).Node(id string) (Node, bool)`; `(*ProcessDefinition).StartNodes() []Node`.
  - `MarshalJSON`/`UnmarshalJSON` on `*ProcessDefinition` round-tripping through `nodeWire`.

- [ ] **Step 1: Write the failing test for the interface + a representative constructor**

Create `model/node_test.go`:

```go
package model_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/model"
)

func TestServiceTaskConstructorAndAccessors(t *testing.T) {
	n := model.NewServiceTask("pay", "charge-card",
		model.WithCompensation("refund-card"),
		model.WithRecoveryFlow("to-manual"),
	)
	if n.Kind() != model.KindServiceTask {
		t.Fatalf("Kind() = %v, want KindServiceTask", n.Kind())
	}
	if n.ID() != "pay" {
		t.Fatalf("ID() = %q, want pay", n.ID())
	}
	st, ok := n.(model.ServiceTask)
	if !ok {
		t.Fatalf("node is %T, want model.ServiceTask", n)
	}
	if st.Action != "charge-card" || st.CompensationAction != "refund-card" || st.RecoveryFlow != "to-manual" {
		t.Fatalf("fields = %+v", st)
	}
}
```

- [ ] **Step 2: Run it red**

Run: `go test ./model/ -run TestServiceTaskConstructorAndAccessors`
Expected: FAIL — `undefined: model.NewServiceTask` (and the type/consts referenced).

- [ ] **Step 3: Write `model/node.go` — interface, base, concrete types**

```go
package model

// Node is a single point in a process: an event, activity, or gateway.
// Concrete types (one per NodeKind) carry only the fields meaningful to their
// kind. Construct nodes with the New* constructors; build definitions with
// DefinitionBuilder or the YAML loader.
type Node interface {
	Kind() NodeKind
	ID() string
	Name() string
}

// baseNode supplies the identity common to every node kind.
type baseNode struct {
	id   string
	name string
}

func (b baseNode) ID() string   { return b.id }
func (b baseNode) Name() string { return b.name }

// --- events ---

type StartEvent struct{ baseNode }

func (StartEvent) Kind() NodeKind { return KindStartEvent }

type EndEvent struct{ baseNode }

func (EndEvent) Kind() NodeKind { return KindEndEvent }

type TerminateEndEvent struct{ baseNode }

func (TerminateEndEvent) Kind() NodeKind { return KindTerminateEndEvent }

type ErrorEndEvent struct {
	baseNode
	ErrorCode string
}

func (ErrorEndEvent) Kind() NodeKind { return KindErrorEndEvent }

// --- activities ---
// activityFields holds the cross-cutting fields every activity kind shares
// (retry, recovery, compensation, cancel, SLA, reminder). Embedded into each
// activity type so the engine reads e.g. node.SLADuration with no kind prefix.
type activityFields struct {
	RetryPolicy        *RetryPolicy
	RecoveryFlow       string
	CompensationAction string
	CancelHandler      string
	SLADuration        string
	SLAFlow            string
	SLAAction          string
	ReminderEvery      string
	ReminderAction     string
}

type ServiceTask struct {
	baseNode
	activityFields
	Action string
}

func (ServiceTask) Kind() NodeKind { return KindServiceTask }

type UserTask struct {
	baseNode
	activityFields
	CandidateRoles  []string
	EligibilityExpr string
}

func (UserTask) Kind() NodeKind { return KindUserTask }

type ReceiveTask struct {
	baseNode
	activityFields
	MessageName    string
	CorrelationKey string
}

func (ReceiveTask) Kind() NodeKind { return KindReceiveTask }

type SendTask struct {
	baseNode
	activityFields
	MessageName string
}

func (SendTask) Kind() NodeKind { return KindSendTask }

type BusinessRuleTask struct {
	baseNode
	activityFields
	Action string
}

func (BusinessRuleTask) Kind() NodeKind { return KindBusinessRuleTask }

type SubProcess struct {
	baseNode
	activityFields
	Subprocess *ProcessDefinition
}

func (SubProcess) Kind() NodeKind { return KindSubProcess }

type CallActivity struct {
	baseNode
	activityFields
	DefRef string
}

func (CallActivity) Kind() NodeKind { return KindCallActivity }

type EventSubProcess struct {
	baseNode
	Subprocess *ProcessDefinition
}

func (EventSubProcess) Kind() NodeKind { return KindEventSubProcess }

// --- intermediate / boundary events ---

type IntermediateCatchEvent struct {
	baseNode
	TimerDuration  string
	SignalName     string
	MessageName    string
	CorrelationKey string
	SLADuration    string
	SLAFlow        string
	SLAAction      string
	ReminderEvery  string
	ReminderAction string
}

func (IntermediateCatchEvent) Kind() NodeKind { return KindIntermediateCatchEvent }

type IntermediateThrowEvent struct {
	baseNode
	SignalName    string
	CompensateRef string
}

func (IntermediateThrowEvent) Kind() NodeKind { return KindIntermediateThrowEvent }

type BoundaryEvent struct {
	baseNode
	AttachedTo      string
	NonInterrupting bool
	ErrorCode       string
	SignalName      string
	MessageName     string
	CorrelationKey  string
	TimerDuration   string
}

func (BoundaryEvent) Kind() NodeKind { return KindBoundaryEvent }

// --- gateways ---

type ExclusiveGateway struct{ baseNode }

func (ExclusiveGateway) Kind() NodeKind { return KindExclusiveGateway }

type ParallelGateway struct{ baseNode }

func (ParallelGateway) Kind() NodeKind { return KindParallelGateway }

type InclusiveGateway struct{ baseNode }

func (InclusiveGateway) Kind() NodeKind { return KindInclusiveGateway }

type EventBasedGateway struct{ baseNode }

func (EventBasedGateway) Kind() NodeKind { return KindEventBasedGateway }
```

- [ ] **Step 4: Write `model/node_constructors.go` — constructors + options**

Use the functional-options pattern. Below is the full pattern for `ServiceTask` and the option helpers shared across activities; replicate the same shape for every other kind (constructors with required positional args from the field map, options for the rest). Shared activity options operate on `*activityFields`.

```go
package model

// --- shared activity options ---

type activityOption func(*activityFields)

func WithRetryPolicy(p *RetryPolicy) activityOption {
	return func(a *activityFields) { a.RetryPolicy = p }
}
func WithRecoveryFlow(flowID string) activityOption {
	return func(a *activityFields) { a.RecoveryFlow = flowID }
}
func WithCompensation(action string) activityOption {
	return func(a *activityFields) { a.CompensationAction = action }
}
func WithCancelHandler(action string) activityOption {
	return func(a *activityFields) { a.CancelHandler = action }
}
func WithSLA(duration, flowID, action string) activityOption {
	return func(a *activityFields) { a.SLADuration, a.SLAFlow, a.SLAAction = duration, flowID, action }
}
func WithReminder(every, action string) activityOption {
	return func(a *activityFields) { a.ReminderEvery, a.ReminderAction = every, action }
}

// --- events ---

func NewStartEvent(id string, name ...string) Node { return StartEvent{baseNode{id, optName(name)}} }
func NewEndEvent(id string, name ...string) Node   { return EndEvent{baseNode{id, optName(name)}} }
func NewTerminateEndEvent(id string, name ...string) Node {
	return TerminateEndEvent{baseNode{id, optName(name)}}
}
func NewErrorEndEvent(id, errorCode string, name ...string) Node {
	return ErrorEndEvent{baseNode{id, optName(name)}, errorCode}
}

// optName returns the first variadic name or "".
func optName(name []string) string {
	if len(name) > 0 {
		return name[0]
	}
	return ""
}

// --- service task (representative activity constructor) ---

func NewServiceTask(id, action string, opts ...activityOption) Node {
	st := ServiceTask{baseNode: baseNode{id: id}, Action: action}
	for _, o := range opts {
		o(&st.activityFields)
	}
	return st
}
```

For the remaining activity kinds (`UserTask`, `ReceiveTask`, `SendTask`, `BusinessRuleTask`, `SubProcess`, `CallActivity`) write analogous constructors: required positional args per the field map (`NewUserTask(id string, roles []string, opts ...activityOption)`, `NewReceiveTask(id, messageName string, opts ...activityOption)`, `NewSendTask(id, messageName string, opts ...activityOption)`, `NewBusinessRuleTask(id, action string, opts ...activityOption)`, `NewSubProcess(id string, sub *ProcessDefinition, opts ...activityOption)`, `NewCallActivity(id, defRef string, opts ...activityOption)`). For `EventSubProcess`: `NewEventSubProcess(id string, sub *ProcessDefinition) Node`. For the catch/throw/boundary and gateways write constructors taking their specific fields (`NewIntermediateCatchEvent` with separate `WithTimer/WithSignal/WithMessage` catch-options; `NewIntermediateThrowEvent`; `NewBoundaryEvent(id, attachedTo string, opts ...boundaryOption)`; `NewExclusiveGateway(id, name...)`, etc.). Use a `name ...string` trailing variadic for nodes whose only optional field is the name.

- [ ] **Step 5: Write `model/node_wire.go` — flat wire form + ProcessDefinition (Un)MarshalJSON**

`nodeWire` is the unexported flat struct (identical field set + JSON tags to today's `Node`), so existing JSONB definitions deserialize unchanged. `toWire` flattens a `Node`; `fromWire` reconstructs the concrete type by `Kind`.

```go
package model

import (
	"encoding/json"
	"fmt"
)

// nodeWire is the flat JSON/JSONB representation of any node. It is the single
// serialization shape; previously stored definitions decode through it
// unchanged. Field names/order mirror the pre-interface Node struct.
type nodeWire struct {
	ID                 string             `json:"id"`
	Kind               NodeKind           `json:"kind"`
	Name               string             `json:"name,omitempty"`
	Action             string             `json:"action,omitempty"`
	CandidateRoles     []string           `json:"candidateRoles,omitempty"`
	EligibilityExpr    string             `json:"eligibilityExpr,omitempty"`
	TimerDuration      string             `json:"timerDuration,omitempty"`
	SLADuration        string             `json:"slaDuration,omitempty"`
	SLAFlow            string             `json:"slaFlow,omitempty"`
	SLAAction          string             `json:"slaAction,omitempty"`
	ReminderEvery      string             `json:"reminderEvery,omitempty"`
	ReminderAction     string             `json:"reminderAction,omitempty"`
	RetryPolicy        *RetryPolicy       `json:"retryPolicy,omitempty"`
	RecoveryFlow       string             `json:"recoveryFlow,omitempty"`
	CompensationAction string             `json:"compensationAction,omitempty"`
	CompensateRef      string             `json:"compensateRef,omitempty"`
	CancelHandler      string             `json:"cancelHandler,omitempty"`
	SignalName         string             `json:"signalName,omitempty"`
	MessageName        string             `json:"messageName,omitempty"`
	CorrelationKey     string             `json:"correlationKey,omitempty"`
	ErrorCode          string             `json:"errorCode,omitempty"`
	AttachedTo         string             `json:"attachedTo,omitempty"`
	NonInterrupting    bool               `json:"nonInterrupting,omitempty"`
	Subprocess         *ProcessDefinition `json:"subprocess,omitempty"`
	DefRef             string             `json:"defRef,omitempty"`
}

// toWire flattens a Node into its wire form.
func toWire(n Node) nodeWire {
	w := nodeWire{ID: n.ID(), Kind: n.Kind(), Name: n.Name()}
	switch v := n.(type) {
	case ErrorEndEvent:
		w.ErrorCode = v.ErrorCode
	case ServiceTask:
		w.Action = v.Action
		applyActivityWire(&w, v.activityFields)
	case UserTask:
		w.CandidateRoles, w.EligibilityExpr = v.CandidateRoles, v.EligibilityExpr
		applyActivityWire(&w, v.activityFields)
	case ReceiveTask:
		w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
		applyActivityWire(&w, v.activityFields)
	case SendTask:
		w.MessageName = v.MessageName
		applyActivityWire(&w, v.activityFields)
	case BusinessRuleTask:
		w.Action = v.Action
		applyActivityWire(&w, v.activityFields)
	case SubProcess:
		w.Subprocess = v.Subprocess
		applyActivityWire(&w, v.activityFields)
	case CallActivity:
		w.DefRef = v.DefRef
		applyActivityWire(&w, v.activityFields)
	case EventSubProcess:
		w.Subprocess = v.Subprocess
	case IntermediateCatchEvent:
		w.TimerDuration, w.SignalName, w.MessageName, w.CorrelationKey = v.TimerDuration, v.SignalName, v.MessageName, v.CorrelationKey
		w.SLADuration, w.SLAFlow, w.SLAAction = v.SLADuration, v.SLAFlow, v.SLAAction
		w.ReminderEvery, w.ReminderAction = v.ReminderEvery, v.ReminderAction
	case IntermediateThrowEvent:
		w.SignalName, w.CompensateRef = v.SignalName, v.CompensateRef
	case BoundaryEvent:
		w.AttachedTo, w.NonInterrupting, w.ErrorCode = v.AttachedTo, v.NonInterrupting, v.ErrorCode
		w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
	}
	return w
}

func applyActivityWire(w *nodeWire, a activityFields) {
	w.RetryPolicy, w.RecoveryFlow = a.RetryPolicy, a.RecoveryFlow
	w.CompensationAction, w.CancelHandler = a.CompensationAction, a.CancelHandler
	w.SLADuration, w.SLAFlow, w.SLAAction = a.SLADuration, a.SLAFlow, a.SLAAction
	w.ReminderEvery, w.ReminderAction = a.ReminderEvery, a.ReminderAction
}

func (w nodeWire) activity() activityFields {
	return activityFields{
		RetryPolicy: w.RetryPolicy, RecoveryFlow: w.RecoveryFlow,
		CompensationAction: w.CompensationAction, CancelHandler: w.CancelHandler,
		SLADuration: w.SLADuration, SLAFlow: w.SLAFlow, SLAAction: w.SLAAction,
		ReminderEvery: w.ReminderEvery, ReminderAction: w.ReminderAction,
	}
}

// fromWire reconstructs the concrete Node for w.Kind.
func fromWire(w nodeWire) (Node, error) {
	b := baseNode{id: w.ID, name: w.Name}
	switch w.Kind {
	case KindStartEvent:
		return StartEvent{b}, nil
	case KindEndEvent:
		return EndEvent{b}, nil
	case KindTerminateEndEvent:
		return TerminateEndEvent{b}, nil
	case KindErrorEndEvent:
		return ErrorEndEvent{b, w.ErrorCode}, nil
	case KindServiceTask:
		return ServiceTask{baseNode: b, activityFields: w.activity(), Action: w.Action}, nil
	case KindUserTask:
		return UserTask{baseNode: b, activityFields: w.activity(), CandidateRoles: w.CandidateRoles, EligibilityExpr: w.EligibilityExpr}, nil
	case KindReceiveTask:
		return ReceiveTask{baseNode: b, activityFields: w.activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}, nil
	case KindSendTask:
		return SendTask{baseNode: b, activityFields: w.activity(), MessageName: w.MessageName}, nil
	case KindBusinessRuleTask:
		return BusinessRuleTask{baseNode: b, activityFields: w.activity(), Action: w.Action}, nil
	case KindSubProcess:
		return SubProcess{baseNode: b, activityFields: w.activity(), Subprocess: w.Subprocess}, nil
	case KindCallActivity:
		return CallActivity{baseNode: b, activityFields: w.activity(), DefRef: w.DefRef}, nil
	case KindEventSubProcess:
		return EventSubProcess{baseNode: b, Subprocess: w.Subprocess}, nil
	case KindIntermediateCatchEvent:
		return IntermediateCatchEvent{baseNode: b, TimerDuration: w.TimerDuration, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, SLADuration: w.SLADuration, SLAFlow: w.SLAFlow, SLAAction: w.SLAAction, ReminderEvery: w.ReminderEvery, ReminderAction: w.ReminderAction}, nil
	case KindIntermediateThrowEvent:
		return IntermediateThrowEvent{baseNode: b, SignalName: w.SignalName, CompensateRef: w.CompensateRef}, nil
	case KindBoundaryEvent:
		return BoundaryEvent{baseNode: b, AttachedTo: w.AttachedTo, NonInterrupting: w.NonInterrupting, ErrorCode: w.ErrorCode, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}, nil
	case KindExclusiveGateway:
		return ExclusiveGateway{b}, nil
	case KindParallelGateway:
		return ParallelGateway{b}, nil
	case KindInclusiveGateway:
		return InclusiveGateway{b}, nil
	case KindEventBasedGateway:
		return EventBasedGateway{b}, nil
	default:
		return nil, fmt.Errorf("workflow-model: unknown node kind %q", w.Kind)
	}
}

// definitionWire mirrors ProcessDefinition with Nodes as wire forms.
type definitionWire struct {
	ID            string         `json:"id"`
	Version       int            `json:"version"`
	Nodes         []nodeWire     `json:"nodes"`
	Flows         []SequenceFlow `json:"flows"`
	CancelActions []string       `json:"cancelActions,omitempty"`
}

func (d ProcessDefinition) MarshalJSON() ([]byte, error) {
	dw := definitionWire{ID: d.ID, Version: d.Version, Flows: d.Flows, CancelActions: d.CancelActions}
	dw.Nodes = make([]nodeWire, len(d.Nodes))
	for i, n := range d.Nodes {
		dw.Nodes[i] = toWire(n)
	}
	return json.Marshal(dw)
}

func (d *ProcessDefinition) UnmarshalJSON(data []byte) error {
	var dw definitionWire
	if err := json.Unmarshal(data, &dw); err != nil {
		return err
	}
	d.ID, d.Version, d.Flows, d.CancelActions = dw.ID, dw.Version, dw.Flows, dw.CancelActions
	d.Nodes = make([]Node, len(dw.Nodes))
	for i, w := range dw.Nodes {
		n, err := fromWire(w)
		if err != nil {
			return err
		}
		d.Nodes[i] = n
	}
	return nil
}
```

- [ ] **Step 6: Edit `model/definition.go` — flip Nodes to `[]Node`, drop the struct**

Remove the flat `Node` struct (lines 31–168). Change `ProcessDefinition.Nodes` to `[]Node`. Update the helpers:

```go
func (d *ProcessDefinition) Node(id string) (Node, bool) {
	for _, n := range d.Nodes {
		if n.ID() == id {
			return n, true
		}
	}
	return nil, false
}

func (d *ProcessDefinition) StartNodes() []Node {
	var starts []Node
	for _, n := range d.Nodes {
		if n.Kind() == KindStartEvent {
			starts = append(starts, n)
		}
	}
	return starts
}
```
Keep `Outgoing`/`Incoming` unchanged (they read `SequenceFlow`, not `Node`). Keep `NodeKind`, `SequenceFlow`, `ProcessDefinition` (minus Nodes type) intact.

- [ ] **Step 7: Edit `model/validate.go` — type-assert field accesses**

Convert the kind-specific reads to type switches/asserts. Pattern for the boundary, subprocess, call-activity, retry, recovery, and compensate-ref rules: replace `n.Field` (where `n` was the flat struct) with a type assertion on the concrete kind. For example the subprocess rule:

```go
// was: if (n.Kind == KindSubProcess || n.Kind == KindEventSubProcess) && n.Subprocess == nil { ... }
switch v := n.(type) {
case SubProcess:
	if v.Subprocess == nil {
		return fmt.Errorf("%w: node %q", ErrMissingSubprocess, n.ID())
	}
case EventSubProcess:
	if v.Subprocess == nil {
		return fmt.Errorf("%w: node %q", ErrMissingSubprocess, n.ID())
	}
case CallActivity:
	if v.DefRef == "" {
		return fmt.Errorf("%w: node %q", ErrMissingDefRef, n.ID())
	}
}
```
Apply the same mechanical conversion to every other field-reading rule in `validate.go` enumerated by the model agent (boundary `AttachedTo`/error-host check; `RetryPolicy` validity; `RecoveryFlow` from-node; `CompensateRef` existence; the catch-event `TimerDuration/SignalName/MessageName` discrimination in the boundary error check at line 295). The graph-level rules (one start event, reachability, dead-ends, flow endpoints, gateway split/join, event-gateway targets, parallel-join pairing) read only `n.Kind()`/`n.ID()` — change `n.Kind`→`n.Kind()` and `n.ID`→`n.ID()` and `n.AttachedTo`→assert to `BoundaryEvent`. Keep every existing sentinel error variable and message unchanged.

- [ ] **Step 8: Migrate the model test files to constructors**

In `model/definition_test.go`, `model/validate_test.go`, `model/nodekind_json_test.go`, replace every `model.Node{ID: "x", Kind: model.KindServiceTask, Action: "a"}` literal with the matching constructor `model.NewServiceTask("x", "a")`, etc. Update field-read assertions to type-assert the concrete type (as in Step 1's test). The `retry_test.go` file has no Node literals — leave it. Update the `linearDef()` / `validSubprocessDef()` test helpers to build `[]model.Node` via constructors.

- [ ] **Step 9: Run the model suite green**

Run: `go test ./model/ -count=1`
Expected: PASS (all migrated tests + the new node_test). Add a round-trip test asserting `json.Unmarshal(json.Marshal(def))` reproduces an equal definition, and a backward-compat test that a hand-written flat JSON (old shape) unmarshals into the right concrete types.

- [ ] **Step 10: Commit (build still red elsewhere — expected)**

```bash
git add model/
git commit -m "feat(model): replace flat Node struct with Node interface + concrete types (ADR-0042)

Repo-wide build is intentionally red until the engine migration (next task):
ProcessDefinition.Nodes is now []Node and engine/step.go has not been migrated."
```

---

### Task 2: Migrate `engine/step.go` to the `Node` interface

Behavior-preserving refactor of the **only** file that reads node fields. Gate: the existing `engine` test suite stays green. No new behavior, no new tests (per CLAUDE.md's pure-refactor carve-out) beyond compile fixes.

**Files:**
- Modify: `engine/step.go` (~108 sites, enumerated below).

**Interfaces:**
- Consumes: the `model.Node` interface + concrete types from Task 1.
- Produces: a compiling, green `engine` package; no exported-signature changes to engine.

- [ ] **Step 1: Confirm the starting red state**

Run: `go build ./engine/`
Expected: FAIL with many `node.Kind undefined (type model.Node has no field or method Kind)` / `node.Action undefined` errors — this is the migration surface.

- [ ] **Step 2: Convert the main drive switch (step.go:778–1370)**

Change the retrieval + switch from field/value to interface/type-switch:

```go
// was: node, ok := tdef.Node(tok.NodeID); ... switch node.Kind {
node, ok := tdef.Node(tok.NodeID)
if !ok {
	tok.State = TokenWaitingCommand
	continue
}
switch n := node.(type) {
case model.StartEvent:
	// ... (uses only n.ID() / routing)
case model.ServiceTask:
	cmds = append(cmds, InvokeAction{CommandID: cmdID, Name: n.Action, Input: serviceActionInput(s, n)})
	bndCmds, err := armBoundaries(tdef, s, tok.ID, n.ID(), at)
	// ...
case model.UserTask:
	spec := authz.AuthzSpec{Roles: n.CandidateRoles, Attribute: n.EligibilityExpr}
	// ... n.SLADuration, n.ReminderEvery, n.ID() ...
case model.IntermediateCatchEvent:
	// ... n.TimerDuration / n.SignalName / n.MessageName / n.CorrelationKey ...
case model.ErrorEndEvent:
	// ... n.ErrorCode ...
case model.EndEvent:
	// ...
case model.SubProcess:
	if n.Subprocess == nil { tok.State = TokenWaitingCommand; continue }
	// ... n.Subprocess.StartNodes(), s.openScope(n.ID(), ...), armEventSubprocesses(n.Subprocess, ...)
case model.ExclusiveGateway:
	// selectExclusiveTarget(tdef, s, n) ...
case model.ParallelGateway:
	if len(tdef.Incoming(n.ID())) > 1 { s.tryParallelJoin(tdef, tok, n, tok.ScopeID, at) } else { s.forkParallel(tdef, tok, n, tok.ScopeID, at) }
case model.InclusiveGateway:
	if len(tdef.Incoming(n.ID())) > 1 { s.tryInclusiveJoin(tdef, tok, n, tok.ScopeID, at) } else { s.forkInclusive(tdef, tok, n, tok.ScopeID, at) }
case model.EventBasedGateway:
	for _, f := range tdef.Outgoing(n.ID()) {
		catchNode, ok := tdef.Node(f.Target)
		ce, _ := catchNode.(model.IntermediateCatchEvent)
		if ce.TimerDuration != "" { /* ... ce.ID() via catchNode.ID() ... */ } else if ce.SignalName != "" { ae.Signal = ce.SignalName } else if ce.MessageName != "" { /* ce.CorrelationKey */ }
		ae.Message = ce.MessageName
	}
case model.CallActivity:
	cmds = append(cmds, engine.StartSubInstance{CommandID: cmdID, Token: tok.ID, DefRef: n.DefRef, Input: copyVars(s.Variables)})
case model.IntermediateThrowEvent:
	if n.CompensateRef != "" { /* ref := n.CompensateRef; tdef.Outgoing(n.ID()) */ } else if n.SignalName != "" { cmds = append(cmds, engine.ThrowSignal{Name: n.SignalName}) }
}
```
Within each `case`, every former `node.Field` becomes `n.Field` (same name) and `node.ID`→`n.ID()`. The `default`-less switch matches today's behavior (unhandled kinds fall through to the post-switch logic).

- [ ] **Step 3: Convert the 7 helper signatures to the interface or concrete type**

These helpers took `model.Node` by value. Change the parameter type and internal reads:

- `forkParallel(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time)` — body uses `node.ID()`.
- `forkInclusive(...node model.Node...)` — `node.ID()`.
- `tryParallelJoin(...node model.Node...)` — `node.ID()`.
- `tryInclusiveJoin(...node model.Node...)` — `node.ID()`.
- `selectExclusiveTarget(def *model.ProcessDefinition, s *InstanceState, node model.Node)` — `node.ID()`.
- `serviceActionInput(s *InstanceState, node model.Node)` — `node.ID()`.
- `effectiveRetryPolicy(node model.Node, opt StepOptions)` — must read a retry policy that exists only on activities. Type-assert via a small optional interface:

```go
// retryCarrier is satisfied by every activity node (they embed activityFields).
type retryCarrier interface{ retryPolicy() *model.RetryPolicy }

func effectiveRetryPolicy(node model.Node, opt StepOptions) (model.RetryPolicy, bool) {
	var rp *model.RetryPolicy
	if rc, ok := node.(retryCarrier); ok {
		rp = rc.retryPolicy()
	}
	// ... existing default/normalize logic, using rp instead of node.RetryPolicy
}
```
To supply `retryPolicy()`, add an unexported method on `activityFields` in `model/node.go`: `func (a activityFields) retryPolicy() *RetryPolicy { return a.RetryPolicy }` — note this requires exporting it across the package boundary. Since `engine` cannot call an unexported model method, instead add an **exported** helper to `model`: `func RetryPolicyOf(n Node) *RetryPolicy` that type-switches over the activity kinds and returns the policy (or nil). Use `model.RetryPolicyOf(node)` in `effectiveRetryPolicy`. (Add this to `model/node.go` with its own TDD cycle in Task 1 if discovered early; otherwise add it here with a model unit test first.)

- [ ] **Step 4: Convert the boundary-scan loops (step.go:1886–1904, 1981–2007) and helpers (2388, 2518, 2620, 2679)**

The two boundary loops iterate `def.Nodes` (now `[]Node`) and filter to boundary events. Replace the field-filter with a type assertion:

```go
for _, raw := range ownDef.Nodes {
	n, ok := raw.(model.BoundaryEvent)
	if !ok || n.AttachedTo != originatingNodeID {
		continue
	}
	if n.TimerDuration != "" || n.SignalName != "" || n.MessageName != "" {
		continue // not an error boundary
	}
	if n.ErrorCode == "" || n.ErrorCode == errorCode {
		handler := n // copy
		directHandler = &handler
		break
	}
}
```
(`directHandler`/`handler` become `*model.BoundaryEvent`.) For the ESP arm (2388): `espNode, ok := enclosingDef.Node(...); esp, ok2 := espNode.(model.EventSubProcess); if !ok || !ok2 || esp.Subprocess == nil { ... }; innerStarts := esp.Subprocess.StartNodes()`. For `handleSLAFired` (2518), `handleReminderFired` (2620), `reinvokeServiceAction` (2679): retrieve the node, type-assert to the kind that carries the field being read (`UserTask`/`ServiceTask` for SLA/Reminder/Action) — or, since SLA/Reminder live on `activityFields` shared by many kinds, add exported `model` accessors (`SLAOf(n Node) (duration, flow, action string)`, `ReminderOf(n Node) (every, action string)`, `ActionOf(n Node) string`) that type-switch internally, and call those. Prefer the accessor approach to avoid asserting every activity kind at each call site. Add each accessor in `model` with a unit test first.

The `espNode.Kind == model.KindEventSubProcess` check at step.go:997 becomes `if _, ok := espNode.(model.EventSubProcess); ok`.

- [ ] **Step 5: Build and run the full engine suite green**

Run: `go build ./... && go test ./engine/ -count=1`
Expected: build clean (repo-wide red window closed); all existing engine tests PASS unchanged. If any fail, the migration changed behavior — fix the type-switch until green; do NOT edit the tests' expectations.

- [ ] **Step 6: Full repo gate + lint**

Run: `go test ./... -count=1` (Docker for testcontainers packages) and `golangci-lint run ./engine/... ./model/...`
Expected: green; no lint findings.

- [ ] **Step 7: Commit**

```bash
git add engine/ model/
git commit -m "refactor(engine): migrate step.go node access to model.Node interface (ADR-0042)"
```

---

### Task 3: Add the `RetryPolicyOf` / `SLAOf` / `ReminderOf` / `ActionOf` model accessors (if not already added in Task 2)

Small TDD task ensuring the exported accessors the engine relies on are tested in `model`. If Task 2 already added them with tests, fold this into Task 2 and skip.

**Files:**
- Modify: `model/node.go` (or `model/accessors.go`), `model/accessors_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestRetryPolicyOf(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 5}
	n := model.NewServiceTask("a", "act", model.WithRetryPolicy(p))
	if model.RetryPolicyOf(n) != p {
		t.Fatal("RetryPolicyOf did not return the activity's policy")
	}
	if model.RetryPolicyOf(model.NewStartEvent("s")) != nil {
		t.Fatal("non-activity must return nil")
	}
}
```

- [ ] **Step 2:** Run red (`undefined: model.RetryPolicyOf`).
- [ ] **Step 3:** Implement `RetryPolicyOf`, `SLAOf`, `ReminderOf`, `ActionOf` as type-switches over the activity kinds.
- [ ] **Step 4:** Run green: `go test ./model/ -run 'Of$'`.
- [ ] **Step 5:** Commit `feat(model): exported activity-field accessors for engine consumption`.

---

### Task 4: `DefinitionBuilder` fluent API

Additive; fully green. Provides ergonomic assembly of nodes + flows with validation.

**Files:**
- Create: `model/builder.go`, `model/builder_test.go`.

**Interfaces:**
- Produces: `type DefinitionBuilder struct{...}`; `func NewDefinition(id string, version int) *DefinitionBuilder`; `(*DefinitionBuilder).Add(n Node) *DefinitionBuilder`; `(*DefinitionBuilder).Connect(fromID, toID string, opts ...FlowOption) *DefinitionBuilder`; `(*DefinitionBuilder).CancelActions(names ...string) *DefinitionBuilder`; `(*DefinitionBuilder).Build() (*ProcessDefinition, error)` (runs `Validate`).

- [ ] **Step 1: Failing test**

```go
func TestDefinitionBuilderBuildsAndValidates(t *testing.T) {
	def, err := model.NewDefinition("p", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewServiceTask("t", "do")).
		Add(model.NewEndEvent("e")).
		Connect("s", "t").
		Connect("t", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(def.Nodes) != 3 || len(def.Flows) != 2 {
		t.Fatalf("got %d nodes %d flows", len(def.Nodes), len(def.Flows))
	}
}

func TestDefinitionBuilderRejectsInvalid(t *testing.T) {
	_, err := model.NewDefinition("p", 1).Add(model.NewServiceTask("t", "do")).Build()
	if err == nil {
		t.Fatal("expected validation error (no start event)")
	}
}
```

- [ ] **Step 2:** Run red.
- [ ] **Step 3:** Implement `builder.go`. `Connect` auto-generates a flow ID (e.g. `from+"->"+to`) unless `WithFlowID` option given; `WithCondition(expr)` and `AsDefault()` options set `SequenceFlow.Condition`/`IsDefault`. `Build` assembles `ProcessDefinition` and returns `&def, Validate(&def)`.
- [ ] **Step 4:** Run green: `go test ./model/ -run DefinitionBuilder`.
- [ ] **Step 5:** Commit `feat(model): fluent DefinitionBuilder`.

---

### Task 5: A testable example for the public authoring API

Library-consumer-facing example (CLAUDE.md rule: write testable examples for code consumed by library users).

**Files:**
- Create: `model/example_test.go`.

- [ ] **Step 1:** Write `func ExampleDefinitionBuilder()` constructing a small process via constructors + builder and printing the node count, with an `// Output:` block.
- [ ] **Step 2:** Run: `go test ./model/ -run Example` → PASS.
- [ ] **Step 3:** Commit `docs(model): testable example for the authoring API`.

---

### Task 6: YAML loader

Additive; fully green. Decodes YAML into a `ProcessDefinition` via the same `kind` discriminator and validation. Adds the `gopkg.in/yaml.v3` dependency.

**Files:**
- Create: `model/yaml.go`, `model/yaml_test.go`, `model/testdata/order.yaml`.
- Modify: `go.mod`, `go.sum`.

**Interfaces:**
- Produces: `func LoadYAML(r io.Reader) (*ProcessDefinition, error)` and `func ParseYAML(data []byte) (*ProcessDefinition, error)`.

- [ ] **Step 1: Add the dependency**

Run: `go get gopkg.in/yaml.v3@latest`
Expected: `go.mod`/`go.sum` updated.

- [ ] **Step 2: Failing test + fixture**

Create `model/testdata/order.yaml`:

```yaml
id: order
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: charge
    kind: serviceTask
    action: charge-card
    compensationAction: refund-card
  - id: e
    kind: endEvent
flows:
  - { id: f1, source: s, target: charge }
  - { id: f2, source: charge, target: e }
```

Create `model/yaml_test.go`:

```go
func TestParseYAML(t *testing.T) {
	data, _ := os.ReadFile("testdata/order.yaml")
	def, err := model.ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if def.ID != "order" || len(def.Nodes) != 3 {
		t.Fatalf("def = %+v", def)
	}
	st, ok := def.Nodes[1].(model.ServiceTask)
	if !ok || st.Action != "charge-card" || st.CompensationAction != "refund-card" {
		t.Fatalf("node[1] = %#v", def.Nodes[1])
	}
}
```

- [ ] **Step 3:** Run red (`undefined: model.ParseYAML`).
- [ ] **Step 4:** Implement `model/yaml.go`. Decode into a YAML mirror of `definitionWire` (reuse `nodeWire` with `yaml:"..."` tags added alongside the existing `json` tags, or a parallel `nodeYAML` struct), then run each through `fromWire` and `Validate`. `LoadYAML` wraps `ParseYAML(io.ReadAll(r))`.
- [ ] **Step 5:** Run green: `go test ./model/ -run YAML`.
- [ ] **Step 6:** Lint + full model suite: `golangci-lint run ./model/... && go test ./model/ -count=1`.
- [ ] **Step 7:** Commit `feat(model): YAML process-definition loader`.

---

### Task 7: ADR-0042 + verification sweep

**Files:**
- Create: `docs/adr/0042-node-interface-model.md`.

- [ ] **Step 1: Write ADR-0042** (Nygard template) recording: Context (flat 35-field god-struct; FOLLOWUPS ②; full-interface chosen over the lower-cost authoring-layer alternative, eyes open re: the 108-site engine migration), Decision (Node interface + per-kind concrete types + constructors; `nodeWire` for backward-compatible JSONB; `DefinitionBuilder` + YAML loader; `gopkg.in/yaml.v3` adopted for YAML authoring), Consequences (segregated per-kind state; ongoing tax: each new kind needs a concrete type, a `fromWire` arm, and a YAML mapping; stored definitions remain readable; engine confined to `engine/step.go`).

- [ ] **Step 2: Full verification**

Run:
```bash
go test -race -coverprofile=cover.out ./model/... && go tool cover -func=cover.out | tail -1
go test ./... -count=1
golangci-lint run ./...
```
Expected: model coverage ≥ 85%; full suite green; lint clean.

- [ ] **Step 3: Commit** `docs(adr): Node-as-interface process-definition model (ADR-0042)`.

---

## Verification checklist (whole sub-project)

- [ ] `model.Node` is an interface; the flat struct is gone; 19 concrete types exist with constructors.
- [ ] `ProcessDefinition.Nodes` is `[]Node`; `Node(id)`/`StartNodes()` return `Node`.
- [ ] JSON round-trips: `Unmarshal(Marshal(def))` equals `def`; a legacy flat-shaped JSON still decodes into the correct concrete types (backward-compat test present).
- [ ] `engine/step.go` compiles against the interface; the **existing** engine test suite passes unchanged (behavior preserved).
- [ ] `DefinitionBuilder` builds + validates; invalid definitions are rejected; a testable Example exists.
- [ ] YAML loader parses the fixture into typed nodes and validates.
- [ ] ADR-0042 recorded; `gopkg.in/yaml.v3` noted in its consequences.
- [ ] `go test ./... -count=1` green; `golangci-lint run ./...` clean; model coverage ≥ 85%.

## Out of scope (other sub-projects)

- Instance serialization DTO (sub-project 3).
- BPMN-wording sweep + README (sub-project 4).
- Layout hygiene (sub-project 1 — lands first).
