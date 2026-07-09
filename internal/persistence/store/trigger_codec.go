package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// Trigger kind discriminators stored in wrkflw_journal.kind.
const (
	kindStartInstance        = "start_instance"
	kindActionCompleted      = "action_completed"
	kindActionFailed         = "action_failed"
	kindHumanCompleted       = "human_completed"
	kindHumanClaimed         = "human_claimed"
	kindHumanReassigned      = "human_reassigned"
	kindTimerFired           = "timer_fired"
	kindSignalReceived       = "signal_received"
	kindMessageReceived      = "message_received"
	kindSubInstanceCompleted = "sub_instance_completed"
	kindSubInstanceFailed    = "sub_instance_failed"
	kindCompensateRequested  = "compensate_requested"
	kindCancelRequested      = "cancel_requested"
	kindResolveIncident      = "resolve_incident"
)

// AllTriggerKinds lists every trigger kind discriminator the codec handles.
// Used by tests to cross-check exhaustiveness against the sealed Trigger variants.
var AllTriggerKinds = []string{
	kindStartInstance,
	kindActionCompleted,
	kindActionFailed,
	kindHumanCompleted,
	kindHumanClaimed,
	kindHumanReassigned,
	kindTimerFired,
	kindSignalReceived,
	kindMessageReceived,
	kindSubInstanceCompleted,
	kindSubInstanceFailed,
	kindCompensateRequested,
	kindCancelRequested,
	kindResolveIncident,
}

