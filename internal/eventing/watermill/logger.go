package watermill

import (
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
)

// NewWatermillLogger returns a watermill.LoggerAdapter that forwards to l.
// Use it to unify watermill's internal logs (e.g. GoChannel) with the app's
// slog output.
func NewWatermillLogger(l *slog.Logger) watermill.LoggerAdapter {
	return &slogLogger{logger: l}
}

type slogLogger struct {
	logger *slog.Logger
}

func (s *slogLogger) Error(msg string, err error, fields watermill.LogFields) {
	s.logger.Error(msg, append(fieldsToArgs(fields), slog.Any("error", err))...)
}

func (s *slogLogger) Info(msg string, fields watermill.LogFields) {
	s.logger.Info(msg, fieldsToArgs(fields)...)
}

func (s *slogLogger) Debug(msg string, fields watermill.LogFields) {
	s.logger.Debug(msg, fieldsToArgs(fields)...)
}

func (s *slogLogger) Trace(msg string, fields watermill.LogFields) {
	s.logger.Debug(msg, fieldsToArgs(fields)...)
}

func (s *slogLogger) With(fields watermill.LogFields) watermill.LoggerAdapter {
	return &slogLogger{logger: s.logger.With(fieldsToArgs(fields)...)}
}

func fieldsToArgs(fields watermill.LogFields) []any {
	args := make([]any, 0, len(fields))
	for k, v := range fields {
		args = append(args, slog.Any(k, v))
	}
	return args
}
