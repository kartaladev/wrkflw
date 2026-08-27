// Package gin_test — ADR-0190 disclosure parity with the stdlib adapter.
package gin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ginlib "github.com/gin-gonic/gin"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

const discloseSecret = "111-22-3333"

// newDiscloseSrv mounts the full route set and seeds one instance carrying the canary.
func newDiscloseSrv(t *testing.T, opts ...httpcore.CustomizeOption[ginlib.IRouter]) (*httptest.Server, string) {
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
	r := ginlib.New()
	ginadapter.Mount(r, svc, opts...)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, pi.State().InstanceID
}

// TestDisclosureParity_Gin asserts gin behaves exactly as stdlib on every entry point.
//
// All three adapters dispatch to an identical 29-member set of httpcore functions, so this
// SHOULD pass once the shared core is wired — and if it does not, the shared-core assumption
// is wrong and that is a finding, not a formality.
func TestDisclosureParity_Gin(t *testing.T) {
	t.Parallel()

	srv, id := newDiscloseSrv(t)

	cases := []struct {
		name string
		do   func() httpResp
	}{
		{"plain read", func() httpResp { return get(t, srv, "/instances/"+id) }},
		{"snapshot", func() httpResp { return get(t, srv, "/instances/"+id+"/snapshot") }},
		{"actionable", func() httpResp { return get(t, srv, "/instances/"+id+"/actionable") }},
		{"signal", func() httpResp {
			return post(t, srv, "/instances/"+id+"/signals",
				map[string]any{"signal": "no-such-waiter"})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := tc.do()
			body := string(resp.Body)
			if resp.StatusCode >= 500 {
				t.Fatalf("unexpected %d: %s", resp.StatusCode, body)
			}
			if strings.Contains(body, discloseSecret) {
				t.Errorf("%s discloses %q unauthenticated\nstatus=%d body=%s",
					tc.name, discloseSecret, resp.StatusCode, body)
			}
			if resp.StatusCode == http.StatusOK && !strings.Contains(body, id) {
				t.Errorf("%s dropped the instance id — the projection is too aggressive\nbody=%s",
					tc.name, body)
			}
		})
	}
}

func TestDisclosureParity_Gin_IdentifiedSeesEverything(t *testing.T) {
	t.Parallel()

	srv, id := newDiscloseSrv(t, ginadapter.WithRequestActor(
		func(context.Context) (authz.Actor, error) {
			return authz.Actor{ID: "alice", Roles: []string{"manager"}}, nil
		}))

	if body := string(get(t, srv, "/instances/"+id).Body); !strings.Contains(body, discloseSecret) {
		t.Errorf("an identified caller must receive full fidelity\nbody=%s", body)
	}
}

func TestDisclosureParity_Gin_DiscloseAll(t *testing.T) {
	t.Parallel()

	srv, id := newDiscloseSrv(t, httpcore.WithDisclosure[ginlib.IRouter](authz.DiscloseAll))

	if body := string(get(t, srv, "/instances/"+id).Body); !strings.Contains(body, discloseSecret) {
		t.Errorf("DiscloseAll must restore the prior shape\nbody=%s", body)
	}
}
