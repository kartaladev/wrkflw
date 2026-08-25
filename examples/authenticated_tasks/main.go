// Command authenticated_tasks demonstrates ADR-0189's identity seam end to end, for
// all three HTTP adapters.
//
// The human-task verbs (claim, complete, reassign) authorize against an actor the
// CONSUMER supplies. Before ADR-0189 that actor came from the request body, so any
// caller could post {"actor":{"id":"alice","roles":["manager"]}} and be believed. It
// now comes from the request context and from nowhere else.
//
// ⚠ THIS EXAMPLE EXISTS BECAUSE THE IDIOM DIFFERS PER FRAMEWORK, AND IN TWO OF THE
// THREE THE MOST NATURAL CHANNEL IS THE ONE THAT DOES NOT WORK:
//
//	stdlib  ✅ r.WithContext(...)
//	gin     ✅ gc.Request = gc.Request.WithContext(...)      ❌ gc.Set(...)
//	fiber   ✅ c.SetContext(...)                             ❌ c.Locals(...)
//
// Both misses are measured, not theoretical: gin's Context values and the request's
// context are disjoint stores, and fiber's Ctx.Context() returns a context the handler
// only sees if SetContext put it there. Using the ❌ column is FAIL-CLOSED — the request
// gets a 401 rather than a false identity — but it looks like your middleware is being
// ignored. This program exercises all FIVE cells of the table above (stdlib has only
// one idiom, so it contributes no ❌), across eleven probes.
//
// Run: go run ./examples/authenticated_tasks
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	ginlib "github.com/gin-gonic/gin"
	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
	fiberadapter "github.com/kartaladev/wrkflw/transport/http/fiber"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// errNoCredential is what a resolver returns when the request carries no credential.
// It must be reported as httpcore.ErrUnauthenticated so the transport answers 401
// rather than 503 — 503 is for the identity system being broken, not absent.
var errNoCredential = errors.New("no credential")

// verifyCredential is this example's stand-in for real authentication.
//
// ⚠ It verifies a bearer token against a secret rather than trusting a header that
// merely NAMES a user. An example about authentication must not teach header-trusting:
// a header a client controls is exactly the self-asserted actor ADR-0189 removed.
func verifyCredential(authorization string) (authz.Actor, error) {
	const scheme = "Bearer "
	if !strings.HasPrefix(authorization, scheme) {
		return authz.Actor{}, errNoCredential
	}
	switch strings.TrimPrefix(authorization, scheme) {
	case "manager-secret":
		return authz.Actor{ID: "alice", Roles: []string{"manager"}}, nil
	case "viewer-secret":
		return authz.Actor{ID: "bob", Roles: []string{"viewer"}}, nil
	default:
		return authz.Actor{}, errNoCredential
	}
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	def, err := approvalDefinition()
	if err != nil {
		return err
	}
	fmt.Println("ADR-0189 — the actor comes from the context, never from the body")
	fmt.Println(strings.Repeat("=", 72))

	for _, probe := range []struct {
		adapter string
		run     func(*model.ProcessDefinition) ([]result, error)
	}{
		{"stdlib", runStdlib},
		{"gin", runGin},
		{"fiber", runFiber},
	} {
		results, err := probe.run(def)
		if err != nil {
			return fmt.Errorf("%s: %w", probe.adapter, err)
		}
		fmt.Printf("\n%s\n", probe.adapter)
		for _, r := range results {
			fmt.Printf("  %-46s → %d  %s\n", r.name, r.status, r.note)
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 72))
	fmt.Println("A body naming a manager never promotes the caller: the identity that decides")
	fmt.Println("is the one the middleware verified. See SECURITY.md for the per-framework idiom.")
	return nil
}

type result struct {
	name   string
	status int
	note   string
}

// forgedBody is what a pre-ADR-0189 attacker would send. Every probe below posts it,
// so each line shows the body being ignored rather than merely unused.
const forgedBody = `{"actor":{"id":"alice","roles":["manager"]}}`

func runStdlib(def *model.ProcessDefinition) ([]result, error) {
	var out []result
	for _, c := range []struct{ name, token, note string }{
		{"no credential, body claims manager", "", "401 — the forged body is inert"},
		{"viewer credential, body claims manager", "viewer-secret", "403 — the MIDDLEWARE's viewer decides"},
		{"manager credential", "manager-secret", "200"},
	} {
		svc, taskID := mustTask(def)
		mux := http.NewServeMux()
		stdlib.Mount(mux, svc)
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a, err := verifyCredential(r.Header.Get("Authorization")); err == nil {
				r = r.WithContext(authz.ContextWithActor(r.Context(), a))
			}
			mux.ServeHTTP(w, r)
		})
		out = append(out, result{c.name, hitStdlib(h, taskID, c.token), c.note})
	}
	return out, nil
}

