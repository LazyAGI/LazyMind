package readonlyorm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// TableSpec defines an external (schema B) table that Core wants to read.
// If RequiredColumns is empty, only existence is checked.
type TableSpec struct {
	Schema          string
	Table           string
	RequiredColumns []string
}

// Validate checks that the external tables exist and (optionally) that required columns exist.
// This is intended to run at Core startup to detect schema drift early.
func Validate(ctx context.Context, db *sql.DB, specs []TableSpec) error {
	for _, s := range specs {
		schema := strings.TrimSpace(s.Schema)
		table := strings.TrimSpace(s.Table)
		if table == "" {
			continue
		}
		ok, err := tableExists(ctx, db, schema, table)
		if err != nil {
			return fmt.Errorf("validate %s: %w", Table(schema, table), err)
		}
		if !ok {
			return fmt.Errorf("missing table %s", Table(schema, table))
		}

		if len(s.RequiredColumns) == 0 {
			continue
		}
		missing, err := missingColumns(ctx, db, schema, table, s.RequiredColumns)
		if err != nil {
			return fmt.Errorf("validate columns %s: %w", Table(schema, table), err)
		}
		if len(missing) > 0 {
			return fmt.Errorf("table %s missing columns: %s", Table(schema, table), strings.Join(missing, ","))
		}
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, schema, table string) (bool, error) {
	const q = `
SELECT 1
FROM information_schema.tables
WHERE table_schema = $1 AND table_name = $2
LIMIT 1`
	var one int
	err := db.QueryRowContext(ctx, q, schema, table).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func missingColumns(ctx context.Context, db *sql.DB, schema, table string, required []string) ([]string, error) {
	const q = `
SELECT column_name
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	have := map[string]struct{}{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		have[c] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var missing []string
	for _, r := range required {
		rc := strings.TrimSpace(r)
		if rc == "" {
			continue
		}
		if _, ok := have[rc]; !ok {
			missing = append(missing, rc)
		}
	}
	return missing, nil
}
