package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Compile-time checks: *CallLinkStore satisfies [kernel.CallLinkStore] and
// [kernel.CallLineageReader].
var _ kernel.CallLinkStore = (*CallLinkStore)(nil)
var _ kernel.CallLineageReader = (*CallLinkStore)(nil)

// CallLinkOption is a functional option for [CallLinkStore].
type CallLinkOption func(*CallLinkStore)

// WithCallLinkLease configures an opt-in claim lease on the store. When ttl > 0
// ClaimPending atomically stamps claimed_at/claimed_by, hiding each row from
// concurrent workers until the lease expires. When ttl <= 0 (the default) the
// original plain SELECT is used unchanged — backward-compatible.
//
// The exact SQL shape varies by dialect capabilities:
//   - Postgres (SupportsReturning=true, SupportsSkipLocked=true): single
//     UPDATE…FROM (SELECT…FOR UPDATE SKIP LOCKED)…RETURNING round-trip.
//   - MySQL (SupportsReturning=false, SupportsSkipLocked=true): transactional
//     SELECT…FOR UPDATE SKIP LOCKED followed by a separate UPDATE.
//   - SQLite (SupportsReturning=true, SupportsSkipLocked=false): plain
//     UPDATE…WHERE child_instance_id IN (SELECT…[LIMIT n])…RETURNING (no locking).
func WithCallLinkLease(owner string, ttl time.Duration) CallLinkOption {
	return func(s *CallLinkStore) {
		s.leaseOwner = owner
		s.leaseTTL = ttl
	}
}

// WithCallLinkClock overrides the clock used for lease timestamps. The default
// is [clock.System]. Inject a fake clock in tests for deterministic behaviour.
// A nil clock is ignored (the default is kept).
func WithCallLinkClock(clk clock.Clock) CallLinkOption {
	return func(s *CallLinkStore) {
		if clk != nil {
			s.clk = clk
		}
	}
}

// CallLinkStore is the vendor-neutral, dialect-parametrised
// [kernel.CallLinkStore] and [kernel.CallLineageReader]. It covers the
// read/claim side of the parent↔child call-activity correlation; the write side
// is fused into [Store.Create] and [Store.Commit] (ADR-0025).
//
// SQL is written once with ? placeholders and converted to the backend's native
// form via [dialect.Dialect.Rebind]. The leased-claim path branches on dialect
// capabilities ([dialect.Dialect.SupportsReturning] and
// [dialect.Dialect.SupportsSkipLocked]) rather than on dialect name, keeping
// per-dialect divergences behind the Dialect abstraction.
//
// CallLinkStore is safe for concurrent use.
type CallLinkStore struct {
	conn       any // *pgxpool.Pool or *sql.DB
	dialect    dialect.Dialect
	leaseOwner string
	leaseTTL   time.Duration
	clk        clock.Clock
}

// NewCallLinkStore constructs a CallLinkStore over conn using dialect d. conn
// must be either a *pgxpool.Pool (Postgres) or a *sql.DB (MySQL, SQLite).
// Pass [CallLinkOption] values to opt in to lease-based multi-replica
// exclusivity; existing zero-option call sites compile unchanged.
// Returns [ErrNilDependency] when conn is nil or d is nil.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	cls, err := store.NewCallLinkStore(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	cls, err := store.NewCallLinkStore(db, dialect.NewSQLite())
func NewCallLinkStore(conn any, d dialect.Dialect, opts ...CallLinkOption) (*CallLinkStore, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	s := &CallLinkStore{conn: conn, dialect: d, clk: clock.System()}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// ClaimPending returns up to limit terminal-but-unnotified call links.
//
// When leaseTTL > 0 (lease mode) it atomically stamps claimed_at/claimed_by,
// hiding each row from concurrent workers until the lease expires. The exact
// mechanism is chosen via dialect capabilities:
//
//   - Postgres: UPDATE…FROM (SELECT…FOR UPDATE SKIP LOCKED)…RETURNING
//   - MySQL:    transactional SELECT…FOR UPDATE SKIP LOCKED + UPDATE
//   - SQLite:   UPDATE…WHERE child_instance_id IN (SELECT…[LIMIT n])…RETURNING (no locking)
//
// When leaseTTL <= 0 (default) a plain SELECT is used (no stamp, no locking —
// backward-compatible). A limit <= 0 means "no limit" (all matching rows).
func (s *CallLinkStore) ClaimPending(ctx context.Context, limit int) ([]kernel.PendingNotify, error) {
	if s.leaseTTL > 0 {
		return s.claimPendingLeased(ctx, limit)
	}
	return s.claimPendingPlain(ctx, limit)
}

// claimPendingPlain is the original plain-SELECT path (ttl <= 0).
func (s *CallLinkStore) claimPendingPlain(ctx context.Context, limit int) ([]kernel.PendingNotify, error) {
	const baseQuery = `
		SELECT child_instance_id, parent_instance_id, parent_command_id,
		       parent_def_id, parent_def_version, depth,
		       status, output, error
		FROM   wrkflw_call_links
		WHERE  status IN ('completed', 'failed')
		  AND  notified_at IS NULL
		ORDER  BY child_instance_id`

	q, err := database.From(s.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim: conn: %w", err)
	}

	var (
		rows     database.Rows
		queryErr error
	)

	if limit > 0 {
		rows, queryErr = q.Query(ctx, s.dialect.Rebind(baseQuery+" LIMIT ?"), limit)
	} else {
		rows, queryErr = q.Query(ctx, s.dialect.Rebind(baseQuery))
	}
	if queryErr != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim: query: %w", queryErr)
	}
	defer func() { _ = rows.Close() }()

	return s.scanPendingRows(rows, "claim")
}

