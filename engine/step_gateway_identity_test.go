package engine_test

// step_gateway_identity_test.go — backlog 74. resolveGatewayWin used to locate
// the parked event-gateway token by the STRING "evtgw:"+ae.GatewayToken via
// InstanceState.tokenAwaiting, which returns the FIRST token whose AwaitCommand
// equals that string. Token.AwaitCommand also carries consumer-supplied ids
// (a human-task id, a service-action command id), and [engine.IDGenerator] is a
// seam the engine delegates to verbatim — nothing validates the returned
// string. A generator that mints "evtgw:<some token id>" therefore lets an
// unrelated token impersonate the gateway token.
//
// Mint order for evtgwIdentityDef, EXECUTED with a sequential generator before
// this test was written (probe output, deleted afterwards):
//
//	mint log: [id1 id2 id3 id4 id5]
//	token[0] id="id2" node="task"  state=Waiting awaitCmd="id4"
//	token[1] id="id3" node="evtgw" state=Waiting awaitCmd="evtgw:id3"
//	task[0]  id="id4" node="task"
//	cmd[0] AwaitHuman{TaskID:id4}
//	cmd[1] ScheduleTimer{TimerID:id5 Token:id3}
//
// so call #1 mints the root token, #2 the task-branch token, #3 the
// gateway-branch token (id3), #4 the human-task id, #5 the gateway timer id.
// Substituting call #4 with "evtgw:id3" reproduces the collision, and the task
// token sits at index 0 — AHEAD of the real gateway token — so the first-match
// lookup returns the wrong one.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// scriptedIDGen mints "id1", "id2", … unless the call index (1-based) has a
// substitution scripted for it, in which case the scripted literal is returned.
// It stands in for a consumer generator whose id space is not under the
// engine's control.
type scriptedIDGen struct {
	mu   sync.Mutex
	n    int
	subs map[int]string
}

func (g *scriptedIDGen) NewID() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	if id, ok := g.subs[g.n]; ok {
		return id, nil
	}
	return fmt.Sprintf("id%d", g.n), nil
}

// evtgwIdentityDef forks a user task alongside an event-based gateway, so the
// instance holds TWO parked tokens and the task token precedes the gateway
// token in s.Tokens.
//
//	start → fork ⇉ task[UserTask]              → end1
//	             ⇉ evtgw → tcatch[timer "1h"]   → end2
//	                     → scatch[signal "ok"]  → end3
func evtgwIdentityDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-evtgw-identity", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("task"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("tcatch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			event.NewIntermediateCatch("scatch", event.WithSignalName("ok")),
			event.NewEnd("end1"),
			event.NewEnd("end2"),
			event.NewEnd("end3"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "fork"},
			{ID: "f-fork-task", Source: "fork", Target: "task"},
			{ID: "f-fork-gw", Source: "fork", Target: "evtgw"},
			{ID: "f-task-end", Source: "task", Target: "end1"},
			{ID: "f-gw-timer", Source: "evtgw", Target: "tcatch"},
			{ID: "f-gw-signal", Source: "evtgw", Target: "scatch"},
			{ID: "f-timer-end", Source: "tcatch", Target: "end2"},
			{ID: "f-signal-end", Source: "scatch", Target: "end3"},
		},
	}
}

// TestEventGatewayWinResolvesTokenByIdentity asserts an event-gateway win moves
// the GATEWAY's own token, identified by ae.GatewayToken, and never a bystander
// token that merely carries a colliding AwaitCommand string.
//
// What makes the "colliding" case fail before the fix: resolveGatewayWin called
// s.tokenAwaiting("evtgw:id3"), the user-task token at index 0 carries the same
// string as its human-task id, so it is returned first — the task token is
// driven down the timer branch and the real gateway token stays parked at
// "evtgw" forever. The "distinct" case is the control: it passes before and
// after, and only proves the fixture really drives a gateway win.
func TestEventGatewayWinResolvesTokenByIdentity(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		subs   map[int]string
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "colliding human-task id does not impersonate the gateway token",
			// Call #4 is the human-task id; "evtgw:id3" is the gateway token's
			// own park sentinel.
			subs: map[int]string{4: "evtgw:id3"},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)

				assert.Nil(t, tokenAtNode(res.State, "evtgw"),
					"the gateway token must leave evtgw when its timer arm wins")

				task := tokenAtNode(res.State, "task")
				require.NotNil(t, task, "the user-task token must stay parked on its own node")
				assert.Equal(t, "id2", task.ID, "the parked token must be the task-branch token")
				assert.Equal(t, engine.TokenWaiting, task.State)
			},
		},
		{
			name: "distinct ids route the gateway token normally",
			subs: nil,
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)

				assert.Nil(t, tokenAtNode(res.State, "evtgw"),
					"the gateway token must leave evtgw when its timer arm wins")

				task := tokenAtNode(res.State, "task")
				require.NotNil(t, task, "the user-task token must stay parked on its own node")
				assert.Equal(t, "id2", task.ID)
				assert.Equal(t, engine.TokenWaiting, task.State)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gen := &scriptedIDGen{subs: tc.subs}
			def := evtgwIdentityDef()

			started, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{IDGenerator: gen})
			require.NoError(t, err)

			var timerID string
			for _, cmd := range started.Commands {
				if st, ok := cmd.(engine.ScheduleTimer); ok {
					timerID = st.TimerID
				}
			}
			require.NotEmpty(t, timerID, "the gateway timer arm must have been scheduled")

			res, err := engine.Step(t.Context(), def, started.State,
				engine.NewTimerFired(at.Add(time.Hour), timerID), engine.StepOptions{IDGenerator: gen})
			tc.assert(t, res, err)
		})
	}
}
