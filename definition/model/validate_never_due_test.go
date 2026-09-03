package model_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
)

// neverDueTimerDef builds a root definition whose IntermediateCatchEvent "catch"
// carries spec as its timer trigger.
func neverDueTimerDef(spec schedule.TriggerSpec) *model.ProcessDefinition {
	return catchDef(event.NewIntermediateCatch("catch", event.WithCatchTimer(spec)))
}

// neverDueWaitDef builds a root definition whose UserTask "ut" carries spec as
// its in-wait reminder trigger (WaitEvery).
func neverDueWaitDef(spec schedule.TriggerSpec) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("ut",
				activity.WithEligibleRoles("reviewer"),
				activity.WithWaitAction(spec, "remind")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "ut"},
			{ID: "f2", Source: "ut", Target: "end"},
		},
	}
}

// nestedDef wraps inner in a KindSubProcess node "sp" of an otherwise valid root
// definition. It is the fixture that distinguishes a rule placed inside
// validateStructure (which recurses) from one placed in Validate (which does
// not) — see ADR-0182.
func nestedDef(inner *model.ProcessDefinition) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", inner),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sp"},
			{ID: "f2", Source: "sp", Target: "end"},
		},
	}
}

// TestValidate_RejectsNeverDueTrigger covers ADR-0182's authoring gate: a node
// whose timer or in-wait trigger is never due at ANY anchor is rejected by
// model.Validate, at the root AND inside a nested sub-process. The
// MUST-NOT-REJECT rows are the soundness guards — every one of them is
// measurably due at some anchor, and rejecting any of them would be the
// ADR-0165 inverted-predicate shape.
func TestValidate_RejectsNeverDueTrigger(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		// --- REJECTED, timer trigger, at the ROOT ---
		{
			name: "root timer Every(0) is rejected",
			def:  neverDueTimerDef(schedule.Every(0)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrTriggerNeverDue)
				assert.Contains(t, err.Error(), `node "catch"`)
			},
		},
		{
			name: "root timer Monthly(0, [15]) is rejected",
			def:  neverDueTimerDef(schedule.Monthly(0, []int{15})),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrTriggerNeverDue)
				assert.Contains(t, err.Error(), `node "catch"`)
			},
		},
		{
			name: "root timer Daily(1, 25:00) is rejected",
			def:  neverDueTimerDef(schedule.Daily(1, schedule.ClockTime{Hour: 25})),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrTriggerNeverDue)
				assert.Contains(t, err.Error(), `node "catch"`)
			},
		},
		{
			name: "root timer Weekly(1, [-1]) is rejected",
			def:  neverDueTimerDef(schedule.Weekly(1, []time.Weekday{time.Weekday(-1)})),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrTriggerNeverDue)
				assert.Contains(t, err.Error(), `node "catch"`)
			},
		},

		// --- REJECTED, timer trigger, NESTED (the load-bearing placement row) ---
		{
			name: "nested timer Every(0) is rejected and names the host sub-process",
			def:  nestedDef(neverDueTimerDef(schedule.Every(0))),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrTriggerNeverDue)
				assert.Contains(t, err.Error(), `subprocess "sp"`)
				assert.Contains(t, err.Error(), `node "catch"`)
			},
		},
		{
			name: "nested timer Monthly(0, [15]) is rejected",
			def:  nestedDef(neverDueTimerDef(schedule.Monthly(0, []int{15}))),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrTriggerNeverDue)
				assert.Contains(t, err.Error(), `subprocess "sp"`)
			},
		},

		// --- REJECTED, WaitEvery (in-wait reminder), root AND nested ---
		{
			name: "root WaitEvery(0) is rejected",
			def:  neverDueWaitDef(schedule.Every(0)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrTriggerNeverDue)
				assert.Contains(t, err.Error(), `node "ut"`)
				assert.Contains(t, err.Error(), "in-wait")
			},
		},
		{
			name: "nested WaitEvery(0) is rejected and names the host sub-process",
			def:  nestedDef(neverDueWaitDef(schedule.Every(0))),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrTriggerNeverDue)
				assert.Contains(t, err.Error(), `subprocess "sp"`)
				assert.Contains(t, err.Error(), `node "ut"`)
				assert.Contains(t, err.Error(), "in-wait")
			},
		},

		// --- MUST NOT REJECT (soundness guards): every spec below is measurably
		// due at some anchor. See spec §3.1.
		{
			name: "root timer Monthly(12, [31]) is anchor-dependent and accepted",
			def:  neverDueTimerDef(schedule.Monthly(12, []int{31})),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "root timer Monthly(1, [-1]) counts back from month end and is accepted",
			def:  neverDueTimerDef(schedule.Monthly(1, []int{-1})),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "root timer Weekly(1, [Weekday(9)]) is a raw day offset and is accepted",
			def:  neverDueTimerDef(schedule.Weekly(1, []time.Weekday{time.Weekday(9)})),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "root timer Weekly(1, [Weekday(-1), Monday]) is a mixed set and is accepted",
			def:  neverDueTimerDef(schedule.Weekly(1, []time.Weekday{time.Weekday(-1), time.Monday})),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "root timer Cron(0 0 30 2 *) is out of scope and is accepted",
			def:  neverDueTimerDef(schedule.Cron("0 0 30 2 *")),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "root timer Every(1h) is accepted",
			def:  neverDueTimerDef(schedule.Every(time.Hour)),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "nested timer Monthly(12, [31]) is accepted",
			def:  nestedDef(neverDueTimerDef(schedule.Monthly(12, []int{31}))),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "root WaitEvery Monthly(12, [31]) is accepted",
			def:  neverDueWaitDef(schedule.Monthly(12, []int{31})),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "root WaitEvery(1h) is accepted",
			def:  neverDueWaitDef(schedule.Every(time.Hour)),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			// The catch probe for the same guard cannot assert NoError: a catch
			// event with an absent timer names no trigger family at all, which
			// ErrCatchEventMissingTrigger rejects for an unrelated reason. What
			// ADR-0182 needs from this row is narrower and still exact — the
			// never-due predicate must not read an ABSENT trigger as never due.
			name: "catch with no timer is not rejected as never due",
			def:  neverDueTimerDef(schedule.TriggerSpec{}),
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrTriggerNeverDue)
			},
		},
		{
			name: "node with no timer and no in-wait trigger is accepted",
			def:  neverDueWaitDef(schedule.TriggerSpec{}),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, model.Validate(tc.def))
		})
	}
}
