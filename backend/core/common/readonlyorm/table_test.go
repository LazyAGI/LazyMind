package readonlyorm

import "testing"

func TestLazyLLMSchemaAllowsExplicitEmptySchema(t *testing.T) {
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	t.Setenv("LAZYMIND_LAZYLLM_SCHEMA", "public")

	if got := LazyLLMSchema(); got != "" {
		t.Fatalf("LazyLLMSchema() = %q, want empty schema", got)
	}
	if got := (LazyLLMDocRow{}).TableName(); got != "lazyllm_documents" {
		t.Fatalf("LazyLLMDocRow.TableName() = %q, want lazyllm_documents", got)
	}
}

func TestSpecsFromEnvUseLazyLLMSchemaForBareTables(t *testing.T) {
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	t.Setenv("LAZYMIND_READONLY_TABLES", "lazyllm_documents,lazyllm_doc_service_tasks")

	specs := specsFromEnv()
	if len(specs) != 2 {
		t.Fatalf("len(specsFromEnv()) = %d, want 2", len(specs))
	}
	for _, spec := range specs {
		if spec.Schema != "" {
			t.Fatalf("spec.Schema = %q, want empty schema", spec.Schema)
		}
	}
}
