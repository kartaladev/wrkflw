// Package postgres — white-box error-path tests for the timer side-effect helpers.
package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func armedTimerFixture() runtime.ArmedTimer {
	return runtime.ArmedTimer{
		InstanceID: "i1", DefID: "d", DefVersion: 1,
		TimerID: "t1", FireAt: time.Unix(0, 0).UTC(), Kind: engine.TimerIntermediate,
	}
}

// TestUpsertTimerExecError covers the DB exec-error branch in upsertTimer.
func TestUpsertTimerExecError(t *testing.T) {
	injected := errors.New("injected upsert exec error")
	err := upsertTimer(context.Background(), errDBTX{err: injected}, armedTimerFixture())
	require.ErrorContains(t, err, "injected upsert exec error")
}

// TestDeleteTimerExecError covers the DB exec-error branch in deleteTimer.
func TestDeleteTimerExecError(t *testing.T) {
	injected := errors.New("injected delete exec error")
	err := deleteTimer(context.Background(), errDBTX{err: injected}, "i1", "t1")
	require.ErrorContains(t, err, "injected delete exec error")
}

// TestApplyTimerOpsArmError covers the arm-loop error branch in applyTimerOps.
func TestApplyTimerOpsArmError(t *testing.T) {
	injected := errors.New("injected arm error")
	step := runtime.AppliedStep{
		State:     engine.InstanceState{InstanceID: "i1"},
		TimerArms: []runtime.ArmedTimer{armedTimerFixture()},
	}
	err := applyTimerOps(context.Background(), errDBTX{err: injected}, step)
	require.ErrorContains(t, err, "injected arm error")
}

// TestApplyTimerOpsCancelError covers the cancel-loop error branch in applyTimerOps.
func TestApplyTimerOpsCancelError(t *testing.T) {
	injected := errors.New("injected cancel error")
	step := runtime.AppliedStep{
		State:        engine.InstanceState{InstanceID: "i1"},
		TimerCancels: []string{"t1"},
	}
	err := applyTimerOps(context.Background(), errDBTX{err: injected}, step)
	require.ErrorContains(t, err, "injected cancel error")
}

// TestApplyTimerOpsEmptyIsNoop confirms a step with no timer ops touches the DB
// not at all (the errDBTX would error if any Exec ran).
func TestApplyTimerOpsEmptyIsNoop(t *testing.T) {
	step := runtime.AppliedStep{State: engine.InstanceState{InstanceID: "i1"}}
	err := applyTimerOps(context.Background(), errDBTX{err: errors.New("must not run")}, step)
	require.NoError(t, err)
}
