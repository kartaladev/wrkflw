package scheduler

import "context"

// JobStore is the durable persistence port for a single [JobKind]'s
// [ScheduledJob]s. A consumer registers one JobStore per kind via
// [WithJobStore]; the scheduler routes each operation to the store whose
// kind matches the job in play.
//
// There is deliberately no Update method: a job's next-fire state changes
// only through Save (re-persisting the full record) or Delete (removing it
// outright) — there is no partial-field mutation in this port.
type JobStore interface {
	// Load rebuilds this kind's executable [ScheduledJob]s from durable
	// storage, e.g. on process start. Implementations reconstruct each job's
	// [JobFunc] and [DataProvider] from persisted data — Load is where a
	// stored, inert record becomes a runnable job again.
	Load(ctx context.Context) ([]ScheduledJob, error)

	// Save persists j, creating or replacing the durable record for its ID.
	Save(ctx context.Context, j ScheduledJob) error

	// Delete removes the durable record identified by id. It is a no-op if
	// no such record exists.
	Delete(ctx context.Context, id string) error
}

// WithJobStore registers provide as the [JobStore] used for jobs of the
// given kind.
//
// provide is a thunk (func() JobStore) rather than a JobStore value so it can
// capture a collaborator (e.g. a process-driver-shaped store) that is only
// fully constructed after the scheduler itself — breaking the
// driver↔jobstore↔scheduler construction cycle. The thunk is invoked lazily;
// registering it does not call it. Once the scheduler first needs the store
// (a Save/Delete route or rehydration on Start), the thunk runs at most once
// and its result is cached.
//
// On first [NativeScheduler.Start] (or the first auto-start triggered by an
// arm) the scheduler self-rehydrates: every registered kind's store is Loaded
// and each returned [ScheduledJob] is activated. Per-job errors are logged at
// WARN and skipped; a Load error wrapping [ErrUnresolvedTimerDefinitions] is
// non-fatal (partial arm + WARN); any other Load error is surfaced to an
// explicit Start caller.
//
// A nil provide, or an empty kind, is silently ignored. Registering the same
// kind more than once keeps only the last registration.
func WithJobStore(kind JobKind, provide func() JobStore) Option {
	return func(c *config) {
		if provide == nil || kind == "" {
			return
		}
		if c.jobStores == nil {
			c.jobStores = make(map[JobKind]func() JobStore)
		}
		c.jobStores[kind] = provide
	}
}
