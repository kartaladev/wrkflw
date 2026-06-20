package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

func TestCommandsImplementInterface(t *testing.T) {
	var cmds []engine.Command = []engine.Command{
		engine.InvokeAction{CommandID: "c1", Name: "greet", Input: map[string]any{"a": 1}},
		engine.CompleteInstance{Result: map[string]any{"done": true}},
		engine.FailInstance{Err: "boom"},
	}
	assert.Len(t, cmds, 3)

	ia, ok := cmds[0].(engine.InvokeAction)
	assert.True(t, ok)
	assert.Equal(t, "greet", ia.Name)
}
