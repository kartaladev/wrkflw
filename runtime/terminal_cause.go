package runtime

import "github.com/kartaladev/wrkflw/engine"

// causeOfDeathIncident returns the first incident on a terminal instance that may
// stand as its published cause of death, and whether one was found.
//
// It is an ALLOW-LIST — it admits [engine.IncidentAction] and nothing else — and
// that direction is the point. Only IncidentAction describes work the instance was
// actually doing when it died. The compensation kinds are walk-scoped (they carry
// no TokenID, because a compensation walk is not driven by a token of its own) and
// describe the cleanup rather than the failure; [engine.IncidentDefinitionDefect]
// does name a token, but it describes a branch that could never run rather than
// anything that killed the instance — an instance carrying one dies by being
// cancelled, and "cancelled" is the honest cause. Written as a deny-list of the
// kinds known today, each new [engine.IncidentKind] would silently become a
// publishable cause of death the moment it was added; written this way it has to
// be opted in deliberately.
//
// The alternative it replaces was positional — st.Incidents[0].Error — and a
// walk-scoped incident reaches index 0 whenever the walk raises it before anything
// else does, which on a cancel walk with no earlier incident is always. The
// operator was then told the instance died of a compensation problem rather than of
// the error that killed it.
//
// ⚠ Its two callers DIVERGE on what happens when this returns false, and that is
// intended — do not harmonise them:
//
//   - [terminalEventErr] (runtime/outbox.go) falls through to its
//     engine.FailInstance{Err} command scan, then to a status-keyed default. It has
//     that rung because the SubInstanceFailed and cancel paths record no incident
//     yet carry a rich message on the command.
//   - [terminalErr] (runtime/processdriver_action.go) has no command to scan — it is
//     handed only the state — so it falls straight to its status switch.
func causeOfDeathIncident(st engine.InstanceState) (engine.Incident, bool) {
	for _, inc := range st.Incidents {
		if inc.Kind == engine.IncidentAction {
			return inc, true
		}
	}
	return engine.Incident{}, false
}
