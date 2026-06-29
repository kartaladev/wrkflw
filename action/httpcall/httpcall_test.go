package httpcall_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/action/httpcall"
)

// TestHTTPCallMissingBaseURL verifies that NewHTTPCall with no base URL configured
// returns a non-retryable error from Do without making any network request.
// No httptest.Server is started — the action errors before any connection attempt.
func TestHTTPCallMissingBaseURL(t *testing.T) {
	a := httpcall.NewHTTPCall(httpcall.WithMethod(http.MethodGet))
	_, err := a.Do(t.Context(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing base URL, got nil")
	}
	if action.IsRetryable(err) {
		t.Fatal("missing base URL error must be non-retryable")
	}
}

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
		"custom output keys": {
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				_, _ = io.WriteString(w, `{"v":1}`)
			},
			func(base string) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithBaseURL(base),
					httpcall.WithMethod(http.MethodGet),
					httpcall.WithOutputKeys("myStatus", "myBody", "myHeaders"),
				}
			},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if out["myStatus"] != 200 {
					t.Fatalf("status = %v, want 200", out["myStatus"])
				}
				if out["myBody"] == nil {
					t.Fatal("myBody should not be nil")
				}
			},
		},
		"custom http client is used": {
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) },
			func(base string) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithBaseURL(base),
					httpcall.WithMethod(http.MethodGet),
					httpcall.WithHTTPClient(&http.Client{}),
				}
			},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if out["httpStatus"] != 204 {
					t.Fatalf("status = %v, want 204", out["httpStatus"])
				}
			},
		},
		"plain text body returned as string": {
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(200)
				_, _ = io.WriteString(w, "hello world")
			},
			func(base string) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(base), httpcall.WithMethod(http.MethodGet)}
			},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if out["httpBody"] != "hello world" {
					t.Fatalf("httpBody = %v, want 'hello world'", out["httpBody"])
				}
			},
		},
		"WithURLExpr builds URL from input variable": {
			func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ping" {
					w.WriteHeader(404)
					return
				}
				w.WriteHeader(200)
			},
			func(base string) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithURLExpr(`srvURL + "/ping"`),
					httpcall.WithMethod(http.MethodGet),
				}
			},
			// srvURL is injected per-test below (see override loop)
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if out["httpStatus"] != 200 {
					t.Fatalf("status = %v, want 200", out["httpStatus"])
				}
			},
		},
		"WithURLExpr bad expression yields non-retryable error at Do": {
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) },
			func(_ string) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithURLExpr(`!!! not an expr`),
					httpcall.WithMethod(http.MethodGet),
				}
			},
			map[string]any{},
			func(t *testing.T, _ map[string]any, err error) {
				if err == nil {
					t.Fatal("expected error for bad url expr")
				}
				if action.IsRetryable(err) {
					t.Fatal("bad url expr should be non-retryable")
				}
			},
		},
		"WithURLExpr non-string result yields non-retryable error": {
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) },
			func(_ string) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithURLExpr(`42`),
					httpcall.WithMethod(http.MethodGet),
				}
			},
			map[string]any{},
			func(t *testing.T, _ map[string]any, err error) {
				if err == nil {
					t.Fatal("expected error for non-string url expr result")
				}
				if action.IsRetryable(err) {
					t.Fatal("non-string url expr result should be non-retryable")
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			in := tc.in
			// Inject the test server URL for WithURLExpr cases that need it.
			if _, needsSrv := in["srvURL"]; !needsSrv {
				in = make(map[string]any, len(tc.in)+1)
				for k, v := range tc.in {
					in[k] = v
				}
				in["srvURL"] = srv.URL
			}
			a := httpcall.NewHTTPCall(tc.opts(srv.URL)...)
			out, err := a.Do(t.Context(), in)
			tc.assert(t, out, err)
		})
	}
}

// ---------------------------------------------------------------------------
// WithHeaderFunc tests
// ---------------------------------------------------------------------------

// TestWithHeaderFunc_SetsHeader verifies that a HeaderFunc can add a request header
// (e.g. a dynamically fetched auth token) that the server sees.
func TestWithHeaderFunc_SetsHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		httpcall.WithMethod(http.MethodGet),
		httpcall.WithHeaderFunc(func(_ context.Context, h http.Header, _ map[string]any) error {
			h.Set("Authorization", "Bearer fake-token")
			return nil
		}),
	)
	out, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["httpStatus"] != http.StatusOK {
		t.Fatalf("status = %v, want 200", out["httpStatus"])
	}
}

