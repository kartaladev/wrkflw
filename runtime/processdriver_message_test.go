package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// msgStartDef builds a minimal message-start definition: a message-start event
// (on msgName) flowing straight to an end, so a created instance runs to
// completion with no external collaborators.
func msgStartDef(defID, msgName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      defID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start", event.WithMessageCorrelator(msgName, "")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

// msgStartDefSingleton builds a keyless message-start definition marked
// WithMessageStartSingleton, so a keyless delivery creates at most one instance
// ever for msgName (name-only deterministic id).
func msgStartDefSingleton(defID, msgName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      defID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start",
				event.WithMessageCorrelator(msgName, ""),
				event.WithMessageStartSingleton()),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

// manualStartDef builds a definition whose start is a manual (trigger-less)
// event — its MessageName is "". Used to prove an empty-name DeliverMessage
// never matches a manual start.
func manualStartDef(defID string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      defID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

// mustMsgDriver builds a ProcessDriver over store with defs registered in a
// fresh MemDefinitionRegistry.
func mustMsgDriver(t *testing.T, store kernel.InstanceStore, defs ...*model.ProcessDefinition) *ProcessDriver {
	t.Helper()
	reg := kernel.NewMemDefinitionRegistry()
	for _, d := range defs {
		require.NoError(t, reg.Register(d))
	}
	driver, err := NewProcessDriver(WithInstanceStore(store), WithDefinitions(reg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })
	return driver
}

// countInstances returns the number of instances currently held by store.
func countInstances(t *testing.T, store *kernel.MemInstanceStore) int {
	t.Helper()
	page, err := store.List(t.Context(), kernel.InstanceFilter{Limit: 200})
	require.NoError(t, err)
	return len(page.Items)
}

// TestDeliverMessageStartBehavior covers the def-less correlate-then-create
// contract of DeliverMessage on the message-START path (no running waiter).
func TestDeliverMessageStartBehavior(t *testing.T) {
	type deliverCall struct {
		name    string
		key     string
		payload map[string]any
		assert  func(t *testing.T, err error)
	}

	type testCase struct {
		name   string
		defs   []*model.ProcessDefinition
		calls  []deliverCall
		assert func(t *testing.T, store *kernel.MemInstanceStore)
	}

	noErr := func(t *testing.T, err error) { require.NoError(t, err) }

	cases := []testCase{
		{
			name: "starts a new instance when no waiter and a unique message-start matches",
			defs: []*model.ProcessDefinition{msgStartDef("order-created-def", "order.created")},
			calls: []deliverCall{
				{name: "order.created", key: "42", payload: map[string]any{"orderId": "42"}, assert: noErr},
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore) {
				id := messageStartInstanceID("order.created", "42")
				st, _, err := store.Load(t.Context(), id)
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, st.Status)
				assert.Equal(t, 1, countInstances(t, store))
			},
		},
		{
			name: "no matching message-start and no waiter is a clean no-op",
			defs: nil,
			calls: []deliverCall{
				{name: "unheard.of", key: "1", assert: noErr},
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore) {
				assert.Equal(t, 0, countInstances(t, store))
			},
		},
		{
			name: "re-delivering the same key after completion is a no-op (single-use per lifetime)",
			defs: []*model.ProcessDefinition{msgStartDef("order-created-def", "order.created")},
			calls: []deliverCall{
				{name: "order.created", key: "77", assert: noErr},
				{name: "order.created", key: "77", assert: noErr},
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore) {
				assert.Equal(t, 1, countInstances(t, store))
			},
		},
		{
			name: "keyless default creates a fresh instance per message (BPMN fan-in)",
			defs: []*model.ProcessDefinition{msgStartDef("keyless-fanin", "order.created")},
			calls: []deliverCall{
				{name: "order.created", key: "", assert: noErr},
				{name: "order.created", key: "", assert: noErr},
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore) {
				assert.Equal(t, 2, countInstances(t, store), "each keyless message must mint a fresh instance")
			},
		},
		{
			name: "keyless singleton creates at most one instance ever",
			defs: []*model.ProcessDefinition{msgStartDefSingleton("keyless-singleton", "order.created")},
			calls: []deliverCall{
				{name: "order.created", key: "", assert: noErr},
				{name: "order.created", key: "", assert: noErr},
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore) {
				assert.Equal(t, 1, countInstances(t, store), "singleton keyless message-start must dedup to one instance")
				id := messageStartInstanceID("order.created", "")
				_, _, err := store.Load(t.Context(), id)
				require.NoError(t, err, "singleton must use the name-only deterministic id")
			},
		},
		{
			name: "keyed message-start dedups by correlation key",
			defs: []*model.ProcessDefinition{msgStartDef("keyed-dedup", "order.created")},
			calls: []deliverCall{
				{name: "order.created", key: "k1", assert: noErr},
				{name: "order.created", key: "k1", assert: noErr},
				{name: "order.created", key: "k2", assert: noErr},
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore) {
				assert.Equal(t, 2, countInstances(t, store), "one instance per distinct key; same key dedups")
			},
		},
		{
			name: "empty message name is a clean no-op and never matches a manual start",
			defs: []*model.ProcessDefinition{manualStartDef("manual-start-def")},
			calls: []deliverCall{
				{name: "", key: "k", assert: noErr},
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore) {
				assert.Equal(t, 0, countInstances(t, store), "empty message name must not spawn any instance")
			},
		},
		{
			name: "ambiguous message-start across two defs returns ErrAmbiguousMessageStart",
			defs: []*model.ProcessDefinition{
				msgStartDef("def-a", "order.created"),
				msgStartDef("def-b", "order.created"),
			},
			calls: []deliverCall{
				{name: "order.created", key: "9", assert: func(t *testing.T, err error) {
					require.ErrorIs(t, err, ErrAmbiguousMessageStart)
				}},
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore) {
				assert.Equal(t, 0, countInstances(t, store))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := kernel.NewMemInstanceStore()
			require.NoError(t, err)
			driver := mustMsgDriver(t, store, tc.defs...)

			for _, call := range tc.calls {
				derr := driver.DeliverMessage(t.Context(), call.name, call.key, call.payload)
				call.assert(t, derr)
			}
			tc.assert(t, store)
		})
	}
}

// msgCatchDef builds start → message-catch(msgName) → end, so an instance parks
// on the message until it is delivered.
func msgCatchDef(defID, msgName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      defID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait", event.WithMessageCorrelator(msgName, "")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
	}
}

