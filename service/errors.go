package service

import (
	"errors"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ErrConflict classifies a wrong-state operation — one targeting an instance or
// task that is not in a state where the operation is valid (e.g. claiming a
// completed task, delivering a signal to a finished instance). Transports map it
// to HTTP 422 / gRPC FailedPrecondition. The cause is wrapped, so
// errors.Is(err, ErrConflict) holds while the cause stays inspectable.
var ErrConflict = errors.New("workflow-service: conflicting state")

// isTerminal reports whether an instance status rejects further triggers.
func isTerminal(s engine.Status) bool {
	return s == engine.StatusCompleted || s == engine.StatusFailed || s == engine.StatusTerminated
}
