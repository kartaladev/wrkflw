package cachetest

import (
	"context"
	"fmt"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// RunTestRedis starts a throwaway Redis 7 container and returns its host:port.
// It skips the test when no Docker daemon is reachable.
func RunTestRedis(t *testing.T) string {
	t.Helper()
	return runContainer(t, "redis:7-alpine", "6379/tcp")
}

// RunTestMemcached starts a throwaway Memcached 1.6 container and returns its host:port.
// It skips the test when no Docker daemon is reachable.
func RunTestMemcached(t *testing.T) string {
	t.Helper()
	return runContainer(t, "memcached:1.6-alpine", "11211/tcp")
}

// runContainer is the shared generic-container launcher. image is the Docker
// image tag; port is the exposed port in "NNNN/tcp" notation.
func runContainer(t *testing.T, image, port string) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        image,
			ExposedPorts: []string{port},
			WaitingFor:   wait.ForListeningPort(port),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("docker unavailable, skipping: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	return fmt.Sprintf("%s:%s", host, mapped.Port())
}
