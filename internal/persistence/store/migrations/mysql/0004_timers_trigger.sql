-- +goose Up

-- Durable timer descriptor (Plan 3 Tasks 1-2): mirror the Postgres 0011 change.
-- Rename fire_at -> next_run and add the trigger descriptor columns so a
-- SQL-backed one-shot timer re-arms at its original absolute fire time after a
-- restart (closes the Plan-2 rehydration regression).
ALTER TABLE wrkflw_timers CHANGE fire_at next_run DATETIME(6) NOT NULL;
DROP INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers;
CREATE INDEX wrkflw_timers_next_run_idx ON wrkflw_timers (next_run);

ALTER TABLE wrkflw_timers ADD COLUMN trigger_kind    SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_payload JSON NULL;

-- +goose Down

ALTER TABLE wrkflw_timers DROP COLUMN trigger_payload;
ALTER TABLE wrkflw_timers DROP COLUMN trigger_kind;
DROP INDEX wrkflw_timers_next_run_idx ON wrkflw_timers;
CREATE INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers (next_run);
ALTER TABLE wrkflw_timers CHANGE next_run fire_at DATETIME(6) NOT NULL;
