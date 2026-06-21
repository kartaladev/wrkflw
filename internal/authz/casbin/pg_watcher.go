package casbin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/casbin/casbin/v2/persist"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time assertion: pgWatcher satisfies casbin persist.Watcher.
var _ persist.Watcher = (*pgWatcher)(nil)

const watcherReconnectDelay = time.Second

// pgWatcher is a casbin persist.Watcher backed by Postgres LISTEN/NOTIFY
// (ADR-0023, reusing the ADR-0022 mechanics). Update() emits a NOTIFY carrying
// this node's id; a listener goroutine invokes the update callback for every
// notification whose payload differs from this node's id (so a node ignores the
// echo of its own write).
type pgWatcher struct {
	pool    *pgxpool.Pool
	channel string
	nodeID  string

	mu       sync.Mutex
	callback func(string)

	cancel context.CancelFunc
	done   chan struct{}
}

func newPGWatcher(pool *pgxpool.Pool, channel, nodeID string) *pgWatcher {
	ctx, cancel := context.WithCancel(context.Background())
	w := &pgWatcher{
		pool:    pool,
		channel: channel,
		nodeID:  nodeID,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go w.listen(ctx)
	return w
}

// SetUpdateCallback stores the callback invoked when another node changes policy.
func (w *pgWatcher) SetUpdateCallback(cb func(string)) error {
	w.mu.Lock()
	w.callback = cb
	w.mu.Unlock()
	return nil
}

// Update notifies other nodes that this node changed the policy. The payload is
// this node's id so peers can ignore their own echo.
func (w *pgWatcher) Update() error {
	if _, err := w.pool.Exec(context.Background(),
		`SELECT pg_notify($1, $2)`, w.channel, w.nodeID); err != nil {
		return fmt.Errorf("casbin pgwatcher: notify: %w", err)
	}
	return nil
}

// Close stops the listener goroutine and waits for it to exit. It is safe to
// call more than once: cancel is idempotent and w.done is closed exactly once by
// the goroutine, so subsequent receives return immediately.
func (w *pgWatcher) Close() {
	w.cancel()
	<-w.done
}

// listen holds a dedicated connection, LISTENs on the channel, and invokes the
// callback for notifications from other nodes. It reconnects on transient
// failure with a cancellable backoff and exits on ctx cancel.
func (w *pgWatcher) listen(ctx context.Context) {
	defer close(w.done)
	for ctx.Err() == nil {
		conn, err := w.pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.backoff(ctx)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+w.channel); err != nil {
			conn.Release()
			if ctx.Err() != nil {
				return
			}
			w.backoff(ctx)
			continue
		}
		for ctx.Err() == nil {
			n, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				break // conn lost or ctx done; outer loop reconnects/exits
			}
			if n.Payload == w.nodeID {
				continue // ignore our own echo
			}
			w.mu.Lock()
			cb := w.callback
			w.mu.Unlock()
			if cb != nil {
				cb(n.Payload)
			}
		}
		conn.Release()
	}
}

func (w *pgWatcher) backoff(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(watcherReconnectDelay):
	}
}
