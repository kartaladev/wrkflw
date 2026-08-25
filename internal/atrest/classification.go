package atrest

// The six at-rest sensitivity classes (ADR-0187 §"The classification").
// Classification is dialect-invariant: a column's class is a judgement
// about its logical role, not its physical type, so the same class
// applies across postgres/mysql/sqlite.
const (
	// ClassFreeform is unstructured, application-authored content:
	// JSON blobs, notes, error text, snapshots. No shape is assumed.
	ClassFreeform Class = "freeform"
	// ClassActor identifies a human principal.
	ClassActor Class = "actor"
	// ClassPolicy is authorization policy data at rest.
	ClassPolicy Class = "policy"
	// ClassReference is an identifier or foreign-key-shaped value:
	// instance/definition/task ids, topic names, dedup keys, a worker
	// lease owner — not a person.
	ClassReference Class = "reference"
	// ClassScalar is a small structured value: a status, a kind, a
	// count, a version number.
	ClassScalar Class = "scalar"
	// ClassTimestamp is a point in time.
	ClassTimestamp Class = "timestamp"
)

// PolicyLocation names one place a wrkflw deployment holds authorization
// policy at rest. Column is empty when the whole table is such a place.
//
// Detail is consumer-facing prose published verbatim by Render: it must say
// WHAT policy sits there and, where the column's class does not by itself
// reveal it, why the column is nonetheless a policy location.
type PolicyLocation struct {
	Table  string
	Column string
	Detail string
}

// PolicyAtRestLocations is the stated, machine-checked enumeration behind the
// generated document's "authorization policy is durable at rest in N places"
// sentence. The count is DERIVED from this slice; it is never retyped.
//
// ⚠ It is deliberately NOT the same thing as "the columns classed
// ClassPolicy". TWO of the entries are classed `freeform`, and that is the whole
// point of the enumeration — a freeform column can carry policy without the
// class ever saying so, and the completeness guard in render.go only checks
// ClassPolicy columns, so neither is discoverable by machine:
//
//   - wrkflw_instances.snapshot holds the serialized instance state, and
//     engine.InstanceState.Tasks carries every in-flight human task whose
//     Eligibility is a full authz.AuthzSpec (backlog 141).
//   - wrkflw_definitions.definition holds the marshalled process definition
//     (internal/persistence/store/definitions.go PutDefinition json.Marshals the
//     whole ProcessDefinition into it), and
//
// definition/model/node_wire.go declares eligible_roles / eligible_privileges /
// eligible_expr on NodeWire — so every user task's eligibility rule is inside
// that JSON. Executed: marshalling a definition whose user task carries all
// three options emits
// `"eligible_roles":["manager"],"eligible_privileges":["approve:invoice"],"eligible_expr":"vars.amount < 100"`.
// A consumer who encrypts only the `policy`-classed columns therefore leaves
// per-node eligibility rules in the clear — the precise harm ADR-0187's Context
// names.
//
// The column keeps class `freeform` rather than being reclassified `policy`:
// one column carries one class, its logical role is "the serialized definition"
// (arbitrary consumer-authored content, possibly PII), and `freeform`'s
// description is the warning that matters most for it. The policy content is
// disclosed by naming the column here instead, which is what a reader can act
// on. Render fails closed if an entry stops resolving, and if any ClassPolicy
// column is not covered by an entry.
var PolicyAtRestLocations = []PolicyLocation{
	{
		Table: "casbin_rule",
		Detail: "holds the deployment's casbin policy rules verbatim, one rule per row " +
			"(class `policy`)",
	},
	{
		Table: "wrkflw_human_task", Column: "eligibility",
		Detail: "holds the per-task eligibility rule the four `Authorize` sites in " +
			"`runtime/task/service.go` evaluate (class `policy`)",
	},
	{
		Table: "wrkflw_instances", Column: "snapshot",
		Detail: "is class `freeform` because it holds the serialized instance state — but " +
			"engine.InstanceState.Tasks carries every in-flight human task, and each one's " +
			"`Eligibility` is a full authz.AuthzSpec, so the eligibility rule governing live " +
			"work is inside that JSON (backlog 141)",
	},
	{
		Table: "wrkflw_definitions", Column: "definition",
		Detail: "is class `freeform` because it holds the whole serialized process " +
			"definition — but every node's `eligible_roles`, `eligible_privileges` and " +
			"`eligible_expr` are serialized INSIDE that JSON, so encrypting only the " +
			"`policy`-classed columns leaves per-node eligibility rules in the clear",
	},
}

