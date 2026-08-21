// gin_bodycap_test.go — inbound request-body cap (ADR-0186 phase 2) for the gin
// adapter: the 413 arm, the under-cap control that pins the binder's behaviour
// byte-for-byte, and the disabled-cap escape.
//
// BASELINE, measured against the unmodified decode path on POST /instances
// before this file existed (throwaway probe, 2026-08-21):
//
//	clean 46 B                     201 + instance JSON
//	clean + 10 trailing "X"        201 + instance JSON   ← the parse stops at the value
//	`{"a":1}{"a":1}` second value  201 + instance JSON
//	`{"xbroken`                    400 {"error":"bad_request","message":"workflow-httpcore: bad input: unexpected EOF"}
//	`{"def_ref":123}`              400 …"json: cannot unmarshal number into Go struct field StartInput.def_ref of type string"
//	empty                          400 …"EOF"
//	`not-json`                     400 …"invalid character 'o' in literal null (expecting 'u')"
//	2 MiB clean                    201 + instance JSON
//	aborted upload (CL=1000,       400 {"error":"bad_request","message":"workflow-httpcore: bad input: unexpected EOF"}
//	  17 B written, half-close)
package gin_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ginlib "github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/service"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// postRaw sends raw bytes as the request body, bypassing json.Marshal so a test
// can send a malformed or deliberately oversized document.
func postRaw(t *testing.T, url, raw string) httpResp {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer drainClose(resp)
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	return httpResp{StatusCode: resp.StatusCode, Body: body.Bytes()}
}

// body413 is the static envelope ClassifyError emits for ErrRequestBodyTooLarge.
const body413 = `{"error":"request_too_large","message":"request body exceeds the configured limit"}`

func TestOverCapIsRejectedInEveryBodyShape(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		raw    string
		assert func(t *testing.T, resp httpResp)
	}

	is413 := func(t *testing.T, resp httpResp) {
		t.Helper()
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
		assert.JSONEq(t, body413, string(resp.Body))
	}

	cases := []testCase{
		{
			// 46 bytes, well past the 32-byte cap, and valid all the way through.
			name:   "well-formed body over the cap",
			raw:    `{"def_ref":"greeting","vars":{"name":"world"}}`,
			assert: is413,
		},
		{
			// Syntax breaks at byte 3, but the request is still oversize: the cap
			// must be decided before the document is judged.
			name:   "syntax error at byte 3 and over the cap",
			raw:    `{"xbroken` + strings.Repeat("X", 30),
			assert: is413,
		},
		{
			// ⚠ THE ROW THAT DECIDES THE MECHANISM. A complete JSON value inside the
			// cap followed by trailing junk that pushes the request over it. Today
			// this is 201 (measured, see the baseline above) because the decoder
			// stops at the closing brace and never sees the trailing bytes. It also
			// fails against any implementation that caps DURING the parse instead of
			// during the read, for exactly the same reason.
			name:   "complete JSON value then over-cap trailing bytes",
			raw:    `{"def_ref":"greeting"}` + strings.Repeat("X", 20),
			assert: is413,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newSrv(t, ginadapter.WithMaxBodyBytes(32))
			tc.assert(t, postRaw(t, srv.URL+"/instances", tc.raw))
		})
	}
}

// postOverDeclared writes a request by hand onto a raw socket, declaring
// contentLength but sending only sent, then half-closing so the server's body
// read dies short. It returns the response's status line and its body.
//
// ⚠ It has to be a raw socket: the net/http client refuses to send a request
// whose declared Content-Length exceeds the bytes it actually writes
// ("ContentLength=1000 with Body length 17"), and httptest.NewRequest with a
// hand-set ContentLength does not make the reader enforce it at all (io.ReadAll
// returns err=nil there, which makes such a test vacuous).
func postOverDeclared(t *testing.T, addr, sent string, contentLength int) (head, body string) {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck

	_, err = fmt.Fprintf(conn, "POST /instances HTTP/1.1\r\nHost: x\r\n"+
		"Content-Type: application/json\r\nContent-Length: %d\r\n"+
		"Connection: close\r\n\r\n%s", contentLength, sent)
	require.NoError(t, err)
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))

	raw, err := io.ReadAll(conn)
	require.NoError(t, err)

	head, body, ok := strings.Cut(string(raw), "\r\n\r\n")
	require.True(t, ok, "malformed response: %q", raw)
	return head, body
}

