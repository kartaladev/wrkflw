package gocron_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-co-op/gocron/v2"
	"github.com/stretchr/testify/require"

	gocronsched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// fakeNeutralLock records whether Unlock ran.
type fakeNeutralLock struct{ unlocked *bool }

func (l fakeNeutralLock) Unlock(context.Context) error { *l.unlocked = true; return nil }

// fakeNeutralLocker is a neutral-shaped locker returning fakeNeutralLock or an error.
type fakeNeutralLocker struct {
	err      error
	unlocked *bool
}

func (l fakeNeutralLocker) Lock(context.Context, string) (gocronsched.NeutralLock, error) {
	if l.err != nil {
		return nil, l.err
	}
	return fakeNeutralLock{unlocked: l.unlocked}, nil
}

// fakeNeutralElector is a neutral-shaped elector returning a configurable result.
type fakeNeutralElector struct{ err error }

func (e fakeNeutralElector) IsLeader(context.Context) error { return e.err }

func TestAdaptLocker_DelegatesLockAndUnlock(t *testing.T) {
	type tc struct {
		name   string
		err    error
		assert func(t *testing.T, l gocron.Lock, err error, unlocked *bool)
	}

	cases := []tc{
		{
			name: "lock obtained then unlocked delegates to neutral lock",
			err:  nil,
			assert: func(t *testing.T, l gocron.Lock, err error, unlocked *bool) {
				require.NoError(t, err)
				require.NotNil(t, l)
				require.NoError(t, l.Unlock(t.Context()))
				require.True(t, *unlocked, "adapter must delegate Unlock to the neutral lock")
			},
		},
		{
			name: "lock error propagates unchanged",
			err:  errors.New("held"),
			assert: func(t *testing.T, l gocron.Lock, err error, _ *bool) {
				require.Error(t, err)
				require.Nil(t, l)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			unlocked := false
			gl := gocronsched.AdaptLocker(fakeNeutralLocker{err: c.err, unlocked: &unlocked}) // gocron.Locker
			l, err := gl.Lock(t.Context(), "k")
			c.assert(t, l, err, &unlocked)
		})
	}
}

func TestAdaptElector_DelegatesIsLeader(t *testing.T) {
	type tc struct {
		name    string
		err     error
		wantNil bool
	}

	cases := []tc{
		{name: "leader returns nil", err: nil, wantNil: true},
		{name: "follower propagates error", err: errors.New("not leader"), wantNil: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ge := gocronsched.AdaptElector(fakeNeutralElector{err: c.err}) // gocron.Elector
			err := ge.IsLeader(context.Background())
			if c.wantNil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
