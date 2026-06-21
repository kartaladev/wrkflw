// Package gocron is the concrete gocron v2-backed Scheduler implementation
// (ADR-0009). It is internal: consumers reach it only through the module-root
// scheduling façade. gocron and clockwork are imported here only — never from
// engine/runtime/model code.
package gocron

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
)

// GocronScheduler is a production runtime.Scheduler backed by gocron v2. It
// shares the engine's clockwork time source so one fake-clock advance drives
// both engine timestamps and timer firing (ADR-0003, ADR-0009).
type GocronScheduler struct {
	sched gocron.Scheduler

	mu   sync.Mutex
	jobs map[string]uuid.UUID // timerID -> gocron job ID
}

// NewGocronScheduler constructs and starts a gocron-backed scheduler driven by
// clk. The caller must Close it to avoid leaking gocron's executor goroutine.
func NewGocronScheduler(clk clockwork.Clock) (*GocronScheduler, error) {
	s, err := gocron.NewScheduler(gocron.WithClock(clk))
	if err != nil {
		return nil, err
	}
	s.Start() // non-blocking
	return &GocronScheduler{
		sched: s,
		jobs:  make(map[string]uuid.UUID),
	}, nil
}

// Schedule registers a one-time timer that calls fire at or after fireAt. If a
// timer with the same timerID already exists it is replaced. Best-effort: a
// gocron job-creation error is logged and the timer is not armed.
func (s *GocronScheduler) Schedule(timerID string, fireAt time.Time, fire func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.jobs[timerID]; ok {
		_ = s.sched.RemoveJob(existing) // ignore ErrJobNotFound: already fired/pruned
		delete(s.jobs, timerID)
	}

	job, err := s.sched.NewJob(
		gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(fireAt)),
		gocron.NewTask(fire),
		gocron.WithEventListeners(gocron.AfterJobRuns(func(uuid.UUID, string) {
			s.mu.Lock()
			delete(s.jobs, timerID)
			s.mu.Unlock()
		})),
	)
	if err != nil {
		slog.Error("gocron: schedule timer failed", "timerID", timerID, "error", err)
		return
	}
	s.jobs[timerID] = job.ID()
}

// Cancel removes a pending timer. No-op if the timer is unknown or already fired.
func (s *GocronScheduler) Cancel(timerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.jobs[timerID]
	if !ok {
		return // unknown id: safe no-op
	}
	delete(s.jobs, timerID)
	if err := s.sched.RemoveJob(id); err != nil && !errors.Is(err, gocron.ErrJobNotFound) {
		slog.Error("gocron: cancel timer failed", "timerID", timerID, "error", err)
	}
}

// Close shuts gocron down gracefully. The scheduler cannot be reused afterward.
func (s *GocronScheduler) Close() error {
	return s.sched.Shutdown()
}
