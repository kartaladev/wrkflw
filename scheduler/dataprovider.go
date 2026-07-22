package scheduler

import "context"

// staticDataProvider is the unexported [DataProvider] built by
// [NewStaticDataProvider] and [NewEmptyDataProvider]. It performs no I/O and
// defensively copies its data both at construction and on every Get, so
// neither the caller's input map nor a previously returned map can alias (and
// so mutate) the provider's own state.
type staticDataProvider struct {
	data map[string]any
}

var _ DataProvider = (*staticDataProvider)(nil)

// cloneMap returns a shallow copy of m, guaranteed non-nil even when m is
// nil or empty.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (p *staticDataProvider) Get(_ context.Context) (map[string]any, error) {
	return cloneMap(p.data), nil
}

func (p *staticDataProvider) Static() bool { return true }

// NewStaticDataProvider builds a [DataProvider] that always returns a copy
// of m. m is copied at construction time, so later mutations of the caller's
// map do not affect the provider; each [DataProvider.Get] call also returns
// a fresh copy, so mutating a returned map does not affect subsequent calls.
func NewStaticDataProvider(m map[string]any) DataProvider {
	return &staticDataProvider{data: cloneMap(m)}
}

// NewEmptyDataProvider builds a [DataProvider] whose [DataProvider.Get]
// always returns an empty, non-nil map.
func NewEmptyDataProvider() DataProvider {
	return &staticDataProvider{data: map[string]any{}}
}
