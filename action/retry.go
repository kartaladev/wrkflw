package action

import "errors"

// RetryableError lets an action error state whether the runtime should retry it.
// An action returns an error implementing this interface (e.g. via NonRetryable)
// to override the runtime's retry-by-default policy.
type RetryableError interface {
	error
	Retryable() bool
}

// NonRetryable wraps err so the runtime will not retry the failed action. The
// returned error unwraps to err, so errors.Is/As see through it. NonRetryable(nil)
// returns nil.
func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryable{err}
}

type nonRetryable struct{ err error }

func (n nonRetryable) Error() string   { return n.err.Error() }
func (n nonRetryable) Unwrap() error   { return n.err }
func (n nonRetryable) Retryable() bool { return false }

// IsRetryable reports whether the runtime should retry a failed action's error.
// A nil error and any plain error are retryable (the historical default); an
// error implementing RetryableError anywhere in its chain overrides that.
func IsRetryable(err error) bool {
	if err == nil {
		return true
	}
	var r RetryableError
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return true
}
