package modelconfig

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
	"strings"
	"testing"
)

func TestReadingParserSelectionAuthorization(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	for _, v := range []struct {
		owner, id, category string
		shared              bool
	}{{"user", "own", "ocr", false}, {"other", "shared", "ocr", true}, {"other", "private", "ocr", false}, {"user", "wrong", "search", false}} {
		for _, row := range []any{&orm.UserModelProvider{ID: v.id, Name: "MinerU", Category: v.category, BaseModel: orm.BaseModel{CreateUserID: v.owner}}, &orm.UserModelProviderGroup{ID: v.id, Name: v.id, UserModelProviderID: v.id, IsVerified: true, BaseURL: "https://" + v.id + ".invalid", APIKey: "secret", BaseModel: orm.BaseModel{CreateUserID: v.owner}}} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if v.shared {
			if err := db.Create(&orm.UserSelectedProvider{UserID: v.owner, Category: "ocr", UserModelProviderGroupID: v.id, Share: true}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Create(&orm.UserSelectedProvider{UserID: "user", Category: "ocr", UserModelProviderGroupID: "own"}).Error; err != nil {
		t.Fatal(err)
	}
	// Existing own defaults are selectable even if their verification flag is old.
	db.Model(&orm.UserModelProviderGroup{}).Where("id=?", "own").Update("is_verified", false)
	options, err := ReadingParserOptions(t.Context(), db, "user")
	if err != nil || len(options) != 2 {
		t.Fatalf("options=%v err=%v", options, err)
	}
	for _, ref := range []string{"own:own", "shared:shared"} {
		cfg, err := ResolveReadingParser(t.Context(), db, "user", ref)
		if err != nil || cfg["ocr_url"] != "https://"+strings.SplitN(ref, ":", 2)[1]+".invalid" {
			t.Fatalf("%s: %v", ref, err)
		}
	}
	for _, ref := range []string{"own:private", "shared:private", "own:wrong", "shared:own"} {
		if _, err := ResolveReadingParser(t.Context(), db, "user", ref); err == nil {
			t.Fatalf("accepted %s", ref)
		}
	}
	inherited, err := ResolveReadingParser(t.Context(), db, "user", "")
	if err != nil || inherited["ocr_url"] != "https://own.invalid" {
		t.Fatal("explicit selection changed inherited parser")
	}
	db.Model(&orm.UserSelectedProvider{}).Where("user_id=?", "other").Update("share", false)
	if _, err := ResolveReadingParser(t.Context(), db, "user", "shared:shared"); err == nil {
		t.Fatal("revoked parser remained executable")
	}
}
func TestReadingStudySelectionIndependentAndRevocable(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedChatRuntimeModel(t, db, "user", "default", true, true)
	seedChatRuntimeModel(t, db, "user", "chosen", true, false)
	seedChatRuntimeModel(t, db, "other", "shared", true, true)
	seedChatRuntimeModel(t, db, "other", "private", true, false)
	for _, ref := range []string{"own:chosen", "shared:shared"} {
		m, err := ResolveReadingStudyModel(t.Context(), db, "user", ref)
		if err != nil || ChatCandidateID(m.Source, m.ID) != ref {
			t.Fatalf("%s %v", ref, err)
		}
	}
	for _, ref := range []string{"own:private", "shared:chosen"} {
		if _, err := ResolveReadingStudyModel(t.Context(), db, "user", ref); err == nil {
			t.Fatalf("accepted %s", ref)
		}
	}
	m, err := ResolveReadingStudyModel(t.Context(), db, "user", "")
	if err != nil || m.ID != "default" {
		t.Fatalf("default mutated %v %v", m, err)
	}
	db.Model(&orm.UserSelectedModel{}).Where("user_id=?", "other").Update("share", false)
	if _, err := ResolveReadingStudyModel(t.Context(), db, "user", "shared:shared"); err == nil {
		t.Fatal("revocation ignored")
	}
}

func TestReadingExplicitCloudCandidateDoesNotChangeDefault(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	provider := &CloudRuntimeProvider{Session: runtimeTestTokens{}, Client: &runtimeTestClient{}}
	SetRuntimeProvider(provider)
	modelprovider.SetCloudCatalogProvider(provider)
	t.Cleanup(func() { SetRuntimeProvider(nil); modelprovider.SetCloudCatalogProvider(nil) })
	options, err := ReadingStudyOptions(t.Context(), db, "user")
	if err != nil || len(options) != 1 || options[0].ID != "cloud:cloud-text" {
		t.Fatalf("cloud options %v %v", options, err)
	}
	selected, err := ResolveReadingStudyModel(t.Context(), db, "user", "cloud:cloud-text")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &ChatRuntimeConfig{}
	if candidate := runtime.PrepareCandidate(t.Context(), *selected); candidate == nil || candidate.CandidateID != "cloud:cloud-text" {
		t.Fatal("explicit cloud could not execute")
	}
	var count int64
	db.Model(&orm.UserSelectedCloudModel{}).Where("user_id=?", "user").Count(&count)
	if count != 0 {
		t.Fatal("reader changed global cloud selection")
	}
	payload, _ := json.Marshal(options)
	if strings.Contains(string(payload), "api_key") || strings.Contains(string(payload), "access_token") || strings.Contains(string(payload), "base_url") {
		t.Fatal("unsafe public options")
	}
}
