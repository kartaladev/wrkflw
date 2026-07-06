package cache_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
)

type payload struct {
	N int `json:"n"`
}

func clonePayload(p payload) payload { return p }

// byteFake is a minimal byte-only Cache (no ValueCache) exercising the JSON path.
type byteFake struct{ m map[string][]byte }

func newByteFake() *byteFake { return &byteFake{m: map[string][]byte{}} }
func (f *byteFake) Get(_ context.Context, k string) ([]byte, bool, error) {
	v, ok := f.m[k]
	return v, ok, nil
}
func (f *byteFake) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	f.m[k] = v
	return nil
}
func (f *byteFake) Delete(_ context.Context, k string) error { delete(f.m, k); return nil }

// valueFake also implements ValueCache exercising the live-value fast path.
type valueFake struct {
	byteFake
	vals    map[string]any
	setCall int
}

func newValueFake() *valueFake { return &valueFake{byteFake: *newByteFake(), vals: map[string]any{}} }
func (f *valueFake) GetValue(_ context.Context, k string) (any, bool, error) {
	v, ok := f.vals[k]
	return v, ok, nil
}
func (f *valueFake) SetValue(_ context.Context, k string, v any, _ time.Duration) error {
	f.setCall++
	f.vals[k] = v
	return nil
}
func (f *valueFake) Delete(_ context.Context, k string) error {
	delete(f.vals, k)
	delete(f.m, k)
	return nil
}

func TestCodec(t *testing.T) {
	tests := []struct {
		name     string
		newCache func() cache.Cache
		assert   func(t *testing.T, cd *cache.Codec[payload], c cache.Cache)
	}{
		{
			name:     "byte path round-trips via JSON",
			newCache: func() cache.Cache { return newByteFake() },
			assert: func(t *testing.T, cd *cache.Codec[payload], c cache.Cache) {
				ctx := t.Context()
				if err := cd.Set(ctx, "k", payload{N: 7}, time.Minute); err != nil {
					t.Fatalf("set: %v", err)
				}
				// A byte-only cache must hold JSON bytes, not a live value.
				raw, _, _ := c.Get(ctx, "k")
				var p payload
				if err := json.Unmarshal(raw, &p); err != nil || p.N != 7 {
					t.Fatalf("stored bytes not JSON payload: %q err=%v", raw, err)
				}
				got, ok, err := cd.Get(ctx, "k")
				if err != nil || !ok || got.N != 7 {
					t.Fatalf("get = %+v ok=%v err=%v", got, ok, err)
				}
			},
		},
		{
			name:     "value path uses ValueCache and skips JSON",
			newCache: func() cache.Cache { return newValueFake() },
			assert: func(t *testing.T, cd *cache.Codec[payload], c cache.Cache) {
				ctx := t.Context()
				vf := c.(*valueFake)
				if err := cd.Set(ctx, "k", payload{N: 9}, time.Minute); err != nil {
					t.Fatalf("set: %v", err)
				}
				if vf.setCall != 1 {
					t.Fatalf("expected ValueCache.SetValue used, setCall=%d", vf.setCall)
				}
				if len(vf.m) != 0 {
					t.Fatalf("byte map should be empty on value path, got %v", vf.m)
				}
				got, ok, err := cd.Get(ctx, "k")
				if err != nil || !ok || got.N != 9 {
					t.Fatalf("get = %+v ok=%v err=%v", got, ok, err)
				}
			},
		},
		{
			name:     "miss returns ok=false",
			newCache: func() cache.Cache { return newByteFake() },
			assert: func(t *testing.T, cd *cache.Codec[payload], _ cache.Cache) {
				_, ok, err := cd.Get(t.Context(), "absent")
				if err != nil || ok {
					t.Fatalf("miss = ok=%v err=%v", ok, err)
				}
			},
		},
		{
			name:     "value path delete removes entry",
			newCache: func() cache.Cache { return newValueFake() },
			assert: func(t *testing.T, cd *cache.Codec[payload], _ cache.Cache) {
				ctx := t.Context()
				if err := cd.Set(ctx, "k", payload{N: 42}, time.Minute); err != nil {
					t.Fatalf("set: %v", err)
				}
				if err := cd.Delete(ctx, "k"); err != nil {
					t.Fatalf("delete: %v", err)
				}
				_, ok, err := cd.Get(ctx, "k")
				if err != nil || ok {
					t.Fatalf("after value-path delete: ok=%v err=%v", ok, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.newCache()
			cd, err := cache.NewCodec[payload](c,
				func(v payload) ([]byte, error) { return json.Marshal(v) },
				func(b []byte) (payload, error) {
					var p payload
					return p, json.Unmarshal(b, &p)
				}, clonePayload)
			if err != nil {
				t.Fatalf("new codec: %v", err)
			}
			tt.assert(t, cd, c)
		})
	}
}

func TestCodecDelete(t *testing.T) {
	c := newByteFake()
	cd, err := cache.NewCodec[payload](c,
		func(v payload) ([]byte, error) { return json.Marshal(v) },
		func(b []byte) (payload, error) {
			var p payload
			return p, json.Unmarshal(b, &p)
		}, clonePayload)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	ctx := t.Context()
	if err := cd.Set(ctx, "k", payload{N: 1}, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := cd.Delete(ctx, "k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, ok, err := cd.Get(ctx, "k")
	if err != nil || ok {
		t.Fatalf("after delete: ok=%v err=%v", ok, err)
	}
}

func TestNewCodecNilCache(t *testing.T) {
	_, err := cache.NewCodec[payload](nil, func(v payload) ([]byte, error) { return json.Marshal(v) }, nil, clonePayload)
	if !errors.Is(err, cache.ErrNilCache) {
		t.Fatalf("expected ErrNilCache, got %v", err)
	}
}
