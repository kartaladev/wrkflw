package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Lister is the vendor-neutral, dialect-parametrised runtime.InstanceLister. It
// executes keyset-cursor-paginated queries over wrkflw_instances, projecting only
// the columns needed for the InstanceSummary admin-list shape.
//
// SQL is written once with ? placeholders and run through [dialect.Dialect.Rebind]
// for the backend's native placeholder style. Dialect-specific fragments (incident
// count expression, keyset cursor predicate) come from the [dialect.Dialect] value.
//
// Lister is read-only; it reads through the pool (no transaction). It is safe for
// concurrent use.
type Lister struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
}

// Compile-time check: *Lister satisfies runtime.InstanceLister.
var _ runtime.InstanceLister = (*Lister)(nil)

// NewLister constructs a Lister over conn using dialect d. conn must be either a
// *pgxpool.Pool (Postgres) or a *sql.DB (MySQL, SQLite); any other type will
// cause [database.From] to return an error when the first query is issued.
//
// NewLister mirrors the [New] constructor shape so callers can pair a Lister
// alongside a Store with the same conn and dialect value.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	lister := store.NewLister(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	lister := store.NewLister(db, dialect.NewSQLite())
func NewLister(conn any, d dialect.Dialect) *Lister {
	return &Lister{conn: conn, dialect: d}
}

// querier returns a pool-backed [database.Querier] over l.conn. The Lister is
// read-only so it never participates in an ambient transaction.
func (l *Lister) querier() database.Querier {
	q, _ := database.From(l.conn)
	return q
}

