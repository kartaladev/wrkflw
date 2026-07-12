# Built-in Service Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship four ready-to-use service actions (`httpcall`, `email`, `transform`, `logaction`) plus a retryable-error contract, extending the engine's public library surface without new runtime dependencies.

**Architecture:** Each action is a configured constructor returning the existing `action.ServiceAction`, living in its own public `action/<name>` subpackage. The one core change is a new `action.Retryabler`/`NonRetryable` contract that `runtime/runner.go` honours (default unchanged — plain errors still retry). Engine and model stay zero-diff.

**Tech Stack:** Go 1.25.7, stdlib (`net/http`, `net/smtp`, `text/template`, `log/slog`, `net/http/httptest`), `github.com/expr-lang/expr` (existing dep), testify, testcontainers (mailpit, integration test only).

## Global Constraints

- Module path: `github.com/kartaladev/wrkflw`. Go 1.25.7.
- No new third-party **runtime** dependency. Only stdlib + `expr-lang/expr` (already present). testcontainers/mailpit is test-only.
- Error sentinel/message prefix: `"workflow-<pkg>: ..."` (e.g. `"workflow-action: ..."`, `"workflow-httpcall: ..."`).
- Black-box tests: package `<pkg>_test`. Table tests use the project `table-test` `assert`-closure form; use `t.Context()` not `context.Background()`.
- Each consumer-facing action package ships a testable `Example`.
- Engine (`engine/`) and model (`model/`) packages stay zero-diff. Only `action/`, new `action/*` subpackages, `runtime/runner.go`, and `docs/` change.
- TDD strict: write the failing test, run it, observe red, then implement. Each task ends green and committed.

---

### Task 1: Retryable-error contract (`action` package)

**Files:**
- Create: `action/retry.go`
- Test: `action/retry_test.go`

**Interfaces:**
- Consumes: nothing (foundation task).
- Produces:
  - `action.Retryabler interface { error; Retryable() bool }`
  - `action.NonRetryable(err error) error` — wraps err so `Retryable()==false`; unwraps to err.
  - `action.IsRetryable(err error) bool` — true for plain errors and nil; honours `Retryabler` found via `errors.As`.

- [ ] **Step 1: Write the failing test**

Create `action/retry_test.go`:

```go
package action_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/action"
)

func TestIsRetryable(t *testing.T) {
	tests := map[string]struct {
		err    error
		assert func(t *testing.T, retryable bool)
	}{
		"nil is retryable-default (no error to inspect)": {
			nil,
			func(t *testing.T, retryable bool) {
				if !retryable {
					t.Fatalf("IsRetryable(nil) = false, want true")
				}
			},
		},
		"plain error is retryable": {
			errors.New("boom"),
			func(t *testing.T, retryable bool) {
				if !retryable {
					t.Fatalf("plain error: IsRetryable = false, want true")
				}
			},
		},
		"NonRetryable marks not retryable": {
			action.NonRetryable(errors.New("4xx")),
			func(t *testing.T, retryable bool) {
				if retryable {
					t.Fatalf("NonRetryable: IsRetryable = true, want false")
				}
			},
		},
		"NonRetryable wrapped deeper still detected": {
			errors.Join(errors.New("ctx"), action.NonRetryable(errors.New("4xx"))),
			func(t *testing.T, retryable bool) {
				if retryable {
					t.Fatalf("wrapped NonRetryable: IsRetryable = true, want false")
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, action.IsRetryable(tc.err))
		})
	}
}

func TestNonRetryableUnwraps(t *testing.T) {
	sentinel := errors.New("original")
	wrapped := action.NonRetryable(sentinel)
	if !errors.Is(wrapped, sentinel) {
		t.Fatalf("errors.Is(NonRetryable(x), x) = false, want true")
	}
	if action.NonRetryable(nil) != nil {
		t.Fatalf("NonRetryable(nil) != nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action/...`
Expected: FAIL — `undefined: action.NonRetryable`, `undefined: action.IsRetryable`.

- [ ] **Step 3: Write minimal implementation**

Create `action/retry.go`:

```go
package action

import "errors"

// Retryabler lets an action error state whether the runtime should retry it.
// An action returns an error implementing this interface (e.g. via NonRetryable)
// to override the runtime's retry-by-default policy.
type Retryabler interface {
	error
	Retryable() bool
}

// NonRetryable wraps err so the runtime will not retry the failed action. The
// returned error unwraps to err, so errors.Is/As see through it. NonRetryable(nil)
// returns nil.
func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryable{err}
}

type nonRetryable struct{ err error }

func (n nonRetryable) Error() string   { return n.err.Error() }
func (n nonRetryable) Unwrap() error   { return n.err }
func (n nonRetryable) Retryable() bool { return false }

// IsRetryable reports whether the runtime should retry a failed action's error.
// A nil error and any plain error are retryable (the historical default); an
// error implementing Retryabler anywhere in its chain overrides that.
func IsRetryable(err error) bool {
	if err == nil {
		return true
	}
	var r Retryabler
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add action/retry.go action/retry_test.go
git commit -m "feat(action): add Retryabler/NonRetryable retry contract"
```