// Classification states, for every column this module's embedded
// migrations declare, which of the six classes it belongs to. It is
// keyed by ColumnKey rather than a bare column name because exactly one
// column name in the schema carries two different classes:
// wrkflw_human_task.claimed_by (ClassActor, a human principal) and
// wrkflw_call_links.claimed_by (ClassReference, a worker lease owner —
// E7) — a map[string]Class would merge them and mis-state one.
//
// This is a stated judgement, not a derivation: it is transcribed
// verbatim from docs/specs/2026-08-22-at-rest-posture.md § "The
// classification", which itself survived two adversarial audit rounds.
// Do not re-derive or second-guess any entry here — see that section
// for the reasoning behind each. TestClassificationCoversTheSchemaExactly
// is the completeness and staleness guard that keeps this map honest
// against the schema the migrations actually declare, and
// TestClassificationPerClassCounts pins the per-class totals (E6).
var Classification = map[ColumnKey]Class{
	// wrkflw_instances (9)
	{Table: "wrkflw_instances", Column: "instance_id"}: ClassReference,
	{Table: "wrkflw_instances", Column: "def_id"}:      ClassReference,
	{Table: "wrkflw_instances", Column: "def_version"}: ClassScalar,
	{Table: "wrkflw_instances", Column: "status"}:      ClassScalar,
	{Table: "wrkflw_instances", Column: "version"}:     ClassScalar,
	{Table: "wrkflw_instances", Column: "snapshot"}:    ClassFreeform,
	{Table: "wrkflw_instances", Column: "started_at"}:  ClassTimestamp,
	{Table: "wrkflw_instances", Column: "ended_at"}:    ClassTimestamp,
	{Table: "wrkflw_instances", Column: "updated_at"}:  ClassTimestamp,

	// wrkflw_journal (6)
	{Table: "wrkflw_journal", Column: "instance_id"}: ClassReference,
	{Table: "wrkflw_journal", Column: "seq"}:         ClassScalar,
	{Table: "wrkflw_journal", Column: "kind"}:        ClassScalar,
	{Table: "wrkflw_journal", Column: "trigger"}:     ClassFreeform,
	{Table: "wrkflw_journal", Column: "occurred_at"}: ClassTimestamp,
	{Table: "wrkflw_journal", Column: "applied_at"}:  ClassTimestamp,

	// wrkflw_outbox (12)
	{Table: "wrkflw_outbox", Column: "id"}:              ClassScalar,
	{Table: "wrkflw_outbox", Column: "status"}:          ClassScalar,
	{Table: "wrkflw_outbox", Column: "retry_count"}:     ClassScalar,
	{Table: "wrkflw_outbox", Column: "instance_id"}:     ClassReference,
	{Table: "wrkflw_outbox", Column: "topic"}:           ClassReference,
	{Table: "wrkflw_outbox", Column: "dedup_key"}:       ClassReference,
	{Table: "wrkflw_outbox", Column: "definition_ref"}:  ClassReference,
	{Table: "wrkflw_outbox", Column: "payload"}:         ClassFreeform,
	{Table: "wrkflw_outbox", Column: "last_error"}:      ClassFreeform,
	{Table: "wrkflw_outbox", Column: "created_at"}:      ClassTimestamp,
	{Table: "wrkflw_outbox", Column: "published_at"}:    ClassTimestamp,
	{Table: "wrkflw_outbox", Column: "next_attempt_at"}: ClassTimestamp,

	// wrkflw_definitions (4)
	{Table: "wrkflw_definitions", Column: "def_id"}:     ClassReference,
	{Table: "wrkflw_definitions", Column: "version"}:    ClassScalar,
	{Table: "wrkflw_definitions", Column: "definition"}: ClassFreeform,
	{Table: "wrkflw_definitions", Column: "created_at"}: ClassTimestamp,

	// wrkflw_processed_message (3)
	{Table: "wrkflw_processed_message", Column: "subscriber"}:   ClassReference,
	{Table: "wrkflw_processed_message", Column: "message_id"}:   ClassReference,
	{Table: "wrkflw_processed_message", Column: "processed_at"}: ClassTimestamp,

	// wrkflw_call_links (13)
	{Table: "wrkflw_call_links", Column: "child_instance_id"}:  ClassReference,
	{Table: "wrkflw_call_links", Column: "parent_instance_id"}: ClassReference,
	{Table: "wrkflw_call_links", Column: "parent_command_id"}:  ClassReference,
	{Table: "wrkflw_call_links", Column: "parent_def_id"}:      ClassReference,
	{Table: "wrkflw_call_links", Column: "claimed_by"}:         ClassReference, // worker lease owner (E7), not a person
	{Table: "wrkflw_call_links", Column: "parent_def_version"}: ClassScalar,
	{Table: "wrkflw_call_links", Column: "depth"}:              ClassScalar,
	{Table: "wrkflw_call_links", Column: "status"}:             ClassScalar,
	{Table: "wrkflw_call_links", Column: "output"}:             ClassFreeform,
	{Table: "wrkflw_call_links", Column: "error"}:              ClassFreeform,
	{Table: "wrkflw_call_links", Column: "created_at"}:         ClassTimestamp,
	{Table: "wrkflw_call_links", Column: "notified_at"}:        ClassTimestamp,
	{Table: "wrkflw_call_links", Column: "claimed_at"}:         ClassTimestamp,

	// wrkflw_timers (8)
	{Table: "wrkflw_timers", Column: "instance_id"}:     ClassReference,
	{Table: "wrkflw_timers", Column: "timer_id"}:        ClassReference,
	{Table: "wrkflw_timers", Column: "def_id"}:          ClassReference,
	{Table: "wrkflw_timers", Column: "next_run"}:        ClassTimestamp,
	{Table: "wrkflw_timers", Column: "kind"}:            ClassScalar,
	{Table: "wrkflw_timers", Column: "def_version"}:     ClassScalar,
	{Table: "wrkflw_timers", Column: "trigger_kind"}:    ClassScalar,
	{Table: "wrkflw_timers", Column: "trigger_payload"}: ClassFreeform,

	// wrkflw_chain_links (7)
	{Table: "wrkflw_chain_links", Column: "predecessor_instance_id"}:    ClassReference,
	{Table: "wrkflw_chain_links", Column: "outcome"}:                    ClassReference,
	{Table: "wrkflw_chain_links", Column: "successor_instance_id"}:      ClassReference,
	{Table: "wrkflw_chain_links", Column: "predecessor_definition_ref"}: ClassReference,
	{Table: "wrkflw_chain_links", Column: "successor_definition_ref"}:   ClassReference,
	{Table: "wrkflw_chain_links", Column: "start_vars"}:                 ClassFreeform,
	{Table: "wrkflw_chain_links", Column: "created_at"}:                 ClassTimestamp,

	// wrkflw_human_task (17)
	{Table: "wrkflw_human_task", Column: "task_id"}:          ClassReference,
	{Table: "wrkflw_human_task", Column: "instance_id"}:      ClassReference,
	{Table: "wrkflw_human_task", Column: "node_id"}:          ClassReference,
	{Table: "wrkflw_human_task", Column: "outcome"}:          ClassReference,
	{Table: "wrkflw_human_task", Column: "state"}:            ClassScalar,
	{Table: "wrkflw_human_task", Column: "claimed_by"}:       ClassActor, // human principal (ADR-0098: scalar projection of claim.actor.id)
	{Table: "wrkflw_human_task", Column: "claim_actor"}:      ClassActor,
	{Table: "wrkflw_human_task", Column: "completed_by"}:     ClassActor,
	{Table: "wrkflw_human_task", Column: "completion_actor"}: ClassActor,
	{Table: "wrkflw_human_task", Column: "candidates"}:       ClassActor,
	{Table: "wrkflw_human_task", Column: "claimed_at"}:       ClassTimestamp,
	{Table: "wrkflw_human_task", Column: "completed_at"}:     ClassTimestamp,
	{Table: "wrkflw_human_task", Column: "created_at"}:       ClassTimestamp,
	{Table: "wrkflw_human_task", Column: "due_at"}:           ClassTimestamp,
	{Table: "wrkflw_human_task", Column: "note"}:             ClassFreeform,
	{Table: "wrkflw_human_task", Column: "vars"}:             ClassFreeform,
	{Table: "wrkflw_human_task", Column: "eligibility"}:      ClassPolicy, // read by 4 Authorize sites in runtime/task/service.go

	// casbin_rule (8)
	{Table: "casbin_rule", Column: "id"}:    ClassScalar,
	{Table: "casbin_rule", Column: "ptype"}: ClassPolicy,
	{Table: "casbin_rule", Column: "v0"}:    ClassPolicy,
	{Table: "casbin_rule", Column: "v1"}:    ClassPolicy,
	{Table: "casbin_rule", Column: "v2"}:    ClassPolicy,
	{Table: "casbin_rule", Column: "v3"}:    ClassPolicy,
	{Table: "casbin_rule", Column: "v4"}:    ClassPolicy,
	{Table: "casbin_rule", Column: "v5"}:    ClassPolicy,
}
