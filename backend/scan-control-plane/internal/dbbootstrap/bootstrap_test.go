package dbbootstrap

import "testing"

func TestClassifyEmptySchema(t *testing.T) {
	t.Parallel()

	if got := classify(schemaSnapshot{Tables: map[string]bool{}, Columns: map[string]map[string]bool{}}); got != StateEmpty {
		t.Fatalf("state = %s, want %s", got, StateEmpty)
	}
}

func TestClassifyNewSchemaFromBaselineMarker(t *testing.T) {
	t.Parallel()

	tables := mapFrom(currentTables)
	tables[markerTable] = true
	got := classify(schemaSnapshot{
		Tables: tables,
		Columns: map[string]map[string]bool{
			"sources":         {"source_id": true},
			"source_bindings": {"binding_id": true},
			"documents":       {"document_id": true},
			"parse_tasks":     {"task_id": true},
		},
		BaselineMarked: true,
	})
	if got != StateNew {
		t.Fatalf("state = %s, want %s", got, StateNew)
	}
}

func TestClassifyMarkerOnlyAsEmptySchema(t *testing.T) {
	t.Parallel()

	got := classify(schemaSnapshot{
		Tables:         map[string]bool{markerTable: true},
		Columns:        map[string]map[string]bool{},
		BaselineMarked: true,
	})
	if got != StateEmpty {
		t.Fatalf("state = %s, want %s", got, StateEmpty)
	}
}

func TestClassifyNewSchemaFromCurrentTables(t *testing.T) {
	t.Parallel()

	got := classify(schemaSnapshot{
		Tables: mapFrom(currentTables),
		Columns: map[string]map[string]bool{
			"sources":         {"source_id": true},
			"source_bindings": {"binding_id": true},
			"documents":       {"document_id": true},
			"parse_tasks":     {"task_id": true},
		},
	})
	if got != StateNew {
		t.Fatalf("state = %s, want %s", got, StateNew)
	}
}

func TestClassifyLegacySchemaFromLegacyTables(t *testing.T) {
	t.Parallel()

	got := classify(schemaSnapshot{
		Tables: map[string]bool{
			"sources":               true,
			"cloud_source_bindings": true,
		},
		Columns: map[string]map[string]bool{
			"sources": {"id": true, "root_path": true},
		},
	})
	if got != StateLegacy {
		t.Fatalf("state = %s, want %s", got, StateLegacy)
	}
}

func TestClassifyMarkedLegacySchemaAsUnknown(t *testing.T) {
	t.Parallel()

	got := classify(schemaSnapshot{
		Tables: map[string]bool{
			markerTable:             true,
			"sources":               true,
			"cloud_source_bindings": true,
		},
		Columns: map[string]map[string]bool{
			"sources": {"id": true, "root_path": true},
		},
		BaselineMarked: true,
	})
	if got != StateUnknown {
		t.Fatalf("state = %s, want %s", got, StateUnknown)
	}
}

func TestClassifyLegacySchemaFromOldSourceColumns(t *testing.T) {
	t.Parallel()

	got := classify(schemaSnapshot{
		Tables: map[string]bool{"sources": true},
		Columns: map[string]map[string]bool{
			"sources": {"id": true, "root_path": true},
		},
	})
	if got != StateLegacy {
		t.Fatalf("state = %s, want %s", got, StateLegacy)
	}
}

func TestClassifyUnknownSchemaRefusesAmbiguousTables(t *testing.T) {
	t.Parallel()

	got := classify(schemaSnapshot{
		Tables: map[string]bool{"sources": true},
		Columns: map[string]map[string]bool{
			"sources": {"source_id": true},
		},
	})
	if got != StateUnknown {
		t.Fatalf("state = %s, want %s", got, StateUnknown)
	}
}

func TestResetTablesIncludeCurrentAndLegacyTables(t *testing.T) {
	t.Parallel()

	tables := mapFrom(resetTables)
	for _, table := range append(append([]string{}, currentTables...), legacyOnlyTables...) {
		if !tables[table] {
			t.Fatalf("reset table list missing %s", table)
		}
	}
}

func mapFrom(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}