---

### Task 2: Runtime honours the retry contract

**Files:**
- Modify: `runtime/runner.go:785`
- Test: `runtime/runner_retry_test.go` (create)

**Interfaces:**
- Consumes: `action.IsRetryable` (Task 1), `engine.InvokeAction`, `engine.ActionFailed`.
- Produces: nothing new exported; behavioral change only.

- [ ] **Step 1: Write the failing test**

Create `runtime/runner_retry_test.go`. Mirror the existing runner test setup for constructing a `Runner` and performing an `InvokeAction` — read `runtime/runner.go` and the nearest existing `*_test.go` in `runtime/` for the exact constructor/helper names before writing, and reuse them. The assertion is the new behavior:

```go
package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/action"
)

// TestActionFailedHonoursRetryContract asserts that when a service action returns
// an action.NonRetryable error, the runtime emits ActionFailed with Retryable=false,
// and that a plain error stays Retryable=true (the historical default).
func TestActionFailedHonoursRetryContract(t *testing.T) {
	tests := map[string]struct {
		actErr       error
		wantRetryable bool
	}{
		"plain error stays retryable":      {errors.New("boom"), true},
		"NonRetryable becomes non-retryable": {action.NonRetryable(errors.New("4xx")), false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			act := action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
				return nil, tc.actErr
			})
			// Build a Runner with a global catalog {"a": act}, perform an
			// InvokeAction{Name:"a", CommandID:"c1"}, capture the returned trigger,
			// assert it is engine.ActionFailed with .Retryable == tc.wantRetryable.
			// (Use the package's existing test harness helpers for Runner construction
			// and command execution — see neighbouring runtime tests.)
			_ = act
			t.Skip("replace with real Runner harness call asserting ActionFailed.Retryable")
		})
	}
}
```

Note for implementer: replace the `t.Skip` with the real harness call. If `runtime` has no helper to perform a single `InvokeAction` and read the trigger, use the lowest-level public entry the other runtime tests use. The test MUST fail before Step 3 (it asserts `Retryable==false` for `NonRetryable`, which the unmodified runner never produces).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run TestActionFailedHonoursRetryContract -v`
Expected: FAIL — the unmodified runner returns `Retryable=true` for the `NonRetryable` case (once the `t.Skip` is replaced with the real assertion).

- [ ] **Step 3: Write minimal implementation**

In `runtime/runner.go`, change the failure return (currently line 785):

```go
// before:
return engine.NewActionFailedJittered(r.clk.Now(), cmd.CommandID, err.Error(), true, r.jitter.Fraction()), nil
// after:
return engine.NewActionFailedJittered(r.clk.Now(), cmd.CommandID, err.Error(), action.IsRetryable(err), r.jitter.Fraction()), nil
```

`action` is already imported in `runner.go`. No other change.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./runtime/...`
Expected: PASS (new test + all existing runtime tests — the default is preserved for plain errors).

- [ ] **Step 5: Commit**

```bash
git add runtime/runner.go runtime/runner_retry_test.go
git commit -m "feat(runtime): honour action retry contract (default unchanged)"
```

---

### Task 3: `action/transform` — variable transform action

**Files:**
- Create: `action/transform/transform.go`
- Test: `action/transform/transform_test.go`, `action/transform/example_test.go`

**Interfaces:**
- Consumes: `action.ServiceAction`, `github.com/expr-lang/expr`.
- Produces:
  - `transform.NewTransform(opts ...transform.Option) (action.ServiceAction, error)`
  - `transform.Set(outKey, exprStr string) transform.Option`

- [ ] **Step 1: Write the failing test**

Create `action/transform/transform_test.go`:

