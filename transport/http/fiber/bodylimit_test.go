package fiber_test

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"

	fiberlib "github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/transport/http/fiber"
)

// ---------------------------------------------------------------------------
// Fixtures and helpers for the inbound request-body cap.
//
// ⚠ EVERY fixture in this file stays BELOW fiber's own fiber.Config.BodyLimit
// (default 4 MiB, MEASURED: fiberlib.New().Config().BodyLimit == 4194304).
// Above that ceiling fasthttp reads and refuses the body before the route group
// is ever entered, so no handler — and therefore no MaxBodyBytes pre-check —
// runs; under app.Test the call fails outright with "body size exceeds the
// given limit" (MEASURED with a 5 MiB plain body) rather than producing an
// ErrorBody envelope. Peak memory on the fiber adapter is therefore bounded by
// fiber.Config.BodyLimit, NOT by MaxBodyBytes; MaxBodyBytes bounds what is
// PARSED, not what is READ.

// newRawPostRequest builds a POST carrying exactly raw as its body, optionally
// declaring a Content-Encoding. Unlike newPostRequest it does not marshal, so a
// test can control the byte count and the compression on the wire.
func newRawPostRequest(t *testing.T, path string, raw []byte, contentEncoding string) *http.Request {
	t.Helper()
	return newRawRequest(t, http.MethodPost, path, raw, contentEncoding)
}

// newRawRequest is newRawPostRequest for an arbitrary method — the DELETE
// policy and role-binding routes also decode a body.
func newRawRequest(t *testing.T, method, path string, raw []byte, contentEncoding string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, path, bytes.NewReader(raw))
	require.NoError(t, err)
	r.Header.Set("Content-Type", "application/json")
	if contentEncoding != "" {
		r.Header.Set("Content-Encoding", contentEncoding)
	}
	return r.WithContext(t.Context())
}

// startBody returns a valid POST /instances payload whose serialized length is
// inflated by padding the "name" variable.
func startBody(padTo int) []byte {
	const prefix = `{"def_ref":"greeting","vars":{"name":"`
	const suffix = `"}}`
	pad := padTo - len(prefix) - len(suffix)
	if pad < 0 {
		pad = 0
	}
	return []byte(prefix + strings.Repeat("a", pad) + suffix)
}

