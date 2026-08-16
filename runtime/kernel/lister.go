package kernel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/engine"
)

// ErrBadCursor is returned by DecodeCursor when the cursor is malformed.
var ErrBadCursor = errors.New("workflow-runtime: malformed instance cursor")

// cursorPayload is the JSON envelope embedded inside the opaque cursor string.
type cursorPayload struct {
	Kind       string    `json:"kind"`
	StartedAt  time.Time `json:"started_at"`
	InstanceID string    `json:"instance_id"`
}

// instanceCursorKind discriminates this cursor from every other opaque cursor
// the library hands out.
//
// It is defence-in-depth rather than the fix itself, and the distinction is
// worth stating: DisallowUnknownFields alone already rejects an armed-timer
// cursor here, because its "kind", "next_run" and "timer_id" are all unknown to
// [cursorPayload]. What the discriminator adds is symmetry with
// [EncodeArmedTimerCursor] — so a third cursor has one obvious shape to copy —
// and rejection of a future cursor whose field set is a SUBSET of
// {started_at, instance_id}, which unknown-fields structurally cannot catch
// (ADR-0160).
const instanceCursorKind = "instance"

// EncodeCursor produces an opaque keyset cursor for keyset pagination.
// The cursor encodes the last-seen (startedAt, instanceID) pair so the next
// page can continue from where this one left off.
//
// The cursor is opaque and the empty string is the first-page sentinel
// ([InstanceFilter.Cursor]). It returns an error rather than swallowing one
// because the only way to fail is time.Time.MarshalJSON rejecting a year
// outside [0,9999] — and the zero value it would otherwise return is the empty
// string, which IS that sentinel. A caller that ignored the error would answer
// HasMore: true with an empty NextCursor, and a client following the contract
// would re-request page one forever. The sentinel is only structural if nothing
// else can produce it.
func EncodeCursor(startedAt time.Time, instanceID string) (string, error) {
	c, err := encodeCursorPayload(cursorPayload{
		Kind:       instanceCursorKind,
		StartedAt:  startedAt,
		InstanceID: instanceID,
	})
	if err != nil {
		return "", fmt.Errorf("workflow-runtime: encode instance cursor for %q: %w", instanceID, err)
	}
	return c, nil
}

// DecodeCursor parses an opaque cursor produced by EncodeCursor.
// Returns [ErrBadCursor] when the cursor is not valid base64, does not hold a
// valid JSON payload, belongs to another cursor family, or carries no instance
// identity.
func DecodeCursor(cursor string) (time.Time, string, error) {
	var p cursorPayload
	if err := decodeCursorInto(cursor, &p); err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %w", ErrBadCursor, err)
	}
	if p.Kind != instanceCursorKind {
		return time.Time{}, "", fmt.Errorf("%w: not an instance cursor", ErrBadCursor)
	}
	// A cursor is always minted from a real row and ADR-0152 forbids empty
	// identity keys, so an empty id means the payload was fabricated or
	// truncated. Under the DESC ordering this listing uses, the resulting key is
	// the lowest possible one, which matches nothing — a silently empty page
	// rather than a diagnostic.
	if p.InstanceID == "" {
		return time.Time{}, "", fmt.Errorf("%w: cursor carries no instance identity", ErrBadCursor)
	}
	// An absent or zero started_at survives every guard above and decodes to the
	// zero time, which under this listing's DESC ordering is the LOWEST key — so
	// it matches nothing and yields a silently empty page with a 200. That is
	// the exact failure this cursor family exists to prevent.
	//
	// Rejecting it is safe here, and deliberately asymmetric with the
	// armed-timer sibling: a zero next_run is a legitimate armed value there
	// (ADR-0159), whereas StartedAt is minted from Trigger.OccurredAt and a
	// zero-StartedAt row sorts LAST under DESC — so a cursor is never
	// legitimately minted from one.
	if p.StartedAt.IsZero() {
		return time.Time{}, "", fmt.Errorf("%w: cursor carries no start time", ErrBadCursor)
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
	// IncidentCount is the number of open incidents on this instance, across
	// every [engine.IncidentKind] — the count does not discriminate, so a
	// non-zero value says only that something needs an operator's attention,
	// not which verb clears it:
	//
	//   - engine.IncidentAction, created when a retryable action exhausts its
	//     retry budget (or encounters a non-retryable error). It parks a token
	//     and IS cleared through ResolveIncident.
	//   - The walk-scoped kinds — engine.IncidentCompensationStall (ADR-0175)
	//     and engine.IncidentCompensationFailed (ADR-0179). They park no token,
	//     and ResolveIncident REFUSES both with engine.ErrIncidentNotResolvable
	//     (it whitelists engine.IncidentAction); the verbs that act on them are
	//     retry, skip and abandon on the compensation walk.
	//
	// Read the instance's Incidents and switch on Kind to decide what to offer
	// an operator. A count alone cannot be routed.
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
