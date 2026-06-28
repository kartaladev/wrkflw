-- +goose Up

CREATE TABLE wrkflw_processed_message (
    subscriber   VARCHAR(255) NOT NULL,
    message_id   VARCHAR(255) NOT NULL,
    processed_at DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (subscriber, message_id)
);

CREATE TABLE wrkflw_call_links (
    child_instance_id   VARCHAR(255) PRIMARY KEY,
    parent_instance_id  VARCHAR(255) NOT NULL,
    parent_command_id   VARCHAR(255) NOT NULL,
    parent_def_id       VARCHAR(255) NOT NULL,
    parent_def_version  INT          NOT NULL,
    depth               INT          NOT NULL,
    status              VARCHAR(50)  NOT NULL DEFAULT 'running',
    output              JSON,
    error               TEXT,
    created_at          DATETIME(6)  NOT NULL,
    notified_at         DATETIME(6),
    claimed_at          DATETIME(6),
    claimed_by          VARCHAR(255)
);
CREATE INDEX wrkflw_call_links_pending_idx        ON wrkflw_call_links (child_instance_id);
CREATE INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links (parent_instance_id);

CREATE TABLE wrkflw_timers (
    instance_id VARCHAR(255) NOT NULL,
    timer_id    VARCHAR(255) NOT NULL,
    fire_at     DATETIME(6)  NOT NULL,
    kind        SMALLINT     NOT NULL,
    def_id      VARCHAR(255) NOT NULL,
    def_version INT          NOT NULL,
    PRIMARY KEY (instance_id, timer_id)
);
CREATE INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers (fire_at);

CREATE TABLE wrkflw_chain_links (
    predecessor_instance_id    VARCHAR(255) NOT NULL,
    outcome                    VARCHAR(255) NOT NULL,
    successor_instance_id      VARCHAR(255) NOT NULL,
    predecessor_definition_ref VARCHAR(255) NOT NULL DEFAULT '',
    successor_definition_ref   VARCHAR(255) NOT NULL DEFAULT '',
    start_vars                 JSON,
    created_at                 DATETIME(6)  NOT NULL,
    PRIMARY KEY (predecessor_instance_id, outcome)
);
CREATE INDEX wrkflw_chain_links_successor_idx ON wrkflw_chain_links (successor_instance_id);

CREATE INDEX wrkflw_instances_list_idx ON wrkflw_instances (started_at DESC, instance_id DESC);

-- +goose Down

DROP INDEX wrkflw_instances_list_idx ON wrkflw_instances;
DROP INDEX wrkflw_chain_links_successor_idx ON wrkflw_chain_links;
DROP TABLE IF EXISTS wrkflw_chain_links;
DROP INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers;
DROP TABLE IF EXISTS wrkflw_timers;
DROP INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links;
DROP INDEX wrkflw_call_links_pending_idx ON wrkflw_call_links;
DROP TABLE IF EXISTS wrkflw_call_links;
DROP TABLE IF EXISTS wrkflw_processed_message;
