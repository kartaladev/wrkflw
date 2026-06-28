// Package mysql — test-only exports.
// This file exposes internal symbols for white-box testing.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// IsConcurrencyError re-exports the unexported isConcurrencyError helper for
// black-box tests in the mysql_test package.
var IsConcurrencyError = isConcurrencyError

// IsUniqueViolation re-exports the unexported isUniqueViolation helper for
// black-box tests in the mysql_test package.
var IsUniqueViolation = isUniqueViolation

// CapHistory re-exports the unexported capHistory helper for black-box tests.
var CapHistory = capHistory

// errDbtx is a DBTX implementation whose methods always return the injected error.
// Used to test error paths in internal write helpers without a real DB.
type errDbtx struct {
	err error
}

func (e errDbtx) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, e.err
}
func (e errDbtx) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, e.err
}
func (e errDbtx) QueryRowContext(_ context.Context, _ string, _ ...any) *sql.Row {
	return nil
}

// TestMysqlMapConflict verifies that mysqlMapConflict passes non-concurrency errors
// through unchanged, returns nil for nil, and maps MySQL deadlock to ErrConcurrentUpdate.
func TestMysqlMapConflict(t *testing.T) {
	t.Run("plain error passes through", func(t *testing.T) {
		plain := errors.New("plain error")
		require.Equal(t, plain, mysqlMapConflict(plain))
	})
	t.Run("nil passes through as nil", func(t *testing.T) {
		require.NoError(t, mysqlMapConflict(nil))
	})
	t.Run("MySQL deadlock (1213) maps to ErrConcurrentUpdate", func(t *testing.T) {
		deadlock := &mysqldriver.MySQLError{Number: 1213}
		require.ErrorIs(t, mysqlMapConflict(deadlock), runtime.ErrConcurrentUpdate)
	})
	t.Run("MySQL lock wait timeout (1205) maps to ErrConcurrentUpdate", func(t *testing.T) {
		lockWait := &mysqldriver.MySQLError{Number: 1205}
		require.ErrorIs(t, mysqlMapConflict(lockWait), runtime.ErrConcurrentUpdate)
	})
}

// TestWriteJournalExecError verifies that mysqlWriteJournal propagates a DB exec error.
func TestWriteJournalExecError(t *testing.T) {
	injected := errors.New("injected journal exec error")
	step := runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "x"},
		Trigger: engine.NewStartInstance(time.Now(), nil),
	}
	err := mysqlWriteJournal(context.Background(), errDbtx{err: injected}, step, 1, time.Now())
	require.ErrorContains(t, err, "injected journal exec error")
}

// TestWriteOutboxExecError verifies that mysqlWriteOutbox propagates a DB exec error.
func TestWriteOutboxExecError(t *testing.T) {
	injected := errors.New("injected outbox exec error")
	events := []runtime.OutboxEvent{{Topic: "t", Payload: map[string]any{"k": "v"}}}
	err := mysqlWriteOutbox(context.Background(), errDbtx{err: injected}, "inst-1", 1, events, time.Now())
	require.ErrorContains(t, err, "injected outbox exec error")
}

// TestInsertCallLinkExecError verifies that mysqlInsertCallLink propagates a DB exec error.
func TestInsertCallLinkExecError(t *testing.T) {
	injected := errors.New("injected call link exec error")
	link := runtime.CallLink{
		ChildInstanceID:  "c1",
		ParentInstanceID: "p1",
		ParentCommandID:  "cmd1",
		ParentDefID:      "d",
		ParentDefVersion: 1,
		Depth:            1,
	}
	err := mysqlInsertCallLink(context.Background(), errDbtx{err: injected}, link, time.Now())
	require.ErrorContains(t, err, "injected call link exec error")
}

// TestFlipCallLinkExecError verifies that mysqlFlipCallLink propagates a DB exec error.
func TestFlipCallLinkExecError(t *testing.T) {
	injected := errors.New("injected flip call link exec error")
	outcome := runtime.CallOutcome{Completed: true, Output: map[string]any{"k": "v"}}
	err := mysqlFlipCallLink(context.Background(), errDbtx{err: injected}, "child-1", outcome, time.Now())
	require.ErrorContains(t, err, "injected flip call link exec error")
}