// TestWithHeaderFunc_OverridesStaticHeader verifies that a HeaderFunc value wins over
// a same-key static WithHeader value (funcs applied after static headers).
func TestWithHeaderFunc_OverridesStaticHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Token"); got != "dynamic" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "got X-Token=%q", got)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		httpcall.WithMethod(http.MethodGet),
		httpcall.WithHeader("X-Token", "static"),
		httpcall.WithHeaderFunc(func(_ context.Context, h http.Header, _ map[string]any) error {
			h.Set("X-Token", "dynamic") // override the static value
			return nil
		}),
	)
	out, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["httpStatus"] != http.StatusOK {
		t.Fatalf("status = %v, want 200; dynamic header did not override static", out["httpStatus"])
	}
}

// TestWithHeaderFunc_ErrorRetryability verifies that a NonRetryable error from a HeaderFunc
// propagates correctly and the server is never hit; a plain error stays retryable.
func TestWithHeaderFunc_ErrorRetryability(t *testing.T) {
	type tc struct {
		name      string
		fn        httpcall.HeaderFunc
		wantRetry bool
	}
	cases := []tc{
		{
			name: "NonRetryable header-func error -> non-retryable",
			fn: func(_ context.Context, _ http.Header, _ map[string]any) error {
				return action.NonRetryable(fmt.Errorf("token service unavailable"))
			},
			wantRetry: false,
		},
		{
			name: "plain header-func error -> retryable",
			fn: func(_ context.Context, _ http.Header, _ map[string]any) error {
				return fmt.Errorf("transient network glitch")
			},
			wantRetry: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			a := httpcall.NewHTTPCall(
				httpcall.WithBaseURL(srv.URL),
				httpcall.WithMethod(http.MethodGet),
				httpcall.WithHeaderFunc(c.fn),
			)
			_, err := a.Do(t.Context(), map[string]any{})
			if err == nil {
				t.Fatal("expected error from failing HeaderFunc, got nil")
			}
			if action.IsRetryable(err) != c.wantRetry {
				t.Fatalf("IsRetryable = %v, want %v", action.IsRetryable(err), c.wantRetry)
			}
			if hits.Load() != 0 {
				t.Fatal("server must NOT have been hit when HeaderFunc returns an error")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WithBodyFunc tests
// ---------------------------------------------------------------------------

// TestWithBodyFunc_SendsCustomBody verifies that a BodyFunc body is sent to the server
// verbatim and Content-Type is NOT auto-set to application/json.
func TestWithBodyFunc_SendsCustomBody(t *testing.T) {
	want := `{"custom":true}`
	var gotBody string
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		httpcall.WithBodyFunc(func(_ context.Context, _ map[string]any) (io.Reader, error) {
			return strings.NewReader(want), nil
		}),
	)
	_, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody != want {
		t.Fatalf("body = %q, want %q", gotBody, want)
	}
	// Consumer controls Content-Type; we must NOT inject application/json automatically.
	if gotCT == "application/json" {
		t.Fatal("Content-Type must NOT be auto-set to application/json for BodyFunc path")
	}
}

// TestWithBodyFunc_PrecedenceOverBodyKey verifies that when both WithBodyFunc and WithBodyKey
// are set, the BodyFunc result is used and the bodyKey value is ignored.
func TestWithBodyFunc_PrecedenceOverBodyKey(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		httpcall.WithBodyKey("payload"),
		httpcall.WithBodyFunc(func(_ context.Context, _ map[string]any) (io.Reader, error) {
			return strings.NewReader("from-func"), nil
		}),
	)
	_, err := a.Do(t.Context(), map[string]any{"payload": map[string]any{"key": "value"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody != "from-func" {
		t.Fatalf("body = %q, want %q (BodyFunc must take precedence)", gotBody, "from-func")
	}
}

// TestWithBodyFunc_ErrorRetryability verifies that BodyFunc errors propagate correctly
// (NonRetryable honored) and the server is never hit.
func TestWithBodyFunc_ErrorRetryability(t *testing.T) {
	type tc struct {
		name      string
		fn        httpcall.BodyFunc
		wantRetry bool
	}
	cases := []tc{
		{
			name: "NonRetryable body-func error -> non-retryable",
			fn: func(_ context.Context, _ map[string]any) (io.Reader, error) {
				return nil, action.NonRetryable(fmt.Errorf("bad body data"))
			},
			wantRetry: false,
		},
		{
			name: "plain body-func error -> retryable",
			fn: func(_ context.Context, _ map[string]any) (io.Reader, error) {
				return nil, fmt.Errorf("storage unavailable")
			},
			wantRetry: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			a := httpcall.NewHTTPCall(
				httpcall.WithBaseURL(srv.URL),
				httpcall.WithBodyFunc(c.fn),
			)
			_, err := a.Do(t.Context(), map[string]any{})
			if err == nil {
				t.Fatal("expected error from failing BodyFunc, got nil")
			}
			if action.IsRetryable(err) != c.wantRetry {
				t.Fatalf("IsRetryable = %v, want %v", action.IsRetryable(err), c.wantRetry)
			}
			if hits.Load() != 0 {
				t.Fatal("server must NOT be hit when BodyFunc returns an error")
			}
		})
	}
}

// TestWithBodyFunc_DefaultMethodPOST verifies that the HTTP method defaults to POST
// when WithBodyFunc is set (and no explicit WithMethod is provided).
func TestWithBodyFunc_DefaultMethodPOST(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		// No WithMethod — should default to POST because BodyFunc is set.
		httpcall.WithBodyFunc(func(_ context.Context, _ map[string]any) (io.Reader, error) {
			return strings.NewReader("body"), nil
		}),
	)
	_, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST (should default when BodyFunc set)", gotMethod)
	}
}

// ---------------------------------------------------------------------------
// WithBodyValidator tests
// ---------------------------------------------------------------------------

// TestWithBodyValidator_PassesAndSendsRequest verifies that a passing validator allows
// the request to proceed and the server receives it.
func TestWithBodyValidator_PassesAndSendsRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		httpcall.WithBodyKey("payload"),
		httpcall.WithBodyValidator(func(_ context.Context, body []byte) error {
			if !json.Valid(body) {
				return fmt.Errorf("invalid JSON")
			}
			return nil
		}),
	)
	_, err := a.Do(t.Context(), map[string]any{"payload": map[string]any{"name": "ada"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hit count = %d, want 1", hits.Load())
	}
}

// TestWithBodyValidator_FailsBlocksSend verifies that a failing validator returns a
// non-retryable error and the server is NOT hit.
func TestWithBodyValidator_FailsBlocksSend(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		httpcall.WithBodyKey("payload"),
		httpcall.WithBodyValidator(func(_ context.Context, _ []byte) error {
			return fmt.Errorf("required field missing")
		}),
	)
	_, err := a.Do(t.Context(), map[string]any{"payload": map[string]any{}})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if action.IsRetryable(err) {
		t.Fatal("body validation error must be non-retryable")
	}
	if hits.Load() != 0 {
		t.Fatal("server must NOT be hit when validation fails")
	}
}

// TestWithBodyValidator_WithBodyFunc_BuffersAndSendsCorrectly verifies that when a
// BodyValidator is combined with a BodyFunc, the validator receives the func's bytes
// and the server also receives the exact same body (buffering preserves the body).
func TestWithBodyValidator_WithBodyFunc_BuffersAndSendsCorrectly(t *testing.T) {
	funcBody := `{"validated":true}`
	var serverBody string
	var validatorBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		serverBody = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		httpcall.WithBodyFunc(func(_ context.Context, _ map[string]any) (io.Reader, error) {
			return bytes.NewBufferString(funcBody), nil
		}),
		httpcall.WithBodyValidator(func(_ context.Context, body []byte) error {
			validatorBody = body
			return nil // pass
		}),
	)
	_, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(validatorBody) != funcBody {
		t.Fatalf("validator got %q, want %q", validatorBody, funcBody)
	}
	if serverBody != funcBody {
		t.Fatalf("server got %q, want %q (body must not be dropped after buffering)", serverBody, funcBody)
	}
}
