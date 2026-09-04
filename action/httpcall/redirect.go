package httpcall

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrCrossHostRedirect is returned (non-retryable) when the DEFAULT client is
// asked to follow a redirect whose Location names a different host than the
// request it is answering.
//
// A client supplied through [WithHTTPClient] never produces it: that client is
// used exactly as given and keeps whatever CheckRedirect it carries, which for a
// bare &http.Client{} is Go's default of up to 10 hops to any host.
var ErrCrossHostRedirect = errors.New("workflow-httpcall: redirect crosses to a different host")

// maxRedirects mirrors the cap Go's own default policy applies. Setting
// CheckRedirect replaces that default wholesale — including its hop limit — so
// the limit has to be restated here or same-host redirects would loop until the
// client Timeout fired.
const maxRedirects = 10

// refuseCrossHostRedirect is the default client's CheckRedirect: it allows a
// redirect that stays on the same host, up to [maxRedirects] hops, and refuses
// one that changes host.
//
// # Why the default is not Go's
//
// This action's request URL can come from process data ([WithURLExpr]) and its
// response body is mapped into output variables. Go's default therefore hands
// anyone who can influence a process variable a read primitive against whatever
// the engine can reach: point the call at a host they control, answer 302 to an
// internal address, and the response returns as workflow data. Refusing the
// cross-host hop removes the redirect half of that. It does NOT remove the
// direct half — a URL expression evaluating straight to an internal address was
// never a redirect problem, and [WithURLExpr] already tells callers to validate
// what they build.
//
// # The boundary is the HOSTNAME, not the origin
//
// A port change on one hostname is followed, and so is an http→https upgrade.
// Both are ordinary, and neither is the shape this guards against — that shape
// is an EXTERNAL host redirecting INWARD. Comparing origins instead would refuse
// scheme-upgrade redirects, which are common and strictly improve transport
// security, in exchange for nothing against this threat.
//
// ⚠ Two consequences of that choice, both MEASURED, both deliberate:
//
//   - A redirect between two services on one host (127.0.0.1:8080 → :9090) is
//     allowed, and Go does not strip Authorization across it, because Go
//     compares url.Hostname() too. On a container host where loopback spans many
//     services, a credential will follow such a hop.
//   - Where a cross-host redirect IS followed — only via [WithHTTPClient] — Go
//     strips exactly Authorization, Www-Authenticate, Cookie and Cookie2. Any
//     other credential header (X-Api-Key, a vendor bearer header, anything a
//     [WithHeaderFunc] invented) crosses untouched.
//
// TestHTTPCall_DefaultRedirectPolicy and
// TestHTTPCall_HeadersSurvivingACrossHostRedirect pin both.
//
// ⚠ The returned error is wrapped by [http.Client] in a *url.Error, which
// unwraps, so errors.Is(err, ErrCrossHostRedirect) still holds at the caller.
// Do classifies it non-retryable explicitly: every other client.Do failure in
// this package is treated as transport trouble and retried, and a refused
// redirect fails identically every time.
func refuseCrossHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("workflow-httpcall: stopped after %d redirects", maxRedirects)
	}
	// via[0] is the ORIGINAL request, not the previous hop.
	//
	// The two happen to be equivalent here — every hop must match its
	// predecessor, so by induction every hop matches the origin, and no chain
	// can walk to a new host one step at a time. MEASURED: swapping this for
	// via[len(via)-1] changes no test, because it changes no behaviour.
	//
	// The origin is still the right reference to write, because it states the
	// invariant directly ("no hop leaves the host the caller asked for") instead
	// of relying on that induction holding after some later edit.
	origin := via[0].URL.Hostname()
	if req.URL.Hostname() != origin {
		return fmt.Errorf("%w: %s -> %s", ErrCrossHostRedirect, origin, req.URL.Hostname())
	}
	return nil
}
