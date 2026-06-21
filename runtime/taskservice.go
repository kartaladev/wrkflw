package runtime

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// TaskService authorizes human-task interactions and returns the engine triggers
// that the caller (typically via Runner.Deliver) feeds back into the process.
//
// Authorization happens here, in the runtime layer, so that the engine core
// remains pure and free of I/O. TaskService never calls engine.Step itself.
//
// Process variables for attribute-based authorization are carried in
// [humantask.HumanTask.Vars], snapshotted by the runtime's AwaitHuman perform
// at task-creation time. TaskService passes task.Vars to the Authorizer so that
// attribute predicates referencing data variables (e.g. vars["region"] == "EU")
// are correctly evaluated.
type TaskService struct {
	store humantask.TaskStore
	authz authz.Authorizer
	clk   clock.Clock
}

// NewTaskService constructs a TaskService with the given task store, authorizer,
// and clock.
func NewTaskService(store humantask.TaskStore, az authz.Authorizer, clk clock.Clock) *TaskService {
	return &TaskService{store: store, authz: az, clk: clk}
}

// Claim authorizes actor against the task's eligibility and, on success, returns
// a HumanClaimed trigger for the caller to deliver to the engine via Runner.Deliver.
//
// task.Vars (snapshotted at task-creation by the runner's AwaitHuman perform) are
// forwarded to the Authorizer so that attribute predicates referencing process
// variables (e.g. vars["region"] == "EU") are correctly evaluated.
func (s *TaskService) Claim(ctx context.Context, taskToken string, actor authz.Actor) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("runtime: taskservice: get task: %w", err)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil {
		return nil, fmt.Errorf("runtime: taskservice: claim: %w", err)
	}
	return engine.NewHumanClaimed(s.clk.Now(), taskToken, actor), nil
}

// Reassign authorizes the by actor and returns a HumanReassigned trigger.
// by is the admin or supervisor performing the reassignment; from/to are actor IDs.
//
// Authorization policy: the reassigner (by) must satisfy the task's eligibility
// spec — the same check as Claim. A distinct admin/reassign-privilege model is
// deferred. from must equal the current claimant (task.ClaimedBy); if they differ,
// an error is returned and no trigger is issued, preventing a false From in the
// journal.
func (s *TaskService) Reassign(ctx context.Context, taskToken string, from, to string, by authz.Actor) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("runtime: taskservice: get task: %w", err)
	}
	if from != task.ClaimedBy {
		return nil, fmt.Errorf("runtime: reassign: from %q is not the current claimant %q", from, task.ClaimedBy)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, by, task.Vars); err != nil {
		return nil, fmt.Errorf("runtime: taskservice: reassign: %w", err)
	}
	return engine.NewHumanReassigned(s.clk.Now(), taskToken, from, to, by), nil
}

// Complete authorizes actor and returns a HumanCompleted trigger carrying the
// actor's output variables.
func (s *TaskService) Complete(ctx context.Context, taskToken string, actor authz.Actor, output map[string]any) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("runtime: taskservice: get task: %w", err)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil {
		return nil, fmt.Errorf("runtime: taskservice: complete: %w", err)
	}
	return engine.NewHumanCompleted(s.clk.Now(), taskToken, output, actor), nil
}
