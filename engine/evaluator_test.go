package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// recordingEvaluator is a fake engine.ConditionEvaluator that records the
// boolean expressions it is asked to evaluate and returns a fixed verdict, so a
// test can prove the injected evaluator (not the package-global default) is the
// one consulted by Step.
type recordingEvaluator struct {
	boolCodes []string
	boolValue bool
	boolErr   error
}

func (r *recordingEvaluator) EvalBool(code string, _ map[string]any) (bool, error) {
	r.boolCodes = append(r.boolCodes, code)
	return r.boolValue, r.boolErr
}

func (r *recordingEvaluator) EvalDuration(string, map[string]any) (time.Duration, error) {
	return 0, nil
}

func (r *recordingEvaluator) EvalString(string, map[string]any) (string, error) {
	return "", nil
}

// TestStepUsesInjectedEvaluatorForGatewayCondition proves that when a
// ConditionEvaluator is supplied via StepOptions, Step consults it for an
// exclusive-gateway condition instead of the package-global default. The fake
// is rigged to return true for the conditional flow so the asserted branch is
// chosen only if the injected evaluator was actually used.
func TestStepUsesInjectedEvaluatorForGatewayCondition(t *testing.T) {
	at := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	rec := &recordingEvaluator{boolValue: true}

	// amount is 5 (< 100): the real evaluator would take the default branch
	// ("small"). The fake forces true, so reaching "big" proves the fake ran.
	res, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}),
		engine.StepOptions{Evaluator: rec})
	require.NoError(t, err)

	require.Len(t, res.State.Tokens, 1)
	assert.Equal(t, "big", res.State.Tokens[0].NodeID,
		"injected evaluator should have decided the branch")
	assert.Equal(t, []string{"amount > 100"}, rec.boolCodes,
		"injected evaluator should have been consulted with the flow condition")
}

// TestStepNilEvaluatorFallsBackToGlobal proves the default path is unchanged:
// with no injected evaluator the package-global (pure) evaluator decides, so
// amount=5 takes the default branch.
func TestStepNilEvaluatorFallsBackToGlobal(t *testing.T) {
	at := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

	res, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}),
		engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.State.Tokens, 1)
	assert.Equal(t, "small", res.State.Tokens[0].NodeID)
}

// TestStepInjectedEvaluatorErrorPropagates proves an evaluator error surfaces
// from Step (e.g. an expreval timeout), wired through the gateway evaluation.
func TestStepInjectedEvaluatorErrorPropagates(t *testing.T) {
	at := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	sentinel := errors.New("boom")
	rec := &recordingEvaluator{boolErr: sentinel}

	_, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}),
		engine.StepOptions{Evaluator: rec})
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

// compile-time check that the fake satisfies the interface.
var _ engine.ConditionEvaluator = (*recordingEvaluator)(nil)
