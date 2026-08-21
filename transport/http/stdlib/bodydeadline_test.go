// bodydeadline_test.go — the body-read deadline that bounds the CAPPED read.
//
// WHY IT EXISTS. The cap reads the body to completion before parsing; the
// uncapped path let json.Decoder stop at the end of the first complete value and
// return. That turns a fast-returning request into an indefinite handler hold,
// and the cap is ON BY DEFAULT.
//
// BASELINE, measured against a real http.Server on POST /instances with
// Content-Length 400000, a complete 41-byte value written, then a stall — no
// further bytes, no half-close (throwaway probe, 2026-08-21):
//
//	cap DISABLED (WithMaxBodyBytes(0))   201 Created in 0s
//	cap ENABLED  (the 1 MiB default)     NO RESPONSE after 3s, goroutine held
package stdlib_test

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// stallResult is what a stalled request produced: the response if one arrived
// within the caller's patience, and how long it took.
type stallResult struct {
	responded bool
	status    int
	elapsed   time.Duration
}

// postAndStall writes a COMPLETE JSON value under a hugely over-declared
// Content-Length, then STALLS — it never writes the promised remainder and never
// half-closes, so the server's body read can only end by deadline. It waits at
// most patience for a response.
//
// ⚠ This needs httptest.NewServer + a raw net.Dial. httptest.NewRequest with a
// hand-set ContentLength does NOT make the body reader enforce the declaration —
// measured, io.ReadAll returns err=nil there — so a test built on it passes
// against the broken implementation too. That exact vacuity was found earlier in
// this delivery.
//
// ⚠ It also must not CloseWrite: a half-close ends the read immediately with
// io.ErrUnexpectedEOF, which is the ABORTED-UPLOAD case, not the stall case. The
// hold is the whole point here.
func postAndStall(t *testing.T, addr string, patience time.Duration) stallResult {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	const complete = `{"def_ref":"greeting","vars":{"name":"x"}}`
	_, err = fmt.Fprintf(conn, "POST /instances HTTP/1.1\r\nHost: x\r\n"+
		"Content-Type: application/json\r\nContent-Length: 400000\r\n"+
		"Connection: close\r\n\r\n%s", complete)
	require.NoError(t, err)

	start := time.Now()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(patience)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	elapsed := time.Since(start)
	if err != nil {
		return stallResult{responded: false, elapsed: elapsed}
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return stallResult{responded: true, status: resp.StatusCode, elapsed: elapsed}
}

func newStallSrv(t *testing.T, opts ...httpcore.CustomizeOption[*http.ServeMux]) *httptest.Server {
	t.Helper()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc, opts...)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestStalledBodyIsBoundedByTheReadDeadline pins that the capped read cannot be
// held open indefinitely, and that the bound is opt-out-able.
//
// FALSIFIER for the first row: remove the SetReadDeadline call from
// requestBodyReader and the row hangs until its patience expires and reports NO
// RESPONSE — exactly the baseline at the top of this file.
//
// FALSIFIER for the "disabled" row: it is the CONTROL that makes the first row
// non-vacuous. It pins that the response in row 1 is produced BY the deadline
// and not by something else finishing the read — with the deadline off, the very
// same request still hangs.
func TestStalledBodyIsBoundedByTheReadDeadline(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opts   []httpcore.CustomizeOption[*http.ServeMux]
		assert func(t *testing.T, got stallResult)
	}

	cases := []testCase{
		{
			// 250ms deadline against 1s of client patience: a response can only
			// arrive because the deadline fired, and it must arrive well inside
			// that second. The buffered prefix holds a COMPLETE value, so the
			// decoder succeeds over it and the instance starts — the deadline
			// bounds the hold, it does not manufacture a failure.
			name: "an armed deadline bounds the hold and answers from the buffered prefix",
			opts: []httpcore.CustomizeOption[*http.ServeMux]{
				stdlib.WithMaxBodyBytes(1 << 20),
				stdlib.WithBodyReadTimeout(250 * time.Millisecond),
			},
			assert: func(t *testing.T, got stallResult) {
				t.Helper()
				require.True(t, got.responded,
					"the handler must not be held open past the deadline (waited %v)", got.elapsed)
				assert.Less(t, got.elapsed, 3*time.Second,
					"the response must arrive on the deadline's timescale, not the client's patience")
				assert.Equal(t, http.StatusCreated, got.status)
			},
		},
		{
			// ⚠ THE CONTROL. d <= 0 opts out, and the pre-deadline behaviour —
			// the indefinite hold measured in the baseline — is what returns.
			name: "an explicitly disabled deadline restores the indefinite hold",
			opts: []httpcore.CustomizeOption[*http.ServeMux]{
				stdlib.WithMaxBodyBytes(1 << 20),
				stdlib.WithBodyReadTimeout(0),
			},
			assert: func(t *testing.T, got stallResult) {
				t.Helper()
				assert.False(t, got.responded,
					"with the deadline disabled the capped read must still block (got %d in %v)",
					got.status, got.elapsed)
			},
		},
		{
			// ⚠ The deadline is armed ONLY when the cap is active. With the cap
			// disabled no read wrapper is installed, json.Decoder streams and stops
			// at the end of the first complete value, and the request answers
			// immediately — the pre-cap behaviour, untouched.
			name: "a disabled cap streams and answers immediately, deadline irrelevant",
			opts: []httpcore.CustomizeOption[*http.ServeMux]{
				stdlib.WithMaxBodyBytes(0),
				stdlib.WithBodyReadTimeout(250 * time.Millisecond),
			},
			assert: func(t *testing.T, got stallResult) {
				t.Helper()
				require.True(t, got.responded, "the uncapped path must answer without reading the remainder")
				assert.Equal(t, http.StatusCreated, got.status)
				assert.Less(t, got.elapsed, 250*time.Millisecond,
					"it must answer BEFORE the deadline could have fired, proving it streamed")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newStallSrv(t, tc.opts...)
			tc.assert(t, postAndStall(t, srv.Listener.Addr().String(), time.Second))
		})
	}
}

// TestFastRequestIsUnaffectedByTheReadDeadline pins that arming the deadline
// costs a normal, complete, fast request nothing at all.
//
// FALSIFIER: arm the deadline around anything wider than the read — or set it
// from a zero/past time — and even this trivially fast request fails.
func TestFastRequestIsUnaffectedByTheReadDeadline(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timeout time.Duration
		assert  func(t *testing.T, resp *http.Response, err error)
	}

	ok201 := func(t *testing.T, resp *http.Response, err error) {
		t.Helper()
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	cases := []testCase{
		{name: "the 30s default leaves a fast request a plain 201", timeout: 30 * time.Second, assert: ok201},
		{
			// The read completes in microseconds, so a 50ms deadline cannot fire.
			name:    "a very short deadline still yields 201 when the body is already there",
			timeout: 50 * time.Millisecond, assert: ok201,
		},
		{name: "a disabled deadline is also a plain 201", timeout: 0, assert: ok201},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newStallSrv(t,
				stdlib.WithMaxBodyBytes(1<<20),
				stdlib.WithBodyReadTimeout(tc.timeout),
			)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				srv.URL+"/instances",
				strings.NewReader(`{"def_ref":"greeting","vars":{"name":"world"}}`))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if resp != nil {
				t.Cleanup(func() { _ = resp.Body.Close() })
			}
			tc.assert(t, resp, err)
		})
	}
}
