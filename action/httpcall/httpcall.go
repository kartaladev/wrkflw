// Package httpcall provides a service action that calls a REST/HTTP endpoint and
// maps the response status, body, and headers into output variables. 4xx responses
// (except 408 and 429) are reported as non-retryable; 5xx, 408, 429, and transport
// errors are retryable, so the runtime's retry policy applies correctly.
//
// # Programmatic headers
//
// Use [WithHeaderFunc] to compute request headers at call time — for example, to
// fetch a short-lived auth token from a token service. Header funcs run after the
// static [WithHeader] headers, so they can read and override previously set values.
//
// # Programmatic body
//
// Use [WithBodyFunc] to construct the request body programmatically. When both
// [WithBodyFunc] and [WithBodyKey] are set, WithBodyFunc takes precedence and the
// bodyKey value is ignored. Unlike the bodyKey path, the BodyFunc path does NOT
// auto-set Content-Type: application/json; the consumer controls the content type
// via [WithHeader] or [WithHeaderFunc].
//
// # Request-body validation
//
// Use [WithBodyValidator] to validate the request body bytes before the request is
// sent. The validator receives both the body bytes and the process variables (the
// same vars map passed to Do), enabling schema selection by variable value or
// cross-validation of body content against process state. A failing validator
// returns a non-retryable error and the request is never issued. When a validator
// is set alongside [WithBodyFunc], the BodyFunc reader is fully buffered into
// memory (io.ReadAll) so the bytes are available for inspection; document this to
// callers who otherwise expect streaming.
//
// # Response size limit
//
// The response body — and any buffered request body — is capped at a maximum
// number of bytes read into memory (default 10 MiB) to guard against memory
// exhaustion from a large or malicious upstream. Override the cap with
// [WithMaxResponseSize]; a non-positive value disables it. A body exceeding the
// cap fails with a non-retryable [ErrBodyTooLarge].
//
// # Connection pooling
//
// The default client carries its own [http.Transport] rather than falling back
// to [http.DefaultTransport], whose per-host idle cap of 2 would make concurrent
// service tasks against one host pay a fresh connection for all but two of them.
// Size the pool with [WithMaxIdleConnsPerHost] and [WithMaxConnsPerHost].
//
// ⚠ Each action built by [NewHTTPCall] owns its pool, so N actions calling the
// same host hold up to N separate sets of idle connections. That is the right
// trade for the usual case — a handful of actions constructed once at startup —
// but a consumer registering many actions against one upstream should build a
// single [http.Client] and pass it to each through [WithHTTPClient], which makes
// them share one pool.
//
// The guidance above describes HTTP/1.1 upstreams, where one request occupies
// one connection and the pool size is therefore the concurrency limit. Against
// an HTTP/2 upstream a single connection multiplexes many concurrent streams, so
// the per-host cap is largely inert and the total cap does not serialise
// requests the way it does on HTTP/1.1.
//
// # Redirects
//
// The default client follows up to 10 redirects that stay on the SAME HOST, and
// refuses one that changes host with a non-retryable [ErrCrossHostRedirect].
// This departs from Go's default of following to any host, deliberately (#67):
// this action's request URL can come from process data ([WithURLExpr]) and its
// response body is mapped into output variables, so following a redirect
// anywhere gives whoever can influence a process variable a read primitive
// against whatever the engine can reach.
//
// The boundary is the hostname, not the origin — a port change or an http→https
// upgrade on one hostname is still followed.
//
// A client supplied through [WithHTTPClient] is unaffected and keeps Go's
// behaviour unless it sets its own CheckRedirect. That is the way back to the
// old default.
//
// # What a redirect carries
//
// ⚠ Relevant when a cross-host redirect is re-enabled through [WithHTTPClient],
// and MEASURED rather than assumed. Go strips exactly six headers when a
// redirect changes host — Authorization, Www-Authenticate, Cookie, Cookie2,
// Proxy-Authorization and Proxy-Authenticate (net/http/client.go, go1.26.8). It
// strips them by NAME, so it makes no difference whether the value came from
// [WithHeader] or [WithHeaderFunc]. Every OTHER header crosses untouched — an
// X-Api-Key, a vendor bearer header, anything a header func invented. The
// example above for [WithHeaderFunc] is fetching a short-lived auth token, and
// nothing obliges such a token to be spelled "Authorization".
//
// The exact length of that list is not what matters — it is FIXED, so a
// credential named anything outside it crosses regardless.
//
// ⚠ Go's stripping rule is subdomain-PERMISSIVE: it ends in isDomainOrSubdomain,
// so foo.com → sub.foo.com keeps Authorization. This package's policy compares
// hostnames for equality and refuses that hop outright, so the default here is
// STRICTER than Go's header rule rather than aligned with it.
//
// ⚠ Neither rule treats a PORT as a boundary, so a redirect between two ports on
// one host is not "cross-host" to Go or to this package: Authorization follows
// it. On a container host where loopback spans many services, that hop is within
// the default policy.
package httpcall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/kartaladev/wrkflw/action"
)

