// Package httpcall — white-box tests for the default redirect policy.
//
// ⚠ These are internal (package httpcall, not httpcall_test) deliberately.
// The scheme rules below need an https ORIGIN, and driving one end-to-end
// through the default client is not possible: that client carries
// [newPooledTransport], whose TLS config does not trust httptest's generated
// certificate, and nothing in this package's option set exposes a root pool.
// Calling the policy function directly tests exactly the decision under test
// with no TLS plumbing standing in for it.
//
// The end-to-end behaviour that CAN be driven — cross-host refusal, the hop cap,
// the injected-client escape hatch — is covered from the black-box side in
// redirect_test.go, and Go's own header handling across a real downgrade is
// measured there too.
package httpcall

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
)

// hop builds the (req, via) pair the http.Client hands CheckRedirect: via is the
// chain already walked, oldest first, and req is the hop being proposed.
func hop(t *testing.T, chain []string, next string) (*http.Request, []*http.Request) {
	t.Helper()
	require.NotEmpty(t, chain, "via always holds at least the original request")

	via := make([]*http.Request, 0, len(chain))
	for _, raw := range chain {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		via = append(via, &http.Request{URL: u})
	}
	u, err := url.Parse(next)
	require.NoError(t, err)
	return &http.Request{URL: u}, via
}

// TestDefaultRedirectPolicy_Scheme pins the scheme half of the policy, decided
// on #84 beside the host half from #67.
//
// # The exposure
//
// #67 chose HOSTNAME as its boundary, which is right for the threat it
// addressed — an external host redirecting inward. But hostname equality is
// symmetric, and one direction is not benign:
//
//	https://api.internal/thing  →  302  →  http://api.internal/thing
//
// Same hostname, so #67's check permits it. Go's own header rule is
// hostname-based too, so nothing is stripped. MEASURED against a real TLS
// origin on the pinned toolchain: the plaintext target received
// Authorization, Cookie AND X-Api-Key — every header the call carried, not
// merely the ones outside Go's strip list. The request leaves the process
// unencrypted with the credentials intact.
//
// # The rule
//
// A hop may not move from https to http. Upgrades are permitted, and so is a
// chain that was plaintext to begin with.
//
// ⚠ Compared against via[0], the ORIGIN — and unlike the host check, that is
// NOT merely a stylistic choice here. The two references genuinely differ for
// http → https → http: relative to the previous hop that final step is a
// downgrade, but relative to the origin it is not, and refusing it would be
// over-strict. A caller who began in plaintext never had a confidentiality
// guarantee for this rule to preserve, and the credential was already exposed
// on hop one. The last row pins that.
//
// No ctx modifier: the policy is a pure function of a request and its chain.
func TestDefaultRedirectPolicy_Scheme(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		chain  []string
		next   string
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:  "https to http on one hostname is refused",
			chain: []string{"https://api.internal/thing"},
			next:  "http://api.internal/thing",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrDowngradeRedirect,
					"a credential would otherwise go out in cleartext")
				assert.NotErrorIs(t, err, ErrCrossHostRedirect,
					"the host is unchanged; borrowing that sentinel would misreport why")
			},
		},
		{
			name:  "http to https on one hostname is allowed",
			chain: []string{"http://api.internal/thing"},
			next:  "https://api.internal/thing",
			assert: func(t *testing.T, err error) {
				assert.NoError(t, err, "an upgrade improves transport security; refusing it buys nothing")
			},
		},
		{
			name:  "https to https is allowed",
			chain: []string{"https://api.internal/a"},
			next:  "https://api.internal/b",
			assert: func(t *testing.T, err error) {
				assert.NoError(t, err)
			},
		},
		{
			name:  "http to http is allowed",
			chain: []string{"http://api.internal/a"},
			next:  "http://api.internal/b",
			assert: func(t *testing.T, err error) {
				assert.NoError(t, err)
			},
		},
		{
			name:  "a downgrade is refused even when the port also changes",
			chain: []string{"https://api.internal:8443/thing"},
			next:  "http://api.internal:8080/thing",
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrDowngradeRedirect,
					"the port is not what makes this unsafe, and a differing port "+
						"must not route it to the host check instead")
			},
		},
		{
			// ⚠ THE ROW THAT DISTINGUISHES via[0] FROM the previous hop.
			// Against the previous hop (https) this final step reads as a
			// downgrade and would be refused. Against the origin (http) it is
			// not one, and must be allowed: the call was plaintext from the
			// start, so there is no confidentiality this rule could preserve
			// and the credential was already exposed on hop one.
			name:  "a chain that began plaintext may return to plaintext",
			chain: []string{"http://api.internal/a", "https://api.internal/b"},
			next:  "http://api.internal/c",
			assert: func(t *testing.T, err error) {
				assert.NoError(t, err,
					"refusing here would be over-strict: the origin was never https")
			},
		},
		{
			name:  "the host check still applies alongside the scheme check",
			chain: []string{"https://api.internal/thing"},
			next:  "https://elsewhere.example/thing",
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrCrossHostRedirect)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, via := hop(t, tc.chain, tc.next)
			tc.assert(t, refuseUnsafeRedirect(req, via))
		})
	}
}

// TestDo_RefusedDowngradeIsNonRetryable pins that a refused downgrade reaches
// the caller classified NON-RETRYABLE.
//
// ⚠ It exists because a mutation found the gap: deleting ErrDowngradeRedirect
// from Do's classification check broke no test. Every client.Do failure in this
// package is retryable by default, so without that line the runtime would keep
// re-issuing a request that fails identically each time — and the table above
// could not see it, because it calls the policy function rather than Do.
//
// ⚠ The client is INJECTED, which normally means "no package policy", and that
// is not a contradiction here: it is assembled with this package's own
// refuseUnsafeRedirect plus the test server's TLS trust. That combination is the
// only way to drive a real https→http hop through Do — the default client's
// transport does not trust httptest's certificate, and no option exposes a root
// pool. What is under test is Do's classification of the refusal, not which
// client carries the policy.
func TestDo_RefusedDowngradeIsNonRetryable(t *testing.T) {
	t.Parallel()

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(plain.Close)

	tlsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/next", http.StatusFound)
	}))
	t.Cleanup(tlsOrigin.Close)

	client := tlsOrigin.Client()
	client.CheckRedirect = refuseUnsafeRedirect

	act := NewHTTPCall(WithBaseURL(tlsOrigin.URL), WithHTTPClient(client))
	_, err := act.Do(t.Context(), map[string]any{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDowngradeRedirect,
		"http.Client wraps a CheckRedirect error in *url.Error, which unwraps")
	assert.False(t, action.IsRetryable(err),
		"a refused downgrade is deterministic; retrying it only re-issues the request")
}