// gzipped returns raw compressed with gzip, for use with Content-Encoding: gzip.
func gzipped(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(raw)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// incompressible returns n bytes that gzip cannot shrink. crypto/rand is used
// rather than a seeded PRNG only because it is the lint-clean source of
// high-entropy bytes; nothing here is security-sensitive.
func incompressible(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// ---------------------------------------------------------------------------

// TestBothWireAndDecompressedAreBounded is the test that decides the fiber
// mechanism: the cap must be enforced against BOTH len(c.BodyRaw()) — the wire
// body — and len(c.Body()) — the DECOMPRESSED body that fiber actually parses
// (fiber/v3@v3.4.0 bind.go:309 passes c.Body() to the JSON binder). Neither
// check alone is sufficient, and each row below falsifies one of them:
//
//   - "gzip bypasses a wire-only check": MEASURED against the unmodified
//     adapter — wire 3,127 bytes, decompressed 3,145,769 bytes, status 201.
//     A len(c.BodyRaw())-only check sees 3,127 and admits a 3 MB parse.
//
//   - "gzip whose decompression fiber itself refuses, wire OVER the cap":
//     MEASURED — len(c.BodyRaw()) == 1,543,869 but len(c.Body()) == 33, because
//     on a gunzip-limit failure fiber's Body() returns the literal string
//     "body size exceeds the given limit". A len(c.Body())-only check reads 33,
//     admits it, and the request is classified 400 instead of 413.
//
//   - "gzip whose decompression fiber itself refuses, wire UNDER the cap": the
//     same degradation, but now NEITHER length check can see it — the wire is
//     8,179 bytes and len(c.Body()) is 33, both under a 1 MiB cap. MEASURED
//     against the adapter before the decode-failure guard existed: status 400,
//     message "invalid character 'b' looking for beginning of value" — the
//     33-byte sentinel parsed as JSON. See the guard's rationale in
//     bodylimit.go for why this is 413 and not 400.
//
// ⚠ A plain (unencoded) oversize body does NOT discriminate between the two
// checks — with no Content-Encoding header fiber's Body() returns getBody(),
// byte-identical to BodyRaw(). It is kept as a row because it is the ordinary
// oversize case, not because it falsifies anything.
func TestBothWireAndDecompressedAreBounded(t *testing.T) {
	t.Parallel()

	const cap1MiB = 1 << 20

	// fiber's own ceiling, restated as a const so the fixtures can assert they
	// sit on the intended side of it. MEASURED at the top of this file:
	// fiberlib.New().Config().BodyLimit == 4194304.
	const fiberBodyLimit = 4 << 20

	type testCase struct {
		name     string
		maxBytes int64
		raw      func(t *testing.T) []byte
		encoding string
		assert   func(t *testing.T, status int, body string)
	}

	cases := []testCase{
		{
			name:     "gzip bypasses a wire-only check: ~3 KB wire, ~3 MB decompressed",
			maxBytes: cap1MiB,
			raw: func(t *testing.T) []byte {
				return gzipped(t, startBody(3<<20))
			},
			encoding: "gzip",
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body=%s", body)
				assert.Contains(t, body, `"request_too_large"`)
			},
		},
		{
			name:     "gzip whose decompression fiber itself refuses: wire over the cap",
			maxBytes: cap1MiB,
			raw: func(t *testing.T) []byte {
				// Wire must land between the 1 MiB cap and fiber's 4 MiB
				// BodyLimit; decompressed must exceed the 4 MiB BodyLimit so
				// fiber's own gunzip refuses and Body() degrades to 33 bytes.
				plain := append(incompressible(t, 1500*1024), bytes.Repeat([]byte("z"), 6<<20)...)
				return gzipped(t, plain)
			},
			encoding: "gzip",
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body=%s", body)
				assert.Contains(t, body, `"request_too_large"`)
			},
		},
		{
			// The row the wire-over-the-cap row above cannot reach: here the
			// wire is UNDER the cap, so the len(c.BodyRaw()) check does not
			// fire, and len(c.Body()) is the 33-byte sentinel, so the
			// decompressed check does not fire either. Only an explicit
			// decode-failure guard classifies this correctly.
			name:     "gzip whose decompression fiber itself refuses: wire under the cap",
			maxBytes: cap1MiB,
			raw: func(t *testing.T) []byte {
				plain := startBody(8 << 20)
				wire := gzipped(t, plain)
				require.Less(t, len(wire), cap1MiB,
					"wire must stay under the cap, else the BodyRaw check fires and proves nothing")
				require.Less(t, len(wire), fiberBodyLimit,
					"wire must stay under fiber's own ceiling, else the route group is never entered")
				require.Greater(t, len(plain), fiberBodyLimit,
					"decompressed size must exceed fiber's ceiling so its gunzip refuses")
				return wire
			},
			encoding: "gzip",
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body=%s", body)
				assert.Contains(t, body, `"request_too_large"`)
				// ⚠ This is the half that pins "the binder never ran". Falling
				// through would hand the 33-byte sentinel to c.Bind().JSON —
				// decompressing the payload a SECOND time — and answer 400
				// bad_request. A 413 that still carried a bad_request envelope
				// would mean the guard fired late rather than instead.
				assert.NotContains(t, body, `"bad_request"`)
			},
		},
		{
			name:     "plain body over the cap",
			maxBytes: cap1MiB,
			raw:      func(*testing.T) []byte { return startBody(2 << 20) },
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body=%s", body)
				assert.Contains(t, body, `"request_too_large"`)
			},
		},
		{
			name:     "plain body under the cap is not over-refused",
			maxBytes: cap1MiB,
			raw:      func(*testing.T) []byte { return startBody(4096) },
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusCreated, status, "body=%s", body)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := transporttest.LinearProcess()
			_, svc := transporttest.NewHarness(t, def)

			app := newApp()
			fiber.Mount(app, svc, fiber.WithMaxBodyBytes(tc.maxBytes))

			status, body := appDo(t, app, newRawPostRequest(t, "/instances", tc.raw(t), tc.encoding))
			tc.assert(t, status, body)
		})
	}
}

