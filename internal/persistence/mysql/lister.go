package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time check: Lister satisfies runtime.InstanceLister.
var _ runtime.InstanceLister = (*Lister)(nil)

// Lister is the MySQL-backed runtime.InstanceLister. It executes a keyset
// cursor query over wrkflw_instances, projecting only the columns needed for
// the InstanceSummary admin list shape. Keyset order is (started_at DESC,
// instance_id DESC).
type Lister struct {
	db *sql.DB
}

// NewLister constructs a Lister over the given *sql.DB.
// Migrate must be applied before calling List.
func NewLister(db *sql.DB) *Lister { return &Lister{db: db} }

// List returns a keyset-cursor-paginated page of instance summaries.
//
// Items are ordered by (started_at DESC, instance_id DESC). When filter.Status
// is non-nil, only instances with that status are included. filter.Cursor is
// the opaque token produced by runtime.EncodeCursor; an empty cursor means
// "start from the beginning". Limit is clamped via runtime.NormalizeLimit.
//
// Incident count is derived from the snapshot JSON column using MySQL's
// JSON_TYPE/JSON_LENGTH functions. Where the Incidents key is absent or not
// an array the expression yields 0.
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
			return runtime.InstancePage{}, fmt.Errorf("workflow-persistence-mysql: lister: decode cursor: %w", err)
		}
		hasCursor = true
	}

	// incidentExpr is the MySQL JSON expression equivalent to postgres's
	// CASE WHEN jsonb_typeof(snapshot->'Incidents')='array' THEN
	//      jsonb_array_length(snapshot->'Incidents') ELSE 0 END.
	// JSON_TYPE returns NULL when the path is absent, so the CASE guard also
	// covers missing or null Incidents keys.
	const incidentExpr = `CASE WHEN JSON_TYPE(JSON_EXTRACT(snapshot, '$.Incidents')) = 'ARRAY'
	                           THEN JSON_LENGTH(JSON_EXTRACT(snapshot, '$.Incidents'))
	                           ELSE 0 END`

	// MySQL has no row-value comparisons with cross-type nullability guarantees,
	// so we reproduce the keyset predicate explicitly. For a DESC cursor the
	// condition is: started_at < ? OR (started_at = ? AND instance_id < ?).
	const baseCols = `instance_id, def_id, def_version, status, started_at, ended_at, ` + incidentExpr

	var (
		rows     *sql.Rows
		queryErr error
	)

	switch {
	case filter.Status != nil && hasCursor:
		rows, queryErr = l.db.QueryContext(ctx,
			`SELECT `+baseCols+`
			   FROM wrkflw_instances
			  WHERE status = ?
			    AND (started_at < ? OR (started_at = ? AND instance_id < ?))
			  ORDER BY started_at DESC, instance_id DESC
			  LIMIT `+fmt.Sprintf("%d", fetch),
			int16(*filter.Status), cursorTime, cursorTime, cursorID,
		)
	case filter.Status != nil:
		rows, queryErr = l.db.QueryContext(ctx,
			`SELECT `+baseCols+`
			   FROM wrkflw_instances
			  WHERE status = ?
			  ORDER BY started_at DESC, instance_id DESC
			  LIMIT `+fmt.Sprintf("%d", fetch),
			int16(*filter.Status),
		)
	case hasCursor:
		rows, queryErr = l.db.QueryContext(ctx,
			`SELECT `+baseCols+`
			   FROM wrkflw_instances
			  WHERE started_at < ? OR (started_at = ? AND instance_id < ?)
			  ORDER BY started_at DESC, instance_id DESC
			  LIMIT `+fmt.Sprintf("%d", fetch),
			cursorTime, cursorTime, cursorID,
		)
	default:
		rows, queryErr = l.db.QueryContext(ctx,
			`SELECT `+baseCols+`
			   FROM wrkflw_instances
			  ORDER BY started_at DESC, instance_id DESC
			  LIMIT `+fmt.Sprintf("%d", fetch),
		)
	}

	if queryErr != nil {
		return runtime.InstancePage{}, fmt.Errorf("workflow-persistence-mysql: lister: query: %w", queryErr)
	}
	defer func() { _ = rows.Close() }()

	items := make([]runtime.InstanceSummary, 0, fetch)
	for rows.Next() {
		var (
			instanceID    string
			defID         string
			defVersion    int
			status        int16
			startedAt     time.Time
			endedAt       *time.Time
			incidentCount int
		)
		if err := rows.Scan(&instanceID, &defID, &defVersion, &status, &startedAt, &endedAt, &incidentCount); err != nil {
			return runtime.InstancePage{}, fmt.Errorf("workflow-persistence-mysql: lister: scan: %w", err)
		}
		startedAt = startedAt.UTC() // normalize DATETIME(6) to UTC-located (guard against non-UTC loc)
		if endedAt != nil {
			t := endedAt.UTC() // normalize DATETIME(6) to UTC-located (guard against non-UTC loc)
			endedAt = &t
		}
		items = append(items, runtime.InstanceSummary{
			InstanceID:    instanceID,
			DefID:         defID,
			DefVersion:    defVersion,
			Status:        engine.Status(status),
			StartedAt:     startedAt,
			EndedAt:       endedAt,
			IncidentCount: incidentCount,
		})
	}
	if err := rows.Err(); err != nil {
		return runtime.InstancePage{}, fmt.Errorf("workflow-persistence-mysql: lister: rows: %w", err)
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

	page := runtime.InstancePage{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}

	if filter.IncludeTotal {
		var totalCount int
		var countErr error
		if filter.Status != nil {
			countErr = l.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM wrkflw_instances WHERE status = ?`,
				int16(*filter.Status),
			).Scan(&totalCount)
		} else {
			countErr = l.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM wrkflw_instances`,
			).Scan(&totalCount)
		}
		if countErr != nil {
			return runtime.InstancePage{}, fmt.Errorf("workflow-persistence-mysql: lister: count: %w", countErr)
		}
		page.TotalCount = totalCount
	}

	return page, nil
}
