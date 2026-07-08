package humantask

import (
	"context"
	"sort"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/authz"
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

// Upsert inserts or replaces the task identified by t.TaskToken.
//
// DefID/DefVersion are write-once (see [HumanTask.DefID]): set at task
// creation, they are preserved across every subsequent re-upsert regardless
// of what t carries, mirroring the SQL-backed HumanTaskStore's dialect
// conflict-update SET clause, which deliberately omits those two columns so
// the engine's zeroed lifecycle task-update skeleton (claim/complete/etc.,
// see engine/step_nodes.go) cannot clobber the original qualifier.
func (s *MemTaskStore) Upsert(_ context.Context, t HumanTask) error {
	// Defensive copy of mutable slice fields before storing.
	t.Candidates = copyStrings(t.Candidates)
	t.Eligibility.Roles = copyStrings(t.Eligibility.Roles)
	t.Eligibility.Privileges = copyStrings(t.Eligibility.Privileges)

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.m[t.TaskToken]; ok {
		t.DefID = existing.DefID
		t.DefVersion = existing.DefVersion
	}
	s.m[t.TaskToken] = t
	return nil
}

// Get returns the task for the given token or [ErrTaskNotFound].
func (s *MemTaskStore) Get(_ context.Context, taskToken string) (HumanTask, error) {
	s.mu.RLock()
	t, ok := s.m[taskToken]
	s.mu.RUnlock()
	if !ok {
		return HumanTask{}, ErrTaskNotFound
	}
	return copyTask(t), nil
}

// AssignedTo returns all tasks currently claimed by actorID, sorted by TaskToken.
func (s *MemTaskStore) AssignedTo(_ context.Context, actorID string) ([]HumanTask, error) {
	s.mu.RLock()
	var result []HumanTask
	for _, t := range s.m {
		if t.ClaimedBy == actorID {
			result = append(result, copyTask(t))
		}
	}
	s.mu.RUnlock()

	sort.Slice(result, func(i, j int) bool { return result[i].TaskToken < result[j].TaskToken })
	return result, nil
}

// ClaimableBy returns all Unclaimed tasks for which the actor is eligible.
//
// Eligibility is granted when either:
//   - actor.ID is present in the task's Candidates slice, OR
//   - actor.Roles and task.Eligibility.Roles share at least one value.
//
// Results are sorted by TaskToken for determinism.
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

	sort.Slice(result, func(i, j int) bool { return result[i].TaskToken < result[j].TaskToken })
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

// copyTask returns a shallow copy of t with its slice fields independently copied
// so callers cannot mutate the store's internal state through the returned value.
func copyTask(t HumanTask) HumanTask {
	t.Candidates = copyStrings(t.Candidates)
	t.Eligibility.Roles = copyStrings(t.Eligibility.Roles)
	t.Eligibility.Privileges = copyStrings(t.Eligibility.Privileges)
	return t
}

// copyStrings returns a new slice with the same elements as src, or nil when src
// is nil/empty, to avoid handing out references to internal backing arrays.
func copyStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// candidateContains reports whether id appears in the candidates slice.
func candidateContains(candidates []string, id string) bool {
	for _, c := range candidates {
		if c == id {
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