```go
package transform_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/action/transform"
)

func TestTransform(t *testing.T) {
	tests := map[string]struct {
		opts   []transform.Option
		in     map[string]any
		assert func(t *testing.T, out map[string]any, err error)
	}{
		"computes a derived field": {
			[]transform.Option{transform.Set("total", "price * qty")},
			map[string]any{"price": 10, "qty": 3},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if out["total"] != 30 {
					t.Fatalf("total = %v, want 30", out["total"])
				}
			},
		},
		"computes a boolean flag": {
			[]transform.Option{transform.Set("vip", "amount > 1000")},
			map[string]any{"amount": 1500},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if out["vip"] != true {
					t.Fatalf("vip = %v, want true", out["vip"])
				}
			},
		},
		"runtime eval error surfaces": {
			[]transform.Option{transform.Set("x", "missing + 1")},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err == nil {
					t.Fatalf("expected eval error, got nil")
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a, err := transform.NewTransform(tc.opts...)
			if err != nil {
				t.Fatalf("NewTransform err = %v", err)
			}
			out, err := a.Do(t.Context(), tc.in)
			tc.assert(t, out, err)
		})
	}
}

func TestNewTransformCompileError(t *testing.T) {
	if _, err := transform.NewTransform(transform.Set("x", "price *")); err == nil {
		t.Fatalf("expected compile error for malformed expression, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action/transform/...`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write minimal implementation**

Create `action/transform/transform.go`:

```go
// Package transform provides a pure service action that computes output
// variables from expr-lang expressions evaluated against the instance variables.
package transform

import (
	"context"
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/kartaladev/wrkflw/action"
)

// Option configures a transform action.
type Option func(*config)

type binding struct {
	out  string
	prog *vm.Program
	src  string
}

type config struct {
	sets []setSpec
}

type setSpec struct {
	out string
	src string
}

// Set maps an output variable key to an expr expression evaluated against the
// input variables. Repeatable; later Sets with the same key overwrite earlier.
func Set(outKey, exprStr string) Option {
	return func(c *config) { c.sets = append(c.sets, setSpec{outKey, exprStr}) }
}

type transform struct {
	bindings []binding
}

// NewTransform compiles each Set expression and returns a service action that, on
// Do, evaluates them against the input variables and returns the results. A
// malformed expression fails here (at wiring time), not mid-process.
func NewTransform(opts ...Option) (action.ServiceAction, error) {
	var c config
	for _, o := range opts {
		o(&c)
	}
	t := &transform{}
	for _, s := range c.sets {
		prog, err := expr.Compile(s.src)
		if err != nil {
			return nil, fmt.Errorf("workflow-transform: compile %q for %q: %w", s.src, s.out, err)
		}
		t.bindings = append(t.bindings, binding{out: s.out, prog: prog, src: s.src})
	}
	return t, nil
}

func (t *transform) Do(_ context.Context, in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(t.bindings))
	for _, b := range t.bindings {
		v, err := expr.Run(b.prog, in)
		if err != nil {
			return nil, fmt.Errorf("workflow-transform: eval %q for %q: %w", b.src, b.out, err)
		}
		out[b.out] = v
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action/transform/...`
Expected: PASS.

- [ ] **Step 5: Add the Example**

Create `action/transform/example_test.go`:

```go
package transform_test

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/action/transform"
)

func ExampleNewTransform() {
	a, _ := transform.NewTransform(
		transform.Set("total", "price * qty"),
		transform.Set("vip", "total > 1000"),
	)
	out, _ := a.Do(context.Background(), map[string]any{"price": 500, "qty": 3})
	fmt.Println(out["total"], out["vip"])
	// Output: 1500 true
}
```

- [ ] **Step 6: Run package tests + commit**

Run: `go test ./action/transform/...`
Expected: PASS (incl. the example).

```bash
git add action/transform/
git commit -m "feat(action/transform): expr-based variable transform action"
```

---

### Task 4: `action/logaction` — structured-log action

**Files:**
- Create: `action/logaction/logaction.go`
- Test: `action/logaction/logaction_test.go`, `action/logaction/example_test.go`

**Interfaces:**
- Consumes: `action.ServiceAction`, `log/slog`.
- Produces:
  - `logaction.NewLog(opts ...logaction.Option) action.ServiceAction`
  - `logaction.WithLogger(*slog.Logger) Option`
  - `logaction.WithLevel(slog.Level) Option`
  - `logaction.WithMessage(string) Option`
  - `logaction.WithKeys(...string) Option`

- [ ] **Step 1: Write the failing test**

Create `action/logaction/logaction_test.go`:

```go
package logaction_test

import (
	"bytes"
	"context"
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
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			opts := append([]logaction.Option{logaction.WithLogger(logger)}, tc.opts...)
			a := logaction.NewLog(opts...)

			out, err := a.Do(context.Background(), tc.in)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action/logaction/...`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write minimal implementation**

Create `action/logaction/logaction.go`:

```go
// Package logaction provides a service action that emits a structured slog record
// of selected instance variables and passes the variables through unchanged. It is
// well suited to fire-and-forget paths (reminders, audit points, debugging).
package logaction

import (
	"context"
	"log/slog"

	"github.com/kartaladev/wrkflw/action"
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action/logaction/...`
Expected: PASS.

