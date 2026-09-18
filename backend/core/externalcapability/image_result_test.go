package externalcapability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
)

func TestImageHistoryRefreshesLegacyAndStableReferencesOnlyForOwner(t *testing.T) {
	db := testDB(t)
	s := New(db, nil)
	ctx := context.Background()
	path := "/static-files/ai_generated/test.png"
	old := path + "?expires=1&sig=old"
	result := map[string]any{"image_url": old, "image_markdown": "![dog](" + old + ")",
		"images": []any{map[string]any{"image_url": old, "image_markdown": "![dog](" + old + ")"}}}
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "user-1"}, ExternalAgent: "codex"}
	row := &orm.ExternalCapabilityInvocation{ID: "image-result", OwnerUserID: "user-1", Agent: "codex",
		CapabilityType: CapabilityTool, CapabilityID: "builtin:image_generator", Status: "running", UsageJSON: json.RawMessage(`{}`)}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	s.finishAudit(call, row, "image_generator", map[string]any{}, result, nil)
	var stored orm.ExternalCapabilityInvocation
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.ResultJSON), "expires=") || strings.Contains(string(stored.ResultJSON), "sig=") {
		t.Fatalf("temporary signature persisted: %s", stored.ResultJSON)
	}
	for _, legacy := range []bool{false, true} {
		if legacy {
			if err := db.Model(row).Update("result_json", auditResultPreview(result)).Error; err != nil {
				t.Fatal(err)
			}
		}
		history, err := s.InvocationHistory(ctx, "user-1", "codex", 10)
		if err != nil || len(history.Invocations) != 1 {
			t.Fatalf("history: %#v %v", history, err)
		}
		raw := string(history.Invocations[0].ResultJSON)
		if strings.Contains(raw, "expires=1&") || strings.Contains(raw, "sig=old") || strings.Count(raw, "expires=") != 4 {
			t.Fatalf("all image and markdown links should be refreshed: %s", raw)
		}
	}
	other, err := s.InvocationHistory(ctx, "user-2", "codex", 10)
	if err != nil || len(other.Invocations) != 0 {
		t.Fatalf("cross-user history: %#v %v", other, err)
	}
}

func TestImageRefreshRejectsRemoteAndUnsafePaths(t *testing.T) {
	for _, path := range []string{"https://example.com/static-files/ai_generated/a.png", "/static-files/ai_generated/../secret.png",
		"/static-files/ai_generated/%2e%2e/secret.png", "/static-files/tenants/secret.png", "//example.com/static-files/ai_generated/a.png"} {
		raw, _ := json.Marshal(map[string]any{"image_url": path})
		if got := string(rewriteImageResult(raw, true)); got != string(raw) {
			t.Fatalf("unsafe URL changed: %s -> %s", raw, got)
		}
	}
}

func TestExternalImageURLs(t *testing.T) {
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://lazy.example/prefix/api/core/")
	for _, prefix := range []string{"", "/api/core"} {
		original := prefix + "/static-files/ai_generated/cat.png?expires=123&sig=abc"
		raw, _ := json.Marshal(map[string]any{"image_url": original, "image_markdown": "![cat](" + original + ")",
			"images": []any{map[string]any{"image_url": original}}})
		got := string(externalImageResult(raw))
		if strings.Count(got, "https://lazy.example/prefix/api/core/static-files/ai_generated/cat.png?expires=123") != 3 || strings.Count(got, "sig=abc") != 3 {
			t.Fatalf("unexpected external URLs: %s", got)
		}
	}
	for _, path := range []string{"https://remote.example/cat.png", "//remote.example/cat.png", "/static-files/ai_generated/../private.png", "/static-files/ai_generated/%2e%2e/private.png"} {
		raw, _ := json.Marshal(map[string]any{"image_url": path})
		if got := string(externalImageResult(raw)); got != string(raw) {
			t.Fatalf("untrusted URL changed: %s", got)
		}
	}
}
