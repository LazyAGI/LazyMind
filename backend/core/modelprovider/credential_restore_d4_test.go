package modelprovider

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

type restoreMemoryKeyStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (store *restoreMemoryKeyStore) key(scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) string {
	return scope.CloudIssuer + "|" + scope.CloudAccountID + "|" + string(kind)
}
func (store *restoreMemoryKeyStore) Load(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, found := store.values[store.key(scope, kind)]
	if !found {
		return nil, credentialvault.ErrLocalKeyNotFound
	}
	return append([]byte(nil), value...), nil
}
func (store *restoreMemoryKeyStore) Save(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind, value []byte) error {
	store.mu.Lock()
	store.values[store.key(scope, kind)] = append([]byte(nil), value...)
	store.mu.Unlock()
	return nil
}
func (store *restoreMemoryKeyStore) Delete(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) error {
	store.mu.Lock()
	delete(store.values, store.key(scope, kind))
	store.mu.Unlock()
	return nil
}

func newCredentialRestoreSinkFixture(t *testing.T) (*CredentialRestoreSink, *gorm.DB, credentialvault.AccountScope) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &credentialvault.CredentialBinding{}); err != nil {
		t.Fatal(err)
	}
	provider := orm.UserModelProvider{
		ID: "restore-provider", DefaultModelProviderID: "fixture-provider", Name: "Fixture Provider",
		BaseURL: "https://provider.example.test", Category: "model", Capabilities: "multi_group,custom_base_url",
	}
	provider.CreateUserID = "local-user"
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	keys, err := credentialvault.NewLocalKeyManager(&restoreMemoryKeyStore{values: make(map[string][]byte)}, bytes.NewReader(bytes.Repeat([]byte{0x51}, 4096)))
	if err != nil {
		t.Fatal(err)
	}
	restoreGlobal := SetCredentialKeyManager(keys)
	t.Cleanup(restoreGlobal)
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	sink, err := NewCredentialRestoreSink(db, keys, "local-user", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return sink, db, credentialvault.AccountScope{CloudIssuer: "https://cloud.example.test", CloudAccountID: "00000000-0000-7000-8000-000000000101"}
}

func TestTrustedRestorePersistsCredentialV2AndBindingAtomicallyWithoutPlaintext(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	restored := credentialvault.RestoredCredential{
		RecordID: "00000000-0000-7000-8000-000000000401", Revision: 7,
		VaultID: "00000000-0000-7000-8000-000000000201", ETag: strings.Repeat("e", 64),
		Provider: credentialvault.ProviderCredential{
			ProviderCatalogKey: "fixture-provider", DisplayName: "Restored Fixture", BaseURL: "https://provider.example.test",
			APIKey: []byte("fixture-restored-api-key"), CredentialRevision: 1,
		},
	}
	if err := sink.PersistTrusted(context.Background(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}, time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("persist trusted restored credential: %v", err)
	}
	var group orm.UserModelProviderGroup
	if err := db.Take(&group).Error; err != nil {
		t.Fatal(err)
	}
	if group.APIKey != "" || group.CredentialVersion != modelProviderCredentialVersion || group.CredentialRevision != 1 ||
		strings.Contains(group.APIKeyCiphertext, "fixture-restored-api-key") {
		t.Fatalf("trusted restore stored plaintext/invalid envelope: %#v", group)
	}
	apiKey, err := apiKeyForGroup(db, &group)
	if err != nil || apiKey != "fixture-restored-api-key" {
		t.Fatalf("decrypt restored local credential = %q, %v", apiKey, err)
	}
	var binding credentialvault.CredentialBinding
	if err := db.Take(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if binding.CloudIssuer != scope.CloudIssuer || binding.CloudAccountID != scope.CloudAccountID ||
		binding.CloudRecordID != restored.RecordID || binding.LastCloudRevision != restored.Revision || binding.LocalProviderGroupID != group.ID ||
		binding.LastETag != strings.Repeat("e", 64) {
		t.Fatalf("restored binding = %+v", binding)
	}
}

func TestSecureStoreFailureRollsBackTrustedRestoreWithoutPartialBinding(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	sink.keys = nil
	restored := credentialvault.RestoredCredential{
		RecordID: "00000000-0000-7000-8000-000000000401", Revision: 7,
		VaultID: "00000000-0000-7000-8000-000000000201", ETag: strings.Repeat("e", 64),
		Provider: credentialvault.ProviderCredential{ProviderCatalogKey: "fixture-provider", APIKey: []byte("fixture-restored-api-key"), CredentialRevision: 1},
	}
	if err := sink.PersistTrusted(context.Background(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}, time.Now()); !errors.Is(err, credentialvault.ErrLocalSecureStoreUnavailable) {
		t.Fatalf("secure-store failure = %v", err)
	}
	var groups, bindings int64
	_ = db.Model(&orm.UserModelProviderGroup{}).Count(&groups).Error
	_ = db.Model(&credentialvault.CredentialBinding{}).Count(&bindings).Error
	if groups != 0 || bindings != 0 {
		t.Fatalf("failed trusted restore left groups/bindings = %d/%d", groups, bindings)
	}
}

func TestTemporaryRestoreUsesMemoryOnlyAndClearsCredentialBytes(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	restored := credentialvault.RestoredCredential{
		RecordID: "00000000-0000-7000-8000-000000000401", Revision: 7,
		VaultID: "00000000-0000-7000-8000-000000000201", ETag: strings.Repeat("e", 64),
		Provider: credentialvault.ProviderCredential{ProviderCatalogKey: "fixture-provider", APIKey: []byte("fixture-temporary-api-key")},
	}
	if err := sink.ActivateTemporary(context.Background(), scope, []credentialvault.RestoredCredential{restored}, time.Now().Add(credentialvault.TemporaryRestoreAbsoluteTTL)); err != nil {
		t.Fatalf("activate temporary credential: %v", err)
	}
	resolved, found := sink.TemporaryCredential(restored.RecordID)
	if !found || string(resolved.APIKey) != "fixture-temporary-api-key" {
		t.Fatalf("temporary credential = %+v/%t", resolved, found)
	}
	var groups, bindings int64
	_ = db.Model(&orm.UserModelProviderGroup{}).Count(&groups).Error
	_ = db.Model(&credentialvault.CredentialBinding{}).Count(&bindings).Error
	if groups != 0 || bindings != 0 {
		t.Fatalf("temporary restore wrote groups/bindings = %d/%d", groups, bindings)
	}
	if err := sink.ClearTemporary(context.Background(), scope); err != nil {
		t.Fatalf("clear temporary credentials: %v", err)
	}
	if _, found := sink.TemporaryCredential(restored.RecordID); found {
		t.Fatal("temporary credential remained after lock/logout cleanup")
	}
}
