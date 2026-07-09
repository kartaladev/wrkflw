package runtime_test

// processdriver_validation_test.go proves the pre-Step validation hook wired
// into deliverLoop (ProcessDriver.validateInput, fed by engine.TargetNode +
// model.ValidationStrategyFor + runtime/validation.Gate): a rejection must
// surface BEFORE any state is committed, for both the start boundary
// (StartInstance) and — the regression this redesign fixes — a message
// boundary on a node NESTED inside a sub-process (the old flat def.Node
// lookup silently skipped nested nodes).

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	vexpr "github.com/zakyalvan/krtlwrkflw/definition/model/validate/expr"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/validation"
)

// startValidationDef returns start[validated: amount > 0] → svc → end. The
// service task resolves "noop" against the shared noopCatalog() helper
// (defined in expression_timeout_test.go).
func startValidationDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "start-validation", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start", event.WithInputValidation(vexpr.New("amount > 0"))),
			activity.NewServiceTask("svc", activity.WithTaskAction("noop")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

// TestValidateInputStart verifies the hook rejects invalid start vars BEFORE
// any instance is created (store.Load must report kernel.ErrInstanceNotFound),
// and lets valid vars proceed.
func TestValidateInputStart(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		instanceID string
		vars       map[string]any
		assert     func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, st engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name:       "reject: amount <= 0 rejects before any commit",
			instanceID: "start-reject-1",
			vars:       map[string]any{"amount": -1},
			assert: func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, _ engine.InstanceState, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, validation.ErrInvalidInput)
				_, _, loadErr := store.Load(t.Context(), instanceID)
				assert.ErrorIs(t, loadErr, kernel.ErrInstanceNotFound, "rejected start trigger must not create an instance")
			},
		},
		{
			name:       "accept: amount > 0 proceeds and completes",
			instanceID: "start-accept-1",
			vars:       map[string]any{"amount": 5},
			assert: func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, st engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, st.Status)
				_, _, loadErr := store.Load(t.Context(), instanceID)
				assert.NoError(t, loadErr, "accepted start trigger must create the instance")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			store := runtimetest.MustMemStore(t)
			r := runtimetest.MustRunner(t, noopCatalog(), store, runtime.WithClock(fc))
			def := startValidationDef()

			st, err := r.Drive(t.Context(), def, tc.instanceID, tc.vars)
			tc.assert(t, store, tc.instanceID, st, err)
		})
	}
}

// nestedMessageValidationDef returns start → SubProcess{ inner-start →
// ReceiveTask("wait", validated: ok == true) → inner-end } → end. This is the
// regression scenario: with the old flat def.Node lookup, "wait" (nested
// inside the sub-process) was invisible to validation and silently skipped.
func nestedMessageValidationDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "nested-wait", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewReceiveTask("wait", "proceed", activity.WithPayloadValidation(vexpr.New("ok == true"))),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "wait"},
			{ID: "if2", Source: "wait", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "nested-msg-validation", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

// TestValidateInputNestedMessage verifies the hook resolves the message
// target node through engine.TargetNode's scope-aware lookup even when the
// node lives inside a sub-process: a rejected payload leaves the instance
// state byte-for-byte unchanged (no advance), and an accepted payload
// advances past the nested ReceiveTask to completion.
func TestValidateInputNestedMessage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		payload map[string]any
		assert  func(t *testing.T, before, after engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name:    "reject: ok=false rejects before any advance",
			payload: map[string]any{"ok": false},
			assert: func(t *testing.T, before, after engine.InstanceState, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, validation.ErrInvalidInput)
				assert.Equal(t, before, after, "instance state must be unchanged when the nested payload is rejected")
			},
		},
		{
			name:    "accept: ok=true advances past the nested ReceiveTask",
			payload: map[string]any{"ok": true},
			assert: func(t *testing.T, _, after engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, after.Status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			store := runtimetest.MustMemStore(t)
			r := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc))
			def := nestedMessageValidationDef()

			const instanceID = "nested-msg-1"
			parked, err := r.Drive(t.Context(), def, instanceID, nil)
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parked.Status, "must park at the nested ReceiveTask")

			before, _, err := store.Load(t.Context(), instanceID)
			require.NoError(t, err)

			derr := r.DeliverMessage(t.Context(), def, "proceed", "", tc.payload)

			after, _, loadErr := store.Load(t.Context(), instanceID)
			require.NoError(t, loadErr)

			tc.assert(t, before, after, derr)
		})
	}
}

// sameIDDifferentSchemaDef returns a definition with a ReceiveTask "x" at the top
// level (payload schema: a == true) AND a ReceiveTask ALSO named "x" nested in a
// sub-process (payload schema: b == true). Node ids are unique per scope but
// collide across scopes — exactly the shape that the old node-location keying
// (topDef:version:"x", identical for both) confused: the second node reused the
// first node's compiled validator, validating against the WRONG schema.
//
//	start → x(top, msg "top-msg", a == true) → sub → end
//	sub:   inner-start → x(msg "inner-msg", b == true) → inner-end
func sameIDDifferentSchemaDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "collide-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewReceiveTask("x", "inner-msg", activity.WithPayloadValidation(vexpr.New("b == true"))),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "x"},
			{ID: "if2", Source: "x", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "collide-scopes", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("x", "top-msg", activity.WithPayloadValidation(vexpr.New("a == true"))),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "x"},
			{ID: "f2", Source: "x", Target: "sub"},
			{ID: "f3", Source: "sub", Target: "end"},
		},
	}
}

