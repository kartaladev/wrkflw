// Package stdlib_test — the inbound request-body cap.
//
// These tests are deliberately separate TestXxx functions rather than one table,
// even though several POST to /instances: each carries a distinct FALSIFIER (the
// mechanism it rules out) and their setups diverge structurally — one needs a
// real TCP server to make the body reader fail mid-read, one mounts with a
// different CustomizeOption, and two mount the admin group instead of Mount.
// Cases that DO share a setup are tabled inside their function.
package stdlib_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// bigPad returns a JSON-safe filler string of n bytes.
func bigPad(n int) string { return strings.Repeat("p", n) }

// newRawPostRequest posts raw (possibly malformed) bytes, bypassing json.Marshal
// so a case can craft trailing bytes and truncated values.
func newRawPostRequest(t *testing.T, path, raw string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, path, strings.NewReader(raw))
	require.NoError(t, err)
	r.Header.Set("Content-Type", "application/json")
	return r.WithContext(t.Context())
}

// cleanStartBody is a well-formed POST /instances payload for LinearProcess.
const cleanStartBody = `{"def_ref":"greeting","vars":{"name":"ada"}}`

// testCap is the small cap these tests mount with. It is deliberately LARGER
// than cleanStartBody (44 bytes) so a body can be "a complete value the parser
// would accept, followed by bytes that push the READ over the cap".
const testCap = 64

// wantStatic413 asserts the oversize response is the static 413 envelope and
// NOT the 400 envelope — i.e. the sentinel reached ClassifyError BARE. Wrapping
// ErrRequestBodyTooLarge in ErrBadInput is silently absorbed by the ordered
// switch, which then answers "bad_request".
func wantStatic413(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusRequestEntityTooLarge, rr.Code, "body=%s", rr.Body)
	var errBody map[string]any
	decodeJSON(t, rr.Body, &errBody)
	assert.Equal(t, "request_too_large", errBody["error"])
	assert.Equal(t, "request body exceeds the configured limit", errBody["message"])
}

// TestOverCapIsRejectedInEveryBodyShape pins that the cap bounds the READ, not
// the PARSE: whatever shape the oversize body has, it is refused with 413.
//
// FALSIFIER (row "complete value then over-cap trailing bytes"): fails against
// any implementation that caps during the parse — e.g.
// json.NewDecoder(http.MaxBytesReader(...)).Decode(&in). MEASURED: json.Decoder
// scans its buffer for a complete value BEFORE consulting the read error, so a
// value that fits inside the first capped chunk decodes successfully and the
// oversize bytes behind it are never noticed. Today that row answers 201.
func TestOverCapIsRejectedInEveryBodyShape(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		raw    string
		assert func(t *testing.T, rr *httptest.ResponseRecorder)
	}

	cases := []testCase{
		{
			name:   "well-formed body over the cap",
			raw:    `{"def_ref":"greeting","vars":{"name":"` + bigPad(500) + `"}}`,
			assert: wantStatic413,
		},
		{
			name:   "over-cap body with a syntax error at byte 3",
			raw:    `{"X` + bigPad(500),
			assert: wantStatic413,
		},
		{
			// The row that discriminates read-capping from parse-capping.
			name:   "complete JSON value then over-cap trailing bytes",
			raw:    cleanStartBody + bigPad(500),
			assert: wantStatic413,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Greater(t, len(tc.raw), testCap, "fixture must exceed the cap")

			def := transporttest.LinearProcess()
			_, svc := transporttest.NewHarness(t, def)

			mux := http.NewServeMux()
			stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(testCap))

			tc.assert(t, do(mux, newRawPostRequest(t, "/instances", tc.raw)))
		})
	}
}

// TestUnderCapBehaviourIsUnchanged is the CONTROL that decides the mechanism:
// with the cap active but the body under it, responses must be byte-for-byte
// what they are today.
//
// TODAY (measured on the unmodified decode path, before any cap existed):
//
//	clean body                       -> 201, variables.greeting = "hi ada"
//	clean + trailing "GARBAGE"       -> 201, variables.greeting = "hi ada"
//	clean + trailing `{"x":1}`       -> 201, variables.greeting = "hi ada"
//
// json.Decoder.Decode reads ONE value and stops; it never looks at what follows.
//
// FALSIFIER: fails against an implementation that substitutes json.Unmarshal for
// the decoder when reading the buffered body — Unmarshal rejects trailing data
// ("invalid character 'G' after top-level value"), turning today's 201 into a
// 400. That is a wire break on every under-cap body with trailing bytes.
func TestUnderCapBehaviourIsUnchanged(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		raw    string
		assert func(t *testing.T, rr *httptest.ResponseRecorder)
	}

	// Every case must land on today's recorded outcome.
	wantTodaysSuccess := func(t *testing.T, rr *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body)
		var resp map[string]any
		decodeJSON(t, rr.Body, &resp)
		assert.NotEmpty(t, resp["instance_id"])
		assert.Equal(t, "greeting", resp["def_id"])
		assert.Equal(t, "completed", resp["status"])
		vars, ok := resp["variables"].(map[string]any)
		require.True(t, ok, "variables missing: %v", resp)
		assert.Equal(t, "hi ada", vars["greeting"])
		assert.Equal(t, "ada", vars["name"])
	}

	cases := []testCase{
		{
			name:   "clean body",
			raw:    cleanStartBody,
			assert: wantTodaysSuccess,
		},
		{
			// The row that dies under json.Unmarshal.
			name:   "complete JSON value plus under-cap trailing bytes",
			raw:    cleanStartBody + `GARBAGE{"x":1}`,
			assert: wantTodaysSuccess,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := transporttest.LinearProcess()
			_, svc := transporttest.NewHarness(t, def)

			mux := http.NewServeMux()
			// Cap ACTIVE and far above these bodies: the point is that an active
			// cap changes nothing for a body under it.
			//
			// ⚠ DiscloseAll is required, not incidental. The adapter projects the
			// response for a caller it cannot identify, and this mount has no
			// authentication — so without the opt-out the assertions below would
			// fail on the DISCLOSURE change and stop testing the body cap at all.
			// Opting out keeps this guard discriminating: it still fails if an
			// under-cap body ever renders differently.
			stdlib.Mount(mux, svc,
				stdlib.WithMaxBodyBytes(64<<10),
				httpcore.WithDisclosure[*http.ServeMux](authz.DiscloseAll))

			tc.assert(t, do(mux, newRawPostRequest(t, "/instances", tc.raw)))
		})
	}
}

