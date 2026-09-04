// Package httpcall_test — the default redirect policy and what crosses a hop.
package httpcall_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/action/httpcall"
)

// crossHostURL re-addresses a httptest server by a DIFFERENT hostname.
//
// ⚠ This indirection is the whole reason the tests below are meaningful. Every
// httptest server listens on 127.0.0.1, so two of them differ only by PORT — and
// a port is not a host boundary to either Go or this package's policy. Swapping
// the literal for "localhost" keeps the connection on loopback while making the
// hop genuinely cross-host, because both Go's header stripping and this
// package's CheckRedirect compare url.Hostname().
func crossHostURL(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	out := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	require.NotEqual(t, srv.URL, out, "httptest server was not on 127.0.0.1 as assumed")
	return out
}

// redirectTo returns a server that answers every request with a 302 to loc.
func redirectTo(t *testing.T, loc string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, loc, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// recordingTarget returns a server that records the headers it received and
// answers 200 with a small JSON body.
func recordingTarget(t *testing.T, got *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"reached":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestHTTPCall_DefaultRedirectPolicy pins the default decided on #67: the
// default client follows redirects that stay on the same host and REFUSES one
// that changes host.
//
// # Why the default changed
//
// This action's request URL can come from process data ([WithURLExpr]), and its
// response body is mapped into output variables. Go's default — up to 10 hops to
// whatever host Location names — therefore gives an attacker who can influence a
// process variable a read primitive against anything the engine can reach: point
// the call at a host they control, answer 302 to an internal address, and the
// response comes back as workflow data.
//
// # Where the boundary is, precisely
//
// HOSTNAME, not origin. A redirect from :8080 to :9090 on the same hostname is
// followed, and an http→https upgrade on the same hostname is followed — both are
// ordinary and neither crosses the boundary that matters here. Refusing on port
// would break scheme-upgrade redirects for no gain against this threat: reaching
// another port on a host the operator already pointed at is not the SSRF shape,
// which is an EXTERNAL host redirecting INWARD.
//
// ⚠ An injected client ([WithHTTPClient]) is NOT given this policy, consistent
// with that option's existing contract that the client is used exactly as given.
// A consumer who wants Go's old behaviour back passes their own client; that is
// the documented escape hatch and the last row pins it.
//
// No ctx modifier: the rows exercise redirect handling, not cancellation, and
// Do already receives t.Context() below.
func TestHTTPCall_DefaultRedirectPolicy(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// build wires the servers and returns the options for the action under
		// test, so each row controls its own topology.
		build  func(t *testing.T) []httpcall.Option
		assert func(t *testing.T, out map[string]any, err error)
	}

	cases := []testCase{
		{
			name: "no redirect is unaffected",
			build: func(t *testing.T) []httpcall.Option {
				var got http.Header
				target := recordingTarget(t, &got)
				return []httpcall.Option{httpcall.WithBaseURL(target.URL)}
			},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err)
				assert.Equal(t, 200, out["httpStatus"])
			},
		},
		{
			name: "same-host redirect across PORTS is followed",
			build: func(t *testing.T) []httpcall.Option {
				var got http.Header
				target := recordingTarget(t, &got)
				origin := redirectTo(t, target.URL+"/next")
				return []httpcall.Option{httpcall.WithBaseURL(origin.URL)}
			},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err, "a port change on one hostname must stay followable")
				assert.Equal(t, 200, out["httpStatus"])
			},
		},
		{
			// ⚠ Guards the hop cap, which had to be RESTATED in the policy:
			// setting CheckRedirect replaces Go's default wholesale, its 10-hop
			// limit included. Without the restatement this row does not fail
			// fast — it spins on a same-host loop until the 30s client Timeout,
			// so a missing cap shows up as a slow suite rather than a red one.
			name: "a same-host redirect loop stops at the hop cap",
			build: func(t *testing.T) []httpcall.Option {
				var srv *httptest.Server
				srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, srv.URL+"/again", http.StatusFound)
				}))
				t.Cleanup(srv.Close)
				return []httpcall.Option{httpcall.WithBaseURL(srv.URL)}
			},
			assert: func(t *testing.T, _ map[string]any, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "stopped after 10 redirects")
				assert.NotErrorIs(t, err, httpcall.ErrCrossHostRedirect,
					"a hop-count refusal is not a cross-host refusal and must not "+
						"borrow its sentinel")
			},
		},
		{
			name: "cross-host redirect is refused",
			build: func(t *testing.T) []httpcall.Option {
				var got http.Header
				target := recordingTarget(t, &got)
				origin := redirectTo(t, crossHostURL(t, target)+"/next")
				return []httpcall.Option{httpcall.WithBaseURL(origin.URL)}
			},
			assert: func(t *testing.T, _ map[string]any, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, httpcall.ErrCrossHostRedirect,
					"the refusal must carry a sentinel a consumer can match on")
				// ⚠ Not merely "an error". Every client.Do failure in this
				// package is retryable by default, so without an explicit
				// classification the runtime would retry a redirect that fails
				// identically every time.
				assert.False(t, action.IsRetryable(err),
					"a refused redirect is deterministic; retrying it is pure waste")
			},
		},
		{
			name: "an injected client keeps Go's default and follows cross-host",
			build: func(t *testing.T) []httpcall.Option {
				var got http.Header
				target := recordingTarget(t, &got)
				origin := redirectTo(t, crossHostURL(t, target)+"/next")
				return []httpcall.Option{
					httpcall.WithBaseURL(origin.URL),
					httpcall.WithHTTPClient(&http.Client{}),
				}
			},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err,
					"WithHTTPClient promises the client is used EXACTLY as given; "+
						"imposing a policy on it would break that contract")
				assert.Equal(t, 200, out["httpStatus"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			act := httpcall.NewHTTPCall(tc.build(t)...)
			out, err := act.Do(t.Context(), map[string]any{})
			tc.assert(t, out, err)
		})
	}
}

