package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time check: CallLinkStore satisfies runtime.CallLinkStore.
var _ runtime.CallLinkStore = (*CallLinkStore)(nil)

// CallLinkOption is a functional option for CallLinkStore.
type CallLinkOption func(*CallLinkStore)

// WithCallLinkLease configures an opt-in claim lease on the store. When
// ttl > 0, ClaimPending uses an UPDATE...FOR UPDATE SKIP LOCKED query that
// stamps claimed_at/claimed_by, hiding each row from concurrent replicas until
// the lease expires. When ttl <= 0 (the default), the original plain SELECT is
// used unchanged.
func WithCallLinkLease(owner string, ttl time.Duration) CallLinkOption {
	return func(s *CallLinkStore) {
		s.leaseOwner = owner
		s.leaseTTL = ttl
	}
}

// WithCallLinkClock overrides the clock used for lease timestamps. The default
// is clock.System(). Inject a fake clock in tests for deterministic behaviour.
func WithCallLinkClock(clk clock.Clock) CallLinkOption {
	return func(s *CallLinkStore) {
		s.clk = clk
	}
}

// CallLinkStore is the Postgres-backed runtime.CallLinkStore (read/claim side).
// The write side is fused into Store.Create / Store.Commit (ADR-0025).
type CallLinkStore struct {
	pool       *pgxpool.Pool
	leaseOwner string
	leaseTTL   time.Duration
	clk        clock.Clock
}

// NewCallLinkStore constructs a CallLinkStore over the given pool.
// Migrate must be applied before calling any method. Pass CallLinkOption values
// to opt in to lease-based multi-replica exclusivity (ADR-0031); existing
// zero-option call sites compile unchanged.
func NewCallLinkStore(pool *pgxpool.Pool, opts ...CallLinkOption) *CallLinkStore {
	s := &CallLinkStore{
		pool: pool,
		clk:  clock.System(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ClaimPending returns up to limit terminal-but-unnotified call links.
//
// When leaseTTL > 0 (lease mode), it runs an UPDATE...FROM (SELECT...FOR UPDATE
// SKIP LOCKED)...RETURNING query that atomically stamps claimed_at/claimed_by
// on each row, hiding it from concurrent replicas until the lease expires
// (ADR-0031). When leaseTTL <= 0 (default), it executes the original plain
// SELECT unchanged (no stamp, no locking — backward-compatible).
//
// The stable ORDER BY child_instance_id ensures deterministic claim order across
// retries and tests. A limit <= 0 means "no limit" (all matching rows), mirroring
// the in-memory store's "0 = all" convention.
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
		rows interface {
			Next() bool
			Scan(...any) error
			Err() error
			Close()
		}
		queryErr error
	)

	if limit > 0 {
		rows, queryErr = c.pool.Query(ctx, baseQuery+" LIMIT $1", limit)
	} else {
		rows, queryErr = c.pool.Query(ctx, baseQuery)
	}
	if queryErr != nil {
		return nil, fmt.Errorf("workflow-postgres: call links: claim: query: %w", queryErr)
	}
	defer rows.Close()

	return scanPendingRows(rows)
}

// claimPendingLeased is the lease-stamping UPDATE...RETURNING path (ttl > 0).
func (c *CallLinkStore) claimPendingLeased(ctx context.Context, limit int) ([]runtime.PendingNotify, error) {
	now := c.clk.Now()
	cutoff := now.Add(-c.leaseTTL)

	// Build query: include LIMIT only when limit > 0.
	const queryNoLimit = `
		UPDATE wrkflw_call_links AS c
		   SET claimed_at = $1, claimed_by = $2
		  FROM (
		    SELECT child_instance_id
		      FROM wrkflw_call_links
		     WHERE status IN ('completed','failed')
		       AND notified_at IS NULL
		       AND (claimed_at IS NULL OR claimed_at <= $3)
		     ORDER BY child_instance_id
		     FOR UPDATE SKIP LOCKED
		  ) AS picked
		 WHERE c.child_instance_id = picked.child_instance_id
		 RETURNING c.child_instance_id, c.parent_instance_id, c.parent_command_id,
		           c.parent_def_id, c.parent_def_version, c.depth, c.status, c.output, c.error`

	const queryWithLimit = `
		UPDATE wrkflw_call_links AS c
		   SET claimed_at = $1, claimed_by = $2
		  FROM (
		    SELECT child_instance_id
		      FROM wrkflw_call_links
		     WHERE status IN ('completed','failed')
		       AND notified_at IS NULL
		       AND (claimed_at IS NULL OR claimed_at <= $3)
		     ORDER BY child_instance_id
		     FOR UPDATE SKIP LOCKED
		     LIMIT $4
		  ) AS picked
		 WHERE c.child_instance_id = picked.child_instance_id
		 RETURNING c.child_instance_id, c.parent_instance_id, c.parent_command_id,
		           c.parent_def_id, c.parent_def_version, c.depth, c.status, c.output, c.error`

	var (
		rows interface {
			Next() bool
			Scan(...any) error
			Err() error
			Close()
		}
		queryErr error
	)

	if limit > 0 {
		rows, queryErr = c.pool.Query(ctx, queryWithLimit, now, c.leaseOwner, cutoff, limit)
	} else {
		rows, queryErr = c.pool.Query(ctx, queryNoLimit, now, c.leaseOwner, cutoff)
	}
	if queryErr != nil {
		return nil, fmt.Errorf("workflow-postgres: call links: claim leased: query: %w", queryErr)
	}
	defer rows.Close()

	return scanPendingRows(rows)
}

// scanPendingRows scans a rows result set (SELECT or RETURNING) into a slice of
// PendingNotify. The column projection must be:
// child_instance_id, parent_instance_id, parent_command_id,
// parent_def_id, parent_def_version, depth, status, output, error.
func scanPendingRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]runtime.PendingNotify, error) {
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
			return nil, fmt.Errorf("workflow-postgres: call links: claim: scan: %w", err)
		}

		var output map[string]any
		if len(outputJSON) > 0 {
			if err := json.Unmarshal(outputJSON, &output); err != nil {
				return nil, fmt.Errorf("workflow-postgres: call links: claim: unmarshal output: %w", err)
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
		return nil, fmt.Errorf("workflow-postgres: call links: claim: rows: %w", err)
	}

	return pending, nil
}

// MarkNotified records that the parent for childInstanceID has been resumed by
// setting status='notified' and stamping notified_at with the current UTC time.
func (c *CallLinkStore) MarkNotified(ctx context.Context, childInstanceID string) error {
	_, err := c.pool.Exec(ctx,
		`UPDATE wrkflw_call_links
		    SET status = 'notified', notified_at = $2
		  WHERE child_instance_id = $1`,
		childInstanceID,
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("workflow-postgres: call links: mark notified: %w", err)
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
	err := c.pool.QueryRow(ctx,
		`SELECT child_instance_id, parent_instance_id, parent_command_id,
		        parent_def_id, parent_def_version, depth
		   FROM wrkflw_call_links
		  WHERE child_instance_id = $1`,
		childInstanceID,
	).Scan(&childID, &parentID, &commandID, &defID, &defVersion, &depth)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return runtime.CallLink{}, false, nil
		}
		return runtime.CallLink{}, false, fmt.Errorf("workflow-postgres: call links: lookup: %w", err)
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
