package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// regWith registers def into a fresh MemDefinitionRegistry under both its bare
// ID and its "ID:Version" key, returning the registry.
func regWith(t *testing.T, def *model.ProcessDefinition) kernel.DefinitionRegistry {
	t.Helper()
	reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		def.ID:         def,
		defRefFor(def): def,
	})
	return reg
}

// TestNewEngineZeroConfig verifies that NewEngine with no options constructs a
// fully-wired engine from coherent in-memory defaults.
func TestNewEngineZeroConfig(t *testing.T) {
	e, err := service.NewEngine()
	require.NoError(t, err)
	require.NotNil(t, e)
}

// TestNewEngineDefaultGraphRoundTrips verifies that the default in-memory graph
// is coherent: the store observed by the driver is the same one the reader loads
// from, so a start→get round-trips.
func TestNewEngineDefaultGraphRoundTrips(t *testing.T) {
	def := linearDef()
	e, err := service.NewEngine(service.WithDefinitions(regWith(t, def)))
	require.NoError(t, err)

	pi, err := e.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: defRefFor(def),
		Vars:   map[string]any{"name": "ada"},
	})
	require.NoError(t, err)

	got, err := e.GetInstance(t.Context(), pi.State().InstanceID)
	require.NoError(t, err)
	assert.Equal(t, pi.State().InstanceID, got.State().InstanceID)
}

// TestNewEngineNilOptionsIgnored verifies that options receiving nil leaves are
// ignored — the coherent in-memory default is kept and NewEngine still succeeds.
func TestNewEngineNilOptionsIgnored(t *testing.T) {
	cases := []struct {
		name string
		opt  service.Option
	}{
		{name: "nil instance store", opt: service.WithInstanceStore(nil)},
		{name: "nil definitions", opt: service.WithDefinitions(nil)},
		{name: "nil lister", opt: service.WithLister(nil)},
		{name: "nil human tasks", opt: service.WithHumanTasks(nil, nil)},
		{name: "nil clock", opt: service.WithClock(nil)},
		{name: "nil process driver", opt: service.WithProcessDriver(nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := service.NewEngine(tc.opt)
			require.NoError(t, err)
			assert.NotNil(t, e)
		})
	}
}