// TestAbortedUploadIsNotA413 is a raw-socket table rather than rows in the
// table above because its setup is structurally different — see postOverDeclared.
//
// Both rows send FEWER bytes than they declare, so io.ReadAll over the capped
// reader yields io.ErrUnexpectedEOF with errors.As(err, new(*http.MaxBytesError))
// FALSE. They differ only in whether a COMPLETE JSON value arrived before the
// abort, and that difference is the whole point: the outcome must be decided by
// the decoder over the buffered prefix, never by the read error itself.
//
// FALSIFIER for row 1: an implementation classifying any read error as oversize
// ships every aborted upload as a 413.
//
// FALSIFIER for row 2: an implementation that returns ANY error for a non-
// MaxBytesError read failure — which is what gin's `default:` arm did, MEASURED
// 2026-08-21: gin+cap answered 400 "unexpected EOF" where stdlib+cap and
// gin-with-the-cap-disabled both answered 201 Created for the byte-identical
// request. That is a wire break introduced by the cap on a request that fits it.
func TestAbortedUploadIsNotA413(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		sent          string
		contentLength int
		assert        func(t *testing.T, head, body string)
	}

	cases := []testCase{
		{
			// 1000 declared, 17 written: the abort lands INSIDE the value, so the
			// decoder over the prefix legitimately reports "unexpected EOF".
			name:          "truncated mid-value is a 400, not a 413",
			sent:          `{"def_ref":"greet`,
			contentLength: 1000,
			assert: func(t *testing.T, head, body string) {
				t.Helper()
				assert.NotContains(t, head, "413", "an aborted upload must not be classified as oversize")
				assert.Contains(t, head, "HTTP/1.1 400 Bad Request")
				assert.JSONEq(t,
					`{"error":"bad_request","message":"workflow-httpcore: bad input: unexpected EOF"}`,
					body)
			},
		},
		{
			// ⚠ THE ROW THAT PINS PARITY WITH stdlib. 400 declared, a COMPLETE
			// 45-byte value written, then half-close. The value the caller sent is
			// entirely present in the buffered prefix, so the decoder succeeds and
			// the instance starts — exactly as it does with the cap disabled and
			// exactly as stdlib answers.
			name:          "truncated AFTER a complete value still starts the instance",
			sent:          `{"def_ref":"greeting","vars":{"name":"world"}}`,
			contentLength: 400,
			assert: func(t *testing.T, head, body string) {
				t.Helper()
				require.Contains(t, head, "HTTP/1.1 201 Created", "body=%s", body)
				assert.Contains(t, body, `"def_id":"greeting"`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newSrv(t, ginadapter.WithMaxBodyBytes(4096))
			head, body := postOverDeclared(t, srv.Listener.Addr().String(), tc.sent, tc.contentLength)
			tc.assert(t, head, body)
		})
	}
}

// recordingAdminSvc counts ResolveIncident calls and reports the AddAttempts it
// was handed, so a case can assert not only the status but whether the handler
// reached the service at all.
type recordingAdminSvc struct {
	service.Service
	calls       atomic.Int64
	addAttempts atomic.Int64
}

func (s *recordingAdminSvc) ResolveIncident(_ context.Context, req service.ResolveIncidentRequest) (service.ProcessInstance, error) {
	s.calls.Add(1)
	s.addAttempts.Store(int64(req.AddAttempts))
	return service.NewProcessInstance(nil, engine.InstanceState{InstanceID: "inst-1"}), nil
}

// TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute covers the ONE
// discarding decode site — POST /admin/instances/{id}/incidents/{incidentID}/resolve,
// whose body is genuinely optional and whose decode error is deliberately
// ignored. It must still refuse an oversize body, so an implementation that only
// edits the twelve propagating sites fails the first row.
//
// ⚠ Admin routes are kept out of Mount by ADR-0095 (admin-by-composition), so
// this route is named explicitly here rather than reached through newSrv.
//
// The second row is the control: the absent body that the discarding site exists
// to serve must still flow through to the service.
func TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		raw    string
		assert func(t *testing.T, resp httpResp, svc *recordingAdminSvc)
	}

	cases := []testCase{
		{
			// 18 bytes of valid, decodable body plus 40 bytes of junk: today the
			// decoder stops at the closing brace and the request is served 200.
			name: "oversized body is refused and never reaches the service",
			raw:  `{"add_attempts":1}` + strings.Repeat("X", 40),
			assert: func(t *testing.T, resp httpResp, svc *recordingAdminSvc) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
				assert.JSONEq(t, body413, string(resp.Body))
				assert.Zero(t, svc.calls.Load(), "413 must short-circuit before ResolveIncident")
			},
		},
		{
			name: "body absent on the optional route still succeeds",
			raw:  "",
			assert: func(t *testing.T, resp httpResp, svc *recordingAdminSvc) {
				require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", resp.Body)
				assert.Equal(t, int64(1), svc.calls.Load())
				assert.Zero(t, svc.addAttempts.Load(), "an absent body leaves the input zero")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := &recordingAdminSvc{}
			r := ginlib.New()
			ginadapter.AdminRoutes{Svc: svc}.Customize(r, ginadapter.WithMaxBodyBytes(32))
			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)

			resp := postRaw(t, srv.URL+"/admin/instances/inst-1/incidents/inc-1/resolve", tc.raw)
			tc.assert(t, resp, svc)
		})
	}
}

func TestUnderCapBehaviourIsUnchanged(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		raw    string
		assert func(t *testing.T, resp httpResp)
	}

	// Both rows are the BASELINE recorded at the top of this file against the
	// unmodified path, re-asserted through the capped one. A body under the cap
	// must behave byte-for-byte as it did before the cap existed.
	startedOK := func(t *testing.T, resp httpResp) {
		t.Helper()
		require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", resp.Body)
		var got map[string]any
		decodeJSON(t, resp, &got)
		assert.NotEmpty(t, got["instance_id"])
		assert.Equal(t, "greeting", got["def_id"])
	}

	cases := []testCase{
		{
			name:   "clean body",
			raw:    `{"def_ref":"greeting","vars":{"name":"world"}}`,
			assert: startedOK,
		},
		{
			// ⚠ THE FALSIFIER FOR THE MECHANISM. gin binds with a json.Decoder, which
			// is lenient about anything after the first complete value; measured
			// today, this body starts an instance and answers 201. Swapping
			// ShouldBindJSON for json.Unmarshal — strict about trailing data — turns
			// this row into a 400, which is a wire break on every such request that
			// is comfortably under the cap and was always accepted.
			name:   "complete JSON value then UNDER-cap trailing bytes",
			raw:    `{"def_ref":"greeting","vars":{"name":"world"}}XXXXXXXXXX`,
			assert: startedOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// 4 KiB: every row above is far under it, so the cap is installed but
			// never trips — which is precisely the path being pinned.
			srv, _ := newSrv(t, ginadapter.WithMaxBodyBytes(4096))
			tc.assert(t, postRaw(t, srv.URL+"/instances", tc.raw))
		})
	}
}

