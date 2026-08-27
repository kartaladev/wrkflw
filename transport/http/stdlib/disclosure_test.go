// Package stdlib_test — ADR-0190 disclosure posture over the net/http adapter.
package stdlib_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

const discloseSecret = "111-22-3333"

// seedDisclose starts an instance whose variables carry the canary.
func seedDisclose(t *testing.T) (*http.ServeMux, string, service.Service) {
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

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)
	return mux, pi.State().InstanceID, svc
}

// TestUnauthenticatedReadsDoNotDisclose covers every InstanceRoutes entry point that
// renders instance-derived data.
//
// ⚠ POST /instances/{id}/signals is the one that matters most. It ends in the SAME
// mapInstance call as the GET, so before ADR-0190 a caller refused `variables` on the read
// obtained the identical document by changing the verb — a signal matching no waiter is a
// clean no-op that still returns the whole instance. Any per-endpoint fix is wrong by
// construction, which is why the table below is exhaustive rather than illustrative.
func TestUnauthenticatedReadsDoNotDisclose(t *testing.T) {
	t.Parallel()

	mux, id, _ := seedDisclose(t)

	cases := []struct {
		name string
		req  func() *http.Request
	}{
		{"plain read", func() *http.Request {
			return newGetRequest(t, "/instances/"+id)
		}},
		{"snapshot", func() *http.Request {
			return newGetRequest(t, "/instances/"+id+"/snapshot")
		}},
		{"actionable", func() *http.Request {
			return newGetRequest(t, "/instances/"+id+"/actionable")
		}},
		{"signal (same render path as the GET)", func() *http.Request {
			return newPostRequest(t, "/instances/"+id+"/signals",
				`{"signal":"no-such-waiter"}`)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rr := do(mux, tc.req())
			body := rr.Body.String()
			if rr.Code >= 500 {
				t.Fatalf("unexpected %d: %s", rr.Code, body)
			}
			if strings.Contains(body, discloseSecret) {
				t.Errorf("%s discloses %q to an unauthenticated caller\nstatus=%d body=%s",
					tc.name, discloseSecret, rr.Code, body)
			}
			// Structural fields must survive — this is a projection, not a refusal.
			if rr.Code == http.StatusOK && !strings.Contains(body, id) {
				t.Errorf("%s dropped the instance id; the projection is too aggressive\nbody=%s",
					tc.name, body)
			}
		})
	}
}

// TestIdentifiedReadReceivesFullFidelity pins the other half: the fix must not blind the
// callers a consumer has authenticated.
//
// ⚠ It configures identity through WithRequestActor — the documented seam for "identity
// lives somewhere the context does not reach". A decision keyed on authz.ActorFromContext
// would pass every unauthenticated case above and STILL fail here, because nothing in
// transport/http puts the actor on the context.
func TestIdentifiedReadReceivesFullFidelity(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	_, svc := transporttest.NewHarness(t, def)
	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("approval"),
		Vars:   map[string]any{"ssn": discloseSecret},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc, stdlib.WithRequestActor(
		func(ctx context.Context) (authz.Actor, error) {
			return authz.Actor{ID: "alice", Roles: []string{"manager"}}, nil
		}))

	rr := do(mux, newGetRequest(t, "/instances/"+pi.State().InstanceID))
	if !strings.Contains(rr.Body.String(), discloseSecret) {
		t.Errorf("an IDENTIFIED caller must receive full fidelity\nstatus=%d body=%s",
			rr.Code, rr.Body)
	}
}

// TestDiscloseAllRestoresThePriorShape pins the opt-out.
func TestDiscloseAllRestoresThePriorShape(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	_, svc := transporttest.NewHarness(t, def)
	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("approval"),
		Vars:   map[string]any{"ssn": discloseSecret},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc, httpcore.WithDisclosure[*http.ServeMux](authz.DiscloseAll))

	rr := do(mux, newGetRequest(t, "/instances/"+pi.State().InstanceID))
	if !strings.Contains(rr.Body.String(), discloseSecret) {
		t.Errorf("DiscloseAll must restore the prior shape\nstatus=%d body=%s", rr.Code, rr.Body)
	}
}