func hitStdlib(h http.Handler, taskID, token string) int {
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/claim", strings.NewReader(forgedBody))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

func runGin(def *model.ProcessDefinition) ([]result, error) {
	ginlib.SetMode(ginlib.ReleaseMode)
	var out []result

	mk := func(useTheBrokenIdiom bool) (*ginlib.Engine, string) {
		svc, taskID := mustTask(def)
		r := ginlib.New()
		r.Use(func(gc *ginlib.Context) {
			a, err := verifyCredential(gc.GetHeader("Authorization"))
			if err == nil {
				if useTheBrokenIdiom {
					// ❌ gin's canonical idiom. gc.Set writes to gin's OWN store, which is
					// NOT the request context the route group reads. Measured: disjoint.
					gc.Set("actor", a)
				} else {
					// ✅ replace the request so its context carries the actor.
					gc.Request = gc.Request.WithContext(authz.ContextWithActor(gc.Request.Context(), a))
				}
			}
			gc.Next()
		})
		ginadapter.Mount(r, svc)
		return r, taskID
	}

	for _, c := range []struct {
		name, token string
		broken      bool
		note        string
	}{
		{"no credential, body claims manager", "", false, "401 — the forged body is inert"},
		{"viewer credential (gc.Request.WithContext)", "viewer-secret", false, "403 — the MIDDLEWARE's viewer decides"},
		{"manager credential (gc.Request.WithContext)", "manager-secret", false, "200"},
		{"manager credential via gc.Set", "manager-secret", true, "401 — ❌ gc.Set never reaches the handler"},
	} {
		r, taskID := mk(c.broken)
		srv := httptest.NewServer(r)
		out = append(out, result{c.name, hitServer(srv.URL, taskID, c.token), c.note})
		srv.Close()
	}
	return out, nil
}

func runFiber(def *model.ProcessDefinition) ([]result, error) {
	var out []result
	for _, c := range []struct {
		name, token string
		broken      bool
		note        string
	}{
		{"no credential, body claims manager", "", false, "401 — the forged body is inert"},
		{"viewer credential (c.SetContext)", "viewer-secret", false, "403 — the MIDDLEWARE's viewer decides"},
		{"manager credential (c.SetContext)", "manager-secret", false, "200"},
		{"manager credential via c.Locals", "manager-secret", true, "401 — ❌ c.Locals never reaches the handler"},
	} {
		svc, taskID := mustTask(def)
		app := fiberlib.New()
		broken := c.broken
		app.Use(func(fc fiberlib.Ctx) error {
			a, err := verifyCredential(fc.Get("Authorization"))
			if err == nil {
				if broken {
					// ❌ fiber's canonical idiom. Ctx.Value reads Locals; Ctx.Context()
					// is a different object, and that is what the route group receives.
					fc.Locals("actor", a)
				} else {
					// ✅ replace the context the handler will read.
					fc.SetContext(authz.ContextWithActor(fc.Context(), a))
				}
			}
			return fc.Next()
		})
		fiberadapter.Mount(app, svc)

		req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/claim", strings.NewReader(forgedBody))
		req.Header.Set("Content-Type", "application/json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := app.Test(req)
		if err != nil {
			return nil, err
		}
		_ = resp.Body.Close()
		out = append(out, result{c.name, resp.StatusCode, c.note})
	}
	return out, nil
}

func hitServer(base, taskID, token string) int {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		base+"/tasks/"+taskID+"/claim", strings.NewReader(forgedBody))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// --- the process under test -------------------------------------------------

// approvalDefinition is start → approve (user task, manager-only) → end.
// The eligibility spec is what makes the viewer probes return 403 rather than 200.
func approvalDefinition() (*model.ProcessDefinition, error) {
	return definition.NewBuilder("approval", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("manager"))).
		Add(event.NewEnd("end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Build()
}

func newService(def *model.ProcessDefinition) (service.Service, error) {
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {manager}})
	az := authz.RoleAuthorizer{}

	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		return nil, err
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	if err != nil {
		return nil, err
	}
	return service.NewProcessEngine(
		service.WithProcessDriver(driver),
		service.WithInstanceStore(memSt),
		service.WithDefinitions(kernel.NewMapDefinitionRegistry(def)),
		service.WithHumanTasks(taskStore, az),
	)
}

func mustTask(def *model.ProcessDefinition) (service.Service, string) {
	svc, err := newService(def)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build service:", err)
		os.Exit(1)
	}
	pi, err := svc.StartInstance(context.Background(), service.StartInstanceRequest{
		DefRef: model.Qualifier{ID: "approval"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start instance:", err)
		os.Exit(1)
	}
	for _, task := range pi.State().Tasks {
		return svc, task.TaskID
	}
	fmt.Fprintln(os.Stderr, "no task was parked")
	os.Exit(1)
	return nil, ""
}
