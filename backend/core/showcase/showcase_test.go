package showcase

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	skillbuiltin "lazymind/core/skillv2/builtin"
)

func TestListCasesDoesNotExposeUnconfiguredLegacyCases(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/core/showcase/cases", nil)
	req.Header.Set("Accept-Language", "en-US")
	rec := httptest.NewRecorder()

	ListCases(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Language"); got != "en-US" {
		t.Fatalf("expected Content-Language en-US, got %q", got)
	}
	if !strings.Contains(strings.ToLower(rec.Header().Get("Vary")), "accept-language") {
		t.Fatalf("expected Vary to include Accept-Language, got %q", rec.Header().Get("Vary"))
	}

	var payload ShowcaseCaseListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Total != 0 || len(payload.Cases) != 0 {
		t.Fatalf("expected no cases without a compiled catalog, got %#v", payload.Cases)
	}
	if len(payload.Categories) != 1 || payload.Categories[0] != "All" {
		t.Fatalf("expected only the localized all category, got %#v", payload.Categories)
	}
}

func TestGetCaseDoesNotFallbackToUnconfiguredCase(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/core/showcase/cases/unconfigured", nil)
	req = mux.SetURLVars(req, map[string]string{"case_id": "unconfigured"})
	rec := httptest.NewRecorder()

	GetCase(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidateFeaturedBindingsRequiresExactHiddenPackage(t *testing.T) {
	visible := false
	entry := skillbuiltin.CatalogSkill{
		Key: "advisor", UID: "bsk_advisor", SourceURL: "https://example.test/advisor.zip",
		ResolvedURL: "https://example.test/advisor.zip", Version: "1.0.0", Name: "advisor",
		Description: "advisor", Category: "external", MarketVisible: &visible,
		ArchiveSHA256: strings.Repeat("a", 64), TreeSHA256: strings.Repeat("b", 64),
		ArchiveSize: 1, PackageFile: "packages/advisor.zip",
	}
	builtinCatalogPath := filepath.Join(t.TempDir(), "catalog.json")
	body, err := json.Marshal(skillbuiltin.Catalog{SchemaVersion: skillbuiltin.CatalogSchemaVersion, Skills: []skillbuiltin.CatalogSkill{entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(builtinCatalogPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	featured := Catalog{Cases: []FeaturedDefinition{{
		ID: "advisor",
		Skill: FeaturedSkillBinding{
			SourceURL: entry.SourceURL, BuiltinSkillUID: entry.UID,
			Version: entry.Version, ArchiveSHA256: entry.ArchiveSHA256,
		},
	}}}
	if err := validateFeaturedBindings(featured, builtinCatalogPath); err != nil {
		t.Fatal(err)
	}
	featured.Cases[0].Skill.ArchiveSHA256 = strings.Repeat("c", 64)
	if err := validateFeaturedBindings(featured, builtinCatalogPath); err == nil {
		t.Fatal("mismatched featured binding was accepted")
	}
}
