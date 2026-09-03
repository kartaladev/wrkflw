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
	"maps"
	"slices"
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

// Claim records who claimed a [HumanTask] and when. A task carries at most one
// claim at a time: a reassignment overwrites it rather than appending, so there
// is no claim history.
type Claim struct {
	// Actor is the claimant, as resolved at claim time.
	Actor authz.Actor `json:"actor"`
	// At is the time the claim was made, stamped from the trigger's occurrence time.
	At time.Time `json:"timestamp"`
}

// Completion records who completed a [HumanTask], when, and with what business
// disposition. Outcome is the declared outcome the actor picked (validated
// against the node's declared outcomes when it declares any); Note is the actor's
// free-text remark. Both are optional.
type Completion struct {
	// Actor is the completer, as carried by the completion trigger.
	Actor authz.Actor `json:"actor"`
	// At is the completion time, stamped from the trigger's occurrence time.
	At time.Time `json:"timestamp"`
	// Outcome is the business outcome chosen by the actor (e.g. "approve"); empty when none.
	Outcome string `json:"outcome,omitempty"`
	// Note is the actor's free-text remark; empty when none.
	Note string `json:"note,omitempty"`
}

// HumanTask is the in-flight record of a human-task node execution. It is stored
// in a [TaskStore] and queried by the runtime and API layer.
//
// Claim and Completion form the task's audit trail: both are nil until the
// corresponding lifecycle event occurs, and both carry the full [authz.Actor] the
// engine observed rather than a bare ID.
//
// DueAt is reserved for deadline / timer integration and is left nil here.
type HumanTask struct {
	// TaskID uniquely identifies this task instance (matches the engine token).
	TaskID string
	// InstanceID is the parent process-instance ID.
	InstanceID string
	// NodeID is the BPMN node that generated this task.
	NodeID string
	// Eligibility describes who may act on this task (roles, privileges, attribute predicate).
	Eligibility authz.AuthzSpec
	// Candidates holds the resolved eligible actors (filled by the runtime via
	// [ActorResolver]). Actors are passed through verbatim: any username/email
	// lives in Attributes only if the resolver populated it.
	Candidates []authz.Actor
	// State is the current lifecycle state.
	State TaskState
	// Claim records the current claimant and claim time; nil when Unclaimed.
	Claim *Claim
	// Completion records the completing actor, time, outcome and note; nil until Completed.
	Completion *Completion
	// CreatedAt is the wall-clock time at which the task was created.
	CreatedAt time.Time
	// DueAt is the optional deadline (nil in this implementation).
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

// Clone returns a copy of the task whose mutable fields — the candidate actors
// (each deep-copied via [authz.Actor.Clone]), the eligibility slices, the Claim
// and Completion pointees, DueAt, and the Vars map — are independently allocated,
// so mutating the copy cannot affect the receiver. Nil fields stay nil.
//
// Vars is cloned one level deep, matching the shallow-snapshot rule documented on
// the field: nested maps and slices inside a variable remain shared.
//
// This is the single deep-copy definition for a task; the engine's instance-state
// clone and the caching task store both delegate here rather than re-deriving it,
// so a field isolated HERE is isolated everywhere at once.
//
// ⚠ That is a claim about the call sites, NOT about the field set. The
// Eligibility slices below are enumerated BY HAND, so a mutable field added to
// authz.AuthzSpec is NOT isolated until it is added here — the earlier wording
// ("a newly added mutable field is isolated everywhere at once") promised
// otherwise and was false. TestCloneIsolatesEveryEligibilityReference derives that
// field set reflectively and fails until this function catches up.
func (t HumanTask) Clone() HumanTask {
	// Guard on nil, not on length: a zero-length slice with spare capacity is
	// still shared, so two clones appending to it would write the same backing
	// array. authz.CloneActors and slices.Clone both map nil to nil and anything
	// else to a fresh array.
	t.Candidates = authz.CloneActors(t.Candidates)
	t.Eligibility.Roles = slices.Clone(t.Eligibility.Roles)
	t.Eligibility.Privileges = slices.Clone(t.Eligibility.Privileges)
	if t.Claim != nil {
		claim := *t.Claim
		claim.Actor = claim.Actor.Clone()
		t.Claim = &claim
	}
	if t.Completion != nil {
		completion := *t.Completion
		completion.Actor = completion.Actor.Clone()
		t.Completion = &completion
	}
	if t.DueAt != nil {
		due := *t.DueAt
		t.DueAt = &due
	}
	if t.Vars != nil {
		t.Vars = maps.Clone(t.Vars)
	}
	return t
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
// All query methods must return stable, deterministic results (sorted by TaskID
// / actor ID) so that callers and tests do not observe random ordering.
type TaskStore interface {
	// Upsert inserts or replaces the task identified by t.TaskID.
	//
	// Implementations MUST reject a task that fails [Validate] — a Claimed task with
	// no claim, an Unclaimed task carrying one, or an out-of-range State — by
	// returning an error wrapping [ErrInvalidTask]. Call
	// [Validate] rather than re-deriving the rule; the read path relies on the
	// invariant holding, and the runtime refuses to commit a step that would project
	// a task violating it. Verify your implementation with
	// processtest.RunTaskStoreConformance.
	Upsert(ctx context.Context, t HumanTask) error
	// Get returns the task for the given token or [ErrTaskNotFound].
	Get(ctx context.Context, taskID string) (HumanTask, error)
	// AssignedTo returns all tasks currently claimed by the given actorID,
	// sorted by TaskID.
	//
	// An empty actorID identifies no actor and returns an empty result; it is
	// NOT a wildcard over unclaimed tasks. Implementations must enforce this
	// explicitly: an unclaimed task carries no claim in memory but an empty
	// claimant column in SQL, so a naive `claimed_by = ?` lookup would turn an
	// unauthenticated or unresolved actor ID into a dump of every unheld task.
	AssignedTo(ctx context.Context, actorID string) ([]HumanTask, error)
	// ClaimableBy returns all Unclaimed tasks for which the actor is eligible:
	// the actor's ID is in Candidates OR the actor shares at least one role
	// with the task's Eligibility.Roles. Results are sorted by TaskID.
	ClaimableBy(ctx context.Context, actor authz.Actor) ([]HumanTask, error)
}