// TestValidateInput_NoDescriptorCollisionAcrossScopes proves the Gate keys its
// compiled-validator cache by strategy DESCRIPTOR (kind + schema), not by node
// location. The top "x" (schema a == true) is exercised first, building its
// validator; the nested "x" (schema b == true) must then be validated against
// ITS OWN schema. We deliver {b: true} (no "a") to the nested node: under the old
// node-location keying it would have reused the top node's a == true validator
// and rejected on the missing "a"; under descriptor keying it validates b == true
// and the instance runs to completion.
func TestValidateInput_NoDescriptorCollisionAcrossScopes(t *testing.T) {
	t.Parallel()

	fc := clockwork.NewFakeClock()
	store := runtimetest.MustMemStore(t)
	r := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc))
	def := sameIDDifferentSchemaDef()

	const instanceID = "collide-1"
	parked, err := r.Drive(t.Context(), def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "must park at top-level x")

	// Satisfy the top-level schema (a == true) to advance into the sub-process.
	require.NoError(t, r.DeliverMessage(t.Context(), def, "top-msg", "", map[string]any{"a": true}))

	// Deliver a payload valid ONLY under the nested schema (b == true). A collision
	// would validate this against a == true and reject on the missing key.
	derr := r.DeliverMessage(t.Context(), def, "inner-msg", "", map[string]any{"b": true})
	require.NoError(t, derr, "nested node must validate against its own schema, not the top node's")

	after, _, loadErr := store.Load(t.Context(), instanceID)
	require.NoError(t, loadErr)
	assert.Equal(t, engine.StatusCompleted, after.Status)
}

// TestValidateInput_DurableReloadUnregisteredKindFailsClosed proves the
// fail-closed durable-reload contract AT THE EXECUTOR (not merely up to
// NewValidator): a definition whose start node carries a validation kind that is
// NOT registered in validate.DefaultRegistry survives json.Unmarshal with its slot
// left PENDING, and driving an input trigger through the ProcessDriver is rejected
// before any state is committed. The rejection is a reconstruction error
// (model.ErrValidationNotReconstructed — a server-config fault mapped to HTTP 500),
// which is intentionally DISTINCT from validation.ErrInvalidInput (bad input →
// 400): a missing adapter is an operator misconfiguration, not caller error.
func TestValidateInput_DurableReloadUnregisteredKindFailsClosed(t *testing.T) {
	t.Parallel()

	// Author a valid expr-validated def, marshal it, then rewrite the validation
	// descriptor's kind to an unregistered "bogus" so durable reload leaves the
	// slot pending. Node kinds serialize as "startEvent"/"endEvent", so
	// "kind":"expr" uniquely identifies the validation descriptor.
	authored := &model.ProcessDefinition{
		ID: "durable-bogus", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start", event.WithInputValidation(vexpr.New("amount > 0"))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
	data, err := json.Marshal(authored)
	require.NoError(t, err)
	data = bytes.Replace(data, []byte(`"kind":"expr"`), []byte(`"kind":"bogus"`), 1)

	var reloaded model.ProcessDefinition
	require.NoError(t, json.Unmarshal(data, &reloaded), "unregistered kind must not break unmarshal")

	fc := clockwork.NewFakeClock()
	store := runtimetest.MustMemStore(t)
	r := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc))

	const instanceID = "durable-bogus-1"
	_, driveErr := r.Drive(t.Context(), &reloaded, instanceID, map[string]any{"amount": 5})

	require.Error(t, driveErr)
	assert.ErrorIs(t, driveErr, model.ErrValidationNotReconstructed,
		"a pending strategy fails closed with a reconstruction error at the executor")
	assert.NotErrorIs(t, driveErr, validation.ErrInvalidInput,
		"reconstruction failure is a server-config fault, not bad input")

	_, _, loadErr := store.Load(t.Context(), instanceID)
	assert.ErrorIs(t, loadErr, kernel.ErrInstanceNotFound, "rejected trigger must not commit any state")
}

// catchMessageValidationDef returns start → IntermediateCatch(message "confirm",
// validated: ok == true) → end. Top-level message catch is enough here; the
// nested-scope path is already covered for ReceiveTask.
func catchMessageValidationDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "catch-msg-validation", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("confirm-catch",
				event.WithMessageCorrelator("confirm", ""),
				event.WithPayloadValidation(vexpr.New("ok == true"))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "confirm-catch"},
			{ID: "f2", Source: "confirm-catch", Target: "end"},
		},
	}
}

// TestValidateInputIntermediateCatchMessage is the driver E2E for the one
// validation-bearing kind that previously lacked a driver-level rejection test:
// an IntermediateCatchEvent message catch. A rejected payload leaves the parked
// instance state unchanged (no advance); an accepted payload advances to
// completion.
func TestValidateInputIntermediateCatchMessage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		payload map[string]any
		assert  func(t *testing.T, before, after engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name:    "reject: ok=false rejects before any advance",
			payload: map[string]any{"ok": false},
			assert: func(t *testing.T, before, after engine.InstanceState, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, validation.ErrInvalidInput)
				assert.Equal(t, before, after, "instance state must be unchanged when the catch payload is rejected")
			},
		},
		{
			name:    "accept: ok=true advances past the catch to completion",
			payload: map[string]any{"ok": true},
			assert: func(t *testing.T, _, after engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, after.Status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			store := runtimetest.MustMemStore(t)
			r := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc))
			def := catchMessageValidationDef()

			const instanceID = "catch-msg-1"
			parked, err := r.Drive(t.Context(), def, instanceID, nil)
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parked.Status, "must park at the message catch")

			before, _, err := store.Load(t.Context(), instanceID)
			require.NoError(t, err)

			derr := r.DeliverMessage(t.Context(), def, "confirm", "", tc.payload)

			after, _, loadErr := store.Load(t.Context(), instanceID)
			require.NoError(t, loadErr)

			tc.assert(t, before, after, derr)
		})
	}
}