// claimPendingLeased branches on dialect capabilities:
//   - SupportsReturning && SupportsSkipLocked → Postgres path
//   - !SupportsReturning && SupportsSkipLocked → MySQL path
//   - SupportsReturning && !SupportsSkipLocked → SQLite path
func (s *CallLinkStore) claimPendingLeased(ctx context.Context, limit int) ([]kernel.PendingNotify, error) {
	now := s.clk.Now().UTC()
	cutoff := now.Add(-s.leaseTTL)

	switch {
	case s.dialect.SupportsReturning() && s.dialect.SupportsSkipLocked():
		return s.claimLeasedReturning(ctx, now, cutoff, limit)
	case !s.dialect.SupportsReturning() && s.dialect.SupportsSkipLocked():
		return s.claimLeasedSelectUpdate(ctx, now, cutoff, limit)
	default:
		// SupportsReturning && !SupportsSkipLocked (SQLite): plain single-writer claim.
		return s.claimLeasedSQLite(ctx, now, cutoff, limit)
	}
}

// claimLeasedReturning implements the Postgres leased-claim path:
// UPDATE … FROM (SELECT … FOR UPDATE SKIP LOCKED [LIMIT n]) … RETURNING …
// This is a single atomic round-trip that both stamps claimed_at/claimed_by
// and returns the claimed rows.
func (s *CallLinkStore) claimLeasedReturning(ctx context.Context, now, cutoff time.Time, limit int) ([]kernel.PendingNotify, error) {
	const queryNoLimit = `
		UPDATE wrkflw_call_links AS c
		   SET claimed_at = ?, claimed_by = ?
		  FROM (
		    SELECT child_instance_id
		      FROM wrkflw_call_links
		     WHERE status IN ('completed','failed')
		       AND notified_at IS NULL
		       AND (claimed_at IS NULL OR claimed_at <= ?)
		     ORDER BY child_instance_id
		     FOR UPDATE SKIP LOCKED
		  ) AS picked
		 WHERE c.child_instance_id = picked.child_instance_id
		 RETURNING c.child_instance_id, c.parent_instance_id, c.parent_command_id,
		           c.parent_def_id, c.parent_def_version, c.depth, c.status, c.output, c.error`

	const queryWithLimit = `
		UPDATE wrkflw_call_links AS c
		   SET claimed_at = ?, claimed_by = ?
		  FROM (
		    SELECT child_instance_id
		      FROM wrkflw_call_links
		     WHERE status IN ('completed','failed')
		       AND notified_at IS NULL
		       AND (claimed_at IS NULL OR claimed_at <= ?)
		     ORDER BY child_instance_id
		     FOR UPDATE SKIP LOCKED
		     LIMIT ?
		  ) AS picked
		 WHERE c.child_instance_id = picked.child_instance_id
		 RETURNING c.child_instance_id, c.parent_instance_id, c.parent_command_id,
		           c.parent_def_id, c.parent_def_version, c.depth, c.status, c.output, c.error`

	q, err := database.From(s.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim leased (returning): conn: %w", err)
	}

	var rows database.Rows
	if limit > 0 {
		rows, err = q.Query(ctx, s.dialect.Rebind(queryWithLimit),
			timeArg(s.dialect, now), s.leaseOwner, timeArg(s.dialect, cutoff), limit)
	} else {
		rows, err = q.Query(ctx, s.dialect.Rebind(queryNoLimit),
			timeArg(s.dialect, now), s.leaseOwner, timeArg(s.dialect, cutoff))
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim leased (returning): query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanPendingRows(rows, "claim leased (returning)")
}

// claimLeasedSelectUpdate implements the MySQL leased-claim path:
// Inside a transaction: SELECT … FOR UPDATE SKIP LOCKED (with optional LIMIT),
// collect the rows, UPDATE claimed_at/claimed_by on the locked IDs, return.
func (s *CallLinkStore) claimLeasedSelectUpdate(ctx context.Context, now, cutoff time.Time, limit int) ([]kernel.PendingNotify, error) {
	var pending []kernel.PendingNotify

	q, err := transaction.JoinOrBegin(ctx, s.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim leased (select-update): begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	// Step 1: SELECT eligible rows with FOR UPDATE SKIP LOCKED.
	// LIMIT must be a literal in the query string (cannot be a ? bind with locking
	// clause on MySQL 8.0).
	selectQuery := `
		SELECT child_instance_id, parent_instance_id, parent_command_id,
		       parent_def_id, parent_def_version, depth,
		       status, output, error
		FROM   wrkflw_call_links
		WHERE  status IN ('completed', 'failed')
		  AND  notified_at IS NULL
		  AND  (claimed_at IS NULL OR claimed_at <= ?)
		ORDER  BY child_instance_id`

	if limit > 0 {
		selectQuery += " LIMIT " + fmt.Sprintf("%d", limit)
	}
	selectQuery += " FOR UPDATE SKIP LOCKED"

	rows, err := q.Query(ctx, s.dialect.Rebind(selectQuery), timeArg(s.dialect, cutoff))
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim leased (select-update): select: %w", err)
	}

	scanned, err := s.scanPendingRows(rows, "claim leased (select-update)")
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if len(scanned) == 0 {
		committed = true
		_ = q.Commit(ctx)
		return nil, nil
	}

	// Step 2: UPDATE claimed_at/claimed_by on the locked IDs.
	ids := make([]string, len(scanned))
	for i, pn := range scanned {
		ids[i] = pn.Link.ChildInstanceID
	}

	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]

	args := []any{timeArg(s.dialect, now), s.leaseOwner}
	for _, id := range ids {
		args = append(args, id)
	}

	if _, err := q.Exec(ctx,
		s.dialect.Rebind(`UPDATE wrkflw_call_links SET claimed_at=?, claimed_by=? WHERE child_instance_id IN (`+placeholders+`)`),
		args...,
	); err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim leased (select-update): update: %w", err)
	}

	if err := q.Commit(ctx); err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim leased (select-update): commit: %w", err)
	}
	committed = true
	pending = scanned
	return pending, nil
}

