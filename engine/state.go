package engine

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/humantask"
)

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

	// Deterministic ID counters (never randomness or the clock).
	CmdSeq   int
	TokenSeq int
	TaskSeq  int
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

// Clone returns a deep copy of the InstanceState. All slice and map fields are
// independently allocated so that mutations to the returned state do not affect
// the receiver (and vice versa).
func (s InstanceState) Clone() InstanceState {
	return cloneState(s)
}
