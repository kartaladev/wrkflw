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

	"github.com/zakyalvan/krtlwrkflw/action"
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
