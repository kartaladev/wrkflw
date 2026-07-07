-- +goose Up

-- Durable timer descriptor (Plan 3 Tasks 1-2): mirror the Postgres 0011 change.
-- Rename fire_at -> next_run and add the trigger descriptor columns so a
-- SQL-backed one-shot timer re-arms at its original absolute fire time after a
-- restart (closes the Plan-2 rehydration regression).
ALTER TABLE wrkflw_timers RENAME COLUMN fire_at TO next_run;
DROP INDEX IF EXISTS wrkflw_timers_fire_at_idx;
CREATE INDEX wrkflw_timers_next_run_idx ON wrkflw_timers (next_run);

ALTER TABLE wrkflw_timers ADD COLUMN trigger_kind    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_payload TEXT;

-- +goose Down

ALTER TABLE wrkflw_timers DROP COLUMN trigger_payload;
ALTER TABLE wrkflw_timers DROP COLUMN trigger_kind;
DROP INDEX IF EXISTS wrkflw_timers_next_run_idx;
CREATE INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers (next_run);
ALTER TABLE wrkflw_timers RENAME COLUMN next_run TO fire_at;
