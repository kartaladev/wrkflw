package runtime

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ResolveIncident clears the named incident on an instance, grants addAttempts
// additional retries, and re-invokes the parked action. It is the admin entry
// point for recovering a retry-exhausted activity. Delegates through Deliver so
// the trigger is journalled and persisted.
func (r *ProcessDriver) ResolveIncident(ctx context.Context, def *definition.ProcessDefinition, instanceID, incidentID string, addAttempts int) (engine.InstanceState, error) {
	st, err := r.Deliver(ctx, def, instanceID, engine.NewResolveIncident(r.clk.Now(), incidentID, addAttempts))
	if err == nil {
		r.obs.incidentsResolved.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
	}
	return st, err
}
