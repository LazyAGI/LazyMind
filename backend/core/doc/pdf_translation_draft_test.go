package doc

import (
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestRevisionProvenanceUsesTheCurrentTranslationSource(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:revision_provider?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	provider := orm.UserModelProvider{ID: "google-provider", Name: "Google Translate Web", Category: "translation",
		BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}}
	group := orm.UserModelProviderGroup{ID: "google-group", UserModelProviderID: provider.ID, BaseURL: "https://translate.googleapis.com/",
		BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}}
	selection := orm.UserSelectedProvider{UserID: "user", Category: "translation", UserModelProviderGroupID: group.ID,
		CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{&provider, &group, &selection} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	base := pdfArtifactRecord{ProviderType: "api", Provider: "DeepL", Model: "legacy-model"}
	current, err := resolvePDFRevisionProvenance(t.Context(), db, "user", base, "api", "Google Translate Web")
	if err != nil {
		t.Fatal(err)
	}
	if current.ProviderType != "api" || current.Provider != "Google Translate Web" || current.Model != "" {
		t.Fatalf("revised provenance retained the old source: %+v", current)
	}
	if _, err := resolvePDFRevisionProvenance(t.Context(), db, "user", base, "api", "DeepL"); !errors.Is(err, errPDFRevisionSourceChanged) {
		t.Fatalf("stale source was accepted: %v", err)
	}
	if err := db.Delete(&selection).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := loadCurrentPDFTranslationSnapshot(t.Context(), db, "user"); !errors.Is(err, errPDFTranslationSourceMissing) {
		t.Fatalf("missing source was accepted for retranslation: %v", err)
	}
	if _, err := resolvePDFRevisionProvenance(t.Context(), db, "user", base, "api", "Google Translate Web"); !errors.Is(err, errPDFTranslationSourceMissing) {
		t.Fatalf("missing source was accepted for revision: %v", err)
	}
}

func TestManualPDFRevisionPreservesExistingProvenance(t *testing.T) {
	base := pdfArtifactRecord{ProviderType: "api", Provider: "DeepL", Model: "legacy-model"}
	current, err := resolvePDFRevisionProvenance(t.Context(), nil, "user", base, "", "")
	if err != nil || current.ProviderType != base.ProviderType || current.Provider != base.Provider || current.Model != base.Model {
		t.Fatalf("manual revision provenance = %+v, %v", current, err)
	}
}
