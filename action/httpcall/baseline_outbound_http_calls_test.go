package httpcall_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/action/httpcall"
)

// errRejectEveryBody is the body validator's own failure, so a test can tell the
// validator's refusal apart from any other error on the request path.
var errRejectEveryBody = errors.New("required field missing")

// TestBaselineOutboundHTTPCallDo characterizes rows of the outbound-http-calls
// capability (openspec/specs/outbound-http-calls/spec.md) that need a target
// server: retryability of a 408 and of an unreachable target, and that a
// rejected or over-cap request body never reaches the target. Every target
// counts the requests it receives.
func TestBaselineOutboundHTTPCallDo(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// status the target answers with.
		status int
		// unreachable closes the target before the call, so nothing listens.
		unreachable bool
		opts        []httpcall.Option
		assert      func(t *testing.T, hits int32, err error)
	}

	cases := []testCase{
		{
			name:   "a 408 answer is a retryable error",
			status: http.StatusRequestTimeout,
			assert: func(t *testing.T, hits int32, err error) {
				require.Error(t, err)
				assert.True(t, action.IsRetryable(err), "408 must be retryable")
				assert.Equal(t, int32(1), hits)
			},
		},
		{
			name:        "an unreachable target is a retryable error",
			status:      http.StatusOK,
			unreachable: true,
			assert: func(t *testing.T, hits int32, err error) {
				require.Error(t, err)
				assert.True(t, action.IsRetryable(err), "a transport failure must be retryable")
				assert.Equal(t, int32(0), hits)
			},
		},
		{
			name:   "a rejecting body validator blocks the request and names the failure",
			status: http.StatusOK,
			opts: []httpcall.Option{
				httpcall.WithBodyKey("payload"),
				httpcall.WithBodyValidator(func(context.Context, []byte, map[string]any) error {
					return errRejectEveryBody
				}),
			},
			assert: func(t *testing.T, hits int32, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, errRejectEveryBody, "the validator's own failure is carried")
				assert.Contains(t, err.Error(), "body validation", "the error names the body validation failure")
				assert.False(t, action.IsRetryable(err), "a body validation failure is not retryable")
				assert.Equal(t, int32(0), hits, "the target must receive no request")
			},
		},
		{
			name:   "an over-cap buffered request body is never sent",
			status: http.StatusOK,
			opts: []httpcall.Option{
				httpcall.WithMaxResponseSize(16),
				httpcall.WithBodyFunc(func(context.Context, map[string]any) (io.Reader, error) {
					return strings.NewReader(strings.Repeat("b", 64)), nil
				}),
				// The validator makes the action buffer the body, which is where the cap applies.
				httpcall.WithBodyValidator(func(context.Context, []byte, map[string]any) error {
					return nil
				}),
			},
			assert: func(t *testing.T, hits int32, err error) {
				require.ErrorIs(t, err, httpcall.ErrBodyTooLarge)
				assert.Equal(t, int32(0), hits, "the target must receive no request")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)
			if tc.unreachable {
				srv.Close()
			}

			opts := append([]httpcall.Option{httpcall.WithBaseURL(srv.URL)}, tc.opts...)
			_, err := httpcall.NewHTTPCall(opts...).Do(t.Context(), map[string]any{"payload": map[string]any{}})
			tc.assert(t, hits.Load(), err)
		})
	}
}