- [ ] **Step 5: Add the Example**

Create `action/logaction/example_test.go`:

```go
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
```

- [ ] **Step 6: Run package tests + commit**

Run: `go test ./action/logaction/...`
Expected: PASS.

```bash
git add action/logaction/
git commit -m "feat(action/logaction): structured-log pass-through action"
```

---

### Task 5: `action/httpcall` — REST/HTTP call action

**Files:**
- Create: `action/httpcall/httpcall.go`
- Test: `action/httpcall/httpcall_test.go`, `action/httpcall/example_test.go`

**Interfaces:**
- Consumes: `action.ServiceAction`, `action.NonRetryable` (Task 1), `net/http`, `expr-lang/expr`.
- Produces:
  - `httpcall.NewHTTPCall(opts ...httpcall.Option) action.ServiceAction`
  - Options: `WithBaseURL(string)`, `WithMethod(string)`, `WithHeader(k, v string)`, `WithHTTPClient(*http.Client)`, `WithURLExpr(string)`, `WithBodyKey(string)`, `WithOutputKeys(status, body, headers string)`.
  - Output keys default: `httpStatus int`, `httpBody any`, `httpHeaders map[string]string`.

- [ ] **Step 1: Write the failing test**

Create `action/httpcall/httpcall_test.go`:

```go
package httpcall_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/action/httpcall"
)

func TestHTTPCall(t *testing.T) {
	tests := map[string]struct {
		handler http.HandlerFunc
		opts    func(base string) []httpcall.Option
		in      map[string]any
		assert  func(t *testing.T, out map[string]any, err error)
	}{
		"GET decodes JSON body and status into output keys": {
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				_, _ = io.WriteString(w, `{"ok":true}`)
			},
			func(base string) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(base), httpcall.WithMethod(http.MethodGet)}
			},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if out["httpStatus"] != 200 {
					t.Fatalf("status = %v, want 200", out["httpStatus"])
				}
				body, _ := out["httpBody"].(map[string]any)
				if body["ok"] != true {
					t.Fatalf("body = %v, want ok:true", out["httpBody"])
				}
			},
		},
		"POST sends JSON body from input key": {
			func(w http.ResponseWriter, r *http.Request) {
				var got map[string]any
				_ = json.NewDecoder(r.Body).Decode(&got)
				if got["name"] != "ada" {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(201)
			},
			func(base string) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(base), httpcall.WithMethod(http.MethodPost), httpcall.WithBodyKey("payload")}
			},
			map[string]any{"payload": map[string]any{"name": "ada"}},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if out["httpStatus"] != 201 {
					t.Fatalf("status = %v, want 201", out["httpStatus"])
				}
			},
		},
		"4xx returns a non-retryable error": {
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400) },
			func(base string) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(base), httpcall.WithMethod(http.MethodGet)}
			},
			map[string]any{},
			func(t *testing.T, _ map[string]any, err error) {
				if err == nil {
					t.Fatalf("expected error for 400")
				}
				if action.IsRetryable(err) {
					t.Fatalf("400 should be non-retryable")
				}
			},
		},
		"429 returns a retryable error": {
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) },
			func(base string) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(base), httpcall.WithMethod(http.MethodGet)}
			},
			map[string]any{},
			func(t *testing.T, _ map[string]any, err error) {
				if err == nil {
					t.Fatalf("expected error for 429")
				}
				if !action.IsRetryable(err) {
					t.Fatalf("429 should be retryable")
				}
			},
		},
		"5xx returns a retryable error": {
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) },
			func(base string) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(base), httpcall.WithMethod(http.MethodGet)}
			},
			map[string]any{},
			func(t *testing.T, _ map[string]any, err error) {
				if err == nil {
					t.Fatalf("expected error for 503")
				}
				if !action.IsRetryable(err) {
					t.Fatalf("503 should be retryable")
				}
			},
		},
		"static header is sent": {
			func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "k1" {
					w.WriteHeader(401)
					return
				}
				w.WriteHeader(200)
			},
			func(base string) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(base), httpcall.WithMethod(http.MethodGet), httpcall.WithHeader("X-Api-Key", "k1")}
			},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if out["httpStatus"] != 200 {
					t.Fatalf("status = %v, want 200 (header not sent?)", out["httpStatus"])
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			a := httpcall.NewHTTPCall(tc.opts(srv.URL)...)
			out, err := a.Do(t.Context(), tc.in)
			tc.assert(t, out, err)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action/httpcall/...`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write minimal implementation**

