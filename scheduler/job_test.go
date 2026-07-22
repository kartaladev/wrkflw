package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/scheduler"
)

func noopJobFunc(context.Context, scheduler.DataProvider) error { return nil }

// fakeDataProvider is a minimal, package-local [scheduler.DataProvider] used
// so job_test.go's RED/GREEN cycle does not depend on dataprovider.go's own
// (separately TDD'd) constructors.
type fakeDataProvider struct{}

func (*fakeDataProvider) Get(context.Context) (map[string]any, error) { return map[string]any{}, nil }
func (*fakeDataProvider) Static() bool                                { return true }

func TestNewJob(t *testing.T) {
	t.Parallel()

	validKind := scheduler.JobKind("demo")
	validTrigger := scheduler.After(time.Minute)
	validData := &fakeDataProvider{}

	type testCase struct {
		name   string
		kind   scheduler.JobKind
		trig   scheduler.Trigger
		fn     scheduler.JobFunc
		data   scheduler.DataProvider
		opts   []scheduler.JobOption
		assert func(t *testing.T, j scheduler.Job, err error)
	}

	cases := []testCase{
		{
			name: "auto-generates a UUID id and defaults to auto activation",
			kind: validKind,
			trig: validTrigger,
			fn:   noopJobFunc,
			data: validData,
			assert: func(t *testing.T, j scheduler.Job, err error) {
				require.NoError(t, err)
				require.NotNil(t, j)
				assert.NotEmpty(t, j.ID())
				_, uerr := uuid.Parse(j.ID())
				assert.NoError(t, uerr, "id must be a valid UUID string")
				assert.Equal(t, scheduler.ActivationAuto, j.Activation())
				assert.Equal(t, validKind, j.Kind())
				assert.Equal(t, validTrigger, j.Trigger())
				assert.Same(t, validData, j.Data())
			},
		},
		{
			name: "WithManualActivation flips activation to manual",
			kind: validKind,
			trig: validTrigger,
			fn:   noopJobFunc,
			data: validData,
			opts: []scheduler.JobOption{scheduler.WithManualActivation()},
			assert: func(t *testing.T, j scheduler.Job, err error) {
				require.NoError(t, err)
				require.NotNil(t, j)
				assert.Equal(t, scheduler.ActivationManual, j.Activation())
			},
		},
		{
			name: "empty kind errors",
			kind: "",
			trig: validTrigger,
			fn:   noopJobFunc,
			data: validData,
			assert: func(t *testing.T, j scheduler.Job, err error) {
				require.Error(t, err)
				assert.Nil(t, j)
				assert.Contains(t, err.Error(), "workflow-scheduler:")
			},
		},
		{
			name: "zero trigger errors",
			kind: validKind,
			trig: scheduler.Trigger{},
			fn:   noopJobFunc,
			data: validData,
			assert: func(t *testing.T, j scheduler.Job, err error) {
				require.Error(t, err)
				assert.Nil(t, j)
				assert.Contains(t, err.Error(), "workflow-scheduler:")
			},
		},
		{
			name: "nil action func errors",
			kind: validKind,
			trig: validTrigger,
			fn:   nil,
			data: validData,
			assert: func(t *testing.T, j scheduler.Job, err error) {
				require.Error(t, err)
				assert.Nil(t, j)
				assert.Contains(t, err.Error(), "workflow-scheduler:")
			},
		},
		{
			name: "nil data provider errors",
			kind: validKind,
			trig: validTrigger,
			fn:   noopJobFunc,
			data: nil,
			assert: func(t *testing.T, j scheduler.Job, err error) {
				require.Error(t, err)
				assert.Nil(t, j)
				assert.Contains(t, err.Error(), "workflow-scheduler:")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			j, err := scheduler.NewJob(tc.kind, tc.trig, tc.fn, tc.data, tc.opts...)
			tc.assert(t, j, err)
		})
	}
}

