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
		name         string
		boundaryOpts []event.BoundaryOption
		fire         func(r1 engine.StepResult) engine.Trigger
		wantAction   bool // whether InvokeAction{Name:"notify"} is expected
		interrupting bool // for doc; does not gate assertions
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
				actionIdx := -1
				firstNonActionIdx := len(cmds) // sentinel: no non-action found
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

// TestBoundaryActionFireOnErrorBoundary verifies Fix 1: when an error boundary
// carries WithBoundaryAction("notify"), the fire-once InvokeAction is emitted
// (FireAndForget:true) when the error is caught — for BOTH direct-attachment
// (ActionFailed on root-level svc) and enclosing-scope (error escapes from
// sub-process). Commands must contain InvokeAction{Name:"notify", FireAndForget:true}
// and no FailInstance.
func TestBoundaryActionFireOnErrorBoundary(t *testing.T) {
	t0 := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	t.Run("direct-attachment-error-boundary-emits-action", func(t *testing.T) {
		t.Parallel()
		// Root: start → svc → end
		//       svc has an error boundary "E1" + WithBoundaryAction("notify") → end-recover
		def := &model.ProcessDefinition{
			ID: "p-bnd-act-err-direct", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
				event.NewBoundary("bnd-err", "svc",
					event.WithBoundaryErrorCode("E1"),
					event.WithBoundaryAction("notify"),
				),
				event.NewEnd("end"),
				event.NewEnd("end-recover"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "start", Target: "svc"},
				{ID: "f2", Source: "svc", Target: "end"},
				{ID: "f3", Source: "bnd-err", Target: "end-recover"},
			},
		}

		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
			engine.NewStartInstance(t0, nil), engine.StepOptions{})
		require.NoError(t, err)

		var ia *engine.InvokeAction
		for _, c := range r1.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				ia = &vv
				break
			}
		}
		require.NotNil(t, ia)

		r2, err := engine.Step(def, r1.State,
			engine.NewActionFailed(t0.Add(time.Second), ia.CommandID, "E1", false),
			engine.StepOptions{})
		require.NoError(t, err)

		// Must NOT fail the instance (error boundary routes to end-recover → StatusCompleted).
		assert.NotEqual(t, engine.StatusFailed, r2.State.Status,
			"instance must NOT be failed when error boundary catches")
		for _, c := range r2.Commands {
			_, isFailInstance := c.(engine.FailInstance)
			require.False(t, isFailInstance, "FailInstance must NOT be emitted when error boundary catches")
		}

		// InvokeAction{Name:"notify", FireAndForget:true} must appear.
		var notifyIA *engine.InvokeAction
		for _, c := range r2.Commands {
			if v, ok := c.(engine.InvokeAction); ok && v.Name == "notify" {
				vv := v
				notifyIA = &vv
				break
			}
		}
		require.NotNil(t, notifyIA,
			"InvokeAction{Name:\"notify\", FireAndForget:true} must be emitted when error boundary catches")
		assert.True(t, notifyIA.FireAndForget,
			"error boundary action InvokeAction must be FireAndForget")
	})

	t.Run("enclosing-scope-error-boundary-emits-action", func(t *testing.T) {
		t.Parallel()
		// Root: start → sub(sp) → end-ok
		//       sp has boundary error "E1" + WithBoundaryAction("notify") → end-recover
		// Nested (sp): start → svc → errorEnd("E1")
		nestedDef := &model.ProcessDefinition{
			ID: "sp-bnd-act-err", Version: 1,
			Nodes: []model.Node{
				event.NewStart("inner-start"),
				activity.NewServiceTask("inner-svc", activity.WithActionName("inner-action")),
				event.NewErrorEnd("inner-err-end", "E1"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "fi1", Source: "inner-start", Target: "inner-svc"},
				{ID: "fi2", Source: "inner-svc", Target: "inner-err-end"},
			},
		}
		def := &model.ProcessDefinition{
			ID: "p-bnd-act-err-enclosing", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				activity.NewSubProcess("sp", nestedDef),
				event.NewBoundary("bnd-err", "sp",
					event.WithBoundaryErrorCode("E1"),
					event.WithBoundaryAction("notify"),
				),
				event.NewEnd("end-ok"),
				event.NewEnd("end-recover"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "start", Target: "sp"},
				{ID: "f2", Source: "sp", Target: "end-ok"},
				{ID: "f3", Source: "bnd-err", Target: "end-recover"},
			},
		}

		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
			engine.NewStartInstance(t0, nil), engine.StepOptions{})
		require.NoError(t, err)

		var ia *engine.InvokeAction
		for _, c := range r1.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				ia = &vv
				break
			}
		}
		require.NotNil(t, ia)

		// complete inner-svc → inner-err-end fires → error E1 propagates → bnd-err catches
		r2, err := engine.Step(def, r1.State,
			engine.NewActionCompleted(t0.Add(time.Second), ia.CommandID, nil), engine.StepOptions{})
		require.NoError(t, err)

		// Must NOT fail the instance (error boundary routes to end-recover → StatusCompleted).
		assert.NotEqual(t, engine.StatusFailed, r2.State.Status,
			"instance must NOT be failed when enclosing-scope error boundary catches")
		for _, c := range r2.Commands {
			_, isFailInstance := c.(engine.FailInstance)
			require.False(t, isFailInstance, "FailInstance must NOT be emitted when error boundary catches")
		}

		// InvokeAction{Name:"notify", FireAndForget:true} must appear.
		var notifyIA *engine.InvokeAction
		for _, c := range r2.Commands {
			if v, ok := c.(engine.InvokeAction); ok && v.Name == "notify" {
				vv := v
				notifyIA = &vv
				break
			}
		}
		require.NotNil(t, notifyIA,
			"InvokeAction{Name:\"notify\", FireAndForget:true} must be emitted by enclosing-scope error boundary")
		assert.True(t, notifyIA.FireAndForget,
			"enclosing-scope error boundary action must be FireAndForget")
	})
}