Create `action/httpcall/httpcall.go`:

```go
// Package httpcall provides a service action that calls a REST/HTTP endpoint and
// maps the response status, body, and headers into output variables. 4xx responses
// (except 408 and 429) are reported as non-retryable; 5xx, 408, 429, and transport
// errors are retryable, so the runtime's retry policy applies correctly.
package httpcall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kartaladev/wrkflw/action"
)

// Option configures an HTTP call action.
type Option func(*httpCall)

type httpCall struct {
	client     *http.Client
	baseURL    string
	method     string
	headers    http.Header
	bodyKey    string
	statusKey  string
	bodyOutKey string
	hdrOutKey  string
}

// WithBaseURL sets the request URL. Required (an empty URL yields a non-retryable error).
func WithBaseURL(u string) Option { return func(h *httpCall) { h.baseURL = u } }

// WithMethod sets the HTTP method. Default: POST when a body key is configured, else GET.
func WithMethod(m string) Option { return func(h *httpCall) { h.method = m } }

// WithHeader adds a static request header. Repeatable.
func WithHeader(k, v string) Option { return func(h *httpCall) { h.headers.Add(k, v) } }

// WithHTTPClient injects the http.Client (e.g. an otel-instrumented one).
// Default: a client with a 30s timeout.
func WithHTTPClient(c *http.Client) Option { return func(h *httpCall) { h.client = c } }

// WithBodyKey names the input variable holding the request body (JSON-encoded).
func WithBodyKey(k string) Option { return func(h *httpCall) { h.bodyKey = k } }

// WithOutputKeys overrides the output variable keys for status, body, and headers.
// Defaults: "httpStatus", "httpBody", "httpHeaders".
func WithOutputKeys(status, body, headers string) Option {
	return func(h *httpCall) { h.statusKey, h.bodyOutKey, h.hdrOutKey = status, body, headers }
}

// NewHTTPCall returns a service action that performs one HTTP request per Do.
func NewHTTPCall(opts ...Option) action.ServiceAction {
	h := &httpCall{
		client:     &http.Client{Timeout: 30 * time.Second},
		headers:    http.Header{},
		statusKey:  "httpStatus",
		bodyOutKey: "httpBody",
		hdrOutKey:  "httpHeaders",
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

func (h *httpCall) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	if h.baseURL == "" {
		return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: no base URL configured"))
	}
	method := h.method
	if method == "" {
		if h.bodyKey != "" {
			method = http.MethodPost
		} else {
			method = http.MethodGet
		}
	}

	var bodyReader io.Reader
	if h.bodyKey != "" {
		if v, ok := in[h.bodyKey]; ok {
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: encode body: %w", err))
			}
			bodyReader = bytes.NewReader(raw)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, h.baseURL, bodyReader)
	if err != nil {
		return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: build request: %w", err))
	}
	for k, vs := range h.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		// Transport/timeout error — retryable (plain error).
		return nil, fmt.Errorf("workflow-httpcall: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{
		h.statusKey:  resp.StatusCode,
		h.bodyOutKey: decodeBody(resp.Header.Get("Content-Type"), raw),
		h.hdrOutKey:  flattenHeaders(resp.Header),
	}

	if resp.StatusCode >= 400 {
		err := fmt.Errorf("workflow-httpcall: %s %s -> %d", method, h.baseURL, resp.StatusCode)
		if resp.StatusCode != http.StatusRequestTimeout &&
			resp.StatusCode != http.StatusTooManyRequests &&
			resp.StatusCode < 500 {
			return out, action.NonRetryable(err)
		}
		return out, err
	}
	return out, nil
}

func decodeBody(contentType string, raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	if strings.Contains(contentType, "application/json") {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			return v
		}
	}
	return string(raw)
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k := range h {
		out[k] = h.Get(k)
	}
	return out
}
```

Note: `out` is returned alongside the error on 4xx/5xx so the response is observable; the runtime ignores the output map on failure but the error carries retryability.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action/httpcall/...`
Expected: PASS.

- [ ] **Step 5: Add the Example**

Create `action/httpcall/example_test.go`:

```go
package httpcall_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/kartaladev/wrkflw/action/httpcall"
)

