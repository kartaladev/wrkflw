package definition

import (
	"encoding/json"
	"fmt"
)

// nodeKindNames maps each NodeKind constant to its stable JSON name.
// The names follow BPMN2 lowerCamelCase convention so stored JSONB is human-readable
// and independent of iota ordering — reordering or inserting a constant in the iota
// block never corrupts previously persisted process definitions.
var nodeKindNames = map[NodeKind]string{
	KindUnspecified:            "unspecified",
	KindStartEvent:             "startEvent",
	KindEndEvent:               "endEvent",
	KindTerminateEndEvent:      "terminateEndEvent",
	KindErrorEndEvent:          "errorEndEvent",
	KindServiceTask:            "serviceTask",
	KindUserTask:               "userTask",
	KindReceiveTask:            "receiveTask",
	KindSendTask:               "sendTask",
	KindBusinessRuleTask:       "businessRuleTask",
	KindSubProcess:             "subProcess",
	KindCallActivity:           "callActivity",
	KindEventSubProcess:        "eventSubProcess",
	KindIntermediateCatchEvent: "intermediateCatchEvent",
	KindIntermediateThrowEvent: "intermediateThrowEvent",
	KindBoundaryEvent:          "boundaryEvent",
	KindExclusiveGateway:       "exclusiveGateway",
	KindParallelGateway:        "parallelGateway",
	KindInclusiveGateway:       "inclusiveGateway",
	KindEventBasedGateway:      "eventBasedGateway",
}

// nodeKindByName is the reverse of nodeKindNames, built at init time.
var nodeKindByName map[string]NodeKind

func init() {
	nodeKindByName = make(map[string]NodeKind, len(nodeKindNames))
	for k, v := range nodeKindNames {
		nodeKindByName[v] = k
	}
}

// String returns the stable lowerCamelCase name of the NodeKind (e.g. "startEvent").
// It implements fmt.Stringer so NodeKind values format correctly with %s and %v.
func (k NodeKind) String() string {
	if name, ok := nodeKindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("NodeKind(%d)", int(k))
}

// MarshalJSON encodes NodeKind as its stable name string (e.g. "startEvent").
// Encoding by name rather than by iota ordinal ensures that reordering or
// inserting new constants in the iota block never corrupts persisted definitions.
func (k NodeKind) MarshalJSON() ([]byte, error) {
	name, ok := nodeKindNames[k]
	if !ok {
		return nil, fmt.Errorf("workflow-definition: unknown NodeKind value %d", int(k))
	}
	return json.Marshal(name)
}

// UnmarshalJSON decodes a NodeKind from its name string (e.g. "startEvent").
// An unrecognised name is rejected with an error rather than silently producing
// a zero-value, so corrupt or out-of-sync stored definitions surface immediately.
func (k *NodeKind) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("workflow-definition: NodeKind must be a JSON string: %w", err)
	}
	v, ok := nodeKindByName[name]
	if !ok {
		return fmt.Errorf("workflow-definition: unknown NodeKind name %q", name)
	}
	*k = v
	return nil
}
