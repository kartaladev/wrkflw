package processtest

import (
	"context"
	"sync"

	"github.com/kartaladev/wrkflw/action"
)

// Invocation is one recorded call to an action resolved through a [SpyCatalog].
type Invocation struct {
	// Name is the action name that was resolved.
	Name string
	// In is the input map passed to Do.
	In map[string]any
	// Out is the output map returned by Do (nil on error).
	Out map[string]any
	// Err is the error returned by Do (nil on success).
	Err error
}

// SpyCatalog wraps an inner [action.Catalog] and records every action invocation
// made through actions it resolves. Use it to assert which actions ran, with what
// inputs, and what they returned. It implements [action.Catalog], so it drops into
// [NewProcessDriver] (or a [Harness]) in place of the real catalog.
//
// Resolve returns the inner action wrapped in a recorder; a miss on the inner
// catalog is passed through unchanged (nil, false). SpyCatalog is safe for
// concurrent use.
type SpyCatalog struct {
	inner action.Catalog

	mu          sync.Mutex
	invocations []Invocation
}

// Compile-time assertion.
var _ action.Catalog = (*SpyCatalog)(nil)

// NewSpyCatalog wraps inner. A nil inner behaves as an empty catalog (every
// Resolve misses).
func NewSpyCatalog(inner action.Catalog) *SpyCatalog {
	if inner == nil {
		inner = action.NewCatalog(nil)
	}
	return &SpyCatalog{inner: inner}
}

// Resolve looks name up in the inner catalog. On a hit it returns the resolved
// action wrapped so that each Do call is recorded; on a miss it returns
// (nil, false).
func (c *SpyCatalog) Resolve(name string) (action.Action, bool) {
	inner, ok := c.inner.Resolve(name)
	if !ok {
		return nil, false
	}
	return &recordingAction{spy: c, name: name, inner: inner}, true
}

// Invocations returns a copy of all recorded invocations in call order.
func (c *SpyCatalog) Invocations() []Invocation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Invocation(nil), c.invocations...)
}

// InvocationsOf returns the recorded invocations for a single action name, in
// call order.
func (c *SpyCatalog) InvocationsOf(name string) []Invocation {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Invocation
	for _, inv := range c.invocations {
		if inv.Name == name {
			out = append(out, inv)
		}
	}
	return out
}

// Count returns how many times the named action was invoked.
func (c *SpyCatalog) Count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, inv := range c.invocations {
		if inv.Name == name {
			n++
		}
	}
	return n
}

func (c *SpyCatalog) record(inv Invocation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invocations = append(c.invocations, inv)
}

// recordingAction wraps an [action.Action] and records each Do call on the spy.
type recordingAction struct {
	spy   *SpyCatalog
	name  string
	inner action.Action
}

func (a *recordingAction) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	out, err := a.inner.Do(ctx, in)
	a.spy.record(Invocation{Name: a.name, In: in, Out: out, Err: err})
	return out, err
}
