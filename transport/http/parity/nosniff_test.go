package parity_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
)

// TestParity_NosniffHeader asserts that every response from every adapter
// carries X-Content-Type-Options: nosniff.
//
// Why this belongs in the parity package rather than in each adapter's own
// tests: the three adapters write responses through three different framework
// APIs (http.ResponseWriter, gin's gc.JSON, fiber's c.Status().JSON()), so
// "all three set it" is precisely a cross-adapter claim, and a per-adapter test
// would let one drift without failing anything.
//
// ⚠ The rows are chosen to cover the three response paths that reach the wire
// by DIFFERENT code, not merely three different status codes:
//
//   - a 2xx JSON body, written by the endpoint handler directly;
//   - a 4xx error envelope, written by each adapter's writeErr;
//   - a health probe, registered through a separate Customize and easy to miss
//     when a header is added only to the main route groups.
//
// A row that only varied the status code would pass while a whole write path
// stayed bare.
func TestParity_NosniffHeader(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// withHealth mounts the health routes, which the probe row needs.
		withHealth bool
		mkReq      reqFactory
		assert     func(t *testing.T, s, g, f adapterResult)
	}

	cases := []testCase{
		{
			name:       "2xx JSON body",
			withHealth: false,
			mkReq:      getReqFactory("/instances/parity-nosniff-missing"),
			assert: func(t *testing.T, s, g, f adapterResult) {
				// This id does not exist, so the row doubles as the 404 path;
				// the point is the header, not the status.
				assertNosniff(t, "GET /instances/:id", s, g, f)
			},
		},
		{
			name:       "4xx error envelope",
			withHealth: false,
			mkReq:      jsonReqFactory(http.MethodPost, "/instances", map[string]any{"bad": "input"}),
			assert: func(t *testing.T, s, g, f adapterResult) {
				assert.GreaterOrEqual(t, s.status, http.StatusBadRequest,
					"row should exercise the error path")
				assertNosniff(t, "POST /instances 4xx", s, g, f)
			},
		},
		{
			name:       "health probe",
			withHealth: true,
			mkReq:      getReqFactory("/readyz"),
			assert: func(t *testing.T, s, g, f adapterResult) {
				assert.Equal(t, http.StatusOK, s.status)
				assertNosniff(t, "GET /readyz", s, g, f)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var svc service.Service
			_, svc = transporttest.NewHarness(t)

			s := hitStdlib(t, svc, tc.mkReq, tc.withHealth)
			g := hitGin(t, svc, tc.mkReq, tc.withHealth)
			f := hitFiber(t, svc, tc.mkReq, tc.withHealth)

			tc.assert(t, s, g, f)
		})
	}
}

// assertNosniff checks the header on all three adapter results, reporting each
// adapter separately so one bare adapter does not mask the others.
func assertNosniff(t *testing.T, caseName string, s, g, f adapterResult) {
	t.Helper()

	for _, a := range []struct {
		adapter string
		got     adapterResult
	}{
		{"stdlib", s},
		{"gin", g},
		{"fiber", f},
	} {
		assert.Equal(t, "nosniff", a.got.header.Get("X-Content-Type-Options"),
			"%s: %s must send X-Content-Type-Options: nosniff", a.adapter, caseName)
	}
}
