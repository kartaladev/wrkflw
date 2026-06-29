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

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/zakyalvan/krtlwrkflw/action"
)

// Option configures an HTTP call action.
type Option func(*httpCall)

type httpCall struct {
	client      *http.Client
	baseURL     string
	urlExprProg *vm.Program
	urlExprErr  error
	method      string
	headers     http.Header
	bodyKey     string
	statusKey   string
	bodyOutKey  string
	hdrOutKey   string
}

// WithBaseURL sets the request URL. Required unless WithURLExpr is set (an empty URL
// with no URL expression yields a non-retryable error).
func WithBaseURL(u string) Option { return func(h *httpCall) { h.baseURL = u } }

// WithURLExpr sets an expr-lang expression that, evaluated against the input variable
// map at Do time, yields the request URL string. When set, it takes precedence over
// WithBaseURL. A compile error is deferred to Do and returned as a non-retryable error.
// The resulting URL is not validated; do not derive it from untrusted input without
// an allowlist or a restricted *http.Client transport (SSRF risk).
func WithURLExpr(exprStr string) Option {
	return func(h *httpCall) {
		prog, err := expr.Compile(exprStr)
		if err != nil {
			h.urlExprErr = fmt.Errorf("workflow-httpcall: compile url expr: %w", err)
			return
		}
		h.urlExprProg = prog
	}
}

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
	// Resolve request URL: url expr takes precedence over baseURL.
	requestURL := h.baseURL
	if h.urlExprErr != nil {
		return nil, action.NonRetryable(h.urlExprErr)
	}
	if h.urlExprProg != nil {
		result, err := expr.Run(h.urlExprProg, in)
		if err != nil {
			return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: eval url expr: %w", err))
		}
		s, ok := result.(string)
		if !ok {
			return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: eval url expr: result is %T, want string", result))
		}
		requestURL = s
	}
	if requestURL == "" {
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

	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
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

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("workflow-httpcall: read body: %w", err)
	}
	out := map[string]any{
		h.statusKey:  resp.StatusCode,
		h.bodyOutKey: decodeBody(resp.Header.Get("Content-Type"), raw),
		h.hdrOutKey:  flattenHeaders(resp.Header),
	}

	if resp.StatusCode >= 400 {
		err := fmt.Errorf("workflow-httpcall: %s %s -> %d", method, requestURL, resp.StatusCode)
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

// flattenHeaders converts a multi-value header map to a single-value map.
// Multi-value headers are collapsed to their first value (map[string]string limitation).
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k := range h {
		out[k] = h.Get(k)
	}
	return out
}
