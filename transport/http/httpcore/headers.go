package httpcore

// ContentTypeOptionsHeader and NoSniff name the response header that tells a
// browser to trust the declared Content-Type instead of inferring one from the
// bytes.
//
// Every endpoint here answers application/json, and MIME sniffing is what turns
// a JSON response into something else: a body whose leading bytes read as HTML
// can be rendered as HTML by a sniffing browser, and these responses embed
// caller-supplied values — process variables, task payloads, the `message` on a
// 4xx envelope — so an attacker has influence over exactly the bytes a sniffer
// inspects. Declaring the type and then letting it be second-guessed is the
// whole gap this closes.
//
// It is set in each adapter's per-route wrapper (stdlib and gin `observe`,
// fiber `observed`), which every route is registered through — all 27 stdlib
// routes, 26 gin, 26 fiber, health probes included — so a route added later
// cannot be registered without it.
//
// ⚠ SCOPE, stated precisely because it is narrower than it looks: this covers
// every response THIS PACKAGE'S HANDLERS WRITE, and nothing else. A consumer
// middleware registered through the adapters' WithMiddleware (or any
// [CustomizeConfig.Wrap] composition) runs OUTSIDE the route wrapper, so a
// middleware that short-circuits — auth returning 401, a rate limiter
// returning 429, panic recovery returning 500 — answers without ever reaching
// this code, and its response carries no nosniff. Measured on gin: a
// WithMiddleware calling AbortWithStatusJSON yields `X-Content-Type-Options: ""`.
//
// Setting it in Wrap instead would cover more, not less; the two are not
// alternatives and Set is idempotent. Doing so means this library injecting
// middleware into a consumer's router, which is a posture decision rather than
// a header fix, and cannot be done uniformly anyway — *http.ServeMux has no
// middleware seam, so a stdlib consumer wraps the mux beyond this package's
// reach. Left open deliberately rather than half-solved.
const (
	ContentTypeOptionsHeader = "X-Content-Type-Options"
	NoSniff                  = "nosniff"
)
