-- +migrate Dialect postgres,sqlite
ALTER TABLE skill_market_items ADD COLUMN display_i18n JSON NOT NULL DEFAULT '{}';
