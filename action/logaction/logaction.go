// Package logaction provides a service action that emits a structured slog record
// of selected instance variables and passes the variables through unchanged. It is
// well suited to fire-and-forget paths (reminders, audit points, debugging).
package logaction

import (
	"context"
	"log/slog"

	"github.com/zakyalvan/krtlwrkflw/action"
)

// Option configures a log action.
type Option func(*logAction)

type logAction struct {
	logger *slog.Logger
	level  slog.Level
	msg    string
	keys   []string // nil ⇒ log all variables
}

// WithLogger sets the slog.Logger. Default: slog.Default().
func WithLogger(l *slog.Logger) Option { return func(a *logAction) { a.logger = l } }

// WithLevel sets the log level. Default: slog.LevelInfo.
func WithLevel(lvl slog.Level) Option { return func(a *logAction) { a.level = lvl } }

// WithMessage sets the log message. Default: "workflow action".
func WithMessage(m string) Option { return func(a *logAction) { a.msg = m } }

// WithKeys restricts the logged variables to the named keys. Default: all.
func WithKeys(keys ...string) Option { return func(a *logAction) { a.keys = keys } }

// NewLog returns a pass-through service action that logs the (selected) input
// variables as a single structured record.
func NewLog(opts ...Option) action.ServiceAction {
	a := &logAction{logger: slog.Default(), level: slog.LevelInfo, msg: "workflow action"}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *logAction) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	attrs := make([]slog.Attr, 0, len(in))
	if a.keys == nil {
		for k, v := range in {
			attrs = append(attrs, slog.Any(k, v))
		}
	} else {
		for _, k := range a.keys {
			if v, ok := in[k]; ok {
				attrs = append(attrs, slog.Any(k, v))
			}
		}
	}
	a.logger.LogAttrs(ctx, a.level, a.msg, attrs...)
	return in, nil
}
