package source

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const initMigrationName = "20260519101723_init"

func TestInitMigrationContract(t *testing.T) {
	t.Parallel()

	ddl := readInitDDL(t)
	normalized := normalizeSQL(ddl)

	requiredTables := []string{
		"sources",
		"source_bindings",
		"source_object_index",
		"source_document_states",
		"documents",
		"parse_tasks",
		"parse_task_dead_letters",
		"source_sync_checkpoints",
		"source_sync_runs",
		"data_source_create_operations",
		"agents",
		"agent_commands",
	}
	for _, table := range requiredTables {
		assertContains(t, normalized, "create table public."+table+" ")
	}

	for _, table := range []string{
		sqlName("cloud", "source", "bindings"),
		sqlName("cloud", "object", "index"),
		sqlName("cloud", "sync", "checkpoints"),
		sqlName("cloud", "sync", "runs"),
	} {
		assertNotContains(t, normalized, table)
	}
}

func TestSourcesTableHasNoTargetFields(t *testing.T) {
	t.Parallel()

	sources := extractCreateTable(t, readInitDDL(t), "sources")
	for _, forbidden := range []string{
		sqlName("root", "path"),
		sqlName("agent", "id"),
		"provider",
		sqlName("target", "ref"),
	} {
		assertNotContains(t, normalizeSQL(sources), forbidden)
	}
	for _, required := range []string{
		"source_id text primary key",
		"dataset_id text not null",
		"source_options_json jsonb",
		"config_version bigint not null default 1",
	} {
		assertContains(t, normalizeSQL(sources), required)
	}
}

func TestMigrationTablesUseBindingScopedIdentity(t *testing.T) {
	t.Parallel()

	ddl := readInitDDL(t)
	for table, required := range map[string][]string{
		"source_object_index": {
			"source_id text not null references public.sources(source_id)",
			"binding_id text not null references public.source_bindings(binding_id)",
			"object_key text not null",
			"primary key (binding_id, object_key)",
		},
		"source_document_states": {
			"source_id text not null references public.sources(source_id)",
			"binding_id text not null references public.source_bindings(binding_id)",
			"binding_generation bigint not null",
			"object_key text not null",
			"primary key (source_id, binding_id, object_key)",
		},
		"documents": {
			"source_id text not null references public.sources(source_id)",
			"binding_id text not null references public.source_bindings(binding_id)",
			"object_key text not null",
		},
		"parse_tasks": {
			"source_id text not null references public.sources(source_id)",
			"binding_id text not null references public.source_bindings(binding_id)",
			"binding_generation bigint not null",
			"object_key text not null",
			"document_id text not null references public.documents(document_id)",
		},
		"source_sync_checkpoints": {
			"source_id text not null references public.sources(source_id)",
			"binding_id text primary key references public.source_bindings(binding_id)",
			"binding_generation bigint not null",
		},
		"source_sync_runs": {
			"source_id text not null references public.sources(source_id)",
			"binding_id text not null references public.source_bindings(binding_id)",
			"binding_generation bigint not null",
			"scheduled_fire_at timestamp with time zone",
			"coverage_json jsonb",
		},
		"parse_task_dead_letters": {
			"source_id text not null",
			"binding_id text not null",
			"binding_generation bigint not null",
			"object_key text not null",
			"document_id text not null",
		},
	} {
		tableDDL := normalizeSQL(extractCreateTable(t, ddl, table))
		for _, column := range required {
			assertContains(t, tableDDL, column)
		}
	}
}

func TestKeyIndexesAndIdempotencyConstraints(t *testing.T) {
	t.Parallel()

	ddl := normalizeSQL(readInitDDL(t))
	for _, required := range []string{
		"create unique index uk_source_binding_current_target on public.source_bindings (source_id, connector_type, target_type, target_fingerprint) where status <> 'deleting'",
		"primary key (binding_id, object_key)",
		"primary key (source_id, binding_id, object_key)",
		"create unique index uk_documents_object on public.documents (source_id, binding_id, object_key)",
		"create unique index uk_parse_task_idempotency on public.parse_tasks (idempotency_key)",
		"create unique index uk_parse_task_active on public.parse_tasks (source_id, binding_id, object_key, target_version_id, task_action) where status in ('pending', 'running', 'submitted')",
		"create unique index uk_source_sync_runs_scheduled_fire on public.source_sync_runs (binding_id, binding_generation, scheduled_fire_at) where trigger_type = 'scheduled' and scheduled_fire_at is not null and status in ('pending', 'running')",
		"create unique index uk_create_operation on public.data_source_create_operations (caller_id, request_id)",
	} {
		assertContains(t, ddl, required)
	}
}

