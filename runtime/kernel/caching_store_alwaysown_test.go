package kernel_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// syncBuffer is a goroutine-safe buffer for capturing slog output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestNewCachingStoreAlwaysOwnWarning(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		owner  kernel.Ownership
		assert func(t *testing.T, logged string)
	}

	cases := []testCase{
		{
			name:  "AlwaysOwn emits a single-replica warning",
			owner: kernel.AlwaysOwn{},
			assert: func(t *testing.T, logged string) {
				assert.Contains(t, strings.ToLower(logged), "single")
				assert.Contains(t, logged, "AlwaysOwn")
			},
		},
		{
			name:  "a real ownership emits no warning",
			owner: stubOwnership{},
			assert: func(t *testing.T, logged string) {
				assert.Empty(t, logged)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf syncBuffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			mustCachingStore(t,
				mustMemStore(t),
				tc.owner,
				kernel.WithCachingStoreClock(clockwork.NewFakeClock()),
				kernel.WithCacheLogger(logger),
			)

			tc.assert(t, buf.String())
		})
	}
}

// stubOwnership is a non-AlwaysOwn Ownership for the no-warning case.
type stubOwnership struct{}

func (stubOwnership) Acquire(context.Context, string) (bool, error) { return true, nil }
func (stubOwnership) Release(context.Context, string) error         { return nil }
