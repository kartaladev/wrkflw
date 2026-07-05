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

	// ErrTokenNotFound is returned when a trigger targets a command/task token that
	// is not awaiting. It is one kind of invalid transition and wraps
	// ErrInvalidTransition so errors.Is holds for both sentinels.
	ErrTokenNotFound = fmt.Errorf("workflow-engine: no token awaiting command: %w", ErrInvalidTransition)

	// ErrNoMatchingFlow is returned when a gateway has no matching/default outgoing
	// flow. It is a definition/data error, not a wrong-state transition.
	ErrNoMatchingFlow = errors.New("workflow-engine: no matching outgoing flow")
)
