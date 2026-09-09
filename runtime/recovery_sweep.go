package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
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
// This is the BOOT pass. It honours the same grace window as the periodic one
// ([WithRecoveryLease]), and that is a correction, not an oversight: an earlier
// version skipped it on the reasoning that "a mark found at boot cannot belong to
// a perform this process is running". True, and beside the point — it can belong
// to a perform ANOTHER LIVE REPLICA is running, and the durable path this sweep
// is wired for (see [WithInstanceLister]) is exactly the multi-replica one. A
// rolling restart would otherwise re-perform every in-flight command of every
// surviving replica.
//
// ⚠ CONCURRENCY, stated precisely because an earlier version of this comment
// overclaimed it. What follows is NOT a lease in the sense
// [kernel.CallLinkStore] uses: there is no claim, nothing is written, nothing is
// hidden from another worker. It is an AGE COMPARISON against a stamp the
// committing step wrote — a grace window, and two replicas evaluating it
// concurrently both pass.
//
// The exclusion mechanism, when there is one, is [WithInstanceOwnership]. With an
// ownership port configured, this pass re-drives only instances this process owns
// and a second replica skips them. Without one, recovery is at-least-once ACROSS
// replicas: N replicas can each perform the same abandoned command once.
//
// Two further residuals, both properties of the mechanism rather than defects:
//
//   - A process that is mid-perform on the same instance can have its work
//     duplicated once the grace window expires. The ENGINE side is safe — the
//     re-driven follow-up goes through the CAS-retrying apply path and an
//     already-resumed token answers [engine.ErrTokenNotFound], which is treated as
//     success — but the action's own side effect runs twice. Actions reached
//     through recovery should be idempotent, and
//     [ProcessDriver.alreadyPerformed] narrows this to the two commands that
//     leave no durable evidence of having run.
//   - The stamp is written by one node's wall clock and compared against
//     another's. Clock skew shifts the window one-for-one: a sweeper five minutes
//     fast against a five-minute window has effectively none. No DB clock and no
//     monotonic source is used. Size [WithRecoveryLease] above your fleet's skew
//     bound, or configure [WithInstanceOwnership], which does not depend on the
//     clock at all.
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
	return driver.sweepPendingCommands(ctx)
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
	if _, err := driver.sweepPendingCommands(ctx); err != nil && ctx.Err() == nil {
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: recovery sweep pass failed, continuing",
			append(driver.obs.tel.LogAttrs(ctx), slog.Any("error", err))...)
	}
}

// sweepPendingCommands is the one pass shared by the boot and periodic entry
// points. Both honour the same lease: see [ProcessDriver.RecoverPendingCommands].
func (driver *ProcessDriver) sweepPendingCommands(ctx context.Context) (int, error) {
	lister := driver.instanceLister()
	if lister == nil || driver.defsReg == nil {
		return 0, nil
	}
	release, ok := driver.admit()
	if !ok {
		return 0, ErrDriverShuttingDown
	}
	defer release()

	cutoff := driver.clk.Now().Add(-driver.recoveryLease)
	recovered := 0
	cursor := ""
	for {
		// Status is nil — EVERY status, terminal included, and that is not an
		// oversight. A terminal step can carry a mark: intermediateThrowEventStrategy
		// emits ThrowSignal and then auto-advances, so `start → throw → end` emits
		// ThrowSignal alongside CompleteInstance in ONE command list; and
		// InstanceState.endInstance's cancelOpenTasks emits one UpdateTask per open
		// human task alongside the terminal command. A crash in the commit→perform
		// window on either leaves a durable mark on a terminal instance — a lost
		// signal, or a task projection stuck `unclaimed` on a cancelled instance.
		// Both were reproduced in review; an earlier version of this file scanned
		// only Running and Compensating and asserted, wrongly, that doing so "loses
		// nothing".
		//
		// ⚠ COST, and it is the real one: this walks every instance the store holds,
		// not just the live working set, so it scales with retention rather than
		// with concurrency. The store's own pruner is what bounds the terminal tail.
		// The bounded fix is a marked-instances predicate on kernel.InstanceFilter
		// so the scan becomes an indexed lookup; deliberately not built here.
		page, err := lister.List(ctx, kernel.InstanceFilter{
			Limit:  driver.recoverySweepBatchSize,
			Cursor: cursor,
		})
		if err != nil {
			return recovered, fmt.Errorf("workflow-runtime: recovery sweep: list: %w", err)
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
			return recovered, nil
		}
		cursor = page.NextCursor
	}
}