// triggerEnvelope is the flat JSON shape written to wrkflw_journal.payload.
// Fields unused by a given kind are omitted (omitempty). The At field carries
// the trigger's OccurredAt timestamp and is always present.
type triggerEnvelope struct {
	At             time.Time      `json:"at"`
	Vars           map[string]any `json:"vars,omitempty"`
	Output         map[string]any `json:"output,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	CommandID      string         `json:"command_id,omitempty"`
	Err            string         `json:"err,omitempty"`
	Retryable      bool           `json:"retryable,omitempty"`
	TaskToken      string         `json:"task_token,omitempty"`
	Actor          authz.Actor    `json:"actor,omitempty"`
	From           string         `json:"from,omitempty"`
	To             string         `json:"to,omitempty"`
	By             authz.Actor    `json:"by,omitempty"`
	TimerID        string         `json:"timer_id,omitempty"`
	Name           string         `json:"name,omitempty"`
	CorrelationKey string         `json:"correlation_key,omitempty"`
	ToNode         string         `json:"to_node,omitempty"`
	ReverseNode    string         `json:"reverse_node,omitempty"`
	ResetVars      bool           `json:"reset_vars,omitempty"`
	Jitter         float64        `json:"jitter,omitempty"`
	IncidentID     string         `json:"incident_id,omitempty"`
	AddAttempts    int            `json:"add_attempts,omitempty"`
}

// MarshalTrigger serialises a sealed Trigger to JSON and returns the JSON bytes,
// a kind discriminator string (suitable for storing in wrkflw_journal.kind), and
// any encoding error.
//
// Every variant of the sealed engine.Trigger set is handled. Passing an unknown
// (future) variant returns a descriptive error rather than silently producing an
// empty payload.
func MarshalTrigger(t engine.Trigger) ([]byte, string, error) {
	env := triggerEnvelope{At: t.OccurredAt()}
	var kind string
	switch v := t.(type) {
	case engine.StartInstance:
		kind, env.Vars = kindStartInstance, v.Vars
	case engine.ActionCompleted:
		kind, env.CommandID, env.Output = kindActionCompleted, v.CommandID, v.Output
	case engine.ActionFailed:
		kind, env.CommandID, env.Err, env.Retryable, env.Jitter = kindActionFailed, v.CommandID, v.Err, v.Retryable, v.JitterFraction
	case engine.HumanCompleted:
		kind, env.TaskToken, env.Output, env.Actor = kindHumanCompleted, v.TaskToken, v.Output, v.Actor
	case engine.HumanClaimed:
		kind, env.TaskToken, env.Actor = kindHumanClaimed, v.TaskToken, v.Actor
	case engine.HumanReassigned:
		kind, env.TaskToken, env.From, env.To, env.By = kindHumanReassigned, v.TaskToken, v.From, v.To, v.By
	case engine.TimerFired:
		kind, env.TimerID = kindTimerFired, v.TimerID
	case engine.SignalReceived:
		kind, env.Name, env.Payload = kindSignalReceived, v.Name, v.Payload
	case engine.MessageReceived:
		kind, env.Name, env.CorrelationKey, env.Payload = kindMessageReceived, v.Name, v.CorrelationKey, v.Payload
	case engine.SubInstanceCompleted:
		kind, env.CommandID, env.Output = kindSubInstanceCompleted, v.CommandID, v.Output
	case engine.SubInstanceFailed:
		kind, env.CommandID, env.Err = kindSubInstanceFailed, v.CommandID, v.Err
	case engine.CompensateRequested:
		kind, env.ToNode, env.ReverseNode, env.ResetVars = kindCompensateRequested, v.ToNode, v.ReverseNode, v.ResetVars
	case engine.CancelRequested:
		kind = kindCancelRequested
	case engine.ResolveIncident:
		kind, env.IncidentID, env.AddAttempts = kindResolveIncident, v.IncidentID, v.AddAttempts
	default:
		return nil, "", fmt.Errorf("workflow-store: marshal trigger: unhandled variant %T", t)
	}
	data, err := json.Marshal(env)
	if err != nil {
		return nil, "", fmt.Errorf("workflow-store: marshal trigger: %w", err)
	}
	return data, kind, nil
}

// UnmarshalTrigger reconstructs a sealed Trigger from its kind discriminator
// and JSON payload (as written by MarshalTrigger). An unrecognised kind returns
// a descriptive error.
func UnmarshalTrigger(kind string, data []byte) (engine.Trigger, error) {
	var env triggerEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("workflow-store: unmarshal trigger %q: %w", kind, err)
	}
	switch kind {
	case kindStartInstance:
		return engine.NewStartInstance(env.At, env.Vars), nil
	case kindActionCompleted:
		return engine.NewActionCompleted(env.At, env.CommandID, env.Output), nil
	case kindActionFailed:
		return engine.NewActionFailed(env.At, env.CommandID, env.Err, env.Retryable, engine.WithJitter(env.Jitter)), nil
	case kindHumanCompleted:
		return engine.NewHumanCompleted(env.At, env.TaskToken, env.Output, env.Actor), nil
	case kindHumanClaimed:
		return engine.NewHumanClaimed(env.At, env.TaskToken, env.Actor), nil
	case kindHumanReassigned:
		return engine.NewHumanReassigned(env.At, env.TaskToken, env.From, env.To, env.By), nil
	case kindTimerFired:
		return engine.NewTimerFired(env.At, env.TimerID), nil
	case kindSignalReceived:
		return engine.NewSignalReceived(env.At, env.Name, env.Payload), nil
	case kindMessageReceived:
		return engine.NewMessageReceived(env.At, env.Name, env.CorrelationKey, env.Payload), nil
	case kindSubInstanceCompleted:
		return engine.NewSubInstanceCompleted(env.At, env.CommandID, env.Output), nil
	case kindSubInstanceFailed:
		return engine.NewSubInstanceFailed(env.At, env.CommandID, env.Err), nil
	case kindCompensateRequested:
		// Reconstruct all three fields explicitly (rather than routing through
		// NewReverseToStart) so every ToNode/ReverseNode/ResetVars combination
		// written by MarshalTrigger — not just the ReverseToStart happy path —
		// round-trips faithfully.
		cr := engine.NewCompensateRequested(env.At, env.ToNode)
		cr.ReverseNode = env.ReverseNode
		cr.ResetVars = env.ResetVars
		return cr, nil
	case kindCancelRequested:
		return engine.NewCancelRequested(env.At), nil
	case kindResolveIncident:
		return engine.NewResolveIncident(env.At, env.IncidentID, env.AddAttempts), nil
	default:
		return nil, fmt.Errorf("workflow-store: unmarshal trigger: unknown kind %q", kind)
	}
}