func ExampleNewHTTPCall() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(httpcall.WithBaseURL(srv.URL), httpcall.WithMethod(http.MethodGet))
	out, _ := a.Do(context.Background(), nil)
	fmt.Println(out["httpStatus"])
	// Output: 200
}
```

- [ ] **Step 6: Run package tests + commit**

Run: `go test ./action/httpcall/...`
Expected: PASS.

```bash
git add action/httpcall/
git commit -m "feat(action/httpcall): REST call action with retry classification"
```

---

### Task 6: `action/email` — SMTP send action (unit + integration)

**Files:**
- Create: `action/email/email.go`
- Test: `action/email/email_test.go`, `action/email/example_test.go`, `action/email/email_integration_test.go`

**Interfaces:**
- Consumes: `action.ServiceAction`, `net/smtp`, `text/template`.
- Produces:
  - `email.NewEmail(opts ...email.Option) action.ServiceAction`
  - Options: `WithSMTPAddr(string)`, `WithAuth(user, pass string)`, `WithFrom(string)`, `WithTo(...string)`, `WithSubjectTemplate(string)`, `WithBodyTemplate(string)`, `WithHTML()`, `WithSender(sender)` (test seam).
  - `email.sender` is an unexported interface `send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error`; default uses `smtp.SendMail`.
  - Output key: `emailSent bool`.

**Note on the seam:** `WithSender` injects a fake for unit tests; the default sender wraps `smtp.SendMail`. The unit test exercises template rendering + recipient + message assembly via the fake; the integration test (Step 6) exercises the real default sender against a mailpit container.

- [ ] **Step 1: Write the failing unit test**

Create `action/email/email_test.go`:

```go
package email_test

import (
	"context"
	"net/smtp"
	"strings"
	"testing"

	"github.com/kartaladev/wrkflw/action/email"
)

type capturedSend struct {
	from string
	to   []string
	msg  []byte
}

func TestEmailRendersAndSends(t *testing.T) {
	var got capturedSend
	fake := email.SenderFunc(func(_ string, _ smtp.Auth, from string, to []string, msg []byte) error {
		got = capturedSend{from, to, msg}
		return nil
	})

	a := email.NewEmail(
		email.WithSMTPAddr("smtp.example.com:587"),
		email.WithFrom("no-reply@example.com"),
		email.WithTo("user@example.com"),
		email.WithSubjectTemplate("Order {{.orderID}} confirmed"),
		email.WithBodyTemplate("Hi {{.name}}, your order {{.orderID}} is confirmed."),
		email.WithSender(fake),
	)

	out, err := a.Do(context.Background(), map[string]any{"orderID": "A-1", "name": "Ada"})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}
	if out["emailSent"] != true {
		t.Fatalf("emailSent = %v, want true", out["emailSent"])
	}
	if got.from != "no-reply@example.com" || len(got.to) != 1 || got.to[0] != "user@example.com" {
		t.Fatalf("envelope wrong: from=%q to=%v", got.from, got.to)
	}
	msg := string(got.msg)
	if !strings.Contains(msg, "Subject: Order A-1 confirmed") {
		t.Fatalf("subject not rendered in message: %q", msg)
	}
	if !strings.Contains(msg, "Hi Ada, your order A-1 is confirmed.") {
		t.Fatalf("body not rendered in message: %q", msg)
	}
}