// TestDecodeFailureGuardReadsAStatusDelta pins the narrowness of the
// decode-failure guard. The guard detects fiber's gunzip-ceiling refusal by the
// side effect c.Body() has on the response — it calls SendStatus(413) — so it
// must compare the status BEFORE and AFTER that call, not read the status
// afterwards in isolation. A middleware that has already put 413 on the
// response (an upstream rate limiter or proxy shim mounted with app.Use, which
// consumers are free to do) would otherwise turn every subsequent body,
// however small and however uncompressed, into a 413 refusal.
//
// ⚠ FALSIFIER: with the "changed during this call" half of the condition
// dropped — `after == StatusRequestEntityTooLarge` alone — this test observes
// 413 instead of 201. MEASURED by exactly that mutation before the clause was
// restored.
func TestDecodeFailureGuardReadsAStatusDelta(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	app := newApp()
	app.Use(func(c fiberlib.Ctx) error {
		c.Status(http.StatusRequestEntityTooLarge)
		return c.Next()
	})
	fiber.Mount(app, svc, fiber.WithMaxBodyBytes(1<<20))

	// A small, plain, perfectly valid body: nothing about it is oversize.
	status, body := appDo(t, app, newRawPostRequest(t, "/instances", startBody(4096), ""))
	assert.Equal(t, http.StatusCreated, status, "body=%.200s", body)
	assert.NotContains(t, body, `"request_too_large"`)
}

// TestDecodeFailuresOtherThanTheCeilingStayBadRequest is the other half of the
// guard's narrowness. c.Body() swallows EVERY decode error and returns the
// error text as if it were the body, but only the gunzip-ceiling failure means
// "too large"; the rest mean "this stream is not decodable", which is a 400.
// The guard must not sweep them up.
//
// ⚠ FALSIFIER: widening the condition to "the status changed at all" — dropping
// the `== StatusRequestEntityTooLarge` half — reclassifies the two
// unsupported-encoding rows as 413, because c.Body() moves the response to 501
// and 415 for those (MEASURED: Content-Encoding: compress → 501 "Not
// Implemented"; an unknown encoding → 415 "Unsupported Media Type"). The two
// corrupt rows leave the status at 200 and so are held by no mutation of this
// condition — they are here as the regression pin for the fall-through path,
// not as falsifiers.
func TestDecodeFailuresOtherThanTheCeilingStayBadRequest(t *testing.T) {
	t.Parallel()

	valid := gzipped(t, []byte(`{"def_ref":"greeting","vars":{"name":"ada"}}`))

	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)/2] ^= 0xff

	type testCase struct {
		name     string
		raw      []byte
		encoding string
		assert   func(t *testing.T, status int, body string)
	}

	badRequest := func(t *testing.T, status int, body string) {
		assert.Equal(t, http.StatusBadRequest, status, "body=%.200s", body)
		assert.Contains(t, body, `"bad_request"`)
		assert.NotContains(t, body, `"request_too_large"`)
	}

	cases := []testCase{
		{
			name:     "valid gzip under the cap is still accepted",
			raw:      valid,
			encoding: "gzip",
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusCreated, status, "body=%.200s", body)
			},
		},
		{name: "corrupt gzip", raw: corrupt, encoding: "gzip", assert: badRequest},
		{name: "truncated gzip", raw: valid[:len(valid)-4], encoding: "gzip", assert: badRequest},
		{name: "Content-Encoding: compress is not implemented", raw: valid, encoding: "compress", assert: badRequest},
		{name: "unknown Content-Encoding", raw: valid, encoding: "banana", assert: badRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := transporttest.LinearProcess()
			_, svc := transporttest.NewHarness(t, def)

			app := newApp()
			fiber.Mount(app, svc, fiber.WithMaxBodyBytes(1<<20))

			status, body := appDo(t, app, newRawPostRequest(t, "/instances", tc.raw, tc.encoding))
			tc.assert(t, status, body)
		})
	}
}

// TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute covers the ONE decode
// site that discards its bind error rather than propagating it —
// POST {basePath}/admin/instances/:id/incidents/:incidentID/resolve, whose body
// is genuinely optional (`_ = c.Bind().JSON(&in)`). Optional does not mean
// unbounded: the site must still refuse an oversize body, while every OTHER
// bind error stays ignored.
//
// ⚠ This is an ADMIN route. Admin routes are kept out of Mount, so the
// test names AdminRoutes.Customize explicitly; a fixture built on Mount would
// 404 on the path and prove nothing.
//
// ⚠ FALSIFIER: the oversize row fails against an implementation that edits only
// the 12 propagating decode sites — the request is admitted and answered 404 by
// the service rather than 413.
//
// ⚠ fiber's discarding site uses this same pre-check, NOT the
// errors.As(*http.MaxBytesError) shape the stdlib and gin adapters use on their
// discarding sites: fiber never wraps the body in http.MaxBytesReader, so no
// such error is ever produced here.
//
// The second row is the control the brief called
// TestBodyAbsentOnTheOptionalRouteStillSucceeds; the project's mandatory
// table-test rule folds two cases over one handler into one table rather than
// two sibling functions.
func TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		raw    []byte
		assert func(t *testing.T, status int, body string)
	}

	cases := []testCase{
		{
			name: "oversized body is refused even though the body is optional",
			raw:  startBody(2 << 20),
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body=%s", body)
				assert.Contains(t, body, `"request_too_large"`)
			},
		},
		{
			name: "body absent still reaches the service",
			raw:  nil,
			assert: func(t *testing.T, status int, body string) {
				// RECORDED against the unmodified adapter: an absent body binds
				// to the zero ResolveIncidentInput, the bind error is discarded,
				// and the unknown instance is answered 404 by the service.
				assert.Equal(t, http.StatusNotFound, status, "body=%s", body)
				assert.NotEqual(t, http.StatusRequestEntityTooLarge, status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t)

			app := newApp()
			fiber.AdminRoutes{Svc: svc}.Customize(app, fiber.WithMaxBodyBytes(1<<20))

			req := newRawPostRequest(t, "/admin/instances/no-such-id/incidents/inc-1/resolve", tc.raw, "")
			status, body := appDo(t, app, req)
			tc.assert(t, status, body)
		})
	}
}

// TestDisabledCapSkipsBothChecks covers the row that is easiest to get wrong.
// httpcore documents n <= 0 as DISABLING the cap (matching
// action/httpcall.WithMaxResponseSize), so a non-positive MaxBodyBytes must
// skip BOTH pre-checks entirely rather than being compared against.
//
// ⚠ FALSIFIER: this fails against the obvious implementation that compares
// unconditionally — with a cap of 0, every body of one byte or more exceeds it,
// so disabling the cap turns into 413 on an ORDINARY request. That is not a
// hypothetical: it is what the pre-check did before the guard was added, and it
// is why the cap-0 case is checked here rather than assumed.
func TestDisabledCapSkipsBothChecks(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		maxBytes int64
	}

	cases := []testCase{
		{name: "cap of zero disables the bound", maxBytes: 0},
		{name: "negative cap disables the bound", maxBytes: -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := transporttest.LinearProcess()
			_, svc := transporttest.NewHarness(t, def)

			app := newApp()
			fiber.Mount(app, svc, fiber.WithMaxBodyBytes(tc.maxBytes))

			// 2 MiB: far above the 1 MiB default cap, still below fiber's own
			// 4 MiB BodyLimit so the route group is actually reached.
			status, body := appDo(t, app, newRawPostRequest(t, "/instances", startBody(2<<20), ""))
			assert.Equal(t, http.StatusCreated, status, "body=%.200s", body)
		})
	}
}

