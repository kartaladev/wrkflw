package stdlib_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// TestBaseline_TaskClaimRefusalCodes characterizes the error codes of two
// task-route refusals in openspec/specs/human-task-authorization, on the
// stdlib adapter:
//
//   - "The forged-actor refusal carries the forbidden code": the resolver
//     authenticates an ineligible "bob" while the body names an eligible
//     manager; the 403 envelope's code is "forbidden".
//   - "Unauthenticated claim is refused identically on every adapter", the
//     row whose body names an eligible actor: mounted with no resolver and no
//     authenticating middleware, the 401 envelope's code is "unauthenticated".
func TestBaseline_TaskClaimRefusalCodes(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// authenticate wraps the mux; nil leaves the mount bare.
		authenticate func(next http.Handler) http.Handler
		assert       func(t *testing.T, status int, envelope map[string]any)
	}

	cases := []testCase{
		{
			name: "forged body actor is refused with the forbidden code",
			authenticate: func(next http.Handler) http.Handler {
				return withActorMiddleware(authz.Actor{ID: "bob", Roles: []string{"viewer"}}, next)
			},
			assert: func(t *testing.T, status int, envelope map[string]any) {
				require.Equal(t, http.StatusForbidden, status, "envelope=%v", envelope)
				assert.Equal(t, "forbidden", envelope["error"])
			},
		},
		{
			name: "unauthenticated claim naming an eligible actor carries the unauthenticated code",
			assert: func(t *testing.T, status int, envelope map[string]any) {
				require.Equal(t, http.StatusUnauthorized, status, "envelope=%v", envelope)
				assert.Equal(t, "unauthenticated", envelope["error"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, svc := transporttest.NewHarness(t, transporttest.ApprovalProcess())
			taskID := transporttest.StartedApprovalInstance(t, h, "baseline-claim-codes-stdlib")

			mux := http.NewServeMux()
			stdlib.Mount(mux, svc) // no WithRequestActor
			var handler http.Handler = mux
			if tc.authenticate != nil {
				handler = tc.authenticate(mux)
			}

			rr := doHandler(handler, newPostRequest(t, "/tasks/"+taskID+"/claim", map[string]any{
				"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
			}))

			var envelope map[string]any
			decodeJSON(t, rr.Body, &envelope)
			tc.assert(t, rr.Code, envelope)
		})
	}
}
