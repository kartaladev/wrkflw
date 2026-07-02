// Package main demonstrates human-task authorization with two mechanisms:
//
//  1. Attribute-based authorization over process variables (ABAC): a UserTask
//     whose EligibilityExpr predicate is evaluated against snapshotted process
//     variables at Claim time. Only an actor with the matching region variable
//     value may claim the task. This proves the headline feature —
//     vars["region"] == "EU" — end-to-end through the full runner→snapshot→claim
//     path with no Docker required.
//
//  2. Casbin RBAC / resource-privilege: a casbin-backed authz.Authorizer wired
//     via casbinauthz.NewCasbinAuthorizerFromStrings. A small inline policy CSV
//     grants the "approver" role the "finance-task claim" privilege. An actor
//     with that role is allowed; an actor without it is denied. Driven through
//     TaskService.Claim for realism.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/casbinauthz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// casbinPolicy is the inline policy CSV used for the casbin RBAC demo.
// Lines starting with "p" define permission rules; lines starting with "g"
// define role-inheritance (unused here but valid in the DefaultModel).
//
// Policy: the "approver" role may perform "claim" on "finance-task".
const casbinPolicy = `
p, approver, finance-task, claim
`

func main() {
	ctx := context.Background()

	fmt.Println("=== Demo 1: Attribute-based authorization over process variables ===")
	demoAttributeAuthz(ctx)

	fmt.Println()
	fmt.Println("=== Demo 2: Casbin RBAC / resource-privilege ===")
	demoCasbinRBAC(ctx)
}

// demoAttributeAuthz proves the full runner→snapshot→claim chain with an
// EligibilityExpr predicate (vars["region"] == "EU") evaluated over snapshotted
// process variables. Two instances are run: one with region=EU (ALLOW) and one
// with region=US (DENY).
func demoAttributeAuthz(ctx context.Context) {
	// Process definition: start → approve[UserTask, role "approver",
	// EligibilityExpr vars["region"] == "EU"] → end.
	def := &model.ProcessDefinition{
		ID:      "region-approval",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("approve", []string{"approver"},
				model.WithEligibilityExpr(`vars["region"] == "EU"`),
			),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}

	// The approver actor qualifies by role but still needs vars["region"]=="EU".
	approver := authz.Actor{ID: "alice", Roles: []string{"approver"}}

	// resolver maps the "approver" role to the actor (used by AwaitHuman).
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"approver": {approver},
	})

	// RoleAuthorizer evaluates both spec.Roles and spec.Attribute (the
	// EligibilityExpr is mapped to AuthzSpec.Attribute by the engine).
	az := authz.RoleAuthorizer{}

	// --- EU instance: should be ALLOWED ---
	{
		taskStore := humantask.NewMemTaskStore()
		r := runtime.NewRunner(nil, runtime.NewMemStore(),
			runtime.WithHumanTasks(resolver, taskStore, az),
		)

		parked, err := r.Run(ctx, def, "region-eu-001", map[string]any{"region": "EU"})
		if err != nil {
			log.Fatal("run EU:", err)
		}
		taskToken := parked.Tokens[0].AwaitCommand

		svc := runtime.NewTaskService(taskStore, az)
		claimTrg, err := svc.Claim(ctx, taskToken, approver)
		if err != nil {
			fmt.Printf("  EU instance Claim: UNEXPECTED DENY — %v\n", err)
		} else {
			fmt.Println("  EU instance Claim: ALLOW (expected)")
			// Complete the task to drive the instance to StatusCompleted.
			completeTrg, cerr := svc.Complete(ctx, taskToken, approver, map[string]any{"decision": "approved"})
			if cerr != nil {
				log.Fatal("complete EU:", cerr)
			}
			if _, cerr = r.Deliver(ctx, def, "region-eu-001", claimTrg); cerr != nil {
				log.Fatal("deliver claim EU:", cerr)
			}
			final, cerr := r.Deliver(ctx, def, "region-eu-001", completeTrg)
			if cerr != nil {
				log.Fatal("deliver complete EU:", cerr)
			}
			if final.Status == engine.StatusCompleted {
				fmt.Println("  EU instance completed → StatusCompleted")
			}
		}
	}

	// --- US instance: should be DENIED ---
	{
		taskStore := humantask.NewMemTaskStore()
		r := runtime.NewRunner(nil, runtime.NewMemStore(),
			runtime.WithHumanTasks(resolver, taskStore, az),
		)

		parked, err := r.Run(ctx, def, "region-us-001", map[string]any{"region": "US"})
		if err != nil {
			log.Fatal("run US:", err)
		}
		taskToken := parked.Tokens[0].AwaitCommand

		svc := runtime.NewTaskService(taskStore, az)
		_, err = svc.Claim(ctx, taskToken, approver)
		if errors.Is(err, authz.ErrNotAuthorized) {
			fmt.Println("  US instance Claim: DENY (expected) — authz.ErrNotAuthorized")
		} else if err != nil {
			fmt.Printf("  US instance Claim: unexpected error — %v\n", err)
		} else {
			fmt.Println("  US instance Claim: UNEXPECTED ALLOW")
		}
	}
}