// TestUpsertTimerExecError verifies that mysqlUpsertTimer propagates a DB exec error.
func TestUpsertTimerExecError(t *testing.T) {
	injected := errors.New("injected upsert timer exec error")
	timer := runtime.ArmedTimer{InstanceID: "i1", TimerID: "t1", FireAt: time.Now(), Kind: engine.TimerIntermediate, DefID: "d", DefVersion: 1}
	err := mysqlUpsertTimer(context.Background(), errDbtx{err: injected}, timer)
	require.ErrorContains(t, err, "injected upsert timer exec error")
}

// TestDeleteTimerExecError verifies that mysqlDeleteTimer propagates a DB exec error.
func TestDeleteTimerExecError(t *testing.T) {
	injected := errors.New("injected delete timer exec error")
	err := mysqlDeleteTimer(context.Background(), errDbtx{err: injected}, "i1", "t1")
	require.ErrorContains(t, err, "injected delete timer exec error")
}

// TestApplyTimerOpsArmsError verifies that mysqlApplyTimerOps propagates upsert errors.
func TestApplyTimerOpsArmsError(t *testing.T) {
	injected := errors.New("injected timer ops arm error")
	step := runtime.AppliedStep{
		State: engine.InstanceState{InstanceID: "i1"},
		TimerArms: []runtime.ArmedTimer{
			{InstanceID: "i1", TimerID: "t1", FireAt: time.Now(), Kind: engine.TimerIntermediate, DefID: "d", DefVersion: 1},
		},
	}
	err := mysqlApplyTimerOps(context.Background(), errDbtx{err: injected}, step)
	require.ErrorContains(t, err, "injected timer ops arm error")
}

// TestApplyTimerOpsCancelsError verifies that mysqlApplyTimerOps propagates delete errors.
func TestApplyTimerOpsCancelsError(t *testing.T) {
	injected := errors.New("injected timer ops cancel error")
	step := runtime.AppliedStep{
		State:        engine.InstanceState{InstanceID: "i1"},
		TimerCancels: []string{"t1"},
	}
	err := mysqlApplyTimerOps(context.Background(), errDbtx{err: injected}, step)
	require.ErrorContains(t, err, "injected timer ops cancel error")
}

// TestApplyTimerOpsDeadlockMapsToErrConcurrentUpdate verifies that a MySQL deadlock
// (1213) from a timer upsert/delete is mapped to runtime.ErrConcurrentUpdate when
// wrapped with mysqlMapConflict — matching the fix applied at the Create/Commit call sites.
func TestApplyTimerOpsDeadlockMapsToErrConcurrentUpdate(t *testing.T) {
	deadlock := &mysqldriver.MySQLError{Number: 1213}

	t.Run("arm deadlock maps via mysqlMapConflict", func(t *testing.T) {
		step := runtime.AppliedStep{
			State: engine.InstanceState{InstanceID: "i1"},
			TimerArms: []runtime.ArmedTimer{
				{InstanceID: "i1", TimerID: "t1", FireAt: time.Now(), Kind: engine.TimerIntermediate, DefID: "d", DefVersion: 1},
			},
		}
		err := mysqlMapConflict(mysqlApplyTimerOps(context.Background(), errDbtx{err: deadlock}, step))
		require.ErrorIs(t, err, runtime.ErrConcurrentUpdate, "deadlock from timer arm must map to ErrConcurrentUpdate")
	})

	t.Run("cancel deadlock maps via mysqlMapConflict", func(t *testing.T) {
		step := runtime.AppliedStep{
			State:        engine.InstanceState{InstanceID: "i1"},
			TimerCancels: []string{"t1"},
		}
		err := mysqlMapConflict(mysqlApplyTimerOps(context.Background(), errDbtx{err: deadlock}, step))
		require.ErrorIs(t, err, runtime.ErrConcurrentUpdate, "deadlock from timer cancel must map to ErrConcurrentUpdate")
	})
}

// TxWith re-exports txWith for integration tests that need a real container.
var TxWith = txWith

// HashKey re-exports the unexported hashKey helper so ownership_test.go can
// verify the 64-char limit and stability without a real DB.
var HashKey = hashKey
