package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time checks: CallLinkStore satisfies runtime.CallLinkStore and
// runtime.CallLineageReader.
var _ runtime.CallLinkStore = (*CallLinkStore)(nil)
var _ runtime.CallLineageReader = (*CallLinkStore)(nil)

// CallLinkOption is a functional option for CallLinkStore.
type CallLinkOption func(*CallLinkStore)

// WithCallLinkLease configures an opt-in claim lease on the store. When
// ttl > 0, ClaimPending uses a SELECT ... FOR UPDATE SKIP LOCKED + UPDATE
// pattern that stamps claimed_at/claimed_by on each row, hiding it from
// concurrent replicas until the lease expires. When ttl <= 0 (the default),
// the original plain SELECT is used unchanged (backward-compatible).
func WithCallLinkLease(owner string, ttl time.Duration) CallLinkOption {
	return func(s *CallLinkStore) {
		s.leaseOwner = owner
		s.leaseTTL = ttl
	}
}

// WithCallLinkClock overrides the clock used for lease timestamps. The default
// is clock.System(). Inject a fake clock in tests for deterministic behaviour.
// A nil clock is ignored (the default is kept).
func WithCallLinkClock(clk clock.Clock) CallLinkOption {
	return func(s *CallLinkStore) {
		if clk != nil {
			s.clk = clk
		}
	}
}

// CallLinkStore is the MySQL-backed runtime.CallLinkStore (read/claim side).
// The write side is fused into Store.Create / Store.Commit (ADR-0025).
type CallLinkStore struct {
	db         *sql.DB
	leaseOwner string
	leaseTTL   time.Duration
	clk        clock.Clock
}

