// gin_bodydeadline_test.go — the body-read deadline for the gin adapter, the
// sibling of stdlib/bodydeadline_test.go.
//
// ⚠ gin adds a real risk stdlib does not have: capBody arms the deadline through
// gc.Writer, a gin.ResponseWriter, NOT the raw http.ResponseWriter.
// http.NewResponseController reaches the connection only by walking Unwrap();
// gin's *responseWriter implements it (response_writer.go, gin v1.12.0), and if
// a future gin drops that method SetReadDeadline starts returning
// http.ErrNotSupported and the deadline silently stops being armed. The first
// row below is what detects that — it is not a duplicate of the stdlib test.
package gin_test

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	ginlib "github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// stallResult is what a stalled request produced.
type stallResult struct {
	responded bool
	status    int
	elapsed   time.Duration
}

// postAndStall writes a COMPLETE JSON value under a hugely over-declared
// Content-Length, then STALLS — never writing the remainder, never half-closing,
// so the server's body read can only end by deadline.
//
// ⚠ It must NOT CloseWrite: a half-close ends the read at once with
// io.ErrUnexpectedEOF, which is the aborted-upload case covered in
// gin_bodycap_test.go, not the stall this file is about.
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

// TestStalledBodyIsBoundedByTheReadDeadline pins that gin's capped read cannot
// be held open indefinitely.
//
// FALSIFIER for row 1: remove the SetReadDeadline call from capBody and the row
// hangs for its full patience and reports NO RESPONSE.
//
// FALSIFIER for row 2 (the CONTROL that makes row 1 non-vacuous): with the
// deadline disabled the identical request still hangs, proving row 1's response
// is produced BY the deadline.
func TestStalledBodyIsBoundedByTheReadDeadline(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opts   []httpcore.CustomizeOption[ginlib.IRouter]
		assert func(t *testing.T, got stallResult)
	}

	cases := []testCase{
		{
			// ⚠ Also the gin.ResponseWriter Unwrap() probe described in the file
			// header: if the controller cannot reach the connection this row hangs.
			name: "an armed deadline bounds the hold and answers from the buffered prefix",
			opts: []httpcore.CustomizeOption[ginlib.IRouter]{
				ginadapter.WithMaxBodyBytes(1 << 20),
				ginadapter.WithBodyReadTimeout(250 * time.Millisecond),
			},
			assert: func(t *testing.T, got stallResult) {
				t.Helper()
				require.True(t, got.responded,
					"the handler must not be held open past the deadline (waited %v)", got.elapsed)
				assert.Less(t, got.elapsed, 3*time.Second,
					"the response must arrive on the deadline's timescale, not the client's patience")
				// The prefix holds a COMPLETE value, so the binder succeeds over it.
				assert.Equal(t, http.StatusCreated, got.status)
			},
		},
		{
			name: "an explicitly disabled deadline restores the indefinite hold",
			opts: []httpcore.CustomizeOption[ginlib.IRouter]{
				ginadapter.WithMaxBodyBytes(1 << 20),
				ginadapter.WithBodyReadTimeout(0),
			},
			assert: func(t *testing.T, got stallResult) {
				t.Helper()
				assert.False(t, got.responded,
					"with the deadline disabled the capped read must still block (got %d in %v)",
					got.status, got.elapsed)
			},
		},
		{
			// The deadline is armed ONLY when the cap is active: with the cap off,
			// capBody returns before installing any reader and the binder streams.
			name: "a disabled cap streams and answers immediately, deadline irrelevant",
			opts: []httpcore.CustomizeOption[ginlib.IRouter]{
				ginadapter.WithMaxBodyBytes(0),
				ginadapter.WithBodyReadTimeout(250 * time.Millisecond),
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

			srv, _ := newSrv(t, tc.opts...)
			tc.assert(t, postAndStall(t, srv.Listener.Addr().String(), time.Second))
		})
	}
}

// TestFastRequestIsUnaffectedByTheReadDeadline pins that arming the deadline
// costs a normal, complete, fast request nothing.
func TestFastRequestIsUnaffectedByTheReadDeadline(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timeout time.Duration
		assert  func(t *testing.T, resp httpResp)
	}

	ok201 := func(t *testing.T, resp httpResp) {
		t.Helper()
		require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", resp.Body)
	}

	cases := []testCase{
		{name: "the 30s default leaves a fast request a plain 201", timeout: 30 * time.Second, assert: ok201},
		{name: "a very short deadline still yields 201 when the body is already there", timeout: 50 * time.Millisecond, assert: ok201},
		{name: "a disabled deadline is also a plain 201", timeout: 0, assert: ok201},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newSrv(t,
				ginadapter.WithMaxBodyBytes(1<<20),
				ginadapter.WithBodyReadTimeout(tc.timeout),
			)
			tc.assert(t, postRaw(t, srv.URL+"/instances",
				`{"def_ref":"greeting","vars":{"name":"world"}}`))
		})
	}
}
