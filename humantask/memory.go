package humantask

import (
	"context"
	"sort"
	"sync"

	"github.com/kartaladev/wrkflw/authz"
)

// Compile-time assertions that the in-memory fakes satisfy the ports.
var (
	_ TaskStore     = (*MemTaskStore)(nil)
	_ ActorResolver = (*StaticActorResolver)(nil)
)

// ─── MemTaskStore ─────────────────────────────────────────────────────────────

// MemTaskStore is an in-memory [TaskStore] for tests and reference wiring.
// It is safe for concurrent use: a [sync.RWMutex] guards the internal map.
// All returned slices are copies so callers cannot mutate internal state.
type MemTaskStore struct {
	mu sync.RWMutex
	m  map[string]HumanTask
}

// NewMemTaskStore returns an initialised, empty [MemTaskStore].
func NewMemTaskStore() *MemTaskStore {
	return &MemTaskStore{m: make(map[string]HumanTask)}
}

// Upsert inserts or replaces the task identified by t.TaskID. It rejects a task
// whose state contradicts its claim with [ErrInvalidTask]; see [Validate].
func (s *MemTaskStore) Upsert(_ context.Context, t HumanTask) error {
	// Validate before storing: this fake backs the reference wiring and much of the
	// suite, so staying permissive would let a test green-light a shape the SQL
	// store rejects.
	if err := Validate(t); err != nil {
		return err
	}
	// Defensive copy of mutable fields before storing.
	t = copyTask(t)

	s.mu.Lock()
	s.m[t.TaskID] = t
	s.mu.Unlock()
	return nil
}

// Get returns the task for the given token or [ErrTaskNotFound].
func (s *MemTaskStore) Get(_ context.Context, taskID string) (HumanTask, error) {
	s.mu.RLock()
	t, ok := s.m[taskID]
	s.mu.RUnlock()
	if !ok {
		return HumanTask{}, ErrTaskNotFound
	}
	return copyTask(t), nil
}

// AssignedTo returns all tasks currently claimed by actorID, sorted by TaskID.
//
// An empty actorID identifies no actor and always returns an empty result: it is
// not a wildcard. Unclaimed tasks are stored with no claim at all (and the SQL
// store keeps their claimant column empty), so treating "" as a match would turn
// an unauthenticated or unresolved actor ID into a dump of every task nobody is
// holding. The guard is explicit rather than incidental so both [TaskStore]
// implementations answer identically.
func (s *MemTaskStore) AssignedTo(_ context.Context, actorID string) ([]HumanTask, error) {
	if actorID == "" {
		return nil, nil
	}

	s.mu.RLock()
	var result []HumanTask
	for _, t := range s.m {
		if t.Claim != nil && t.Claim.Actor.ID == actorID {
			result = append(result, copyTask(t))
		}
	}
	s.mu.RUnlock()

	sort.Slice(result, func(i, j int) bool { return result[i].TaskID < result[j].TaskID })
	return result, nil
}

// ClaimableBy returns all Unclaimed tasks for which the actor is eligible.
//
// Eligibility is granted when either:
//   - actor.ID is present in the task's Candidates slice, OR
//   - actor.Roles and task.Eligibility.Roles share at least one value.
//
// Results are sorted by TaskID for determinism.
func (s *MemTaskStore) ClaimableBy(_ context.Context, actor authz.Actor) ([]HumanTask, error) {
	actorRoleSet := roleSet(actor.Roles)

	s.mu.RLock()
	var result []HumanTask
	for _, t := range s.m {
		if t.State != Unclaimed {
			continue
		}
		if candidateContains(t.Candidates, actor.ID) || hasRoleOverlap(actorRoleSet, t.Eligibility.Roles) {
			result = append(result, copyTask(t))
		}
	}
	s.mu.RUnlock()

	sort.Slice(result, func(i, j int) bool { return result[i].TaskID < result[j].TaskID })
	return result, nil
}

// ─── StaticActorResolver ──────────────────────────────────────────────────────

// StaticActorResolver is an [ActorResolver] backed by a static role→actors map.
// It is intended for tests and reference wiring where no external group service
// is available. Candidates returns the union of all actors across the spec's
// roles, deduped by actor ID and sorted by ID for determinism.
type StaticActorResolver struct {
	roleActors map[string][]authz.Actor
}

// NewStaticActorResolver returns a [StaticActorResolver] backed by roleActors.
// The key is a role name; the value is the list of actors that hold that role.
func NewStaticActorResolver(roleActors map[string][]authz.Actor) *StaticActorResolver {
	// Defensive copy of the input map so callers cannot mutate internal state.
	cp := make(map[string][]authz.Actor, len(roleActors))
	for role, actors := range roleActors {
		cp[role] = append([]authz.Actor(nil), actors...)
	}
	return &StaticActorResolver{roleActors: cp}
}

// Candidates returns the deduplicated, ID-sorted union of actors for the roles
// listed in spec.Roles. vars is accepted for interface compatibility but ignored
// by this static implementation.
func (r *StaticActorResolver) Candidates(_ context.Context, spec authz.AuthzSpec, _ map[string]any) ([]authz.Actor, error) {
	seen := make(map[string]authz.Actor)
	for _, role := range spec.Roles {
		for _, actor := range r.roleActors[role] {
			seen[actor.ID] = actor
		}
	}

	result := make([]authz.Actor, 0, len(seen))
	for _, actor := range seen {
		result = append(result, actor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// copyTask returns a copy of t whose mutable fields are independently allocated
// so callers cannot mutate the store's internal state through the returned value.
func copyTask(t HumanTask) HumanTask {
	return t.Clone()
}

// candidateContains reports whether id identifies one of the candidate actors.
func candidateContains(candidates []authz.Actor, id string) bool {
	for _, c := range candidates {
		if c.ID == id {
			return true
		}
	}
	return false
}

// roleSet builds a set from a slice of role strings for O(1) lookup.
func roleSet(roles []string) map[string]struct{} {
	s := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		s[r] = struct{}{}
	}
	return s
}

// hasRoleOverlap reports whether specRoles contains any role present in actorSet.
func hasRoleOverlap(actorSet map[string]struct{}, specRoles []string) bool {
	for _, r := range specRoles {
		if _, ok := actorSet[r]; ok {
			return true
		}
	}
	return false
}
