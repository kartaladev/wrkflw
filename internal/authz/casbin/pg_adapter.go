package casbin

import (
	"context"
	"errors"
	"fmt"

	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time assertion: pgAdapter satisfies casbin's persist.Adapter.
var _ persist.Adapter = (*pgAdapter)(nil)

// errNotImpl is returned by the auto-save stubs (AddPolicy / RemovePolicy /
// RemoveFilteredPolicy) until Task 3 implements them.
var errNotImpl = errors.New("casbin pgadapter: auto-save method not yet implemented")

// pgAdapter is a pgx-native casbin persist.Adapter backed by the casbin_rule
// table. Rules are stored as (ptype, v0..v5); unused trailing fields are empty
// strings. The auto-save methods (Add/Remove/RemoveFiltered) are stubbed until
// Task 3.
type pgAdapter struct {
	pool *pgxpool.Pool
}

func newPGAdapter(pool *pgxpool.Pool) *pgAdapter { return &pgAdapter{pool: pool} }

// padRule maps a casbin rule slice to the six fixed v0..v5 columns, padding
// missing trailing fields with empty strings. Rules longer than 6 fields are
// truncated to 6 (casbin_rule only has v0..v5).
func padRule(rule []string) [6]string {
	var v [6]string
	for i := 0; i < len(rule) && i < 6; i++ {
		v[i] = rule[i]
	}
	return v
}

// ruleFromCols rebuilds a casbin rule slice from six column values, trimming
// trailing empty fields so a 3-field rule round-trips as 3 fields, not 6.
func ruleFromCols(v [6]string) []string {
	n := 6
	for n > 0 && v[n-1] == "" {
		n--
	}
	return append([]string(nil), v[:n]...)
}

// LoadPolicy loads every rule from casbin_rule into the model.
func (a *pgAdapter) LoadPolicy(m model.Model) error {
	ctx := context.Background()
	rows, err := a.pool.Query(ctx,
		`SELECT ptype, v0, v1, v2, v3, v4, v5 FROM casbin_rule ORDER BY id`)
	if err != nil {
		return fmt.Errorf("casbin pgadapter: load: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ptype string
		var v [6]string
		if err := rows.Scan(&ptype, &v[0], &v[1], &v[2], &v[3], &v[4], &v[5]); err != nil {
			return fmt.Errorf("casbin pgadapter: load: scan: %w", err)
		}
		rule := append([]string{ptype}, ruleFromCols(v)...)
		if err := persist.LoadPolicyArray(rule, m); err != nil {
			return fmt.Errorf("casbin pgadapter: load: apply: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("casbin pgadapter: load: rows: %w", err)
	}
	return nil
}

// SavePolicy replaces all stored rules with the model's current p and g sections
// in a single transaction (DELETE-all then bulk INSERT via pgx.Batch).
func (a *pgAdapter) SavePolicy(m model.Model) error {
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("casbin pgadapter: save: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM casbin_rule`); err != nil {
		return fmt.Errorf("casbin pgadapter: save: delete: %w", err)
	}

	batch := &pgx.Batch{}
	queued := 0
	for _, sec := range []string{"p", "g"} {
		ast := m[sec]
		for ptype, assertion := range ast {
			for _, rule := range assertion.Policy {
				v := padRule(rule)
				batch.Queue(
					`INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
					ptype, v[0], v[1], v[2], v[3], v[4], v[5])
				queued++
			}
		}
	}
	if queued > 0 {
		br := tx.SendBatch(ctx, batch)
		for i := 0; i < queued; i++ {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return fmt.Errorf("casbin pgadapter: save: insert: %w", err)
			}
		}
		if err := br.Close(); err != nil {
			return fmt.Errorf("casbin pgadapter: save: batch close: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("casbin pgadapter: save: commit: %w", err)
	}
	return nil
}

// AddPolicy is a stub — auto-save implemented in Task 3.
func (a *pgAdapter) AddPolicy(string, string, []string) error { return errNotImpl }

// RemovePolicy is a stub — auto-save implemented in Task 3.
func (a *pgAdapter) RemovePolicy(string, string, []string) error { return errNotImpl }

// RemoveFilteredPolicy is a stub — auto-save implemented in Task 3.
func (a *pgAdapter) RemoveFilteredPolicy(string, string, int, ...string) error { return errNotImpl }
