package gocron_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs the package's tests under goleak so that a GocronScheduler whose
// executor goroutine is not Closed — or any other leaked goroutine spawned by the
// scheduler/locker code — fails the suite instead of silently surviving.
//
// gocron v2's scheduler is started in NewGocronScheduler and tears its executor
// goroutine down on Shutdown (called from GocronScheduler.Close). A correctly
// closed scheduler leaves no goroutines behind, so no IgnoreTopFunction waivers
// are required here.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// The Postgres-locker tests provision a real database via testcontainers.
		// testcontainers keeps a Ryuk "reaper" connection goroutine alive for the
		// life of the process to clean up containers, and pgxpool runs a background
		// health-check goroutine for the pool's lifetime. Neither is owned by the
		// scheduler under test, so they are scoped waivers rather than leaks to fix.
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
		goleak.IgnoreTopFunction("github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck"),
	)
}
