package grpctransport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// ---- fake ports ----

type fakeRelayStatsAdmin struct {
	statsFn func(ctx context.Context) (kernel.OutboxStats, error)
}

func (f *fakeRelayStatsAdmin) OutboxStats(ctx context.Context) (kernel.OutboxStats, error) {
	return f.statsFn(ctx)
}

type fakeTimerAdmin struct {
	statsFn     func(ctx context.Context) (kernel.TimerStats, error)
	listArmedFn func(ctx context.Context) ([]kernel.ArmedTimer, error)
}

func (f *fakeTimerAdmin) Stats(ctx context.Context) (kernel.TimerStats, error) {
	return f.statsFn(ctx)
}

func (f *fakeTimerAdmin) ListArmed(ctx context.Context) ([]kernel.ArmedTimer, error) {
	return f.listArmedFn(ctx)
}

type fakeLineageAdmin struct {
	lineageFn func(ctx context.Context, instanceID string) (kernel.InstanceLineage, error)
}

func (f *fakeLineageAdmin) Lineage(ctx context.Context, instanceID string) (kernel.InstanceLineage, error) {
	return f.lineageFn(ctx, instanceID)
}

// ---- GetRelayStats tests ----

func TestServerGetRelayStats(t *testing.T) {
	t.Parallel()

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.GetRelayStats(t.Context(), &workflowpb.GetRelayStatsRequest{})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("wired returns relay stats", func(t *testing.T) {
		t.Parallel()
		age := 90 * time.Second
		admin := &fakeRelayStatsAdmin{statsFn: func(_ context.Context) (kernel.OutboxStats, error) {
			return kernel.OutboxStats{
				Pending:          3,
				Dead:             1,
				OldestPendingAge: age,
			}, nil
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithRelayStatsAdmin(admin))
		resp, err := client.GetRelayStats(t.Context(), &workflowpb.GetRelayStatsRequest{})
		require.NoError(t, err)
		assert.Equal(t, int64(3), resp.GetPending())
		assert.Equal(t, int64(1), resp.GetDead())
		assert.Equal(t, int64(90), resp.GetOldestPendingAgeSeconds())
	})

	t.Run("admin error maps to Internal", func(t *testing.T) {
		t.Parallel()
		admin := &fakeRelayStatsAdmin{statsFn: func(_ context.Context) (kernel.OutboxStats, error) {
			return kernel.OutboxStats{}, errors.New("workflow-postgres: outbox stats: boom")
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithRelayStatsAdmin(admin))
		_, err := client.GetRelayStats(t.Context(), &workflowpb.GetRelayStatsRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestWithRelayStatsAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { grpctransport.WithRelayStatsAdmin(nil) })
}

// ---- ListTimers tests ----

func TestServerListTimers(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.ListTimers(t.Context(), &workflowpb.ListTimersRequest{})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("wired returns timer list", func(t *testing.T) {
		t.Parallel()
		admin := &fakeTimerAdmin{
			statsFn: func(_ context.Context) (kernel.TimerStats, error) {
				return kernel.TimerStats{Armed: 1, NextFireAt: &now}, nil
			},
			listArmedFn: func(_ context.Context) ([]kernel.ArmedTimer, error) {
				return []kernel.ArmedTimer{
					{
						InstanceID: "inst-1",
						DefID:      "proc",
						DefVersion: 2,
						TimerID:    "t1",
						FireAt:     now,
						Kind:       engine.TimerIntermediate,
					},
				}, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithTimerAdmin(admin))
		resp, err := client.ListTimers(t.Context(), &workflowpb.ListTimersRequest{})
		require.NoError(t, err)
		assert.Equal(t, int64(1), resp.GetCount())
		require.NotNil(t, resp.GetNextFireAt())
		assert.Equal(t, now.Unix(), resp.GetNextFireAt().AsTime().Unix())
		require.Len(t, resp.GetItems(), 1)
		item := resp.GetItems()[0]
		assert.Equal(t, "inst-1", item.GetInstanceId())
		assert.Equal(t, "proc", item.GetDefId())
		assert.Equal(t, int32(2), item.GetDefVersion())
		assert.Equal(t, "t1", item.GetTimerId())
		assert.Equal(t, now.Unix(), item.GetFireAt().AsTime().Unix())
		assert.Equal(t, "TimerIntermediate", item.GetKind())
	})

	t.Run("admin stats error maps to Internal", func(t *testing.T) {
		t.Parallel()
		admin := &fakeTimerAdmin{
			statsFn: func(_ context.Context) (kernel.TimerStats, error) {
				return kernel.TimerStats{}, errors.New("workflow-postgres: timer stats: boom")
			},
			listArmedFn: func(_ context.Context) ([]kernel.ArmedTimer, error) {
				return nil, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithTimerAdmin(admin))
		_, err := client.ListTimers(t.Context(), &workflowpb.ListTimersRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("nil NextFireAt when no timers", func(t *testing.T) {
		t.Parallel()
		admin := &fakeTimerAdmin{
			statsFn: func(_ context.Context) (kernel.TimerStats, error) {
				return kernel.TimerStats{Armed: 0, NextFireAt: nil}, nil
			},
			listArmedFn: func(_ context.Context) ([]kernel.ArmedTimer, error) {
				return []kernel.ArmedTimer{}, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithTimerAdmin(admin))
		resp, err := client.ListTimers(t.Context(), &workflowpb.ListTimersRequest{})
		require.NoError(t, err)
		assert.Equal(t, int64(0), resp.GetCount())
		assert.Nil(t, resp.GetNextFireAt())
		assert.Empty(t, resp.GetItems())
	})
}

func TestWithTimerAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { grpctransport.WithTimerAdmin(nil) })
}

// ---- GetInstanceLineage tests ----

func TestServerGetInstanceLineage(t *testing.T) {
	t.Parallel()

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.GetInstanceLineage(t.Context(), &workflowpb.GetInstanceLineageRequest{InstanceId: "p1"})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("wired root instance returns lineage with nil parents", func(t *testing.T) {
		t.Parallel()
		admin := &fakeLineageAdmin{lineageFn: func(_ context.Context, id string) (kernel.InstanceLineage, error) {
			return kernel.InstanceLineage{
				InstanceID:       id,
				CallParent:       nil,
				CallChildren:     []kernel.CallLinkRef{},
				ChainPredecessor: nil,
				ChainSuccessors:  []kernel.ChainLinkRef{},
			}, nil
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithLineageAdmin(admin))
		resp, err := client.GetInstanceLineage(t.Context(), &workflowpb.GetInstanceLineageRequest{InstanceId: "root-inst"})
		require.NoError(t, err)
		assert.Equal(t, "root-inst", resp.GetInstanceId())
		assert.Nil(t, resp.GetCallParent())
		assert.Empty(t, resp.GetCallChildren())
		assert.Nil(t, resp.GetChainPredecessor())
		assert.Empty(t, resp.GetChainSuccessors())
	})

	t.Run("wired child instance returns populated lineage", func(t *testing.T) {
		t.Parallel()
		admin := &fakeLineageAdmin{lineageFn: func(_ context.Context, id string) (kernel.InstanceLineage, error) {
			return kernel.InstanceLineage{
				InstanceID: id,
				CallParent: &kernel.CallLinkRef{
					InstanceID: "parent-inst",
					DefID:      "parent-def",
					DefVersion: 1,
					Depth:      0,
				},
				CallChildren: []kernel.CallLinkRef{
					{InstanceID: "child-inst", DefID: "", DefVersion: 0, Depth: 1},
				},
				ChainPredecessor: &kernel.ChainLinkRef{
					InstanceID:    "pred-inst",
					DefinitionRef: "pred-def:1",
					Outcome:       "completed",
				},
				ChainSuccessors: []kernel.ChainLinkRef{
					{InstanceID: "succ-inst", DefinitionRef: "succ-def:1", Outcome: "completed"},
				},
			}, nil
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithLineageAdmin(admin))
		resp, err := client.GetInstanceLineage(t.Context(), &workflowpb.GetInstanceLineageRequest{InstanceId: "child-inst"})
		require.NoError(t, err)
		assert.Equal(t, "child-inst", resp.GetInstanceId())
		require.NotNil(t, resp.GetCallParent())
		assert.Equal(t, "parent-inst", resp.GetCallParent().GetInstanceId())
		assert.Equal(t, "parent-def", resp.GetCallParent().GetDefId())
		assert.Equal(t, int32(1), resp.GetCallParent().GetDefVersion())
		assert.Equal(t, int32(0), resp.GetCallParent().GetDepth())
		require.Len(t, resp.GetCallChildren(), 1)
		assert.Equal(t, "child-inst", resp.GetCallChildren()[0].GetInstanceId())
		require.NotNil(t, resp.GetChainPredecessor())
		assert.Equal(t, "pred-inst", resp.GetChainPredecessor().GetInstanceId())
		assert.Equal(t, "pred-def:1", resp.GetChainPredecessor().GetDefinitionRef())
		assert.Equal(t, "completed", resp.GetChainPredecessor().GetOutcome())
		require.Len(t, resp.GetChainSuccessors(), 1)
		assert.Equal(t, "succ-inst", resp.GetChainSuccessors()[0].GetInstanceId())
	})

	t.Run("admin error maps to Internal", func(t *testing.T) {
		t.Parallel()
		admin := &fakeLineageAdmin{lineageFn: func(_ context.Context, _ string) (kernel.InstanceLineage, error) {
			return kernel.InstanceLineage{}, errors.New("workflow-postgres: lineage: boom")
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithLineageAdmin(admin))
		_, err := client.GetInstanceLineage(t.Context(), &workflowpb.GetInstanceLineageRequest{InstanceId: "p1"})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestWithLineageAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { grpctransport.WithLineageAdmin(nil) })
}

// ---- DeadLetter.category population test ----

func TestDeadLetterCategoryPopulated(t *testing.T) {
	t.Parallel()
	// LastError that matches the "timeout" category.
	created := time.Now()
	dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]monitor.DeadLetter, error) {
		return []monitor.DeadLetter{
			{ID: 1, InstanceID: "p1", Topic: "t", RetryCount: 0, LastError: "context deadline exceeded", CreatedAt: created},
		}, nil
	}}
	client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithDeadLetterAdmin(dla))
	resp, err := client.ListDeadLetters(t.Context(), &workflowpb.ListDeadLettersRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, resp.GetItems(), 1)
	assert.Equal(t, "timeout", resp.GetItems()[0].GetCategory())
}
