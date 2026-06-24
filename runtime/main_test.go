package runtime_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs the package's tests under goleak. The runtime drives the engine
// and orchestrates background collaborators (relay, scheduler) in its tests;
// goleak ensures none of those tests leave a goroutine running after they return.
//
// The waivers cover third-party background goroutines not owned by this package
// that live for the process lifetime: testcontainers' Ryuk reaper connection and
// pgxpool's per-pool health-check goroutine.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
		goleak.IgnoreTopFunction("github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck"),
	)
}