// TestHTTPCall_HeadersSurvivingACrossHostRedirect records what Go does and does
// not strip when a redirect changes host.
//
// # Why this test exists even though the default now refuses such redirects
//
// It documents the exposure a consumer takes on when they use the escape hatch —
// [WithHTTPClient] with Go's default policy — which is the only way a cross-host
// hop still happens. It is therefore a test of the ADVICE, not of this package's
// default path, and it must keep passing for that advice to stay true.
//
// # The measurement
//
// #67 supposed that "an Authorization value added by a header func can travel to
// the redirect target". MEASURED: it does not. Go strips Authorization, and does
// so identically whether the value came from [WithHeader] or [WithHeaderFunc] —
// the stripping is by header NAME, so who set it is irrelevant.
//
// What does travel is every OTHER header, and that is the real exposure. Go's
// strip list is six names — Authorization, Www-Authenticate, Cookie, Cookie2,
// Proxy-Authorization, Proxy-Authenticate (net/http/client.go, go1.26.8) — and
// what matters is that it is FIXED, not how long it is: a credential named
// anything outside it, X-Api-Key or a vendor bearer header, crosses untouched.
// The package doc's own example for [WithHeaderFunc] is fetching a short-lived
// auth token, and nothing obliges that token to be spelled "Authorization".
//
// ⚠ A second finding, and the sharper one: a PORT is not a boundary to Go's
// rule, so a redirect from :8080 to :9090 on ONE hostname is not "cross-host"
// and Authorization is NOT stripped. On a container host where 127.0.0.1 spans
// many services, an Authorization header will follow a redirect between them.
//
// ⚠ Go's rule is also subdomain-PERMISSIVE — it ends in isDomainOrSubdomain, so
// foo.com → sub.foo.com keeps Authorization. This package's default refuses that
// hop, being hostname equality, so the two do not agree and the package default
// is the stricter.
func TestHTTPCall_HeadersSurvivingACrossHostRedirect(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// crossHost selects whether the hop changes hostname or only port.
		crossHost bool
		// viaFunc sets Authorization through WithHeaderFunc instead of WithHeader.
		viaFunc bool
		assert  func(t *testing.T, received http.Header)
	}

	cases := []testCase{
		{
			name:      "cross-host strips Authorization and Cookie, keeps custom headers",
			crossHost: true,
			viaFunc:   false,
			assert: func(t *testing.T, received http.Header) {
				assert.Empty(t, received.Get("Authorization"), "Go strips Authorization cross-host")
				assert.Empty(t, received.Get("Cookie"), "Go strips Cookie cross-host")
				// ⚠ Proxy-Authorization is on the strip list too. Asserted
				// rather than only cited: an earlier draft of this file stated
				// the list as four names, which was the older Go formulation
				// and wrong on go1.26.8. A doc claim nothing exercises is how
				// that survived.
				assert.Empty(t, received.Get("Proxy-Authorization"),
					"Proxy-Authorization is on Go's strip list as well")
				assert.Equal(t, "APIKEY", received.Get("X-Api-Key"),
					"THE EXPOSURE: Go's strip list is a FIXED set of header names, so a "+
						"credential under any name outside it crosses untouched")
			},
		},
		{
			name:      "a header func gets no different treatment from a static header",
			crossHost: true,
			viaFunc:   true,
			assert: func(t *testing.T, received http.Header) {
				assert.Empty(t, received.Get("Authorization"),
					"#67 supposed a header func's Authorization would travel; it does not — "+
						"stripping is by header NAME, not by who set it")
				assert.Equal(t, "FUNC-SECRET", received.Get("X-Func-Secret"),
					"but its custom header still crosses, exactly as a static one does")
			},
		},
		{
			name:      "a PORT change is not cross-host, so nothing is stripped",
			crossHost: false,
			viaFunc:   false,
			assert: func(t *testing.T, received http.Header) {
				assert.Equal(t, "Bearer STATIC", received.Get("Authorization"),
					"Go compares url.Hostname(), so Authorization follows a redirect "+
						"between two services on one host")
				assert.Equal(t, "APIKEY", received.Get("X-Api-Key"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var received http.Header
			target := recordingTarget(t, &received)

			loc := target.URL
			if tc.crossHost {
				loc = crossHostURL(t, target)
			}
			origin := redirectTo(t, loc+"/next")

			opts := []httpcall.Option{
				httpcall.WithBaseURL(origin.URL),
				httpcall.WithHeader("X-Api-Key", "APIKEY"),
				httpcall.WithHeader("Cookie", "session=COOKIE"),
				httpcall.WithHeader("Proxy-Authorization", "Basic PROXYCRED"),
				// ⚠ Go's default policy, deliberately: this test measures what
				// Go does on a cross-host hop, which the package default now
				// prevents. Using the default client would exercise the refusal
				// instead and assert nothing about headers.
				httpcall.WithHTTPClient(&http.Client{}),
			}
			if tc.viaFunc {
				opts = append(opts, httpcall.WithHeaderFunc(
					func(_ context.Context, hdr http.Header, _ map[string]any) error {
						hdr.Set("Authorization", "Bearer FUNC")
						hdr.Set("X-Func-Secret", "FUNC-SECRET")
						return nil
					}))
			} else {
				opts = append(opts, httpcall.WithHeader("Authorization", "Bearer STATIC"))
			}

			act := httpcall.NewHTTPCall(opts...)
			_, err := act.Do(t.Context(), map[string]any{})
			require.NoError(t, err)

			tc.assert(t, received)
		})
	}
}

// TestHTTPCall_EverythingSurvivesASchemeDowngrade is the measurement behind
// #84: what Go does when a redirect leaves https for http on ONE hostname.
//
// # Why it is not obvious
//
// Go's header stripping is keyed on the HOSTNAME
// (shouldCopyHeaderOnRedirect → isDomainOrSubdomain). A scheme change alone
// therefore triggers nothing at all — not merely "the fixed list is stripped and
// the rest travels", which is the cross-host story, but NOTHING is stripped.
// Authorization included.
//
// It would be easy to infer the opposite from the fact that Go strips
// Authorization on a cross-host hop, or to assume TLS downgrade is special-cased
// the way the Referer header is (client.go does suppress Referer on
// https→http, which makes it look like downgrades are handled). They are not,
// for request headers. Hence a test rather than a reasoned note.
//
// ⚠ Uses the TLS server's OWN client through [WithHTTPClient], for two reasons:
// it is the only client that trusts httptest's generated certificate, and it
// carries Go's default CheckRedirect, so this measures GO rather than this
// package's policy. The package's own refusal of this hop is pinned in
// redirect_internal_test.go, which needs no TLS to state it.
//
// This test therefore also documents what the [WithHTTPClient] escape hatch
// exposes a consumer to, and must keep passing for that documentation to hold.
func TestHTTPCall_EverythingSurvivesASchemeDowngrade(t *testing.T) {
	t.Parallel()

	var received http.Header
	plain := recordingTarget(t, &received)

	tlsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/next", http.StatusFound)
	}))
	t.Cleanup(tlsOrigin.Close)

	require.Contains(t, tlsOrigin.URL, "127.0.0.1")
	require.Contains(t, plain.URL, "127.0.0.1",
		"both servers must share a hostname, or this measures a cross-host hop instead")

	act := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(tlsOrigin.URL),
		httpcall.WithHeader("Authorization", "Bearer SECRET-TOKEN"),
		httpcall.WithHeader("Cookie", "session=SECRET-COOKIE"),
		httpcall.WithHeader("X-Api-Key", "SECRET-APIKEY"),
		httpcall.WithHTTPClient(tlsOrigin.Client()),
	)
	_, err := act.Do(t.Context(), map[string]any{})
	require.NoError(t, err)

	assert.Equal(t, "Bearer SECRET-TOKEN", received.Get("Authorization"),
		"THE EXPOSURE: Authorization reached a PLAINTEXT endpoint. Go strips by "+
			"hostname, and the hostname did not change, so the downgrade stripped nothing")
	assert.Equal(t, "session=SECRET-COOKIE", received.Get("Cookie"))
	assert.Equal(t, "SECRET-APIKEY", received.Get("X-Api-Key"))
}
