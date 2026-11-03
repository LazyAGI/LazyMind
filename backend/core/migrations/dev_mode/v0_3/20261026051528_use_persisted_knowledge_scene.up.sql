-- +migrate Up
-- +migrate Dialect postgres,sqlite
ALTER TABLE datasets DROP COLUMN scenario_key;
