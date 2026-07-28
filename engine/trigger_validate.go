package engine

import "fmt"

// validatedTriggerKinds maps a trigger's type name to the identity field
// validateTriggerKey requires to be non-empty.
//
// exemptTriggerKinds lists the variants deliberately NOT validated, each with the
// reason. Together the two sets must cover every variant of the sealed Trigger
// interface; TestValidateTriggerKindsAreExhaustive enforces that, so a variant
// added later cannot silently fall through validateTriggerKey's default arm.
var (
	validatedTriggerKinds = map[string]string{
		"engine.ActionCompleted":         "CommandID",
		"engine.ActionFailed":            "CommandID",
		"engine.SubInstanceCompleted":    "CommandID",
		"engine.SubInstanceFailed":       "CommandID",
		"engine.HumanCompleted":          "TaskID",
		"engine.HumanClaimed":            "TaskID",
		"engine.HumanReassigned":         "TaskID",
		"engine.HumanCandidatesResolved": "TaskID",
		"engine.SignalReceived":          "Name",
		"engine.MessageReceived":         "Name",
		"engine.ResolveIncident":         "IncidentID",
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
	field, ok := validatedTriggerKinds[triggerTypeName(trg)]
	if !ok {
		return nil
	}
	var key string
	switch t := trg.(type) {
	case ActionCompleted:
		key = t.CommandID
	case ActionFailed:
		key = t.CommandID
	case SubInstanceCompleted:
		key = t.CommandID
	case SubInstanceFailed:
		key = t.CommandID
	case HumanCompleted:
		key = t.TaskID
	case HumanClaimed:
		key = t.TaskID
	case HumanReassigned:
		key = t.TaskID
	case HumanCandidatesResolved:
		key = t.TaskID
	case SignalReceived:
		key = t.Name
	case MessageReceived:
		key = t.Name
	case ResolveIncident:
		key = t.IncidentID
	}
	if key == "" {
		return fmt.Errorf("%w: %T.%s", ErrEmptyTriggerKey, trg, field)
	}
	return nil
}