// NewCallLinkStore constructs a CallLinkStore over the given *sql.DB.
// Migrate must be applied before calling any method. Pass CallLinkOption values
// to opt in to lease-based multi-replica exclusivity; existing zero-option call
// sites compile unchanged.
func NewCallLinkStore(db *sql.DB, opts ...CallLinkOption) *CallLinkStore {
	s := &CallLinkStore{
		db:  db,
		clk: clock.System(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ClaimPending returns up to limit terminal-but-unnotified call links.
//
// When leaseTTL > 0 (lease mode), it runs a transactional SELECT...FOR UPDATE
// SKIP LOCKED + UPDATE that atomically stamps claimed_at/claimed_by, hiding
// each row from concurrent replicas until the lease expires. When leaseTTL <= 0
// (default), it executes a plain SELECT (no stamp, no locking — backward-compat).
//
// A limit <= 0 means "no limit" (all matching rows).
func (c *CallLinkStore) ClaimPending(ctx context.Context, limit int) ([]runtime.PendingNotify, error) {
	if c.leaseTTL > 0 {
		return c.claimPendingLeased(ctx, limit)
	}
	return c.claimPendingPlain(ctx, limit)
}

// claimPendingPlain is the original plain-SELECT path (ttl <= 0).
func (c *CallLinkStore) claimPendingPlain(ctx context.Context, limit int) ([]runtime.PendingNotify, error) {
	const baseQuery = `
		SELECT child_instance_id, parent_instance_id, parent_command_id,
		       parent_def_id, parent_def_version, depth,
		       status, output, error
		FROM   wrkflw_call_links
		WHERE  status IN ('completed', 'failed')
		  AND  notified_at IS NULL
		ORDER  BY child_instance_id`

	var (
		rows     *sql.Rows
		queryErr error
	)

	if limit > 0 {
		rows, queryErr = c.db.QueryContext(ctx, baseQuery+" LIMIT "+fmt.Sprintf("%d", limit))
	} else {
		rows, queryErr = c.db.QueryContext(ctx, baseQuery)
	}
	if queryErr != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: call links: claim: query: %w", queryErr)
	}
	defer func() { _ = rows.Close() }()

	return mysqlScanPendingRowsWithIDs(rows)
}

// claimPendingLeased is the lease-stamping SELECT...FOR UPDATE SKIP LOCKED +
// UPDATE path (ttl > 0). MySQL has no RETURNING clause, so we do it in a
// transaction: SELECT ids with FOR UPDATE SKIP LOCKED, then UPDATE those ids,
// then return the pre-fetched rows.
func (c *CallLinkStore) claimPendingLeased(ctx context.Context, limit int) ([]runtime.PendingNotify, error) {
	now := c.clk.Now().UTC()
	cutoff := now.Add(-c.leaseTTL)

	var pending []runtime.PendingNotify

	err := txWith(ctx, c.db, func(tx *sql.Tx) error {
		// Step 1: SELECT rows eligible for claiming with FOR UPDATE SKIP LOCKED.
		// LIMIT must be formatted as a literal in the query (cannot use ? alongside
		// locking clause in MySQL 8.0).
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

		rows, err := tx.QueryContext(ctx, selectQuery, cutoff)
		if err != nil {
			return fmt.Errorf("workflow-persistence-mysql: call links: claim leased: select: %w", err)
		}
		defer func() { _ = rows.Close() }()

		scanned, err := mysqlScanPendingRowsWithIDs(rows)
		if err != nil {
			return err
		}
		if len(scanned) == 0 {
			return nil
		}

		// Step 2: UPDATE claimed_at/claimed_by on the locked ids.
		ids := make([]string, len(scanned))
		for i, pn := range scanned {
			ids[i] = pn.Link.ChildInstanceID
		}

		placeholders := strings.Repeat("?,", len(ids))
		placeholders = placeholders[:len(placeholders)-1] // trim trailing comma

		args := []any{now, c.leaseOwner}
		for _, id := range ids {
			args = append(args, id)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE wrkflw_call_links SET claimed_at=?, claimed_by=? WHERE child_instance_id IN (`+placeholders+`)`,
			args...,
		); err != nil {
			return fmt.Errorf("workflow-persistence-mysql: call links: claim leased: update: %w", err)
		}

		pending = scanned
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pending, nil
}

// mysqlScanPendingRowsWithIDs scans a *sql.Rows result into a slice of PendingNotify.
// The column projection must be:
// child_instance_id, parent_instance_id, parent_command_id,
// parent_def_id, parent_def_version, depth, status, output, error.
func mysqlScanPendingRowsWithIDs(rows *sql.Rows) ([]runtime.PendingNotify, error) {
	var pending []runtime.PendingNotify
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
			return nil, fmt.Errorf("workflow-persistence-mysql: call links: claim: scan: %w", err)
		}

		var output map[string]any
		if len(outputJSON) > 0 {
			if err := json.Unmarshal(outputJSON, &output); err != nil {
				return nil, fmt.Errorf("workflow-persistence-mysql: call links: claim: unmarshal output: %w", err)
			}
		}

		var errStr string
		if errText != nil {
			errStr = *errText
		}

		pending = append(pending, runtime.PendingNotify{
			Link: runtime.CallLink{
				ChildInstanceID:  childID,
				ParentInstanceID: parentID,
				ParentCommandID:  commandID,
				ParentDefID:      defID,
				ParentDefVersion: defVersion,
				Depth:            depth,
			},
			Outcome: runtime.CallOutcome{
				Completed: status == "completed",
				Output:    output,
				Err:       errStr,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: call links: claim: rows: %w", err)
	}
	return pending, nil
}

// MarkNotified records that the parent for childInstanceID has been resumed by
// setting status='notified' and stamping notified_at with the current UTC time.
func (c *CallLinkStore) MarkNotified(ctx context.Context, childInstanceID string) error {
	_, err := c.db.ExecContext(ctx,
		`UPDATE wrkflw_call_links
		    SET status = 'notified', notified_at = ?
		  WHERE child_instance_id = ?`,
		c.clk.Now().UTC(),
		childInstanceID,
	)
	if err != nil {
		return fmt.Errorf("workflow-persistence-mysql: call links: mark notified: %w", err)
	}
	return nil
}

// LookupChild returns the call link for a child instance. It returns
// (link, true, nil) when found, and (CallLink{}, false, nil) when no row
// exists for the given childInstanceID (i.e. it is a root instance).
func (c *CallLinkStore) LookupChild(ctx context.Context, childInstanceID string) (runtime.CallLink, bool, error) {
	var (
		childID    string
		parentID   string
		commandID  string
		defID      string
		defVersion int
		depth      int
	)
	err := c.db.QueryRowContext(ctx,
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE child_instance_id = ?`,
		childInstanceID,
	).Scan(&childID, &parentID, &commandID, &defID, &defVersion, &depth)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runtime.CallLink{}, false, nil
		}
		return runtime.CallLink{}, false, fmt.Errorf("workflow-persistence-mysql: call links: lookup: %w", err)
	}

	return runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  commandID,
		ParentDefID:      defID,
		ParentDefVersion: defVersion,
		Depth:            depth,
	}, true, nil
}

// ParentOf returns the CallLink for the given childID. It returns (nil, nil)
// when childID is a root instance (no row in wrkflw_call_links).
func (c *CallLinkStore) ParentOf(ctx context.Context, childID string) (*runtime.CallLink, error) {
	var (
		childInstID string
		parentID    string
		commandID   string
		defID       string
		defVersion  int
		depth       int
	)
	err := c.db.QueryRowContext(ctx,
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE child_instance_id = ?`,
		childID,
	).Scan(&childInstID, &parentID, &commandID, &defID, &defVersion, &depth)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("workflow-persistence-mysql: call links: parent of: %w", err)
	}
	return &runtime.CallLink{
		ChildInstanceID:  childInstID,
		ParentInstanceID: parentID,
		ParentCommandID:  commandID,
		ParentDefID:      defID,
		ParentDefVersion: defVersion,
		Depth:            depth,
	}, nil
}

// ChildrenOf returns all CallLinks whose parent_instance_id equals parentID,
// ordered by (created_at, child_instance_id). Returns an empty (non-nil) slice
// when no children exist.
func (c *CallLinkStore) ChildrenOf(ctx context.Context, parentID string) ([]runtime.CallLink, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE parent_instance_id = ?
		  ORDER BY created_at, child_instance_id`,
		parentID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: call links: children of: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	links := []runtime.CallLink{}
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
			return nil, fmt.Errorf("workflow-persistence-mysql: call links: children of: scan: %w", err)
		}
		links = append(links, runtime.CallLink{
			ChildInstanceID:  childInstID,
			ParentInstanceID: parentInstID,
			ParentCommandID:  commandID,
			ParentDefID:      defID,
			ParentDefVersion: defVersion,
			Depth:            depth,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: call links: children of: rows: %w", err)
	}
	return links, nil
}

// ListRunningChildren returns all non-terminal child links whose
// parent_instance_id matches parentInstanceID and whose status is 'running',
// ordered by child_instance_id for deterministic results.
func (c *CallLinkStore) ListRunningChildren(ctx context.Context, parentInstanceID string) ([]runtime.CallLink, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE parent_instance_id = ?
		    AND status = 'running'
		  ORDER BY child_instance_id`,
		parentInstanceID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: call links: list running children: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []runtime.CallLink
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
			return nil, fmt.Errorf("workflow-persistence-mysql: call links: list running children: scan: %w", err)
		}
		links = append(links, runtime.CallLink{
			ChildInstanceID:  childID,
			ParentInstanceID: parentID,
			ParentCommandID:  commandID,
			ParentDefID:      defID,
			ParentDefVersion: defVersion,
			Depth:            depth,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: call links: list running children: rows: %w", err)
	}

	if links == nil {
		links = []runtime.CallLink{}
	}
	return links, nil
}
