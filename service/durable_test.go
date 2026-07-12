package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
)

// fakeProvider is a test DurableProvider whose leaves are supplied directly so
// the precedence and nil-leaf-validation paths can be exercised without a real
// database.
type fakeProvider struct {
	store  kernel.InstanceStore
	reg    kernel.DefinitionRegistry
	lister kernel.InstanceLister
	tasks  humantask.TaskStore
	timers kernel.TimerStore
	links  kernel.CallLinkStore
}

func (f fakeProvider) InstanceStore() kernel.InstanceStore    { return f.store }
func (f fakeProvider) Definitions() kernel.DefinitionRegistry { return f.reg }
func (f fakeProvider) Lister() kernel.InstanceLister          { return f.lister }
func (f fakeProvider) TaskStore() humantask.TaskStore         { return f.tasks }
func (f fakeProvider) TimerStore() kernel.TimerStore          { return f.timers }
func (f fakeProvider) CallLinkStore() kernel.CallLinkStore    { return f.links }

// compile-time guard: the fake satisfies the public interface.
var _ service.DurableProvider = fakeProvider{}

func memStore(t *testing.T) *kernel.MemInstanceStore {
	t.Helper()
	ms, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	return ms
}

func TestWithDurableStore(t *testing.T) {
	ms := memStore(t)
	p := fakeProvider{
		store:  ms,
		reg:    kernel.NewMemDefinitionRegistry(),
		lister: ms,
		tasks:  humantask.NewMemTaskStore(),
	}
	e, err := service.NewEngine(service.WithDurableStore(p))
	require.NoError(t, err)
	require.NotNil(t, e)
}

func TestWithDurableStoreNilLeafFails(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeProvider)
	}{
		{"nil store", func(p *fakeProvider) { p.store = nil }},
		{"nil lister", func(p *fakeProvider) { p.lister = nil }},
		{"nil task store", func(p *fakeProvider) { p.tasks = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := memStore(t)
			p := fakeProvider{
				store:  ms,
				reg:    kernel.NewMemDefinitionRegistry(),
				lister: ms,
				tasks:  humantask.NewMemTaskStore(),
			}
			tt.mutate(&p)
			_, err := service.NewEngine(service.WithDurableStore(p))
			require.ErrorIs(t, err, service.ErrNilDependency)
		})
	}
}

func TestWithDurableStorePrecedenceLaterOverrideWins(t *testing.T) {
	ms1 := memStore(t)
	ms2 := memStore(t)
	p := fakeProvider{
		store:  ms1,
		reg:    kernel.NewMemDefinitionRegistry(),
		lister: ms1,
		tasks:  humantask.NewMemTaskStore(),
	}
	// A later WithInstanceStore override must win over the provider's store.
	e, err := service.NewEngine(
		service.WithDurableStore(p),
		service.WithInstanceStore(ms2),
	)
	require.NoError(t, err)
	require.NotNil(t, e)
}

func TestWithDurableStoreNilProviderIgnored(t *testing.T) {
	// A nil provider is ignored; the engine falls back to in-memory defaults.
	e, err := service.NewEngine(service.WithDurableStore(nil))
	require.NoError(t, err)
	require.NotNil(t, e)
}
