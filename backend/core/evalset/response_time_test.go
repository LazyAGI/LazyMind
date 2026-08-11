package evalset

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEvalSetItemResponseMarshalsTimesInBeijing(t *testing.T) {
	utcTime := time.Date(2026, 6, 17, 8, 16, 17, 18086000, time.UTC)
	raw, err := json.Marshal(EvalSetItemResponse{
		ID:        "eval_item_1",
		CreatedAt: utcTime,
		UpdatedAt: utcTime,
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(raw), `"updated_at":"2026-06-17T08:16:17.018086Z"`) {
		t.Fatalf("updated_at must not be returned as UTC: %s", string(raw))
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["created_at"]; got != "2026-06-17T16:16:17.018086+08:00" {
		t.Fatalf("unexpected created_at: %v; raw=%s", got, string(raw))
	}
	if got := payload["updated_at"]; got != "2026-06-17T16:16:17.018086+08:00" {
		t.Fatalf("unexpected updated_at: %v; raw=%s", got, string(raw))
	}
}

// TestFormatResponseTime converts UTC to Asia/Shanghai timezone.
func TestFormatResponseTime(t *testing.T) {
	utcTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := formatResponseTime(utcTime)
	if !strings.Contains(got, "+08:00") {
		t.Fatalf("got %q, want +08:00 timezone", got)
	}
}

// TestFormatOptionalResponseTime returns nil for nil, formatted string for non-nil.
func TestFormatOptionalResponseTime(t *testing.T) {
	// Nil
	if got := formatOptionalResponseTime(nil); got != nil {
		t.Fatalf("nil got %v, want nil", got)
	}

	// Non-nil
	utcTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := formatOptionalResponseTime(&utcTime)
	if got == nil {
		t.Fatal("non-nil should return non-nil")
	}
	if !strings.Contains(*got, "+08:00") {
		t.Fatalf("got %q, want +08:00 timezone", *got)
	}
}
