package readonlyorm

import (
	"os"
	"strings"
)

// LazyLLMSchema returns the schema name that contains readonly external tables.
// Prefer LAZYMIND_READONLY_SCHEMA, and keep LAZYMIND_LAZYLLM_SCHEMA as backward-compatible fallback.
// An explicitly empty LAZYMIND_READONLY_SCHEMA is meaningful for schema-less stores such as SQLite.
func LazyLLMSchema() string {
	if v, ok := os.LookupEnv("LAZYMIND_READONLY_SCHEMA"); ok {
		return strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_LAZYLLM_SCHEMA")); v != "" {
		return v
	}
	return "public"
}

// Table returns a fully-qualified table name: schema.table
// Use it with GORM: db.Table(readonlyorm.Table("ragservice", "documents"))
func Table(schema, table string) string {
	s := strings.TrimSpace(schema)
	t := strings.TrimSpace(table)
	if s == "" {
		return t
	}
	return s + "." + t
}
