package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// crosscheckBoundaryDef wraps host between a start and an end event, attaches a
// timer boundary to it, and gives the boundary its own escape route so the
// definition is structurally sound apart from the host question under test.
func crosscheckBoundaryDef(host model.Node) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "boundary-crosscheck", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			host,
			event.NewBoundary("bnd", "host",
				event.WithBoundaryTimer(schedule.AfterDuration(time.Hour))),
			activity.NewServiceTask("escalate", activity.WithTaskAction("escalate")),
			event.NewEnd("end"),
			event.NewEnd("end-escalated"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "host"},
			{ID: "f2", Source: "host", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "escalate"},
			{ID: "f4", Source: "escalate", Target: "end-escalated"},
		},
	}
}

// crosscheckSubprocessDef is the nested definition for the sub-process host row.
func crosscheckSubprocessDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("ns-start"),
			activity.NewServiceTask("ns-task", activity.WithTaskAction("inner")),
			event.NewEnd("ns-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nf1", Source: "ns-start", Target: "ns-task"},
			{ID: "nf2", Source: "ns-task", Target: "ns-end"},
		},
	}
}

// TestBoundaryArmingMatchesValidation is the executable guard on an invariant
// that two packages currently state to each other only in prose:
// model.triggerBoundaryHostKinds is a hand-copied mirror of the engine strategies
// that call armBoundaries, and nothing failed when the two drifted.
//
// For every activity kind that may host a boundary event, it asserts the
// biconditional: model.Validate accepts a timer boundary on that host IF AND
// ONLY IF driving an instance to that host actually records a boundary arm.
// Both directions matter and each catches a different regression —
//
//   - accepted but not armed is the bug this test was written for: validation
//     blessing a boundary the engine drops on the floor.
//   - armed but rejected is its mirror, and the likelier future mistake: wiring
//     armBoundaries into a new strategy (a SubProcess, once scope-keyed arms
//     exist) while leaving the gate rejecting the shape that now works.
//
// model.Validate is its own oracle here, so the test needs no new export from
// either package. It reads the arm through len(State.Boundaries): boundaryArm is
// unexported, but its count is all the correspondence needs.
//
// ⚠ The rows are hand-enumerated because model's activity-kind set is
// unexported. A new activity kind that can host a boundary needs a row here, or
// the invariant simply goes unchecked for it.
func TestBoundaryArmingMatchesValidation(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		host model.Node
		// wantArmed is what the ENGINE is expected to do. Validation's verdict is
		// not stated separately: the test asserts the two agree, so writing both
		// would let a row assert its own contradiction away.
		wantArmed bool
	}{
		{
			name:      "service task",
			host:      activity.NewServiceTask("host", activity.WithTaskAction("work")),
			wantArmed: true,
		},
		{
			name:      "business-rule task",
			host:      activity.NewBusinessRuleTask("host", activity.WithTaskAction("decide")),
			wantArmed: true,
		},
		{
			name:      "receive task",
			host:      activity.NewReceiveTask("host", "msg"),
			wantArmed: true,
		},
		{
			name:      "user task",
			host:      activity.NewUserTask("host"),
			wantArmed: true,
		},
		{
			name:      "send task never parks, so nothing can be armed on it",
			host:      activity.NewSendTask("host", "notify"),
			wantArmed: false,
		},
		{
			name:      "sub-process consumes its host token on entry",
			host:      activity.NewSubProcess("host", crosscheckSubprocessDef()),
			wantArmed: false,
		},
		{
			name:      "call activity has no way to cancel its started child",
			host:      activity.NewCallActivity("host", model.Latest("child")),
			wantArmed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := crosscheckBoundaryDef(tc.host)

			// Isolate the host rule: any OTHER validation complaint would make
			// this row vacuous rather than failing it, so assert there is none.
			validationErr := model.Validate(def)
			accepted := !errors.Is(validationErr, model.ErrBoundaryTriggerHost)
			if accepted {
				require.NoError(t, validationErr,
					"fixture must be valid apart from the boundary-host rule")
			}

			r, err := engine.Step(t.Context(), def,
				engine.InstanceState{InstanceID: "crosscheck-1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)
			armed := len(r.State.Boundaries) > 0

			assert.Equal(t, tc.wantArmed, armed,
				"engine arming for this host kind changed")
			assert.Equal(t, armed, accepted,
				"model.Validate and armBoundaries disagree about this host: "+
					"validation accepts=%v, engine arms=%v. Whichever moved, move "+
					"the other — model.triggerBoundaryHostKinds mirrors the "+
					"strategies that call armBoundaries", accepted, armed)
		})
	}
}
