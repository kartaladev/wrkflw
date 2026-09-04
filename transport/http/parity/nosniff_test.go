package parity_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/internal/transporttest"
)

// TestParity_NosniffHeader asserts that every response this library writes,
// from every adapter, carries X-Content-Type-Options: nosniff.
//
// Why this belongs in the parity package rather than in each adapter's own
// tests: the three adapters write responses through three different framework
// APIs (http.ResponseWriter, gin's gc.JSON, fiber's c.Status().JSON()), so
// "all three set it" is precisely a cross-adapter claim, and a per-adapter test
// would let one drift without failing anything.
//
// ⚠ The rows cover three response paths that reach the wire by DIFFERENT code,
// not merely three different status codes:
//
//   - a 2xx body written by the endpoint handler itself;
//   - a 4xx envelope written by each adapter's writeErr;
//   - a health probe, registered through a separate Customize and easy to miss
//     when a header is added only to the main route groups.
//
// A row that only varied the status code would leave a whole write path
// unasserted while still passing — an earlier draft of this test did exactly
// that, with two rows that both happened to run through writeErr.
//
// ⚠ SCOPE: this covers responses THIS LIBRARY writes. A consumer middleware
// registered through WithMiddleware composes OUTSIDE the route wrapper, so a
// middleware that short-circuits (auth, rate limiting, CORS) answers without
// the header. That gap is tracked separately; it is not asserted here because
// it is not currently closed.
func TestParity_NosniffHeader(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// withHealth mounts the health routes, which the probe row needs.
		withHealth bool
		mkReq      reqFactory
		// wantStatus is asserted on ALL THREE adapters, not just one: a row
		// whose status silently changed on one adapter would stop exercising
		// that adapter's intended write path while still passing.
		wantStatus int
	}

	cases := []testCase{
		{
			name:       "2xx handler-written body",
			withHealth: false,
			mkReq: jsonReqFactory(http.MethodPost, "/instances", map[string]any{
				"def_ref": "greeting",
				"vars":    map[string]any{"name": "ada"},
			}),
			wantStatus: http.StatusCreated,
		},
		{
			name:       "4xx error envelope",
			withHealth: false,
			mkReq:      getReqFactory("/instances/parity-nosniff-missing"),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "health probe",
			withHealth: true,
			mkReq:      getReqFactory("/readyz"),
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// LinearProcess is registered for every row so the 201 row has a
			// definition to start; the other rows are unaffected by it.
			_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())

			for _, a := range []struct {
				adapter string
				got     adapterResult
			}{
				{"stdlib", hitStdlib(t, svc, tc.mkReq, tc.withHealth)},
				{"gin", hitGin(t, svc, tc.mkReq, tc.withHealth)},
				{"fiber", hitFiber(t, svc, tc.mkReq, tc.withHealth)},
			} {
				assert.Equal(t, tc.wantStatus, a.got.status,
					"%s: row must exercise the intended write path (body=%s)",
					a.adapter, a.got.rawBody)
				assert.Equal(t, "nosniff", a.got.header.Get("X-Content-Type-Options"),
					"%s: must send X-Content-Type-Options: nosniff", a.adapter)
			}
		})
	}
}
