-- Duplicate projection rows cannot be reconstructed without inventing execution data.
-- +migrate Dialect postgres
SELECT 1;

-- +migrate Dialect sqlite
SELECT 1;
