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
// into the JSON body returned by GET /instances/{id}. The default is NewInstanceView.
func WithInstanceMapper(fn func(engine.InstanceState) any) Option {
	return func(c *config) {
		c.instanceMapper = fn
	}
}
