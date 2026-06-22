package runtime

import (
	"context"
	"errors"
)

// ErrNoCallLink is returned by CallLinkStore.LookupChild when the instance has no
// parent (it is a root instance, not a child of any call activity).
var ErrNoCallLink = errors.New("workflow-runtime: no call link for instance")

// CallLink is the durable parent↔child correlation for one async call activity
// (ADR-0024). It is recorded atomically with the child's Create (ADR-0025).
type CallLink struct {
	ChildInstanceID  string
	ParentInstanceID string
	ParentCommandID  string // the parent token's AwaitCommand (StartSubInstance.CommandID)
	ParentDefID      string
	ParentDefVersion int
	Depth            int // call-chain depth (runaway/cycle guard)
}

// CallOutcome is a child's terminal result, recorded for the parent notification.
type CallOutcome struct {
	Completed bool           // true => SubInstanceCompleted; false => SubInstanceFailed
	Output    map[string]any // child terminal variables (when Completed)
	Err       string         // child error (when !Completed)
}

// PendingNotify is a claimed terminal link awaiting parent delivery.
type PendingNotify struct {
	Link    CallLink
	Outcome CallOutcome
}

// CallLinkStore persists parent↔child call-activity correlation and the durable
// parent-notification queue. The write side is fused into the transactional Store
// (AppliedStep.NewCallLink / CallOutcome, ADR-0025); this port is the read/claim
// side the CallNotifier uses.
type CallLinkStore interface {
	// ClaimPending returns up to limit terminal-but-unnotified links.
	ClaimPending(ctx context.Context, limit int) ([]PendingNotify, error)
	// MarkNotified records that the parent for childInstanceID has been resumed.
	MarkNotified(ctx context.Context, childInstanceID string) error
	// LookupChild returns the link for a child instance; ok=false (ErrNoCallLink)
	// when the instance is a root (no parent).
	LookupChild(ctx context.Context, childInstanceID string) (CallLink, bool, error)
	// ListRunningChildren returns all non-terminal child links whose
	// ParentInstanceID equals parentInstanceID, ordered by ChildInstanceID.
	ListRunningChildren(ctx context.Context, parentInstanceID string) ([]CallLink, error)
}