func TestForbiddenLegacyDDLNamesAreAbsent(t *testing.T) {
	t.Parallel()

	ddl := normalizeSQL(readInitDDL(t))
	for _, forbidden := range []string{
		sqlName("cloud", "source", "bindings"),
		sqlName("cloud", "object", "index"),
		sqlName("cloud", "sync", "checkpoints"),
		sqlName("cloud", "sync", "runs"),
		sqlName("root", "path"),
		"origin" + "type",
		"local" + "_fs",
		"cloud" + "_sync",
		"schedule_expr",
		"schedule_tz",
	} {
		assertNotContains(t, ddl, forbidden)
	}
}

func TestBindingGenerationAndCoverageArePersisted(t *testing.T) {
	t.Parallel()

	ddl := normalizeSQL(readInitDDL(t))
	for table, columns := range map[string][]string{
		"source_document_states":  {"binding_generation bigint not null", "source_state text not null", "sync_state text not null"},
		"parse_tasks":             {"binding_generation bigint not null", "idempotency_key text not null"},
		"source_sync_checkpoints": {"binding_generation bigint not null"},
		"source_sync_runs":        {"binding_generation bigint not null", "coverage_json jsonb"},
		"parse_task_dead_letters": {"binding_generation bigint not null"},
		"source_bindings":         {"schedule_policy_json jsonb"},
	} {
		tableDDL := normalizeSQL(extractCreateTable(t, ddl, table))
		for _, column := range columns {
			assertContains(t, tableDDL, column)
		}
	}
}

func TestWorkerSafetyDDLContract(t *testing.T) {
	t.Parallel()

	ddl := normalizeSQL(readInitDDL(t))
	parseTasks := normalizeSQL(extractCreateTable(t, ddl, "parse_tasks"))
	deadLetters := normalizeSQL(extractCreateTable(t, ddl, "parse_task_dead_letters"))
	for _, required := range []string{
		"lease_owner text",
		"lease_until timestamp with time zone",
		"retry_count bigint not null default 0",
		"next_run_at timestamp with time zone not null",
		"core_task_id text",
		"core_document_id text",
	} {
		assertContains(t, parseTasks, required)
	}
	for _, required := range []string{
		"dead_letter_id text primary key",
		"task_id text not null",
		"retry_count bigint not null",
		"error_code text",
		"failed_at timestamp with time zone not null",
	} {
		assertContains(t, deadLetters, required)
	}
	assertContains(t, ddl, "create index idx_parse_tasks_due on public.parse_tasks (status, next_run_at)")
	assertContains(t, ddl, "create index idx_parse_task_dead_letters_task on public.parse_task_dead_letters (task_id)")
}

func TestJSONAndTimestampColumnTypes(t *testing.T) {
	t.Parallel()

	ddl := readInitDDL(t)
	for _, line := range strings.Split(ddl, "\n") {
		normalizedLine := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(normalizedLine, "_json text") || strings.Contains(normalizedLine, "_jsons text") {
			t.Fatalf("json column must not use text: %s", line)
		}
		if strings.Contains(normalizedLine, "_at timestamp") && !strings.Contains(normalizedLine, "timestamp with time zone") {
			t.Fatalf("timestamp column must use timestamp with time zone: %s", line)
		}
	}
}

func readInitDDL(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "migrations", initMigrationName+".up.sql")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return string(content)
}

func extractCreateTable(t *testing.T, ddl, table string) string {
	t.Helper()

	re := regexp.MustCompile(`(?is)create\s+table\s+public\.` + regexp.QuoteMeta(table) + `\s*\(.*?\);`)
	match := re.FindString(ddl)
	if match == "" {
		t.Fatalf("CREATE TABLE public.%s not found", table)
	}
	return match
}

func normalizeSQL(sql string) string {
	fields := strings.Fields(strings.ToLower(sql))
	return strings.Join(fields, " ")
}

func sqlName(parts ...string) string {
	return strings.Join(parts, "_")
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected SQL to contain %q", needle)
	}
}

func assertNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected SQL not to contain %q", needle)
	}
}
