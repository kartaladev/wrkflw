-- +goose Up

-- Adds the process-definition Qualifier to wrkflw_human_task so the task
-- service can resolve the UserTask node's CompletionValidation via a
-- DefinitionResolver (Item 3 / input validation). def_version == 0 means
-- "latest".
ALTER TABLE wrkflw_human_task ADD COLUMN def_id      VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE wrkflw_human_task ADD COLUMN def_version BIGINT NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE wrkflw_human_task DROP COLUMN def_version;
ALTER TABLE wrkflw_human_task DROP COLUMN def_id;
