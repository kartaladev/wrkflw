-- +goose Up

-- Process-instance chaining lineage (ADR-0045). One row per predecessor->successor
-- hop. The (predecessor_instance_id, outcome) primary key is the exactly-once
-- backstop: at most one successor per terminal outcome of a predecessor.
CREATE TABLE wrkflw_chain_links (
    predecessor_instance_id TEXT        NOT NULL,
    outcome                 TEXT        NOT NULL,
    successor_instance_id   TEXT        NOT NULL,
    predecessor_definition_ref         TEXT        NOT NULL DEFAULT '',
    successor_definition_ref           TEXT        NOT NULL DEFAULT '',
    start_vars              JSONB,
    created_at              TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (predecessor_instance_id, outcome)
);

-- Ancestry lookup: which predecessor produced a given successor.
CREATE INDEX wrkflw_chain_links_successor_idx
    ON wrkflw_chain_links (successor_instance_id);

-- +goose Down

DROP INDEX IF EXISTS wrkflw_chain_links_successor_idx;
DROP TABLE IF EXISTS wrkflw_chain_links;