// List returns a keyset-cursor-paginated page of instance summaries.
//
// Items are ordered by (started_at DESC, instance_id DESC). When filter.Status
// is non-nil, only instances with that status are included. filter.Cursor is
// the opaque token produced by [runtime.EncodeCursor]; an empty cursor means
// "start from the beginning". Limit is clamped via [runtime.NormalizeLimit].
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
			return runtime.InstancePage{}, fmt.Errorf("workflow-store: lister: decode cursor: %w", err)
		}
		hasCursor = true
	}

	// Build the SELECT with a dialect-specific incident count expression.
	incExpr := l.dialect.IncidentCountExpr()
	baseSel := `SELECT instance_id, def_id, def_version, status, started_at, ended_at, ` + incExpr +
		` FROM wrkflw_instances`

	var (
		querySQL  string
		queryArgs []any
	)

	ct := timeArg(l.dialect, cursorTime)

	switch {
	case filter.Status != nil && hasCursor:
		pred := l.dialect.KeysetCursorPredicate() // includes leading "AND "
		querySQL = baseSel + ` WHERE status = ? ` + pred +
			`ORDER BY started_at DESC, instance_id DESC LIMIT ?`
		if l.dialect.KeysetCursorArgCount() == 2 {
			queryArgs = []any{int16(*filter.Status), ct, cursorID, fetch}
		} else {
			queryArgs = []any{int16(*filter.Status), ct, ct, cursorID, fetch}
		}
	case filter.Status != nil:
		querySQL = baseSel + ` WHERE status = ? ORDER BY started_at DESC, instance_id DESC LIMIT ?`
		queryArgs = []any{int16(*filter.Status), fetch}
	case hasCursor:
		// Strip "AND " from the cursor predicate so it can follow WHERE directly.
		pred := trimLeadingAND(l.dialect.KeysetCursorPredicate())
		querySQL = baseSel + ` WHERE ` + pred +
			`ORDER BY started_at DESC, instance_id DESC LIMIT ?`
		if l.dialect.KeysetCursorArgCount() == 2 {
			queryArgs = []any{ct, cursorID, fetch}
		} else {
			queryArgs = []any{ct, ct, cursorID, fetch}
		}
	default:
		querySQL = baseSel + ` ORDER BY started_at DESC, instance_id DESC LIMIT ?`
		queryArgs = []any{fetch}
	}

	q := l.querier()
	rows, err := q.Query(ctx, l.dialect.Rebind(querySQL), queryArgs...)
	if err != nil {
		return runtime.InstancePage{}, fmt.Errorf("workflow-store: lister: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]runtime.InstanceSummary, 0, fetch)
	for rows.Next() {
		summary, err := l.scanSummaryRow(rows)
		if err != nil {
			return runtime.InstancePage{}, err
		}
		items = append(items, summary)
	}
	if err := rows.Err(); err != nil {
		return runtime.InstancePage{}, fmt.Errorf("workflow-store: lister: rows: %w", err)
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
		total, err := l.countInstances(ctx, q, filter.Status)
		if err != nil {
			return runtime.InstancePage{}, err
		}
		page.TotalCount = total
	}

	return page, nil
}

// scanSummaryRow reads one row from the query result set into an InstanceSummary.
// It handles the dialect split for time columns: when [dialect.Dialect.TimestampsAsText]
// is true (SQLite), started_at / ended_at are stored as RFC3339Nano TEXT strings
// and must be parsed; on Postgres / MySQL the driver returns time.Time natively.
func (l *Lister) scanSummaryRow(rows database.Rows) (runtime.InstanceSummary, error) {
	var (
		instanceID    string
		defID         string
		defVersion    int
		status        int16
		incidentCount int
	)

	var startedAt time.Time
	var endedAt *time.Time

	if l.dialect.TimestampsAsText() {
		// TEXT-timestamp path (SQLite): timestamps are RFC3339Nano strings.
		// Scan into strings and parse manually (ADR-0080).
		var startedAtStr string
		var endedAtStr *string

		if err := rows.Scan(
			&instanceID, &defID, &defVersion, &status,
			&startedAtStr, &endedAtStr,
			&incidentCount,
		); err != nil {
			return runtime.InstanceSummary{}, fmt.Errorf("workflow-store: lister: scan (text-timestamp): %w", err)
		}

		t, err := parseTimeText(startedAtStr)
		if err != nil {
			return runtime.InstanceSummary{}, err
		}
		startedAt = t

		if endedAtStr != nil {
			t2, err := parseTimeText(*endedAtStr)
			if err != nil {
				return runtime.InstanceSummary{}, err
			}
			endedAt = &t2
		}
	} else {
		// Native time.Time path (Postgres / MySQL): driver provides time.Time.
		if err := rows.Scan(
			&instanceID, &defID, &defVersion, &status,
			&startedAt, &endedAt,
			&incidentCount,
		); err != nil {
			return runtime.InstanceSummary{}, fmt.Errorf("workflow-store: lister: scan: %w", err)
		}
		// Normalise to UTC (Postgres may return host-zone TIMESTAMPTZ;
		// MySQL may return the connection timezone).
		startedAt = startedAt.UTC()
		if endedAt != nil {
			t := endedAt.UTC()
			endedAt = &t
		}
	}

	return runtime.InstanceSummary{
		InstanceID:    instanceID,
		DefID:         defID,
		DefVersion:    defVersion,
		Status:        engine.Status(status),
		StartedAt:     startedAt,
		EndedAt:       endedAt,
		IncidentCount: incidentCount,
	}, nil
}

// countInstances executes a COUNT(*) query optionally filtered by status.
func (l *Lister) countInstances(ctx context.Context, q database.Querier, status *engine.Status) (int, error) {
	var total int
	var err error
	if status != nil {
		err = q.QueryRow(ctx, l.dialect.Rebind(
			`SELECT COUNT(*) FROM wrkflw_instances WHERE status = ?`),
			int16(*status),
		).Scan(&total)
	} else {
		err = q.QueryRow(ctx, `SELECT COUNT(*) FROM wrkflw_instances`).Scan(&total)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("workflow-store: lister: count: %w", err)
	}
	return total, nil
}

// trimLeadingAND removes a leading "AND " prefix so the keyset predicate can
// follow WHERE directly (no-status path). The predicates are authored with a
// leading "AND " to append cleanly after an existing WHERE clause; this helper
// strips it for the cursor-only branch.
func trimLeadingAND(pred string) string {
	if len(pred) >= 4 && pred[:4] == "AND " {
		return pred[4:]
	}
	return pred
}
