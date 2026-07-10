package readonlyorm

import (
	"os"
	"reflect"
	"strings"
)

// Specs returns the readonly external tables Core expects to read.
func Specs() []TableSpec {
	if specs := specsFromEnv(); len(specs) > 0 {
		return specs
	}
	schema := LazyLLMSchema()
	return []TableSpec{
		{
			Schema:          schema,
			Table:           "lazyllm_documents",
			RequiredColumns: requiredColumnsOf(reflect.TypeOf(LazyLLMDocRow{})),
		},
		{
			Schema:          schema,
			Table:           "lazyllm_doc_service_tasks",
			RequiredColumns: requiredColumnsOf(reflect.TypeOf(LazyLLMDocServiceTaskRow{})),
		},
		{
			Schema:          schema,
			Table:           "lazyllm_kb_documents",
			RequiredColumns: requiredColumnsOf(reflect.TypeOf(LazyLLMKBDocRow{})),
		},
		{
			Schema:          schema,
			Table:           "lazyllm_kb_algorithm",
			RequiredColumns: requiredColumnsOf(reflect.TypeOf(LazyLLMKBAlgorithmRow{})),
		},
	}
}

// LAZYMIND_READONLY_TABLES supports items like:
// - "ragservice.documents"
// - "ragservice.documents,ragservice.jobs"
// - "documents" (schema defaults to LazyLLMSchema)
func specsFromEnv() []TableSpec {
	raw := strings.TrimSpace(os.Getenv("LAZYMIND_READONLY_TABLES"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]TableSpec, 0, len(parts))
	for _, p := range parts {
		item := strings.TrimSpace(p)
		if item == "" {
			continue
		}
		schema := LazyLLMSchema()
		table := item
		if strings.Contains(item, ".") {
			ss := strings.SplitN(item, ".", 2)
			schema = strings.TrimSpace(ss[0])
			table = strings.TrimSpace(ss[1])
		}
		out = append(out, TableSpec{Schema: schema, Table: table})
	}
	return out
}

func requiredColumnsOf(t reflect.Type) []string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var cols []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		// Only use explicit column tags; ignore embedded/anonymous.
		tag := f.Tag.Get("gorm")
		if tag == "" {
			continue
		}
		// parse "column:xxx" from the gorm tag
		parts := strings.Split(tag, ";")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "column:") {
				col := strings.TrimSpace(strings.TrimPrefix(p, "column:"))
				if col != "" {
					cols = append(cols, col)
				}
				break
			}
		}
	}
	return cols
}
