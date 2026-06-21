package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time check: CallLinkStore satisfies runtime.CallLinkStore.
var _ runtime.CallLinkStore = (*CallLinkStore)(nil)

// CallLinkStore is the Postgres-backed runtime.CallLinkStore (read/claim side).
// The write side is fused into Store.Create / Store.Commit (ADR-0025).
type CallLinkStore struct {
	pool *pgxpool.Pool
}

// NewCallLinkStore constructs a CallLinkStore over the given pool.
// Migrate must be applied before calling any method.
func NewCallLinkStore(pool *pgxpool.Pool) *CallLinkStore {
	return &CallLinkStore{pool: pool}
}

// ClaimPending returns up to limit terminal-but-unnotified call links.
//
// It executes a plain SELECT (not FOR UPDATE SKIP LOCKED) — a tx-holding lock
// would be released before the notifier delivers the parent callback, making it
// pointless. The stable ORDER BY child_instance_id ensures deterministic claim
// order across retries and tests. A limit <= 0 means "no limit" (all matching
// rows), mirroring the in-memory store's "0 = all" convention.
func (c *CallLinkStore) ClaimPending(ctx context.Context, limit int) ([]runtime.PendingNotify, error) {
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
		return nil, fmt.Errorf("postgres: call links: claim: query: %w", queryErr)
	}
	defer rows.Close()

	var pending []runtime.PendingNotify
	for rows.Next() {
		var (
			childID          string
			parentID         string
			commandID        string
			defID            string
			defVersion       int
			depth            int
			status           string
			outputJSON       []byte
			errText          *string
		)
		if err := rows.Scan(
			&childID, &parentID, &commandID,
			&defID, &defVersion, &depth,
			&status, &outputJSON, &errText,
		); err != nil {
			return nil, fmt.Errorf("postgres: call links: claim: scan: %w", err)
		}

		var output map[string]any
		if len(outputJSON) > 0 {
			if err := json.Unmarshal(outputJSON, &output); err != nil {
				return nil, fmt.Errorf("postgres: call links: claim: unmarshal output: %w", err)
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
		return nil, fmt.Errorf("postgres: call links: claim: rows: %w", err)
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
		return fmt.Errorf("postgres: call links: mark notified: %w", err)
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
		return runtime.CallLink{}, false, fmt.Errorf("postgres: call links: lookup: %w", err)
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