// lookupOnlyRegistry is a DefinitionRegistry that does NOT implement
// kernel.DefinitionLister, so it exercises the listDefinitions non-lister branch
// (message-start disabled; only correlate-by-lookup works).
type lookupOnlyRegistry struct {
	defs map[model.Qualifier]*model.ProcessDefinition
}

func (r lookupOnlyRegistry) Lookup(_ context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	if d, ok := r.defs[q]; ok {
		return d, nil
	}
	return nil, kernel.ErrDefinitionNotFound
}

// TestCreateAtNodeGeneratesIDWhenInstanceIDEmpty covers createAtNode's id-minting
// branch (used by the signal/timer starts in later tasks): an empty instanceID
// mints a fresh id via the driver's generator rather than using a caller id.
func TestCreateAtNodeGeneratesIDWhenInstanceIDEmpty(t *testing.T) {
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driver := mustMsgDriver(t, store, msgStartDef("order-created-def", "order.created"))

	st, err := driver.createAtNode(t.Context(), msgStartDef("order-created-def", "order.created"), "start", "", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, st.InstanceID, "an empty instanceID must be replaced with a generated one")
	assert.NotEqual(t, messageStartInstanceID("order.created", ""), st.InstanceID)
	assert.Equal(t, engine.StatusCompleted, st.Status)
}

// TestDeliverMessageCorrelateUnresolvedDefinitionErrors verifies the correlate
// path surfaces an error when the parked instance's definition cannot be resolved
// from the driver's registry (the caller no longer supplies it).
func TestDeliverMessageCorrelateUnresolvedDefinitionErrors(t *testing.T) {
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	// Empty registry: the driver can track the waiter but cannot resolve its def.
	emptyReg := kernel.NewMemDefinitionRegistry()
	driver, err := NewProcessDriver(WithInstanceStore(store), WithDefinitions(emptyReg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

	def := msgCatchDef("catch-def", "order.shipped")
	parked, err := driver.Drive(t.Context(), def, "inst-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)

	derr := driver.DeliverMessage(t.Context(), "order.shipped", "", map[string]any{"ok": true})
	require.ErrorIs(t, derr, kernel.ErrDefinitionNotFound)
}

// TestDeliverMessageWithNonListerRegistryIsNoop verifies that when the registry
// cannot enumerate definitions (no DefinitionLister), message-start is disabled:
// an unmatched message is a clean no-op rather than an error.
func TestDeliverMessageWithNonListerRegistryIsNoop(t *testing.T) {
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driver, err := NewProcessDriver(
		WithInstanceStore(store),
		WithDefinitions(lookupOnlyRegistry{defs: map[model.Qualifier]*model.ProcessDefinition{}}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

	err = driver.DeliverMessage(t.Context(), "order.created", "42", nil)
	require.NoError(t, err)
	assert.Equal(t, 0, countInstances(t, store))
}

// TestDeliverMessageConcurrentSameKeyCreatesOne verifies that many concurrent
// deliveries of the same (name, key) create EXACTLY ONE instance — the
// deterministic id + Store.Create's ErrInstanceExists is the authoritative
// dedup. Divergent setup (goroutine fan-out under -race) keeps it out of the
// table above.
func TestDeliverMessageConcurrentSameKeyCreatesOne(t *testing.T) {
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driver := mustMsgDriver(t, store, msgStartDef("order-created-def", "order.created"))

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := range n {
		go func() {
			defer wg.Done()
			errs[i] = driver.DeliverMessage(t.Context(), "order.created", "42", map[string]any{"orderId": "42"})
		}()
	}
	wg.Wait()

	for i, e := range errs {
		require.NoErrorf(t, e, "delivery %d", i)
	}
	assert.Equal(t, 1, countInstances(t, store))
	id := messageStartInstanceID("order.created", "42")
	st, _, err := store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)
}