// TestCapBoundaryIsExactlyN pins the off-by-one. The comparison is strictly
// greater-than: a body of EXACTLY n bytes is ACCEPTED, n+1 is refused. That is
// http.MaxBytesReader's semantics, which the stdlib and gin adapters inherit by
// construction; fiber compares lengths by hand and so could silently drift one
// byte away from its siblings. A one-byte divergence between adapters is
// invisible without this test.
//
// ⚠ FALSIFIER: the "exactly n" row fails against an implementation using >=.
func TestCapBoundaryIsExactlyN(t *testing.T) {
	t.Parallel()

	const n = 4096

	type testCase struct {
		name   string
		size   int
		assert func(t *testing.T, status int, body string)
	}

	cases := []testCase{
		{
			name: "a body of exactly n bytes is accepted",
			size: n,
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusCreated, status, "body=%.200s", body)
			},
		},
		{
			name: "a body of n+1 bytes is refused",
			size: n + 1,
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body=%.200s", body)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := startBody(tc.size)
			require.Len(t, raw, tc.size, "fixture must be exactly the intended size")

			def := transporttest.LinearProcess()
			_, svc := transporttest.NewHarness(t, def)

			app := newApp()
			fiber.Mount(app, svc, fiber.WithMaxBodyBytes(n))

			status, body := appDo(t, app, newRawPostRequest(t, "/instances", raw, ""))
			tc.assert(t, status, body)
		})
	}
}

// TestEveryDecodeSiteIsBounded enumerates ALL THIRTEEN routes in this adapter
// that decode a request body — the twelve that propagate their bind error plus
// the one (resolve-incident) that discards it — and asserts each refuses an
// oversize body with 413.
//
// ⚠ This exists because "every decode site is bounded" is otherwise an
// enumeration asserted only in prose, and prose enumerations in this repo rot.
// Without it exactly two of the thirteen sites were exercised, and a guard
// omitted from any of the other eleven would have been invisible.
//
// The pre-check runs BEFORE any service call, so the path parameters need not
// resolve to real entities — every row 413s on the body alone.
func TestEveryDecodeSiteIsBounded(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		method string
		path   string
	}

	cases := []testCase{
		{name: "POST /instances", method: http.MethodPost, path: "/instances"},
		{name: "POST /instances/:id/signals", method: http.MethodPost, path: "/instances/x/signals"},
		{name: "POST /messages", method: http.MethodPost, path: "/messages"},
		{name: "POST /tasks/:token/claim", method: http.MethodPost, path: "/tasks/x/claim"},
		{name: "POST /tasks/:token/complete", method: http.MethodPost, path: "/tasks/x/complete"},
		{name: "POST /tasks/:token/reassign", method: http.MethodPost, path: "/tasks/x/reassign"},
		{
			name:   "POST /admin/instances/:id/incidents/:incidentID/resolve (discarding site)",
			method: http.MethodPost, path: "/admin/instances/x/incidents/y/resolve",
		},
		{
			name:   "POST /admin/instances/:id/compensation/resolve-stall",
			method: http.MethodPost, path: "/admin/instances/x/compensation/resolve-stall",
		},
		{name: "POST /admin/dead-letters/redrive", method: http.MethodPost, path: "/admin/dead-letters/redrive"},
		{name: "POST /admin/policies", method: http.MethodPost, path: "/admin/policies"},
		{name: "DELETE /admin/policies", method: http.MethodDelete, path: "/admin/policies"},
		{name: "POST /admin/role-bindings", method: http.MethodPost, path: "/admin/role-bindings"},
		{name: "DELETE /admin/role-bindings", method: http.MethodDelete, path: "/admin/role-bindings"},
	}

	// The count is asserted, not just implied: adding a decode site without a
	// row here should fail loudly rather than silently go unbounded.
	require.Len(t, cases, 13, "13 decode sites: 12 propagating + 1 discarding")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := transporttest.LinearProcess()
			_, svc := transporttest.NewHarness(t, def)

			app := newApp()
			opt := fiber.WithMaxBodyBytes(1 << 20)
			// ⚠ The mount AUTHENTICATES. This test is about the body bound, and the
			// three task routes refuse an unresolved identity BEFORE the body is
			// read — so without an actor they would 401 and this test would stop
			// testing what it is named for. The 401-precedes-413 ordering is pinned
			// separately by TestUnauthenticatedOversizeBodyIs401NotThe413.
			fiber.Mount(app, svc, opt, fiber.WithRequestActor(staticActor("alice", "manager")))
			fiber.AdminRoutes{
				Svc:         svc,
				DeadLetters: newAlwaysDeadLetterAdmin(t),
				Policies:    newAlwaysPoliciesAdmin(t),
			}.Customize(app, opt)

			req := newRawRequest(t, tc.method, tc.path, startBody(2<<20), "")
			status, body := appDo(t, app, req)
			assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body=%.200s", body)
			assert.Contains(t, body, `"request_too_large"`)
		})
	}
}