// resolveIncidentPath is the ONE route whose body is genuinely optional:
// POST /admin/instances/{id}/incidents/{incidentID}/resolve discards its decode
// error by design. Admin routes are kept out of Mount, so these tests
// mount AdminRoutes explicitly.
const resolveIncidentPath = "/admin/instances/no-such/incidents/inc-1/resolve"

// TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute covers the ONE decode
// site that DISCARDS its error. Ignoring the decode error is deliberate (the
// body is optional); ignoring the SIZE is not, and would leave a single route
// reading an unbounded body into memory.
//
// FALSIFIER: fails against an implementation that only edits the 12 propagating
// decode sites. Today that route answers 404 — it read the whole oversize body,
// dropped the error, and called the service.
func TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc}.Customize(mux, stdlib.WithMaxBodyBytes(testCap))

	raw := `{"add_attempts":1,"pad":"` + bigPad(500) + `"}`
	require.Greater(t, len(raw), testCap, "fixture must exceed the cap")

	wantStatic413(t, do(mux, newRawPostRequest(t, resolveIncidentPath, raw)))
}

// TestBodyAbsentOnTheOptionalBodyAdminRoute_StillSucceeds is the CONTROL for
// TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute. Without it, an
// implementation that answers 413 to an ABSENT body passes the oversize test
// and breaks the route for every caller who omits the body.
//
// "Succeeds" here means the handler got PAST the body read and reached the
// service — observable as the service's own 404 for the unknown instance,
// which is what the route answers today for an absent body. A 200 would need a
// live instance carrying a real incident, and there is no Service mock to stub
// one.
func TestBodyAbsentOnTheOptionalBodyAdminRoute_StillSucceeds(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		raw  string
	}

	cases := []testCase{
		{name: "no body at all", raw: ""},
		{name: "whitespace-only body", raw: "  \n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t)

			mux := http.NewServeMux()
			stdlib.AdminRoutes{Svc: svc}.Customize(mux, stdlib.WithMaxBodyBytes(testCap))

			rr := do(mux, newRawPostRequest(t, resolveIncidentPath, tc.raw))

			assert.NotEqual(t, http.StatusRequestEntityTooLarge, rr.Code,
				"an absent body must not be refused as oversize (body=%s)", rr.Body)
			require.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body)
			var errBody map[string]any
			decodeJSON(t, rr.Body, &errBody)
			assert.Equal(t, "not_found", errBody["error"])
		})
	}
}

// TestAbortedUploadIsNotA413 pins that only *http.MaxBytesError means oversize.
//
// ⚠ This test needs a REAL server, not httptest.NewRequest. MEASURED: setting
// req.ContentLength on an httptest.NewRequest does NOT make the body reader
// enforce it — io.ReadAll returned err=nil and the case was vacuous. Only the
// net/http server's own body reader errors on a short read, and there it
// yields "unexpected EOF" with errors.As(*http.MaxBytesError) FALSE.
//
// FALSIFIER: fails against an implementation that treats any read error as
// oversize — that ships every aborted upload as a 413.
func TestAbortedUploadIsNotA413(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(testCap))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))

	// Declare far more than we send — and stay UNDER the cap, so the only way
	// to reach 413 is by misclassifying the short read.
	const sent = `{"def_ref":"gree`
	_, err = fmt.Fprintf(conn,
		"POST /instances HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n"+
			"Content-Length: 40\r\n\r\n%s", sent)
	require.NoError(t, err)
	require.Less(t, len(sent), testCap, "the truncated body must be UNDER the cap")
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.NotEqual(t, http.StatusRequestEntityTooLarge, resp.StatusCode,
		"an aborted upload must not be reported as oversize (body=%s)", raw)
	assert.NotContains(t, string(raw), "request_too_large")
	// It is the same 400 the unmodified decode path produced for a truncated body.
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%s", raw)
}

// TestDisabledCapDoesNotInstallTheReader pins that WithMaxBodyBytes(0) disables
// the cap entirely rather than passing 0 down to http.MaxBytesReader.
//
// FALSIFIER: fails against an implementation that passes 0 to MaxBytesReader.
// MEASURED: http.MaxBytesReader(w, body, 0) rejects EVERY non-empty body —
// io.ReadAll returns "http: request body too large" with errors.As true — so a
// naive wrap would turn "cap disabled" into "no body accepted at all".
func TestDisabledCapDoesNotInstallTheReader(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(0))

	// Far over the 1 MiB default, so this can only pass with the cap disabled.
	req := newPostRequest(t, "/instances", map[string]any{
		"def_ref": "greeting",
		"vars":    map[string]any{"name": "ada", "pad": bigPad(1500000)},
	})
	rr := do(mux, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body)
	assert.NotContains(t, rr.Body.String(), "request_too_large")
}
