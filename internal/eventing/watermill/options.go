// Package watermill adapts a watermill message.Publisher to the
// runtime.Publisher port. It is the only package besides eventing/ that imports
// watermill; engine/model/runtime never do.
package watermill

import "log/slog"

// Option configures a Publisher.
type Option func(*config)

type config struct {
	logger *slog.Logger
}

// WithLogger sets the structured logger (default slog.Default()). A nil logger
// is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}
