// Package main is the reference cache-wiring example: it shows a consumer
// putting a read-through cache in front of the persistence stores, at the
// runtime (ProcessDriver) level, using the neutral persistence/cache substrate.
//
// The engine's hot read paths (instance state on every ApplyTrigger, human-task
// snapshots on every claim/complete) should be cached so they do not overload
// the backing store (ADR-0073, ADR-0099). Caching is a thin, opt-in wrapper:
// two constructors — persistence.NewCachingInstanceStore and
// persistence.NewCachingTaskStore — wrap ANY backing store with a cache.Provider
// substrate, and the wrapped stores drop straight into the normal driver
// options (WithInstanceStore, WithHumanTasks). Nothing else changes.
//
// This example wires two DIFFERENT cache substrates to make the point that the
// substrate is pluggable:
//
//   - INSTANCE store → hotcache (in-process, samber-hot). Ideal for the
//     instance hot path: it implements cache.ValueCache, so owned instances are
//     served without any (de)serialization.
//   - HUMAN-TASK store → rediscache (distributed, go-redis). A shared cache is
//     the right choice for human tasks in a multi-replica deployment, where any
//     replica may serve a claim/complete. Redis is connected LIVE against
//     REDIS_ADDR (default localhost:6379); if it is unreachable the example
//     logs a notice and falls back to a second hotcache so it still runs.
//
// To make the cache's effect observable, each backing store is wrapped in a
// tiny read-counting decorator: after the process runs (write-through warms the
// cache), repeated Loads/Gets are served from the cache and the backing read
// count stays flat.
//
// This is reference wiring ONLY — NOT a shipped binary. See the comment block at
// the end of main for the distributed-adapter swaps (ottercache/memcache) and
// the service-level persistence.NewDurableProvider(WithCacheProvider(...)) path
// used with SQL backends.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/hotcache"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/rediscache"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func main() {
	if err := run(); err != nil {
		slog.Error("cache_wiring example failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// --- Instance store: in-memory backing, wrapped with an in-process hotcache ---
	//
	// A read counter sits between the cache and the real store so we can show the
	// backing store is not touched once the cache is warm.
	memInstances, err := kernel.NewMemInstanceStore()
	if err != nil {
		return err
	}
	countedInstances := &countingInstanceStore{InstanceStore: memInstances}

	instanceProvider := hotcache.New(
		hotcache.WithCapacity(4096),
		hotcache.WithTTL(5*time.Minute),
	)
	// AlwaysOwn is the correct single-process ownership gate: it lets every
	// instance be cached. A multi-replica deployment would pass a real ownership
	// value (e.g. an advisory-lock ownership) so only the owning replica caches.
	cachingInstances, err := persistence.NewCachingInstanceStore(
		countedInstances, kernel.AlwaysOwn{}, instanceProvider,
		persistence.WithInstanceCacheTTL(5*time.Minute),
		persistence.WithInstanceCacheLogger(logger),
	)
	if err != nil {
		return err
	}

	// --- Human-task store: in-memory backing, wrapped with a Redis cache ---
	//
	// Redis is connected live; on failure we fall back to an in-process hotcache
	// so the example remains runnable with no external dependency.
	memTasks := humantask.NewMemTaskStore()
	countedTasks := &countingTaskStore{TaskStore: memTasks}

	taskProvider, taskBackend, closeRedis := taskCacheProvider(ctx, logger)
	defer closeRedis()

	cachingTasks, err := persistence.NewCachingTaskStore(
		countedTasks, taskProvider,
		persistence.WithHumanTaskCacheTTL(30*time.Second),
	)
	if err != nil {
		return err
	}
	logger.Info("cache providers wired",
		"instance_cache", "hotcache (in-process)",
		"human_task_cache", taskBackend)

	// --- Wire the caching stores into the driver exactly like un-cached ones ---
	reviewer := authz.Actor{ID: "alice", Roles: []string{"reviewer"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"reviewer": {reviewer},
	})
	cat := action.NewCatalog(map[string]action.Action{})

	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(cachingInstances),
		runtime.WithHumanTasks(resolver, cachingTasks, authz.RoleAuthorizer{}),
	)
	if err != nil {
		return err
	}

	def, err := definition.NewBuilder("review", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("review", activity.WithCandidateRoles("reviewer"))).
		Add(event.NewEnd("end")).
		Connect("start", "review").
		Connect("review", "end").
		Build()
	if err != nil {
		return err
	}

	const instanceID = "review-001"

	// Run parks at the UserTask. The write-through caches warm both stores: the
	// instance state and the created human-task snapshot are now in cache.
	if _, err := driver.Drive(ctx, def, instanceID, map[string]any{"amount": 100}); err != nil {
		return err
	}

	// Read the instance back twice through the caching store. Because the cache
	// was warmed write-through, both loads are served from hotcache and the
	// backing store's Load is never called.
	for i := 0; i < 2; i++ {
		st, _, lerr := cachingInstances.Load(ctx, instanceID)
		if lerr != nil {
			return lerr
		}
		logger.Info("instance load", "attempt", i+1, "status", st.Status.String())
	}
	logger.Info("instance cache effect",
		"backing_loads", countedInstances.loads,
		"note", "0 backing reads across 2 loads — served from hotcache")

	// Do the same for the human task. Find the token the runtime created (a
	// set-wide ClaimableBy query, which passes through to the backing store),
	// then point-read it twice through the caching task store.
	claimable, err := cachingTasks.ClaimableBy(ctx, reviewer)
	if err != nil {
		return err
	}
	if len(claimable) == 0 {
		return errors.New("expected one claimable task")
	}
	token := claimable[0].TaskToken
	for i := 0; i < 2; i++ {
		t, gerr := cachingTasks.Get(ctx, token)
		if gerr != nil {
			return gerr
		}
		logger.Info("task get", "attempt", i+1, "state", t.State.String())
	}
	logger.Info("human-task cache effect",
		"backend", taskBackend,
		"backing_gets", countedTasks.gets,
		"note", "point-reads served from the task cache after write-through")

	// --- Documented alternatives (not run here) ------------------------------
	//
	// Swap the in-process adapter — same cache.Provider interface:
	//
	//	instanceProvider := ottercache.New(ottercache.WithCapacity(8192))     // S3-FIFO/W-TinyLFU
	//	taskProvider     := memcache.New(gomcClient)                          // distributed, gomemcache
	//
	// Service-level SQL wiring — persistence.NewDurableProvider caches ON by
	// default (hotcache for both instance and human-task stores) and hands the
	// already-wrapped stores to the engine in one call. The DurableProvider keeps
	// TWO independent cache providers, one per store:
	//
	//	WithCacheProvider(p)            sets BOTH the instance-state and the
	//	                               human-task cache to p.
	//	WithInstanceCacheProvider(p)   sets ONLY the instance-state cache.
	//	WithHumanTaskCacheProvider(p)  sets ONLY the human-task cache.
	//
	// Options apply in order and later ones win, so this pair COMPOSES rather than
	// being two alternatives — WithCacheProvider puts both on Redis, then
	// WithInstanceCacheProvider overrides just the instance store back to hotcache:
	//
	//	provider, _ := persistence.NewDurableProvider(ctx, pgxPool,
	//		persistence.WithCacheProvider(rediscache.New(redisClient)),        // instance=Redis, tasks=Redis
	//		persistence.WithInstanceCacheProvider(hotcache.New()),             // instance=hotcache (overrides); tasks stay Redis
	//		persistence.WithDurableInstanceCacheTTL(time.Minute),
	//		// persistence.WithoutCache(),                                      // opt out entirely
	//	)
	//	eng, _ := service.NewEngine(service.WithDurableStore(provider))
	//
	// That split — instance state in the in-process hotcache, human tasks in a
	// shared Redis — is usually what you want in a MULTI-REPLICA deployment:
	// instance state is served from the fast in-process cache (guarded by
	// instance ownership, so only the owning replica caches it — no cross-replica
	// staleness), while human-task snapshots live in a shared Redis so ANY replica
	// can serve a claim/complete coherently. The equivalent, less order-dependent
	// way to express the same split is to set each store explicitly:
	//
	//	provider, _ := persistence.NewDurableProvider(ctx, pgxPool,
	//		persistence.WithInstanceCacheProvider(hotcache.New()),             // instance state → in-process
	//		persistence.WithHumanTaskCacheProvider(rediscache.New(redisClient)), // human tasks → shared Redis
	//	)
	//
	// -------------------------------------------------------------------------
	return nil
}

