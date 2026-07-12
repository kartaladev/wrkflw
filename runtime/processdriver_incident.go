package runtime

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// ResolveIncident clears the named incident on an instance, grants addAttempts
// additional retries, and re-invokes the parked action. It is the admin entry
// point for recovering a retry-exhausted activity. Delegates through ApplyTrigger so
// the trigger is journalled and persisted.
func (driver *ProcessDriver) ResolveIncident(ctx context.Context, def *model.ProcessDefinition, instanceID, incidentID string, addAttempts int) (engine.InstanceState, error) {
	st, err := driver.ApplyTrigger(ctx, def, instanceID, engine.NewResolveIncident(driver.clk.Now(), incidentID, addAttempts))
	if err == nil {
		driver.obs.incidentsResolved.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
	}
	return st, err
}
