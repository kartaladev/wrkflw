package grpctransport_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
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
func snapshotDef() *model.ProcessDefinition {
	def, err := model.NewDefinition("snap-def", 1).
		RegisterAction("scoped-svc", action.Func(snapNoopFn)).
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("named-svc", model.WithActionName("scoped-svc"))).
		Add(model.NewServiceTask("inline-svc", model.WithActionFunc(snapNoopFn))).
		Add(model.NewServiceTask("default-svc")).
		Add(model.NewEndEvent("end")).
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

// TestGetInstanceSnapshotNotFound verifies that GetInstanceSnapshot returns
// codes.NotFound when the instance ID does not exist.
func TestGetInstanceSnapshotNotFound(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t)

	_, err := h.client.GetInstanceSnapshot(t.Context(), &workflowpb.GetInstanceRequest{
		InstanceId: "no-such-instance",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestGetActionableViewNotFound verifies that GetActionableView returns
// codes.NotFound when the instance ID does not exist.
func TestGetActionableViewNotFound(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t)

	_, err := h.client.GetActionableView(t.Context(), &workflowpb.GetInstanceRequest{
		InstanceId: "no-such-instance",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
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