// recoverInstance re-drives one instance's unperformed commands, returning 1
// when it did and 0 when the instance needed nothing.
//
// Three properties make it safe to run repeatedly, and each answers a defect
// reproduced in review:
//
//  1. **Per-command progress.** Each command is removed from the durable mark as
//     it succeeds, so a pass that fails on the fourth command does not re-run the
//     first three next time. Without it, a permanently unperformable mark
//     re-executed its already-succeeded siblings on every tick, forever — measured
//     at eleven charges of a payment action for one committed step.
//  2. **A re-stamped mark on failure.** The remainder is written back with
//     `at = now`, so the grace window measures from the last ATTEMPT rather than
//     from the original commit. Without it, a mark once older than the window
//     was retried on every tick for the life of the deployment.
//  3. **An already-performed gate.** A command whose effect is durably visible is
//     skipped rather than repeated. This is what stops a re-driven AwaitHuman
//     from replacing a CLAIMED task row with a fresh Unclaimed one and erasing
//     the claimant, and stops a re-driven StartSubInstance from reporting a live
//     child as a failure.
//
// Ordering is deliberate: the follow-up triggers are applied AFTER the mark is
// written. A follow-up commits a new snapshot carrying its own mark, at a new
// version, so writing first is the only ordering under which progress survives.
//
// ⚠ RESIDUAL, stated because it is real and bounded rather than hidden: applying
// a follow-up re-marks the snapshot for the step it commits, which supersedes any
// remainder this pass could not perform. So a step that both produced a follow-up
// trigger AND left a command unperformable loses that command's mark. It is
// strictly better than the alternative — dropping the follow-up strands a live
// token forever — and it is a property of the mark living in the snapshot that
// deliverLoop also owns.
func (driver *ProcessDriver) recoverInstance(ctx context.Context, instanceID string, cutoff time.Time) (int, error) {
	st, token, err := driver.store.Load(ctx, instanceID)
	if err != nil {
		return 0, fmt.Errorf("load: %w", err)
	}
	if len(st.PendingCommands) == 0 {
		return 0, nil
	}
	if st.PendingCommandsAt.After(cutoff) {
		// Inside the grace window: assume a live perform still owns these commands.
		return 0, nil
	}

	// Consult the single-writer guarantee, when the driver has one. The repo
	// already ships this port precisely so a multi-replica deployment can name one
	// writer per instance (kernel.InstanceOwnership, persistence.NewAdvisoryLockOwnership),
	// and a sweep that ignored it would hand a deployment that bought that
	// guarantee N concurrent re-drives on a rolling restart. Acquire is contractually
	// sticky and O(1) for an instance this process already owns, and it is called
	// only for instances that actually carry an expired mark — never for the whole
	// scan.
	if driver.ownership != nil {
		owned, oerr := driver.ownership.Acquire(ctx, instanceID)
		if oerr != nil {
			return 0, fmt.Errorf("ownership: %w", oerr)
		}
		if !owned {
			return 0, nil // another replica is the writer for this instance
		}
	}

	def, err := driver.defsReg.Lookup(ctx, model.Version(st.DefID, st.DefVersion))
	if err != nil {
		// Leave the mark standing. An unresolvable definition is a registration
		// problem, and the instance becomes recoverable the moment it is fixed —
		// the same call the CallNotifier makes for an unresolvable parent def.
		return 0, fmt.Errorf("lookup %s: %w", model.Version(st.DefID, st.DefVersion), err)
	}

	var (
		followups []engine.Trigger
		failure   error
		performed int
		i         int
	)
	for ; i < len(st.PendingCommands); i++ {
		pending := st.PendingCommands[i]
		cmd, cerr := pending.Command()
		if cerr != nil {
			// A mark this build cannot read (a kind written by a newer one). Stop
			// here and leave it in the remainder rather than re-driving it as
			// something else.
			failure = cerr
			break
		}
		done, derr := driver.alreadyPerformed(ctx, st, pending)
		if derr != nil {
			failure = derr
			break
		}
		if done {
			continue
		}
		next, perr := driver.perform(ctx, def, st, cmd)
		if perr != nil {
			failure = fmt.Errorf("perform %s: %w", pending.Kind, perr)
			break
		}
		performed++
		if next != nil {
			followups = append(followups, next)
		}
	}

	// Write progress back BEFORE any follow-up apply, while the token is still
	// current. An empty remainder is the clear.
	remainder := append([]engine.PendingCommand(nil), st.PendingCommands[i:]...)
	stamp := time.Time{}
	if len(remainder) > 0 {
		stamp = driver.clk.Now().UTC()
	}
	driver.writePendingCommands(ctx, instanceID, token, remainder, stamp)

	if performed > 0 {
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelInfo, "runtime: recovery sweep: re-drove a committed step's unperformed commands",
			append(driver.obs.tel.LogAttrs(ctx),
				slog.String("instance_id", instanceID),
				slog.Int("performed", performed),
				slog.Int("still_pending", len(remainder)))...)
	}

	for _, trg := range followups {
		if _, aerr := driver.applyTriggerRetryingCAS(ctx, def, instanceID, trg); aerr != nil {
			if errors.Is(aerr, engine.ErrTokenNotFound) {
				// Another writer already resumed this token. The command was
				// duplicated, the engine state was not: a benign at-least-once
				// outcome, exactly as the call notifier treats it.
				continue
			}
			if failure == nil {
				failure = fmt.Errorf("apply %T: %w", trg, aerr)
			}
		}
	}
	if failure != nil {
		return 0, failure
	}
	if performed == 0 {
		return 0, nil
	}
	return 1, nil
}

