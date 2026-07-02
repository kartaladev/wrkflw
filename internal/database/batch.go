package database

import "context"

// Batch accumulates queued statements to run together.
type Batch interface {
	Queue(query string, args ...any)
}

// Batcher sends a Batch. A Querier obtained from [From] implements Batcher when
// the underlying driver supports batching. The pgx adapter pipelines natively;
// the database/sql adapter (if added later) would emulate by sequential
// execution — identical observable results, no round-trip savings.
type Batcher interface {
	SendBatch(ctx context.Context, b Batch) BatchResults
}

// BatchResults iterates the results of a sent Batch, in queue order.
// Call [BatchResults.Close] when done (defer is recommended).
type BatchResults interface {
	Exec() (Result, error)
	Query() (Rows, error)
	Close() error
}

type queued struct {
	query string
	args  []any
}

type batch struct{ items []queued }

// NewBatch returns an empty Batch.
func NewBatch() Batch { return &batch{} }

func (b *batch) Queue(query string, args ...any) {
	b.items = append(b.items, queued{query, args})
}
