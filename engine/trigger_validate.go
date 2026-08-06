package engine

import (
	"fmt"
	"log/slog"
)

// triggerKey names a trigger's identity field, reads that field off the
// trigger, and names the log attribute the same value is emitted under.
//
// The bare type assertion inside read is safe only while a row's key names the
// same concrete type the assertion names. That pairing is not expressible in the
// type system, so TestValidateTriggerKindsAreExhaustive calls read on every row
// that registers one — in BOTH classification sets, validated and exempt — so a
// mis-paired row panics there rather than inside Step. It pins logKey the same
// way. That test is the only thing standing between a mis-paired row and a
// hot-path panic, so a new accessor MUST be reachable from it: while the loop
// skipped exempt rows, a mis-paired exempt accessor passed the whole suite
// green.
type triggerKey struct {
	// field is the Go field name, used in ErrEmptyTriggerKey's message.
	field string
	// logKey is the snake_case slog attribute name the same value is logged
	// under by dispatch's terminal guard (ADR-0165). It is a column of THIS row
	// rather than a derived table so that naming the field, extracting its value
	// and naming its log attribute stay ONE registration point — a variant
	// cannot gain an accessor while its log attribute silently goes unnamed.
	logKey string
	read   func(Trigger) string
}

// exemptTriggerKind records why a variant is exempt from validateTriggerKey's
// non-empty check and, optionally, how to read its identity field anyway.
//
// Exempt means "may legitimately be empty", NOT "has no identity". TimerFired
// carries a TimerID that is merely allowed to be blank, and dispatch's terminal
// guard must still log it — an operator asking "why did my timer do nothing"
// is not served by instance_id/trigger/status alone (ADR-0165). key's zero
// value (a nil read) means the variant genuinely carries no single identity
// field, and the guard then omits the attribute entirely rather than emitting
// an empty one.
type exemptTriggerKind struct {
	reason string
	key    triggerKey
}

// validatedTriggerKinds maps a trigger's type name to the identity field
// validateTriggerKey requires to be non-empty, paired with the accessor that
// reads it. The map is the single registration point: naming the field and
// extracting its value are one row, so a variant cannot be registered for the
// error message while its value silently goes unread.
//
// exemptTriggerKinds lists the variants deliberately NOT validated, each with the
// reason. Together the two sets must cover every variant of the sealed Trigger
// interface; TestValidateTriggerKindsAreExhaustive enforces that, so a variant
// added later cannot silently fall through validateTriggerKey's default arm.
var (
	validatedTriggerKinds = map[string]triggerKey{
		"engine.ActionCompleted":         {"CommandID", "command_id", func(t Trigger) string { return t.(ActionCompleted).CommandID }},
		"engine.ActionFailed":            {"CommandID", "command_id", func(t Trigger) string { return t.(ActionFailed).CommandID }},
		"engine.SubInstanceCompleted":    {"CommandID", "command_id", func(t Trigger) string { return t.(SubInstanceCompleted).CommandID }},
		"engine.SubInstanceFailed":       {"CommandID", "command_id", func(t Trigger) string { return t.(SubInstanceFailed).CommandID }},
		"engine.HumanCompleted":          {"TaskID", "task_id", func(t Trigger) string { return t.(HumanCompleted).TaskID }},
		"engine.HumanClaimed":            {"TaskID", "task_id", func(t Trigger) string { return t.(HumanClaimed).TaskID }},
		"engine.HumanReassigned":         {"TaskID", "task_id", func(t Trigger) string { return t.(HumanReassigned).TaskID }},
		"engine.HumanCandidatesResolved": {"TaskID", "task_id", func(t Trigger) string { return t.(HumanCandidatesResolved).TaskID }},
		"engine.SignalReceived":          {"Name", "signal_name", func(t Trigger) string { return t.(SignalReceived).Name }},
		"engine.MessageReceived":         {"Name", "message_name", func(t Trigger) string { return t.(MessageReceived).Name }},
		"engine.ResolveIncident":         {"IncidentID", "incident_id", func(t Trigger) string { return t.(ResolveIncident).IncidentID }},
	}

	exemptTriggerKinds = map[string]exemptTriggerKind{
		// A stale TimerFired must stay a clean no-op: timers are inherently racy
		// with other completion paths and can arrive late. Pinned by
		// TestTimerFiredStaleTokenIsNoop. The state-layer guards already give an
		// empty TimerID exactly that behaviour.
		//
		// It carries an identity accessor anyway: TimerFired is rejectSilently, so
		// it is one of the variants dispatch's terminal guard actually logs, and
		// timer_id is the field an operator needs there (ADR-0165).
		"engine.TimerFired": {
			reason: "an empty TimerID is a documented stale-timer no-op",
			key:    triggerKey{"TimerID", "timer_id", func(t Trigger) string { return t.(TimerFired).TimerID }},
		},
		// An empty StartNodeID resolves the definition's manual start.
		"engine.StartInstance": {
			reason: "an empty StartNodeID selects the manual start",
			key:    triggerKey{"StartNodeID", "start_node_id", func(t Trigger) string { return t.(StartInstance).StartNodeID }},
		},
		// An empty ToNode means full rollback; an empty ReverseNode means terminate.
		//
		// It carries an accessor even though the variant has TWO identity fields,
		// because it is logged: dispatch's guard logs BOTH refusal flavours, and a
		// rollback carrying resume intent is rejectWithError, so a refused targeted
		// rollback DID produce a line with no node on it — leaving an operator
		// unable to tell which rollback was refused. (The comment here previously
		// claimed the guard never logs this variant, reasoning that it is "never
		// rejectSilently"; running it disproved both halves.)
		//
		// One attribute, ToNode first, mirroring walkMode's precedence when both
		// are somehow set. On the rejectWithError path exactly one is non-empty —
		// no constructor sets both — and on the allowOnTerminal path (plain full
		// rollback, both empty) the read returns "" and the attribute is omitted
		// entirely, which is also the only path that is not logged at all.
		"engine.CompensateRequested": {
			reason: "empty ToNode/ReverseNode mean full rollback",
			key: triggerKey{"ToNode", "rollback_target", func(t Trigger) string {
				c := t.(CompensateRequested)
				if c.ToNode != "" {
					return c.ToNode
				}
				return c.ReverseNode
			}},
		},
		// Carries no identity key at all, so the guard emits no fourth attribute.
		"engine.CancelRequested": {reason: "carries no identity key"},
	}
)

