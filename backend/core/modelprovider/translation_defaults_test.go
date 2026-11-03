package modelprovider

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestFreeTranslationServicesAreEnabledOnceAndPreserveChosenDefault(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.UserSelectedProvider{}); err != nil {
		t.Fatal(err)
	}
	names := []string{"Google Translate Web", "Bing Translate Web", "Sogou Translate Web", "DeepL Web", "Tencent TranSmart Web"}
	for _, name := range names {
		provider := orm.DefaultModelProvider{ID: name, Name: name, Category: "translation", Capabilities: "keyless_translation", BaseURL: "https://example.invalid/", CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err := db.Create(&provider).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := EnsureDefaultTranslationServices(t.Context(), db, "reader"); err != nil {
			t.Fatal(err)
		}
	}
	var groups []orm.UserModelProviderGroup
	if err := db.Where("create_user_id = ? AND is_verified = ?", "reader", true).Find(&groups).Error; err != nil || len(groups) != 5 {
		t.Fatalf("expected five enabled connections, groups=%+v, err=%v", groups, err)
	}
	var selected orm.UserSelectedProvider
	if err := db.Where("user_id = ? AND category = ?", "reader", "translation").Take(&selected).Error; err != nil {
		t.Fatal(err)
	}
	var defaultGroup orm.UserModelProviderGroup
	if err := db.Where("id = ?", selected.UserModelProviderGroupID).Take(&defaultGroup).Error; err != nil || defaultGroup.Name != "Google Translate Web" {
		t.Fatalf("initial default = %+v, err=%v", defaultGroup, err)
	}
	if err := db.Model(&selected).Update("user_model_provider_group_id", groups[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefaultTranslationServices(t.Context(), db, "reader"); err != nil {
		t.Fatal(err)
	}
	var after orm.UserSelectedProvider
	if err := db.First(&after, selected.ID).Error; err != nil || after.UserModelProviderGroupID != groups[0].ID {
		t.Fatalf("chosen default changed: %+v, err=%v", after, err)
	}
	// A default whose source has been removed must not leave selection translation broken.
	if err := db.Model(&selected).Update("user_model_provider_group_id", "removed-connection").Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefaultTranslationServices(t.Context(), db, "reader"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&after, selected.ID).Error; err != nil || after.UserModelProviderGroupID != defaultGroup.ID {
		t.Fatalf("unavailable default was not replaced by the free default: %+v, err=%v", after, err)
	}
}
