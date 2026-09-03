package engine

import (
	"context"
	"log/slog"
	"slices"

	"github.com/kartaladev/wrkflw/humantask"
)

// filterableCommand reports whether c is a kind dropStaleTokenCommands could
// ever drop: one of the three awaiter-parking kinds, carrying a non-empty
// correlation id. It is the fast-path predicate, so it must stay in step with
// the switch in dropStaleTokenCommands — a kind that is filterable there but not
// here would never be reached.
func filterableCommand(c Command) bool {
	switch cmd := c.(type) {
	case InvokeAction:
		return !cmd.FireAndForget && cmd.CommandID != ""
	case StartSubInstance:
		return cmd.CommandID != ""
	case AwaitHuman:
		return cmd.TaskID != ""
	}
	return false
}

// liveAwaiters returns the ids that something in s is still waiting on: every
// surviving token's non-empty AwaitCommand, plus Compensating.ActiveCmdID while
// the instance is not terminal.
//
// Both sources are load-bearing, and the second is not obvious.
// startCompensationWalk (step_nodes.go) calls consumeToken on the throw token
// BEFORE it stamps cursor.ActiveCmdID and appends the walk's non-FireAndForget
// InvokeAction (compensationInvoke), which is correlated by that cursor id and
// by nothing else — so a token-only set would drop that command and hang every
// compensation walk. Cited by symbol on purpose: the ":982-993" this sentence
// used to carry was two moves stale (982 → 1035 → 1079) when it was replaced.
// Every beginCompensation call site sets
// Status = StatusCompensating immediately before calling it, which is what makes
// the non-terminal condition safe for the in-flight case.
//
// The terminal exclusion is the mirror image: a step that starts a walk and then
// force-terminates would otherwise keep a compensation action alive for a
// terminated instance, whose ActionCompleted would land on a
// non-StatusCompensating state and fail. endInstance also zeroes the cursor at
// every terminal site, so this exclusion is now defence in depth rather than the
// only defence — it still earns its place, because the token half below has no
// such backstop.
//
// Empty values are skipped: an empty key names nothing, and admitting it would
// turn every malformed command id into a live awaiter.
func liveAwaiters(s *InstanceState) map[string]struct{} {
	out := make(map[string]struct{}, len(s.Tokens)+1)
	// A terminal instance can never consume a resumption trigger, so nothing it
	// still holds is a live awaiter. The exclusion CANNOT be scoped to the cursor
	// alone: handleUnhandledError's immediate-fail branch
	// sets StatusFailed and reconciles tasks and timers but never clears
	// s.Tokens, so every sibling survives with its AwaitCommand intact and a
	// token-only set would re-admit it — leaving the filter a no-op on exactly the
	// path it exists for.
	if s.Status.IsTerminal() {
		return out
	}
	for i := range s.Tokens {
		if id := s.Tokens[i].AwaitCommand; id != "" {
			out[id] = struct{}{}
		}
	}
	if id := s.Compensating.ActiveCmdID; id != "" {
		out[id] = struct{}{}
	}
	return out
}