// triggerIdentityAttr returns the slog attribute naming the trigger's identity
// field and carrying its value — command_id, task_id, timer_id, incident_id and
// so on — reading through the same registry validateTriggerKey uses so there is
// no second mapping to drift.
//
// ok is false when the variant registers no accessor (CancelRequested) or its
// value is empty, so the caller omits the attribute entirely rather than logging
// an empty string. validateTriggerKey has already rejected an empty key on any
// VALIDATED variant by the time dispatch runs, but this does not rely on that:
// an unregistered variant degrades to "no attribute", never a panic.
//
// It is a LOGGING helper only and must never influence routing or policy.
func triggerIdentityAttr(trg Trigger) (slog.Attr, bool) {
	name := triggerTypeName(trg)
	k, ok := validatedTriggerKinds[name]
	if !ok {
		ex, exempt := exemptTriggerKinds[name]
		if !exempt || ex.key.read == nil {
			return slog.Attr{}, false
		}
		k = ex.key
	}
	value := k.read(trg)
	if value == "" {
		return slog.Attr{}, false
	}
	return slog.String(k.logKey, value), true
}

// triggerTypeName returns the trigger's concrete type name (e.g.
// "engine.SignalReceived"), the key used by the classification sets above.
func triggerTypeName(trg Trigger) string { return fmt.Sprintf("%T", trg) }

// validateTriggerKey rejects a trigger whose identity key is empty.
//
// An identity key names one specific record; the empty string names none. Before
// ADR-0152 an empty key reached the state-layer lookups, where it matched every
// record whose corresponding field was also empty — a SignalReceived with no name
// resumed every token not awaiting a signal, and an empty name matched an
// error-boundary arm, interrupting a live activity. The state helpers now refuse an
// empty key on their own; this is the outer layer, so a consumer that builds a
// malformed trigger gets a named error instead of a silent no-op.
//
// MessageReceived validates Name only — an empty CorrelationKey means "uncorrelated".
func validateTriggerKey(trg Trigger) error {
	k, ok := validatedTriggerKinds[triggerTypeName(trg)]
	if !ok {
		return nil
	}
	if k.read(trg) == "" {
		return fmt.Errorf("%w: %T.%s", ErrEmptyTriggerKey, trg, k.field)
	}
	return nil
}