func TestEmailTemplateError(t *testing.T) {
	a := email.NewEmail(
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithBodyTemplate("{{.unclosed"),
		email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error { return nil })),
	)
	if _, err := a.Do(context.Background(), map[string]any{}); err == nil {
		t.Fatalf("expected template parse error, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action/email/...`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write minimal implementation**

Create `action/email/email.go`:

```go
// Package email provides a service action that sends an email over SMTP, rendering
// the subject and body as text/templates over the instance variables.
package email

import (
	"bytes"
	"context"
	"fmt"
	"net/smtp"
	"strings"
	"text/template"

	"github.com/kartaladev/wrkflw/action"
)

// sender abstracts the SMTP send so message assembly is testable without a server.
type sender interface {
	send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

// SenderFunc adapts a function to the sender seam (exported for tests).
type SenderFunc func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error

func (f SenderFunc) send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return f(addr, auth, from, to, msg)
}

type smtpSender struct{}

func (smtpSender) send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return smtp.SendMail(addr, auth, from, to, msg)
}

// Option configures an email action.
type Option func(*emailAction)

type emailAction struct {
	addr        string
	auth        smtp.Auth
	from        string
	to          []string
	subjectTmpl string
	bodyTmpl    string
	html        bool
	snd         sender
}

// WithSMTPAddr sets the SMTP server address ("host:port").
func WithSMTPAddr(addr string) Option { return func(a *emailAction) { a.addr = addr } }

// WithAuth sets PLAIN SMTP auth. host is derived from the SMTP address.
func WithAuth(user, pass string) Option {
	return func(a *emailAction) {
		host := a.addr
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		a.auth = smtp.PlainAuth("", user, pass, host)
	}
}

// WithFrom sets the envelope/From address.
func WithFrom(addr string) Option { return func(a *emailAction) { a.from = addr } }

// WithTo sets recipient addresses.
func WithTo(addrs ...string) Option { return func(a *emailAction) { a.to = addrs } }

// WithSubjectTemplate sets the subject as a text/template over the variables.
func WithSubjectTemplate(t string) Option { return func(a *emailAction) { a.subjectTmpl = t } }

// WithBodyTemplate sets the body as a text/template over the variables.
func WithBodyTemplate(t string) Option { return func(a *emailAction) { a.bodyTmpl = t } }

// WithHTML sets the Content-Type to text/html (default text/plain).
func WithHTML() Option { return func(a *emailAction) { a.html = true } }

// WithSender overrides the SMTP sender (test seam).
func WithSender(s sender) Option { return func(a *emailAction) { a.snd = s } }

// NewEmail returns a service action that sends one email per Do.
func NewEmail(opts ...Option) action.ServiceAction {
	a := &emailAction{snd: smtpSender{}}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *emailAction) Do(_ context.Context, in map[string]any) (map[string]any, error) {
	subject, err := render(a.subjectTmpl, in)
	if err != nil {
		return nil, action.NonRetryable(fmt.Errorf("workflow-email: subject template: %w", err))
	}
	body, err := render(a.bodyTmpl, in)
	if err != nil {
		return nil, action.NonRetryable(fmt.Errorf("workflow-email: body template: %w", err))
	}

	contentType := "text/plain"
	if a.html {
		contentType = "text/html"
	}
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", a.from)
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(a.to, ", "))
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: %s; charset=UTF-8\r\n\r\n", contentType)
	msg.WriteString(body)

	if err := a.snd.send(a.addr, a.auth, a.from, a.to, msg.Bytes()); err != nil {
		return nil, fmt.Errorf("workflow-email: send: %w", err)
	}
	return map[string]any{"emailSent": true}, nil
}

func render(tmpl string, vars map[string]any) (string, error) {
	t, err := template.New("email").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, vars); err != nil {
		return "", err
	}
	return b.String(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action/email/...`
Expected: PASS.

- [ ] **Step 5: Add the Example**

Create `action/email/example_test.go`:

```go
package email_test

import (
	"context"
	"fmt"
	"net/smtp"

	"github.com/kartaladev/wrkflw/action/email"
)

func ExampleNewEmail() {
	// Inject a fake sender so the example is hermetic.
	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithTo("user@example.com"),
		email.WithSubjectTemplate("Welcome {{.name}}"),
		email.WithBodyTemplate("Hello {{.name}}!"),
		email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error { return nil })),
	)
	out, _ := a.Do(context.Background(), map[string]any{"name": "Ada"})
	fmt.Println(out["emailSent"])
	// Output: true
}
```

- [ ] **Step 6: Write the integration test (mailpit via testcontainers)**

First check `use-testcontainers` skill and whether a mailpit helper exists. If none, create the test using `testcontainers-go` directly with image `axllent/mailpit:latest`, exposing SMTP port 1025 and HTTP API port 8025. Guard with a build/run that skips when Docker is unavailable per the repo's existing integration-test convention (inspect a neighbouring `*_integration_test.go` for the exact skip/tag pattern and mirror it).

Create `action/email/email_integration_test.go`:

```go
package email_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/kartaladev/wrkflw/action/email"
)

func TestEmailSendsViaMailpit(t *testing.T) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit:latest",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForListeningPort("1025/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	smtpPort, err := c.MappedPort(ctx, "1025")
	require.NoError(t, err)
	apiPort, err := c.MappedPort(ctx, "8025")
	require.NoError(t, err)

	a := email.NewEmail(
		email.WithSMTPAddr(host+":"+smtpPort.Port()),
		email.WithFrom("no-reply@example.com"),
		email.WithTo("user@example.com"),
		email.WithSubjectTemplate("Order {{.orderID}}"),
		email.WithBodyTemplate("Hi {{.name}}"),
	)
	out, err := a.Do(ctx, map[string]any{"orderID": "A-1", "name": "Ada"})
	require.NoError(t, err)
	require.Equal(t, true, out["emailSent"])

	// Poll mailpit's API for the delivered message.
	var total int
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + host + ":" + apiPort.Port() + "/api/v1/messages")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		var body struct {
			Total int `json:"total"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		total = body.Total
		return total >= 1
	}, 10*time.Second, 200*time.Millisecond)
	require.GreaterOrEqual(t, total, 1)
}
```

Run: `go test ./action/email/ -run TestEmailSendsViaMailpit -v` (requires Docker).
Expected: PASS. If Docker is unavailable, the test should be skipped per the repo convention you mirrored.

- [ ] **Step 7: Run package tests + commit**

Run: `go test ./action/email/...` (unit; integration runs when Docker is up).
Expected: PASS.

```bash
git add action/email/
git commit -m "feat(action/email): SMTP send action with templated subject/body"
```

---

### Task 7: ADRs

**Files:**
- Create: `docs/adr/0074-retryable-action-error-contract.md`
- Create: `docs/adr/0075-builtin-service-action-catalog.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Write ADR-0074**

