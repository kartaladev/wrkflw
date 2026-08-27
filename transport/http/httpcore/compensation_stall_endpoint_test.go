package httpcore_test

// ADR-0175 — the admin endpoint carrying the three compensation-stall escapes.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// TestResolveCompensationStallEndpoint covers the endpoint's disposition
// decoding and its refusal of an unknown verb.
//
// Decoding is the security-relevant part: the wire carries a STRING, and an
// unrecognised one must be rejected rather than defaulting. The zero value of
// engine.CompensationDisposition is CompensationRetry — a remote re-execution
// primitive — so a silently-defaulted unknown verb would re-invoke a named
// action the operator never asked to run.
func TestResolveCompensationStallEndpoint(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()

	type testCase struct {
		name   string
		in     httpcore.ResolveCompensationStallInput
		assert func(t *testing.T, status int, body any, err error)
	}

	cases := []testCase{
		{
			name: "unknown disposition is rejected, never defaulted",
			in:   httpcore.ResolveCompensationStallInput{CommandID: "c1", Disposition: "obliterate"},
			assert: func(t *testing.T, status int, _ any, err error) {
				require.Error(t, err, "an unrecognised verb must not fall through to retry")
				assert.Zero(t, status)
			},
		},
		{
			name: "missing disposition is rejected",
			in:   httpcore.ResolveCompensationStallInput{CommandID: "c1"},
			assert: func(t *testing.T, status int, _ any, err error) {
				require.Error(t, err,
					"an absent verb must not decode to the zero value, which is retry")
			},
		},
		{
			name: "missing command id is rejected before the service is touched",
			in:   httpcore.ResolveCompensationStallInput{Disposition: "skip"},
			assert: func(t *testing.T, _ int, _ any, err error) {
				require.Error(t, err, "CommandID is required")
			},
		},
		{
			name: "retry decodes and reaches the service",
			in:   httpcore.ResolveCompensationStallInput{CommandID: "c1", Disposition: "retry"},
			assert: func(t *testing.T, _ int, _ any, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, engine.ErrNoCompensationWalk)
			},
		},
		{
			name: "skip decodes and reaches the service",
			in:   httpcore.ResolveCompensationStallInput{CommandID: "c1", Disposition: "skip"},
			assert: func(t *testing.T, _ int, _ any, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, engine.ErrNoCompensationWalk)
			},
		},
		{
			name: "abandon decodes and reaches the service",
			in:   httpcore.ResolveCompensationStallInput{CommandID: "c1", Disposition: "abandon"},
			assert: func(t *testing.T, _ int, _ any, err error) {
				// The harness instance has no walk in flight, so the ENGINE refuses
				// it — which proves the request was decoded and delegated.
				require.Error(t, err)
				assert.ErrorIs(t, err, engine.ErrNoCompensationWalk)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, svc := transporttest.NewHarness(t, def)
			transporttest.StartedApprovalInstance(t, h, "inst-1")

			status, body, err := httpcore.ResolveCompensationStall(t.Context(), svc, "inst-1", tc.in, nil)
			tc.assert(t, status, body, err)
		})
	}
}
