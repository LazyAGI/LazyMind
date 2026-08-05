package showcase

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListCasesLocalizesEnglishResponse(t *testing.T) {
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
	if len(payload.Cases) != len(showcaseCases) {
		t.Fatalf("expected %d cases, got %d", len(showcaseCases), len(payload.Cases))
	}
	if payload.Categories[0] != "All" {
		t.Fatalf("expected localized first category, got %#v", payload.Categories)
	}
	if payload.Cases[0].Title != showcaseCaseTranslationsEnUS[payload.Cases[0].ID].Title {
		t.Fatalf("expected localized title, got %q", payload.Cases[0].Title)
	}
	if len(payload.Cases[0].Steps) != 5 {
		t.Fatalf("expected the product case to expose 5 localized steps, got %d", len(payload.Cases[0].Steps))
	}
	if len(payload.Cases[0].Tasks) != 4 {
		t.Fatalf("expected the product case to expose 4 localized tasks, got %d", len(payload.Cases[0].Tasks))
	}
	if len(payload.Cases[0].SecondaryOptions) != 2 || payload.Cases[0].SecondaryOptions[1].Prompt == "" {
		t.Fatalf("expected localized secondary options with prompts, got %#v", payload.Cases[0].SecondaryOptions)
	}

	for _, item := range payload.Cases {
		if item.ID == "knowledgeQa" && len(item.SecondaryOptions) != 0 {
			t.Fatalf("expected knowledge Q&A to have no secondary options, got %#v", item.SecondaryOptions)
		}
	}
}
