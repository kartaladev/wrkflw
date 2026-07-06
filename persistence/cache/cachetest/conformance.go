// Package cachetest provides a reusable conformance suite for cache.Provider
// adapters plus testcontainers helpers for distributed backends.
package cachetest

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
)

// RunConformance exercises the behavioral contract every cache.Cache must honor.
// newProvider must return a fresh, empty provider on each call.
func RunConformance(t *testing.T, newProvider func() cache.Provider) {
	t.Helper()

	t.Run("miss returns false", func(t *testing.T) {
		c, err := newProvider().Cache("ns")
		if err != nil {
			t.Fatalf("cache: %v", err)
		}
		_, ok, err := c.Get(t.Context(), "absent")
		if err != nil || ok {
			t.Fatalf("miss = ok=%v err=%v", ok, err)
		}
	})

	t.Run("set then get round-trips", func(t *testing.T) {
		c, err := newProvider().Cache("ns")
		if err != nil {
			t.Fatalf("cache: %v", err)
		}
		if err := c.Set(t.Context(), "k", []byte("v"), time.Minute); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, ok, err := c.Get(t.Context(), "k")
		if err != nil || !ok || string(got) != "v" {
			t.Fatalf("get = %q ok=%v err=%v", got, ok, err)
		}
	})

	t.Run("overwrite replaces value", func(t *testing.T) {
		c, err := newProvider().Cache("ns")
		if err != nil {
			t.Fatalf("cache: %v", err)
		}
		if err := c.Set(t.Context(), "k", []byte("a"), time.Minute); err != nil {
			t.Fatalf("set: %v", err)
		}
		_ = c.Set(t.Context(), "k", []byte("b"), time.Minute)
		got, _, _ := c.Get(t.Context(), "k")
		if string(got) != "b" {
			t.Fatalf("overwrite got %q", got)
		}
	})

	t.Run("delete removes", func(t *testing.T) {
		c, err := newProvider().Cache("ns")
		if err != nil {
			t.Fatalf("cache: %v", err)
		}
		if err := c.Set(t.Context(), "k", []byte("v"), time.Minute); err != nil {
			t.Fatalf("set: %v", err)
		}
		if err := c.Delete(t.Context(), "k"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		_, ok, _ := c.Get(t.Context(), "k")
		if ok {
			t.Fatal("expected miss after delete")
		}
	})

	t.Run("namespaces are isolated", func(t *testing.T) {
		p := newProvider()
		a, err := p.Cache("a")
		if err != nil {
			t.Fatalf("cache a: %v", err)
		}
		b, err := p.Cache("b")
		if err != nil {
			t.Fatalf("cache b: %v", err)
		}
		_ = a.Set(t.Context(), "k", []byte("va"), time.Minute)
		if _, ok, _ := b.Get(t.Context(), "k"); ok {
			t.Fatal("namespace b should not see namespace a's key")
		}
	})
}
