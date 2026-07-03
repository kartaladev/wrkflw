package service

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// LineageAdmin is the admin port for single-hop process-instance lineage
// lookups. It is satisfied by *runtime.LineageReader and can be wired directly
// in a transport (e.g. an HTTP handler or gRPC service) without any adapter.
type LineageAdmin interface {
	// Lineage returns the call and chain lineage for the given instanceID:
	// call parent (nil when root), call children (empty when none), chain
	// predecessor (nil when chain root), chain successors (empty when none).
	Lineage(ctx context.Context, instanceID string) (kernel.InstanceLineage, error)
}

// Compile-time assertion: *runtime.LineageReader satisfies LineageAdmin.
var _ LineageAdmin = (*runtime.LineageReader)(nil)
