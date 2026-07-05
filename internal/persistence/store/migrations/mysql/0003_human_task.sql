-- +goose Up

-- Human task state table (ADR-0098). task_token is the primary key.
-- eligibility/candidates/vars stored as JSON for fast predicate evaluation.
-- due_at is nullable: absent means no deadline.
CREATE TABLE wrkflw_human_task (
    task_token  VARCHAR(255) NOT NULL,
    instance_id VARCHAR(255) NOT NULL,
    node_id     VARCHAR(255) NOT NULL,
    state       VARCHAR(64)  NOT NULL,
    claimed_by  VARCHAR(255) NOT NULL DEFAULT '',
    eligibility JSON         NOT NULL,
    candidates  JSON         NOT NULL,
    vars        JSON         NOT NULL,
    created_at  DATETIME(6)  NOT NULL,
    due_at      DATETIME(6)  NULL,
    PRIMARY KEY (task_token),
    INDEX idx_wrkflw_human_task_instance   (instance_id),
    INDEX idx_wrkflw_human_task_state      (state),
    INDEX idx_wrkflw_human_task_claimed_by (claimed_by)
);

-- +goose Down

DROP TABLE IF EXISTS wrkflw_human_task;