// HeaderFunc sets request headers programmatically with access to ctx and the
// process variables; it may perform I/O (e.g. fetch an auth token).
// A non-nil error aborts the request. Wrap the error with [action.NonRetryable]
// to mark the failure as permanent; a plain error is treated as retryable.
type HeaderFunc func(ctx context.Context, h http.Header, vars map[string]any) error

// BodyFunc builds the request body from ctx and process variables; it may
// perform I/O before the request. A nil [io.Reader] means no body. Wrap any
// returned error with [action.NonRetryable] to mark the failure as permanent;
// a plain error is treated as retryable.
//
// When [WithBodyValidator] is also set and the returned reader is non-nil, the
// reader is fully buffered via io.ReadAll before validation. Callers that return
// a streaming body should be aware of this buffering behaviour.
type BodyFunc func(ctx context.Context, vars map[string]any) (io.Reader, error)

// BodyValidator validates the request body bytes before the request is sent,
// with access to the process variables. A validation failure is a permanent
// (non-retryable) error — the library wraps the returned error with
// [action.NonRetryable] automatically. Dependency-free: consumers plug in
// JSON-schema validation with their own library. The vars parameter is the
// same input variable map passed to [action.Action.Do], enabling
// schema selection by variable value or cross-validation of the body against
// process state — consistent with [WithHeaderFunc] and [WithBodyFunc] which
// also receive vars.
type BodyValidator func(ctx context.Context, body []byte, vars map[string]any) error

// Option configures an HTTP call action.
type Option func(*httpCall)

// defaultMaxResponseSize bounds the response body (and any buffered request
// body) read into memory, guarding against memory exhaustion from a large or
// malicious upstream. Overridable via [WithMaxResponseSize]; a non-positive
// value disables the bound.
const defaultMaxResponseSize int64 = 10 << 20 // 10 MiB

// defaultMaxIdleConnsPerHost is how many idle connections the default client
// keeps warm per host. It matters because a client with a nil Transport uses
// [http.DefaultTransport], whose per-host idle cap is
// [http.DefaultMaxIdleConnsPerHost] — 2. A workflow whose service tasks call one
// host concurrently keeps two connections warm and pays a fresh TCP (and, over
// TLS, handshake) cost for every other request, on the engine's main outbound
// path.
//
// The value matches [defaultMaxIdleConns], the process-wide cap net/http also
// defaults to 100, so neither cap is more restrictive than the other and "the
// pool holds 100 idle connections" means what a reader expects. It is not a
// claim about any particular workload; a consumer who knows their own
// concurrency should set [WithMaxIdleConnsPerHost], and the process-wide cap
// rises with it rather than clamping it.
//
// ⚠ This is deliberately NOT sized to "the driver's concurrency". The driver
// has no concurrency setting to size against: ProcessDriver.inflight is a
// sync.WaitGroup that counts admitted work rather than bounding it, and no
// option caps parallelism. Outbound concurrency is whatever a consumer drives
// in, so the library cannot know it and does not pretend to.
const defaultMaxIdleConnsPerHost = 100

// defaultMaxConnsPerHost is the default TOTAL per-host connection cap, and 0
// (unlimited) is deliberate — it leaves Go's own default in place.
//
// Unlike the idle cap, this one BLOCKS: once it is reached, a request waits for
// a connection to be returned instead of opening another. That converts a
// throughput limit into added latency, and choosing it wrongly is worse than
// not choosing it. Sizing it needs the consumer's concurrency and the upstream's
// tolerance, neither of which the library knows, so it is exposed through
// [WithMaxConnsPerHost] and left off by default.
const defaultMaxConnsPerHost = 0

// defaultMaxIdleConns is the process-wide idle-connection cap, matching
// net/http's own default. It is a floor rather than a fixed value:
// [newPooledTransport] raises it to match a larger per-host setting, because
// this cap would otherwise silently clamp the per-host one.
const defaultMaxIdleConns = 100