// TestUnderCapBehaviourIsUnchanged pins the byte-for-byte behaviour of a body
// that does NOT exceed the cap. Both rows were RECORDED against the unmodified
// adapter, before any pre-check existed:
//
//	clean body            → 201, response carries instance_id
//	complete JSON + 4 KiB
//	of trailing garbage   → 400 bad_request, message
//	                        "…invalid character 'G' after top-level value"
//
// The second row is the interesting one: it is a REJECTED request that must
// keep being rejected as 400, not silently reclassified as 413 by a pre-check
// that measures the wrong thing. It also documents that fiber's binder parses
// the WHOLE body (unlike stdlib's json.Decoder, which stops after one value),
// so trailing bytes are an error here.
func TestUnderCapBehaviourIsUnchanged(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		raw    []byte
		assert func(t *testing.T, status int, body string)
	}

	cases := []testCase{
		{
			name: "clean body under the cap",
			raw:  []byte(`{"def_ref":"greeting","vars":{"name":"ada"}}`),
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusCreated, status, "body=%s", body)
				assert.Contains(t, body, `"instance_id"`)
			},
		},
		{
			name: "complete JSON value plus under-cap trailing bytes",
			raw: []byte(`{"def_ref":"greeting","vars":{"name":"ada"}}` +
				strings.Repeat(" ", 4096) + `GARBAGE`),
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusBadRequest, status, "body=%s", body)
				assert.Contains(t, body, `"bad_request"`)
				assert.Contains(t, body, "invalid character 'G' after top-level value")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := transporttest.LinearProcess()
			_, svc := transporttest.NewHarness(t, def)

			app := newApp()
			fiber.Mount(app, svc)

			status, body := appDo(t, app, newRawPostRequest(t, "/instances", tc.raw, ""))
			tc.assert(t, status, body)
		})
	}
}

// TestUnauthenticatedOversizeBodyIs401NotThe413 pins the ORDERING that pre-decode
// identity resolution establishes on the three human-task routes: identity is
// decided BEFORE the body is read, so an unauthenticated caller never gets far enough
// to learn the body limit.
//
// ⚠ This deliberately INVERTS what an earlier revision pinned (413 → 401 → 404). The
// earlier order meant an unauthenticated caller could force a full MaxBodyBytes read
// and hold the handler for BodyReadTimeout before its 401 — a resource-consumption
// primitive on the only routes that authenticate.
//
// FAILS IF RESOLUTION MOVES BACK INSIDE THE ENDPOINT: the oversize body is read first
// and the answer becomes 413.
func TestUnauthenticatedOversizeBodyIs401NotThe413(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	app := newApp()
	fiber.Mount(app, svc, fiber.WithMaxBodyBytes(1<<20)) // no WithRequestActor

	for _, path := range []string{"/tasks/x/claim", "/tasks/x/complete", "/tasks/x/reassign"} {
		req := newRawRequest(t, http.MethodPost, path, startBody(2<<20), "")
		status, body := appDo(t, app, req)
		assert.Equal(t, http.StatusUnauthorized, status,
			"%s: identity must be refused before the body is read; body=%.120s", path, body)
	}
}
