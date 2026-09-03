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
	// ReasonIncident means the instance has an open incident and no other park to
	// report. It is raised from TWO rungs at opposite ends of [Classify]'s ladder,
	// because the two shapes of incident demand opposite treatment:
	//
	//   - TOKEN-SCOPED (a token in engine.TokenIncident, or an incident whose
	//     engine.Incident.TokenID is non-empty — engine.IncidentAction from an
	//     exhausted retry budget or a non-retryable failure, or
	//     engine.IncidentDefinitionDefect from a node no trigger can resume). The
	//     token is stuck and nothing lower in the ladder can free it, so it fires
	//     from the HIGH rung, above signal, message and timer. ResolveIncident
	//     frees an IncidentAction; it REFUSES a defect, which no instance-level
	//     verb clears — that definition has to be corrected and the instance
	//     cancelled. Both belong on this rung regardless, because the question
	//     the rung answers is what the harness is blocked ON, not whether the
	//     block is clearable.
	//   - WALK-SCOPED (engine.IncidentCompensationStall and
	//     engine.IncidentCompensationFailed). These park no token: they
	//     carry an empty TokenID because a compensation walk is not driven by a
	//     token of its own, and ResolveIncident REFUSES them with
	//     engine.ErrIncidentNotResolvable. The escape verbs are retry, skip and
	//     abandon on ProcessDriver.ResolveCompensationStall. It fires from the LAST
	//     rung, below every reason a harness can act on and immediately above
	//     [ReasonUnknown].
	//
	// ⚠ The split is what a walk-scoped record's meaning requires. Reason names
	// what the harness must DO to unblock the instance; a walk-scoped incident is
	// a record that something failed, not an action, so it must never displace a
	// signal, message, timer, human task or command wait that IS actionable. The
	// engine raises engine.IncidentCompensationFailed on every failed compensation
	// action for every consumer, retry policy or not, and such a record is never
	// retired on an instance the walk RESUMED — so before the split a healthy
	// Running instance could carry one forever and report this reason for a park a
	// Reason-switching harness had a perfectly good case for.
	//
	// Every incident is reported in full on Park.Incidents regardless of which
	// rung fired, or whether either did. A handler that gets this Reason must not
	// feed Park.Incidents[0].ID to ResolveIncident blindly — index 0 may be a
	// walk-scoped record while the incident that raised the reason sits further
	// along. Switch on the incident's Kind.
	ReasonIncident
	// ReasonSignal means a token is waiting on a named signal.
	ReasonSignal
	// ReasonMessage means a token is waiting on a named message.
	ReasonMessage
	// ReasonTimer means the instance is parked on an armed timer, with nothing
	// higher-priority to resolve.
	//
	// ⚠ ON, not merely BESIDE. A timer that is a secondary attachment to work the
	// instance is waiting on through another handler — a boundary arm or a
	// timer-triggered event sub-process arm on a token parked on an in-flight
	// action or child instance — yields to [ReasonAsyncChild] instead. Park's
	// HasArmedTimers still reports it. See primaryTimerPark.
	ReasonTimer
	// ReasonAsyncChild means a token is waiting on a command (typically an async
	// call activity awaiting its child instance's outcome), and no armed timer
	// outranks it.
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
	// HasArmedTimers reports whether the instance has any armed timer a harness
	// may legitimately fire.
	//
	// ⚠ It EXCLUDES a compensation-stall timer. Such a record is a
	// DETECTION deadline, not work the instance waits on: counting it would make
	// [AutoTimers] fire it by itself and manufacture the very stall incident the
	// window exists to detect.
	//
	// The predicate is engine.InstanceState.HasArmedTimers rather than a
	// re-derivation here. A consumer CAN read the kinds — [engine.TimerKind] is
	// exported and [engine.InstanceState.TimerWaiters] surfaces one per armed
	// timer — but which kinds are excludable is the engine's decision
	// to make and to keep current, not a rule a harness should copy.
	HasArmedTimers bool
	// Incidents holds the instance's open incident records — EVERY one of them,
	// including a walk-scoped compensation record that was outranked by a lower
	// rung (such a record raises [ReasonIncident] only when nothing
	// actionable is parked). It is the harness's visibility surface, and it is
	// populated from the snapshot before any rung is evaluated, so it is
	// independent of which rung fired — nothing is hidden by the rung split.
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
//
//	terminal > human-task > TOKEN-SCOPED incident > signal > message > timer >
//	async-child > WALK-SCOPED incident > unknown
//
// [ReasonIncident] therefore appears TWICE, at opposite ends. That split, and one
// other rung, exist so a park the harness CAN drive is never masked by one it
// cannot:
//
//   - incident, split by SCOPE: a token in engine.TokenIncident, or an incident
//     naming a token, keeps the high rung — ResolveIncident is the only thing that
//     frees it. A WALK-SCOPED incident (empty TokenID:
//     engine.IncidentCompensationStall, engine.IncidentCompensationFailed) is a
//     record of a failure rather than an actionable park — ResolveIncident refuses
//     the kind by design — so it drops to the last rung, where it still outranks
//     [ReasonUnknown] and nothing else. See [tokenScopedIncident] and
//     [ReasonIncident].
//   - timer vs async-child: a timer outranks a command wait only when the timer
//     is what the instance waits ON, not when it is a boundary or
//     event-sub-process arm attached to work some other handler resolves.
//     [primaryTimerPark] draws that line.
//
// ⚠ The high rung's position is load-bearing and must not be merged downwards: a
// token-parked engine.IncidentAction still outranks an armed timer, which is what
// keeps a stuck token from being papered over by a timer the harness happens to
// be able to fire.
//
// AwaitingSignals and AwaitingMessages come from the engine's own authorities,
// [engine.InstanceState.SignalWaiters] and [engine.InstanceState.MessageWaiters],
// so an instance parked purely on a boundary, event-gateway or event-subprocess
// ARM classifies as a signal/message park like a token await does — such arms set
// no field on any token.
//
// KNOWN GAP — a compensation-walk park still has no reason of its own, EXCEPT
// while its retry backoff is armed. An instance waiting on an in-flight
// reverse-order compensation walk sits at [engine.StatusCompensating] with zero
// tokens: the walk is awaited through the engine's compensation cursor, which no
// token carries, so ReasonAsyncChild (which requires a token with AwaitCommand)
// does not match and the park falls through to [ReasonUnknown]. A handler with no
// case for it Passes, and drive then reports [ErrUnhandledPark]. Measured
// reason="unknown" on both a hand-built snapshot and a real mid-walk state
// produced by the engine (the routes reaching that state have since widened;
// it is reachable through whole-instance rollback regardless). It bites a
// consumer classifying a STORED mid-walk snapshot: measured, the default
// synchronous drive loop completes the walk inside one ApplyTrigger and never
// parks on it.
//
// The gap is narrowed at one point, measured on an engine-built state: a
// walk paused on an armed engine.TimerCompensationRetry backoff classifies
// [ReasonTimer], because that timer is forward work the engine reports through
// HasArmedTimers. [AutoTimers] fires it and the walk re-dispatches. Every other
// walk park is untouched — a walk whose engine.IncidentCompensationStall has been
// raised still classifies [ReasonIncident] naming the stalled record's node
// (nothing else is parked, so the last rung is the one that fires), and a walk
// with an action merely in flight still classifies [ReasonUnknown].
//
// Closing the gap properly still means a ReasonCompensation whose [Park] surfaces
// the awaited command id so a handler can deliver the walk's ActionCompleted —
// deliberately not done here.
func Classify(state engine.InstanceState) Park {
	p := Park{
		State:          state,
		HasArmedTimers: state.HasArmedTimers(),
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
	// silently dropping boundary, event-gateway and event-subprocess arms.
	// The authorities may return duplicates; Park documents these fields as
	// distinct, so dedup here.
	p.AwaitingSignals = distinctStrings(state.SignalWaiters())
	p.AwaitingMessages = distinctWaiters(state.MessageWaiters())

	// Primary reason, in priority order.
	switch {
	case IsTerminal(state.Status):
		p.Reason = ReasonTerminal
	case len(p.OpenTasks) > 0:
		p.Reason = ReasonHumanTask
		p.Node = p.OpenTasks[0].NodeID
	// TOKEN-SCOPED incidents only. A token stuck here is unblocked by
	// ResolveIncident and by nothing below it, so it keeps the high rung. The
	// WALK-SCOPED half sits at the BOTTOM of the ladder — see the penultimate
	// case and [tokenScopedIncident].
	case hasIncidentToken(state.Tokens) || tokenScopedIncident(state.Incidents) != nil:
		p.Reason = ReasonIncident
		p.Node = incidentNode(state)
	case len(p.AwaitingSignals) > 0:
		p.Reason = ReasonSignal
		p.Node = awaitNode(state, func(t engine.Token) bool { return t.AwaitSignal != "" })
	case len(p.AwaitingMessages) > 0:
		p.Reason = ReasonMessage
		p.Node = awaitNode(state, func(t engine.Token) bool { return t.AwaitMessage != "" })
	// A timer outranks a command wait only when the timer is what the instance is
	// actually waiting ON. See [primaryTimerPark].
	case p.HasArmedTimers && (primaryTimerPark(state) || !hasCommandWait(state.Tokens)):
		p.Reason = ReasonTimer
		p.Node = firstNodeWhere(state.Tokens, func(t engine.Token) bool { return t.State == engine.TokenWaiting })
	case hasCommandWait(state.Tokens):
		p.Reason = ReasonAsyncChild
		p.Node = firstNodeWhere(state.Tokens, func(t engine.Token) bool {
			return t.State == engine.TokenWaiting && t.AwaitCommand != ""
		})
	// WALK-SCOPED incidents (every incident left over once the rung above named
	// none that carries a TokenID). Deliberately the LAST rung before
	// [ReasonUnknown]: such a record is a report that something failed, not a park
	// a harness has any verb for, so it must not displace one that IS actionable.
	// It stays above ReasonUnknown because with nothing else parked it is the most
	// informative thing to report.
	case len(state.Incidents) > 0:
		p.Reason = ReasonIncident
		p.Node = incidentNode(state)
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
// fallback is the arm-derived case: a boundary, event-gateway or
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
// It exists for the timer promotion in harnessEnv.classify. Once
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

// tokenScopedIncident returns the first incident that NAMES a token, or nil when
// every incident present is walk-scoped (or there are none).
//
// It is the predicate of [Classify]'s HIGH incident rung, and the thing the LAST
// rung is defined as the complement of. Both directions are load-bearing:
//
//   - A walk-scoped record must NOT mask a park the harness can drive. The engine
//     raises an engine.IncidentCompensationFailed on every failed compensation
//     action, for every consumer, with retry switched off — and no route retires
//     it on an instance the walk RESUMED (a throw-targeted or partial rollback),
//     so it is permanent. While it sat on the high rung, an instance parked on an
//     ordinary timer, signal, message or command wait classified as an incident
//     the harness had no verb for and drive reported [ErrUnhandledPark].
//   - A walk-scoped record must STILL raise the reason when nothing actionable is
//     parked. engine.IncidentCompensationStall reaching [ReasonIncident] is an
//     intended consequence, and consumers are told to handle a stall by
//     switching on the incident's Kind. Dropping the rung to [ReasonUnknown]
//     would silently retract that, so the last rung sits ABOVE unknown rather
//     than replacing it.
//
// ⚠ An earlier revision expressed the first point as a yield term on the single
// rung — a walk-scoped incident raised the reason only when [Park.HasArmedTimers]
// was false. That could not be made correct in place: HasArmedTimers is not the
// timer rung's own condition (which additionally requires
// [primaryTimerPark] || !hasCommandWait), so a walk-scoped incident beside a
// SECONDARY timer arm yielded to a rung that then declined the park, and the term
// said nothing at all about the signal, message and command-wait rungs it was
// also outranking. Splitting by scope subsumes every case the term was reaching
// for and needs no timer term at all.
//
// The scan is deliberately not a look at Incidents[0]: a walk-scoped record and a
// token-parked one coexist routinely — a cancel walk raises its own incident
// while an earlier action failure still sits open — and slice position says
// nothing about which is which. The runtime's cause-of-death resolvers had the
// same positional defect.
//
// The test is TokenID rather than Kind: it is the property the rung actually
// depends on (is a token stuck here?), so a compensation kind added later needs
// no change here to be treated correctly.
func tokenScopedIncident(incidents []engine.Incident) *engine.Incident {
	for i := range incidents {
		if incidents[i].TokenID != "" {
			return &incidents[i]
		}
	}
	return nil
}

// incidentNode names the node the incident park sits on. It serves BOTH incident
// rungs, and its order is the order in which their predicates can fire: the first
// token-scoped incident, else a token sitting in engine.TokenIncident (the high
// rung's two disjuncts), else — the last rung's walk-scoped case — the first
// incident of any kind.
//
// The last fallback is not decorative. It is how a stall park keeps naming the
// record it was compensating (measured: node "b"), which a plain Incidents[0] read
// gave it in an earlier revision and which dropping the fallback silently
// took away.
func incidentNode(state engine.InstanceState) string {
	stuckTokenNode := func() string {
		return firstNodeWhere(state.Tokens, func(t engine.Token) bool { return t.State == engine.TokenIncident })
	}
	if len(state.Incidents) == 0 {
		return stuckTokenNode()
	}
	if inc := tokenScopedIncident(state.Incidents); inc != nil {
		return inc.NodeID
	}
	if id := stuckTokenNode(); id != "" {
		return id
	}
	return state.Incidents[0].NodeID
}

// primaryTimerPark reports whether an armed timer is the thing the instance is
// waiting ON, as opposed to a SECONDARY attachment to work it is waiting on
// through some other handler. Only a primary timer may outrank a command wait:
// otherwise [AutoTimers] fires an activity's boundary timer to its timeout
// instead of letting the action handler resolve the park, contradicting the
// contract AutoTimers documents.
//
// Two shapes are primary, and they are measured, not argued:
//
//   - A waiting token whose AwaitCommand IS one of the armed timer ids. That is
//     the plain timer intermediate catch (token parks on the timer id) and the
//     retry backoff (token parks on the TimerRetry record's id). This is a set
//     membership test against the engine's own enumeration
//     ([engine.InstanceState.TimerWaiters]), NOT an attempt to read meaning out
//     of an identity, which is forbidden. It is also why Token.AwaitTimer is not
//     used here: AwaitTimer is unset on a token persisted by an older version
//     and unset on a retry park, while AwaitCommand has always been persisted.
//   - An in-flight event-based gateway carrying a timer arm. Its token parks on
//     an "evtgw:" sentinel no handler can deliver, so the timer race IS the park;
//     yielding to that command wait would leave the gateway unresolvable by
//     AutoTimers, which is what it did before HasArmedTimers was widened.
//
// Everything else — a timer boundary arm, a timer-triggered event sub-process
// arm — is secondary. Measured on engine-built states (instance "i"):
//
//	svc[action work] ⊸ bnd[timer 3h]     token awaitCmd="i-c1"        waiter "i-tm1" → secondary
//	evtsub[start timer 5h] + svc action  token awaitCmd="i-c1"        waiter "i-tm1" → secondary
//	t-catch[timer 1h]                    token awaitCmd="i-tm1"       waiter "i-tm1" → primary
//	svc[retry] after a retryable failure token awaitCmd="i-tm1"       waiter "i-tm1" → primary
//	egw ⊸ t-arm[timer 1h]                token awaitCmd="evtgw:i-t1"  waiter "i-tm1" → primary
func primaryTimerPark(state engine.InstanceState) bool {
	if len(state.TimerArmedEventWaiters()) > 0 {
		return true
	}
	armed := make(map[string]struct{})
	for _, w := range state.TimerWaiters() {
		armed[w.TimerID] = struct{}{}
	}
	for _, t := range state.Tokens {
		if t.State != engine.TokenWaiting || t.AwaitCommand == "" {
			continue
		}
		if _, ok := armed[t.AwaitCommand]; ok {
			return true
		}
	}
	return false
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