// ErrBodyTooLarge is returned (non-retryable) when a response or buffered
// request body exceeds the configured maximum size.
var ErrBodyTooLarge = errors.New("workflow-httpcall: body exceeds max size")

type httpCall struct {
	client        *http.Client
	baseURL       string
	urlExprProg   *vm.Program
	urlExprErr    error
	method        string
	headers       http.Header
	headerFuncs   []HeaderFunc
	bodyKey       string
	bodyFunc      BodyFunc
	bodyValidator BodyValidator
	statusKey     string
	bodyOutKey    string
	hdrOutKey     string

	// maxResponseSize bounds the response body (and buffered request body) read
	// into memory. A non-positive value disables the bound. See [WithMaxResponseSize].
	maxResponseSize int64

	// maxIdleConnsPerHost and maxConnsPerHost size the connection pool of the
	// DEFAULT client only. A client supplied through [WithHTTPClient] carries
	// its own Transport and is never modified — see that option's doc.
	maxIdleConnsPerHost int
	maxConnsPerHost     int
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

// WithMethod sets the HTTP method. Default: POST when a body source (WithBodyKey or
// WithBodyFunc) is configured, else GET.
func WithMethod(m string) Option { return func(h *httpCall) { h.method = m } }

// WithHeader adds a static request header. Repeatable.
func WithHeader(k, v string) Option { return func(h *httpCall) { h.headers.Add(k, v) } }

// WithHeaderFunc appends a programmatic header setter. Repeatable — funcs run in
// registration order, after all static [WithHeader] values are applied, so they can
// read and override previously set headers. On error the request is aborted; wrap
// with [action.NonRetryable] for a permanent failure.
func WithHeaderFunc(fn HeaderFunc) Option {
	return func(h *httpCall) { h.headerFuncs = append(h.headerFuncs, fn) }
}

// WithHTTPClient injects the http.Client (e.g. an otel-instrumented one).
// Default: a client with a 30s timeout and a connection pool sized by
// [WithMaxIdleConnsPerHost] and [WithMaxConnsPerHost].
//
// ⚠ An injected client is used EXACTLY as given. Its Transport is not modified
// and the two pool-sizing options above do not apply to it — a client is a
// consumer-owned object, and reaching into its Transport would retune whatever
// else shares it. A caller who wants both an injected client and a tuned pool
// sets the pool on that client's own Transport.
func WithHTTPClient(c *http.Client) Option { return func(h *httpCall) { h.client = c } }

// WithMaxIdleConnsPerHost sets how many idle connections the default client
// keeps warm per host. Default: [defaultMaxIdleConnsPerHost].
//
// Raising it above the number of requests a workflow runs concurrently against
// one host buys nothing; setting it below that number makes the excess requests
// pay a fresh connection each time, which is the ceiling Go's own default of 2
// imposes. A non-positive value restores Go's default.
//
// The transport's process-wide MaxIdleConns rises to match a larger value, so
// setting 200 here really does keep 200 idle connections per host rather than
// being clamped at the 100 net/http defaults to.
//
// It has no effect when a client is supplied through [WithHTTPClient].
func WithMaxIdleConnsPerHost(n int) Option {
	return func(h *httpCall) { h.maxIdleConnsPerHost = n }
}

// WithMaxConnsPerHost caps the TOTAL connections the default client opens per
// host, idle or in use. Default: unlimited, which is Go's own default.
//
// ⚠ This one blocks rather than degrading: once the cap is reached, a request
// WAITS for a connection to be returned instead of opening another, so it turns
// excess concurrency into latency and, against a slow upstream, into timeouts.
// Use it to protect an upstream that cannot take the load, not to tune
// throughput — [WithMaxIdleConnsPerHost] is the throughput knob. A non-positive
// value means unlimited.
//
// It has no effect when a client is supplied through [WithHTTPClient].
func WithMaxConnsPerHost(n int) Option {
	return func(h *httpCall) { h.maxConnsPerHost = n }
}

// WithBodyKey names the input variable holding the request body (JSON-encoded).
// When [WithBodyFunc] is also set, WithBodyFunc takes precedence and this key is
// ignored.
func WithBodyKey(k string) Option { return func(h *httpCall) { h.bodyKey = k } }

// WithBodyFunc sets a programmatic body builder. It takes precedence over [WithBodyKey]
// when both are configured. Unlike the bodyKey path, the BodyFunc path does NOT
// auto-set Content-Type: application/json — the consumer controls the content type via
// [WithHeader] or [WithHeaderFunc]. A nil reader returned by fn means no body.
func WithBodyFunc(fn BodyFunc) Option { return func(h *httpCall) { h.bodyFunc = fn } }

// WithBodyValidator registers a validator that runs on the built body bytes before
// the HTTP request is issued. Any error returned by the validator is wrapped with
// [action.NonRetryable] — validation failures are always permanent. The validator
// is called only when a body is present. When combined with [WithBodyFunc], the
// BodyFunc reader is fully buffered into memory (io.ReadAll) so bytes are available
// for the validator; set a validator only when the body fits comfortably in memory.
func WithBodyValidator(v BodyValidator) Option {
	return func(h *httpCall) { h.bodyValidator = v }
}

// WithOutputKeys overrides the output variable keys for status, body, and headers.
// Defaults: "httpStatus", "httpBody", "httpHeaders".
func WithOutputKeys(status, body, headers string) Option {
	return func(h *httpCall) { h.statusKey, h.bodyOutKey, h.hdrOutKey = status, body, headers }
}

// WithMaxResponseSize bounds the response body (and any buffered request body)
// read into memory, to n bytes. A non-positive n disables the bound. The
// default is 10 MiB. A response exceeding the bound fails with a non-retryable
// [ErrBodyTooLarge].
func WithMaxResponseSize(n int64) Option { return func(h *httpCall) { h.maxResponseSize = n } }

// readAllCapped reads r fully, but errors with [ErrBodyTooLarge] once more than
// max bytes are available. A non-positive max disables the bound.
func readAllCapped(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		return io.ReadAll(r)
	}
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrBodyTooLarge
	}
	return b, nil
}

