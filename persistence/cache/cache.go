// Package cache defines a neutral, library-agnostic cache substrate used by the
// persistence layer. Adapters (in-memory or distributed) implement [Cache];
// in-process adapters may additionally implement [ValueCache] to avoid
// serialization on the hot path. [Provider] is a factory a consumer supplies via
// the persistence caching options so the store wires per-kind caches internally.
package cache

import (
	"context"
	"errors"
	"time"
)

// ErrNilCache is returned by constructors when a required Cache is nil.
var ErrNilCache = errors.New("workflow-cache: nil cache")

// Cache is the byte-oriented substrate every adapter implements. A miss returns
// (nil, false, nil); an I/O failure returns a non-nil error. ttl <= 0 means the
// adapter's configured default (or no expiry).
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// ValueCache is an optional capability implemented by in-process adapters that
// can store live values without serialization. [Codec] type-asserts it and, when
// present, skips (un)marshaling. Mirrors the dialect.Notifier/Locker pattern.
type ValueCache interface {
	GetValue(ctx context.Context, key string) (any, bool, error)
	SetValue(ctx context.Context, key string, v any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Provider builds namespaced caches. A store calls it once per cache-kind
// (e.g. "instances", "humantasks") so a consumer supplies one Provider and the
// store wires the concrete caches.
type Provider interface {
	Cache(namespace string) (Cache, error)
}
