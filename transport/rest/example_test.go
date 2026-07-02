package rest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// Example_responseShapes demonstrates mounting rest.NewHandler on an
// httptest.Server and exercising the two ProcessInstance response shapes:
//
//   - GET /instances/{id}/snapshot → runtime.InstanceSnapshot: full state
//     projection (instance_id, def_id, def_version, status, variables, tokens,
//     history, …) with engine bookkeeping stripped.
//   - GET /instances/{id}/actionable → runtime.ActionableView: task-centric
//     projection (instance_id, status, open_tasks with allowed_actions) that
//     contains only the fields needed by a task-inbox UI.
//
// The two shapes share instance_id and status but differ structurally: snapshot
// carries def_id and variables; actionable carries open_tasks with
// allowed_actions derived from the definition's outgoing sequence flows.
//
// Wiring is entirely in-memory (MemStore, MemTaskStore, FakeClock) — no Docker
// or external services required.
func Example_responseShapes() {
	// ── 1. In-memory wiring ──────────────────────────────────────────────────

	fc := clockwork.NewFakeClock()
	store, err := runtime.NewMemStore()
	if err != nil {
		panic(err)
	}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {{ID: "alice", Roles: []string{"manager"}}},
	})
	az := authz.RoleAuthorizer{}
	cat := action.NewMapCatalog(nil)
	runner, err := runtime.NewRunner(cat, store,
		runtime.WithRunnerClock(fc),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	if err != nil {
		panic(err)
	}

	// approval: start → approve (UserTask, candidates: ["manager"]) → end
	def := &model.ProcessDefinition{
		ID: "approval", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("approve", []string{"manager"}),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}

	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"approval:1": def,
		"approval":   def,
	})
	taskSvc, err := runtime.NewTaskService(taskStore, az, runtime.WithTaskServiceClock(fc))
	if err != nil {
		panic(err)
	}
	svc := service.New(runner, taskSvc, reg, store, store, taskStore,
		service.WithEngineClock(fc),
	)

	// ── 2. Mount the handler under /wf/ to prove prefix mounting ────────────

	h := rest.NewHandler(svc)
	outer := http.NewServeMux()
	outer.Handle("/wf/", http.StripPrefix("/wf", h))
	srv := httptest.NewServer(outer)
	defer srv.Close()
	client := srv.Client()

	// ── 3. Start an instance — parks at the UserTask ─────────────────────────

	// Use runner.Run directly so the instance parks at the human task before
	// service.StartInstance (which also uses runner) — same observable state.
	_, err = runner.Run(context.Background(), def, "demo-instance-1", nil)
	if err != nil {
		fmt.Printf("runner.Run error: %v\n", err)
		return
	}

	// ── 4. GET /wf/instances/{id}/snapshot ──────────────────────────────────

	snapResp, err := client.Get(srv.URL + "/wf/instances/demo-instance-1/snapshot")
	if err != nil {
		fmt.Printf("snapshot request error: %v\n", err)
		return
	}
	defer snapResp.Body.Close() //nolint:errcheck

	var snap map[string]any
	if err := json.NewDecoder(snapResp.Body).Decode(&snap); err != nil {
		fmt.Printf("snapshot decode error: %v\n", err)
		return
	}

	// Print only the stable, deterministic subset (no timestamps / UUIDs).
	fmt.Printf("snapshot: status=%s def_id=%s def_version=%v\n",
		snap["status"], snap["def_id"], int(snap["def_version"].(float64)))
	_, hasVariables := snap["variables"]
	_, hasOpenTasks := snap["open_tasks"]
	fmt.Printf("snapshot: has_variables=%v has_open_tasks=%v\n", hasVariables, hasOpenTasks)

	// ── 5. GET /wf/instances/{id}/actionable ────────────────────────────────

	actResp, err := client.Get(srv.URL + "/wf/instances/demo-instance-1/actionable")
	if err != nil {
		fmt.Printf("actionable request error: %v\n", err)
		return
	}
	defer actResp.Body.Close() //nolint:errcheck

	var act map[string]any
	if err := json.NewDecoder(actResp.Body).Decode(&act); err != nil {
		fmt.Printf("actionable decode error: %v\n", err)
		return
	}

	// Print only the stable subset.
	fmt.Printf("actionable: status=%s\n", act["status"])
	_, hasDefID := act["def_id"]
	_, hasActOpenTasks := act["open_tasks"]
	fmt.Printf("actionable: has_def_id=%v has_open_tasks=%v\n", hasDefID, hasActOpenTasks)

	// Open task node and allowed next action are fully deterministic.
	openTasks, _ := act["open_tasks"].([]any)
	if len(openTasks) > 0 {
		task, _ := openTasks[0].(map[string]any)
		fmt.Printf("actionable: open_tasks[0].node_id=%s\n", task["node_id"])
		allowed, _ := task["allowed_actions"].([]any)
		if len(allowed) > 0 {
			flow, _ := allowed[0].(map[string]any)
			fmt.Printf("actionable: open_tasks[0].allowed_actions[0].target=%s\n", flow["target"])
		}
	}

	// Output:
	// snapshot: status=running def_id=approval def_version=1
	// snapshot: has_variables=false has_open_tasks=false
	// actionable: status=running
	// actionable: has_def_id=false has_open_tasks=true
	// actionable: open_tasks[0].node_id=approve
	// actionable: open_tasks[0].allowed_actions[0].target=end
}
