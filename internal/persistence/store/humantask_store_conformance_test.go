package store_test

import (
	"context"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// compile-time guard: the neutral store satisfies the public interface.
var _ humantask.TaskStore = (*store.HumanTaskStore)(nil)

func TestHumanTaskStoreConformance(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ts, err := store.NewHumanTaskStore(b.conn, b.dialect)
		require.NoError(t, err)

		t.Run("upsert_get_round_trip", func(t *testing.T) {
			due := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
			seed := humantask.HumanTask{
				TaskID:      "tok-rt-" + b.name,
				InstanceID:  "inst-1",
				NodeID:      "approve",
				State:       humantask.Unclaimed,
				Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
				Candidates:  []authz.Actor{{ID: "alice"}},
				Vars:        map[string]any{"amount": float64(100)},
				CreatedAt:   time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
				DueAt:       &due,
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: Upsert", b.name)

			got, err := ts.Get(t.Context(), "tok-rt-"+b.name)
			require.NoError(t, err, "%s: Get", b.name)
			assert.Equal(t, "inst-1", got.InstanceID, "%s: InstanceID", b.name)
			assert.Equal(t, humantask.Unclaimed, got.State, "%s: State", b.name)
			assert.Equal(t, []string{"manager"}, got.Eligibility.Roles, "%s: Eligibility.Roles", b.name)
			assert.Equal(t, []authz.Actor{{ID: "alice"}}, got.Candidates, "%s: Candidates", b.name)
			require.NotNil(t, got.DueAt, "%s: DueAt", b.name)
			assert.True(t, got.DueAt.Equal(due), "%s: DueAt value", b.name)
			assert.True(t, got.CreatedAt.Equal(seed.CreatedAt), "%s: CreatedAt", b.name)
			assert.Equal(t, map[string]any{"amount": float64(100)}, got.Vars, "%s: Vars", b.name)
		})

		t.Run("get_miss_returns_err_task_not_found", func(t *testing.T) {
			_, err := ts.Get(t.Context(), "tok-no-such-"+b.name)
			require.Error(t, err, "%s: Get missing must error", b.name)
			require.ErrorIs(t, err, humantask.ErrTaskNotFound,
				"%s: must wrap ErrTaskNotFound; got %v", b.name, err)
		})

		t.Run("upsert_no_due_at", func(t *testing.T) {
			seed := humantask.HumanTask{
				TaskID:     "tok-no-due-" + b.name,
				InstanceID: "inst-nd",
				NodeID:     "review",
				State:      humantask.Unclaimed,
				CreatedAt:  time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC),
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: Upsert no-due", b.name)

			got, err := ts.Get(t.Context(), "tok-no-due-"+b.name)
			require.NoError(t, err, "%s: Get no-due", b.name)
			assert.Nil(t, got.DueAt, "%s: DueAt must be nil", b.name)
		})

		t.Run("assigned_to_filters_by_claimed_by_and_sorts", func(t *testing.T) {
			// Seed tasks: two claimed by "bob", one by "carol", one unclaimed.
			// Every claim carries a real timestamp: claimed_at is the presence
			// discriminator (ADR-0148 amendment 2), and a zero time.Time is outside
			// the MySQL DATETIME range anyway.
			claimedAt := time.Date(2026, 7, 28, 6, 0, 0, 0, time.UTC)
			tasks := []humantask.HumanTask{
				{
					TaskID: "tok-bob-2-" + b.name, InstanceID: "i1", NodeID: "n1",
					State:     humantask.Claimed,
					Claim:     &humantask.Claim{Actor: authz.Actor{ID: "bob"}, At: claimedAt},
					CreatedAt: time.Now().UTC(),
				},
				{
					TaskID: "tok-bob-1-" + b.name, InstanceID: "i2", NodeID: "n1",
					State:     humantask.Claimed,
					Claim:     &humantask.Claim{Actor: authz.Actor{ID: "bob"}, At: claimedAt},
					CreatedAt: time.Now().UTC(),
				},
				{
					TaskID: "tok-carol-1-" + b.name, InstanceID: "i3", NodeID: "n1",
					State:     humantask.Claimed,
					Claim:     &humantask.Claim{Actor: authz.Actor{ID: "carol"}, At: claimedAt},
					CreatedAt: time.Now().UTC(),
				},
				{
					TaskID: "tok-uncl-1-" + b.name, InstanceID: "i4", NodeID: "n1",
					State:     humantask.Unclaimed,
					CreatedAt: time.Now().UTC(),
				},
			}
			for _, task := range tasks {
				require.NoError(t, ts.Upsert(t.Context(), task), "%s: seed Upsert %s", b.name, task.TaskID)
			}

			result, err := ts.AssignedTo(t.Context(), "bob")
			require.NoError(t, err, "%s: AssignedTo", b.name)
			require.Len(t, result, 2, "%s: AssignedTo must return 2 tasks for bob", b.name)
			// Must be sorted by task_id ascending.
			assert.Equal(t, "tok-bob-1-"+b.name, result[0].TaskID, "%s: first token", b.name)
			assert.Equal(t, "tok-bob-2-"+b.name, result[1].TaskID, "%s: second token", b.name)

			// carol gets only her task
			carolResult, err := ts.AssignedTo(t.Context(), "carol")
			require.NoError(t, err, "%s: AssignedTo carol", b.name)
			require.Len(t, carolResult, 1, "%s: carol must have 1 task", b.name)
		})

		t.Run("claimable_by_candidate", func(t *testing.T) {
			due := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
			seed := humantask.HumanTask{
				TaskID:      "tok-cand-" + b.name,
				InstanceID:  "ic1",
				NodeID:      "approve",
				State:       humantask.Unclaimed,
				Candidates:  []authz.Actor{{ID: "dave"}},
				Eligibility: authz.AuthzSpec{Roles: []string{"auditor"}},
				CreatedAt:   time.Now().UTC(),
				DueAt:       &due,
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: seed claimable-by-candidate", b.name)

			actor := authz.Actor{ID: "dave", Roles: []string{"developer"}}
			result, err := ts.ClaimableBy(t.Context(), actor)
			require.NoError(t, err, "%s: ClaimableBy by candidate", b.name)

			found := false
			for _, r := range result {
				if r.TaskID == "tok-cand-"+b.name {
					found = true
					break
				}
			}
			assert.True(t, found, "%s: dave must see tok-cand as claimable (by candidate)", b.name)
		})

		t.Run("claimable_by_role", func(t *testing.T) {
			seed := humantask.HumanTask{
				TaskID:      "tok-role-" + b.name,
				InstanceID:  "ir1",
				NodeID:      "review",
				State:       humantask.Unclaimed,
				Candidates:  []authz.Actor{{ID: "other-user"}},
				Eligibility: authz.AuthzSpec{Roles: []string{"manager", "supervisor"}},
				CreatedAt:   time.Now().UTC(),
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: seed claimable-by-role", b.name)

			actor := authz.Actor{ID: "eve", Roles: []string{"supervisor"}}
			result, err := ts.ClaimableBy(t.Context(), actor)
			require.NoError(t, err, "%s: ClaimableBy by role", b.name)

			found := false
			for _, r := range result {
				if r.TaskID == "tok-role-"+b.name {
					found = true
					break
				}
			}
			assert.True(t, found, "%s: eve must see tok-role as claimable (by role)", b.name)
		})

		// audit_columns_round_trip is the ADR-0148 guardrail: the normalized
		// wrkflw_human_task row must carry the FULL claim/completion audit, not a
		// bare claimant id. Each case applies its seeds in order (the second seed
		// exercises the dialect's upsert-conflict SET list) and then re-reads the
		// task by id.
		t.Run("audit_columns_round_trip", func(t *testing.T) {
			claimedAt := time.Date(2026, 7, 27, 9, 30, 15, 123456000, time.UTC)
			completedAt := time.Date(2026, 7, 27, 11, 45, 0, 0, time.UTC)

			richClaimant := authz.Actor{
				ID:         "grace",
				Roles:      []string{"manager", "auditor"},
				Attributes: map[string]any{"email": "grace@example.test", "level": float64(3)},
			}
			richCompleter := authz.Actor{
				ID:         "heidi",
				Roles:      []string{"approver"},
				Attributes: map[string]any{"department": "finance"},
			}

			type testCase struct {
				name   string
				seeds  []humantask.HumanTask
				assert func(t *testing.T, got humantask.HumanTask, err error)
			}

			cases := []testCase{
				{
					name: "fresh insert preserves full claim and completion audit",
					seeds: []humantask.HumanTask{{
						TaskID:     "tok-audit-fresh-" + b.name,
						InstanceID: "ia1",
						NodeID:     "approve",
						State:      humantask.Completed,
						Claim:      &humantask.Claim{Actor: richClaimant, At: claimedAt},
						Completion: &humantask.Completion{
							Actor:   richCompleter,
							At:      completedAt,
							Outcome: "approve",
							Note:    "looks good to me",
						},
						CreatedAt: time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
					}},
					assert: func(t *testing.T, got humantask.HumanTask, err error) {
						require.NoError(t, err)
						require.NotNil(t, got.Claim, "Claim must survive the round trip")
						assert.Equal(t, richClaimant, got.Claim.Actor, "claim actor must keep roles and attributes")
						assert.True(t, got.Claim.At.Equal(claimedAt),
							"claim timestamp must round-trip; got %v want %v", got.Claim.At, claimedAt)
						require.NotNil(t, got.Completion, "Completion must survive the round trip")
						assert.Equal(t, richCompleter, got.Completion.Actor, "completion actor must keep roles and attributes")
						assert.True(t, got.Completion.At.Equal(completedAt),
							"completion timestamp must round-trip; got %v want %v", got.Completion.At, completedAt)
						assert.Equal(t, "approve", got.Completion.Outcome, "completion outcome")
						assert.Equal(t, "looks good to me", got.Completion.Note, "completion note")
					},
				},
				{
					name: "claim present, completion absent",
					seeds: []humantask.HumanTask{{
						TaskID:     "tok-audit-claimonly-" + b.name,
						InstanceID: "ia9",
						NodeID:     "approve",
						State:      humantask.Claimed,
						Claim:      &humantask.Claim{Actor: richClaimant, At: claimedAt},
						CreatedAt:  time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
					}},
					assert: func(t *testing.T, got humantask.HumanTask, err error) {
						require.NoError(t, err)
						require.NotNil(t, got.Claim, "a claimed-but-open task must keep its claim")
						assert.Equal(t, richClaimant, got.Claim.Actor, "claim actor")
						assert.True(t, got.Claim.At.Equal(claimedAt), "claim timestamp")
						assert.Nil(t, got.Completion, "an open task must not fabricate a completion")
					},
				},
				{
					// Presence is keyed on the timestamp, never on the id: an empty
					// claimant id is a legitimate value, and keying on it would
					// resurrect the fabricated/dropped-claim bug of amendment 1 §4.
					name: "claim with an empty actor id still reads back as a claim",
					seeds: []humantask.HumanTask{{
						TaskID:     "tok-audit-anonclaim-" + b.name,
						InstanceID: "ia10",
						NodeID:     "approve",
						State:      humantask.Claimed,
						Claim:      &humantask.Claim{Actor: authz.Actor{Roles: []string{"kiosk"}}, At: claimedAt},
						CreatedAt:  time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
					}},
					assert: func(t *testing.T, got humantask.HumanTask, err error) {
						require.NoError(t, err)
						require.NotNil(t, got.Claim, "presence must key on claimed_at, not on claimed_by")
						assert.Empty(t, got.Claim.Actor.ID, "an empty claimant id must round-trip as empty")
						assert.Equal(t, []string{"kiosk"}, got.Claim.Actor.Roles, "claimant roles")
						assert.True(t, got.Claim.At.Equal(claimedAt), "claim timestamp")
					},
				},
				{
					name: "completion present, claim absent",
					seeds: []humantask.HumanTask{{
						TaskID:     "tok-audit-completiononly-" + b.name,
						InstanceID: "ia11",
						NodeID:     "approve",
						State:      humantask.Completed,
						Completion: &humantask.Completion{
							Actor:   richCompleter,
							At:      completedAt,
							Outcome: "approve",
							Note:    "auto-completed",
						},
						CreatedAt: time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
					}},
					assert: func(t *testing.T, got humantask.HumanTask, err error) {
						require.NoError(t, err)
						assert.Nil(t, got.Claim, "an unclaimed completion must not fabricate a claim")
						require.NotNil(t, got.Completion, "the completion must survive without a claim")
						assert.Equal(t, richCompleter, got.Completion.Actor, "completion actor")
						assert.True(t, got.Completion.At.Equal(completedAt), "completion timestamp")
						assert.Equal(t, "approve", got.Completion.Outcome, "completion outcome")
						assert.Equal(t, "auto-completed", got.Completion.Note, "completion note")
					},
				},
				{
					// Same discriminator rule as the claim: completed_at decides,
					// never completed_by or a non-empty outcome.
					name: "completion with an empty actor id, outcome and note still reads back",
					seeds: []humantask.HumanTask{{
						TaskID:     "tok-audit-anoncompletion-" + b.name,
						InstanceID: "ia12",
						NodeID:     "approve",
						State:      humantask.Completed,
						Completion: &humantask.Completion{At: completedAt},
						CreatedAt:  time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
					}},
					assert: func(t *testing.T, got humantask.HumanTask, err error) {
						require.NoError(t, err)
						require.NotNil(t, got.Completion, "presence must key on completed_at, not on completed_by")
						assert.Empty(t, got.Completion.Actor.ID, "empty completer id round-trips as empty")
						assert.Empty(t, got.Completion.Outcome, "empty outcome round-trips as empty")
						assert.Empty(t, got.Completion.Note, "empty note round-trips as empty")
						assert.True(t, got.Completion.At.Equal(completedAt), "completion timestamp")
					},
				},
				{
					name: "conflict update overwrites audit columns",
					seeds: []humantask.HumanTask{
						{
							TaskID:     "tok-audit-conflict-" + b.name,
							InstanceID: "ia2",
							NodeID:     "approve",
							State:      humantask.Unclaimed,
							CreatedAt:  time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
						},
						{
							TaskID:     "tok-audit-conflict-" + b.name,
							InstanceID: "ia2",
							NodeID:     "approve",
							State:      humantask.Completed,
							Claim:      &humantask.Claim{Actor: richClaimant, At: claimedAt},
							Completion: &humantask.Completion{
								Actor:   richCompleter,
								At:      completedAt,
								Outcome: "reject",
							},
							CreatedAt: time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
						},
					},
					assert: func(t *testing.T, got humantask.HumanTask, err error) {
						require.NoError(t, err)
						require.NotNil(t, got.Claim, "upsert conflict must write the claim column")
						assert.Equal(t, richClaimant, got.Claim.Actor, "claim actor after conflict update")
						assert.True(t, got.Claim.At.Equal(claimedAt), "claim timestamp after conflict update")
						require.NotNil(t, got.Completion, "upsert conflict must write the completion column")
						assert.Equal(t, "reject", got.Completion.Outcome, "completion outcome after conflict update")
						assert.Empty(t, got.Completion.Note, "completion note stays empty when unset")
					},
				},
				{
					name: "nil claim and completion read back as nil",
					seeds: []humantask.HumanTask{
						{
							TaskID:     "tok-audit-cleared-" + b.name,
							InstanceID: "ia3",
							NodeID:     "approve",
							State:      humantask.Claimed,
							Claim:      &humantask.Claim{Actor: richClaimant, At: claimedAt},
							CreatedAt:  time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
						},
						{
							TaskID:     "tok-audit-cleared-" + b.name,
							InstanceID: "ia3",
							NodeID:     "approve",
							State:      humantask.Unclaimed,
							CreatedAt:  time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
						},
					},
					assert: func(t *testing.T, got humantask.HumanTask, err error) {
						require.NoError(t, err)
						assert.Nil(t, got.Claim, "a cleared claim must read back as nil, never fabricated")
						assert.Nil(t, got.Completion, "an absent completion must read back as nil")
					},
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ctx := t.Context()
					for _, seed := range tc.seeds {
						require.NoError(t, ts.Upsert(ctx, seed), "%s: seed Upsert %s", b.name, seed.TaskID)
					}
					got, err := ts.Get(ctx, tc.seeds[len(tc.seeds)-1].TaskID)
					tc.assert(t, got, err)
				})
			}
		})

		// audit_columns_are_queryable_as_sql is the whole point of ADR-0148
		// amendment 2: the audit scalars live in typed columns, so a reporting
		// query filters on them in SQL instead of scanning every row and decoding
		// JSON in Go. Each assertion is a plain SQL predicate over one column.
		t.Run("audit_columns_are_queryable_as_sql", func(t *testing.T) {
			ctx := t.Context()
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)

			claimedAt := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
			claimedID := "tok-query-claimed-" + b.name
			openID := "tok-query-open-" + b.name
			require.NoError(t, ts.Upsert(ctx, humantask.HumanTask{
				TaskID:     claimedID,
				InstanceID: "iq1",
				NodeID:     "approve",
				State:      humantask.Claimed,
				Claim: &humantask.Claim{
					Actor: authz.Actor{ID: "queryable-claimant-" + b.name, Roles: []string{"manager"}},
					At:    claimedAt,
				},
				CreatedAt: time.Date(2026, 7, 28, 7, 0, 0, 0, time.UTC),
			}), "%s: seed claimed row", b.name)
			require.NoError(t, ts.Upsert(ctx, humantask.HumanTask{
				TaskID:     openID,
				InstanceID: "iq1",
				NodeID:     "approve",
				State:      humantask.Unclaimed,
				CreatedAt:  time.Date(2026, 7, 28, 7, 0, 0, 0, time.UTC),
			}), "%s: seed unclaimed row", b.name)

			completedID := "tok-query-completed-" + b.name
			outcome := "queryable-outcome-" + b.name
			completer := "queryable-completer-" + b.name
			require.NoError(t, ts.Upsert(ctx, humantask.HumanTask{
				TaskID:     completedID,
				InstanceID: "iq1",
				NodeID:     "approve",
				State:      humantask.Completed,
				Completion: &humantask.Completion{
					Actor:   authz.Actor{ID: completer, Roles: []string{"approver"}},
					At:      time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
					Outcome: outcome,
					Note:    "signed off",
				},
				CreatedAt: time.Date(2026, 7, 28, 7, 0, 0, 0, time.UTC),
			}), "%s: seed completed row", b.name)

			var gotID string
			require.NoError(t, s.QuerierForTest(ctx).QueryRow(ctx, b.dialect.Rebind(
				`SELECT task_id FROM wrkflw_human_task
				 WHERE instance_id = ? AND claimed_at IS NOT NULL`), "iq1").Scan(&gotID),
				"%s: claimed_at must be a queryable column", b.name)
			assert.Equal(t, claimedID, gotID, "%s: only the claimed row has a claimed_at", b.name)

			// The headline reporting query of ADR-0148 amendment 2: "every task
			// completed with outcome X", answered by an index-friendly predicate
			// rather than by decoding JSON for every row in Go.
			var gotOutcomeID, gotNote, gotCompletedBy string
			require.NoError(t, s.QuerierForTest(ctx).QueryRow(ctx, b.dialect.Rebind(
				`SELECT task_id, completed_by, note FROM wrkflw_human_task WHERE outcome = ?`),
				outcome).Scan(&gotOutcomeID, &gotCompletedBy, &gotNote),
				"%s: outcome must be a queryable column", b.name)
			assert.Equal(t, completedID, gotOutcomeID, "%s: outcome predicate selects the completed row", b.name)
			assert.Equal(t, completer, gotCompletedBy, "%s: completed_by is a queryable scalar", b.name)
			assert.Equal(t, "signed off", gotNote, "%s: note is a queryable scalar", b.name)

			var openCount int
			require.NoError(t, s.QuerierForTest(ctx).QueryRow(ctx, b.dialect.Rebind(
				`SELECT COUNT(*) FROM wrkflw_human_task
				 WHERE instance_id = ? AND completed_at IS NULL`), "iq1").Scan(&openCount),
				"%s: completed_at must be a queryable column", b.name)
			assert.Equal(t, 2, openCount,
				"%s: the claimed and unclaimed rows are the open ones (%s, %s)", b.name, claimedID, openID)
		})

		// A caller-supplied Actor attribute is arbitrary Go data, so the audit
		// encoder must surface an encoding failure instead of silently writing a
		// row with the audit missing.
		t.Run("upsert_rejects_unencodable_audit_payload", func(t *testing.T) {
			unencodable := map[string]any{"callback": func() {}}
			actor := authz.Actor{ID: "mallory", Attributes: unencodable}

			type testCase struct {
				name   string
				seed   humantask.HumanTask
				assert func(t *testing.T, err error)
			}

			cases := []testCase{
				{
					name: "claim actor attribute",
					seed: humantask.HumanTask{
						TaskID:     "tok-audit-badclaim-" + b.name,
						InstanceID: "ia6",
						NodeID:     "approve",
						State:      humantask.Claimed,
						Claim:      &humantask.Claim{Actor: actor, At: time.Now().UTC()},
						CreatedAt:  time.Now().UTC(),
					},
					assert: func(t *testing.T, err error) {
						require.Error(t, err, "an unencodable claim must not be silently dropped")
						assert.Contains(t, err.Error(), "marshal claim_actor", "error must name the failing column")
					},
				},
				{
					name: "completion actor attribute",
					seed: humantask.HumanTask{
						TaskID:     "tok-audit-badcompletion-" + b.name,
						InstanceID: "ia6",
						NodeID:     "approve",
						State:      humantask.Completed,
						Completion: &humantask.Completion{Actor: actor, At: time.Now().UTC()},
						CreatedAt:  time.Now().UTC(),
					},
					assert: func(t *testing.T, err error) {
						require.Error(t, err, "an unencodable completion must not be silently dropped")
						assert.Contains(t, err.Error(), "marshal completion_actor", "error must name the failing column")
					},
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					tc.assert(t, ts.Upsert(t.Context(), tc.seed))
				})
			}
		})

		// A structurally valid but wrongly shaped audit column must fail the read
		// loudly rather than degrade to a silently absent audit.
		// A corrupt audit column must not deny every actor their whole inbox: Get
		// stays fail-loud (the caller named that exact task), but the list queries
		// degrade — they drop the unreadable audit, keep the task actionable, and
		// leave the corruption visible in logs.
		// Presence is keyed on the timestamp, so an audit record without one is
		// incoherent. It also cannot be stored: MySQL DATETIME starts at 1000-01-01,
		// so a zero time.Time reaches the driver as an opaque out-of-range error.
		// TaskStore is a public port, so a consumer building a Claim by hand must
		// get a clear message instead.
		t.Run("upsert_rejects_an_audit_record_with_no_timestamp", func(t *testing.T) {
			type testCase struct {
				name   string
				task   humantask.HumanTask
				assert func(t *testing.T, err error)
			}

			base := func(id string) humantask.HumanTask {
				return humantask.HumanTask{
					TaskID: id, InstanceID: "ia9", NodeID: "approve",
					State: humantask.Claimed, CreatedAt: time.Now().UTC(),
				}
			}

			cases := []testCase{
				{
					name: "claim without a timestamp",
					task: func() humantask.HumanTask {
						task := base("tok-zero-claim-" + b.name)
						task.Claim = &humantask.Claim{Actor: authz.Actor{ID: "u-jane"}}
						return task
					}(),
					assert: func(t *testing.T, err error) {
						require.Error(t, err, "a claim with a zero timestamp must be rejected")
						assert.Contains(t, err.Error(), "claim", "the error must name the offending record")
					},
				},
				{
					name: "completion without a timestamp",
					task: func() humantask.HumanTask {
						task := base("tok-zero-completion-" + b.name)
						task.State = humantask.Completed
						task.Completion = &humantask.Completion{Actor: authz.Actor{ID: "u-jane"}, Outcome: "approve"}
						return task
					}(),
					assert: func(t *testing.T, err error) {
						require.Error(t, err, "a completion with a zero timestamp must be rejected")
						assert.Contains(t, err.Error(), "completion", "the error must name the offending record")
					},
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					tc.assert(t, ts.Upsert(t.Context(), tc.task))
				})
			}
		})

		// The claim invariant (ADR-0183) is enforced on WRITE. For direction R1 it
		// cannot be enforced on read at all — a state='claimed' row whose claimed_at
		// is NULL is indistinguishable from one that was never claimed — so Upsert is
		// the only seam there is.
		//
		// The two leading rows are POSITIVE CONTROLS, and they are load-bearing:
		// without them the rejection rows would prove only that a row which was never
		// written cannot be listed, which no implementation could fail. The second
		// control also pins the ADR-0148 amendment 1 §4 kiosk shape as LEGAL, so a
		// guard that over-rejects an empty claimant fails here too.
		//
		// The follow-on inbox checks use assert.*, not require.*, deliberately:
		// require is FailNow, which with the guard working makes them tautologies and
		// with it broken aborts the subtest before it ever reaches them.
		t.Run("upsert_rejects_an_invalid_task", func(t *testing.T) {
			at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

			contains := func(tasks []humantask.HumanTask, taskID string) bool {
				for _, task := range tasks {
					if task.TaskID == taskID {
						return true
					}
				}
				return false
			}

			// Every fixture declares alice as a candidate AND "mgr" as an eligibility
			// role, so both inbox queries below are asked with an actor the row would
			// genuinely match — absence is then evidence, not a foregone conclusion.
			base := func(id string) humantask.HumanTask {
				return humantask.HumanTask{
					TaskID:      id + "-" + b.name,
					InstanceID:  "inst-inv",
					NodeID:      "approve",
					Eligibility: authz.AuthzSpec{Roles: []string{"mgr"}},
					Candidates:  []authz.Actor{{ID: "alice"}},
					CreatedAt:   at,
				}
			}

			// mustNotBeListed asserts a rejected task reached neither inbox.
			//
			// ⚠ Which of the two queries can actually discriminate differs per shape,
			// and none of them is redundant: an Unclaimed row carrying a claim is
			// double-listed when unguarded (measured: AssignedTo=1 AND ClaimableBy=1);
			// an out-of-range row is caught by AssignedTo only, since ClaimableBy
			// filters on state='unclaimed'; a Claimed row with no claim reaches
			// NEITHER inbox even unguarded — for that shape the Get assertion above is
			// the sole discriminator.
			mustNotBeListed := func(t *testing.T, taskID string) {
				t.Helper()

				assigned, err := ts.AssignedTo(t.Context(), "alice")
				assert.NoError(t, err, "%s: AssignedTo", b.name)
				assert.False(t, contains(assigned, taskID),
					"%s: a rejected task must not reach AssignedTo", b.name)

				claimable, err := ts.ClaimableBy(t.Context(), authz.Actor{ID: "alice", Roles: []string{"mgr"}})
				assert.NoError(t, err, "%s: ClaimableBy", b.name)
				assert.False(t, contains(claimable, taskID),
					"%s: a rejected task must not reach ClaimableBy", b.name)
			}

			rejected := func(t *testing.T, task humantask.HumanTask, err error) {
				assert.ErrorIs(t, err, humantask.ErrInvalidTask,
					"%s: a contradictory task must be refused; got %v", b.name, err)

				_, getErr := ts.Get(t.Context(), task.TaskID)
				assert.ErrorIs(t, getErr, humantask.ErrTaskNotFound,
					"%s: a rejected Upsert must persist nothing", b.name)

				mustNotBeListed(t, task.TaskID)
			}

			type testCase struct {
				name   string
				task   humantask.HumanTask
				assert func(t *testing.T, task humantask.HumanTask, err error)
			}

			cases := []testCase{
				{
					name: "control: a legally claimed task is stored and listed",
					task: func() humantask.HumanTask {
						task := base("tok-legal")
						task.State = humantask.Claimed
						task.Claim = &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at}
						return task
					}(),
					assert: func(t *testing.T, task humantask.HumanTask, err error) {
						require.NoError(t, err, "%s: a legal shape must persist", b.name)

						assigned, err := ts.AssignedTo(t.Context(), "alice")
						require.NoError(t, err, "%s: AssignedTo", b.name)
						assert.True(t, contains(assigned, task.TaskID),
							"%s: control — a legally Claimed task MUST appear in AssignedTo", b.name)
					},
				},
				{
					// ADR-0148 amendment 1 §4: the kiosk claimant is anonymous but
					// carries roles. Claimed + an EMPTY claimant is legal, and a
					// design round that rejected it was reversed.
					name: "control: the kiosk shape (claimed, empty claimant) stays legal",
					task: func() humantask.HumanTask {
						task := base("tok-kiosk")
						task.State = humantask.Claimed
						task.Claim = &humantask.Claim{Actor: authz.Actor{Roles: []string{"kiosk"}}, At: at}
						return task
					}(),
					assert: func(t *testing.T, task humantask.HumanTask, err error) {
						require.NoError(t, err, "%s: the kiosk shape must persist", b.name)

						got, getErr := ts.Get(t.Context(), task.TaskID)
						require.NoError(t, getErr, "%s: the kiosk shape must read back", b.name)
						require.NotNil(t, got.Claim, "%s: the kiosk claim must survive", b.name)
						assert.Empty(t, got.Claim.Actor.ID, "%s: an empty claimant stays empty", b.name)
						assert.Equal(t, []string{"kiosk"}, got.Claim.Actor.Roles, "%s: claimant roles", b.name)
					},
				},
				{
					name: "claimed without a claim is rejected",
					task: func() humantask.HumanTask {
						task := base("tok-inv-claimednil")
						task.State = humantask.Claimed
						return task
					}(),
					assert: rejected,
				},
				{
					name: "unclaimed carrying a claim is rejected",
					task: func() humantask.HumanTask {
						task := base("tok-inv-unclaimedclaim")
						task.State = humantask.Unclaimed
						task.Claim = &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at}
						return task
					}(),
					assert: rejected,
				},
				{
					name: "an out-of-range state is rejected",
					task: func() humantask.HumanTask {
						task := base("tok-inv-badstate")
						task.State = humantask.TaskState(99)
						task.Claim = &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at}
						return task
					}(),
					assert: rejected,
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					tc.assert(t, tc.task, ts.Upsert(t.Context(), tc.task))
				})
			}
		})

		// A degraded row logs a WARN and is otherwise invisible forever. Corruption
		// that silently persists is an operational problem, so the drop is also
		// counted, making it alertable rather than something a human must notice in
		// a log stream.
		t.Run("a_degraded_audit_column_increments_the_drop_counter", func(t *testing.T) {
			ctx := t.Context()
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

			metered, err := store.NewHumanTaskStore(b.conn, b.dialect, store.WithHumanTaskMeterProvider(mp))
			require.NoError(t, err)

			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)

			taskID := "tok-metric-" + b.name
			require.NoError(t, metered.Upsert(ctx, humantask.HumanTask{
				TaskID: taskID, InstanceID: "ia10", NodeID: "approve",
				State:       humantask.Claimed,
				Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
				Claim:       &humantask.Claim{Actor: authz.Actor{ID: "u-jane"}, At: time.Now().UTC()},
				CreatedAt:   time.Now().UTC(),
			}), "%s: seed", b.name)
			t.Cleanup(func() {
				cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, _ = s.QuerierForTest(cctx).Exec(cctx,
					b.dialect.Rebind(`DELETE FROM wrkflw_human_task WHERE task_id = ?`), taskID)
			})
			_, err = s.QuerierForTest(ctx).Exec(ctx,
				b.dialect.Rebind(`UPDATE wrkflw_human_task SET claim_actor = ? WHERE task_id = ?`),
				`{"roles":42}`, taskID)
			require.NoError(t, err, "%s: corrupt claim_actor", b.name)

			got, err := metered.AssignedTo(ctx, "u-jane")
			require.NoError(t, err, "%s: the list query must still succeed", b.name)
			require.Len(t, got, 1)
			require.NotNil(t, got[0].Claim,
				"%s: the claim is rebuilt from its scalar columns, not blanked", b.name)
			assert.Equal(t, authz.Actor{ID: "u-jane"}, got[0].Claim.Actor,
				"%s: only the unreadable roles/attributes remainder is dropped", b.name)

			var rm metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(ctx, &rm))
			assert.True(t, htFindCounter(rm, "wrkflw_human_task_audit_drops_total") >= 1,
				"%s: a dropped audit column must be counted", b.name)
		})

		t.Run("list_queries_degrade_around_an_undecodable_audit_column", func(t *testing.T) {
			ctx := t.Context()
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)

			// The audit scalars are typed columns now, so the only decodable —
			// and therefore degradable — audit data left is the actor remainder,
			// which exists only on a row that actually carries an audit. Hence a
			// claimed pair queried through AssignedTo rather than an unclaimed
			// pair through ClaimableBy.
			claimant := "u-degrade-" + b.name
			healthy := "tok-degrade-ok-" + b.name
			corrupt := "tok-degrade-bad-" + b.name
			for _, id := range []string{healthy, corrupt} {
				require.NoError(t, ts.Upsert(ctx, humantask.HumanTask{
					TaskID:      id,
					InstanceID:  "ia8",
					NodeID:      "approve",
					State:       humantask.Claimed,
					Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
					Claim: &humantask.Claim{
						Actor: authz.Actor{ID: claimant, Roles: []string{"manager"}},
						At:    time.Date(2026, 7, 28, 5, 0, 0, 0, time.UTC),
					},
					CreatedAt: time.Now().UTC(),
				}), "%s: seed %s", b.name, id)
			}
			t.Cleanup(func() {
				cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, id := range []string{healthy, corrupt} {
					_, _ = s.QuerierForTest(cctx).Exec(cctx,
						b.dialect.Rebind(`DELETE FROM wrkflw_human_task WHERE task_id = ?`), id)
				}
			})
			// Valid JSON of the WRONG SHAPE, not malformed text: Postgres (JSONB) and
			// MySQL (JSON) validate syntax at the column, so only a shape mismatch —
			// what a bad migration or a future struct change produces — can reach the
			// decoder on every dialect.
			_, err = s.QuerierForTest(ctx).Exec(ctx,
				b.dialect.Rebind(`UPDATE wrkflw_human_task SET claim_actor = ? WHERE task_id = ?`),
				`{"roles":123}`, corrupt)
			require.NoError(t, err, "%s: corrupt the claim_actor column", b.name)

			// Get names the task, so it must still fail loudly.
			_, err = ts.Get(ctx, corrupt)
			require.Error(t, err, "%s: Get must stay fail-loud", b.name)

			// AssignedTo scans every row claimed by the actor: one bad row must not
			// sink the whole list.
			assigned, err := ts.AssignedTo(ctx, claimant)
			require.NoError(t, err, "%s: one corrupt row must not fail the inbox query", b.name)

			byID := make(map[string]humantask.HumanTask, len(assigned))
			for _, task := range assigned {
				byID[task.TaskID] = task
			}
			require.Contains(t, byID, healthy, "%s: the healthy task must still be listed", b.name)
			require.Contains(t, byID, corrupt, "%s: the corrupt task must still be actionable", b.name)
			require.NotNil(t, byID[healthy].Claim, "%s: the healthy row keeps its claim", b.name)
			require.NotNil(t, byID[corrupt].Claim,
				"%s: a row AssignedTo matched on claimed_by must not come back looking unclaimed", b.name)
			assert.Equal(t, authz.Actor{ID: claimant}, byID[corrupt].Claim.Actor,
				"%s: the claimant is rebuilt from claimed_by; only the unreadable remainder is dropped", b.name)
			assert.True(t, byID[corrupt].Claim.At.Equal(time.Date(2026, 7, 28, 5, 0, 0, 0, time.UTC)),
				"%s: claimed_at is its own column and survives the corrupt remainder", b.name)
		})

		// ADR-0148's degrade must not trade one silent corruption for another. A
		// task returned with State Claimed/Completed but a nil Claim/Completion
		// reads as "never claimed" to every consumer — the exact invariant
		// [humantask.HumanTask] documents against — so an inbox would offer a Claim
		// action that cannot succeed and any task.Claim.Actor.ID would nil-deref.
		// The degrade therefore drops only the column that actually failed and
		// rebuilds the record around the scalar columns that did decode.
		t.Run("degrade_drops_only_the_failing_column", func(t *testing.T) {
			claimant := authz.Actor{
				ID:         "u-invariant-" + b.name,
				Roles:      []string{"manager"},
				Attributes: map[string]any{"region": "EU"},
			}
			completer := authz.Actor{
				ID:         "u-invariant-done-" + b.name,
				Roles:      []string{"director"},
				Attributes: map[string]any{"region": "APAC"},
			}
			claimedAt := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
			completedAt := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)

			type testCase struct {
				name    string
				column  string
				payload string
				assert  func(t *testing.T, got humantask.HumanTask)
			}

			cases := []testCase{
				{
					name:    "corrupt completion actor leaves the claim whole",
					column:  "completion_actor",
					payload: `{"roles":123}`,
					assert: func(t *testing.T, got humantask.HumanTask) {
						require.NotNil(t, got.Claim, "an untouched column must not be blanked")
						assert.Equal(t, claimant, got.Claim.Actor, "the claim actor survives whole")
						assert.True(t, got.Claim.At.Equal(claimedAt), "the claim timestamp survives")

						require.NotNil(t, got.Completion,
							"State says completed, so Completion must not come back nil")
						assert.Equal(t, authz.Actor{ID: completer.ID}, got.Completion.Actor,
							"only the unreadable roles/attributes remainder is lost")
						assert.True(t, got.Completion.At.Equal(completedAt), "completed_at is its own column")
						assert.Equal(t, "approve", got.Completion.Outcome, "outcome is its own column")
						assert.Equal(t, "looks good", got.Completion.Note, "note is its own column")
					},
				},
				{
					name:    "corrupt claim actor leaves the completion whole",
					column:  "claim_actor",
					payload: `{"attributes":"not-an-object"}`,
					assert: func(t *testing.T, got humantask.HumanTask) {
						require.NotNil(t, got.Completion, "an untouched column must not be blanked")
						assert.Equal(t, completer, got.Completion.Actor, "the completion actor survives whole")
						assert.True(t, got.Completion.At.Equal(completedAt), "the completion timestamp survives")

						require.NotNil(t, got.Claim,
							"a row AssignedTo matched on claimed_by must not come back looking unclaimed")
						assert.Equal(t, authz.Actor{ID: claimant.ID}, got.Claim.Actor,
							"the claimant is rebuilt from the claimed_by column the query matched on")
						assert.True(t, got.Claim.At.Equal(claimedAt), "claimed_at is its own column")
					},
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ctx := t.Context()
					reader := sdkmetric.NewManualReader()
					mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
					metered, err := store.NewHumanTaskStore(b.conn, b.dialect,
						store.WithHumanTaskMeterProvider(mp))
					require.NoError(t, err)

					s, err := store.New(b.conn, b.dialect)
					require.NoError(t, err)

					taskID := "tok-invariant-" + tc.column + "-" + b.name
					require.NoError(t, ts.Upsert(ctx, humantask.HumanTask{
						TaskID:      taskID,
						InstanceID:  "ia15",
						NodeID:      "approve",
						State:       humantask.Completed,
						Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
						Claim:       &humantask.Claim{Actor: claimant, At: claimedAt},
						Completion: &humantask.Completion{
							Actor:   completer,
							At:      completedAt,
							Outcome: "approve",
							Note:    "looks good",
						},
						CreatedAt: time.Now().UTC(),
					}), "%s: seed audit-invariant row", b.name)

					// Cleanup needs its own context: t.Context() is already cancelled
					// by the time cleanups run.
					t.Cleanup(func() {
						cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						_, delErr := s.QuerierForTest(cctx).Exec(cctx,
							b.dialect.Rebind(`DELETE FROM wrkflw_human_task WHERE task_id = ?`), taskID)
						require.NoError(t, delErr, "%s: drop corrupt row %s", b.name, taskID)
					})

					// Valid JSON of the WRONG SHAPE: Postgres (JSONB) and MySQL (JSON)
					// validate syntax at the column, so only a shape mismatch reaches
					// the decoder on all three dialects.
					_, err = s.QuerierForTest(ctx).Exec(ctx, b.dialect.Rebind(
						`UPDATE wrkflw_human_task SET `+tc.column+` = ? WHERE task_id = ?`),
						tc.payload, taskID)
					require.NoError(t, err, "%s: corrupt the %s column", b.name, tc.column)

					// The point read named this exact task, so it still fails loud.
					_, err = metered.Get(ctx, taskID)
					require.Error(t, err, "%s: Get must stay fail-loud", b.name)
					assert.Contains(t, err.Error(), tc.column,
						"%s: the error must name the failing column", b.name)

					assigned, err := metered.AssignedTo(ctx, claimant.ID)
					require.NoError(t, err, "%s: the list query must degrade, not fail", b.name)
					require.Len(t, assigned, 1, "%s: the degraded row stays listed", b.name)
					got := assigned[0]
					require.Equal(t, taskID, got.TaskID, "%s: listed task", b.name)
					assert.Equal(t, humantask.Completed, got.State, "%s: state is preserved", b.name)
					tc.assert(t, got)

					// The drop stays alertable rather than living only in a log line.
					var rm metricdata.ResourceMetrics
					require.NoError(t, reader.Collect(ctx, &rm))
					assert.GreaterOrEqual(t,
						htFindCounter(rm, "wrkflw_human_task_audit_drops_total"), int64(1),
						"%s: a dropped audit column must still be counted", b.name)
				})
			}
		})

		t.Run("get_reports_undecodable_audit_column", func(t *testing.T) {
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)

			// Only a row that actually carries an audit has an actor remainder to
			// decode, so each case seeds the lifecycle event whose column it then
			// corrupts.
			claimedSeed := func(taskID string) humantask.HumanTask {
				return humantask.HumanTask{
					TaskID:     taskID,
					InstanceID: "ia7",
					NodeID:     "approve",
					State:      humantask.Claimed,
					Claim: &humantask.Claim{
						Actor: authz.Actor{ID: "u-corrupt"},
						At:    time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC),
					},
					CreatedAt: time.Now().UTC(),
				}
			}

			type testCase struct {
				name    string
				column  string
				payload string
				seed    func(taskID string) humantask.HumanTask
				assert  func(t *testing.T, err error)
			}

			cases := []testCase{
				{
					name:    "claim_actor",
					column:  "claim_actor",
					payload: `{"roles":42}`,
					seed:    claimedSeed,
					assert: func(t *testing.T, err error) {
						require.Error(t, err, "an undecodable claim actor column must error")
						assert.Contains(t, err.Error(), "unmarshal claim_actor", "error must name the failing column")
					},
				},
				{
					name:    "completion_actor",
					column:  "completion_actor",
					payload: `{"attributes":"not-an-object"}`,
					seed: func(taskID string) humantask.HumanTask {
						return humantask.HumanTask{
							TaskID:     taskID,
							InstanceID: "ia7",
							NodeID:     "approve",
							State:      humantask.Completed,
							Completion: &humantask.Completion{
								Actor: authz.Actor{ID: "u-corrupt"},
								At:    time.Date(2026, 7, 28, 4, 30, 0, 0, time.UTC),
							},
							CreatedAt: time.Now().UTC(),
						}
					},
					assert: func(t *testing.T, err error) {
						require.Error(t, err, "an undecodable completion actor column must error")
						assert.Contains(t, err.Error(), "unmarshal completion_actor", "error must name the failing column")
					},
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ctx := t.Context()
					taskID := "tok-audit-corrupt-" + tc.column + "-" + b.name
					require.NoError(t, ts.Upsert(ctx, tc.seed(taskID)),
						"%s: seed corrupt-column row", b.name)

					// A corrupt row poisons every list query that scans it, so it must
					// not outlive this case. Cleanup needs its own context: t.Context()
					// is already cancelled by the time cleanups run.
					t.Cleanup(func() {
						cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						_, delErr := s.QuerierForTest(cleanupCtx).Exec(cleanupCtx,
							b.dialect.Rebind(`DELETE FROM wrkflw_human_task WHERE task_id = ?`), taskID)
						require.NoError(t, delErr, "%s: drop corrupt row %s", b.name, taskID)
					})

					// Valid JSON, wrong shape: the decoder — not the column — must
					// reject it, on every dialect.
					q := s.QuerierForTest(ctx)
					_, execErr := q.Exec(ctx, b.dialect.Rebind(
						`UPDATE wrkflw_human_task SET `+tc.column+` = ? WHERE task_id = ?`),
						tc.payload, taskID)
					require.NoError(t, execErr, "%s: corrupt the %s column", b.name, tc.column)

					_, getErr := ts.Get(ctx, taskID)
					tc.assert(t, getErr)
				})
			}
		})

		// A NULL actor remainder alongside a present timestamp is the shape a row
		// takes when the claimant carried neither roles nor attributes. The
		// reconstruction must be faithful — id-only actor, real timestamp — and
		// must not be mistaken for an absent claim.
		t.Run("audit_reconstructs_from_a_null_actor_remainder", func(t *testing.T) {
			ctx := t.Context()
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)

			claimedAt := time.Date(2026, 7, 28, 3, 0, 0, 0, time.UTC)
			taskID := "tok-audit-nullactor-" + b.name
			require.NoError(t, ts.Upsert(ctx, humantask.HumanTask{
				TaskID:     taskID,
				InstanceID: "ia13",
				NodeID:     "approve",
				State:      humantask.Claimed,
				Claim:      &humantask.Claim{Actor: authz.Actor{ID: "u-plain"}, At: claimedAt},
				CreatedAt:  time.Now().UTC(),
			}), "%s: seed null-remainder row", b.name)

			_, err = s.QuerierForTest(ctx).Exec(ctx, b.dialect.Rebind(
				`UPDATE wrkflw_human_task SET claim_actor = NULL WHERE task_id = ?`), taskID)
			require.NoError(t, err, "%s: null the claim_actor column", b.name)

			got, err := ts.Get(ctx, taskID)
			require.NoError(t, err, "%s: a NULL remainder is not a decode failure", b.name)
			require.NotNil(t, got.Claim, "%s: claimed_at alone proves the claim", b.name)
			assert.Equal(t, authz.Actor{ID: "u-plain"}, got.Claim.Actor,
				"%s: an id-only actor must round-trip without roles or attributes", b.name)
			assert.True(t, got.Claim.At.Equal(claimedAt), "%s: claim timestamp", b.name)
		})

		// The SQLite TEXT timestamp is the one audit column left that can hold
		// garbage — Postgres (TIMESTAMPTZ) and MySQL (DATETIME) type-check it at
		// the column. Its decode failure degrades to the narrowest possible loss:
		// a non-NULL claimed_at still proves the claim happened, so the list query
		// keeps the claim and drops only the unreadable instant. The point read
		// fails loudly, as always.
		t.Run("unparseable_audit_timestamp_degrades_like_the_actor_remainder", func(t *testing.T) {
			if !b.dialect.TimestampsAsText() {
				t.Skipf("%s type-checks timestamps at the column: an unparseable claimed_at is unrepresentable", b.name)
			}
			ctx := t.Context()
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)

			claimant := "u-badtime-" + b.name
			taskID := "tok-audit-badtime-" + b.name
			require.NoError(t, ts.Upsert(ctx, humantask.HumanTask{
				TaskID:     taskID,
				InstanceID: "ia14",
				NodeID:     "approve",
				State:      humantask.Claimed,
				Claim: &humantask.Claim{
					Actor: authz.Actor{ID: claimant},
					At:    time.Date(2026, 7, 28, 3, 30, 0, 0, time.UTC),
				},
				CreatedAt: time.Now().UTC(),
			}), "%s: seed bad-timestamp row", b.name)

			_, err = s.QuerierForTest(ctx).Exec(ctx, b.dialect.Rebind(
				`UPDATE wrkflw_human_task SET claimed_at = ? WHERE task_id = ?`), "not-a-time", taskID)
			require.NoError(t, err, "%s: corrupt the claimed_at column", b.name)

			_, err = ts.Get(ctx, taskID)
			require.Error(t, err, "%s: Get must stay fail-loud", b.name)
			assert.Contains(t, err.Error(), "claimed_at", "%s: error must name the failing column", b.name)

			assigned, err := ts.AssignedTo(ctx, claimant)
			require.NoError(t, err, "%s: an unparseable timestamp must not sink the list query", b.name)
			require.Len(t, assigned, 1, "%s: the row stays listed", b.name)
			assert.Equal(t, taskID, assigned[0].TaskID, "%s: listed task", b.name)
			require.NotNil(t, assigned[0].Claim,
				"%s: a non-NULL claimed_at proves the claim even when its instant is unreadable", b.name)
			assert.Equal(t, authz.Actor{ID: claimant}, assigned[0].Claim.Actor,
				"%s: the claimant survives — only the instant was unreadable", b.name)
			assert.True(t, assigned[0].Claim.At.IsZero(),
				"%s: the unreadable instant degrades to the zero time, not to a fabricated one", b.name)
		})

		// An empty actorID identifies no actor, and claimed_by is NOT NULL
		// DEFAULT '', so a bare `WHERE claimed_by = ?` matches every unclaimed row:
		// an unauthenticated or unresolved actor id would come back holding every
		// task nobody has picked up. [humantask.MemTaskStore] guards the same way,
		// so both implementations of the port must answer identically.
		t.Run("assigned_to_rejects_an_empty_actor_id", func(t *testing.T) {
			ctx := t.Context()
			require.NoError(t, ts.Upsert(ctx, humantask.HumanTask{
				TaskID:      "tok-empty-actor-" + b.name,
				InstanceID:  "ia16",
				NodeID:      "approve",
				State:       humantask.Unclaimed,
				Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
				CreatedAt:   time.Now().UTC(),
			}), "%s: seed an unclaimed row, whose claimed_by is the empty string", b.name)

			got, err := ts.AssignedTo(ctx, "")
			require.NoError(t, err, "%s: an empty actor id is not an error, it simply matches nothing", b.name)
			assert.Empty(t, got, "%s: an empty actor id must never dump the unclaimed rows", b.name)
		})

		t.Run("assigned_to_preserves_claim_audit", func(t *testing.T) {
			claimedAt := time.Date(2026, 7, 27, 14, 5, 0, 0, time.UTC)
			claimant := authz.Actor{
				ID:         "ivan",
				Roles:      []string{"reviewer"},
				Attributes: map[string]any{"region": "EU"},
			}
			seed := humantask.HumanTask{
				TaskID:     "tok-audit-assigned-" + b.name,
				InstanceID: "ia4",
				NodeID:     "review",
				State:      humantask.Claimed,
				Claim:      &humantask.Claim{Actor: claimant, At: claimedAt},
				CreatedAt:  time.Date(2026, 7, 27, 14, 0, 0, 0, time.UTC),
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: seed assigned-to audit", b.name)

			result, err := ts.AssignedTo(t.Context(), "ivan")
			require.NoError(t, err, "%s: AssignedTo", b.name)
			require.Len(t, result, 1, "%s: claimed_by must stay queryable for the rich claim", b.name)
			require.NotNil(t, result[0].Claim, "%s: AssignedTo row must carry the claim", b.name)
			assert.Equal(t, claimant, result[0].Claim.Actor, "%s: AssignedTo claim actor", b.name)
			assert.True(t, result[0].Claim.At.Equal(claimedAt), "%s: AssignedTo claim timestamp", b.name)
		})

		t.Run("claimable_by_preserves_candidate_audit", func(t *testing.T) {
			candidate := authz.Actor{
				ID:         "judy",
				Roles:      []string{"clerk"},
				Attributes: map[string]any{"office": "berlin"},
			}
			seed := humantask.HumanTask{
				TaskID:      "tok-audit-claimable-" + b.name,
				InstanceID:  "ia5",
				NodeID:      "review",
				State:       humantask.Unclaimed,
				Candidates:  []authz.Actor{candidate},
				Eligibility: authz.AuthzSpec{Roles: []string{"clerk"}},
				CreatedAt:   time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC),
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: seed claimable-by audit", b.name)

			result, err := ts.ClaimableBy(t.Context(), authz.Actor{ID: "judy"})
			require.NoError(t, err, "%s: ClaimableBy", b.name)

			var found *humantask.HumanTask
			for i := range result {
				if result[i].TaskID == "tok-audit-claimable-"+b.name {
					found = &result[i]
					break
				}
			}
			require.NotNil(t, found, "%s: judy must see her unclaimed task", b.name)
			assert.Equal(t, []authz.Actor{candidate}, found.Candidates, "%s: ClaimableBy candidates", b.name)
			assert.Nil(t, found.Claim, "%s: unclaimed row must not fabricate a claim", b.name)
			assert.Nil(t, found.Completion, "%s: unclaimed row must not fabricate a completion", b.name)
		})

		t.Run("claimable_by_excludes_non_unclaimed", func(t *testing.T) {
			// Seed a claimed task that eve would otherwise be eligible for.
			seed := humantask.HumanTask{
				TaskID:     "tok-claimed-" + b.name,
				InstanceID: "icl1",
				NodeID:     "review",
				State:      humantask.Claimed,
				Claim: &humantask.Claim{
					Actor: authz.Actor{ID: "frank"},
					At:    time.Date(2026, 7, 28, 6, 30, 0, 0, time.UTC),
				},
				Candidates:  []authz.Actor{{ID: "eve"}},
				Eligibility: authz.AuthzSpec{Roles: []string{"supervisor"}},
				CreatedAt:   time.Now().UTC(),
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: seed claimed-not-claimable", b.name)

			actor := authz.Actor{ID: "eve", Roles: []string{"supervisor"}}
			result, err := ts.ClaimableBy(t.Context(), actor)
			require.NoError(t, err, "%s: ClaimableBy excludes claimed", b.name)

			for _, r := range result {
				assert.NotEqual(t, "tok-claimed-"+b.name, r.TaskID,
					"%s: claimed task must NOT appear in ClaimableBy", b.name)
			}
		})
	})
}

// htFindCounter sums an int64 counter across all its data points, or returns -1
// when the instrument was never recorded.
func htFindCounter(rm metricdata.ResourceMetrics, name string) int64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			var total int64
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
			return total
		}
	}
	return -1
}
