package workflow

import (
	"testing"

	"lazymind/core/common/orm"
)

// --- Pure function tests ---

func TestStringSliceFromAny_StringSlice(t *testing.T) {
	got := stringSliceFromAny([]string{"a", "b", "c"})
	if len(got) != 3 || got[0] != "a" {
		t.Fatalf("got %v", got)
	}
}

func TestStringSliceFromAny_AnySlice(t *testing.T) {
	got := stringSliceFromAny([]any{"x", "y", 42, nil, "z"})
	if len(got) != 3 || got[2] != "z" {
		t.Fatalf("got %v, want [x y z]", got)
	}
}

func TestStringSliceFromAny_AnySliceFiltersEmpty(t *testing.T) {
	got := stringSliceFromAny([]any{"", "  ", "valid"})
	if len(got) != 1 || got[0] != "valid" {
		t.Fatalf("got %v, want [valid]", got)
	}
}

func TestStringSliceFromAny_Nil(t *testing.T) {
	if got := stringSliceFromAny(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestStringSliceFromAny_UnsupportedType(t *testing.T) {
	if got := stringSliceFromAny("not a slice"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestDatasetIDsFromSearchConfig_FlatIDs(t *testing.T) {
	sc := map[string]any{"dataset_ids": []string{"kb-1", "kb-2"}}
	got := datasetIDsFromSearchConfig(sc)
	if len(got) != 2 || got[0] != "kb-1" {
		t.Fatalf("got %v", got)
	}
}

func TestDatasetIDsFromSearchConfig_ListFormat(t *testing.T) {
	sc := map[string]any{
		"dataset_list": []any{
			map[string]any{"id": "ds-1"},
			map[string]any{"id": "ds-2"},
		},
	}
	got := datasetIDsFromSearchConfig(sc)
	if len(got) != 2 || got[1] != "ds-2" {
		t.Fatalf("got %v", got)
	}
}

func TestDatasetIDsFromSearchConfig_ListFormatSkipsEmpty(t *testing.T) {
	sc := map[string]any{
		"dataset_list": []any{
			map[string]any{"id": ""},
			map[string]any{"id": "  "},
			map[string]any{"id": "valid-id"},
			map[string]any{},
		},
	}
	got := datasetIDsFromSearchConfig(sc)
	if len(got) != 1 || got[0] != "valid-id" {
		t.Fatalf("got %v", got)
	}
}

func TestDatasetIDsFromSearchConfig_Empty(t *testing.T) {
	if got := datasetIDsFromSearchConfig(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := datasetIDsFromSearchConfig(map[string]any{}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// --- DB-backed tests ---

func newConversationTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, &orm.Conversation{})
}

// TestLoadConversationSearchConfig_NoDB returns nil for nil DB.
func TestLoadConversationSearchConfig_NoDB(t *testing.T) {
	if got := loadConversationSearchConfig(nil, "conv-1"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestLoadConversationSearchConfig_EmptyConvID returns nil.
func TestLoadConversationSearchConfig_EmptyConvID(t *testing.T) {
	db := newConversationTestDB(t)
	if got := loadConversationSearchConfig(db.DB, ""); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestLoadConversationSearchConfig_NotFound returns nil.
func TestLoadConversationSearchConfig_NotFound(t *testing.T) {
	db := newConversationTestDB(t)
	if got := loadConversationSearchConfig(db.DB, "nonexistent"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestLoadConversationSearchConfig_EmptyJSON returns nil when search_config is {}.
func TestLoadConversationSearchConfig_EmptyJSON(t *testing.T) {
	db := newConversationTestDB(t)
	db.DB.Create(&orm.Conversation{ID: "conv-1", SearchConfig: []byte("{}")})
	if got := loadConversationSearchConfig(db.DB, "conv-1"); got != nil {
		t.Fatalf("got %v, want nil for empty JSON", got)
	}
}

// TestLoadConversationSearchConfig_ValidJSON returns parsed map.
func TestLoadConversationSearchConfig_ValidJSON(t *testing.T) {
	db := newConversationTestDB(t)
	db.DB.Create(&orm.Conversation{ID: "conv-2", SearchConfig: []byte(`{"dataset_ids":["kb-1"],"creators":["Alice"]}`)})
	sc := loadConversationSearchConfig(db.DB, "conv-2")
	if sc == nil || len(sc) != 2 {
		t.Fatalf("got %v, want 2 keys", sc)
	}
}

// TestPersistConversationSearchConfig_NoDB returns nil for nil DB.
func TestPersistConversationSearchConfig_NoDB(t *testing.T) {
	sc := map[string]any{"dataset_ids": []string{"kb-1"}}
	if err := persistConversationSearchConfig(nil, "conv-1", "u1", sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPersistConversationSearchConfig_EmptyConvID returns nil.
func TestPersistConversationSearchConfig_EmptyConvID(t *testing.T) {
	db := newConversationTestDB(t)
	sc := map[string]any{"key": "val"}
	if err := persistConversationSearchConfig(db.DB, "", "u1", sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPersistConversationSearchConfig_EmptyMap returns nil.
func TestPersistConversationSearchConfig_EmptyMap(t *testing.T) {
	db := newConversationTestDB(t)
	if err := persistConversationSearchConfig(db.DB, "conv-1", "u1", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPersistConversationSearchConfig_UpdatesRow writes search_config.
func TestPersistConversationSearchConfig_UpdatesRow(t *testing.T) {
	db := newConversationTestDB(t)
	db.DB.Create(&orm.Conversation{ID: "conv-3"})
	sc := map[string]any{"dataset_ids": []string{"kb-1"}}
	if err := persistConversationSearchConfig(db.DB, "conv-3", "", sc); err != nil {
		t.Fatalf("persist: %v", err)
	}
	loaded := loadConversationSearchConfig(db.DB, "conv-3")
	if loaded == nil {
		t.Fatal("expected non-nil search config after persist")
	}
}

// TestFiltersFromConversation builds filters from search_config.
func TestFiltersFromConversation(t *testing.T) {
	db := newConversationTestDB(t)
	db.DB.Create(&orm.Conversation{ID: "conv-5", SearchConfig: []byte(`{"dataset_ids":["kb-1"],"creators":["Alice"],"tags":["p0"]}`)})
	filters := filtersFromConversation(db.DB, "conv-5")
	if filters == nil {
		t.Fatal("expected non-nil filters")
	}
	if len(filters) != 3 {
		t.Fatalf("expected 3 filter keys, got %d: %v", len(filters), filters)
	}
}

// TestFiltersFromConversation_NoConfig returns nil.
func TestFiltersFromConversation_NoConfig(t *testing.T) {
	db := newConversationTestDB(t)
	db.DB.Create(&orm.Conversation{ID: "conv-6"})
	if got := filtersFromConversation(db.DB, "conv-6"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}
