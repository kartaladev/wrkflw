package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// Compile-time assertion: CachingTaskStore satisfies humantask.TaskStore.
var _ humantask.TaskStore = (*CachingTaskStore)(nil)

const defaultHumanTaskCacheTTL = 30 * time.Second

// CachingTaskStore is a point-read cache (read-through on Get, write-through on
// Upsert) in front of a [humantask.TaskStore]. Set-wide queries ([AssignedTo],
// [ClaimableBy]) pass straight through to the backing store and are not cached in
// v1 — the key-space is unbounded and the read-through benefit is negligible.
//
// Storage is delegated to a [cache.Provider] substrate (in-memory by default via
// persistence/cache/hotcache). Capacity and TTL expiry are the substrate's
// responsibility; this store supplies only a per-Set TTL hint.
//
// ErrTaskNotFound is never cached: a miss means the task may not yet exist, and
// suppressing a miss for up to TTL seconds would hide a race between Upsert and Get.
type CachingTaskStore struct {
	backing humantask.TaskStore
	codec   *cache.Codec[humantask.HumanTask]
	ttl     time.Duration
}

// CachingTaskStoreOption configures a [CachingTaskStore].
type CachingTaskStoreOption func(*CachingTaskStore)

// WithHumanTaskCacheTTL sets the max age hint passed to the cache substrate when
// storing a task snapshot. Values <= 0 are ignored. Default: 30s.
func WithHumanTaskCacheTTL(d time.Duration) CachingTaskStoreOption {
	return func(c *CachingTaskStore) {
		if d > 0 {
			c.ttl = d
		}
	}
}

// NewCachingTaskStore wraps backing with a point-read cache whose storage comes
// from provider.Cache("humantasks"). It fails fast with [kernel.ErrNilDependency]
// when backing or provider is nil.
func NewCachingTaskStore(backing humantask.TaskStore, provider cache.Provider, opts ...CachingTaskStoreOption) (*CachingTaskStore, error) {
	if backing == nil {
		return nil, fmt.Errorf("%w: backing task store", kernel.ErrNilDependency)
	}
	if provider == nil {
		return nil, fmt.Errorf("%w: cache provider", kernel.ErrNilDependency)
	}
	raw, err := provider.Cache("humantasks")
	if err != nil {
		return nil, err
	}
	codec, err := cache.NewCodec[humantask.HumanTask](
		raw,
		func(t humantask.HumanTask) ([]byte, error) { return json.Marshal(t) },
		unmarshalTask,
		cloneTask,
	)
	if err != nil {
		return nil, err
	}
	c := &CachingTaskStore{backing: backing, codec: codec, ttl: defaultHumanTaskCacheTTL}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Get serves the task from cache when present (read-through). On a cache miss it
// reads the backing store and, if successful, populates the cache. Errors —
// including [humantask.ErrTaskNotFound] — are never cached.
func (s *CachingTaskStore) Get(ctx context.Context, token string) (humantask.HumanTask, error) {
	if t, ok, err := s.codec.Get(ctx, token); err == nil && ok {
		return t, nil
	}
	t, err := s.backing.Get(ctx, token)
	if err != nil {
		return humantask.HumanTask{}, err
	}
	_ = s.codec.Set(ctx, token, t, s.ttl)
	return t, nil
}

// Upsert delegates to the backing store; on success it write-through refreshes the
// cached entry so a subsequent Get returns the updated snapshot without a backing
// round-trip.
func (s *CachingTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	if err := s.backing.Upsert(ctx, t); err != nil {
		return err
	}
	_ = s.codec.Set(ctx, t.TaskToken, t, s.ttl)
	return nil
}

// AssignedTo returns all tasks claimed by actorID. Not cached — see type doc.
func (s *CachingTaskStore) AssignedTo(ctx context.Context, actorID string) ([]humantask.HumanTask, error) {
	return s.backing.AssignedTo(ctx, actorID)
}

// ClaimableBy returns all Unclaimed tasks for which actor is eligible. Not cached — see type doc.
func (s *CachingTaskStore) ClaimableBy(ctx context.Context, actor authz.Actor) ([]humantask.HumanTask, error) {
	return s.backing.ClaimableBy(ctx, actor)
}

// unmarshalTask decodes a [humantask.HumanTask] from the byte-oriented substrate path.
func unmarshalTask(b []byte) (humantask.HumanTask, error) {
	var t humantask.HumanTask
	return t, json.Unmarshal(b, &t)
}

// cloneTask deep-copies the mutable fields so a live value in a [ValueCache]
// substrate cannot be aliased by a caller.
func cloneTask(t humantask.HumanTask) humantask.HumanTask {
	t.Candidates = append([]string(nil), t.Candidates...)
	t.Eligibility.Roles = append([]string(nil), t.Eligibility.Roles...)
	t.Eligibility.Privileges = append([]string(nil), t.Eligibility.Privileges...)
	if t.Vars != nil {
		t.Vars = maps.Clone(t.Vars)
	}
	if t.DueAt != nil {
		d := *t.DueAt
		t.DueAt = &d
	}
	return t
}
