package service

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// ProcessInstance is the read-only, fused view of a running instance: its
// definition and state. It serializes directly to a stable, frontend-ready JSON
// document via MarshalJSON; the serialized shape is an internal detail (no
// exported DTO fields), so a consumer can embed it in its own domain/DTO type
// and marshal with no transformation.
type ProcessInstance interface {
	Definition() *model.ProcessDefinition // raw template (nil if unresolved)
	State() engine.InstanceState          // raw running state
	json.Marshaler                        // MarshalJSON() ([]byte, error)

	// ActiveTasks returns every open human task (Unclaimed or Claimed;
	// humantask.IsOpen) in the instance, sorted by TaskID in ascending
	// lexicographic order. Never nil: an instance with no open tasks yields a
	// non-nil empty slice.
	//
	// Every task is a [humantask.HumanTask.Clone] of the stored record, so writing
	// through the returned value — including through its Claim pointer, Candidates
	// slice or Vars map — cannot reach the instance's audit state.
	ActiveTasks() []humantask.HumanTask

	// ActiveTask returns the open human task at nodeID and true, or the zero
	// HumanTask and false if the node has no open task. "Open" means Unclaimed or
	// Claimed (humantask.IsOpen). A well-formed graph has at most one open task
	// per node; if a pathological definition produces more than one, the first in
	// ascending TaskID order is returned. The task is cloned on the same terms as
	// [ProcessInstance.ActiveTasks].
	ActiveTask(nodeID string) (humantask.HumanTask, bool)
}

// NewProcessInstance fuses a definition (may be nil) and instance state into a
// ProcessInstance. Exported so consumers and tests can fabricate one. The
// marshalled document embeds a non-nil definition; the engine-level
// opt-out [WithoutEmbeddedDefinition] applies to instances the ProcessEngine
// hands out, not to one fabricated here.
func NewProcessInstance(def *model.ProcessDefinition, st engine.InstanceState) ProcessInstance {
	return newProcessInstance(def, st, false)
}

// newProcessInstance is the internal constructor carrying the marshalling
// policy: omitDefinition drops the `definition` embed from the serialized
// document. It backs both [NewProcessInstance] and ProcessEngine.instance, which
// applies the engine's [WithoutEmbeddedDefinition] setting.
func newProcessInstance(def *model.ProcessDefinition, st engine.InstanceState, omitDefinition bool) ProcessInstance {
	return processInstance{def: def, st: st, omitDefinition: omitDefinition}
}

type processInstance struct {
	def *model.ProcessDefinition
	st  engine.InstanceState
	// omitDefinition suppresses the `definition` embed at marshal time only. The
	// Definition accessor keeps returning def regardless.
	omitDefinition bool
}

func (p processInstance) Definition() *model.ProcessDefinition { return p.def }
func (p processInstance) State() engine.InstanceState          { return p.st }

func (p processInstance) ActiveTasks() []humantask.HumanTask {
	out := make([]humantask.HumanTask, 0, len(p.st.Tasks))
	for _, t := range p.st.Tasks {
		if t.IsOpen() {
			// Clone before exposing: Claim/Completion are pointers and
			// Candidates/Vars/Eligibility slices and maps, all reachable into the
			// live InstanceState. A consumer mutating a returned task must not
			// reach the engine's audit record. Mirrors runtime/view's guard.
			out = append(out, t.Clone())
		}
	}
	slices.SortFunc(out, func(a, b humantask.HumanTask) int {
		return cmp.Compare(a.TaskID, b.TaskID)
	})
	return out
}

func (p processInstance) ActiveTask(nodeID string) (humantask.HumanTask, bool) {
	for _, t := range p.ActiveTasks() { // already open-filtered and token-sorted
		if t.NodeID == nodeID {
			return t, true
		}
	}
	return humantask.HumanTask{}, false
}

func (p processInstance) MarshalJSON() ([]byte, error) {
	return json.Marshal(newInstanceJSON(p.def, p.st, p.omitDefinition))
}

