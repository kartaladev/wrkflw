-- +goose Up

-- Human task state table (ADR-0098). task_token is the primary key.
-- eligibility/candidates/vars stored as JSONB for fast predicate evaluation.
-- due_at is nullable: absent means no deadline.
CREATE TABLE wrkflw_human_task (
    task_token  TEXT        NOT NULL,
    instance_id TEXT        NOT NULL,
    node_id     TEXT        NOT NULL,
    state       TEXT        NOT NULL,
    claimed_by  TEXT        NOT NULL DEFAULT '',
    eligibility JSONB       NOT NULL,
    candidates  JSONB       NOT NULL,
    vars        JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    due_at      TIMESTAMPTZ,
    PRIMARY KEY (task_token)
);

CREATE INDEX idx_wrkflw_human_task_instance   ON wrkflw_human_task (instance_id);
CREATE INDEX idx_wrkflw_human_task_state      ON wrkflw_human_task (state);
CREATE INDEX idx_wrkflw_human_task_claimed_by ON wrkflw_human_task (claimed_by);

-- +goose Down

DROP INDEX IF EXISTS idx_wrkflw_human_task_claimed_by;
DROP INDEX IF EXISTS idx_wrkflw_human_task_state;
DROP INDEX IF EXISTS idx_wrkflw_human_task_instance;
DROP TABLE IF EXISTS wrkflw_human_task;
