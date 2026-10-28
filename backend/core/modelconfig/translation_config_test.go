package modelconfig

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestLoadTranslationConfigAllowsOnlyKnownKeylessSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"Google Translate Web", true},
		{"Tencent TranSmart Web", true},
		{"MyMemory", true},
		{"Bing Translate Web", true},
		{"Sogou Translate Web", true},
		{"DeepL Web", true},
		{"Unknown Translation", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{}); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			provider := orm.UserModelProvider{ID: "provider", Name: tc.name, Category: "translation", BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}}
			group := orm.UserModelProviderGroup{ID: "group", UserModelProviderID: "provider", Name: tc.name, BaseURL: "https://example.invalid/", APIKey: "", BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}}
			selection := orm.UserSelectedProvider{UserID: "user", Category: "translation", UserModelProviderGroupID: "group", CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&provider).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&group).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&selection).Error; err != nil {
				t.Fatal(err)
			}
			config, err := LoadTranslationConfig(t.Context(), db, "user")
			if err != nil {
				t.Fatal(err)
			}
			if (config != nil) != tc.want {
				t.Fatalf("config = %#v, want configured=%v", config, tc.want)
			}
		})
	}
}

func TestTranslationConfigSnapshotUsesRevocableGroupReference(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:translation_snapshot?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, name := range []string{"Baidu Translation", "Google Translate Web"} {
		provider := orm.UserModelProvider{ID: name, Name: name, Category: "translation", BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}}
		if err := db.Create(&provider).Error; err != nil {
			t.Fatal(err)
		}
		group := orm.UserModelProviderGroup{ID: name, UserModelProviderID: name, Name: name, BaseURL: "https://example.invalid/", CredentialRevision: 1,
			BaseModel: orm.BaseModel{CreateUserID: "user", CreatedAt: now, UpdatedAt: now}}
		if name == "Baidu Translation" {
			group.APIKey = "private-test-key"
		}
		if err := db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
	}
	selection := orm.UserSelectedProvider{UserID: "user", Category: "translation", UserModelProviderGroupID: "Baidu Translation", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&selection).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadTranslationConfigSnapshot(t.Context(), db, "user")
	if err != nil || snapshot == nil {
		t.Fatalf("capture snapshot: %+v, %v", snapshot, err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-test-key") || strings.Contains(string(raw), "api_key") ||
		strings.Contains(string(raw), "ciphertext") || strings.Contains(string(raw), "https://example.invalid/") {
		t.Fatalf("snapshot contains credential material: %s", raw)
	}
	if snapshot.GroupID != "Baidu Translation" || snapshot.CredentialRevision != 1 {
		t.Fatalf("snapshot reference: %+v", snapshot)
	}
	if err := db.Model(&selection).Update("user_model_provider_group_id", "Google Translate Web").Error; err != nil {
		t.Fatal(err)
	}
	config, err := ResolveTranslationConfigSnapshot(t.Context(), db, "user", *snapshot)
	if err != nil || config.ProviderName != "Baidu Translation" || config.APIKey != "private-test-key" {
		t.Fatalf("resolved snapshot: %+v, %v", config, err)
	}
	current, err := LoadTranslationConfigSnapshot(t.Context(), db, "user")
	if err != nil || current.ProviderName != "Google Translate Web" || current.CacheIdentity() == snapshot.CacheIdentity() {
		t.Fatalf("current selection: %+v, %v", current, err)
	}
	if _, err := ResolveTranslationConfigSnapshot(t.Context(), db, "another-user", *snapshot); err == nil {
		t.Fatal("another user resolved the group")
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", snapshot.GroupID).Update("base_url", "https://changed.invalid/").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTranslationConfigSnapshot(t.Context(), db, "user", *snapshot); !errors.Is(err, ErrTranslationProviderReferenceChanged) {
		t.Fatal("changed endpoint resolved from stale reference")
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", snapshot.GroupID).Update("base_url", "https://example.invalid/").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", snapshot.GroupID).Update("credential_revision", 2).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTranslationConfigSnapshot(t.Context(), db, "user", *snapshot); !errors.Is(err, ErrTranslationProviderReferenceChanged) {
		t.Fatal("revised credential resolved from stale reference")
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", snapshot.GroupID).Update("credential_revision", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", snapshot.GroupID).Update("deleted_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTranslationConfigSnapshot(t.Context(), db, "user", *snapshot); !errors.Is(err, ErrTranslationProviderReferenceChanged) {
		t.Fatal("deleted group resolved from stale reference")
	}
}

func TestListTranslationProviderGroupsOnlyReturnsOwnedVerifiedConfiguredSources(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:translation_available_groups?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, item := range []struct {
		id, name, category, owner, apiKey string
		verified                          bool
	}{
		{"google", "Google Translate Web", "translation", "user", "", true},
		{"baidu", "Baidu Translation", "translation", "user", "private-test-key", true},
		{"no-key", "DeepL", "translation", "user", "", true},
		{"broken-key", "Caiyun Translation", "translation", "user", "", true},
		{"unverified", "Bing Translate Web", "translation", "user", "", false},
		{"other-user", "Sogou Translate Web", "translation", "other", "", true},
		{"wrong-category", "Google Translate Web", "search", "user", "", true},
	} {
		provider := orm.UserModelProvider{ID: item.id, Name: item.name, Category: item.category,
			BaseModel: orm.BaseModel{CreateUserID: item.owner, CreatedAt: now, UpdatedAt: now}}
		group := orm.UserModelProviderGroup{ID: item.id, UserModelProviderID: item.id, Name: item.name + " connection", BaseURL: "https://example.invalid/",
			APIKey: item.apiKey, IsVerified: item.verified,
			BaseModel: orm.BaseModel{CreateUserID: item.owner, CreatedAt: now, UpdatedAt: now}}
		if err := db.Create(&provider).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "broken-key").
		Update("api_key_ciphertext", "invalid-test-ciphertext").Error; err != nil {
		t.Fatal(err)
	}
	groups, err := ListTranslationProviderGroups(t.Context(), db, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].GroupID != "baidu" || groups[1].GroupID != "google" {
		t.Fatalf("available groups = %+v", groups)
	}
	if groups[0].ProviderName != "Baidu Translation" || groups[0].GroupName != "Baidu Translation connection" ||
		groups[0].Snapshot.GroupID != "baidu" {
		t.Fatalf("available group metadata = %+v", groups[0])
	}
	raw, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-test-key") || strings.Contains(string(raw), "api_key") {
		t.Fatalf("group listing exposed credentials: %s", raw)
	}
}
