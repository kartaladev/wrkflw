package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

func TestTriggersCarryOccurredAt(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)

	var trs []engine.Trigger = []engine.Trigger{
		engine.NewStartInstance(at, map[string]any{"x": 1}),
		engine.NewActionCompleted(at, "c1", map[string]any{"ok": true}),
		engine.NewActionFailed(at, "c1", "boom", true),
	}
	for _, tr := range trs {
		assert.Equal(t, at, tr.OccurredAt())
	}

	ac := engine.NewActionCompleted(at, "c1", map[string]any{"ok": true})
	assert.Equal(t, "c1", ac.CommandID)
	assert.Equal(t, true, ac.Output["ok"])
}
