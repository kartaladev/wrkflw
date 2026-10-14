// Internal for the same reason as redirect_internal_test.go: driving a hop to a
// subdomain end-to-end needs DNS for that name, so the policy function is called
// directly.
package httpcall

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBaselineRedirectPolicySubdomainIsCrossHost characterizes "The boundary is
// hostname equality, not Go's subdomain rule" of the outbound-http-calls
// capability (openspec/specs/outbound-http-calls/spec.md): Go's own header rule
// treats a subdomain as the same site, but the default redirect policy refuses
// it as a different hostname. The elsewhere.example row is
// TestDefaultRedirectPolicy_Scheme.
func TestBaselineRedirectPolicySubdomainIsCrossHost(t *testing.T) {
	t.Parallel()

	req, via := hop(t, []string{"https://api.internal/thing"}, "https://sub.api.internal/thing")
	err := refuseUnsafeRedirect(req, via)

	assert.ErrorIs(t, err, ErrCrossHostRedirect,
		"a subdomain is a different hostname, so the hop is refused")
}
