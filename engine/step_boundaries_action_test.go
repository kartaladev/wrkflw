package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// boundaryActionDef builds a minimal definition with a single boundary event
// whose trigger and interrupting flag are caller-controlled. The boundary
// carries WithBoundaryAction("notify") so the fire-once action is exercised.
//
//	Start → UserTask("work") → End
//	               ↑ boundary "bnd" (timer/signal/message, interrupting/non-interrupting) → End2
func boundaryActionDef(boundaryOpts ...event.BoundaryOption) *model.ProcessDefinition {
	nodes := []model.Node{
		event.NewStart("start"),
		activity.NewUserTask("work", nil),
		event.NewBoundary("bnd", "work", boundaryOpts...),
		event.NewEnd("end"),
		event.NewEnd("end2"),
	}
	flows := []flow.SequenceFlow{
		{ID: "f-start", Source: "start", Target: "work"},
		{ID: "f-work-end", Source: "work", Target: "end"},
		{ID: "f-bnd-end2", Source: "bnd", Target: "end2"},
	}
	return &model.ProcessDefinition{ID: "p-bnd-action", Version: 1, Nodes: nodes, Flows: flows}
}

// TestBoundaryActionFireOnce verifies that when a boundary event carries
// WithBoundaryAction("notify"), firing the boundary (via timer, signal, or
// message trigger; interrupting or non-interrupting) emits an
// InvokeAction{Name:"notify", FireAndForget:true} BEFORE any routing or
// drive commands in the returned command slice.
//
// Also asserts that a boundary WITHOUT an action emits NO InvokeAction for
// the "notify" name on fire.
func TestBoundaryActionFireOnce(t *testing.T) {
	t0 := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name          string
		boundaryOpts  []event.BoundaryOption
		fire          func(r1 engine.StepResult) engine.Trigger
		wantAction    bool // whether InvokeAction{Name:"notify"} is expected
		interrupting  bool // for doc; does not gate assertions
	}

	cases := []testCase{
		// --- interrupting boundaries ---
		{
			name: "timer/interrupting/with-action",
			boundaryOpts: []event.BoundaryOption{
				event.WithBoundaryTimer(schedule.AfterExpr(`"60s"`)),
				event.WithBoundaryAction("notify"),
			},
			fire: func(r1 engine.StepResult) engine.Trigger {
				for _, c := range r1.Commands {
					if st, ok := c.(engine.ScheduleTimer); ok {
						return engine.NewTimerFired(t0, st.TimerID)
					}
				}
				return nil
			},
			wantAction:   true,
			interrupting: true,
		},
		{
			name: "signal/interrupting/with-action",
			boundaryOpts: []event.BoundaryOption{
				event.WithBoundarySignal("sig"),
				event.WithBoundaryAction("notify"),
			},
			fire: func(engine.StepResult) engine.Trigger {
				return engine.NewSignalReceived(t0, "sig", nil)
			},
			wantAction:   true,
			interrupting: true,
		},
		{
			name: "message/interrupting/with-action",
			boundaryOpts: []event.BoundaryOption{
				event.WithBoundaryMessage("msg", ""),
				event.WithBoundaryAction("notify"),
			},
			fire: func(engine.StepResult) engine.Trigger {
				return engine.NewMessageReceived(t0, "msg", "", nil)
			},
			wantAction:   true,
			interrupting: true,
		},
		// --- non-interrupting boundaries ---
		{
			name: "timer/non-interrupting/with-action",
			boundaryOpts: []event.BoundaryOption{
				event.WithBoundaryTimer(schedule.AfterExpr(`"60s"`)),
				event.WithBoundaryNonInterrupting(),
				event.WithBoundaryAction("notify"),
			},
			fire: func(r1 engine.StepResult) engine.Trigger {
				for _, c := range r1.Commands {
					if st, ok := c.(engine.ScheduleTimer); ok {
						return engine.NewTimerFired(t0, st.TimerID)
					}
				}
				return nil
			},
			wantAction:   true,
			interrupting: false,
		},
		{
			name: "signal/non-interrupting/with-action",
			boundaryOpts: []event.BoundaryOption{
				event.WithBoundarySignal("sig"),
				event.WithBoundaryNonInterrupting(),
				event.WithBoundaryAction("notify"),
			},
			fire: func(engine.StepResult) engine.Trigger {
				return engine.NewSignalReceived(t0, "sig", nil)
			},
			wantAction:   true,
			interrupting: false,
		},
		{
			name: "message/non-interrupting/with-action",
			boundaryOpts: []event.BoundaryOption{
				event.WithBoundaryMessage("msg", ""),
				event.WithBoundaryNonInterrupting(),
				event.WithBoundaryAction("notify"),
			},
			fire: func(engine.StepResult) engine.Trigger {
				return engine.NewMessageReceived(t0, "msg", "", nil)
			},
			wantAction:   true,
			interrupting: false,
		},
		// --- no-action case ---
		{
			name: "signal/interrupting/no-action",
			boundaryOpts: []event.BoundaryOption{
				event.WithBoundarySignal("sig"),
				// no WithBoundaryAction
			},
			fire: func(engine.StepResult) engine.Trigger {
				return engine.NewSignalReceived(t0, "sig", nil)
			},
			wantAction:   false,
			interrupting: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := boundaryActionDef(tc.boundaryOpts...)

			// Step 1: park at UserTask — boundary should be armed.
			r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(t0, nil), engine.StepOptions{})
			require.NoError(t, err)
			require.Len(t, r1.State.Boundaries, 1, "boundary must be armed after start")

			// Build trigger.
			trg := tc.fire(r1)
			require.NotNil(t, trg, "fire trigger must be derivable")

			// Step 2: fire the boundary.
			r2, err := engine.Step(def, r1.State, trg, engine.StepOptions{})
			require.NoError(t, err)

			// Collect commands by position.
			cmds := r2.Commands

			if tc.wantAction {
				// Find the InvokeAction for "notify" and verify it comes FIRST
				// (before any routing/drive command).
				var actionIdx int = -1
				var firstNonActionIdx int = len(cmds) // sentinel: no non-action found
				for i, c := range cmds {
					if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "notify" {
						if actionIdx == -1 {
							actionIdx = i
						}
						assert.True(t, ia.FireAndForget,
							"boundary action InvokeAction must be FireAndForget")
					} else if actionIdx == -1 {
						// Track first non-action command position BEFORE the action is found.
						if firstNonActionIdx == len(cmds) {
							firstNonActionIdx = i
						}
					}
				}
				require.NotEqual(t, -1, actionIdx,
					"expected InvokeAction{Name:\"notify\", FireAndForget:true} in commands %v", cmds)
				assert.Less(t, actionIdx, firstNonActionIdx,
					"boundary InvokeAction must precede routing/drive commands")
			} else {
				// Ensure no spurious InvokeAction{Name:"notify"} was emitted.
				for _, c := range cmds {
					if ia, ok := c.(engine.InvokeAction); ok {
						assert.NotEqual(t, "notify", ia.Name,
							"boundary without action must not emit InvokeAction for \"notify\"")
					}
				}
			}
		})
	}
}
