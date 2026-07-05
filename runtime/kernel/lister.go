package kernel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ErrBadCursor is returned by DecodeCursor when the cursor is malformed.
var ErrBadCursor = errors.New("workflow-runtime: malformed instance cursor")

// cursorPayload is the JSON envelope embedded inside the opaque cursor string.
type cursorPayload struct {
	StartedAt  time.Time `json:"started_at"`
	InstanceID string    `json:"instance_id"`
}

// EncodeCursor produces an opaque keyset cursor for keyset pagination.
// The cursor encodes the last-seen (startedAt, instanceID) pair so the next
// page can continue from where this one left off.
func EncodeCursor(startedAt time.Time, instanceID string) string {
	b, _ := json.Marshal(cursorPayload{StartedAt: startedAt, InstanceID: instanceID})
	return base64.URLEncoding.EncodeToString(b)
}

// DecodeCursor parses an opaque cursor produced by EncodeCursor.
// Returns [ErrBadCursor] when the cursor is not valid base64 or is not a valid
// JSON payload.
func DecodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %w", ErrBadCursor, err)
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %w", ErrBadCursor, err)
	}
	return p.StartedAt, p.InstanceID, nil
}

// NormalizeLimit clamps a requested limit to [1, 200] with a default of 50.
// Limit ≤ 0 returns the default (50); Limit > 200 is clamped to 200.
func NormalizeLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 200:
		return 200
	default:
		return n
	}
}

// InstanceFilter controls which process instances are returned by InstanceLister.List.
//
// Status, when non-nil, restricts results to instances with that status.
// Limit is the maximum number of items to return (default 50, max 200).
// Cursor is the opaque pagination token returned in the previous InstancePage;
// empty means start from the beginning.
// IncludeTotal, when true, requests that InstancePage.TotalCount be populated
// with the total number of instances matching the status filter (ignoring
// Limit and Cursor). When false (the default), no count query is issued and
// TotalCount is 0.
type InstanceFilter struct {
	// Status restricts results to instances with this lifecycle state.
	// nil means all statuses.
	Status *engine.Status
	// Limit is the page size. ≤0 defaults to 50; >200 is clamped to 200.
	Limit int
	// Cursor is the opaque keyset pagination token from the previous page.
	// Empty string means start from the first page.
	Cursor string
	// IncludeTotal, when true, requests a total count of all matching instances
	// independent of Limit and Cursor. Set only when explicitly requested to
	// avoid the extra query on every list call.
	IncludeTotal bool
}

// InstanceSummary is a lightweight projection of engine.InstanceState for
// admin listing and monitoring. It intentionally omits large fields (tokens,
// history, tasks) to keep the admin-list payload small.
type InstanceSummary struct {
	// InstanceID is the unique process instance identifier.
	InstanceID string
	// DefID is the process-definition ID.
	DefID string
	// DefVersion is the process-definition version.
	DefVersion int
	// Status is the current lifecycle state of the instance.
	Status engine.Status
	// StartedAt is the time the instance was created.
	StartedAt time.Time
	// EndedAt is the time the instance reached a terminal state, or nil if
	// the instance is still running.
	EndedAt *time.Time
	// IncidentCount is the number of open incidents on this instance. An
	// incident is created when a retryable action exhausts its retry budget
	// (or encounters a non-retryable error). A non-zero value indicates the
	// instance is parked and requires operator intervention via ResolveIncident.
	IncidentCount int
}

// InstancePage is one page of results from InstanceLister.List.
type InstancePage struct {
	// Items holds the summaries for this page, ordered by (StartedAt DESC, InstanceID DESC).
	Items []InstanceSummary
	// NextCursor is the opaque cursor to pass as InstanceFilter.Cursor on the next
	// call to retrieve the following page. Empty when HasMore is false.
	NextCursor string
	// HasMore is true when there are additional items beyond this page.
	HasMore bool
	// TotalCount is the total number of instances matching the filter's Status
	// (ignoring Limit and Cursor). Set only when the filter requested IncludeTotal;
	// 0 otherwise.
	TotalCount int
}

// InstanceLister is the read-side port for enumerating process instances.
// Implementations must return items ordered by (StartedAt DESC, InstanceID DESC),
// where InstanceID uses lexicographic (string) comparison. This ordering is consistent
// between MemInstanceStore and Postgres (varchar), so callers should use sortable instance IDs
// (e.g. UUIDs/ULIDs) for intuitive ordering.
type InstanceLister interface {
	// List returns a page of process-instance summaries matching filter.
	List(ctx context.Context, filter InstanceFilter) (InstancePage, error)
}
