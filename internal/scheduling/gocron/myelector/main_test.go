package myelector_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs the package's tests under goleak so that a MySQLElector whose
// heartbeat goroutine is not Closed — or any other leaked goroutine — fails the
// suite instead of silently surviving. A correctly closed elector leaves no
// goroutines behind.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// These elector tests provision a real database via testcontainers, which
		// keeps a Ryuk "reaper" connection goroutine alive for the life of the
		// process to clean up containers. It is not owned by the elector under
		// test, so it is a scoped waiver rather than a leak to fix.
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
	)
}
