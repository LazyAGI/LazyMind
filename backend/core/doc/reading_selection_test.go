package doc

import (
	"bytes"
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadingPreferencesDeletedSelectionCanBePreservedOrCleared(t *testing.T) {
	db := newDocumentTestDB(t)
	p := readingpolicy.Defaults()
	p.ParserRef = "own:deleted"
	p.StudyModelRef = "own:deleted"
	if _, err := readingpolicy.Save(t.Context(), db.DB, "owner", p); err != nil {
		t.Fatal(err)
	}
	call := func(p readingpolicy.Policy) int {
		b, _ := json.Marshal(p)
		r := httptest.NewRequest("PUT", "/", bytes.NewReader(b))
		r.Header.Set("X-User-Id", "owner")
		w := httptest.NewRecorder()
		ReadingPreferences(w, r)
		if strings.Contains(w.Body.String(), "api_key") {
			t.Fatal("unsafe metadata")
		}
		return w.Code
	}
	p.Preset = "advance"
	if code := call(p); code != 200 {
		t.Fatalf("unchanged unavailable selection rejected: %d", code)
	}
	p.StudyModelRef = "own:new-unknown"
	if code := call(p); code != 400 {
		t.Fatalf("unknown selection accepted: %d", code)
	}
	p.StudyModelRef = ""
	p.ParserRef = ""
	if code := call(p); code != 200 {
		t.Fatalf("inherit rejected: %d", code)
	}
	got, err := readingpolicy.Load(t.Context(), db.DB, "owner")
	if err != nil || got.ParserRef != "" || got.StudyModelRef != "" {
		t.Fatalf("clear not saved: %+v %v", got, err)
	}
}
func TestReadingStudyOverrideSharedByManualAndAutomaticExecution(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "p", Name: "OpenAI", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", BaseURL: "https://fixture.invalid/v1", APIKey: "secret", IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserModelProviderGroupModel{ID: "a", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "global", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserModelProviderGroupModel{ID: "b", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "study", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "a"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := readingpolicy.Defaults()
	p.StudyModelRef = "own:b"
	if _, err := readingpolicy.Save(t.Context(), db.DB, "owner", p); err != nil {
		t.Fatal(err)
	}
	_, identity, name, err := loadFormulaExplanationModel(t.Context(), "owner")
	if err != nil || name != "study" {
		t.Fatalf("manual %s %v", name, err)
	}
	_, autoIdentity, autoName, err := loadFormulaExplanationModel(readingpolicy.AutomaticContext(t.Context()), "owner")
	if err != nil || autoName != name || autoIdentity != identity {
		t.Fatalf("automatic selection differs: %s %v", autoName, err)
	}
	db.Delete(&orm.UserModelProviderGroupModel{}, "id=?", "b")
	if _, _, _, err := loadFormulaExplanationModel(t.Context(), "owner"); err == nil {
		t.Fatal("deleted override fell back to global")
	}
}
