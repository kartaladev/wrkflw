package fiber

import (
	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// oversizeBody reports whether the inbound request body exceeds
// cfg.MaxBodyBytes and must be refused with [httpcore.ErrRequestBodyTooLarge]
// before it is parsed.
//
// ⚠ BOTH lengths are checked, and NEITHER alone is sufficient. This is the one
// place the fiber adapter genuinely differs from the stdlib and gin adapters,
// which wrap the body in http.MaxBytesReader and get a single correct answer:
//
//   - len(c.BodyRaw()) is the body as it arrived on the wire (req.go:92-96).
//     Reading it has no response side effect.
//   - len(c.Body()) is the DECOMPRESSED body (req.go:150). This is the one that
//     matters, because fiber's JSON binder parses c.Body(), not c.BodyRaw()
//     (bind.go:308) — so a wire-only check bounds the transfer while the parse
//     consumes the decompressed bytes. MEASURED against the unbounded adapter:
//     a 3,127-byte gzip request parsed 3,145,769 bytes and returned 201.
//
// The reverse check alone fails too: when decompression exceeds fiber's OWN
// gunzip ceiling (fiber.Config.BodyLimit), Body() does not report the real size
// — it returns the 33-byte string "body size exceeds the given limit"
// (req.go:186-196). MEASURED: a request with len(c.BodyRaw()) == 1,543,869 has
// len(c.Body()) == 33, so a decompressed-only check admits it.
//
// Calling c.Body() is not wasted work on the uncompressed path — c.Bind().JSON
// calls it regardless. ⚠ On the COMPRESSED path it is not free: for a single
// Content-Encoding fiber never writes the decoded bytes back onto the request
// (req.go:132-138 only calls SetBodyRaw when i > 0, i.e. from the second
// encoding of a chain onwards), so the raw body stays compressed and EVERY
// c.Body() call decompresses again. MEASURED: after c.Body() returns,
// len(c.BodyRaw()) is still 8,179 and still begins with the gzip magic 1f 8b.
// The payload is therefore decompressed once here and once more by the binder.
// That is bounded — both decodes are capped by fiber.Config.BodyLimit — but it
// is a factor of two, which is why the decode-failure branch below refuses
// rather than falling through to a second decode.
//
// DECODE FAILURE — why 413 and not 400. When decompression trips fiber's own
// gunzip ceiling (fiber.Config.BodyLimit), c.Body() returns neither the body
// nor an error: it returns the 33-byte text of fasthttp.ErrBodyTooLarge and, on
// the way, calls SendStatus(413) on the response (req.go:186-196). If the wire
// body is ALSO over the cap, the len(c.BodyRaw()) check above already caught it.
// If the wire body is UNDER the cap — a compression bomb, which is the whole
// point of the attack — then NEITHER length check can see it: MEASURED, an
// 8,179-byte gzip request decompressing to 8,388,608 bytes yields
// len(c.BodyRaw()) == 8,179 and len(c.Body()) == 33, both under a 1 MiB cap.
// Falling through then costs a SECOND full decompression in the binder and
// answers 400 with "invalid character 'b' looking for beginning of value" —
// the sentinel text parsed as JSON. Both halves of that are wrong: 400 asserts
// a malformed payload, and the payload here is well-formed JSON that is simply
// too large to decompress; and fiber has already classified it 413 itself, so
// the adapter would be overwriting its own framework's correct answer with an
// incorrect one. The limit that tripped is fiber's rather than MaxBodyBytes,
// but 413 is the status for "the entity is too large", not for "the entity
// exceeded THIS PARTICULAR limit", so 413 is the truthful answer on both.
//
// The guard is deliberately narrow: it fires only when c.Body() ITSELF moved
// the response to 413. MEASURED, the other decode failures leave the status
// untouched or move it elsewhere, and each keeps its current classification:
//
//	gzip over BodyLimit  200 → 413  len(Body()) == 33  "body size exceeds the given limit"
//	corrupt gzip         200 → 200  len(Body()) == 22  "gzip: invalid checksum"
//	truncated gzip       200 → 200  len(Body()) == 14  "unexpected EOF"
//	Content-Encoding: compress  200 → 501  "Not Implemented"
//	unknown encoding     200 → 415  "Unsupported Media Type"
//	valid gzip / no encoding    200 → 200  the real body
//
// The two corrupt rows still fall through to the binder and are answered 400,
// which is right — a stream that does not decode IS a bad request. The 415/501
// rows also fall through to a 400 today; fiber's own status is not surfaced.
// Both are pre-existing behaviour left unchanged here, MEASURED through the
// mounted adapter WITH this guard in place: corrupt gzip 400, truncated gzip
// 400, Content-Encoding: compress 400, unknown encoding 400, valid gzip under
// the cap 201. Both are also cheap to re-attempt: req.go's encoding switch
// returns before decoding at all for an unsupported encoding, and a corrupt
// stream fails where its bytes are corrupt.
//
// BOUNDARY: the comparison is strictly greater-than, so a body of EXACTLY n
// bytes is ACCEPTED and n+1 is refused. This deliberately matches
// http.MaxBytesReader, which the stdlib and gin adapters use, so the three
// adapters agree to the byte.
func oversizeBody(cfg httpcore.CustomizeConfig[fiberlib.Router], c fiberlib.Ctx) bool {
	n := cfg.MaxBodyBytes
	// ⚠ n <= 0 DISABLES the cap (httpcore.CustomizeConfig.MaxBodyBytes). Both
	// checks must be skipped outright, not merely compared against: with n == 0
	// a plain comparison refuses every body of one byte or more, so "disabled"
	// would mean 413 on an ordinary request. MEASURED before this guard existed.
	if n <= 0 {
		return false
	}
	if int64(len(c.BodyRaw())) > n {
		return true
	}

	// statusBefore is read rather than assumed to be 200 so the guard means
	// "this c.Body() call refused the body", not "something upstream already
	// set 413".
	statusBefore := c.Response().StatusCode()
	// Hoisted: this is the single decompression this function performs, shared
	// by the failure guard and the length comparison.
	decoded := c.Body()
	if after := c.Response().StatusCode(); after != statusBefore && after == fiberlib.StatusRequestEntityTooLarge {
		return true
	}
	return int64(len(decoded)) > n
}

// bindOptionalBody decodes an OPTIONAL JSON body into dst under cfg's inbound
// cap. An ABSENT or UNDECODABLE body leaves dst at its zero value; an OVERSIZE
// body is still refused. It returns [httpcore.ErrRequestBodyTooLarge] for that
// refusal and nil otherwise, so a caller writes:
//
//	if err := bindOptionalBody(cfg, c, &in); err != nil {
//		return writeErr(cfg, c, err)
//	}
//
// ⚠ The sentinel is returned BARE. Wrapping it in [httpcore.ErrBadInput] — as
// the REQUIRED decode sites rightly do with their bind error — would be
// absorbed by the ordered switch in httpcore.ClassifyError, which answers
// "bad_request" for anything matching ErrBadInput, and the 413 would silently
// become a 400.
//
// ⚠ Optional is not unbounded. Skipping the cap here because the payload is
// ignorable would leave one route decoding an arbitrarily large body into
// memory, which is the exact hole ADR-0186's cap exists to close — and the
// decompression accounting in [oversizeBody] applies unchanged.
//
// Only the claim route uses this: ADR-0189 emptied httpcore.ClaimInput, so a
// correctly-migrated client sends NO body at all and requiring one would answer
// 400 (MEASURED before this helper existed: "workflow-httpcore: bad input:
// bind from body: unexpected end of JSON input"). CompleteInput and
// ReassignInput still carry required content and keep the strict decode.
func bindOptionalBody(cfg httpcore.CustomizeConfig[fiberlib.Router], c fiberlib.Ctx, dst any) error {
	if oversizeBody(cfg, c) {
		return httpcore.ErrRequestBodyTooLarge
	}
	_ = c.Bind().JSON(dst) // body is optional
	return nil
}
