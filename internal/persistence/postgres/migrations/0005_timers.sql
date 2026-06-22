-- +goose Up

-- Armed-timer table for timer rehydration on restart (ADR-0027). One row per
-- (instance, timer); written atomically with the instance state commit.
CREATE TABLE wrkflw_timers (
    instance_id TEXT        NOT NULL,
    timer_id    TEXT        NOT NULL,
    fire_at     TIMESTAMPTZ NOT NULL,
    kind        SMALLINT    NOT NULL,
    def_id      TEXT        NOT NULL,
    def_version INT         NOT NULL,
    PRIMARY KEY (instance_id, timer_id)
);

CREATE INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers (fire_at);

-- +goose Down

DROP INDEX IF EXISTS wrkflw_timers_fire_at_idx;
DROP TABLE IF EXISTS wrkflw_timers;
