package engine

// timerRecord is the engine's internal bookkeeping entry for a scheduled timer.
// It allows the engine to route a TimerFired back to the correct token and task
// without relying on the token's AwaitCommand (which is set to the TaskID for
// user-task nodes, not the deadline timer ID).
//
// It does NOT cover every scheduled timer. A plain intermediate-catch-event
// timer is arm-borne: no record is written for it, the token parks on the
// TimerID itself, and handleTimerFired routes it through the tokenAwaiting
// fall-through instead of through this table (ADR-0177).
type timerRecord struct {
	// TimerID is the unique timer identifier emitted in ScheduleTimer.
	TimerID string
	// Kind discriminates the record's purpose: [TimerDeadline], [TimerInWait],
	// [TimerRetry], [TimerCompensationStall] or [TimerCompensationRetry] — the
	// five kinds written to this table, re-derived from the package's
	// `s.Timers = append` sites when ADR-0179 added the fifth.
	// [TimerIntermediate] is never one of them: an intermediate timer is
	// arm-borne and gets no record (ADR-0177).
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
	// CommandID is the compensation command this timer guards, set only on a
	// TimerCompensationStall record (ADR-0175). It is what makes a LATE fire
	// safe: the walk may have advanced under the timer, so the fire handler
	// raises an incident only when this still equals Compensating.ActiveCmdID.
	// Empty on every other kind.
	CommandID string
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
	// nil, not an empty slice, when nothing survives — matching cancelAllTimers
	// and cancelCompensationWalkTimers. Assigning the freshly make()d slice
	// unconditionally flips the persisted snapshot from `"timers": null` to
	// `"timers": []`, including on a call that removed nothing at all.
	if len(out) == 0 {
		s.Timers = nil
		return
	}
	s.Timers = out
}

// cancelTimersWhere removes every timer record whose keyOf value equals key
// (except the timer named by excludeTimerID) and returns their TimerIDs. An
// empty key names no record and cancels nothing (ADR-0152); an empty
// excludeTimerID excludes nothing.
func (s *InstanceState) cancelTimersWhere(key, excludeTimerID string, keyOf func(timerRecord) string) []string {
	if key == "" {
		return nil
	}
	var toCancel []string
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if keyOf(tr) == key && tr.TimerID != excludeTimerID {
			toCancel = append(toCancel, tr.TimerID)
			continue
		}
		out = append(out, tr)
	}
	// nil, not an empty slice, when nothing survives — see removeTimer.
	if len(out) == 0 {
		s.Timers = nil
		return toCancel
	}
	s.Timers = out
	return toCancel
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
	return s.cancelTimersWhere(taskID, excludeTimerID, func(tr timerRecord) string { return tr.TaskID })
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
	return s.cancelTimersWhere(tokenID, excludeTimerID, func(tr timerRecord) string { return tr.Token })
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

// HasArmedTimers reports whether the instance holds any timer a consumer's test
// harness may legitimately fire. It is the FILTERED view of
// [InstanceState.TimerWaiters], which enumerates every source unconditionally.
//
// It excludes detection-only kinds — today [TimerCompensationStall] alone
// (ADR-0175); see [TimerKind.detectionOnly]. Such a record is a detection
// deadline, not work the instance is waiting to do: firing it manufactures the
// very incident the window exists to detect.
//
// ⚠ The filter is NOT "belongs to a compensation walk". A
// [TimerCompensationRetry] is walk-scoped and is still reported here, because it
// is forward work the harness must fire for the backoff to end (ADR-0179).
//
// ⚠ It is deliberately NOT a len(s.Timers) test, and a consumer must not
// re-derive it as one. Until ADR-0177 it was, and four of the five timer-arm
// sources — boundary arms, event-gateway arms, event-sub-process arms and plain
// timer intermediate catch events — never reach s.Timers, so an instance
// waiting on exactly one of them measured false.
func (s *InstanceState) HasArmedTimers() bool {
	for _, w := range s.TimerWaiters() {
		if !w.Kind.detectionOnly() {
			return true
		}
	}
	return false
}
