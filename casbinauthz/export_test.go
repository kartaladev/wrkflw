package casbinauthz

import internalcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"

// WithListenReady is a test-only DBOption that signals ch once the DB watcher's
// LISTEN is established. It lets a test synchronise on the actual listen state
// rather than guessing with a sleep, closing the NOTIFY-before-LISTEN race.
func WithListenReady(ch chan struct{}) DBOption {
	return func(c *internalcasbin.DBConfig) { c.ListenReady = ch }
}
