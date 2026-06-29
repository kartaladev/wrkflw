package action_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
)

func TestIsRetryable(t *testing.T) {
	tests := map[string]struct {
		err    error
		assert func(t *testing.T, retryable bool)
	}{
		"nil is retryable-default (no error to inspect)": {
			nil,
			func(t *testing.T, retryable bool) {
				if !retryable {
					t.Fatalf("IsRetryable(nil) = false, want true")
				}
			},
		},
		"plain error is retryable": {
			errors.New("boom"),
			func(t *testing.T, retryable bool) {
				if !retryable {
					t.Fatalf("plain error: IsRetryable = false, want true")
				}
			},
		},
		"NonRetryable marks not retryable": {
			action.NonRetryable(errors.New("4xx")),
			func(t *testing.T, retryable bool) {
				if retryable {
					t.Fatalf("NonRetryable: IsRetryable = true, want false")
				}
			},
		},
		"NonRetryable wrapped deeper still detected": {
			errors.Join(errors.New("ctx"), action.NonRetryable(errors.New("4xx"))),
			func(t *testing.T, retryable bool) {
				if retryable {
					t.Fatalf("wrapped NonRetryable: IsRetryable = true, want false")
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, action.IsRetryable(tc.err))
		})
	}
}

func TestNonRetryableUnwraps(t *testing.T) {
	sentinel := errors.New("original")
	wrapped := action.NonRetryable(sentinel)
	if !errors.Is(wrapped, sentinel) {
		t.Fatalf("errors.Is(NonRetryable(x), x) = false, want true")
	}
	if action.NonRetryable(nil) != nil {
		t.Fatalf("NonRetryable(nil) != nil")
	}
}
