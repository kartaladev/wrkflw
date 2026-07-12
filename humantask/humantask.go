// Package humantask defines the HumanTask model and the ports (interfaces) that
// allow the engine to create, query, and resolve actors for human tasks.
//
// It is intentionally pure: it imports only stdlib and the in-repo authz package
// so that the abstraction remains free of any transport, storage, event-bus, or
// time-source vendor.
package humantask

import (
	"context"
	"errors"
	"time"

	"github.com/kartaladev/wrkflw/authz"
)

// ErrTaskNotFound is returned by [TaskStore.Get] when no task with the given
// token exists in the store.
var ErrTaskNotFound = errors.New("workflow-humantask: task not found")

// TaskState is the lifecycle state of a [HumanTask].
type TaskState int

const (
	// Unclaimed is the initial state: the task exists but no actor has claimed it.
	Unclaimed TaskState = iota
	// Claimed means an actor has picked up the task and is working on it.
	Claimed
	// Completed means the task was successfully finished.
	Completed
	// Cancelled means the task was abandoned or superseded.
	Cancelled
)

// String returns the canonical lowercase name of the state ("unclaimed",
// "claimed", "completed", "cancelled"); out-of-range values map to "unknown". It
// implements [fmt.Stringer], so a TaskState formats correctly with %s/%v and is
// the source of the string form used in the runtime view DTOs.
func (s TaskState) String() string {
	switch s {
	case Unclaimed:
		return "unclaimed"
	case Claimed:
		return "claimed"
	case Completed:
		return "completed"
	case Cancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// HumanTask is the in-flight record of a human-task node execution. It is stored
// in a [TaskStore] and queried by the runtime and API layer.
//
// DueAt is reserved for Plan 5 (deadline / timer integration) and is left nil here.
type HumanTask struct {
	// TaskToken uniquely identifies this task instance (matches the engine token).
	TaskToken string
	// InstanceID is the parent process-instance ID.
	InstanceID string
	// NodeID is the BPMN node that generated this task.
	NodeID string
	// Eligibility describes who may act on this task (roles, privileges, attribute predicate).
	Eligibility authz.AuthzSpec
	// Candidates holds the resolved actor IDs (filled by the runtime via [ActorResolver]).
	Candidates []string
	// State is the current lifecycle state.
	State TaskState
	// ClaimedBy is the actor ID that claimed the task; empty when Unclaimed.
	ClaimedBy string
	// CreatedAt is the wall-clock time at which the task was created.
	CreatedAt time.Time
	// DueAt is the optional deadline (Plan 5; nil in this implementation).
	DueAt *time.Time
	// Vars is a snapshot of the process Variables at task-creation time, used for
	// attribute-based eligibility predicates that reference data variables
	// (e.g. vars["region"] == "EU"). It is set by the runtime when an AwaitHuman
	// command is performed and must not be aliased to the live process-variable map.
	// Note: the snapshot is a shallow copy (maps.Clone) — top-level keys are copied
	// defensively, but nested maps/slices remain shared with the instance variables;
	// eligibility predicates should rely on top-level scalar variables only.
	Vars map[string]any
}

// IsOpen reports whether the task is still in progress — that is, it has been
// created but not yet completed or cancelled. An open task may be Unclaimed or
// Claimed. Use this in engine handlers to check whether a task is still
// actionable (e.g. before applying a deadline breach or reminder). The caller is
// still responsible for guarding against a nil *HumanTask before calling IsOpen.
func (t HumanTask) IsOpen() bool {
	return t.State == Unclaimed || t.State == Claimed
}

// ActorResolver expands an eligibility spec together with process variables into
// the concrete actor slice that forms the Candidates list. The resolution may
// involve I/O (e.g. a group-membership lookup); therefore it accepts a context.
//
// Implementations that perform I/O live in internal/; pure/static fakes (such as
// [StaticActorResolver]) live here for tests and reference wiring.
type ActorResolver interface {
	Candidates(ctx context.Context, spec authz.AuthzSpec, vars map[string]any) ([]authz.Actor, error)
}

// TaskStore is the queryable projection of [HumanTask] records. It is maintained
// by the runtime from UpdateTask commands and read by the API/authz layer.
//
// All query methods must return stable, deterministic results (sorted by TaskToken
// / actor ID) so that callers and tests do not observe random ordering.
type TaskStore interface {
	// Upsert inserts or replaces the task identified by t.TaskToken.
	Upsert(ctx context.Context, t HumanTask) error
	// Get returns the task for the given token or [ErrTaskNotFound].
	Get(ctx context.Context, taskToken string) (HumanTask, error)
	// AssignedTo returns all tasks currently claimed by the given actorID,
	// sorted by TaskToken.
	AssignedTo(ctx context.Context, actorID string) ([]HumanTask, error)
	// ClaimableBy returns all Unclaimed tasks for which the actor is eligible:
	// the actor's ID is in Candidates OR the actor shares at least one role
	// with the task's Eligibility.Roles. Results are sorted by TaskToken.
	ClaimableBy(ctx context.Context, actor authz.Actor) ([]HumanTask, error)
}
