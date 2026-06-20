package engine

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// timerRecord is the engine's internal bookkeeping entry for a scheduled timer.
// It allows the engine to route a TimerFired back to the correct token and task
// without relying on the token's AwaitCommand (which is set to the TaskToken for
// user-task nodes, not the SLA timer ID).
//
// For intermediate-catch-event timers the TaskToken field is empty because the
// token parks on the TimerID itself and the tokenAwaiting lookup still works.
// Recording them here provides a single, unified dispatch table.
type timerRecord struct {
	// TimerID is the unique timer identifier emitted in ScheduleTimer.
	TimerID string
	// Kind discriminates intermediate, SLA, and in-wait timers.
	Kind TimerKind
	// Token is the ID of the parked engine token this timer guards.
	Token string
	// TaskToken is the human-task correlation token ("" for intermediate timers).
	TaskToken string
	// NodeID is the BPMN node that owns the timer (needed to resolve SLAFlow/SLAAction).
	NodeID string
}

// Status is the lifecycle state of a process instance.
type Status int

const (
	StatusRunning Status = iota
	StatusCompleted
	StatusFailed
	StatusCompensating
	StatusTerminated
)

// TokenState is the execution state of a single token.
type TokenState int

const (
	TokenActive TokenState = iota
	TokenWaitingCommand
	TokenAtJoin
)

// Token marks where execution currently sits and what it is waiting on.
type Token struct {
	ID           string
	NodeID       string
	ScopeID      string
	State        TokenState
	AwaitCommand string // CommandID this token is parked on, if any
	Payload      map[string]any
	EnteredAt    time.Time
}

// NodeVisit is one traversal of one node by one token (audit/history).
type NodeVisit struct {
	NodeID    string
	TokenID   string
	EnteredAt time.Time
	LeftAt    *time.Time
	ActorID   *string // who completed a human-task visit (later plans)
}

// InstanceState is the authoritative snapshot of a running instance.
type InstanceState struct {
	InstanceID string
	DefID      string
	DefVersion int
	Status     Status
	Variables  map[string]any
	Tokens     []Token
	StartedAt  time.Time
	EndedAt    *time.Time
	History    []NodeVisit

	// Tasks holds the in-flight human-task records for this instance.
	Tasks []humantask.HumanTask

	// Timers is the auxiliary bookkeeping table for all scheduled timers.
	// Keyed implicitly by index; looked up by TimerID. A timer record is removed
	// when the timer is consumed (fired and handled) or cancelled so that a
	// late/duplicate TimerFired is a clean no-op.
	// Appended in TimerSeq order; iteration is deterministic by construction.
	Timers []timerRecord

	// Deterministic ID counters (never randomness or the clock).
	CmdSeq   int
	TokenSeq int
	TaskSeq  int
	TimerSeq int
}

// TaskByToken returns a pointer to the HumanTask with the given taskToken, or
// nil if no such task exists in the state.
func (s *InstanceState) TaskByToken(taskToken string) *humantask.HumanTask {
	for i := range s.Tasks {
		if s.Tasks[i].TaskToken == taskToken {
			return &s.Tasks[i]
		}
	}
	return nil
}

// timerByID returns a pointer to the timerRecord with the given timerID, or nil
// if no such record exists.
func (s *InstanceState) timerByID(timerID string) *timerRecord {
	for i := range s.Timers {
		if s.Timers[i].TimerID == timerID {
			return &s.Timers[i]
		}
	}
	return nil
}

// removeTimer removes the timerRecord with the given timerID from the Timers
// slice. It is a no-op if no record with that timerID exists.
func (s *InstanceState) removeTimer(timerID string) {
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.TimerID != timerID {
			out = append(out, tr)
		}
	}
	s.Timers = out
}

// cancelTimersByTaskToken removes all timer records associated with the given
// taskToken (excluding the one already being handled), returning their TimerIDs
// so the caller can emit CancelTimer commands. Used to cancel in-wait/reminder
// timers when an SLA breach or task completion supersedes them.
func (s *InstanceState) cancelTimersByTaskToken(taskToken, excludeTimerID string) []string {
	var toCancel []string
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.TaskToken == taskToken && tr.TimerID != excludeTimerID {
			toCancel = append(toCancel, tr.TimerID)
			continue
		}
		out = append(out, tr)
	}
	s.Timers = out
	return toCancel
}

// Clone returns a deep copy of the InstanceState. All slice and map fields are
// independently allocated so that mutations to the returned state do not affect
// the receiver (and vice versa).
func (s InstanceState) Clone() InstanceState {
	return cloneState(s)
}
