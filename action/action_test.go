package action_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
)

// TestFunc_Do verifies that the ActionFunc adapter satisfies Action correctly.
func TestFunc_Do(t *testing.T) {
	sentinel := errors.New("boom")

	tests := map[string]struct {
		fn      func(context.Context, map[string]any) (map[string]any, error)
		in      map[string]any
		wantOut map[string]any
		wantErr error
	}{
		"happy path returns output": {
			fn: func(_ context.Context, in map[string]any) (map[string]any, error) {
				return map[string]any{"echo": in["v"]}, nil
			},
			in:      map[string]any{"v": "ping"},
			wantOut: map[string]any{"echo": "ping"},
		},
		"propagates error": {
			fn:      func(_ context.Context, _ map[string]any) (map[string]any, error) { return nil, sentinel },
			wantErr: sentinel,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var a action.Action = action.ActionFunc(tc.fn)
			out, err := a.Do(t.Context(), tc.in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantOut, out)
		})
	}
}