// alreadyPerformed reports whether pending's effect is already durably visible,
// so the sweep can skip it instead of repeating it.
//
// It exists because "re-drive" and "re-execute" are not the same thing. Recovery
// is at-least-once at the level of the SWEEP, but three of the five recoverable
// commands leave durable evidence of having run, and repeating those is not a
// harmless duplicate:
//
//   - AwaitHuman writes the task row with State: Unclaimed and no Claim, and
//     TaskStore.Upsert REPLACES the row — so re-performing one after a claim
//     erases the claimant from every inbox and from what TaskService loads before
//     authorizing. Reproduced in review.
//   - StartSubInstance derives a deterministic child id, so a second start hits
//     ErrInstanceExists; without this gate that surfaced as a fabricated
//     SubInstanceFailed against a live child.
//   - InvokeAction parks a token on its CommandID. No token awaiting it means the
//     reply already came back, so re-invoking would repeat an external side effect
//     for work that is complete.
//
// The two it cannot gate — UpdateTask and ThrowSignal — leave no durable evidence
// of delivery, so they are re-performed and are covered by the at-least-once
// contract on [ProcessDriver.RecoverPendingCommands].
//
// An error means "cannot tell", and is returned rather than swallowed: guessing
// "not performed" is exactly the guess that erases a claim.
func (driver *ProcessDriver) alreadyPerformed(ctx context.Context, st engine.InstanceState, pending engine.PendingCommand) (bool, error) {
	switch pending.Kind {
	case engine.PendingAwaitHuman:
		if driver.tasks == nil {
			return false, nil // perform will report the missing TaskStore
		}
		_, err := driver.tasks.Get(ctx, pending.TaskID)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, humantask.ErrTaskNotFound):
			return false, nil
		default:
			return false, fmt.Errorf("task %q lookup: %w", pending.TaskID, err)
		}

	case engine.PendingStartSubInstance:
		childID := childInstanceIDFor(st.InstanceID, pending.CommandID)
		_, _, err := driver.store.Load(ctx, childID)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, kernel.ErrInstanceNotFound):
			return false, nil
		default:
			return false, fmt.Errorf("child %q lookup: %w", childID, err)
		}

	case engine.PendingInvokeAction:
		if pending.FireAndForget {
			// No token ever awaits one, so token state cannot answer the question.
			return false, nil
		}
		for _, tok := range st.Tokens {
			if tok.AwaitCommand == pending.CommandID {
				return false, nil
			}
		}
		return true, nil

	default:
		return false, nil
	}
}
