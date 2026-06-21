package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/model"
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
		{model.KindTerminateEndEvent, `"terminateEndEvent"`},
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

// TestNodeKindJSONInNode verifies that a Node containing a NodeKind field
// round-trips through json.Marshal/Unmarshal with the name encoding.
func TestNodeKindJSONInNode(t *testing.T) {
	node := model.Node{
		ID:   "start",
		Kind: model.KindStartEvent,
		Name: "Order Received",
	}

	data, err := json.Marshal(node)
	require.NoError(t, err)

	// The JSON must contain the string "startEvent", not a number like "1".
	assert.Contains(t, string(data), `"startEvent"`,
		"NodeKind inside a Node must be encoded as a name string")

	var got model.Node
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, model.KindStartEvent, got.Kind)
	assert.Equal(t, "Order Received", got.Name)
}
