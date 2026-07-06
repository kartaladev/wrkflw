package cachetest_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache/cachetest"
)

func TestRunTestRedisReturnsAddr(t *testing.T) {
	addr := cachetest.RunTestRedis(t)
	if addr == "" {
		t.Fatal("expected non-empty redis addr")
	}
}

func TestRunTestMemcachedReturnsAddr(t *testing.T) {
	addr := cachetest.RunTestMemcached(t)
	if addr == "" {
		t.Fatal("expected non-empty memcached addr")
	}
}
