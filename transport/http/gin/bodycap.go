package gin

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"time"

	ginlib "github.com/gin-gonic/gin"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// capBody bounds the inbound request body at cfg.MaxBodyBytes and re-buffers
// what it read so the caller's gc.ShouldBindJSON still sees a complete,
// replayable io.ReadCloser. It returns:
//
//   - [httpcore.ErrRequestBodyTooLarge] — BARE, never wrapped — when the body
//     exceeded the cap;
//   - nil in EVERY other case: the cap disabled (n <= 0), no body to read, the
//     whole body fitted, or the read failed for a reason that is not oversize.
//
// ⚠ The cap is enforced on the READ, not on the PARSE. gin's ShouldBindJSON
// decodes with a json.Decoder, which stops at the end of the first complete
// value and never touches trailing bytes — so a parse-side bound would admit
// `{"a":1}` + 10 MiB of junk. Reading first is what makes the bound total.
//
// ⚠ The sentinel is returned BARE — but NOT because wrapping would misclassify.
// MEASURED (mutation, 2026-08-21): returning
// fmt.Errorf("%w: %w", httpcore.ErrBadInput, httpcore.ErrRequestBodyTooLarge)
// still answers 413 and every test here stays green, because httpcore hoisted
// the 413 arm above the 400 arm precisely so a wrapped sentinel resolves the
// right way. Bare is the choice anyway: it makes the classification independent
// of that arm ordering instead of load-bearing on it. Nothing in this package
// can detect the difference, so do not expect a gin test to catch a regression
// here — httpcore's own arm-ordering test is what pins it.
//
// ⚠ "The read failed" is NOT the oversize test. An over-declared Content-Length
// whose body ends early surfaces as io.ErrUnexpectedEOF with errors.As against
// *http.MaxBytesError false; classifying on the error's mere presence would ship
// every aborted upload as a 413.
//
// ⚠ Nor is a non-oversize read failure an error to REPORT. It is dropped and the
// buffered prefix handed to the binder, matching stdlib's requestBodyReader
// exactly. MEASURED (2026-08-21, raw socket, Content-Length 400 with a complete
// 45-byte value then half-close): returning the read error here answered 400
// "unexpected EOF" where stdlib+cap AND gin with the cap DISABLED both answered
// 201 Created for the byte-identical request — a request that fits the cap
// changing status because the cap was switched on. Dropping the error restores
// parity: the truncated-mid-value case still reaches 400 because the DECODER
// reports "unexpected EOF" over the short prefix, which is the same 400 the
// uncapped path produced.
func capBody[R any](cfg httpcore.CustomizeConfig[R], gc *ginlib.Context) error {
	if cfg.MaxBodyBytes <= 0 || gc.Request == nil || gc.Request.Body == nil {
		return nil
	}

	// ⚠ Armed only past the guard above — the cap being active is what creates
	// the blocking read — and cleared once the read is done. See
	// armBodyReadDeadline for why gc.Writer (a gin.ResponseWriter) can reach the
	// connection at all.
	clearDeadline := armBodyReadDeadline(cfg.BodyReadTimeout, gc.Writer)
	defer clearDeadline()

	buf, err := io.ReadAll(http.MaxBytesReader(gc.Writer, gc.Request.Body, cfg.MaxBodyBytes))
	// Re-buffer unconditionally, including the prefix of a truncated upload: the
	// unmodified path also hands the decoder whatever arrived before the failure,
	// and a caller that ignores the error (the optional-body admin route) must see
	// the same bytes it would have seen straight from the wire.
	gc.Request.Body = io.NopCloser(bytes.NewReader(buf))

	if errors.As(err, new(*http.MaxBytesError)) {
		return httpcore.ErrRequestBodyTooLarge
	}
	return nil
}

// armBodyReadDeadline sets a read deadline of d on the connection behind w and
// returns the function that clears it again. d <= 0 arms nothing and returns a
// no-op, matching the disabled convention of MaxBodyBytes.
//
// ⚠ SIBLING: stdlib/body.go holds a byte-identical copy. The two are
// deliberately separate unexported helpers rather than one exported httpcore
// symbol — this is adapter-internal plumbing, not consumer API — but a change to
// either must be made to both. fiber has no equivalent and needs none: fasthttp
// has read the whole body before the handler runs.
//
// ⚠ w here is gc.Writer, a gin.ResponseWriter, NOT the raw http.ResponseWriter.
// http.NewResponseController reaches the connection by walking Unwrap(), and
// gin's *responseWriter implements Unwrap() http.ResponseWriter (gin v1.12.0
// response_writer.go). Should a future gin drop it, SetReadDeadline returns
// http.ErrNotSupported and the deadline silently stops being armed — which is
// why gin_bodydeadline_test.go asserts the bound against a real stalled socket
// rather than trusting this.
//
// ⚠ SetReadDeadline can also fail with http.ErrNotSupported when a consumer's
// middleware wraps the writer without an Unwrap. That degrades to the
// pre-deadline behaviour rather than failing the request: refusing a valid
// request is a worse outcome than the exposure the deadline mitigates, and the
// consumer's own Server.ReadTimeout remains as the backstop.
//
// ⚠ CLEARING, not restoring, and it is HYGIENE rather than a load-bearing
// guard. MEASURED on the stdlib sibling (mutation, 2026-08-21): replacing the
// clear with a no-op, and then with a deadline expired an hour ago, still left a
// second request on the same keep-alive connection answering 201, because
// net/http's serve loop re-arms the read deadline before every request. Do not
// write a "does not bleed across requests" test here; it would be vacuous.
func armBodyReadDeadline(d time.Duration, w http.ResponseWriter) func() {
	noop := func() {}
	if d <= 0 {
		return noop
	}
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(d)); err != nil {
		return noop // http.ErrNotSupported — degrade, do not fail the request.
	}
	return func() { _ = rc.SetReadDeadline(time.Time{}) }
}
