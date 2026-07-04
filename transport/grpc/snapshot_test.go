package grpctransport_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// snapNoopFn is a reusable no-op action function for snapshot test fixtures.
func snapNoopFn(_ context.Context, in map[string]any) (map[string]any, error) {
	return in, nil
}

// snapshotDef returns a definition registered under "snap-def:1" with:
//   - a scoped-catalog entry "scoped-svc"
//   - a ServiceTask "named-svc" bound to "scoped-svc" by name
//   - a ServiceTask "inline-svc" with an inline action
//   - a ServiceTask "default-svc" with no explicit action (default-by-id)
//
// Tasks execute in sequence: named-svc → inline-svc → default-svc → end.
// named-svc and inline-svc resolve successfully; default-svc will fail with
// an incident (no "default-svc" in any catalog) but the instance is stored.
func snapshotDef() *definition.ProcessDefinition {
	def, err := definition.NewBuilder("snap-def", 1).
		RegisterAction("scoped-svc", action.Func(snapNoopFn)).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("named-svc", activity.WithActionName("scoped-svc"))).
		Add(activity.NewServiceTask("inline-svc", activity.WithActionFunc(snapNoopFn))).
		Add(activity.NewServiceTask("default-svc")).
		Add(event.NewEnd("end")).
		Connect("start", "named-svc").
		Connect("named-svc", "inline-svc").
		Connect("inline-svc", "default-svc").
		Connect("default-svc", "end").
		Build()
	if err != nil {
		panic("snapshotDef: " + err.Error())
	}
	return def
}

// TestGetInstanceSnapshotNotFound and TestGetActionableViewNotFound are
// merged here: both RPCs should return codes.NotFound when the instance ID is
// deliberately absent / never registered.
func TestSnapshotNotFound(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "GetInstanceSnapshot returns NotFound for absent instance",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Equal(t, codes.NotFound, status.Code(err))
			},
		},
		{
			name: "GetActionableView returns NotFound for absent instance",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Equal(t, codes.NotFound, status.Code(err))
			},
		},
	}

	// Each row needs its own harness so they run fully in parallel.
	rpcs := []func(h *grpcHarness) error{
		func(h *grpcHarness) error {
			// Instance ID is deliberately absent — never registered in this harness.
			_, err := h.client.GetInstanceSnapshot(t.Context(), &workflowpb.GetInstanceRequest{
				InstanceId: "no-such-instance",
			})
			return err
		},
		func(h *grpcHarness) error {
			// Instance ID is deliberately absent — never registered in this harness.
			_, err := h.client.GetActionableView(t.Context(), &workflowpb.GetInstanceRequest{
				InstanceId: "no-such-instance",
			})
			return err
		},
	}

	for i, tc := range cases {
		rpc := rpcs[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newGRPCHarness(t)
			tc.assert(t, rpc(h))
		})
	}
}

