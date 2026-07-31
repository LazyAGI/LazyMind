package chat

import (
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common"
)

// TestToString_String returns the string as-is.
func TestToString_String(t *testing.T) {
	if got := toString("hello"); got != "hello" {
		t.Fatalf("toString(string) = %q, want hello", got)
	}
}

// TestToString_Float64 converts to string without trailing zeros.
func TestToString_Float64(t *testing.T) {
	if got := toString(float64(3.14)); got != "3.14" {
		t.Fatalf("toString(float64) = %q, want 3.14", got)
	}
}

// TestToString_Int converts to decimal string.
func TestToString_Int(t *testing.T) {
	if got := toString(int(42)); got != "42" {
		t.Fatalf("toString(int) = %q, want 42", got)
	}
}

// TestToString_Int64 converts to decimal string.
func TestToString_Int64(t *testing.T) {
	if got := toString(int64(123456789)); got != "123456789" {
		t.Fatalf("toString(int64) = %q, want 123456789", got)
	}
}

// TestToString_IntegerFloat64 strips the decimal part.
func TestToString_IntegerFloat64(t *testing.T) {
	if got := toString(float64(10.0)); got != "10" {
		t.Fatalf("toString(10.0) = %q, want 10", got)
	}
}

// TestToString_Unknown returns empty string for unsupported types.
func TestToString_Unknown(t *testing.T) {
	if got := toString([]int{1, 2}); got != "" {
		t.Fatalf("toString([]int) = %q, want empty", got)
	}
	if got := toString(nil); got != "" {
		t.Fatalf("toString(nil) = %q, want empty", got)
	}
	if got := toString(true); got != "" {
		t.Fatalf("toString(bool) = %q, want empty", got)
	}
}

// TestExtractMessageForACL_EmptyBody returns userID with no items.
func TestExtractMessageForACL_EmptyBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-User-Id", "user-1")
	uid, items := extractMessageForACL(req, nil)
	if uid != "user-1" {
		t.Fatalf("userID: got %q, want user-1", uid)
	}
	if items != nil {
		t.Fatalf("items: expected nil, got %v", items)
	}
}

// TestExtractMessageForACL_KbIDOnly returns a single KB check item.
func TestExtractMessageForACL_KbIDOnly(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"kb_id":"kb-1"}`))
	req.Header.Set("X-User-Id", "user-2")
	uid, items := extractMessageForACL(req, []byte(`{"kb_id":"kb-1"}`))
	if uid != "user-2" {
		t.Fatalf("userID: got %q, want user-2", uid)
	}
	if len(items) != 1 {
		t.Fatalf("items: got %d, want 1", len(items))
	}
	if items[0].ResourceType != "kb" || items[0].ResourceID != "kb-1" || items[0].NeedPerm != "read" {
		t.Fatalf("item: got %+v", items[0])
	}
}

// TestExtractMessageForACL_DatasetIDOnly returns a single DB check item.
func TestExtractMessageForACL_DatasetIDOnly(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"dataset_id":"ds-1"}`))
	req.Header.Set("X-User-Id", "user-3")
	uid, items := extractMessageForACL(req, []byte(`{"dataset_id":"ds-1"}`))
	if uid != "user-3" {
		t.Fatalf("userID: got %q, want user-3", uid)
	}
	if len(items) != 1 {
		t.Fatalf("items: got %d, want 1", len(items))
	}
	if items[0].ResourceType != "db" || items[0].ResourceID != "ds-1" {
		t.Fatalf("item: got %+v", items[0])
	}
}

// TestExtractMessageForACL_Both returns two ACL check items.
func TestExtractMessageForACL_Both(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"kb_id":"kb-1","dataset_id":"ds-1"}`))
	req.Header.Set("X-User-Id", "user-4")
	uid, items := extractMessageForACL(req, []byte(`{"kb_id":"kb-1","dataset_id":"ds-1"}`))
	if uid != "user-4" {
		t.Fatalf("userID: got %q, want user-4", uid)
	}
	if len(items) != 2 {
		t.Fatalf("items: got %d, want 2", len(items))
	}
	if got := items[0].ResourceType; got != "kb" {
		t.Fatalf("first type: got %q, want kb", got)
	}
	if got := items[1].ResourceType; got != "db" {
		t.Fatalf("second type: got %q, want db", got)
	}
}

// TestExtractMessageForACL_NoRelevantKeys returns nil items.
func TestExtractMessageForACL_NoRelevantKeys(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"other_field":"value"}`))
	req.Header.Set("X-User-Id", "user-5")
	uid, items := extractMessageForACL(req, []byte(`{"other_field":"value"}`))
	if uid != "user-5" {
		t.Fatalf("userID: got %q, want user-5", uid)
	}
	if items != nil {
		t.Fatalf("items: expected nil, got %v", items)
	}
}

// TestExtractMessageForACL_InvalidJSON returns nil items gracefully.
func TestExtractMessageForACL_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`not json`))
	req.Header.Set("X-User-Id", "user-6")
	uid, items := extractMessageForACL(req, []byte(`not json`))
	if uid != "user-6" {
		t.Fatalf("userID: got %q, want user-6", uid)
	}
	if items != nil {
		t.Fatalf("items: expected nil for invalid json, got %v", items)
	}
}

// TestExtractMessageForACL_EmptyUserID returns empty userID.
func TestExtractMessageForACL_EmptyUserID(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"kb_id":"kb-1"}`))
	uid, items := extractMessageForACL(req, []byte(`{"kb_id":"kb-1"}`))
	if uid != "" {
		t.Fatalf("userID: got %q, want empty", uid)
	}
	if len(items) != 1 {
		t.Fatalf("items: got %d, want 1 (items still extracted)", len(items))
	}
}

// TestExtractMessageForACL_WhitespaceUserID trims the header value.
func TestExtractMessageForACL_WhitespaceUserID(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"kb_id":"kb-1"}`))
	req.Header.Set("X-User-Id", "  user-7  ")
	uid, _ := extractMessageForACL(req, []byte(`{"kb_id":"kb-1"}`))
	if uid != "user-7" {
		t.Fatalf("userID: got %q, want user-7", uid)
	}
}

// TestExtractMessageForACL_NeedPermIsRead ensures all items request read permission.
func TestExtractMessageForACL_NeedPermIsRead(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"kb_id":"kb-1","dataset_id":"ds-1"}`))
	req.Header.Set("X-User-Id", "user-8")
	_, items := extractMessageForACL(req, []byte(`{"kb_id":"kb-1","dataset_id":"ds-1"}`))
	for _, item := range items {
		if item.NeedPerm != "read" {
			t.Fatalf("item %+v: need_perm = %q, want read", item, item.NeedPerm)
		}
	}
}

// Verify the ACLCheckItem type shape is correct (compile-time check).
var _ = []common.ACLCheckItem{
	{ResourceType: "kb", ResourceID: "x", NeedPerm: "read"},
}