// dropStaleTokenCommands returns cmds without the commands whose awaiter s no
// longer holds. For a dropped AwaitHuman whose record is still OPEN it also
// cancels that record and emits the UpdateTask that keeps the task store in
// step — the same open-only rule cancelOpenTasks applies. A
// ghost id, or a record already Completed or Cancelled, is dropped without any
// update; that guard is what keeps a terminal path from emitting a second
// UpdateTask for a record cancelOpenTasks already reconciled.
//
// drive accumulates every token's commands in one pass, so a later token can
// destroy an earlier one's park while its command is already in the slice:
// forceTerminate nils s.Tokens, an interrupting boundary consumes its host, an
// interrupting event sub-process cancels its scope, propagateError tears down
// the erroring scope, and beginCompensation cancels everything. Without this
// filter the runtime performs those commands post-commit — the action really
// runs and its ActionCompleted resolves to ErrTokenNotFound, an AwaitHuman mints
// an inbox task nothing can complete, a StartSubInstance starts an orphan child.
//
// It mutates s, so callers pass &res.State rather than the working clone: every
// handler builds its own StepResult{State: *s} literal, which shallow-copies the
// struct, and mutating the clone would work only by backing-array sharing.
//
// A command whose correlation id is EMPTY is always kept. IDGenerator is
// consumer-supplied and ("", nil) is a legal return, so an empty id means a
// malformed command, not a stale one — today that fails loudly via
// ErrTokenNotFound, and dropping it would replace the error with a permanently
// parked instance. Only three kinds are filtered; FireAndForget InvokeActions
// and every ScheduleTimer are deliberately exempt.
//
// ctx is used ONLY for trace-correlated logging: every drop emits one Warn
// record, because a suppressed command leaves no other trace anywhere — no
// error, no event, no history entry — and an operator asking "why did this
// refund never run?" would otherwise have nothing to work from. This is the site
// class Step's doc comment reserves ctx for.
func dropStaleTokenCommands(ctx context.Context, s *InstanceState, cmds []Command) []Command {
	// Pre-scan. Step runs this on every trigger, and a delivery carrying no
	// filterable kind at all (ScheduleTimer, CancelTimer, UpdateTask,
	// SendMessage... — a recurring-timer reschedule, a terminal task sweep) skips
	// both the awaiter map and the slice copy.
	//
	// It is NOT the typical step: a ServiceTask entry, a retry re-invoke, a
	// UserTask entry, a CallActivity entry and a completion-action park each emit
	// exactly one filterable command, so a normal non-trivial trigger misses this
	// scan and pays the full path to keep everything. That cost is the point of
	// the filter and is measured by BenchmarkDropStaleTokenCommandsLiveAwaiter.
	if !slices.ContainsFunc(cmds, filterableCommand) {
		return cmds
	}
	live := liveAwaiters(s)
	stale := func(id string) bool {
		if id == "" {
			return false
		}
		_, ok := live[id]
		return !ok
	}

	logDrop := func(kind, correlationID string) {
		slog.WarnContext(ctx, "dropping command whose awaiter this step cancelled",
			"instance_id", s.InstanceID,
			"command_kind", kind,
			"correlation_id", correlationID,
		)
	}

	kept := make([]Command, 0, len(cmds))
	for _, c := range cmds {
		switch cmd := c.(type) {
		case InvokeAction:
			if !cmd.FireAndForget && stale(cmd.CommandID) {
				logDrop("InvokeAction", cmd.CommandID)
				continue
			}
		case StartSubInstance:
			if stale(cmd.CommandID) {
				logDrop("StartSubInstance", cmd.CommandID)
				continue
			}
		case AwaitHuman:
			if stale(cmd.TaskID) {
				logDrop("AwaitHuman", cmd.TaskID)
				// The record was minted in this same step and the runtime was
				// never told about it. Cancel it so the ActiveTasks projection
				// (which filters on IsOpen) stops advertising a task
				// nothing can complete, and keep the record itself so the
				// NodeVisit.TaskID audit link stays resolvable.
				//
				// Clone before the record escapes: the command is handed to a
				// consumer-supplied TaskStore while the record it was built from is
				// committed as instance state, so a shallow copy would share the
				// Claim/Completion pointees, the Vars map and the actor slices
				// across that boundary. HumanTask.Clone is the single deep-copy
				// definition; performAwaitHuman guards the same seam on the entry
				// path (runtime/processdriver_action.go:449-461).
				if task := s.TaskByID(cmd.TaskID); task != nil && task.IsOpen() {
					task.State = humantask.Cancelled
					kept = append(kept, UpdateTask{Task: task.Clone()})
				}
				continue
			}
		}
		kept = append(kept, c)
	}
	return kept
}
