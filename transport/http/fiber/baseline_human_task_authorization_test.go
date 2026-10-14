package fiber_test

import (
	"encoding/json"
	"net/http"
	"testing"

	fiberlib "github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/transport/http/fiber"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// TestBaseline_TaskClaimUnauthenticatedCode characterizes the 401 envelope
// code of an unauthenticated claim in openspec/specs/human-task-authorization,
// on the fiber adapter mounted with no resolver and no authenticating
// middleware:
//
//   - "Unauthenticated claim is refused identically on every adapter", the row
//     whose body names an eligible actor;
//   - "The pre-empting 401 carries the unauthenticated code", whose fiber body
//     is larger than the configured cap.
func TestBaseline_TaskClaimUnauthenticatedCode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		opts []httpcore.CustomizeOption[fiberlib.Router]
		body []byte
		// assert receives the decoded error envelope.
		assert func(t *testing.T, status int, envelope map[string]any)
	}

	wantUnauthenticated := func(t *testing.T, status int, envelope map[string]any) {
		require.Equal(t, http.StatusUnauthorized, status, "envelope=%v", envelope)
		assert.Equal(t, "unauthenticated", envelope["error"])
	}

	cases := []testCase{
		{
			name:   "body naming an eligible actor",
			body:   []byte(`{"actor":{"id":"alice","roles":["manager"]}}`),
			assert: wantUnauthenticated,
		},
		{
			name:   "body larger than the configured cap",
			opts:   []httpcore.CustomizeOption[fiberlib.Router]{fiber.WithMaxBodyBytes(1 << 20)},
			body:   startBody(2 << 20),
			assert: wantUnauthenticated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, svc := transporttest.NewHarness(t, transporttest.ApprovalProcess())
			taskID := transporttest.StartedApprovalInstance(t, h, "baseline-claim-401-fiber")

			app := newApp()
			fiber.Mount(app, svc, tc.opts...) // no WithRequestActor

			status, raw := appDo(t, app, newRawRequest(t, http.MethodPost, "/tasks/"+taskID+"/claim", tc.body, ""))

			var envelope map[string]any
			require.NoError(t, json.Unmarshal([]byte(raw), &envelope), "body=%.120s", raw)
			tc.assert(t, status, envelope)
		})
	}
}