// taskCacheProvider builds the human-task cache substrate. It prefers a live
// Redis at REDIS_ADDR (default localhost:6379); if the ping fails it falls back
// to an in-process hotcache so the example still runs. It returns the provider,
// a human-readable backend label, and a close func for any Redis client opened.
func taskCacheProvider(ctx context.Context, logger *slog.Logger) (cache.Provider, string, func()) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	// Silence go-redis's internal dial-retry logging so the fallback path stays
	// quiet when no Redis is running; our own Warn below reports the outcome.
	redis.SetLogger(noopRedisLogger{})
	client := redis.NewClient(&redis.Options{
		Addr:        addr,
		DialTimeout: 1 * time.Second,
		MaxRetries:  -1, // fail fast instead of retrying a dead address
	})

	pingCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		logger.Warn("redis unreachable — falling back to in-process hotcache for human tasks",
			"addr", addr, "err", err)
		_ = client.Close()
		return hotcache.New(), "hotcache (redis fallback)", func() {}
	}
	logger.Info("redis connected for human-task cache", "addr", addr)
	return rediscache.New(client, rediscache.WithKeyPrefix("wrkflw:example:")),
		"redis (" + addr + ")",
		func() { _ = client.Close() }
}

// noopRedisLogger discards go-redis's internal logging (satisfies the logger
// interface accepted by redis.SetLogger via its Printf method).
type noopRedisLogger struct{}

func (noopRedisLogger) Printf(context.Context, string, ...any) {}

// countingInstanceStore counts backing Load calls so the example can show the
// cache absorbing reads. It embeds kernel.InstanceStore and overrides only Load.
type countingInstanceStore struct {
	kernel.InstanceStore
	loads int
}

func (s *countingInstanceStore) Load(ctx context.Context, id string) (engine.InstanceState, kernel.Version, error) {
	s.loads++
	return s.InstanceStore.Load(ctx, id)
}

// countingTaskStore counts backing Get calls for the same reason.
type countingTaskStore struct {
	humantask.TaskStore
	gets int
}

func (s *countingTaskStore) Get(ctx context.Context, token string) (humantask.HumanTask, error) {
	s.gets++
	return s.TaskStore.Get(ctx, token)
}
