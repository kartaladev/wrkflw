package activity_test

import (
	"context"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/model/validate"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestWithEligibleRoles(t *testing.T) {
	n := activity.NewUserTask("approve",
		activity.WithEligibleRoles("manager", "director"))
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if len(ut.EligibleRoles) != 2 || ut.EligibleRoles[0] != "manager" || ut.EligibleRoles[1] != "director" {
		t.Fatalf("EligibleRoles = %v, want [manager director]", ut.EligibleRoles)
	}
}

func TestWithManual(t *testing.T) {
	cases := []struct {
		name      string
		immediate bool
		assert    func(t *testing.T, ut activity.UserTask)
	}{
		{
			name:      "wait mode",
			immediate: false,
			assert: func(t *testing.T, ut activity.UserTask) {
				if !ut.Manual {
					t.Fatal("Manual = false, want true")
				}
				if ut.ManualImmediate {
					t.Fatal("ManualImmediate = true, want false (wait mode)")
				}
			},
		},
		{
			name:      "immediate mode",
			immediate: true,
			assert: func(t *testing.T, ut activity.UserTask) {
				if !ut.Manual {
					t.Fatal("Manual = false, want true")
				}
				if !ut.ManualImmediate {
					t.Fatal("ManualImmediate = false, want true (immediate mode)")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := activity.NewUserTask("confirm", activity.WithManual(tc.immediate))
			ut, ok := n.(activity.UserTask)
			if !ok {
				t.Fatalf("node is %T, want activity.UserTask", n)
			}
			if len(ut.EligibleRoles) != 0 {
				t.Fatalf("EligibleRoles = %v, want empty", ut.EligibleRoles)
			}
			tc.assert(t, ut)
		})
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
	u := activity.NewUserTask("u1", activity.WithEligibleRoles("r"), activity.WithCompletionAction("recordApproval")).(activity.UserTask)
	assert.Equal(t, "recordApproval", u.CompletionAction)

	r := activity.NewReceiveTask("r1", "m", activity.WithCompletionAction("ackOrder")).(activity.ReceiveTask)
	assert.Equal(t, "ackOrder", r.CompletionAction)
}

func TestWithWaitDeadline_And_WithDeadlineAction(t *testing.T) {
	t.Parallel()
	st := activity.NewUserTask("u1", activity.WithEligibleRoles("r"),
		activity.WithWaitDeadline(schedule.AfterDuration(72*time.Hour), "escalate"),
		activity.WithDeadlineAction("notify"),
	).(activity.UserTask)
	assert.Equal(t, "escalate", st.DeadlineFlow)
	assert.Equal(t, "notify", st.DeadlineAction)
	assert.False(t, st.DeadlineTimer.IsZero())
}

// TestUserTaskOutcomeOptions covers the completion-outcome declaration options:
// the accepted outcome set and the two mutually-exclusive variable-exposure
// forms.
func TestUserTaskOutcomeOptions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opts   []activity.UserTaskOption
		assert func(t *testing.T, u activity.UserTask)
	}

	cases := []testCase{
		{
			name: "no outcome options leave the node unconstrained",
			opts: nil,
			assert: func(t *testing.T, u activity.UserTask) {
				assert.Empty(t, u.Outcomes)
				assert.False(t, u.ExposeOutcome)
				assert.Empty(t, u.OutcomeVariable)
			},
		},
		{
			name: "WithOutcomes declares the accepted set and is additive",
			opts: []activity.UserTaskOption{
				activity.WithOutcomes("approve"),
				activity.WithOutcomes("reject", "escalate"),
			},
			assert: func(t *testing.T, u activity.UserTask) {
				assert.Equal(t, []string{"approve", "reject", "escalate"}, u.Outcomes)
			},
		},
		{
			name: "WithExposeOutcome opts into the conventional variable name",
			opts: []activity.UserTaskOption{activity.WithExposeOutcome()},
			assert: func(t *testing.T, u activity.UserTask) {
				assert.True(t, u.ExposeOutcome)
				assert.Empty(t, u.OutcomeVariable)
			},
		},
		{
			name: "WithOutcomeVariable names the exposed variable explicitly",
			opts: []activity.UserTaskOption{activity.WithOutcomeVariable("review_decision")},
			assert: func(t *testing.T, u activity.UserTask) {
				assert.Equal(t, "review_decision", u.OutcomeVariable)
				assert.False(t, u.ExposeOutcome)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := activity.NewUserTask("review", tc.opts...)
			u, ok := n.(activity.UserTask)
			require.True(t, ok, "node is %T, want activity.UserTask", n)
			tc.assert(t, u)
		})
	}
}
