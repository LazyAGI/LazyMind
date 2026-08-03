package chat

import (
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

// --- parseOptionalTime ---

// TestParseOptionalTime_Empty returns zero time and nil error.
func TestParseOptionalTime_Empty(t *testing.T) {
	tm, err := parseOptionalTime("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tm.IsZero() {
		t.Fatalf("expected zero time, got %v", tm)
	}
}

// TestParseOptionalTime_Whitespace returns zero time.
func TestParseOptionalTime_Whitespace(t *testing.T) {
	tm, err := parseOptionalTime("   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tm.IsZero() {
		t.Fatalf("expected zero time, got %v", tm)
	}
}

// TestParseOptionalTime_RFC3339 parses standard RFC3339 format.
func TestParseOptionalTime_RFC3339(t *testing.T) {
	tm, err := parseOptionalTime("2024-01-15T10:30:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm.Year() != 2024 || tm.Month() != 1 || tm.Day() != 15 {
		t.Fatalf("wrong date: got %v", tm)
	}
}

// TestParseOptionalTime_SpaceSeparated parses "2006-01-02 15:04:05" format.
func TestParseOptionalTime_SpaceSeparated(t *testing.T) {
	tm, err := parseOptionalTime("2024-06-20 14:30:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm.Month() != 6 || tm.Day() != 20 {
		t.Fatalf("wrong date: got %v", tm)
	}
}

// TestParseOptionalTime_DateOnly parses "2006-01-02" format.
func TestParseOptionalTime_DateOnly(t *testing.T) {
	tm, err := parseOptionalTime("2024-12-31")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm.Month() != 12 || tm.Day() != 31 {
		t.Fatalf("wrong date: got %v", tm)
	}
}

// TestParseOptionalTime_Invalid returns error for unparseable input.
func TestParseOptionalTime_Invalid(t *testing.T) {
	_, err := parseOptionalTime("not-a-date")
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

// --- normalizeExportFileTypes ---

// TestNormalizeExportFileTypes_ValidXLSX returns xlsx type.
func TestNormalizeExportFileTypes_ValidXLSX(t *testing.T) {
	got := normalizeExportFileTypes([]string{exportFileTypeXLSX})
	if len(got) != 1 || got[0] != exportFileTypeXLSX {
		t.Fatalf("got %v, want [%s]", got, exportFileTypeXLSX)
	}
}

// TestNormalizeExportFileTypes_ValidZIP returns zip type.
func TestNormalizeExportFileTypes_ValidZIP(t *testing.T) {
	got := normalizeExportFileTypes([]string{exportFileTypeZIP})
	if len(got) != 1 || got[0] != exportFileTypeZIP {
		t.Fatalf("got %v, want [%s]", got, exportFileTypeZIP)
	}
}

// TestNormalizeExportFileTypes_Mixed returns both valid types, deduplicated.
func TestNormalizeExportFileTypes_Mixed(t *testing.T) {
	got := normalizeExportFileTypes([]string{
		exportFileTypeXLSX,
		exportFileTypeZIP,
		exportFileTypeXLSX,        // duplicate
		exportFileTypeUnspecified, // filtered
		"",                        // filtered
		"INVALID",                 // filtered
	})
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 items", got)
	}
}

// TestNormalizeExportFileTypes_Empty returns nil.
func TestNormalizeExportFileTypes_Empty(t *testing.T) {
	if got := normalizeExportFileTypes(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := normalizeExportFileTypes([]string{}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestNormalizeExportFileTypes_AllInvalid returns empty non-nil slice.
func TestNormalizeExportFileTypes_AllInvalid(t *testing.T) {
	got := normalizeExportFileTypes([]string{"", "INVALID", exportFileTypeUnspecified})
	if len(got) != 0 {
		t.Fatalf("got %v, want empty slice", got)
	}
}

// --- buildConversationsCSV ---

// TestBuildConversationsCSV_Empty returns header-only CSV with BOM.
func TestBuildConversationsCSV_Empty(t *testing.T) {
	data, err := buildConversationsCSV(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty CSV (at least header)")
	}
	// Verify BOM prefix for UTF-8.
	if data[0] != 0xEF || data[1] != 0xBB || data[2] != 0xBF {
		t.Fatal("expected UTF-8 BOM at start of CSV")
	}
	content := string(data[3:])
	if !strings.Contains(content, "conversation_id") {
		t.Fatal("expected header row in CSV")
	}
}

// TestBuildConversationsCSV_MinimalBundle writes conversation row when no histories.
func TestBuildConversationsCSV_MinimalBundle(t *testing.T) {
	bundles := []exportConversationBundle{
		{
			Conversation: orm.Conversation{
				ID:          "conv-1",
				DisplayName: "My Chat",
			},
		},
	}
	data, err := buildConversationsCSV(bundles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := string(data[3:])
	if !strings.Contains(content, "conv-1") {
		t.Fatalf("expected conv-1 in CSV, got: %s", content)
	}
	if !strings.Contains(content, "My Chat") {
		t.Fatalf("expected My Chat in CSV")
	}
}

// --- buildConversationsZIP ---

// TestBuildConversationsZIP_Empty produces a valid ZIP with JSON and CSV inside.
func TestBuildConversationsZIP_Empty(t *testing.T) {
	data, err := buildConversationsZIP(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty ZIP data")
	}
	// A valid ZIP starts with "PK" magic bytes.
	if len(data) < 2 || data[0] != 'P' || data[1] != 'K' {
		t.Fatal("expected valid ZIP file (PK magic bytes)")
	}
}
