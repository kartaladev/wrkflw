package grpctransport_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// TestMutatingRPCRequestValidation asserts every mutating RPC rejects a request
// whose genuinely-required field is empty with codes.InvalidArgument at the
// transport boundary — mirroring the existing StartInstance guard and the REST
// validation — rather than falling through to a deeper/other code.
func TestMutatingRPCRequestValidation(t *testing.T) {
	t.Parallel()

	// validActor is a non-empty actor so a case can isolate a different empty field.
	validActor := &workflowpb.Actor{Id: "alice", Roles: []string{"manager"}}

	type testCase struct {
		name string
		// call invokes the RPC on the client with the (invalid) request and
		// returns the resulting error.
		call func(t *testing.T, c workflowpb.WorkflowServiceClient) error
	}

	cases := []testCase{
		{
			name: "DeliverSignal empty instance_id",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.DeliverSignal(t.Context(), &workflowpb.DeliverSignalRequest{InstanceId: "", Signal: "approved"})
				return err
			},
		},
		{
			name: "DeliverSignal empty signal",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.DeliverSignal(t.Context(), &workflowpb.DeliverSignalRequest{InstanceId: "i1", Signal: ""})
				return err
			},
		},
		{
			name: "DeliverMessage empty def_ref",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.DeliverMessage(t.Context(), &workflowpb.DeliverMessageRequest{DefRef: "", Name: "msg"})
				return err
			},
		},
		{
			name: "DeliverMessage empty name",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.DeliverMessage(t.Context(), &workflowpb.DeliverMessageRequest{DefRef: "d:1", Name: ""})
				return err
			},
		},
		{
			name: "ClaimTask empty task_token",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.ClaimTask(t.Context(), &workflowpb.ClaimTaskRequest{TaskToken: "", Actor: validActor})
				return err
			},
		},
		{
			name: "ClaimTask empty actor id",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.ClaimTask(t.Context(), &workflowpb.ClaimTaskRequest{TaskToken: "tok", Actor: &workflowpb.Actor{}})
				return err
			},
		},
		{
			name: "CompleteTask empty task_token",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.CompleteTask(t.Context(), &workflowpb.CompleteTaskRequest{TaskToken: "", Actor: validActor})
				return err
			},
		},
		{
			name: "CompleteTask nil actor",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.CompleteTask(t.Context(), &workflowpb.CompleteTaskRequest{TaskToken: "tok", Actor: nil})
				return err
			},
		},
		{
			name: "ReassignTask empty task_token",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.ReassignTask(t.Context(), &workflowpb.ReassignTaskRequest{TaskToken: "", From: "a", To: "b", By: validActor})
				return err
			},
		},
		{
			name: "ReassignTask empty from",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.ReassignTask(t.Context(), &workflowpb.ReassignTaskRequest{TaskToken: "tok", From: "", To: "b", By: validActor})
				return err
			},
		},
		{
			name: "ReassignTask empty to",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.ReassignTask(t.Context(), &workflowpb.ReassignTaskRequest{TaskToken: "tok", From: "a", To: "", By: validActor})
				return err
			},
		},
		{
			name: "ReassignTask nil by actor",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.ReassignTask(t.Context(), &workflowpb.ReassignTaskRequest{TaskToken: "tok", From: "a", To: "b", By: nil})
				return err
			},
		},
		{
			name: "CancelInstance empty instance_id",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.CancelInstance(t.Context(), &workflowpb.CancelInstanceRequest{InstanceId: ""})
				return err
			},
		},
		{
			name: "ResolveIncident empty instance_id",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.ResolveIncident(t.Context(), &workflowpb.ResolveIncidentRequest{InstanceId: "", IncidentId: "inc1"})
				return err
			},
		},
		{
			name: "ResolveIncident empty incident_id",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.ResolveIncident(t.Context(), &workflowpb.ResolveIncidentRequest{InstanceId: "i1", IncidentId: ""})
				return err
			},
		},
	}

	h := newGRPCHarness(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.call(t, h.client)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err),
				"missing required field must map to InvalidArgument")
		})
	}
}

// TestPolicyAdminRequestValidation asserts the policy-admin mutating RPCs reject
// requests with required-empty fields with InvalidArgument once the admin is
// configured (so validation is reached rather than Unimplemented).
func TestPolicyAdminRequestValidation(t *testing.T) {
	t.Parallel()

	pa := &paStub{
		addPolicyFn:    func(context.Context, service.PolicyRule) (bool, error) { return true, nil },
		removePolicyFn: func(context.Context, service.PolicyRule) (bool, error) { return true, nil },
		addRoleFn:      func(context.Context, service.RoleBinding) (bool, error) { return true, nil },
		removeRoleFn:   func(context.Context, service.RoleBinding) (bool, error) { return true, nil },
	}
	client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithPolicyAdmin(pa))

	type testCase struct {
		name string
		call func(t *testing.T, c workflowpb.WorkflowServiceClient) error
	}

	cases := []testCase{
		{
			name: "AddPolicy nil rule",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.AddPolicy(t.Context(), &workflowpb.AddPolicyRequest{Rule: nil})
				return err
			},
		},
		{
			name: "AddPolicy empty subject",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.AddPolicy(t.Context(), &workflowpb.AddPolicyRequest{Rule: &workflowpb.PolicyRule{Subject: "", Object: "/o", Action: "read"}})
				return err
			},
		},
		{
			name: "RemovePolicy empty action",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.RemovePolicy(t.Context(), &workflowpb.RemovePolicyRequest{Rule: &workflowpb.PolicyRule{Subject: "s", Object: "/o", Action: ""}})
				return err
			},
		},
		{
			name: "AddRole nil binding",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.AddRole(t.Context(), &workflowpb.AddRoleRequest{Binding: nil})
				return err
			},
		},
		{
			name: "AddRole empty user",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.AddRole(t.Context(), &workflowpb.AddRoleRequest{Binding: &workflowpb.RoleBinding{User: "", Role: "admin"}})
				return err
			},
		},
		{
			name: "RemoveRole empty role",
			call: func(t *testing.T, c workflowpb.WorkflowServiceClient) error {
				_, err := c.RemoveRole(t.Context(), &workflowpb.RemoveRoleRequest{Binding: &workflowpb.RoleBinding{User: "u", Role: ""}})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.call(t, client)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err),
				"missing required field must map to InvalidArgument")
		})
	}
}

// TestRedriveDeadLettersRequestValidation asserts RedriveDeadLetters rejects an
// empty id list with InvalidArgument once the DLQ admin is configured.
func TestRedriveDeadLettersRequestValidation(t *testing.T) {
	t.Parallel()

	dla := &dlaStub{
		redriveFn: func(context.Context, ...int64) (int, error) { return 0, nil },
	}
	client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithDeadLetterAdmin(dla))

	_, err := client.RedriveDeadLetters(t.Context(), &workflowpb.RedriveDeadLettersRequest{Ids: nil})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err),
		"empty id list must map to InvalidArgument")
}