// instanceJSON is the UNEXPORTED serialized projection. Its field names/tags
// define the ProcessInstance JSON wire format.
//
// The definition appears in two complementary forms. `def_id` / `def_version`
// are the IDENTITY: taken from the instance state, they are always available,
// cost two scalars, and are the stable key a consumer routes, groups or filters
// on — so they are emitted unconditionally, even for an instance whose template
// could not be resolved. `definition` is the whole template, EMBEDDED rather
// than summarized: it marshals through its own canonical snake_case
// wire, so the document is self-contained and there is no derived projection to
// keep in sync, but it is omitted when the definition is unresolved.
//
// The embed did retire the derived `action_bindings` list and the top-level
// `scoped_actions` mirror — both readable from `definition` (whose wire carries
// `scoped_actions`).
type instanceJSON struct {
	InstanceID string `json:"instance_id"`
	// DefID and DefVersion identify the template this instance runs. Unlike
	// Definition they are never omitted: they come from the instance state, not
	// from a resolved definition.
	DefID      string          `json:"def_id"`
	DefVersion int             `json:"def_version"`
	Status     string          `json:"status"`
	Variables  map[string]any  `json:"variables,omitempty"`
	Tokens     []tokenJSON     `json:"tokens,omitempty"`
	History    []nodeVisitJSON `json:"history,omitempty"`
	Tasks      []taskJSON      `json:"tasks,omitempty"`
	Incidents  []incidentJSON  `json:"incidents,omitempty"`
	// Compensating projects the in-flight compensation walk's cursor, and is
	// omitted entirely when no walk is in flight. It is what
	// makes a WEDGED instance findable: a stalled walk never dispatches again, so
	// an instance that stalled under an older version raises no incident and
	// would otherwise be invisible. It also carries the command id the three
	// escape verbs require.
	Compensating *compensatingJSON `json:"compensating,omitempty"`
	StartedAt    time.Time         `json:"started_at"`
	EndedAt      *time.Time        `json:"ended_at,omitempty"`
	// Definition is the process template, marshalled by
	// model.ProcessDefinition.MarshalJSON. Omitted when the instance was built
	// without one (an unresolved definition), or when the engine was built with
	// WithoutEmbeddedDefinition — the identity above survives either way.
	Definition *model.ProcessDefinition `json:"definition,omitempty"`
}

type tokenJSON struct {
	ID            string         `json:"id"`
	NodeID        string         `json:"node_id"`
	ScopeID       string         `json:"scope_id,omitempty"`
	State         string         `json:"state"`
	Payload       map[string]any `json:"payload,omitempty"`
	EnteredAt     time.Time      `json:"entered_at"`
	RetryAttempts int            `json:"retry_attempts,omitempty"`
}

type nodeVisitJSON struct {
	NodeID    string     `json:"node_id"`
	TokenID   string     `json:"token_id"`
	EnteredAt time.Time  `json:"entered_at"`
	LeftAt    *time.Time `json:"left_at,omitempty"`
	// TaskID links a user-task visit to its `tasks` entry; CloseKind names why
	// the visit closed and is emitted only for an abnormal close.
	TaskID    string `json:"task_id,omitempty"`
	CloseKind string `json:"close_kind,omitempty"`
}

// compensatingJSON is the in-flight compensation walk's operator-facing
// projection.
//
// ⚠ ActiveCommandID exposes an internal command id. That is a deliberate trade:
// without it an operator cannot name the stalled dispatch, and every escape verb
// requires naming it.
//
// The exposure is narrower than an earlier revision of this comment claimed. It
// said this leaks an "<instance>-cN sequence oracle"; that form is only the
// DETERMINISTIC FALLBACK nextID uses when no IDGenerator is injected (pure-engine
// tests and replay). The runtime always injects the xid generator, and
// the same generator mints the token and task ids this document already exposes
// as tokens[].id and tasks[].task_id — so in a product deployment this field adds
// no ordering signal a reader did not already have.
type compensatingJSON struct {
	// ActiveCommandID is the compensation command currently in flight — the value
	// a retry/skip/abandon request must carry.
	ActiveCommandID string `json:"active_command_id"`
	// Since is when the walk began, stamped once at walk start. It is what lets an
	// operator tell a walk that started seconds ago from one stuck for a day.
	//
	// A POINTER, omitted when unknown. A walk already in flight when this build
	// was deployed carries no stamp — and that is exactly the population this
	// projection exists to make findable, since a stalled walk never dispatches
	// again and so never raises an incident. Rendering year 1 for them would read
	// as data rather than as "unknown".
	Since *time.Time `json:"since,omitempty"`
	// ScopeID is the scope whose records the walk is compensating ("" = root).
	ScopeID string `json:"scope_id,omitempty"`
}

