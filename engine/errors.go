package engine

import (
	"errors"
	"fmt"
)

// ErrInvalidTransition classifies a trigger that cannot be applied because the
// targeted instance/token is not in a state that accepts it. The instance exists —
// this is a conflict, not a "not found". Consumers classify wrong-state transitions
// with errors.Is(err, ErrInvalidTransition); the service layer maps it to ErrConflict
// and transports map it to HTTP 422.
var ErrInvalidTransition = errors.New("workflow-engine: invalid state transition")

var (
	// ErrUnknownTrigger is returned when a trigger type has no handler. It is an
	// infrastructure/programming error, not a wrong-state transition.
	ErrUnknownTrigger = errors.New("workflow-engine: unknown trigger")

	// ErrTokenNotFound is returned when a trigger targets a command/task id that
	// is not awaiting. It is one kind of invalid transition and wraps
	// ErrInvalidTransition so errors.Is holds for both sentinels.
	ErrTokenNotFound = fmt.Errorf("workflow-engine: no token awaiting command: %w", ErrInvalidTransition)

	// ErrNoMatchingFlow is returned when a gateway has no matching/default outgoing
	// flow. It is a definition/data error, not a wrong-state transition.
	ErrNoMatchingFlow = errors.New("workflow-engine: no matching outgoing flow")

	// ErrManualTaskPayload is returned when a wait-mode manual UserTask is completed
	// with a non-empty output, outcome, or note. A manual task is a form-less
	// checkpoint; supplying a payload is a caller error. Immediate-mode manual tasks
	// never take a trigger. See ADR-0118.
	ErrManualTaskPayload = errors.New("workflow-engine: manual user task cannot carry a completion payload")

	// ErrInvalidOutcome is returned when a UserTask completion carries an outcome
	// the node does not declare. Validation fails closed: once a node declares
	// outcomes, only those are accepted. A node declaring none is unconstrained —
	// any outcome at all completes it. See ADR-0146.
	ErrInvalidOutcome = errors.New("workflow-engine: completion outcome is not declared by the user task")

	// ErrOutcomeRequired is returned when a UserTask that declares outcomes is
	// completed without one. A declared set is a closed, mandatory value domain:
	// the outcome typically routes a downstream exclusive gateway, so accepting a
	// blank one would publish no routing variable and fail the step later with
	// ErrNoMatchingFlow. It is deliberately distinct from ErrInvalidOutcome so a
	// caller can tell "you sent nothing" from "you sent a value I do not accept".
	// A manual UserTask is exempt — it is forbidden from declaring outcomes
	// (model.ErrManualTaskOutcome) and completes on a bare trigger. See ADR-0146.
	ErrOutcomeRequired = errors.New("workflow-engine: user task requires a completion outcome")

	// ErrEmptyTriggerKey is returned when an inbound trigger's identity key is
	// empty. An identity key names one specific record; the empty string names
	// none, so the trigger cannot be dispatched.
	//
	// It is deliberately NOT wrapped in ErrInvalidTransition: the instance state is
	// irrelevant here, the trigger itself is malformed. Transports classify it 400,
	// alongside the other caller-correctable input sentinels. See ADR-0152.
	ErrEmptyTriggerKey = errors.New("workflow-engine: trigger identity key is empty")
)