// claimLeasedSQLite implements the SQLite leased-claim path.
// SQLite has no FOR UPDATE SKIP LOCKED and is single-writer (SetMaxOpenConns(1)).
// A plain UPDATE … WHERE child_instance_id = (SELECT … LIMIT 1) … RETURNING …
// is correct under the single-writer contract; concurrency is NOT asserted for SQLite.
func (s *CallLinkStore) claimLeasedSQLite(ctx context.Context, now, cutoff time.Time, limit int) ([]kernel.PendingNotify, error) {
	const queryNoLimit = `
		UPDATE wrkflw_call_links
		   SET claimed_at = ?, claimed_by = ?
		 WHERE child_instance_id IN (
		   SELECT child_instance_id
		     FROM wrkflw_call_links
		    WHERE status IN ('completed','failed')
		      AND notified_at IS NULL
		      AND (claimed_at IS NULL OR claimed_at <= ?)
		    ORDER BY child_instance_id
		 )
		 RETURNING child_instance_id, parent_instance_id, parent_command_id,
		           parent_def_id, parent_def_version, depth, status, output, error`

	const queryWithLimit = `
		UPDATE wrkflw_call_links
		   SET claimed_at = ?, claimed_by = ?
		 WHERE child_instance_id IN (
		   SELECT child_instance_id
		     FROM wrkflw_call_links
		    WHERE status IN ('completed','failed')
		      AND notified_at IS NULL
		      AND (claimed_at IS NULL OR claimed_at <= ?)
		    ORDER BY child_instance_id
		    LIMIT ?
		 )
		 RETURNING child_instance_id, parent_instance_id, parent_command_id,
		           parent_def_id, parent_def_version, depth, status, output, error`

	q, err := database.From(s.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim leased (sqlite): conn: %w", err)
	}

	var rows database.Rows
	if limit > 0 {
		rows, err = q.Query(ctx, s.dialect.Rebind(queryWithLimit),
			timeArg(s.dialect, now), s.leaseOwner, timeArg(s.dialect, cutoff), limit)
	} else {
		rows, err = q.Query(ctx, s.dialect.Rebind(queryNoLimit),
			timeArg(s.dialect, now), s.leaseOwner, timeArg(s.dialect, cutoff))
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: claim leased (sqlite): query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanPendingRows(rows, "claim leased (sqlite)")
}

// scanPendingRows scans a [database.Rows] result set into a slice of
// [kernel.PendingNotify]. The column projection must be:
// child_instance_id, parent_instance_id, parent_command_id,
// parent_def_id, parent_def_version, depth, status, output, error.
func (s *CallLinkStore) scanPendingRows(rows database.Rows, op string) ([]kernel.PendingNotify, error) {
	var pending []kernel.PendingNotify
	for rows.Next() {
		var (
			childID    string
			parentID   string
			commandID  string
			defID      string
			defVersion int
			depth      int
			status     string
			outputJSON []byte
			errText    *string
		)
		if err := rows.Scan(
			&childID, &parentID, &commandID,
			&defID, &defVersion, &depth,
			&status, &outputJSON, &errText,
		); err != nil {
			return nil, fmt.Errorf("workflow-store: call links: %s: scan: %w", op, err)
		}

		var output map[string]any
		if len(outputJSON) > 0 {
			if err := json.Unmarshal(outputJSON, &output); err != nil {
				return nil, fmt.Errorf("workflow-store: call links: %s: unmarshal output: %w", op, err)
			}
		}

		var errStr string
		if errText != nil {
			errStr = *errText
		}

		pending = append(pending, kernel.PendingNotify{
			Link: kernel.CallLink{
				ChildInstanceID:  childID,
				ParentInstanceID: parentID,
				ParentCommandID:  commandID,
				ParentDefID:      defID,
				ParentDefVersion: defVersion,
				Depth:            depth,
			},
			Outcome: kernel.CallOutcome{
				Completed: status == "completed",
				Output:    output,
				Err:       errStr,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: call links: %s: rows: %w", op, err)
	}
	return pending, nil
}

// MarkNotified records that the parent for childInstanceID has been resumed by
// setting status='notified' and stamping notified_at with the current UTC time
// (from the injected clock).
func (s *CallLinkStore) MarkNotified(ctx context.Context, childInstanceID string) error {
	q, err := database.From(s.conn)
	if err != nil {
		return fmt.Errorf("workflow-store: call links: mark notified: conn: %w", err)
	}
	_, err = q.Exec(ctx, s.dialect.Rebind(
		`UPDATE wrkflw_call_links
		    SET status = 'notified', notified_at = ?
		  WHERE child_instance_id = ?`),
		timeArg(s.dialect, s.clk.Now().UTC()),
		childInstanceID,
	)
	if err != nil {
		return fmt.Errorf("workflow-store: call links: mark notified: %w", err)
	}
	return nil
}

// LookupChild returns the call link for a child instance. It returns
// (link, true, nil) when found, and (CallLink{}, false, nil) when no row
// exists for the given childInstanceID (i.e. it is a root instance).
func (s *CallLinkStore) LookupChild(ctx context.Context, childInstanceID string) (kernel.CallLink, bool, error) {
	q, err := database.From(s.conn)
	if err != nil {
		return kernel.CallLink{}, false, fmt.Errorf("workflow-store: call links: lookup: conn: %w", err)
	}

	var (
		childID    string
		parentID   string
		commandID  string
		defID      string
		defVersion int
		depth      int
	)
	err = q.QueryRow(ctx, s.dialect.Rebind(
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE child_instance_id = ?`),
		childInstanceID,
	).Scan(&childID, &parentID, &commandID, &defID, &defVersion, &depth)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return kernel.CallLink{}, false, nil
		}
		return kernel.CallLink{}, false, fmt.Errorf("workflow-store: call links: lookup: %w", err)
	}

	return kernel.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  commandID,
		ParentDefID:      defID,
		ParentDefVersion: defVersion,
		Depth:            depth,
	}, true, nil
}

// ListRunningChildren returns all non-terminal child links whose
// parent_instance_id matches parentInstanceID and whose status is 'running',
// ordered by child_instance_id for deterministic results.
// Returns a non-nil empty slice when no running children exist.
func (s *CallLinkStore) ListRunningChildren(ctx context.Context, parentInstanceID string) ([]kernel.CallLink, error) {
	q, err := database.From(s.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: list running children: conn: %w", err)
	}

	rows, err := q.Query(ctx, s.dialect.Rebind(
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE parent_instance_id = ?
		    AND status = 'running'
		  ORDER BY child_instance_id`),
		parentInstanceID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: list running children: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []kernel.CallLink
	for rows.Next() {
		var (
			childID    string
			parentID   string
			commandID  string
			defID      string
			defVersion int
			depth      int
		)
		if err := rows.Scan(&childID, &parentID, &commandID, &defID, &defVersion, &depth); err != nil {
			return nil, fmt.Errorf("workflow-store: call links: list running children: scan: %w", err)
		}
		links = append(links, kernel.CallLink{
			ChildInstanceID:  childID,
			ParentInstanceID: parentID,
			ParentCommandID:  commandID,
			ParentDefID:      defID,
			ParentDefVersion: defVersion,
			Depth:            depth,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: call links: list running children: rows: %w", err)
	}

	// Non-nil empty slice per the CallLinkStore contract.
	if links == nil {
		links = []kernel.CallLink{}
	}
	return links, nil
}

// ParentOf returns the [kernel.CallLink] describing the parent call
// relationship for childID. Returns (nil, nil) when childID is a root instance
// (no row in wrkflw_call_links). Implements [kernel.CallLineageReader].
func (s *CallLinkStore) ParentOf(ctx context.Context, childID string) (*kernel.CallLink, error) {
	q, err := database.From(s.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: parent of: conn: %w", err)
	}

	var (
		childInstID string
		parentID    string
		commandID   string
		defID       string
		defVersion  int
		depth       int
	)
	err = q.QueryRow(ctx, s.dialect.Rebind(
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE child_instance_id = ?`),
		childID,
	).Scan(&childInstID, &parentID, &commandID, &defID, &defVersion, &depth)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("workflow-store: call links: parent of: %w", err)
	}
	return &kernel.CallLink{
		ChildInstanceID:  childInstID,
		ParentInstanceID: parentID,
		ParentCommandID:  commandID,
		ParentDefID:      defID,
		ParentDefVersion: defVersion,
		Depth:            depth,
	}, nil
}

// ChildrenOf returns all [kernel.CallLink]s whose parent_instance_id equals
// parentID, ordered by (created_at, child_instance_id). Returns an empty
// (non-nil) slice when no children exist. Implements [kernel.CallLineageReader].
func (s *CallLinkStore) ChildrenOf(ctx context.Context, parentID string) ([]kernel.CallLink, error) {
	q, err := database.From(s.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: children of: conn: %w", err)
	}

	rows, err := q.Query(ctx, s.dialect.Rebind(
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE parent_instance_id = ?
		  ORDER BY created_at, child_instance_id`),
		parentID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: call links: children of: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	links := []kernel.CallLink{}
	for rows.Next() {
		var (
			childInstID  string
			parentInstID string
			commandID    string
			defID        string
			defVersion   int
			depth        int
		)
		if err := rows.Scan(&childInstID, &parentInstID, &commandID, &defID, &defVersion, &depth); err != nil {
			return nil, fmt.Errorf("workflow-store: call links: children of: scan: %w", err)
		}
		links = append(links, kernel.CallLink{
			ChildInstanceID:  childInstID,
			ParentInstanceID: parentInstID,
			ParentCommandID:  commandID,
			ParentDefID:      defID,
			ParentDefVersion: defVersion,
			Depth:            depth,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: call links: children of: rows: %w", err)
	}
	return links, nil
}
