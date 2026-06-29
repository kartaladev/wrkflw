package httpcall_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/action/httpcall"
)

// TestHTTPCall_ResponseSizeCap verifies the response body is bounded by the
// configured cap (default 10 MiB), that over-cap responses fail NonRetryable
// with ErrBodyTooLarge, and that a non-positive cap disables the bound.
func TestHTTPCall_ResponseSizeCap(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		bodySize int
		opts     []httpcall.Option
		assert   func(t *testing.T, out map[string]any, err error)
	}

	const tenMiB = 10 << 20

	cases := []testCase{
		{
			name:     "body under explicit cap succeeds",
			bodySize: 8,
			opts:     []httpcall.Option{httpcall.WithMaxResponseSize(16)},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err)
				assert.Equal(t, 200, out["httpStatus"])
			},
		},
		{
			name:     "body over explicit cap fails NonRetryable with ErrBodyTooLarge",
			bodySize: 64,
			opts:     []httpcall.Option{httpcall.WithMaxResponseSize(16)},
			assert: func(t *testing.T, _ map[string]any, err error) {
				require.Error(t, err)
				require.ErrorIs(t, err, httpcall.ErrBodyTooLarge)
				assert.False(t, action.IsRetryable(err), "over-cap response must be NonRetryable")
			},
		},
		{
			name:     "zero cap disables the bound",
			bodySize: tenMiB + 1024,
			opts:     []httpcall.Option{httpcall.WithMaxResponseSize(0)},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err)
				assert.Equal(t, 200, out["httpStatus"])
			},
		},
		{
			name:     "default cap rejects a body over 10 MiB",
			bodySize: tenMiB + 1,
			opts:     nil,
			assert: func(t *testing.T, _ map[string]any, err error) {
				require.ErrorIs(t, err, httpcall.ErrBodyTooLarge)
				assert.False(t, action.IsRetryable(err))
			},
		},
		{
			// The buffered-validator BodyFunc path caps the REQUEST body too: an
			// over-cap body fails NonRetryable before the request is ever sent.
			name:     "over-cap buffered request body fails NonRetryable before send",
			bodySize: 8, // server response is irrelevant; the failure precedes the send
			opts: []httpcall.Option{
				httpcall.WithMaxResponseSize(16),
				httpcall.WithBodyFunc(func(_ context.Context, _ map[string]any) (io.Reader, error) {
					return strings.NewReader(strings.Repeat("b", 64)), nil
				}),
				httpcall.WithBodyValidator(func(_ context.Context, _ []byte, _ map[string]any) error {
					t.Error("validator must not run: the cap must trip while buffering the body")
					return nil
				}),
			},
			assert: func(t *testing.T, _ map[string]any, err error) {
				require.ErrorIs(t, err, httpcall.ErrBodyTooLarge)
				assert.False(t, action.IsRetryable(err), "over-cap request body must be NonRetryable")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := strings.Repeat("a", tc.bodySize)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			opts := append([]httpcall.Option{httpcall.WithBaseURL(srv.URL)}, tc.opts...)
			out, err := httpcall.NewHTTPCall(opts...).Do(t.Context(), map[string]any{})
			tc.assert(t, out, err)
		})
	}
}