Create `docs/adr/0074-retryable-action-error-contract.md` using the Nygard template (Status/Date, Context, Decision, Consequences). Read `docs/adr/0001-record-architecture-decisions.md` for the exact heading structure and match it. Content:

- **Status:** Accepted, 2026-06-29.
- **Context:** `runtime/runner.go` returned `ActionFailed` with `Retryable=true` for every action error. Built-in actions (HTTP 4xx, malformed config/templates) must signal non-retryable failures; without a contract every error retries until backoff exhaustion.
- **Decision:** Add `action.Retryabler interface { error; Retryable() bool }`, `action.NonRetryable(err) error`, and `action.IsRetryable(err) bool`. The runtime uses `action.IsRetryable(err)` when building `ActionFailed`. Default unchanged: nil and plain errors are retryable; only an error implementing `Retryabler` (found via `errors.As`) overrides it. The engine `ActionFailed` trigger already carried `Retryable bool`, so engine and model stay zero-diff.
- **Consequences:** Backward compatible (existing actions keep retrying). Consumers can now mark their own errors non-retryable. One core file changes (`runtime/runner.go`); the contract lives in the public `action` package next to `ServiceAction`.

- [ ] **Step 2: Write ADR-0075**

Create `docs/adr/0075-builtin-service-action-catalog.md`, same template. Content:

- **Status:** Accepted, 2026-06-29.
- **Context:** Consumers re-implemented common integrations (REST calls, email) as inline closures; nothing shipped in the library. The engine had deliberately avoided built-in I/O to dodge dependency bloat.
- **Decision:** Ship four public `action/*` subpackages — `httpcall`, `email`, `transform`, `logaction` — each a configured constructor returning `action.ServiceAction`, built on stdlib + the existing `expr-lang/expr` (no new runtime dependency). Per-call values use configurable key-mapping + interpolation (`expr` for scalar values, `text/template` for email bodies). Slack/Teams/webhook (thin HTTP wrappers), shell-exec, and standalone delay/publish are deferred (the last duplicate engine timers/outbox).
- **Consequences:** Faster consumer onboarding; a maintained, tested, documented surface to evolve. `go.mod` gains no runtime dependency; the email integration test adds a test-only mailpit container. A deliberate scope boundary keeps vendor-specific connectors out of core.

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0074-retryable-action-error-contract.md docs/adr/0075-builtin-service-action-catalog.md
git commit -m "docs(adr): 0074 retryable-action-error contract, 0075 builtin action catalog"
```

---

### Task 8: Final verification

**Files:** none (verification only).

- [ ] **Step 1: Full build + test + lint**

Run:
```bash
go build ./... && \
go test ./action/... ./runtime/... && \
golangci-lint run ./action/... ./runtime/...
```
Expected: build clean, all tests pass, lint clean.

- [ ] **Step 2: Coverage check on new packages**

Run:
```bash
go test -race -coverprofile=cover.out ./action/... && go tool cover -func=cover.out | tail -1
```
Expected: ≥ 85% line coverage across the action packages. If any new package is below 85%, add the missing-case test (e.g. transform missing-variable path, httpcall transport-error path, email auth-host derivation) before finishing.

- [ ] **Step 3: Confirm engine/model zero-diff**

Run: `git diff --name-only main -- engine/ model/`
Expected: empty output (no engine/model files changed).

---

## Self-Review notes

- **Spec coverage:** retry contract (T1, T2), httpcall (T5), email unit+integration (T6), transform (T3), logaction (T4), ADRs 0074/0075 (T7), zero-diff + coverage verification (T8). All spec sections mapped.
- **Type consistency:** `action.IsRetryable`/`NonRetryable`/`Retryabler` used identically in T1/T2/T5/T6. Output keys `httpStatus`/`httpBody`/`httpHeaders` consistent between T5 impl and test. `email.SenderFunc` seam consistent T6 impl/test/example.
- **Deferred-to-implementer specifics (intentional, not placeholders):** the exact runtime test harness call (T2 Step 1) and the integration-test skip/tag convention (T6 Step 6) must mirror existing neighbouring tests — the plan says which file to read and what to assert.