// TestDisabledCapDoesNotInstallTheReader pins the n <= 0 escape hatch.
//
// ⚠ THE FALSIFIER: http.MaxBytesReader(w, r, 0) rejects EVERY non-empty body —
// measured, io.ReadAll over it returns 0 bytes and "http: request body too
// large". So an implementation that installs the reader unconditionally and
// hands it a zero limit turns the documented "disabled" setting into "refuse
// everything", and the first row below catches it.
func TestDisabledCapDoesNotInstallTheReader(t *testing.T) {
	t.Parallel()

	// 2 MiB of payload — comfortably over the 1 MiB ResolveConfig default.
	overDefault := `{"def_ref":"greeting","vars":{"name":"` + strings.Repeat("a", 2<<20) + `"}}`

	type testCase struct {
		name   string
		opts   []httpcore.CustomizeOption[ginlib.IRouter]
		assert func(t *testing.T, resp httpResp)
	}

	cases := []testCase{
		{
			name: "explicit zero admits a body over the default",
			opts: []httpcore.CustomizeOption[ginlib.IRouter]{ginadapter.WithMaxBodyBytes(0)},
			assert: func(t *testing.T, resp httpResp) {
				require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", resp.Body)
			},
		},
		{
			name: "explicit negative admits a body over the default",
			opts: []httpcore.CustomizeOption[ginlib.IRouter]{ginadapter.WithMaxBodyBytes(-1)},
			assert: func(t *testing.T, resp httpResp) {
				require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", resp.Body)
			},
		},
		{
			// The control: without the option, the very same body is refused. This is
			// what makes the two rows above non-vacuous — and it pins that
			// ResolveConfig's 1 MiB default actually reaches the gin handlers.
			name: "no option at all refuses the same body at the 1 MiB default",
			opts: nil,
			assert: func(t *testing.T, resp httpResp) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
				assert.JSONEq(t, body413, string(resp.Body))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newSrv(t, tc.opts...)
			tc.assert(t, postRaw(t, srv.URL+"/instances", overDefault))
		})
	}
}

// TestBinderSurvivesTheBufferSwap pins the BINDER, not gin's validator: reading
// the body into a buffer and reassigning gc.Request.Body must leave
// ShouldBindJSON producing byte-identical outcomes for every body shape.
//
// Each want* below is the response MEASURED against the unmodified path before
// the cap existed (see the baseline at the top of this file).
//
// ⚠ Deliberately NOT a `binding:` tag test. The repo declares zero binding tags,
// so gin's validator never engages on these DTOs and such a test could not fail.
// The decoder is the surface the buffer swap actually moves.
func TestBinderSurvivesTheBufferSwap(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		raw    string
		assert func(t *testing.T, resp httpResp)
	}

	badInput := func(status int, message string) func(*testing.T, httpResp) {
		return func(t *testing.T, resp httpResp) {
			t.Helper()
			assert.Equal(t, status, resp.StatusCode)
			assert.JSONEq(t, `{"error":"bad_request","message":`+strconvQuote(message)+`}`, string(resp.Body))
		}
	}

	cases := []testCase{
		{
			name: "valid",
			raw:  `{"def_ref":"greeting","vars":{"name":"world"}}`,
			assert: func(t *testing.T, resp httpResp) {
				require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", resp.Body)
			},
		},
		{
			name: "trailing bytes after a complete value",
			raw:  `{"def_ref":"greeting","vars":{"name":"world"}}XXXXXXXXXX`,
			assert: func(t *testing.T, resp httpResp) {
				require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", resp.Body)
			},
		},
		{
			name:   "malformed",
			raw:    `not-json`,
			assert: badInput(http.StatusBadRequest, "workflow-httpcore: bad input: invalid character 'o' in literal null (expecting 'u')"),
		},
		{
			name:   "type mismatch",
			raw:    `{"def_ref":123}`,
			assert: badInput(http.StatusBadRequest, "workflow-httpcore: bad input: json: cannot unmarshal number into Go struct field StartInput.def_ref of type string"),
		},
		{
			name:   "truncated mid-value",
			raw:    `{"xbroken`,
			assert: badInput(http.StatusBadRequest, "workflow-httpcore: bad input: unexpected EOF"),
		},
		{
			name:   "empty",
			raw:    ``,
			assert: badInput(http.StatusBadRequest, "workflow-httpcore: bad input: EOF"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// 4 KiB: the reader and the buffer swap are installed for every row, and
			// no row is anywhere near tripping them.
			srv, _ := newSrv(t, ginadapter.WithMaxBodyBytes(4096))
			tc.assert(t, postRaw(t, srv.URL+"/instances", tc.raw))
		})
	}
}

// strconvQuote is strconv.Quote, named locally so the JSON envelope literals
// above read as one line.
func strconvQuote(s string) string { return strconv.Quote(s) }
