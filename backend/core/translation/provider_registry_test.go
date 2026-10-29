package translation

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
)

func TestDocumentProviderSnapshotUsesRequestedOwnedGroupWithoutChangingDefault(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:document_provider_by_group?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, item := range []struct {
		id, name, owner string
		verified        bool
	}{
		{"google", "Google Translate Web", "user", true},
		{"baidu", "Baidu Translation", "user", true},
		{"memory", "MyMemory", "user", true},
		{"disabled", "Bing Translate Web", "user", false},
		{"someone-else", "DeepL Web", "other", true},
	} {
		provider := orm.UserModelProvider{ID: item.id, Name: item.name, Category: "translation",
			BaseModel: orm.BaseModel{CreateUserID: item.owner, CreatedAt: now, UpdatedAt: now}}
		group := orm.UserModelProviderGroup{ID: item.id, UserModelProviderID: item.id, Name: item.name,
			BaseURL: "https://example.invalid/", IsVerified: item.verified,
			BaseModel: orm.BaseModel{CreateUserID: item.owner, CreatedAt: now, UpdatedAt: now}}
		if item.id == "baidu" {
			group.APIKey = "server-secret"
		}
		if err := db.Create(&provider).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
	}
	selection := orm.UserSelectedProvider{UserID: "user", Category: "translation", UserModelProviderGroupID: "google", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&selection).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, supported, err := DocumentProviderSnapshot(t.Context(), db, "user", "baidu")
	if err != nil || !supported || snapshot == nil || snapshot.GroupID != "baidu" || snapshot.ProviderName != "Baidu Translation" {
		t.Fatalf("requested provider snapshot = %+v, supported=%v, error=%v", snapshot, supported, err)
	}
	defaultSnapshot, err := modelconfig.LoadTranslationConfigSnapshot(t.Context(), db, "user")
	if err != nil || defaultSnapshot.GroupID != "google" {
		t.Fatalf("global default changed: %+v, %v", defaultSnapshot, err)
	}
	for _, groupID := range []string{"memory", "disabled", "someone-else", "missing"} {
		if got, ok, err := DocumentProviderSnapshot(t.Context(), db, "user", groupID); err != nil || ok || got != nil {
			t.Fatalf("unavailable provider %s returned %+v, supported=%v, error=%v", groupID, got, ok, err)
		}
	}
}

func TestDocumentProviderOptionsExcludeTextOnlyAndUnknownSources(t *testing.T) {
	groups := []modelconfig.TranslationProviderGroup{
		{GroupID: "google", GroupName: "Google connection", ProviderName: "Google Translate Web"},
		{GroupID: "memory", GroupName: "MyMemory connection", ProviderName: "MyMemory"},
		{GroupID: "unknown", GroupName: "Unknown connection", ProviderName: "Unknown Translation"},
	}
	options := documentProviderOptions(groups)
	if len(options) != 1 || options[0].GroupID != "google" ||
		options[0].ProviderName != "Google Translate Web" || options[0].GroupName != "Google connection" {
		t.Fatalf("document translation options = %+v", options)
	}
}

func TestTranslationProviderCatalogIncludesFreeSources(t *testing.T) {
	for _, name := range []string{
		"Google Translate Web", "Tencent TranSmart Web",
		"Bing Translate Web", "Sogou Translate Web", "DeepL Web",
		"Baidu Translation", "Niutrans", "Aliyun Translation",
	} {
		if _, ok := lookupProvider(name); !ok {
			t.Errorf("missing translation provider %q", name)
		}
	}
}

func TestTranslationProviderLimits(t *testing.T) {
	baidu, ok := lookupProvider("Baidu Translation")
	if !ok {
		t.Fatal("Baidu provider missing")
	}
	if baidu.MaxChunkRunes > 1000 || baidu.MinInterval < 1000000000 {
		t.Fatalf("Baidu limits: %+v", baidu)
	}
	for _, name := range []string{"Bing Translate Web", "Sogou Translate Web", "DeepL Web"} {
		provider, ok := lookupProvider(name)
		if !ok || !provider.Keyless || !provider.SupportsDocuments || provider.MaxChunkRunes <= 0 || provider.MinInterval <= 0 {
			t.Fatalf("web provider %q has invalid limits: %+v, found=%v", name, provider, ok)
		}
	}
}

func TestRemovedAndPausedTranslationProvidersAreUnavailable(t *testing.T) {
	for _, name := range []string{"MyMemory", "Tencent Translation", "Volcengine Translation", "Microsoft Translator", "Google Cloud Translation", "Youdao Translation", "DeepL", "Caiyun Translation", "Tencent TokenHub Translation"} {
		if _, ok := lookupProvider(name); ok {
			t.Errorf("inactive provider %q is still selectable", name)
		}
	}
}
