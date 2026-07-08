package definition_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestLintReminderIgnored(t *testing.T) {
	every := schedule.Every(30 * time.Minute)

	cases := []struct {
		name   string
		node   model.Node
		assert func(t *testing.T, warns []definition.Warning)
	}{
		{
			name: "reminder on ServiceTask is flagged (kind does not arm reminders)",
			node: activity.NewServiceTask("svc", activity.WithActionName("do"), activity.WithWaitReminder(every, "nudge")),
			assert: func(t *testing.T, warns []definition.Warning) {
				require.Len(t, warns, 1)
				assert.Equal(t, "svc", warns[0].NodeID)
				assert.Equal(t, "reminder-ignored", warns[0].Rule)
				assert.NotEmpty(t, warns[0].Detail)
			},
		},
		{
			name: "reminder on UserTask is honoured (no warning)",
			node: activity.NewUserTask("ut", []string{"reviewer"}, activity.WithWaitReminder(every, "nudge")),
			assert: func(t *testing.T, warns []definition.Warning) {
				assert.Empty(t, warns)
			},
		},
		{
			name: "reminder on ReceiveTask is honoured (no warning)",
			node: activity.NewReceiveTask("rcv", "PaymentReceived", activity.WithWaitReminder(every, "nudge")),
			assert: func(t *testing.T, warns []definition.Warning) {
				assert.Empty(t, warns)
			},
		},
		{
			name: "reminder on IntermediateCatchEvent is honoured (no warning)",
			node: event.NewCatch("catch", event.WithCatchSignal("approved"), event.WithCatchWaitReminder(every, "nudge")),
			assert: func(t *testing.T, warns []definition.Warning) {
				assert.Empty(t, warns)
			},
		},
		{
			name: "ServiceTask without a reminder produces no warning",
			node: activity.NewServiceTask("svc2", activity.WithActionName("do")),
			assert: func(t *testing.T, warns []definition.Warning) {
				assert.Empty(t, warns)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := &model.ProcessDefinition{ID: "d", Version: 1, Nodes: []model.Node{tc.node}}
			tc.assert(t, definition.Lint(def))
		})
	}
}

func TestLintNilDefinition(t *testing.T) {
	assert.Nil(t, definition.Lint(nil))
}
