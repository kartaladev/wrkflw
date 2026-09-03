package runtime

import (
	"context"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// ResolveCompensationStall applies an operator's escape from a compensation walk
// whose dispatched action stopped reporting back: retry re-dispatches
// the stalled record, skip gives up on it and advances the walk, and abandon
// ends the walk and terminates the instance.
//
// commandID is REQUIRED and must equal the walk's in-flight command id — read it
// from the instance's compensation cursor, which the service projection exposes.
// That match is what makes the verbs idempotent: a replay finds the cursor
// already moved on and is refused rather than acting on whatever is in flight
// now. incidentID is optional; empty targets the walk in flight.
//
// It delegates through applyTrigger so the operator's decision is journalled and
// persisted like any other trigger.
//
// ⚠ abandon is accepted only on a walk that TERMINATES (a cancel, error or admin
// full rollback). On a compensation throw, a partial rollback or a reverse it
// returns engine.ErrCompensationWalkResumes and names skip instead — on those
// walks it was measured destroying records the walk had never run.
func (driver *ProcessDriver) ResolveCompensationStall(ctx context.Context, def *model.ProcessDefinition, instanceID, commandID, incidentID string, disposition engine.CompensationDisposition) (engine.InstanceState, error) {
	release, ok := driver.admit()
	if !ok {
		return engine.InstanceState{}, ErrDriverShuttingDown
	}
	defer release()

	trg := engine.NewResolveCompensationStall(driver.clk.Now(), commandID, incidentID, disposition)
	return driver.applyTrigger(ctx, def, instanceID, trg)
}
