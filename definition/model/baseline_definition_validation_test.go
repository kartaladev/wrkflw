package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestBaselineValidateRefusesTwoBlankNodeIDs characterizes the
// definition-validation scenario "Each structural defect is refused by its
// sentinel", row "has two nodes whose ID is blank"
// (openspec/specs/definition-validation/spec.md): a blank node ID is a lookup
// key like any other, so two of them are reported as ErrDuplicateNodeID.
func TestBaselineValidateRefusesTwoBlankNodeIDs(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s"),
			event.NewEnd(""),
			event.NewEnd(""),
		},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "s", Target: ""}},
	}

	err := model.Validate(def)
	require.ErrorIs(t, err, model.ErrDuplicateNodeID)
	assert.Contains(t, err.Error(), `node ""`, "the duplicate reported must be the blank ID")
}
