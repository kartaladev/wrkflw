package rest

import "github.com/zakyalvan/krtlwrkflw/engine"

// config holds the resolved handler configuration.
type config struct {
	instanceMapper func(engine.InstanceState) any
}

func defaultConfig() config {
	return config{
		instanceMapper: func(st engine.InstanceState) any {
			return NewInstanceView(st)
		},
	}
}

// Option is a functional option for NewHandler.
type Option func(*config)

// WithInstanceMapper overrides the function used to convert an engine.InstanceState
// into the JSON body returned by any endpoint that returns a process instance
// (start, get, signal, claim, complete, reassign). The default is NewInstanceView.
//
// Panics immediately if fn is nil — a nil mapper would only be caught at request
// time, producing a cryptic nil-pointer panic in production.
func WithInstanceMapper(fn func(engine.InstanceState) any) Option {
	if fn == nil {
		panic("rest: WithInstanceMapper: fn must not be nil")
	}
	return func(c *config) {
		c.instanceMapper = fn
	}
}
