package modelprovider

import (
	"context"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

func customRestorePayload(record, name, endpoint, key string) credentialvault.RestoredCredential {
	return credentialvault.RestoredCredential{RecordID: record, Revision: 1, VaultID: "00000000-0000-7000-8000-000000000201", ETag: strings.Repeat("e", 64), Provider: credentialvault.ProviderCredential{ProviderCatalogKey: "fixture-provider", DisplayName: name, BaseURL: endpoint, APIKey: []byte(key)}}
}

func TestRestoreCustomSuppliersSharingCatalogRemainSeparateAndIdempotent(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	rows := []credentialvault.RestoredCredential{customRestorePayload("00000000-0000-7000-8000-000000000401", "Proxy A", "https://a.example/v1", "key-a"), customRestorePayload("00000000-0000-7000-8000-000000000402", "Proxy B", "https://b.example/v1", "key-b")}
	resolutions := map[string]credentialvault.ConflictResolution{rows[0].RecordID: credentialvault.ConflictFail, rows[1].RecordID: credentialvault.ConflictFail}
	for i := 0; i < 2; i++ {
		if err := sink.PersistTrusted(context.Background(), scope, rows, resolutions, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var providers []orm.UserModelProvider
	if err := db.Where("is_custom = ?", true).Order("name").Find(&providers).Error; err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("custom suppliers merged or duplicated: %d", len(providers))
	}
	for i, p := range providers {
		if p.Name != rows[i].Provider.DisplayName || p.BaseURL != rows[i].Provider.BaseURL || RuntimeProviderName(p) != "Fixture Provider" {
			t.Fatal("restore lost custom supplier identity")
		}
		var groups []orm.UserModelProviderGroup
		db.Where("user_model_provider_id = ? AND deleted_at IS NULL", p.ID).Find(&groups)
		if len(groups) != 1 {
			t.Fatal("restore created multiple private groups")
		}
		key, err := apiKeyForGroup(db, &groups[0])
		if err != nil || key != string(rows[i].Provider.APIKey) {
			t.Fatal("restore crossed custom supplier keys", err)
		}
	}
}

func TestBoundCustomRestoreKeepsLocallyEditedEndpoint(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	row := customRestorePayload("00000000-0000-7000-8000-000000000401", "Proxy A", "https://a.example/v1", "key-a")
	if err := sink.PersistTrusted(context.Background(), scope, []credentialvault.RestoredCredential{row}, map[string]credentialvault.ConflictResolution{row.RecordID: credentialvault.ConflictFail}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var p orm.UserModelProvider
	db.Where("is_custom = ?", true).Take(&p)
	if err := setProviderEndpoint(context.Background(), db, &p, "https://changed.example/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := sink.PersistTrusted(context.Background(), scope, []credentialvault.RestoredCredential{row}, map[string]credentialvault.ConflictResolution{row.RecordID: credentialvault.ConflictReplaceLocal}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var group orm.UserModelProviderGroup
	db.Where("user_model_provider_id = ?", p.ID).Take(&group)
	if group.BaseURL != "https://changed.example/v1" || group.IsVerified {
		t.Fatal("old backup replaced local endpoint or revived verification")
	}
}

func TestTemporaryCustomRestoreKeepsPayloadIdentityWithoutAddingSupplier(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	row := customRestorePayload("00000000-0000-7000-8000-000000000401", "Proxy A", "https://a.example/v1", "key-a")
	if err := sink.ActivateTemporary(context.Background(), scope, []credentialvault.RestoredCredential{row}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, found := sink.TemporaryCredential(row.RecordID)
	if !found || got.BaseURL != row.Provider.BaseURL || got.DisplayName != row.Provider.DisplayName {
		t.Fatal("temporary restore borrowed catalog identity")
	}
	var n int64
	db.Model(&orm.UserModelProvider{}).Where("is_custom = ?", true).Count(&n)
	if n != 0 {
		t.Fatal("temporary restore persisted a supplier")
	}
}

func TestCustomBackupRestoresAcrossDifferentCatalogIDs(t *testing.T) {
	source := setupListProviderTestDB(t)
	p := orm.UserModelProvider{ID: "custom-source", DefaultModelProviderID: "source-installation-id", Name: "Portable Proxy", ProtocolName: "Fixture Provider", IsCustom: true, Category: "model", BaseURL: "https://portable.example/v1", RequestConfig: `{"default_protocol":"openai","endpoints":{"openai":"https://portable.example/v1","anthropic":"https://anthropic.example"},"defaults":{"openai":"https://portable.example/v1","anthropic":"https://portable.example"}}`, BaseModel: orm.BaseModel{CreateUserID: "local-user"}}
	g := orm.UserModelProviderGroup{ID: "source-group", UserModelProviderID: p.ID, Name: p.Name, BaseURL: p.BaseURL, APIKey: "portable-key", CredentialRevision: 1, BaseModel: p.BaseModel}
	for _, row := range []any{&p, &g} {
		if err := source.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	payload, err := (CredentialBackupSource{DB: source}).LoadCredential(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload.APIKey)
	if payload.ProviderCatalogKey != "Fixture Provider" {
		t.Fatal("backup used installation-specific catalog ID")
	}
	sink, target, scope := newCredentialRestoreSinkFixture(t)
	restored := customRestorePayload("00000000-0000-7000-8000-000000000401", payload.DisplayName, payload.BaseURL, string(payload.APIKey))
	restored.Provider = payload
	if err := sink.PersistTrusted(context.Background(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var row orm.UserModelProvider
	target.Where("is_custom = ?", true).Take(&row)
	if row.DefaultModelProviderID != "fixture-provider" || row.Name != p.Name || row.ProtocolName != "Fixture Provider" || row.RequestConfig != p.RequestConfig {
		t.Fatal("restore did not resolve stable protocol to target catalog identity")
	}
}
