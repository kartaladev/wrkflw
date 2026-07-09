package activity_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// stubStrategy is a no-op validate.ValidationStrategy used to exercise the
// CompletionValidation/PayloadValidation option wiring without depending on any
// concrete adapter package.
type stubStrategy struct{}

func (stubStrategy) NewValidator() (validate.Validator, error) {
	return stubValidator{}, nil
}

type stubValidator struct{}

func (stubValidator) Validate(context.Context, map[string]any) error { return nil }

func TestWithCandidateRoles(t *testing.T) {
	n := activity.NewUserTask("approve",
		activity.WithCandidateRoles("manager", "director"))
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if len(ut.CandidateRoles) != 2 || ut.CandidateRoles[0] != "manager" || ut.CandidateRoles[1] != "director" {
		t.Fatalf("CandidateRoles = %v, want [manager director]", ut.CandidateRoles)
	}
}

func TestWithCompletionValidation_SetsSlot(t *testing.T) {
	t.Parallel()
	n := activity.NewUserTask("approve", activity.WithCompletionValidation(stubStrategy{}))
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node kind = %T", n)
	}
	if ut.CompletionValidation == nil {
		t.Fatal("CompletionValidation not set")
	}
}

func TestWithPayloadValidation_Receive_SetsSlot(t *testing.T) {
	t.Parallel()
	n := activity.NewReceiveTask("await", "OrderPlaced", activity.WithPayloadValidation(stubStrategy{}))
	rt, ok := n.(activity.ReceiveTask)
	if !ok {
		t.Fatalf("node kind = %T", n)
	}
	if rt.PayloadValidation == nil {
		t.Fatal("PayloadValidation not set")
	}
}

func TestWithCompletionAction_SetsFieldOnUserAndReceive(t *testing.T) {
	t.Parallel()
	u := activity.NewUserTask("u1", activity.WithCandidateRoles("r"), activity.WithCompletionAction("recordApproval")).(activity.UserTask)
	assert.Equal(t, "recordApproval", u.CompletionAction)

	r := activity.NewReceiveTask("r1", "m", activity.WithCompletionAction("ackOrder")).(activity.ReceiveTask)
	assert.Equal(t, "ackOrder", r.CompletionAction)
}

func TestWithWaitDeadline_And_WithDeadlineAction(t *testing.T) {
	t.Parallel()
	st := activity.NewUserTask("u1", activity.WithCandidateRoles("r"),
		activity.WithWaitDeadline(schedule.AfterDuration(72*time.Hour), "escalate"),
		activity.WithDeadlineAction("notify"),
	).(activity.UserTask)
	assert.Equal(t, "escalate", st.DeadlineFlow)
	assert.Equal(t, "notify", st.DeadlineAction)
	assert.False(t, st.DeadlineTimer.IsZero())
}