// demoCasbinRBAC wires casbinauthz.NewCasbinAuthorizerFromStrings with an inline
// policy CSV granting the "approver" role the "finance-task claim" privilege. Two
// actors are tested through TaskService.Claim: one with the "approver" role
// (ALLOW) and one without (DENY).
func demoCasbinRBAC(ctx context.Context) {
	// Build a casbin-backed authorizer from the inline policy CSV.
	// The default model (casbinauthz.DefaultModel) supports RBAC via g lines and
	// resource-privilege via p lines; no custom model text needed here.
	casbinAz, err := casbinauthz.NewCasbinAuthorizerFromStrings("", casbinPolicy)
	if err != nil {
		log.Fatal("build casbin authorizer:", err)
	}

	// AuthzSpec using Privileges: "finance-task claim" means obj="finance-task",
	// act="claim" in casbin's (sub, obj, act) model.
	spec := authz.AuthzSpec{
		Privileges: []string{"finance-task claim"},
	}

	// Process definition: start → finance-review[UserTask, no candidate roles but
	// Privileges check done by casbin] → end.
	//
	// We embed the privilege directly into the task via a pre-built HumanTask in the
	// in-memory store, bypassing the model builder — the engine maps CandidateRoles to
	// AuthzSpec.Roles but not Privileges (which is reserved for casbin-style checks).
	// The cleanest path is to drive TaskService.Claim directly with a pre-stored task.
	taskStore := humantask.NewMemTaskStore()
	const financeTaskID = "finance-task-001" // opaque task identifier (not a credential)
	if uErr := taskStore.Upsert(ctx, humantask.HumanTask{
		TaskToken:   financeTaskID,
		Eligibility: spec,
		Vars:        map[string]any{},
		State:       humantask.Unclaimed,
	}); uErr != nil {
		log.Fatal("upsert task:", uErr)
	}

	svc := runtime.NewTaskService(taskStore, casbinAz)

	// Actor WITH the "approver" role → casbin policy grants finance-task claim.
	withRole := authz.Actor{ID: "bob", Roles: []string{"approver"}}
	_, err = svc.Claim(ctx, financeTaskID, withRole)
	if err == nil {
		fmt.Println("  Actor with 'approver' role: ALLOW (expected)")
	} else {
		fmt.Printf("  Actor with 'approver' role: UNEXPECTED DENY — %v\n", err)
	}

	// Reset task state so the second claim attempt is on an unclaimed task.
	if uErr := taskStore.Upsert(ctx, humantask.HumanTask{
		TaskToken:   financeTaskID,
		Eligibility: spec,
		Vars:        map[string]any{},
		State:       humantask.Unclaimed,
	}); uErr != nil {
		log.Fatal("reset task:", uErr)
	}

	// Actor WITHOUT the "approver" role → casbin denies.
	withoutRole := authz.Actor{ID: "carol", Roles: []string{"viewer"}}
	_, err = svc.Claim(ctx, financeTaskID, withoutRole)
	if errors.Is(err, authz.ErrNotAuthorized) {
		fmt.Println("  Actor without 'approver' role: DENY (expected) — authz.ErrNotAuthorized")
	} else if err != nil {
		fmt.Printf("  Actor without 'approver' role: unexpected error — %v\n", err)
	} else {
		fmt.Println("  Actor without 'approver' role: UNEXPECTED ALLOW")
	}
}