type incidentJSON struct {
	ID string `json:"id"`
	// Kind is the field a consumer switches on, because the three kinds are
	// resolved by DIFFERENT operations:
	//
	//   - "IncidentAction" — a service action that failed and exhausted its
	//     retries. It parks a token and IS cleared by resolve-incident.
	//   - "IncidentCompensationStall" and "IncidentCompensationFailed" —
	//     walk-scoped: token_id is empty, because a compensation walk
	//     is not driven by a token of its own. resolve-incident REFUSES both and
	//     names the three escape verbs (retry, skip, abandon) instead.
	//
	// ⚠ Do NOT route on slice position. A walk-scoped record and a token-parked
	// one coexist routinely, and `incidents[0]` says nothing about which is which
	// — the positional read is the defect fixed in the runtime's own
	// cause-of-death resolvers.
	Kind      string    `json:"kind"`
	TokenID   string    `json:"token_id"`
	NodeID    string    `json:"node_id"`
	ScopeID   string    `json:"scope_id,omitempty"`
	Error     string    `json:"error"`
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"created_at"`
}

// taskJSON is the human-task audit projection. The actor, claim and
// completion types carry their own wire tags, so they are EMBEDDED verbatim
// rather than re-mapped here: an actor renders as {id, roles?, attributes?}
// exactly as the engine observed it, and a slot the consumer's ActorResolver
// left empty stays empty.
//
// Claim is nil while the task is Unclaimed. Completion is nil until the task is
// completed BY AN ACTOR — an immediate manual task is marked Completed inline by
// the engine with no actor, so a "completed" state does not imply a completion
// record.
type taskJSON struct {
	TaskID     string                `json:"task_id"`
	NodeID     string                `json:"node_id"`
	State      string                `json:"state"`
	Candidates []authz.Actor         `json:"candidates,omitempty"`
	Claim      *humantask.Claim      `json:"claim,omitempty"`
	Completion *humantask.Completion `json:"completion,omitempty"`
	CreatedAt  time.Time             `json:"created_at"`
	DueAt      *time.Time            `json:"due_at,omitempty"`
}

// tokenStateString converts an engine.TokenState to its canonical string
// representation. Out-of-range values map to "unknown".
func tokenStateString(s engine.TokenState) string {
	switch s {
	case engine.TokenActive:
		return "active"
	case engine.TokenWaiting:
		return "waiting"
	case engine.TokenJoining:
		return "joining"
	case engine.TokenIncident:
		return "incident"
	default:
		return "unknown"
	}
}

// newInstanceJSON projects def and st onto the wire document. omitDefinition
// suppresses the `definition` embed only (see WithoutEmbeddedDefinition) —
// def_id / def_version come from st and are always emitted.
func newInstanceJSON(def *model.ProcessDefinition, st engine.InstanceState, omitDefinition bool) instanceJSON {
	tokens := make([]tokenJSON, 0, len(st.Tokens))
	for _, t := range st.Tokens {
		tokens = append(tokens, tokenJSON{
			ID:            t.ID,
			NodeID:        t.NodeID,
			ScopeID:       t.ScopeID,
			State:         tokenStateString(t.State),
			Payload:       t.Payload,
			EnteredAt:     t.EnteredAt,
			RetryAttempts: t.RetryAttempts,
		})
	}

	history := make([]nodeVisitJSON, 0, len(st.History))
	for _, v := range st.History {
		history = append(history, nodeVisitJSON{
			NodeID:    v.NodeID,
			TokenID:   v.TokenID,
			EnteredAt: v.EnteredAt,
			LeftAt:    v.LeftAt,
			TaskID:    v.TaskID,
			// The DTO field stays a plain string: this is the wire projection, and
			// the enum's value is exactly what the document carries.
			CloseKind: v.CloseKind.String(),
		})
	}

	tasks := make([]taskJSON, 0, len(st.Tasks))
	for _, t := range st.Tasks {
		tasks = append(tasks, taskJSON{
			TaskID:     t.TaskID,
			NodeID:     t.NodeID,
			State:      t.State.String(),
			Candidates: t.Candidates,
			Claim:      t.Claim,
			Completion: t.Completion,
			CreatedAt:  t.CreatedAt,
			DueAt:      t.DueAt,
		})
	}

	incidents := make([]incidentJSON, 0, len(st.Incidents))
	for _, i := range st.Incidents {
		incidents = append(incidents, incidentJSON{
			ID:        i.ID,
			Kind:      i.Kind.String(),
			TokenID:   i.TokenID,
			NodeID:    i.NodeID,
			ScopeID:   i.ScopeID,
			Error:     i.Error,
			Attempts:  i.Attempts,
			CreatedAt: i.CreatedAt,
		})
	}

	// Project the cursor only while a walk is actually in flight. ActiveCmdID is
	// the discriminator rather than Status: endInstance clears the cursor on every
	// terminal transition, so a non-empty id means a live dispatch.
	var compensating *compensatingJSON
	if st.Compensating.ActiveCmdID != "" {
		compensating = &compensatingJSON{
			ActiveCommandID: st.Compensating.ActiveCmdID,
			ScopeID:         st.Compensating.ScopeID,
		}
		if since := st.Compensating.StartedAt; !since.IsZero() {
			compensating.Since = &since
		}
	}

	embedded := def
	if omitDefinition {
		embedded = nil
	}

	return instanceJSON{
		InstanceID:   st.InstanceID,
		DefID:        st.DefID,
		DefVersion:   st.DefVersion,
		Status:       st.Status.String(),
		Variables:    st.Variables,
		Tokens:       tokens,
		History:      history,
		Tasks:        tasks,
		Incidents:    incidents,
		Compensating: compensating,
		StartedAt:    st.StartedAt,
		EndedAt:      st.EndedAt,
		Definition:   embedded,
	}
}