func TestNewJobWithID(t *testing.T) {
	t.Parallel()

	validKind := scheduler.JobKind("demo")
	validTrigger := scheduler.After(time.Minute)
	validData := &fakeDataProvider{}

	type testCase struct {
		name   string
		id     string
		assert func(t *testing.T, j scheduler.Job, err error)
	}

	cases := []testCase{
		{
			name: "empty id errors",
			id:   "",
			assert: func(t *testing.T, j scheduler.Job, err error) {
				require.Error(t, err)
				assert.Nil(t, j)
				assert.Contains(t, err.Error(), "workflow-scheduler:")
			},
		},
		{
			name: "explicit id is preserved verbatim",
			id:   "job-42",
			assert: func(t *testing.T, j scheduler.Job, err error) {
				require.NoError(t, err)
				require.NotNil(t, j)
				assert.Equal(t, "job-42", j.ID())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			j, err := scheduler.NewJobWithID(tc.id, validKind, validTrigger, noopJobFunc, validData)
			tc.assert(t, j, err)
		})
	}
}

func TestNewScheduledJob(t *testing.T) {
	t.Parallel()

	validJob, err := scheduler.NewJob(scheduler.JobKind("demo"), scheduler.After(time.Minute), noopJobFunc, &fakeDataProvider{})
	require.NoError(t, err)

	nextRun := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name    string
		job     scheduler.Job
		nextRun time.Time
		assert  func(t *testing.T, sj scheduler.ScheduledJob, err error)
	}

	cases := []testCase{
		{
			name:    "nil job errors",
			job:     nil,
			nextRun: nextRun,
			assert: func(t *testing.T, sj scheduler.ScheduledJob, err error) {
				require.Error(t, err)
				assert.Nil(t, sj)
				assert.Contains(t, err.Error(), "workflow-scheduler:")
			},
		},
		{
			name:    "round-trips NextRun and the wrapped Job",
			job:     validJob,
			nextRun: nextRun,
			assert: func(t *testing.T, sj scheduler.ScheduledJob, err error) {
				require.NoError(t, err)
				require.NotNil(t, sj)
				assert.True(t, nextRun.Equal(sj.NextRun()))
				assert.Equal(t, validJob.ID(), sj.ID())
				assert.Equal(t, validJob.Kind(), sj.Kind())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sj, err := scheduler.NewScheduledJob(tc.job, tc.nextRun)
			tc.assert(t, sj, err)
		})
	}
}

// TestJobSingleton exercises the unexported singleton() flag (production item
// ③, overrun protection) through the export_test.go test-only accessor
// scheduler.JobIsSingleton, since the method itself is unexported package API
// consumed only by the in-package façade (Tasks 5-11).
func TestJobSingleton(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		trig   scheduler.Trigger
		opts   []scheduler.JobOption
		assert func(t *testing.T, singleton bool)
	}

	cases := []testCase{
		{
			name: "recurring trigger defaults to singleton",
			trig: scheduler.Every(time.Minute),
			assert: func(t *testing.T, singleton bool) {
				assert.True(t, singleton)
			},
		},
		{
			name: "one-shot trigger defaults to non-singleton",
			trig: scheduler.After(time.Minute),
			assert: func(t *testing.T, singleton bool) {
				assert.False(t, singleton)
			},
		},
		{
			name: "WithoutOverrunProtection opts a recurring job out of singleton mode",
			trig: scheduler.Every(time.Minute),
			opts: []scheduler.JobOption{scheduler.WithoutOverrunProtection()},
			assert: func(t *testing.T, singleton bool) {
				assert.False(t, singleton)
			},
		},
		{
			name: "WithoutOverrunProtection has no effect on one-shots",
			trig: scheduler.After(time.Minute),
			opts: []scheduler.JobOption{scheduler.WithoutOverrunProtection()},
			assert: func(t *testing.T, singleton bool) {
				assert.False(t, singleton)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			j, err := scheduler.NewJob(scheduler.JobKind("demo"), tc.trig, noopJobFunc, &fakeDataProvider{}, tc.opts...)
			require.NoError(t, err)

			tc.assert(t, scheduler.JobIsSingleton(j))
		})
	}
}