// TestGetInstanceSnapshotMappedFields verifies that GetInstanceSnapshot returns
// the full snapshot projection including action_bindings and scoped_actions for
// a started instance whose definition has scoped and inline service tasks.
func TestGetInstanceSnapshotMappedFields(t *testing.T) {
	t.Parallel()
	def := snapshotDef()
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	// Start the instance. named-svc and inline-svc complete; default-svc will
	// create an incident (no catalog entry). We ignore the start error since the
	// instance is persisted regardless.
	_, _ = h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     defRefFor(def),
		InstanceId: "snap-inst-1",
	})

	resp, err := h.client.GetInstanceSnapshot(ctx, &workflowpb.GetInstanceRequest{
		InstanceId: "snap-inst-1",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.GetSnapshot())

	snap := resp.GetSnapshot()
	assert.Equal(t, "snap-inst-1", snap.GetInstanceId())
	assert.Equal(t, "snap-def", snap.GetDefId())
	assert.Equal(t, int32(1), snap.GetDefVersion())
	assert.NotEmpty(t, snap.GetStatus())

	// ScopedActions must contain the registered scoped action name.
	assert.Contains(t, snap.GetScopedActions(), "scoped-svc")

	// ActionBindings must include all 3 service tasks, sorted by node_id.
	bindings := snap.GetActionBindings()
	require.Len(t, bindings, 3)

	// All bindings should be of kind "serviceTask".
	for _, b := range bindings {
		assert.Equal(t, "serviceTask", b.GetNodeKind())
	}

	// Find each expected binding by node_id.
	bindingByID := make(map[string]*workflowpb.ActionBindingView)
	for _, b := range bindings {
		bindingByID[b.GetNodeId()] = b
	}

	namedBinding, ok := bindingByID["named-svc"]
	require.True(t, ok, "expected binding for named-svc")
	assert.Equal(t, "scoped-svc", namedBinding.GetAction())
	assert.False(t, namedBinding.GetInline())

	inlineBinding, ok := bindingByID["inline-svc"]
	require.True(t, ok, "expected binding for inline-svc")
	assert.Empty(t, inlineBinding.GetAction()) // inline action has no catalog name
	assert.True(t, inlineBinding.GetInline())

	defaultBinding, ok := bindingByID["default-svc"]
	require.True(t, ok, "expected binding for default-svc")
	assert.Empty(t, defaultBinding.GetAction()) // default-by-id: no explicit name
	assert.False(t, defaultBinding.GetInline())

	// Concrete history assertions: start, named-svc, inline-svc, and default-svc
	// must all appear (the instance reached default-svc before failing).
	history := snap.GetHistory()
	require.NotEmpty(t, history, "completed nodes must appear in history")
	// Collect visited node IDs and verify each entry has an entered_at timestamp.
	visitedNodes := make(map[string]bool, len(history))
	for _, v := range history {
		visitedNodes[v.GetNodeId()] = true
		assert.NotNil(t, v.GetEnteredAt(), "every history entry must have entered_at")
	}
	assert.True(t, visitedNodes["start"], "start event must appear in history")
	assert.True(t, visitedNodes["named-svc"], "named-svc must appear in history")
	assert.True(t, visitedNodes["inline-svc"], "inline-svc must appear in history")
	assert.True(t, visitedNodes["default-svc"], "default-svc must appear in history")

	// The instance fails when default-svc has no catalog entry (no retry policy →
	// no incident, status goes to "failed" with EndedAt populated).
	assert.Equal(t, "failed", snap.GetStatus())
	assert.NotNil(t, snap.GetEndedAt(), "a failed instance must have ended_at populated")
}

// TestGetInstanceSnapshotTokensAndHistory verifies that tokens and history
// fields are populated in the snapshot response.
func TestGetInstanceSnapshotTokensAndHistory(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t, serverLinearDef())
	ctx := t.Context()

	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     defRefFor(serverLinearDef()),
		InstanceId: "snap-history-inst-1",
		Vars:       mustStruct(map[string]any{"name": "snap-test"}),
	})
	require.NoError(t, err)

	resp, err := h.client.GetInstanceSnapshot(ctx, &workflowpb.GetInstanceRequest{
		InstanceId: "snap-history-inst-1",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.GetSnapshot())

	snap := resp.GetSnapshot()
	assert.Equal(t, "completed", snap.GetStatus())
	// A completed instance has no live tokens but has history entries.
	assert.NotEmpty(t, snap.GetHistory(), "completed instance must have non-empty history")
	assert.NotNil(t, snap.GetStartedAt())

	// A completed instance must have EndedAt populated.
	assert.NotNil(t, snap.GetEndedAt(), "completed instance must have non-nil ended_at")

	// Verify concrete history field: every entry has a node_id and entered_at.
	for _, v := range snap.GetHistory() {
		assert.NotEmpty(t, v.GetNodeId(), "history entry must have a node_id")
		assert.NotNil(t, v.GetEnteredAt(), "history entry must have entered_at")
	}
}

// TestGetActionableViewMappedFields verifies that GetActionableView returns the
// actionable projection for a started instance (open tasks and allowed actions).
// We use the existing approval def (userTask → end) so there is an open task.
func TestGetActionableViewMappedFields(t *testing.T) {
	t.Parallel()
	def := serverApprovalDef()
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     defRefFor(def),
		InstanceId: "actionable-inst-1",
	})
	require.NoError(t, err)

	resp, err := h.client.GetActionableView(ctx, &workflowpb.GetInstanceRequest{
		InstanceId: "actionable-inst-1",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.GetActionable())

	av := resp.GetActionable()
	assert.Equal(t, "actionable-inst-1", av.GetInstanceId())
	assert.Equal(t, "running", av.GetStatus())
	require.Len(t, av.GetOpenTasks(), 1)

	task := av.GetOpenTasks()[0]
	assert.Equal(t, "approve", task.GetNodeId())
	assert.NotEmpty(t, task.GetTaskToken())
	assert.Equal(t, "unclaimed", task.GetState())
	// The approval def has one outgoing flow from the user task → end.
	require.Len(t, task.GetAllowedActions(), 1)
	assert.Equal(t, "end", task.GetAllowedActions()[0].GetTarget())
}
