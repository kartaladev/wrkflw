package processtest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/processtest"
)

func TestSpyAuthorizer(t *testing.T) {
	t.Parallel()

	spec := authz.AuthzSpec{Roles: []string{"manager"}}
	actor := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	denied := errors.New("nope")

	type testCase struct {
		name    string
		program func(s *processtest.SpyAuthorizer)
		assert  func(t *testing.T, err error, calls []processtest.AuthzCall)
	}

	cases := []testCase{
		{
			name:    "default allows and records the call",
			program: func(*processtest.SpyAuthorizer) {},
			assert: func(t *testing.T, err error, calls []processtest.AuthzCall) {
				require.NoError(t, err)
				require.Len(t, calls, 1)
				assert.Equal(t, "alice", calls[0].Actor.ID)
				assert.Equal(t, spec, calls[0].Spec)
				assert.NoError(t, calls[0].Err)
			},
		},
		{
			name:    "Deny returns the error and records it",
			program: func(s *processtest.SpyAuthorizer) { s.Deny(denied) },
			assert: func(t *testing.T, err error, calls []processtest.AuthzCall) {
				require.ErrorIs(t, err, denied)
				require.Len(t, calls, 1)
				assert.ErrorIs(t, calls[0].Err, denied)
			},
		},
		{
			name:    "Deny(nil) falls back to ErrNotAuthorized",
			program: func(s *processtest.SpyAuthorizer) { s.Deny(nil) },
			assert: func(t *testing.T, err error, calls []processtest.AuthzCall) {
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		{
			name: "Allow after Deny re-permits",
			program: func(s *processtest.SpyAuthorizer) {
				s.Deny(denied)
				s.Allow()
			},
			assert: func(t *testing.T, err error, _ []processtest.AuthzCall) {
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spy := processtest.NewSpyAuthorizer()
			tc.program(spy)

			err := spy.Authorize(context.Background(), spec, actor, map[string]any{"amount": 10})
			tc.assert(t, err, spy.Calls())
		})
	}
}
