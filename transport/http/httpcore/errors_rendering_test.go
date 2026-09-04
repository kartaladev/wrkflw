// Package httpcore_test — which ClassifyError arms render the wrapped chain.
package httpcore_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// internalMarker stands in for anything a wrapped error might carry that a
// client must not see — a driver message, a DSN, a filesystem path, a policy
// expression. Deliberately unmistakable in a failure diff.
const internalMarker = "INTERNAL-DETAIL-a7f3c2"

// TestClassifyError_ChainRenderingPerArm pins WHICH arms render the wrapped
// chain into the client's body and which do not. This is the enforcement #69
// asked for, and it is worth being precise about what it can and cannot do.
//
// # What it asserts
//
// Each arm gets a row supplying an error that both matches it and carries
// [internalMarker] in its chain; the row asserts whether the marker reaches
// ErrorBody.Message. Add an arm without a row, or change what an arm renders,
// and this fails.
//
// # What it deliberately does NOT assert
//
// It does not assert "internal detail never reaches a client", and no test at
// this layer can. ClassifyError sees one error; it cannot distinguish a
// sentinel's own actionable text from a driver message someone wrapped into it.
// The 4xx arms render err.Error() ON PURPOSE — that specificity is what makes a
// 4xx body worth sending ("instance %q is in a terminal state") — so a row
// asserting the marker is ABSENT from the 404 arm would assert against the
// design.
//
// The invariant has two halves and only one lives here:
//
//   - HERE: exactly which arms render the chain. Enforced.
//   - AT EVERY WRAPPING SITE: nothing carrying internal detail may wrap a
//     sentinel belonging to a rendering arm. NOT enforceable from here — it is
//     a property of each fmt.Errorf call site. This test's job is to make the
//     consequence of getting that wrong explicit and greppable.
//
// ⚠ #69 was filed believing every wrapping site was safe. The audit for this
// change found two that were not: authz.RoleAuthorizer.Authorize and the casbin
// authorizer each wrapped an expression-evaluation failure in
// authz.ErrNotAuthorized, and every evaluator error embeds the predicate SOURCE
// verbatim — so a denied caller received the deployment's own authorization
// rule in a 403 body. Both are fixed; the last row pins the fix. Read the
// rendering rows below as a live hazard list, not as decoration.
//
// No ctx modifier: ClassifyError is a pure function of one error.
func TestClassifyError_ChainRenderingPerArm(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		err  error
		// wantStatus is asserted so a row cannot silently stop exercising the
		// arm it was written for.
		wantStatus int
		// rendersChain is the property under test: does this arm put the
		// wrapped chain in front of the client?
		rendersChain bool
	}

	// wrap builds an error matching sentinel whose chain also carries the
	// marker, the way a real call site wraps a downstream failure.
	wrap := func(sentinel error) error {
		return fmt.Errorf("%w: %s", sentinel, internalMarker)
	}

	cases := []testCase{
		{
			// ⚠ The one 4xx that does NOT render the chain — its message is a
			// fixed string. Easy to misread as an oversight when scanning the
			// switch; it is not.
			name:         "401 unauthenticated renders a static message",
			err:          wrap(httpcore.ErrUnauthenticated),
			wantStatus:   http.StatusUnauthorized,
			rendersChain: false,
		},
		{
			name:         "503 identity unavailable sends no message at all",
			err:          wrap(httpcore.ErrIdentityUnavailable),
			wantStatus:   http.StatusServiceUnavailable,
			rendersChain: false,
		},
		{
			name:         "404 not found renders the chain",
			err:          wrap(kernel.ErrInstanceNotFound),
			wantStatus:   http.StatusNotFound,
			rendersChain: true,
		},
		{
			name:         "403 forbidden renders the chain",
			err:          wrap(authz.ErrNotAuthorized),
			wantStatus:   http.StatusForbidden,
			rendersChain: true,
		},
		{
			name:         "409 concurrent update renders the chain",
			err:          wrap(kernel.ErrConcurrentUpdate),
			wantStatus:   http.StatusConflict,
			rendersChain: true,
		},
		{
			// ⚠ The arm's own comment says it must not inherit the err.Error()
			// pattern by accident. This row is what makes that stick.
			name:         "413 request too large renders a static message",
			err:          wrap(httpcore.ErrRequestBodyTooLarge),
			wantStatus:   http.StatusRequestEntityTooLarge,
			rendersChain: false,
		},
		{
			name:         "400 bad input renders the chain",
			err:          wrap(httpcore.ErrBadInput),
			wantStatus:   http.StatusBadRequest,
			rendersChain: true,
		},
		{
			name:         "422 conflict state renders the chain",
			err:          wrap(service.ErrConflict),
			wantStatus:   http.StatusUnprocessableEntity,
			rendersChain: true,
		},
		{
			name:         "500 default sends no message at all",
			err:          fmt.Errorf("some driver blew up: %s", internalMarker),
			wantStatus:   http.StatusInternalServerError,
			rendersChain: false,
		},
		{
			// The #69 regression, stated at the layer where a reader would meet
			// it: authz must not report a broken policy as a denial, because on
			// the 403 arm above the predicate source would be rendered.
			name:         "an unevaluable authz predicate classifies 500, not 403",
			err:          fmt.Errorf("workflow-authz: attribute predicate: %s", internalMarker),
			wantStatus:   http.StatusInternalServerError,
			rendersChain: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := httpcore.ClassifyError(tc.err)

			require.Equal(t, tc.wantStatus, status,
				"row must still exercise the arm it was written for (body=%+v)", body)

			if tc.rendersChain {
				assert.Contains(t, body.Message, internalMarker,
					"this arm renders err.Error() BY DESIGN, so the invariant it rests on "+
						"— nothing carrying internal detail wraps its sentinel — must be "+
						"maintained at every wrapping site, not here")
				return
			}
			assert.NotContains(t, body.Message, internalMarker,
				"this arm must never put the wrapped chain in front of a client")
		})
	}
}
