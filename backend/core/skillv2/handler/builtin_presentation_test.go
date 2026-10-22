package handler

import (
	"encoding/json"
	b "lazymind/core/skillv2/builtin"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinPresentationLocaleAndCanonicalPreservation(t *testing.T) {
	useBuiltinCatalog(t, b.Catalog{SchemaVersion: 1, Skills: []b.CatalogSkill{{Key: "demo", UID: "uid", SourceURL: "https://example.test/demo.zip", ResolvedURL: "https://example.test/demo.zip", Version: "1.0.0", Name: "demo", Description: "original", Category: "tools", Tags: []string{"tools"}, Content: "# Original", ArchiveSHA256: strings.Repeat("a", 64), TreeSHA256: strings.Repeat("b", 64), ArchiveSize: 1, PackageFile: "packages/demo.zip"}}})
	p := b.Presentation{SchemaVersion: 1, Skills: map[string]b.SkillPresentation{"uid": {PresentationID: "stable", Locales: map[string]b.PresentationText{"zh-CN": {Name: "名称", Description: "描述"}}}}, TagLabels: map[string]map[string]string{"zh-CN": {"tools": "工具"}}}
	data, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(filepath.Dir(b.CatalogPath()), "presentation.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	db := testutil.NewTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, locale := range []string{"zh-CN", "en-US"} {
		req := httptest.NewRequest(http.MethodGet, "/api/core/builtin-skills", nil)
		req.Header.Set("Accept-Language", locale)
		rec := httptest.NewRecorder()
		ListBuiltinSkills(rec, req)
		if rec.Code != 200 || rec.Header().Get("Content-Language") != locale || !strings.Contains(rec.Header().Get("Vary"), "Accept-Language") {
			t.Fatalf("bad response %d %s", rec.Code, rec.Body.String())
		}
		var response struct {
			Data struct {
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		item := response.Data.Items[0]
		if item["search_text"] != "名称 描述" {
			t.Fatalf("localized search text missing for %s: %#v", locale, item)
		}
		if item["name"] != "demo" || item["description"] != "original" || item["content"] != "# Original" {
			t.Fatal("canonical metadata changed")
		}
		want := "demo"
		if locale == "zh-CN" {
			want = "名称"
			if item["tag_labels"].(map[string]any)["tools"] != "工具" {
				t.Fatal("tag label lost")
			}
		}
		if item["display_name"] != want || item["display_locale"] != locale {
			t.Fatalf("bad display: %#v", item)
		}
	}
}
