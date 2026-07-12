package logaction_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/kartaladev/wrkflw/action/logaction"
)

func TestLog(t *testing.T) {
	tests := map[string]struct {
		opts   []logaction.Option
		in     map[string]any
		assert func(t *testing.T, rec map[string]any, out map[string]any)
	}{
		"logs all variables and passes them through": {
			nil,
			map[string]any{"a": float64(1), "b": "x"},
			func(t *testing.T, rec map[string]any, out map[string]any) {
				if rec["a"] != float64(1) || rec["b"] != "x" {
					t.Fatalf("record missing vars: %v", rec)
				}
				if out["a"] != float64(1) || out["b"] != "x" {
					t.Fatalf("output not pass-through: %v", out)
				}
			},
		},
		"WithKeys limits logged variables": {
			[]logaction.Option{logaction.WithKeys("a")},
			map[string]any{"a": float64(1), "b": "secret"},
			func(t *testing.T, rec map[string]any, _ map[string]any) {
				if _, ok := rec["b"]; ok {
					t.Fatalf("b should not be logged: %v", rec)
				}
				if rec["a"] != float64(1) {
					t.Fatalf("a missing: %v", rec)
				}
			},
		},
		"WithMessage sets the log message": {
			[]logaction.Option{logaction.WithMessage("audit")},
			map[string]any{},
			func(t *testing.T, rec map[string]any, _ map[string]any) {
				if rec["msg"] != "audit" {
					t.Fatalf("msg = %v, want audit", rec["msg"])
				}
			},
		},
		"WithLevel emits at the configured level": {
			[]logaction.Option{logaction.WithLevel(slog.LevelWarn)},
			map[string]any{"x": "y"},
			func(t *testing.T, rec map[string]any, _ map[string]any) {
				if rec["level"] != "WARN" {
					t.Fatalf("level = %v, want WARN", rec["level"])
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			opts := append([]logaction.Option{logaction.WithLogger(logger)}, tc.opts...)
			a := logaction.NewLog(opts...)

			out, err := a.Do(t.Context(), tc.in)
			if err != nil {
				t.Fatalf("Do err = %v", err)
			}
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("log not valid JSON: %v (%q)", err, buf.String())
			}
			tc.assert(t, rec, out)
		})
	}
}
