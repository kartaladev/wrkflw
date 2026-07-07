package pgelector_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs the package's tests under goleak so that a PostgresElector whose
// heartbeat goroutine is not Closed — or any other leaked goroutine — fails the
// suite instead of silently surviving. A correctly closed elector leaves no
// goroutines behind.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// These elector tests provision a real database via testcontainers.
		// testcontainers keeps a Ryuk "reaper" connection goroutine alive for the
		// life of the process to clean up containers, and pgxpool runs a background
		// health-check goroutine for the pool's lifetime. Neither is owned by the
		// elector under test, so they are scoped waivers rather than leaks to fix.
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
		goleak.IgnoreTopFunction("github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck"),
	)
}
