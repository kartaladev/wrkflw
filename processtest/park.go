package processtest

import (
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// Reason is the primary classification of why an instance is parked, i.e. why a
// [DriveToCompletion] step could not proceed without external stimulus. When more
// than one park applies (e.g. a user task that also has a boundary timer), Reason
// reports the highest-priority one; the discrete fields on [Park] still expose the
// rest.
type Reason int

const (
	// ReasonTerminal means the instance has reached a terminal status.
	ReasonTerminal Reason = iota
	// ReasonHumanTask means at least one human task is open (unclaimed or claimed).
	ReasonHumanTask
	// ReasonIncident means a token is parked as an incident (retry budget exhausted
	// or a non-retryable failure).
	ReasonIncident
	// ReasonSignal means a token is waiting on a named signal.
	ReasonSignal
	// ReasonMessage means a token is waiting on a named message.
	ReasonMessage
	// ReasonTimer means the instance is parked with at least one armed timer and
	// nothing higher-priority to resolve.
	ReasonTimer
	// ReasonAsyncChild means a token is waiting on a command (typically an async
	// call activity awaiting its child instance's outcome).
	ReasonAsyncChild
	// ReasonUnknown means the instance is non-terminal but the classifier could not
	// identify a resolvable park (e.g. an active token mid-burst).
	ReasonUnknown
)

// String returns the lowercase name of the reason.
func (r Reason) String() string {
	switch r {
	case ReasonTerminal:
		return "terminal"
	case ReasonHumanTask:
		return "human-task"
	case ReasonIncident:
		return "incident"
	case ReasonSignal:
		return "signal"
	case ReasonMessage:
		return "message"
	case ReasonTimer:
		return "timer"
	case ReasonAsyncChild:
		return "async-child"
	default:
		return "unknown"
	}
}

// Park is the classified state of a parked instance handed to a [ParkHandler].
// The handler may switch on Reason for the common case or inspect the discrete
// fields (and State) to resolve secondary parks.
type Park struct {
	// State is the full instance snapshot; a handler may inspect anything on it.
	State engine.InstanceState
	// Reason is the primary (highest-priority) park classification.
	Reason Reason
	// Node is the best-effort id of the node the primary park sits on.
	Node string
	// OpenTasks holds every open (unclaimed or claimed) human task.
	OpenTasks []humantask.HumanTask
	// AwaitingSignals holds the distinct signal names any token is waiting on.
	AwaitingSignals []string
	// AwaitingMessages holds the distinct message names any token is waiting on.
	AwaitingMessages []string
	// HasArmedTimers reports whether the instance has any armed timer.
	HasArmedTimers bool
	// Incidents holds the instance's open incident records.
	Incidents []engine.Incident
}

// IsTerminal reports whether s is a terminal status (completed, failed, or
// terminated). It mirrors the engine's internal terminal predicate so a consumer
// can detect completion without depending on unexported engine internals.
func IsTerminal(s engine.Status) bool {
	return s == engine.StatusCompleted || s == engine.StatusFailed || s == engine.StatusTerminated
}

// Classify inspects an instance snapshot and returns its [Park]. It always fills
// the discrete fields (OpenTasks, AwaitingSignals, AwaitingMessages,
// HasArmedTimers, Incidents) and sets Reason to the highest-priority park:
// terminal > human-task > incident > signal > message > timer > async-child >
// unknown.
func Classify(state engine.InstanceState) Park {
	p := Park{
		State:          state,
		HasArmedTimers: len(state.Timers) > 0,
		Incidents:      state.Incidents,
	}

	// Discrete fields (independent of the primary reason).
	for _, tsk := range state.Tasks {
		if tsk.IsOpen() {
			p.OpenTasks = append(p.OpenTasks, tsk)
		}
	}
	p.AwaitingSignals = distinctAwaits(state.Tokens, func(t engine.Token) string { return t.AwaitSignal })
	p.AwaitingMessages = distinctAwaits(state.Tokens, func(t engine.Token) string { return t.AwaitMessage })

	// Primary reason, in priority order.
	switch {
	case IsTerminal(state.Status):
		p.Reason = ReasonTerminal
	case len(p.OpenTasks) > 0:
		p.Reason = ReasonHumanTask
		p.Node = p.OpenTasks[0].NodeID
	case len(p.Incidents) > 0 || hasIncidentToken(state.Tokens):
		p.Reason = ReasonIncident
		p.Node = incidentNode(state)
	case len(p.AwaitingSignals) > 0:
		p.Reason = ReasonSignal
		p.Node = firstNodeWhere(state.Tokens, func(t engine.Token) bool { return t.AwaitSignal != "" })
	case len(p.AwaitingMessages) > 0:
		p.Reason = ReasonMessage
		p.Node = firstNodeWhere(state.Tokens, func(t engine.Token) bool { return t.AwaitMessage != "" })
	case p.HasArmedTimers:
		p.Reason = ReasonTimer
		p.Node = firstNodeWhere(state.Tokens, func(t engine.Token) bool { return t.State == engine.TokenWaitingCommand })
	case hasCommandWait(state.Tokens):
		p.Reason = ReasonAsyncChild
		p.Node = firstNodeWhere(state.Tokens, func(t engine.Token) bool {
			return t.State == engine.TokenWaitingCommand && t.AwaitCommand != ""
		})
	default:
		p.Reason = ReasonUnknown
	}

	return p
}

func distinctAwaits(tokens []engine.Token, get func(engine.Token) string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, t := range tokens {
		v := get(t)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func hasIncidentToken(tokens []engine.Token) bool {
	for _, t := range tokens {
		if t.State == engine.TokenIncident {
			return true
		}
	}
	return false
}

func incidentNode(state engine.InstanceState) string {
	if len(state.Incidents) > 0 {
		return state.Incidents[0].NodeID
	}
	return firstNodeWhere(state.Tokens, func(t engine.Token) bool { return t.State == engine.TokenIncident })
}

func hasCommandWait(tokens []engine.Token) bool {
	for _, t := range tokens {
		if t.State == engine.TokenWaitingCommand && t.AwaitCommand != "" {
			return true
		}
	}
	return false
}

func firstNodeWhere(tokens []engine.Token, pred func(engine.Token) bool) string {
	for _, t := range tokens {
		if pred(t) {
			return t.NodeID
		}
	}
	return ""
}