// NewHTTPCall returns a service action that performs one HTTP request per Do,
// mapping the response status, body, and headers into output variables. See the
// package doc for the retry classification and response size limit.
func NewHTTPCall(opts ...Option) action.Action {
	h := &httpCall{
		headers:             http.Header{},
		statusKey:           "httpStatus",
		bodyOutKey:          "httpBody",
		hdrOutKey:           "httpHeaders",
		maxResponseSize:     defaultMaxResponseSize,
		maxIdleConnsPerHost: defaultMaxIdleConnsPerHost,
		maxConnsPerHost:     defaultMaxConnsPerHost,
	}
	for _, o := range opts {
		o(h)
	}
	// Built AFTER the options so the pool sizes they set are honoured, and only
	// when no client was supplied: a consumer's own client keeps its own
	// Transport untouched.
	if h.client == nil {
		h.client = &http.Client{
			Timeout:       30 * time.Second,
			Transport:     newPooledTransport(h.maxIdleConnsPerHost, h.maxConnsPerHost),
			CheckRedirect: refuseCrossHostRedirect,
		}
	}
	return h
}

// newPooledTransport builds the default client's transport.
//
// It constructs a transport explicitly rather than cloning
// [http.DefaultTransport], for two reasons. First, DefaultTransport is a mutable
// package variable and wrapping it is a mainstream pattern
// (`http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)`, and
// every APM agent and replay harness that does the same). A type assertion back
// to *http.Transport panics in any such process, and it would panic during
// wiring at startup, where a consumer has no error to handle. Second, inheriting
// whatever a global has been mutated into makes this package's behaviour depend
// on unrelated code having run first.
//
// The field values below mirror net/http's own DefaultTransport, so a caller
// gets the same proxy, dial and timeout behaviour they would have got before —
// this is a pool change, not a replacement of transport semantics.
func newPooledTransport(maxIdleConnsPerHost, maxConnsPerHost int) *http.Transport {
	// Both values are normalised to 0 rather than passed through, so the
	// documented "a non-positive value means the default" holds by construction.
	// http.Transport does not treat the two the same way on its own:
	// maxIdleConnsPerHost() falls back to the default only on exactly 0 and
	// would carry a negative through, whereas MaxConnsPerHost already reads any
	// value <= 0 as unlimited. Normalising here makes the contract this
	// package's, not a detail of the standard library's internals.
	if maxIdleConnsPerHost < 0 {
		maxIdleConnsPerHost = 0
	}
	if maxConnsPerHost < 0 {
		maxConnsPerHost = 0
	}

	// MaxIdleConns is the PROCESS-wide idle cap and would otherwise clamp the
	// per-host one: a caller asking for 200 idle connections per host against
	// net/http's default of 100 would silently keep 100. Raising it in step
	// means the per-host option delivers what it says.
	maxIdleConns := defaultMaxIdleConns
	if maxIdleConnsPerHost > maxIdleConns {
		maxIdleConns = maxIdleConnsPerHost
	}

	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		MaxConnsPerHost:       maxConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// Do resolves the request URL, builds and (optionally) validates the request
// body, sends the request, and maps the response into the output keys. The
// response body — and any buffered request body — is capped at maxResponseSize;
// exceeding it returns a non-retryable [ErrBodyTooLarge]. Retry classification:
// 4xx except 408/429 are non-retryable; 5xx, 408, 429, and transport errors are
// retryable.
func (h *httpCall) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	// 1. Resolve request URL: urlExpr takes precedence over baseURL.
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

	// 2. Determine HTTP method (updated: POST when EITHER body source is configured).
	method := h.method
	if method == "" {
		if h.bodyFunc != nil || h.bodyKey != "" {
			method = http.MethodPost
		} else {
			method = http.MethodGet
		}
	}

	// 3. Build request body. BodyFunc takes precedence over bodyKey.
	//    Track whether we're on the bodyKey/auto-JSON path so we know
	//    whether to auto-set Content-Type later.
	var bodyReader io.Reader
	var bodyBytes []byte      // set when we have raw bytes (bodyKey path or buffered BodyFunc)
	usingBodyKeyPath := false // true → auto-set CT: application/json when unset after header funcs

	if h.bodyFunc != nil {
		// BodyFunc path: call the func, handle errors.
		r, err := h.bodyFunc(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("workflow-httpcall: build body: %w", err)
		}
		if r != nil {
			if h.bodyValidator != nil {
				// Buffer so the validator can inspect the bytes, then re-wrap for sending.
				// A non-nil reader producing zero bytes is still "body present" — the
				// validator must see it (e.g. to enforce non-empty / required-field rules).
				raw, err := readAllCapped(r, h.maxResponseSize)
				if err != nil {
					if errors.Is(err, ErrBodyTooLarge) {
						return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: request body: %w", err))
					}
					return nil, fmt.Errorf("workflow-httpcall: read body: %w", err)
				}
				bodyBytes = raw // non-nil (may be empty) — signals body was produced
				bodyReader = bytes.NewReader(raw)
			} else {
				// No validator: stream directly — no buffering.
				bodyReader = r
			}
		}
	} else if h.bodyKey != "" {
		// bodyKey path: JSON-encode the input variable.
		if v, ok := in[h.bodyKey]; ok {
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: encode body: %w", err))
			}
			bodyBytes = raw
			bodyReader = bytes.NewReader(raw)
			usingBodyKeyPath = true
		}
	}

	// 4. Validate body before sending (only when body is present and validator set).
	// Gate on bodyBytes != nil, not len > 0: a non-nil BodyFunc returning zero bytes
	// is "body present" and the validator must run (e.g. to catch empty-body errors).
	// A nil bodyBytes means no body source was configured at all — skip validation.
	if h.bodyValidator != nil && bodyBytes != nil {
		if err := h.bodyValidator(ctx, bodyBytes, in); err != nil {
			return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: body validation: %w", err))
		}
	}

	// 5. Build the request.
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: build request: %w", err))
	}

	// 6. Apply static headers first, then header funcs (funcs can override statics).
	for k, vs := range h.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for _, fn := range h.headerFuncs {
		if err := fn(ctx, req.Header, in); err != nil {
			return nil, fmt.Errorf("workflow-httpcall: set headers: %w", err)
		}
	}

	// 7. Auto-set Content-Type: application/json ONLY for the bodyKey path and only
	//    if the header is still unset after all static headers and header funcs.
	if usingBodyKeyPath && bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// 8. Send, map response (unchanged).
	resp, err := h.client.Do(req)
	if err != nil {
		// ⚠ A refused cross-host redirect is NOT transport trouble: it fails
		// identically on every attempt, so retrying it only multiplies the
		// requests made to the redirecting host. Classified before the general
		// case below, which treats any client.Do failure as retryable.
		//
		// http.Client wraps a CheckRedirect error in *url.Error, which unwraps,
		// so errors.Is reaches the sentinel.
		if errors.Is(err, ErrCrossHostRedirect) {
			return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: request failed: %w", err))
		}
		// Transport/timeout error — retryable (plain error).
		return nil, fmt.Errorf("workflow-httpcall: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := readAllCapped(resp.Body, h.maxResponseSize)
	if err != nil {
		if errors.Is(err, ErrBodyTooLarge) {
			return nil, action.NonRetryable(fmt.Errorf("workflow-httpcall: response body: %w", err))
		}
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
