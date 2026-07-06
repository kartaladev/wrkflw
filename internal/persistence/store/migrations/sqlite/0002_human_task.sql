-- +goose Up

-- Human task state table (ADR-0098). task_token is the primary key.
-- eligibility/candidates/vars stored as TEXT (JSON serialised by the store layer).
-- due_at is nullable: absent means no deadline.
-- Timestamps stored as RFC3339 UTC strings, consistent with wrkflw_instances.
CREATE TABLE wrkflw_human_task (
    task_token  TEXT NOT NULL PRIMARY KEY,
    instance_id TEXT NOT NULL,
    node_id     TEXT NOT NULL,
    state       TEXT NOT NULL,
    claimed_by  TEXT NOT NULL DEFAULT '',
    eligibility TEXT NOT NULL,
    candidates  TEXT NOT NULL,
    vars        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    due_at      TEXT
);

CREATE INDEX idx_wrkflw_human_task_instance   ON wrkflw_human_task (instance_id);
CREATE INDEX idx_wrkflw_human_task_state      ON wrkflw_human_task (state);
CREATE INDEX idx_wrkflw_human_task_claimed_by ON wrkflw_human_task (claimed_by);

-- +goose Down

DROP INDEX IF EXISTS idx_wrkflw_human_task_claimed_by;
DROP INDEX IF EXISTS idx_wrkflw_human_task_state;
DROP INDEX IF EXISTS idx_wrkflw_human_task_instance;
DROP TABLE IF EXISTS wrkflw_human_task;
