package logaction_test

import (
	"context"
	"log/slog"
	"os"

	"github.com/kartaladev/wrkflw/action/logaction"
)

func ExampleNewLog() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
	a := logaction.NewLog(logaction.WithLogger(logger), logaction.WithMessage("audit"), logaction.WithKeys("user"))
	_, _ = a.Do(context.Background(), map[string]any{"user": "ada", "secret": "x"})
	// Output: level=INFO msg=audit user=ada
}
