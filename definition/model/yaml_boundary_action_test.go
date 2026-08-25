package model_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
)

// boundaryActionYAML authors a boundary event carrying both of the fields
// event.WithBoundaryAction and event.WithBoundaryErrorExpr set in Go.
//
// What makes this fail before backlog 143 is fixed: nodeYAML declares neither
// boundary_action nor boundary_error_expr, and ParseYAML decodes with
// KnownFields(true), so the document is REJECTED at parse time.
const boundaryActionYAML = `
id: boundary-action-yaml
version: 1
nodes:
  - id: start
    kind: startEvent
  - id: charge
    kind: serviceTask
    action: charge-card
  - id: overdue
    kind: boundaryEvent
    attached_to: charge
    error_code: CARD_DECLINED
    boundary_action: notify-overdue
    boundary_error_expr: '_error == "CARD_DECLINED"'
  - id: finish
    kind: endEvent
flows:
  - id: f1
    source: start
    target: charge
  - id: f2
    source: charge
    target: finish
  - id: f3
    source: overdue
    target: finish
`

// TestParseYAMLBoundaryActionAndErrorExpr is the regression test for backlog
// 143: two supported, documented, exampled boundary options were unreachable
// from YAML, which CLAUDE.md makes a first-class authoring form.
//
// ⚠ Adding the fields to nodeYAML WITHOUT the fromNodeYAML mapping is a NET
// REGRESSION rather than a partial fix: the keys would then parse cleanly and
// arrive empty, where today they fail loudly. The value assertions below — not
// the parse — are what distinguish the two.
func TestParseYAMLBoundaryActionAndErrorExpr(t *testing.T) {
	t.Parallel()

	loaded, err := model.ParseYAML(strings.NewReader(boundaryActionYAML))
	require.NoError(t, err, "boundary_action / boundary_error_expr must be accepted by the YAML surface")

	def, err := loaded.Build()
	require.NoError(t, err)

	var found *event.BoundaryEvent
	for _, n := range def.Nodes {
		if n.ID() != "overdue" {
			continue
		}
		be, ok := n.(event.BoundaryEvent)
		require.True(t, ok, "node %q decoded as %T, want event.BoundaryEvent", n.ID(), n)
		found = &be
	}
	require.NotNil(t, found, "boundary node %q missing from the built definition", "overdue")

	assert.Equal(t, "notify-overdue", found.Action,
		"boundary_action must reach BoundaryEvent.Action — an empty value here means the "+
			"nodeYAML field was added without its fromNodeYAML mapping")
	assert.Equal(t, `_error == "CARD_DECLINED"`, found.ErrorExpr,
		"boundary_error_expr must reach BoundaryEvent.ErrorExpr")

	// ⚠ The predicate above uses `_error`, the ONLY variable the evaluator injects
	// (engine/step_errors.go: env = instance vars + env["_error"] = errorCode, a
	// string). An earlier draft of this fixture wrote `error.code == "..."`, which
	// no environment defines. That matters more than a typo: when ErrorExpr is set
	// it is the SOLE decider (definition/event/options.go), the evaluation error is
	// only slog.Debug'd, and the boundary then silently never catches — with the
	// error_code authored one line above never consulted. This fixture is the only
	// YAML documentation of the key, so it must show the working form.
}
