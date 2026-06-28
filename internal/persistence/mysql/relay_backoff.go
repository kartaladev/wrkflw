package mysql

import "time"

// RelayBackoff returns the capped exponential delay to wait before the given
// zero-based retry attempt: min(base × 2^retryCount, maxInterval).
//
// It is pure — it reads no clock and holds no state — so the relay can compute a
// row's next_attempt_at deterministically from its persisted retry_count. A
// negative retryCount is treated as 0, and a non-positive base yields 0.
func RelayBackoff(retryCount int, base, maxInterval time.Duration) time.Duration {
	if base <= 0 || retryCount < 0 {
		return 0
	}
	delay := base
	for range retryCount {
		delay *= 2
		// Stop doubling once we exceed the cap to avoid overflow on large counts.
		if delay >= maxInterval {
			return maxInterval
		}
	}
	if delay > maxInterval {
		return maxInterval
	}
	return delay
}
