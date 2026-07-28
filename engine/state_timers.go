package engine

// timerRecord is the engine's internal bookkeeping entry for a scheduled timer.
// It allows the engine to route a TimerFired back to the correct token and task
// without relying on the token's AwaitCommand (which is set to the TaskID for
// user-task nodes, not the deadline timer ID).
//
// For intermediate-catch-event timers the TaskID field is empty because the
// token parks on the TimerID itself and the tokenAwaiting lookup still works.
// Recording them here provides a single, unified dispatch table.
type timerRecord struct {
	// TimerID is the unique timer identifier emitted in ScheduleTimer.
	TimerID string
	// Kind discriminates intermediate, deadline, and in-wait timers.
	Kind TimerKind
	// Token is the ID of the parked engine token this timer guards.
	Token string
	// TaskID is the human-task correlation token ("" for intermediate timers).
	TaskID string
	// NodeID is the BPMN node that owns the timer (needed to resolve DeadlineFlow/DeadlineAction).
	NodeID string
	// ScopeID is the execution scope of the token that owns this timer. Empty
	// string means the root scope. Used to resolve the correct nested definition
	// when a deadline or reminder timer fires inside a sub-process.
	ScopeID string
}

// timerByID returns a pointer to the timerRecord with the given timerID, or nil
// if no such record exists. An empty timerID names no record (ADR-0152).
func (s *InstanceState) timerByID(timerID string) *timerRecord {
	if timerID == "" {
		return nil
	}
	for i := range s.Timers {
		if s.Timers[i].TimerID == timerID {
			return &s.Timers[i]
		}
	}
	return nil
}

// removeTimer removes the timerRecord with the given timerID from the Timers
// slice. It is a no-op if no record with that timerID exists, and an empty
// timerID names no record (ADR-0152).
func (s *InstanceState) removeTimer(timerID string) {
	if timerID == "" {
		return
	}
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.TimerID != timerID {
			out = append(out, tr)
		}
	}
	s.Timers = out
}

// cancelTimersByTaskID removes all timer records associated with the given
// taskID (excluding the one already being handled), returning their TimerIDs
// so the caller can emit CancelTimer commands.
//
// An empty taskID cancels NOTHING (ADR-0152). A task id is an identity; the empty
// string names no task. TimerRetry records carry no TaskID, so without this guard
// an empty key matched every retry timer in the instance — including retries owned
// by tokens in sibling scopes that were not being cancelled, leaving those tokens
// parked in TokenWaiting forever with their timer cancelled in the scheduler.
//
// excludeTimerID is deliberately NOT guarded: an empty value means "exclude
// nothing", and five of the seven call sites rely on that.
func (s *InstanceState) cancelTimersByTaskID(taskID, excludeTimerID string) []string {
	if taskID == "" {
		return nil
	}
	var toCancel []string
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.TaskID == taskID && tr.TimerID != excludeTimerID {
			toCancel = append(toCancel, tr.TimerID)
			continue
		}
		out = append(out, tr)
	}
	s.Timers = out
	return toCancel
}

// cancelTimersForToken removes all timer records whose Token matches the given
// parked-token id (excluding excludeTimerID), returning their TimerIDs so the
// caller can emit CancelTimer commands. It is the token-keyed counterpart of
// cancelTimersByTaskID, used to cancel a parked token's in-wait reminder when
// its wait resolves or its scope is interrupted (ReceiveTask / IntermediateCatchEvent
// have no human-task correlation token).
//
// An empty tokenID names no token (ADR-0152). excludeTimerID is NOT guarded.
func (s *InstanceState) cancelTimersForToken(tokenID, excludeTimerID string) []string {
	if tokenID == "" {
		return nil
	}
	var toCancel []string
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.Token == tokenID && tr.TimerID != excludeTimerID {
			toCancel = append(toCancel, tr.TimerID)
			continue
		}
		out = append(out, tr)
	}
	s.Timers = out
	return toCancel
}

// cancelAllTimers returns a CancelTimer command for every outstanding timer
// record in s.Timers (in deterministic slice order) and empties s.Timers.
// Call this on any terminal-failure path to avoid orphaned timers in the
// scheduler.
//
// NOTE: A comprehensive sweep across ALL terminal transitions (not just
// ActionFailed) is deferred to the errors/compensation plan (Plan 8).
func (s *InstanceState) cancelAllTimers() []Command {
	if len(s.Timers) == 0 {
		return nil
	}
	cmds := make([]Command, 0, len(s.Timers))
	for _, tr := range s.Timers {
		cmds = append(cmds, CancelTimer{TimerID: tr.TimerID})
	}
	s.Timers = nil
	return cmds
}
