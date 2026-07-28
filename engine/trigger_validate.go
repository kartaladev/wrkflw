package engine

import "fmt"

// triggerKey names the identity field validateTriggerKey requires to be
// non-empty and reads that field off the trigger.
//
// The bare type assertion inside read is safe only while a row's key names the
// same concrete type the assertion names. That pairing is not expressible in the
// type system, so TestValidateTriggerKindsAreExhaustive calls read on every row
// to pin it: a mis-paired row panics there rather than inside Step.
type triggerKey struct {
	field string
	read  func(Trigger) string
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
		"engine.ActionCompleted":         {"CommandID", func(t Trigger) string { return t.(ActionCompleted).CommandID }},
		"engine.ActionFailed":            {"CommandID", func(t Trigger) string { return t.(ActionFailed).CommandID }},
		"engine.SubInstanceCompleted":    {"CommandID", func(t Trigger) string { return t.(SubInstanceCompleted).CommandID }},
		"engine.SubInstanceFailed":       {"CommandID", func(t Trigger) string { return t.(SubInstanceFailed).CommandID }},
		"engine.HumanCompleted":          {"TaskID", func(t Trigger) string { return t.(HumanCompleted).TaskID }},
		"engine.HumanClaimed":            {"TaskID", func(t Trigger) string { return t.(HumanClaimed).TaskID }},
		"engine.HumanReassigned":         {"TaskID", func(t Trigger) string { return t.(HumanReassigned).TaskID }},
		"engine.HumanCandidatesResolved": {"TaskID", func(t Trigger) string { return t.(HumanCandidatesResolved).TaskID }},
		"engine.SignalReceived":          {"Name", func(t Trigger) string { return t.(SignalReceived).Name }},
		"engine.MessageReceived":         {"Name", func(t Trigger) string { return t.(MessageReceived).Name }},
		"engine.ResolveIncident":         {"IncidentID", func(t Trigger) string { return t.(ResolveIncident).IncidentID }},
	}

	exemptTriggerKinds = map[string]string{
		// A stale TimerFired must stay a clean no-op: timers are inherently racy
		// with other completion paths and can arrive late. Pinned by
		// TestTimerFiredStaleTokenIsNoop. The state-layer guards already give an
		// empty TimerID exactly that behaviour.
		"engine.TimerFired": "an empty TimerID is a documented stale-timer no-op",
		// An empty StartNodeID resolves the definition's manual start.
		"engine.StartInstance": "an empty StartNodeID selects the manual start",
		// An empty ToNode means full rollback; an empty ReverseNode means terminate.
		"engine.CompensateRequested": "empty ToNode/ReverseNode mean full rollback",
		// Carries no identity key at all.
		"engine.CancelRequested": "carries no identity key",
	}
)

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
