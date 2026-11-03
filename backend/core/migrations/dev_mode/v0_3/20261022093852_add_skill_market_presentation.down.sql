-- +migrate Dialect postgres,sqlite
ALTER TABLE skill_market_items DROP COLUMN display_i18n;
