package definition_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition"
)

func TestDefaultRetryPolicy(t *testing.T) {
	p := definition.DefaultRetryPolicy()
	if p.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", p.MaxAttempts)
	}
	if p.InitialInterval != time.Second {
		t.Fatalf("InitialInterval = %v, want 1s", p.InitialInterval)
	}
	if p.BackoffCoef != 2.0 {
		t.Fatalf("BackoffCoef = %v, want 2.0", p.BackoffCoef)
	}
	if p.MaxInterval != 100*time.Second {
		t.Fatalf("MaxInterval = %v, want 100s", p.MaxInterval)
	}
}

func TestRetryPolicyBackoff(t *testing.T) {
	p := definition.RetryPolicy{InitialInterval: time.Second, BackoffCoef: 2.0, MaxInterval: 10 * time.Second}
	cases := []struct {
		name    string
		attempt int
		assert  func(t *testing.T, d time.Duration)
	}{
		{"attempt0", 0, func(t *testing.T, d time.Duration) {
			if d != time.Second {
				t.Fatalf("got %v, want 1s", d)
			}
		}},
		{"attempt1", 1, func(t *testing.T, d time.Duration) {
			if d != 2*time.Second {
				t.Fatalf("got %v, want 2s", d)
			}
		}},
		{"attempt2", 2, func(t *testing.T, d time.Duration) {
			if d != 4*time.Second {
				t.Fatalf("got %v, want 4s", d)
			}
		}},
		{"capped", 10, func(t *testing.T, d time.Duration) {
			if d != 10*time.Second {
				t.Fatalf("got %v, want capped 10s", d)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, p.Backoff(tc.attempt))
		})
	}
}

func TestRetryPolicyBackoffZeroInitial(t *testing.T) {
	p := definition.RetryPolicy{InitialInterval: 0, BackoffCoef: 2.0, MaxInterval: 10 * time.Second}
	if d := p.Backoff(0); d != 0 {
		t.Fatalf("expected 0 for zero InitialInterval, got %v", d)
	}
}

func TestRetryPolicyIsNonRetryable(t *testing.T) {
	p := definition.RetryPolicy{NonRetryableErrors: []string{"validation", "not found"}}
	if !p.IsNonRetryable("input validation failed") {
		t.Fatal("expected substring match to be non-retryable")
	}
	if p.IsNonRetryable("timeout") {
		t.Fatal("unexpected non-retryable")
	}
}

func TestRetryPolicyNormalizeFillsZeros(t *testing.T) {
	got := definition.RetryPolicy{MaxAttempts: 5}.Normalize()
	if got.MaxAttempts != 5 {
		t.Fatalf("MaxAttempts overwritten: %d", got.MaxAttempts)
	}
	if got.InitialInterval != time.Second || got.BackoffCoef != 2.0 {
		t.Fatalf("zero fields not filled from default: %+v", got)
	}
}

func TestRetryPolicyNormalizePreservesUnlimited(t *testing.T) {
	// MaxAttempts==0 means unlimited and must be preserved by Normalize.
	got := definition.RetryPolicy{MaxAttempts: 0}.Normalize()
	if got.MaxAttempts != 0 {
		t.Fatalf("MaxAttempts==0 (unlimited) was overwritten to %d", got.MaxAttempts)
	}
}

func TestRetryPolicyNormalizeNegativeMaxAttempts(t *testing.T) {
	// MaxAttempts<0 is treated as unset and replaced with the default (3).
	got := definition.RetryPolicy{MaxAttempts: -1}.Normalize()
	if got.MaxAttempts != 3 {
		t.Fatalf("negative MaxAttempts not replaced with default 3: %d", got.MaxAttempts)
	}
}
