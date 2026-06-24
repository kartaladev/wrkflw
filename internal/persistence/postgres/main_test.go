package postgres_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs the package's tests under goleak so that the relay's listen loop
// and any other goroutine spawned by the persistence layer must exit on context
// cancellation; a leak fails the suite.
//
// The waivers below cover third-party background goroutines that are not owned by
// this package and live for the process lifetime: testcontainers' Ryuk reaper
// connection and pgxpool's per-pool health-check goroutine.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
		goleak.IgnoreTopFunction("github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck"),
	)
}
