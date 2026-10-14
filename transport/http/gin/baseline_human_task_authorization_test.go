package gin_test

import (
	"encoding/json"
	"net/http"
	"testing"

	ginlib "github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// TestBaseline_TaskClaimRefusalCodes characterizes the error codes of the
// task-route refusals in openspec/specs/human-task-authorization, on the gin
// adapter:
//
//   - "The forged-actor refusal carries the forbidden code";
//   - "Unauthenticated claim is refused identically on every adapter", the row
//     whose body names an eligible actor;
//   - "The pre-empting 401 carries the unauthenticated code", whose gin body is
//     a JSON string where an object belongs.
func TestBaseline_TaskClaimRefusalCodes(t *testing.T) {
	t.Parallel()

	const eligibleActorBody = `{"actor":{"id":"alice","roles":["manager"]}}`

	type testCase struct {
		name string
		opts []httpcore.CustomizeOption[ginlib.IRouter]
		body string
		// assert receives the decoded error envelope.
		assert func(t *testing.T, status int, envelope map[string]any)
	}

	authenticateBob := func(gc *ginlib.Context) {
		gc.Request = gc.Request.WithContext(authz.ContextWithActor(gc.Request.Context(),
			authz.Actor{ID: "bob", Roles: []string{"viewer"}}))
		gc.Next()
	}

	wantCode := func(status int, code string) func(t *testing.T, got int, envelope map[string]any) {
		return func(t *testing.T, got int, envelope map[string]any) {
			require.Equal(t, status, got, "envelope=%v", envelope)
			assert.Equal(t, code, envelope["error"])
		}
	}

	cases := []testCase{
		{
			name:   "forged body actor is refused with the forbidden code",
			opts:   []httpcore.CustomizeOption[ginlib.IRouter]{ginadapter.WithMiddleware(authenticateBob)},
			body:   eligibleActorBody,
			assert: wantCode(http.StatusForbidden, "forbidden"),
		},
		{
			name:   "unauthenticated claim naming an eligible actor carries the unauthenticated code",
			body:   eligibleActorBody,
			assert: wantCode(http.StatusUnauthorized, "unauthenticated"),
		},
		{
			name:   "unauthenticated claim with a JSON string body carries the unauthenticated code",
			body:   `"not-json"`,
			assert: wantCode(http.StatusUnauthorized, "unauthenticated"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, token := newApprovalSrv(t, "baseline-claim-codes-gin", tc.opts...)
			body := tc.body
			resp := postClaim(t, srv, token, &body)

			var envelope map[string]any
			require.NoError(t, json.Unmarshal(resp.Body, &envelope), "body=%s", resp.Body)
			tc.assert(t, resp.StatusCode, envelope)
		})
	}
}
