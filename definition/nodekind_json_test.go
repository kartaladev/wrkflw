package definition_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
)

// TestNodeKindJSONMarshal verifies that every NodeKind constant serialises to
// a stable, human-readable JSON string (NOT a bare integer) and round-trips
// back via unmarshal to the original value.
func TestNodeKindJSONMarshal(t *testing.T) {
	cases := []struct {
		kind    definition.NodeKind
		wantStr string // the JSON representation, including surrounding quotes
	}{
		{definition.KindUnspecified, `"unspecified"`},
		{definition.KindStartEvent, `"startEvent"`},
		{definition.KindEndEvent, `"endEvent"`},
		{definition.KindTerminateEndEvent, `"terminateEndEvent"`},
		{definition.KindErrorEndEvent, `"errorEndEvent"`},
		{definition.KindServiceTask, `"serviceTask"`},
		{definition.KindUserTask, `"userTask"`},
		{definition.KindReceiveTask, `"receiveTask"`},
		{definition.KindSendTask, `"sendTask"`},
		{definition.KindBusinessRuleTask, `"businessRuleTask"`},
		{definition.KindSubProcess, `"subProcess"`},
		{definition.KindCallActivity, `"callActivity"`},
		{definition.KindEventSubProcess, `"eventSubProcess"`},
		{definition.KindIntermediateCatchEvent, `"intermediateCatchEvent"`},
		{definition.KindIntermediateThrowEvent, `"intermediateThrowEvent"`},
		{definition.KindBoundaryEvent, `"boundaryEvent"`},
		{definition.KindExclusiveGateway, `"exclusiveGateway"`},
		{definition.KindParallelGateway, `"parallelGateway"`},
		{definition.KindInclusiveGateway, `"inclusiveGateway"`},
		{definition.KindEventBasedGateway, `"eventBasedGateway"`},
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
			var got definition.NodeKind
			require.NoError(t, json.Unmarshal(data, &got))
			assert.Equal(t, tc.kind, got, "NodeKind must round-trip through JSON marshal/unmarshal")
		})
	}
}

// TestNodeKindJSONUnmarshalUnknown verifies that unmarshalling an unrecognised
// name returns an error rather than silently producing a zero-value.
func TestNodeKindJSONUnmarshalUnknown(t *testing.T) {
	var k definition.NodeKind
	err := json.Unmarshal([]byte(`"notANodeKind"`), &k)
	require.Error(t, err, "unmarshalling an unknown NodeKind name must return an error")
}

// TestNodeKindJSONInNode verifies that a Node containing a NodeKind field
// round-trips through json.Marshal/Unmarshal with the name encoding.
// Uses ProcessDefinition (Un)MarshalJSON which routes through NodeWire.
func TestNodeKindJSONInNode(t *testing.T) {
	def := &definition.ProcessDefinition{
		ID:      "p",
		Version: 1,
		Nodes:   []definition.Node{definition.NewStartEvent("start", definition.WithName("Order Received"))},
		Flows:   []definition.SequenceFlow{},
	}

	data, err := json.Marshal(def)
	require.NoError(t, err)

	// The JSON must contain the string "startEvent", not a number like "1".
	assert.Contains(t, string(data), `"startEvent"`,
		"NodeKind inside a Node must be encoded as a name string")

	var got definition.ProcessDefinition
	require.NoError(t, json.Unmarshal(data, &got))
	require.Len(t, got.Nodes, 1)
	assert.Equal(t, definition.KindStartEvent, got.Nodes[0].Kind())
	assert.Equal(t, "Order Received", got.Nodes[0].Name())
}
