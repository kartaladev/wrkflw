package processtest

import (
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
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
	// AwaitingSignals holds the distinct signal names the instance can be woken by,
	// as enumerated by [engine.InstanceState.SignalWaiters]: token signal-catch
	// awaits, armed signal boundaries, event-based-gateway signal arms, and
	// signal-triggered event sub-process arms.
	AwaitingSignals []string
	// AwaitingMessages holds the distinct message waiters the instance can be woken
	// by, as enumerated by [engine.InstanceState.MessageWaiters] — the same four
	// sources as AwaitingSignals. Distinct is on the {Name, CorrelationKey} PAIR:
	// two constructs awaiting one name under different keys are two waiters.
	AwaitingMessages []engine.MessageWaiter
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
//
// AwaitingSignals and AwaitingMessages come from the engine's own authorities,
// [engine.InstanceState.SignalWaiters] and [engine.InstanceState.MessageWaiters],
// so an instance parked purely on a boundary, event-gateway or event-subprocess
// ARM classifies as a signal/message park like a token await does — such arms set
// no field on any token.
//
// KNOWN GAP — a compensation-walk park has no reason of its own. An instance
// waiting on an in-flight reverse-order compensation walk sits at
// [engine.StatusCompensating] with zero tokens: the walk is awaited through the
// engine's compensation cursor, which no token carries, so ReasonAsyncChild
// (which requires a token with AwaitCommand) does not match and the park falls
// through to [ReasonUnknown]. A handler with no case for it Passes, and drive
// then reports [ErrUnhandledPark]. Measured reason="unknown" on both a
// hand-built snapshot and a real mid-walk state produced by the engine
// (ADR-0168, which widens the routes reaching that state; it is reachable
// through whole-instance rollback regardless). It bites a consumer classifying
// a STORED mid-walk snapshot: measured, the default synchronous drive loop
// completes the walk inside one ApplyTrigger and never parks on it. Closing the
// gap means a ReasonCompensation whose [Park] surfaces the awaited command id so
// a handler can deliver the walk's ActionCompleted — deliberately not done here.
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
	// Delegate to the engine's waiter authorities rather than re-deriving from
	// Token.AwaitSignal/AwaitMessage: those cover only the FIRST of four sources,
	// silently dropping boundary, event-gateway and event-subprocess arms
	// (ADR-0166). The authorities may return duplicates; Park documents these
	// fields as distinct, so dedup here.
	p.AwaitingSignals = distinctStrings(state.SignalWaiters())
	p.AwaitingMessages = distinctWaiters(state.MessageWaiters())

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
		p.Node = awaitNode(state, func(t engine.Token) bool { return t.AwaitSignal != "" })
	case len(p.AwaitingMessages) > 0:
		p.Reason = ReasonMessage
		p.Node = awaitNode(state, func(t engine.Token) bool { return t.AwaitMessage != "" })
	case p.HasArmedTimers:
		p.Reason = ReasonTimer
		p.Node = firstNodeWhere(state.Tokens, func(t engine.Token) bool { return t.State == engine.TokenWaiting })
	case hasCommandWait(state.Tokens):
		p.Reason = ReasonAsyncChild
		p.Node = firstNodeWhere(state.Tokens, func(t engine.Token) bool {
			return t.State == engine.TokenWaiting && t.AwaitCommand != ""
		})
	default:
		p.Reason = ReasonUnknown
	}

	return p
}

// distinctStrings returns in with empties dropped and duplicates collapsed,
// preserving first-seen order. [engine.InstanceState.SignalWaiters] documents that
// it does not dedup (a set-based SignalBus.Sync collapses them downstream), so
// Park does.
func distinctStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, v := range in {
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

// distinctWaiters is distinctStrings for message waiters, collapsing on the whole
// {Name, CorrelationKey} pair: two constructs awaiting one name under different
// keys are genuinely different waiters and both must survive.
func distinctWaiters(in []engine.MessageWaiter) []engine.MessageWaiter {
	seen := make(map[engine.MessageWaiter]struct{}, len(in))
	var out []engine.MessageWaiter
	for _, w := range in {
		if w.Name == "" {
			continue
		}
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

// awaitNode returns the node id of the first token matching pred, falling back to
// the first waiting token's node when no token carries the await at all. That
// fallback is the arm-derived case (ADR-0166 D5): a boundary, event-gateway or
// event-subprocess arm sets no Token.AwaitSignal/AwaitMessage, so Node would
// otherwise collapse to "" and degrade both errors a consumer sees
// ([ErrUnhandledPark], [ErrDriveLimitExceeded]). The arm's OWN node stays
// unreachable — the arm slices on [engine.InstanceState] have unexported element
// types.
func awaitNode(state engine.InstanceState, pred func(engine.Token) bool) string {
	if id := firstNodeWhere(state.Tokens, pred); id != "" {
		return id
	}
	return firstNodeWhere(state.Tokens, func(t engine.Token) bool { return t.State == engine.TokenWaiting })
}

// armDerivedReason reports whether p's primary Reason came from an ARM rather
// than from a token await: the name reached AwaitingSignals/AwaitingMessages via
// a boundary, event-gateway or event-subprocess arm, and no token carries the
// matching await.
//
// It exists for the timer promotion in harnessEnv.classify (ADR-0166 D3). Once
// arms compete in the ladder, an arm-derived ReasonSignal would otherwise displace
// ReasonAsyncChild/ReasonUnknown and silently disable [AutoTimers] on any
// definition carrying a live arm. Only an arm-derived reason yields to the timer;
// a genuine token signal-catch still outranks it.
func armDerivedReason(p Park) bool {
	switch p.Reason {
	case ReasonSignal, ReasonMessage:
		// NEITHER await may be token-carried. Testing only the await that produced
		// the reason is not enough: a signal ARM raises ReasonSignal while a token
		// holds a live AwaitMessage, and treating that as arm-derived promotes the
		// park to ReasonTimer — firing a timer that a genuine token message await
		// should have outranked.
		return !anyToken(p.State.Tokens, func(t engine.Token) bool {
			return t.AwaitSignal != "" || t.AwaitMessage != ""
		})
	default:
		return false
	}
}

func anyToken(tokens []engine.Token, pred func(engine.Token) bool) bool {
	for _, t := range tokens {
		if pred(t) {
			return true
		}
	}
	return false
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
		if t.State == engine.TokenWaiting && t.AwaitCommand != "" {
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
