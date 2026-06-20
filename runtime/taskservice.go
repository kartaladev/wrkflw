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
// For authorization, vars are not carried in the TaskStore (the store holds only
// the HumanTask record). TaskService therefore passes nil vars to the Authorizer.
// A future attribute-predicate implementation can be extended here to fetch live
// instance variables from the StateStore.
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
// Authorization note: process variables are not available at the TaskService level
// (they live in the StateStore snapshot). We pass nil vars here; this suffices for
// role-based and candidate-based checks. Attribute predicates that need live vars
// should extend TaskService with a StateStore dependency.
func (s *TaskService) Claim(ctx context.Context, taskToken string, actor authz.Actor) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("runtime: taskservice: get task: %w", err)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, actor, nil); err != nil {
		return nil, fmt.Errorf("runtime: taskservice: claim: %w", err)
	}
	return engine.NewHumanClaimed(s.clk.Now(), taskToken, actor), nil
}

// Reassign authorizes the by actor and returns a HumanReassigned trigger.
// by is the admin or supervisor performing the reassignment; from/to are actor IDs.
func (s *TaskService) Reassign(ctx context.Context, taskToken string, from, to string, by authz.Actor) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("runtime: taskservice: get task: %w", err)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, by, nil); err != nil {
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
	if err := s.authz.Authorize(ctx, task.Eligibility, actor, nil); err != nil {
		return nil, fmt.Errorf("runtime: taskservice: complete: %w", err)
	}
	return engine.NewHumanCompleted(s.clk.Now(), taskToken, output, actor), nil
}
