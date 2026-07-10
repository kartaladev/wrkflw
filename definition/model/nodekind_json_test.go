package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// TestNodeKindJSONMarshal verifies that every NodeKind constant serialises to
// a stable, human-readable JSON string (NOT a bare integer) and round-trips
// back via unmarshal to the original value.
func TestNodeKindJSONMarshal(t *testing.T) {
	cases := []struct {
		kind    model.NodeKind
		wantStr string // the JSON representation, including surrounding quotes
	}{
		{model.KindUnspecified, `"unspecified"`},
		{model.KindStartEvent, `"startEvent"`},
		{model.KindEndEvent, `"endEvent"`},
		{model.KindErrorEndEvent, `"errorEndEvent"`},
		{model.KindServiceTask, `"serviceTask"`},
		{model.KindUserTask, `"userTask"`},
		{model.KindReceiveTask, `"receiveTask"`},
		{model.KindSendTask, `"sendTask"`},
		{model.KindBusinessRuleTask, `"businessRuleTask"`},
		{model.KindSubProcess, `"subProcess"`},
		{model.KindCallActivity, `"callActivity"`},
		{model.KindEventSubProcess, `"eventSubProcess"`},
		{model.KindIntermediateCatchEvent, `"intermediateCatchEvent"`},
		{model.KindIntermediateThrowEvent, `"intermediateThrowEvent"`},
		{model.KindBoundaryEvent, `"boundaryEvent"`},
		{model.KindExclusiveGateway, `"exclusiveGateway"`},
		{model.KindParallelGateway, `"parallelGateway"`},
		{model.KindInclusiveGateway, `"inclusiveGateway"`},
		{model.KindEventBasedGateway, `"eventBasedGateway"`},
	}

	for _, tc := range cases {
		t.Run(tc.wantStr, func(t *testing.T) {
			// Marshal → must produce a JSON string (not a number).
			data, err := json.Marshal(tc.kind)
			require.NoError(t, err)
			assert.Equal(t, tc.wantStr, string(data),
				"NodeKind must marshal to its name string, not a raw integer")

			// The marshalled form must start with a quote (i.e. it is a JSON string).
			assert.Equal(t, byte('"'), data[0],
				"marshalled NodeKind must be a JSON string, not a number")

			// Round-trip: unmarshal back to NodeKind.
			var got model.NodeKind
			require.NoError(t, json.Unmarshal(data, &got))
			assert.Equal(t, tc.kind, got, "NodeKind must round-trip through JSON marshal/unmarshal")
		})
	}
}

// TestNodeKindJSONUnmarshalUnknown verifies that unmarshalling an unrecognised
// name returns an error rather than silently producing a zero-value.
func TestNodeKindJSONUnmarshalUnknown(t *testing.T) {
	var k model.NodeKind
	err := json.Unmarshal([]byte(`"notANodeKind"`), &k)
	require.Error(t, err, "unmarshalling an unknown NodeKind name must return an error")
}

// TestNodeKindTerminateEndRetired verifies that the retired "terminateEndEvent"
// wire name no longer resolves to a NodeKind (TerminateEndEvent was folded into
// EndEvent's ForceTermination field — ADR-0119).
func TestNodeKindTerminateEndRetired(t *testing.T) {
	t.Parallel()
	var k model.NodeKind
	if err := k.UnmarshalJSON([]byte(`"terminateEndEvent"`)); err == nil {
		t.Fatal("UnmarshalJSON(\"terminateEndEvent\") = nil error, want error (kind retired)")
	}
}

// TestNodeKindJSONInNode verifies that a Node containing a NodeKind field
// round-trips through json.Marshal/Unmarshal with the name encoding.
// Uses ProcessDefinition (Un)MarshalJSON which routes through NodeWire.
func TestNodeKindJSONInNode(t *testing.T) {
	def := &model.ProcessDefinition{
		ID:      "p",
		Version: 1,
		Nodes:   []model.Node{event.NewStart("start", event.WithName("Order Received"))},
		Flows:   []flow.SequenceFlow{},
	}

	data, err := json.Marshal(def)
	require.NoError(t, err)

	// The JSON must contain the string "startEvent", not a number like "1".
	assert.Contains(t, string(data), `"startEvent"`,
		"NodeKind inside a Node must be encoded as a name string")

	var got model.ProcessDefinition
	require.NoError(t, json.Unmarshal(data, &got))
	require.Len(t, got.Nodes, 1)
	assert.Equal(t, model.KindStartEvent, got.Nodes[0].Kind())
	assert.Equal(t, "Order Received", got.Nodes[0].Name())
}
