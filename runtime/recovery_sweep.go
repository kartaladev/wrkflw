package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Recovery-sweep defaults. Overridable via [WithRecoveryLease],
// [WithRecoverySweepInterval] and [WithRecoverySweepBatchSize].
const (
	// defaultRecoveryLease is how old a pending-command mark must be before a
	// PERIODIC sweep tick will re-drive it. It is comfortably above
	// defaultActionTimeout (30s), which bounds how long a single in-process
	// invocation can hold a mark open, so a healthy long-running action is never
	// overtaken by the ticker.
	defaultRecoveryLease = 5 * time.Minute
	// defaultRecoverySweepInterval is the gap between periodic sweep passes.
	defaultRecoverySweepInterval = time.Minute
	// defaultRecoverySweepBatchSize is the instance page size one pass reads,
	// matching the call notifier's claim batch.
	defaultRecoverySweepBatchSize = 100
)

// nonTerminalStatuses are the statuses an instance carrying a live
// pending-command mark can be in.
//
// A mark is only ever written for the commands the runtime performs AFTER the
// commit, and every command whose emission accompanies a terminal status is
// excluded from the mark by construction (see [engine.PendingCommandKind]): a
// terminal step's CompleteInstance / FailInstance are delivered inside the
// commit, and InvokeCancelAction is best-effort by contract. So restricting the
// scan to these two statuses loses nothing, and it is what makes the sweep an
// indexed lookup rather than a full-table walk — `wrkflw_instances_status_idx`
// is `(status) WHERE ended_at IS NULL`.
var nonTerminalStatuses = []engine.Status{engine.StatusRunning, engine.StatusCompensating}

// instanceLister resolves the enumeration capability the sweep needs.
//
// It is explicit ([WithInstanceLister]) first and PROBED off the configured
// store second. The probe matters: kernel.MemInstanceStore is itself a
// kernel.InstanceLister, so the reference wiring and every test driver get
// recovery with no extra configuration, while the SQL path — whose lister is a
// separate store.Lister over the same connection — supplies one explicitly.
//
// nil means the driver cannot enumerate instances, and the sweep is then a
// documented no-op rather than an error: a driver with no lister is exactly the
// pre-#110 driver.
func (driver *ProcessDriver) instanceLister() kernel.InstanceLister {
	if driver.lister != nil {
		return driver.lister
	}
	if l, ok := driver.store.(kernel.InstanceLister); ok {
		return l
	}
	return nil
}

// RecoverPendingCommands re-drives every instance whose most recent committed
// step carries commands that were never performed, and returns how many
// instances it recovered.
//
// This is the BOOT pass, and it deliberately ignores the lease: a mark found at
// boot cannot belong to a perform this process is running, because this process
// has not performed anything yet. [ProcessDriver.RunRecoverySweep] is the
// periodic counterpart and does honour the lease.
//
// ⚠ Recovery is AT-LEAST-ONCE, and cannot be otherwise while a command's only
// durable record is written in the same transaction as the step that emits it.
// Two residuals follow, and both are properties of the mechanism rather than
// defects in it:
//
//   - A second live process that is mid-perform on the same instance will have
//     its work duplicated. The ENGINE side is safe — the re-driven follow-up goes
//     through the CAS-retrying apply path and an already-resumed token answers
//     [engine.ErrTokenNotFound], which is treated as success — but the action's
//     own side effect runs twice. Actions reached through recovery should be
//     idempotent.
//   - A step whose perform failed part-way re-drives ALL of that step's
//     commands, including the ones that had already succeeded.
//
// It requires an enumeration capability ([WithInstanceLister], or a store that
// is itself a [kernel.InstanceLister]) and a definition registry
// ([WithDefinitions]). Without either it recovers nothing and returns no error:
// this is a capability, not a requirement, and a driver lacking it behaves
// exactly as it did before recovery existed.
//
// COST, stated because it is accepted rather than hidden: [kernel.InstanceSummary]
// carries no mark, so a pass reads one page of summaries and then one
// [kernel.InstanceStore.Load] per listed instance simply to test whether it is
// marked. On a deployment with tens of thousands of running instances that is
// tens of thousands of snapshot reads per pass, and the boot pass runs
// synchronously inside [ProcessDriver.Start]. It is the price of a mark that
// needs no migration. The cheap fix, if it ever bites, is a marked-instances
// predicate on [kernel.InstanceFilter] so the scan becomes an indexed lookup —
// deliberately not built here, because nothing has measured it yet.
func (driver *ProcessDriver) RecoverPendingCommands(ctx context.Context) (int, error) {
	return driver.sweepPendingCommands(ctx, 0)
}

// RunRecoverySweep re-drives abandoned pending commands on every tick until ctx
// is cancelled, returning ctx.Err() when the context is done.
//
// Each tick only re-drives marks OLDER than the configured lease
// ([WithRecoveryLease]), so a perform that is legitimately still running in this
// or another process is not overtaken. Unlike the boot pass, a sweep error is
// logged and the loop continues: a transient store failure must not silently
// stop recovery for the process's whole lifetime.
//
// It mirrors [calllink.CallNotifier.Run]: an immediate pass before the first
// tick, then one pass per tick — and, like it and like the outbox relay, it is a
// CONSUMER-STARTED background worker. Nothing in this module starts it:
//
//	go driver.RunRecoverySweep(ctx)
//
// Boot recovery alone ([ProcessDriver.Start]) already closes the crash window
// this mechanism exists for. The periodic loop covers the second shape — a
// process that stays up but abandons a perform, and the lent-transaction
// consumer who commits and never resumes — and a deployment that wants it opts
// in the same way it opts into relaying the outbox.
func (driver *ProcessDriver) RunRecoverySweep(ctx context.Context) error {
	ticker := driver.clk.NewTicker(driver.recoverySweepInterval)
	defer ticker.Stop()

	driver.sweepTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.Chan():
			driver.sweepTick(ctx)
		}
	}
}

