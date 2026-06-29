package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestClassifyDeadLetter(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		lastError string
		assert    func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name:      "deadline string returns timeout",
			lastError: "context deadline exceeded",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "timeout", got)
			},
		},
		{
			name:      "timeout string returns timeout",
			lastError: "operation timeout after 30s",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "timeout", got)
			},
		},
		{
			name:      "connection string returns connection",
			lastError: "failed to establish connection to broker",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "connection", got)
			},
		},
		{
			name:      "dial string returns connection",
			lastError: "dial tcp 10.0.0.1:5432: connect: no route to host",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "connection", got)
			},
		},
		{
			name:      "refused string returns connection",
			lastError: "connect: connection refused",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "connection", got)
			},
		},
		{
			name:      "eof string returns connection",
			lastError: "unexpected EOF reading response",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "connection", got)
			},
		},
		{
			name:      "validation string returns validation",
			lastError: "payload failed validation: required field missing",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "validation", got)
			},
		},
		{
			name:      "invalid string returns validation",
			lastError: "invalid topic name: must not be empty",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "validation", got)
			},
		},
		{
			name:      "unrecognized error returns unknown",
			lastError: "something went wrong",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "unknown", got)
			},
		},
		{
			name:      "empty string returns unknown",
			lastError: "",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "unknown", got)
			},
		},
		// Precedence: timeout wins over connection when both substrings present.
		{
			name:      "timeout takes precedence over connection keyword",
			lastError: "connection timeout exceeded",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "timeout", got)
			},
		},
		// Case-insensitivity.
		{
			name:      "TIMEOUT upper-case is matched",
			lastError: "TIMEOUT waiting for response",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "timeout", got)
			},
		},
		{
			name:      "DEADLINE upper-case is matched",
			lastError: "DEADLINE exceeded",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "timeout", got)
			},
		},
		{
			name:      "CONNECTION upper-case is matched",
			lastError: "CONNECTION refused by remote",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "connection", got)
			},
		},
		{
			name:      "VALIDATION upper-case is matched",
			lastError: "VALIDATION error on field X",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "validation", got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runtime.ClassifyDeadLetter(tc.lastError)
			tc.assert(t, got)
		})
	}
}
