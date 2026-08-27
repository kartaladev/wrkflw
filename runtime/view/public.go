package view

import (
	"slices"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// PublicState returns a projection of st carrying only the fields classified public,
// widened by d (ADR-0190).
//
// # Why it BUILDS rather than copies
//
// It constructs a FRESH engine.InstanceState naming only allow-listed fields, instead of
// copying st and clearing the sensitive ones. Anything not named here is therefore absent
// BY CONSTRUCTION, so a field added to the engine's state tomorrow is withheld without
// anyone remembering to withhold it.
//
// That inversion is the whole design. The predecessor cleared a deny-list from a wholesale
// copy and leaked four separate snapshots of the process variables — Tasks[].Vars,
// RootCompensations[].Input, Scopes[].Compensations[].Input and
// ArchivedCompensations[k][].Input — each of which someone had simply not listed.
// TestClassification_IsTotal fails on any field that is neither classified public nor
// withheld, so the omission cannot recur silently.
//
// It is possible only because Go forbids SPECIFYING another package's unexported fields but
// not OMITTING them: engine.InstanceState carries an unexported id source and six sequence
// counters, and a keyed literal from this package simply leaves them zero.
//
// # ⚠ RENDER-ONLY — never feed this back into the engine
//
// The projection drops that unexported id source and those counters, along with timers,
// armed events, boundaries and scopes. It is suitable for marshalling and for nothing else.
// Persisting it, or resuming from it, would corrupt the instance.
//
// # ⚠ It answers engine.InstanceState's methods FALSELY
//
// InstanceState carries exported methods (HasArmedTimers, SignalWaiters, TaskByID and
// others) that answer from whatever state they are on. Called on this projection they
// report on the PROJECTION, not on the instance — HasArmedTimers says false because timers
// were withheld, not because none are armed. A consumer's InstanceMapper therefore receives
// answers that are wrong rather than merely partial.
//
// A distinct projection type would fix that. It was rejected because InstanceMapper is
// func(engine.InstanceState) any — a documented public seam — so a new type would break
// every consumer's mapper. The hazard is documented and pinned by a test instead.
//
// PublicState never mutates st: callers hold state obtained from ProcessInstance.State(),
// which in-process consumers rely on for full fidelity.
func PublicState(st engine.InstanceState, d authz.DisclosureSet) engine.InstanceState {
	out := engine.InstanceState{
		InstanceID:         st.InstanceID,
		DefID:              st.DefID,
		DefVersion:         st.DefVersion,
		Status:             st.Status,
		StartedAt:          st.StartedAt,
		EndedAt:            st.EndedAt,
		PendingCancel:      st.PendingCancel,
		PendingFinalStatus: st.PendingFinalStatus,
		// NodeVisit has no withheld field, so the whole element is public. It is cloned
		// rather than aliased so a caller mutating the projection cannot reach st.
		History: slices.Clone(st.History),
	}

	out.Tokens = projectTokens(st.Tokens, d)
	out.Tasks = projectTasks(st.Tasks, d)

	if d.Has(authz.DiscloseOperations) {
		// ⚠ The cursor is copied wholesale because its TYPE is unexported, so this package
		// cannot build a fresh one — "withheld by construction" does NOT protect it, exactly
		// as it does not protect Claim, Completion and Actor. Two of its fields are therefore
		// cleared explicitly, and `compensationCursor` is a classified type so a field added
		// to it FAILS TestClassification_IsTotal until someone decides its disposition.
		out.Compensating = st.Compensating
		if !d.Has(authz.DiscloseVariables) {
			// Records[].Input is documented as "a snapshot of the instance variables at the
			// moment the activity was invoked" — the FIFTH such snapshot, and the only one
			// that was not gated on DiscloseVariables with its four siblings.
			out.Compensating.Records = nil
			// FinalErr is the SAME STRING as PendingFinalErr, which is withheld under every
			// category, and the same errorCode that populates Incident.Error, which
			// projectIncidents blanks. One value must not have three dispositions.
			out.Compensating.FinalErr = ""
		}
		out.Incidents = projectIncidents(st.Incidents, d)
	}

	if d.Has(authz.DiscloseVariables) {
		out.Variables = st.Variables
		out.StartVariables = st.StartVariables
		out.RootCompensations = st.RootCompensations
		out.Scopes = st.Scopes
		out.ArchivedCompensations = st.ArchivedCompensations
	}
	return out
}

// projectTokens rebuilds each token from Token's own public field list.
func projectTokens(in []engine.Token, d authz.DisclosureSet) []engine.Token {
	if in == nil {
		return nil
	}
	out := make([]engine.Token, len(in))
	for i, t := range in {
		out[i] = engine.Token{
			ID:             t.ID,
			NodeID:         t.NodeID,
			ScopeID:        t.ScopeID,
			State:          t.State,
			EnteredAt:      t.EnteredAt,
			RetryAttempts:  t.RetryAttempts,
			RetryStartedAt: t.RetryStartedAt,
		}
		if d.Has(authz.DiscloseVariables) {
			out[i].Payload = t.Payload
		}
		// The Await* fields stay withheld under every category: a correlation key or a
		// signal name is a business identifier, not a variable, an actor or a policy.
	}
	return out
}

// projectTasks rebuilds each task from HumanTask's own public field list.
func projectTasks(in []humantask.HumanTask, d authz.DisclosureSet) []humantask.HumanTask {
	if in == nil {
		return nil
	}
	out := make([]humantask.HumanTask, len(in))
	for i, tk := range in {
		// State survives, so an unidentified reader still learns WHETHER a task is claimed
		// or completed — only never by whom. Withholding Claim and Completion wholesale
		// therefore trims no discriminator.
		out[i] = humantask.HumanTask{
			TaskID:     tk.TaskID,
			NodeID:     tk.NodeID,
			InstanceID: tk.InstanceID,
			State:      tk.State,
			CreatedAt:  tk.CreatedAt,
			DueAt:      tk.DueAt,
		}
		if d.Has(authz.DiscloseVariables) {
			out[i].Vars = tk.Vars
		}
		if d.Has(authz.DisclosePolicy) {
			out[i].Eligibility = tk.Eligibility
		}
		if d.Has(authz.DiscloseActors) {
			out[i].Candidates = tk.Candidates
			out[i].Claim = tk.Claim
		}
		// ⚠ Completion is restored by EITHER category, because it carries two independently
		// governed things: who completed the task (actors) and what they wrote (notes).
		// Gating the whole struct on DiscloseActors made DiscloseNotes inert on its own,
		// silently producing nothing while its godoc promised independence.
		if d.Has(authz.DiscloseActors) || d.Has(authz.DiscloseNotes) {
			out[i].Completion = projectCompletion(tk.Completion, d)
		}
	}
	return out
}

// projectIncidents restores the operator-facing incident fields, withholding the error text
// unless variables are disclosed too.
//
// ⚠ Incident.Error is err.Error() from the consumer's own action, verbatim. The id is what
// makes an incident actionable through ResolveIncident; the text is what makes it leak.
func projectIncidents(in []engine.Incident, d authz.DisclosureSet) []engine.Incident {
	if in == nil {
		return nil
	}
	out := make([]engine.Incident, len(in))
	for i, inc := range in {
		out[i] = inc
		if !d.Has(authz.DiscloseVariables) {
			out[i].Error = ""
		}
	}
	return out
}

// projectCompletion restores a completion under DiscloseActors, blanking its note unless
// DiscloseNotes is also set.
//
// ⚠ It COPIES before blanking. Completion is a pointer, and two ProcessInstance.State()
// calls hand back the same one, so clearing the note in place would write through into the
// caller's live state — a rendering policy silently corrupting the instance. The categories
// are independent by design: who completed a task is a different disclosure decision from
// what they wrote about it.
func projectCompletion(c *humantask.Completion, d authz.DisclosureSet) *humantask.Completion {
	if c == nil {
		return nil
	}
	cc := *c
	if !d.Has(authz.DiscloseNotes) {
		cc.Note = ""
	}
	if !d.Has(authz.DiscloseActors) {
		cc.Actor = authz.Actor{}
	}
	return &cc
}
