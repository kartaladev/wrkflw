package casbin

import (
	"context"
	"fmt"

	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time assertion: pgAdapter satisfies casbin's persist.Adapter.
var _ persist.Adapter = (*pgAdapter)(nil)

// pgAdapter is a pgx-native casbin persist.Adapter backed by the casbin_rule
// table. Rules are stored as (ptype, v0..v5); unused trailing fields are empty
// strings. It supports casbin's Auto-Save feature: AddPolicy/RemovePolicy/
// RemoveFilteredPolicy persist policy changes directly to the database.
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

// AddPolicy inserts one rule (Auto-Save).
func (a *pgAdapter) AddPolicy(_ string, ptype string, rule []string) error {
	v := padRule(rule)
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ptype, v[0], v[1], v[2], v[3], v[4], v[5]); err != nil {
		return fmt.Errorf("casbin pgadapter: add: %w", err)
	}
	return nil
}

// RemovePolicy deletes rows matching the exact rule (Auto-Save).
func (a *pgAdapter) RemovePolicy(_ string, ptype string, rule []string) error {
	v := padRule(rule)
	if _, err := a.pool.Exec(context.Background(),
		`DELETE FROM casbin_rule
		  WHERE ptype=$1 AND v0=$2 AND v1=$3 AND v2=$4 AND v3=$5 AND v4=$6 AND v5=$7`,
		ptype, v[0], v[1], v[2], v[3], v[4], v[5]); err != nil {
		return fmt.Errorf("casbin pgadapter: remove: %w", err)
	}
	return nil
}

// RemoveFilteredPolicy deletes rows matching ptype plus the provided non-empty
// filter fields starting at fieldIndex (Auto-Save). Empty filter values are
// treated as "don't care" (casbin semantics).
func (a *pgAdapter) RemoveFilteredPolicy(_ string, ptype string, fieldIndex int, fieldValues ...string) error {
	args := []any{ptype}
	where := "ptype = $1"
	col := 2
	for i, val := range fieldValues {
		if val == "" {
			continue // skip don't-care slots
		}
		idx := fieldIndex + i
		if idx < 0 || idx > 5 {
			continue
		}
		where += fmt.Sprintf(" AND v%d = $%d", idx, col)
		args = append(args, val)
		col++
	}
	if _, err := a.pool.Exec(context.Background(),
		`DELETE FROM casbin_rule WHERE `+where, args...); err != nil {
		return fmt.Errorf("casbin pgadapter: remove filtered: %w", err)
	}
	return nil
}
