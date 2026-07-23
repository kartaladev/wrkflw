package scheduler_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/kartaladev/wrkflw/scheduler"
)

// exampleData is a minimal [scheduler.DataProvider] for the examples below:
// it performs no I/O, so Static reports true.
type exampleData map[string]any

func (d exampleData) Get(_ context.Context) (map[string]any, error) { return d, nil }
func (d exampleData) Static() bool                                  { return true }

// ExampleNewJob builds a one-shot [scheduler.Job] and inspects its identity.
func ExampleNewJob() {
	j, err := scheduler.NewJob(
		scheduler.JobKind("reminder"),
		scheduler.After(time.Minute),
		func(_ context.Context, _ scheduler.DataProvider) error { return nil },
		exampleData{"to": "ops@example.com"},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(j.Kind())
	fmt.Println(j.Activation() == scheduler.ActivationAuto)
	// Output:
	// reminder
	// true
}

// ExampleAt builds a one-shot [scheduler.Trigger] for an absolute instant and
// reads its next fire time back.
func ExampleAt() {
	fireAt := time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)
	trig := scheduler.At(fireAt)

	next, ok := trig.Next(fireAt.Add(-time.Hour))
	fmt.Println(ok, next.Equal(fireAt))
	// Output:
	// true true
}

// ExampleNewScheduler constructs a [scheduler.NativeScheduler], schedules a
// one-shot job against a fake clock, and advances the clock to observe the
// job fire.
func ExampleNewScheduler() {
	fakeClock := clockwork.NewFakeClock()
	s, err := scheduler.NewScheduler(scheduler.WithClock(fakeClock))
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() { _ = s.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	j, err := scheduler.NewJob(
		scheduler.JobKind("demo"),
		scheduler.At(fakeClock.Now().Add(time.Second)),
		func(_ context.Context, _ scheduler.DataProvider) error {
			defer wg.Done()
			fmt.Println("fired")
			return nil
		},
		exampleData{},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if _, err := s.Schedule(context.Background(), j); err != nil {
		fmt.Println("error:", err)
		return
	}

	_ = fakeClock.BlockUntilContext(context.Background(), 1)
	fakeClock.Advance(time.Second)
	wg.Wait()
	// Output:
	// fired
}

// exampleJobStore is a minimal in-memory [scheduler.JobStore] for
// ExampleWithJobStore below; a real consumer backs this port with durable
// storage (see the persistence package's SQL-backed timer store).
type exampleJobStore struct {
	mu    sync.Mutex
	items map[string]scheduler.ScheduledJob
}

func newExampleJobStore() *exampleJobStore {
	return &exampleJobStore{items: make(map[string]scheduler.ScheduledJob)}
}

func (s *exampleJobStore) Load(_ context.Context) ([]scheduler.ScheduledJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]scheduler.ScheduledJob, 0, len(s.items))
	for _, j := range s.items {
		out = append(out, j)
	}
	return out, nil
}

func (s *exampleJobStore) Save(_ context.Context, j scheduler.ScheduledJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[j.ID()] = j
	return nil
}

func (s *exampleJobStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

// ExampleWithJobStore registers a durable [scheduler.JobStore] for one
// [scheduler.JobKind]: jobs of that kind scheduled through this scheduler are
// persisted (and, on the next Start, rehydrated) through it.
func ExampleWithJobStore() {
	store := newExampleJobStore()
	s, err := scheduler.NewScheduler(
		scheduler.WithClock(clockwork.NewFakeClock()),
		scheduler.WithJobStore(scheduler.JobKind("demo"), func() scheduler.JobStore { return store }),
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() { _ = s.Close() }()

	j, err := scheduler.NewJob(
		scheduler.JobKind("demo"),
		scheduler.After(time.Minute),
		func(_ context.Context, _ scheduler.DataProvider) error { return nil },
		exampleData{},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if _, err := s.Schedule(context.Background(), j); err != nil {
		fmt.Println("error:", err)
		return
	}

	saved, err := store.Load(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(len(saved))
	// Output:
	// 1
}
