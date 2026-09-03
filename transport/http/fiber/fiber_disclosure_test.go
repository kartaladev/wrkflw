// Package fiber_test — disclosure parity with the stdlib adapter.
package fiber_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/fiber"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

const discloseSecret = "111-22-3333"

func newDiscloseApp(t *testing.T, opts ...httpcore.CustomizeOption[fiberlib.Router]) (*fiberlib.App, string) {
	t.Helper()

	def := transporttest.ApprovalProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("approval"),
		Vars:   map[string]any{"ssn": discloseSecret},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// ⚠ Claim the task with an actor whose ID IS the canary. Without this the
	// /actionable case is VACUOUS: that view renders no variables at all, so an
	// assertion that the secret is absent passes whether or not the projection runs.
	// Proven by ablation — with the wiring removed, three of four cases went RED and
	// actionable stayed green.
	for _, tk := range pi.State().Tasks {
		if _, cerr := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
			TaskID: tk.TaskID,
			Actor:  authz.Actor{ID: discloseSecret, Roles: []string{"manager"}},
		}); cerr != nil {
			t.Fatalf("claim: %v", cerr)
		}
	}
	app := newApp()
	fiber.Mount(app, svc, opts...)
	return app, pi.State().InstanceID
}

// TestDisclosureParity_Fiber asserts fiber behaves exactly as stdlib and gin.
//
// ⚠ fasthttp has already read the whole body before the handler runs, and fiber's context
// plumbing differs from net/http's — so "the three share a core" is an assumption worth
// executing rather than asserting.
func TestDisclosureParity_Fiber(t *testing.T) {
	t.Parallel()

	app, id := newDiscloseApp(t)

	cases := []struct {
		name string
		req  func() *http.Request
	}{
		{"plain read", func() *http.Request { return newGetRequest(t, "/instances/"+id) }},
		{"snapshot", func() *http.Request { return newGetRequest(t, "/instances/"+id+"/snapshot") }},
		{"actionable", func() *http.Request { return newGetRequest(t, "/instances/"+id+"/actionable") }},
		{"signal", func() *http.Request {
			return newPostRequest(t, "/instances/"+id+"/signals",
				map[string]any{"signal": "no-such-waiter"})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := appDo(t, app, tc.req())
			if status >= 500 {
				t.Fatalf("unexpected %d: %s", status, body)
			}
			if strings.Contains(body, discloseSecret) {
				t.Errorf("%s discloses %q unauthenticated\nstatus=%d body=%s",
					tc.name, discloseSecret, status, body)
			}
			if status == http.StatusOK && !strings.Contains(body, id) {
				t.Errorf("%s dropped the instance id — the projection is too aggressive\nbody=%s",
					tc.name, body)
			}
		})
	}
}

func TestDisclosureParity_Fiber_IdentifiedSeesEverything(t *testing.T) {
	t.Parallel()

	app, id := newDiscloseApp(t, fiber.WithRequestActor(
		func(context.Context) (authz.Actor, error) {
			return authz.Actor{ID: "alice", Roles: []string{"manager"}}, nil
		}))

	_, body := appDo(t, app, newGetRequest(t, "/instances/"+id))
	if !strings.Contains(body, discloseSecret) {
		t.Errorf("an identified caller must receive full fidelity\nbody=%s", body)
	}
}

func TestDisclosureParity_Fiber_DiscloseAll(t *testing.T) {
	t.Parallel()

	app, id := newDiscloseApp(t, httpcore.WithDisclosure[fiberlib.Router](authz.DiscloseAll))

	_, body := appDo(t, app, newGetRequest(t, "/instances/"+id))
	if !strings.Contains(body, discloseSecret) {
		t.Errorf("DiscloseAll must restore the prior shape\nbody=%s", body)
	}
}
