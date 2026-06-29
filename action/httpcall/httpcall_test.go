package httpcall_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/action/httpcall"
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
		"missing base URL returns non-retryable error": {
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) },
			func(_ string) []httpcall.Option {
				return []httpcall.Option{httpcall.WithMethod(http.MethodGet)}
			},
			map[string]any{},
			func(t *testing.T, _ map[string]any, err error) {
				if err == nil {
					t.Fatal("expected error for missing base URL")
				}
				if action.IsRetryable(err) {
					t.Fatal("missing base URL should be non-retryable")
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
