package model

import (
	"math"
	"strings"
	"time"
)

// RetryPolicy describes how a failed action.ServiceAction is retried when its
// execution returns an error. The zero value is not directly usable; call
// [DefaultRetryPolicy] to get safe defaults or call [RetryPolicy.Normalize]
// on a partially-populated value to fill any zero fields from those defaults.
type RetryPolicy struct {
	// MaxAttempts is the total number of execution attempts including the first
	// (non-retry) attempt. Default 3. A value of 0 means unlimited attempts.
	// Negative values are treated as unset by [RetryPolicy.Normalize].
	MaxAttempts int

	// InitialInterval is the delay before the first retry. Default 1s.
	// A zero or negative value causes [RetryPolicy.Backoff] to return 0.
	InitialInterval time.Duration

	// BackoffCoef is the per-attempt exponential multiplier applied to
	// InitialInterval. Default 2.0 (doubles each attempt). Values below 1.0
	// are replaced with the default by [RetryPolicy.Normalize].
	BackoffCoef float64

	// MaxInterval is the per-attempt cap on the delay returned by
	// [RetryPolicy.Backoff]. Default 100s (100 × default InitialInterval).
	// A zero or negative value disables the cap.
	MaxInterval time.Duration

	// MaxElapsed is the total time budget across all attempts. When the elapsed
	// time exceeds this value no further retries are issued. Zero means no cap.
	MaxElapsed time.Duration

	// NonRetryableErrors is a list of error-message substrings. When the error
	// from a failed attempt contains any of these substrings, retrying is aborted
	// and the error is propagated immediately.
	NonRetryableErrors []string
}

// DefaultRetryPolicy returns a RetryPolicy with Temporal-style defaults and a
// finite attempt cap suited for most workflow service actions.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: time.Second,
		BackoffCoef:     2.0,
		MaxInterval:     100 * time.Second,
	}
}

// Backoff returns the capped, un-jittered delay to wait before the zero-based
// attempt number given by attempt (0 = first retry, 1 = second retry, …).
//
// The formula is: delay = InitialInterval × BackoffCoef^attempt, capped at
// MaxInterval when MaxInterval > 0.
//
// Backoff returns 0 when InitialInterval is zero or negative.
func (p RetryPolicy) Backoff(attempt int) time.Duration {
	if p.InitialInterval <= 0 {
		return 0
	}
	d := float64(p.InitialInterval) * math.Pow(p.BackoffCoef, float64(attempt))
	if p.MaxInterval > 0 && d > float64(p.MaxInterval) {
		return p.MaxInterval
	}
	return time.Duration(d)
}

// IsNonRetryable reports whether errMsg contains any of the substrings listed
// in NonRetryableErrors. An empty substring entry is silently ignored.
func (p RetryPolicy) IsNonRetryable(errMsg string) bool {
	for _, s := range p.NonRetryableErrors {
		if s != "" && strings.Contains(errMsg, s) {
			return true
		}
	}
	return false
}

// Normalize returns a copy of p with every zero-valued field replaced by the
// corresponding value from [DefaultRetryPolicy].
//
// Special cases:
//   - MaxAttempts == 0 is preserved (it means unlimited); only a negative value
//     is treated as unset and replaced with the default (3).
//   - BackoffCoef < 1.0 is treated as unset and replaced with the default (2.0).
func (p RetryPolicy) Normalize() RetryPolicy {
	d := DefaultRetryPolicy()
	if p.MaxAttempts < 0 {
		p.MaxAttempts = d.MaxAttempts
	}
	if p.InitialInterval <= 0 {
		p.InitialInterval = d.InitialInterval
	}
	if p.BackoffCoef < 1.0 {
		p.BackoffCoef = d.BackoffCoef
	}
	if p.MaxInterval <= 0 {
		p.MaxInterval = d.MaxInterval
	}
	return p
}
