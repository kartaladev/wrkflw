package cache

import (
	"context"
	"time"
)

// Codec adapts a byte-oriented [Cache] to a typed value V. When the underlying
// cache implements [ValueCache], Codec stores/returns live cloned values (no
// serialization); otherwise it marshals/unmarshals with the supplied functions.
type Codec[V any] struct {
	raw       Cache
	val       ValueCache
	marshal   func(V) ([]byte, error)
	unmarshal func([]byte) (V, error)
	clone     func(V) V
}

// NewCodec wraps c. marshal/unmarshal are used only on the byte path; clone is
// used only on the value path (to prevent aliasing of cached live values).
// Returns [ErrNilCache] when c is nil.
func NewCodec[V any](c Cache, marshal func(V) ([]byte, error), unmarshal func([]byte) (V, error), clone func(V) V) (*Codec[V], error) {
	if c == nil {
		return nil, ErrNilCache
	}
	cd := &Codec[V]{raw: c, marshal: marshal, unmarshal: unmarshal, clone: clone}
	if vc, ok := c.(ValueCache); ok {
		cd.val = vc
	}
	return cd, nil
}

// Get returns the value for key. A miss is (zero, false, nil).
func (cd *Codec[V]) Get(ctx context.Context, key string) (V, bool, error) {
	var zero V
	if cd.val != nil {
		v, ok, err := cd.val.GetValue(ctx, key)
		if err != nil || !ok {
			return zero, ok, err
		}
		tv, ok := v.(V)
		if !ok {
			return zero, false, nil // foreign value: treat as miss
		}
		return cd.clone(tv), true, nil
	}
	b, ok, err := cd.raw.Get(ctx, key)
	if err != nil || !ok {
		return zero, ok, err
	}
	tv, err := cd.unmarshal(b)
	if err != nil {
		return zero, false, err
	}
	return tv, true, nil
}

// Set stores v under key with ttl.
func (cd *Codec[V]) Set(ctx context.Context, key string, v V, ttl time.Duration) error {
	if cd.val != nil {
		return cd.val.SetValue(ctx, key, cd.clone(v), ttl)
	}
	b, err := cd.marshal(v)
	if err != nil {
		return err
	}
	return cd.raw.Set(ctx, key, b, ttl)
}

// Delete removes key.
func (cd *Codec[V]) Delete(ctx context.Context, key string) error {
	return cd.raw.Delete(ctx, key)
}
