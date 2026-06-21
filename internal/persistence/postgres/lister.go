package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time check: Lister satisfies runtime.InstanceLister.
var _ runtime.InstanceLister = (*Lister)(nil)

// Lister is the Postgres-backed runtime.InstanceLister. It executes a keyset
// cursor query over wrkflw_instances, projecting only the columns needed for
// the InstanceSummary admin list shape.
type Lister struct {
	pool *pgxpool.Pool
}

// NewLister constructs a Lister over the given pool.
// Migrate must be applied before calling List.
func NewLister(pool *pgxpool.Pool) *Lister { return &Lister{pool: pool} }

// List returns a keyset-cursor-paginated page of instance summaries.
//
// Items are ordered by (started_at DESC, instance_id DESC). When filter.Status
// is non-nil, only instances with that status are included. filter.Cursor is
// the opaque token produced by runtime.EncodeCursor; an empty cursor means
// "start from the beginning". Limit is clamped via runtime.NormalizeLimit.
//
// The keyset predicate for the cursor is:
//
//	(started_at, instance_id) < ($cursorTime, $cursorID)
//
// which Postgres evaluates as a row-value comparison, matching the MemStore's
// skip logic exactly so both implementations paginate identically.
func (l *Lister) List(ctx context.Context, filter runtime.InstanceFilter) (runtime.InstancePage, error) {
	limit := runtime.NormalizeLimit(filter.Limit)
	fetch := limit + 1 // fetch one extra to detect HasMore

	// Decode cursor (optional).
	var (
		hasCursor  bool
		cursorTime time.Time
		cursorID   string
	)
	if filter.Cursor != "" {
		var err error
		cursorTime, cursorID, err = runtime.DecodeCursor(filter.Cursor)
		if err != nil {
			return runtime.InstancePage{}, fmt.Errorf("postgres lister: decode cursor: %w", err)
		}
		hasCursor = true
	}

	// Build the query. We conditionally include the cursor predicate to avoid
	// Postgres needing to handle NULL timestamptz in a row-value comparison,
	// which can confuse the planner on some versions.
	var (
		rows interface {
			Next() bool
			Scan(...any) error
			Err() error
			Close()
		}
		queryErr error
	)

	if filter.Status != nil && hasCursor {
		rows, queryErr = l.pool.Query(ctx, `
			SELECT instance_id, def_id, def_version, status, started_at, ended_at
			FROM   wrkflw_instances
			WHERE  status = $1
			  AND  (started_at, instance_id) < ($2, $3)
			ORDER  BY started_at DESC, instance_id DESC
			LIMIT  $4`,
			int16(*filter.Status), cursorTime, cursorID, fetch)
	} else if filter.Status != nil {
		rows, queryErr = l.pool.Query(ctx, `
			SELECT instance_id, def_id, def_version, status, started_at, ended_at
			FROM   wrkflw_instances
			WHERE  status = $1
			ORDER  BY started_at DESC, instance_id DESC
			LIMIT  $2`,
			int16(*filter.Status), fetch)
	} else if hasCursor {
		rows, queryErr = l.pool.Query(ctx, `
			SELECT instance_id, def_id, def_version, status, started_at, ended_at
			FROM   wrkflw_instances
			WHERE  (started_at, instance_id) < ($1, $2)
			ORDER  BY started_at DESC, instance_id DESC
			LIMIT  $3`,
			cursorTime, cursorID, fetch)
	} else {
		rows, queryErr = l.pool.Query(ctx, `
			SELECT instance_id, def_id, def_version, status, started_at, ended_at
			FROM   wrkflw_instances
			ORDER  BY started_at DESC, instance_id DESC
			LIMIT  $1`,
			fetch)
	}

	if queryErr != nil {
		return runtime.InstancePage{}, fmt.Errorf("postgres lister: query: %w", queryErr)
	}
	defer rows.Close()

	items := make([]runtime.InstanceSummary, 0, fetch)
	for rows.Next() {
		var (
			instanceID string
			defID      string
			defVersion int
			status     int16
			startedAt  time.Time
			endedAt    *time.Time
		)
		if err := rows.Scan(&instanceID, &defID, &defVersion, &status, &startedAt, &endedAt); err != nil {
			return runtime.InstancePage{}, fmt.Errorf("postgres lister: scan: %w", err)
		}
		items = append(items, runtime.InstanceSummary{
			InstanceID: instanceID,
			DefID:      defID,
			DefVersion: defVersion,
			Status:     engine.Status(status),
			StartedAt:  startedAt,
			EndedAt:    endedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return runtime.InstancePage{}, fmt.Errorf("postgres lister: rows: %w", err)
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	var nextCursor string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = runtime.EncodeCursor(last.StartedAt, last.InstanceID)
	}

	return runtime.InstancePage{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}