// sweepTick runs one lease-honouring pass and logs, rather than propagates, its
// error.
func (driver *ProcessDriver) sweepTick(ctx context.Context) {
	if _, err := driver.sweepPendingCommands(ctx, driver.recoveryLease); err != nil && ctx.Err() == nil {
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: recovery sweep pass failed, continuing",
			append(driver.obs.tel.LogAttrs(ctx), slog.Any("error", err))...)
	}
}

// sweepPendingCommands is the one pass shared by the boot and periodic entry
// points. minAge is how old a mark must be to be re-driven; zero re-drives every
// mark it finds.
func (driver *ProcessDriver) sweepPendingCommands(ctx context.Context, minAge time.Duration) (int, error) {
	lister := driver.instanceLister()
	if lister == nil || driver.defsReg == nil {
		return 0, nil
	}
	release, ok := driver.admit()
	if !ok {
		return 0, ErrDriverShuttingDown
	}
	defer release()

	cutoff := driver.clk.Now().Add(-minAge)
	recovered := 0
	for _, status := range nonTerminalStatuses {
		cursor := ""
		for {
			page, err := lister.List(ctx, kernel.InstanceFilter{
				Status: &status,
				Limit:  driver.recoverySweepBatchSize,
				Cursor: cursor,
			})
			if err != nil {
				return recovered, fmt.Errorf("workflow-runtime: recovery sweep: list %s: %w", status, err)
			}
			for _, item := range page.Items {
				if ctx.Err() != nil {
					return recovered, ctx.Err()
				}
				n, err := driver.recoverInstance(ctx, item.InstanceID, cutoff)
				if err != nil {
					// One unrecoverable instance must never abort the batch — the
					// same rule RehydrateTimers applies to one unschedulable timer.
					driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: recovery sweep: instance skipped",
						append(driver.obs.tel.LogAttrs(ctx),
							slog.String("instance_id", item.InstanceID),
							slog.Any("error", err))...)
					continue
				}
				recovered += n
			}
			if !page.HasMore || page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	return recovered, nil
}

// recoverInstance re-drives one instance's unperformed commands, returning 1
// when it did and 0 when the instance needed nothing.
//
// Ordering is deliberate: the follow-up triggers are applied FIRST and the mark
// is cleared LAST. A follow-up commits a new snapshot carrying its own mark, at
// a new version, so the trailing clear then finds a moved version and no-ops —
// which is exactly right. Clearing first would instead drop the mark and then
// risk losing the follow-up, turning a recoverable instance into an
// unrecoverable one on the failure path.
func (driver *ProcessDriver) recoverInstance(ctx context.Context, instanceID string, cutoff time.Time) (int, error) {
	st, token, err := driver.store.Load(ctx, instanceID)
	if err != nil {
		return 0, fmt.Errorf("load: %w", err)
	}
	if len(st.PendingCommands) == 0 {
		return 0, nil
	}
	if st.PendingCommandsAt.After(cutoff) {
		// Inside the lease: assume a live perform still owns these commands.
		return 0, nil
	}

	def, err := driver.defsReg.Lookup(ctx, model.Version(st.DefID, st.DefVersion))
	if err != nil {
		// Leave the mark standing. An unresolvable definition is a registration
		// problem, and the instance becomes recoverable the moment it is fixed —
		// the same call the CallNotifier makes for an unresolvable parent def.
		return 0, fmt.Errorf("lookup %s: %w", model.Version(st.DefID, st.DefVersion), err)
	}

	var followups []engine.Trigger
	for _, pending := range st.PendingCommands {
		cmd, cerr := pending.Command()
		if cerr != nil {
			// A mark this build cannot read (a kind written by a newer one). Report
			// it rather than re-driving it as something else.
			return 0, cerr
		}
		next, perr := driver.perform(ctx, def, st, cmd)
		if perr != nil {
			return 0, fmt.Errorf("perform %s: %w", pending.Kind, perr)
		}
		if next != nil {
			followups = append(followups, next)
		}
	}

	driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelInfo, "runtime: recovery sweep: re-drove a committed step's unperformed commands",
		append(driver.obs.tel.LogAttrs(ctx),
			slog.String("instance_id", instanceID),
			slog.Int("command_count", len(st.PendingCommands)))...)

	for _, trg := range followups {
		if _, aerr := driver.applyTriggerRetryingCAS(ctx, def, instanceID, trg); aerr != nil {
			if errors.Is(aerr, engine.ErrTokenNotFound) {
				// Another writer already resumed this token. The command was
				// duplicated, the engine state was not: a benign at-least-once
				// outcome, exactly as the call notifier treats it.
				continue
			}
			return 0, fmt.Errorf("apply %T: %w", trg, aerr)
		}
	}
	driver.clearPendingCommands(ctx, instanceID, token)
	return 1, nil
}
