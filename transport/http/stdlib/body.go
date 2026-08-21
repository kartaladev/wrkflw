package stdlib

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// requestBodyReader returns the reader every decode site parses from, bounding
// the read to cfg.MaxBodyBytes when that is positive. The only error it returns
// is the BARE [httpcore.ErrRequestBodyTooLarge].
//
// ⚠ It bounds the READ, not the PARSE. Capping inside the decoder —
// json.NewDecoder(http.MaxBytesReader(...)).Decode(&in) — does NOT bound the
// request: json.Decoder scans its buffer for a complete value BEFORE consulting
// the read error, so an oversize body whose leading value fits under the cap
// decodes happily and the bytes behind it are never read. MEASURED at a 64-byte
// cap: such a body answered 201, not 413.
//
// ⚠ A non-positive cap means DISABLED, and the wrapper must then not be
// installed AT ALL rather than installed with 0. MEASURED:
// http.MaxBytesReader(w, body, 0) makes io.ReadAll return "http: request body
// too large" for every NON-EMPTY body, so passing the disabled value through
// would refuse every request. The disabled path therefore streams straight from
// req.Body — which is also why it must not simply run an uncapped io.ReadAll,
// an unbounded read into memory being the very primitive this bounds.
//
// ⚠ It also bounds how LONG the read may block, via cfg.BodyReadTimeout. Reading
// to completion is what makes the cap total, but it also converts a
// fast-returning request into an indefinite handler hold: MEASURED 2026-08-21
// against a real server, Content-Length 400000 carrying a complete 41-byte value
// then a stall answered 201 in 0s with the cap disabled and produced NO RESPONSE
// after 3s with the cap at its 1 MiB default. Bounding only the SIZE would trade
// a memory-exhaustion primitive for a cheaper slowloris one.
func requestBodyReader(
	cfg httpcore.CustomizeConfig[*http.ServeMux],
	w http.ResponseWriter,
	req *http.Request,
) (io.Reader, error) {
	if cfg.MaxBodyBytes <= 0 {
		return req.Body, nil
	}

	// ⚠ Armed only on this branch — the cap being active is what creates the
	// blocking read — and cleared the moment the read is done, because the
	// deadline lives on the CONNECTION and would otherwise outlive the handler
	// and strike the next request on a keep-alive connection.
	clearDeadline := armBodyReadDeadline(cfg.BodyReadTimeout, w)
	defer clearDeadline()

	buf, err := io.ReadAll(http.MaxBytesReader(w, req.Body, cfg.MaxBodyBytes))

	// ⚠ Oversize is *http.MaxBytesError specifically, NEVER "the read returned
	// an error". MEASURED against a real server: a request that over-declares
	// Content-Length and ends early yields "unexpected EOF" with errors.As
	// false — so classifying any read error as oversize would answer 413 to
	// every aborted upload.
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, httpcore.ErrRequestBodyTooLarge
	}

	// Any OTHER read error is deliberately dropped and the buffered prefix
	// handed to the decoder, which reproduces today's behaviour exactly: a
	// truncated stream makes json.Decoder report "unexpected EOF" either way,
	// and a complete value that arrived before the abort still decodes.
	return bytes.NewReader(buf), nil
}

// armBodyReadDeadline sets a read deadline of d on the connection behind w and
// returns the function that clears it again. d <= 0 arms nothing and returns a
// no-op, matching the disabled convention of MaxBodyBytes.
//
// ⚠ SIBLING: gin/bodycap.go holds a byte-identical copy for gc.Writer. The two
// are deliberately separate unexported helpers rather than one exported
// httpcore symbol — this is adapter-internal plumbing, not consumer API — but a
// change to either must be made to both. fiber has no equivalent and needs none:
// fasthttp has read the whole body before the handler runs.
//
// ⚠ SetReadDeadline can legitimately FAIL with http.ErrNotSupported when a
// ResponseWriter wrapper in the consumer's middleware chain does not implement
// Unwrap() down to the connection. That degrades to the pre-deadline behaviour —
// an unbounded wait — rather than failing the request, because refusing a valid
// request is a worse outcome than the exposure the deadline mitigates, and the
// consumer's own Server.ReadTimeout remains as the backstop.
//
// ⚠ CLEARING, not restoring, and it is HYGIENE rather than a load-bearing
// guard — do not write a test claiming otherwise. There is no getter for the
// deadline net/http already set, so the cleanup clears it outright.
//
// MEASURED (mutation, 2026-08-21): replacing this clear with a no-op, and then
// with a deadline deliberately expired ONE HOUR AGO, left a second request on
// the same keep-alive connection answering 201 both times. The reason is in
// net/http/server.go's serve loop, which re-arms the read deadline before every
// request unconditionally — idleTimeout or clear, then readHeaderTimeout or
// clear. So cross-request bleed cannot occur under net/http whatever this
// function leaves behind, and a "does not bleed" test here would be vacuous.
// The clear is kept because it costs one syscall and keeps the connection's
// state honest for anything that inspects it inside the same request.
//
// ⚠ It CAN, however, extend the CURRENT request's whole-request deadline:
// server.go sets Server.ReadTimeout as a wholeReqDeadline before the handler
// runs, and arming overwrites it. A consumer whose Server.ReadTimeout is shorter
// than BodyReadTimeout should set BodyReadTimeout no higher than it, or disable
// BodyReadTimeout and rely on ReadTimeout alone.
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

// decodeRequestBody decodes a REQUIRED JSON body into dst under cfg's inbound
// cap, writing the response itself and reporting false when it did. An oversize
// body is 413; a decode failure is 400.
//
// ⚠ The oversize sentinel is passed to writeErr BARE. Wrapping it in
// [httpcore.ErrBadInput] — as the decode-failure arm rightly does — would be
// silently absorbed by the ordered switch in httpcore.ClassifyError, which
// answers "bad_request" for anything matching ErrBadInput.
func decodeRequestBody(
	cfg httpcore.CustomizeConfig[*http.ServeMux],
	w http.ResponseWriter,
	req *http.Request,
	dst any,
) bool {
	body, err := requestBodyReader(cfg, w, req)
	if err != nil {
		writeErr(cfg, w, req, err)
		return false
	}
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
		return false
	}
	return true
}

// decodeOptionalRequestBody decodes an OPTIONAL JSON body into dst under cfg's
// inbound cap. Decode failures stay ignored — an absent or malformed body
// leaves dst at its zero value, which is the documented contract of the route
// that uses it — but an OVERSIZE body is still refused with 413. Ignoring that
// too would leave one route reading an unbounded body into memory, which is the
// exact hole the cap exists to close.
func decodeOptionalRequestBody(
	cfg httpcore.CustomizeConfig[*http.ServeMux],
	w http.ResponseWriter,
	req *http.Request,
	dst any,
) bool {
	body, err := requestBodyReader(cfg, w, req)
	if err != nil {
		writeErr(cfg, w, req, err)
		return false
	}
	_ = json.NewDecoder(body).Decode(dst) // body is optional
	return true
}
