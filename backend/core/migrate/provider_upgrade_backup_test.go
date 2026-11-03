package migrate

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
	"lazymind/core/credentialvault"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

// This closes the upgrade/backup boundary with real SQL migrations and crypto.
// The legacy catalog had a plan endpoint under the brand name; once independent
// service templates arrive, that unchanged built-in connection is rebound in
// place. User-created custom suppliers must not be reclassified by this path.
func TestLegacyEncryptedSupplierUpgradeAndBackupRestore(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			const (
				owner          = "upgrade-owner"
				providerID     = "00000000-0000-7000-8000-000000000801"
				groupID        = "00000000-0000-7000-8000-000000000802"
				modelID        = "00000000-0000-7000-8000-000000000803"
				conversationID = "00000000-0000-7000-8000-000000000804"
				endpoint       = "https://coding.dashscope.aliyuncs.com/v1/"
				pool           = "fixture-upgrade-first\nfixture-upgrade-second"
				secret         = "fixture-upgrade-device-secret"
			)
			t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", secret)
			raw := customProviderMigrationDB(t, driver)
			var dialector gorm.Dialector = gormsqlite.Dialector{Conn: raw}
			if driver == "postgres" {
				dialector = gormpostgres.New(gormpostgres.Config{Conn: raw})
			}
			db, err := gorm.Open(dialector, &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			ciphertext, err := secretcrypto.EncodeAESGCM([]byte(pool), secret)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(ciphertext, []byte("fixture-upgrade-")) {
				t.Fatal("legacy fixture is not encrypted")
			}
			now := time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC)
			base := orm.BaseModel{CreateUserID: owner, CreatedAt: now, UpdatedAt: now}
			// The opaque ID is the persisted historical active-key contract. Its
			// value must survive both schema migration and service rebinding.
			selectedKeyID := fmt.Sprintf("%x", sha256.Sum256([]byte(groupID+"\x00fixture-upgrade-second")))
			for _, fixture := range []struct {
				table string
				row   map[string]any
			}{
				{"default_model_providers", map[string]any{"id": "legacy-qwen", "name": "Qwen", "description": "", "base_url": endpoint, "category": "model", "capabilities": "multi_group,custom_base_url,has_models", "created_at": now, "updated_at": now}},
				{"user_model_providers", map[string]any{"id": providerID, "default_model_provider_id": "legacy-qwen", "name": "Qwen", "description": "", "base_url": endpoint, "category": "model", "capabilities": "multi_group,custom_base_url,has_models", "active_key_initialized": true, "active_key_group_id": groupID, "active_key_id": selectedKeyID, "active_key_revision": 4, "create_user_id": owner, "create_user_name": "", "created_at": now, "updated_at": now}},
				{"user_model_provider_groups", map[string]any{"id": groupID, "user_model_provider_id": providerID, "name": "Qwen", "base_url": endpoint, "api_key": "", "api_key_ciphertext": string(ciphertext), "credential_version": 1, "credential_revision": 7, "is_verified": true, "create_user_id": owner, "create_user_name": "", "created_at": now, "updated_at": now}},
				{"user_model_provider_group_models", map[string]any{"id": modelID, "user_model_provider_id": providerID, "user_model_provider_group_id": groupID, "provider_name": "Qwen", "name": "qwen3-coder-plus", "model_type": "llm", "max_input_tokens": "262144", "is_default": false, "create_user_id": owner, "create_user_name": "", "created_at": now, "updated_at": now}},
				{"user_selected_models", map[string]any{"user_id": owner, "user_name": "", "model_type": "llm", "user_model_provider_group_model_id": modelID, "share": true, "created_at": now, "updated_at": now}},
				{"conversations", map[string]any{"id": conversationID, "display_name": "Retained conversation", "chat_model_mode": "fixed", "chat_model_id": modelID, "chat_model_source": "personal", "chat_model_snapshot": fmt.Sprintf(`{"model_id":%q,"provider_id":%q,"provider_group_id":%q}`, modelID, providerID, groupID), "create_user_id": owner, "create_user_name": "", "created_at": now, "updated_at": now}},
			} {
				if err := db.Table(fixture.table).Create(fixture.row).Error; err != nil {
					t.Fatal(err)
				}
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, migration := range catalog.Modes[len(catalog.Modes)-1].Dev {
				if migration.FileVersion >= 20261022072824 {
					execMigrationFileForDriver(t, raw, migration.UpPath, driver)
				}
			}
			for range 2 {
				if err := modelprovider.MigrateLegacyAPIKeys(db); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Model(&orm.DefaultModelProvider{}).Where("id = ?", "legacy-qwen").Update("base_url", "https://dashscope.aliyuncs.com/").Error; err != nil {
				t.Fatal(err)
			}
			plan := orm.DefaultModelProvider{ID: "qwen-coding", Name: "QwenCodingPlan", BaseURL: endpoint, Category: "model", Capabilities: "multi_group,custom_base_url,has_models", CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&plan).Error; err != nil {
				t.Fatal(err)
			}
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			for range 2 {
				request := httptest.NewRequest(http.MethodGet, "/model-providers", nil)
				request.Header.Set("X-User-Id", owner)
				recorder := httptest.NewRecorder()
				modelprovider.ListUserProviders(recorder, request)
				if recorder.Code != http.StatusOK {
					t.Fatalf("sync service profiles: status %d: %s", recorder.Code, recorder.Body.String())
				}
			}
			assertReferences := func() {
				t.Helper()
				var parent orm.UserModelProvider
				if err := db.Take(&parent, "id = ?", providerID).Error; err != nil {
					t.Fatal(err)
				}
				if parent.Name != plan.Name || parent.DefaultModelProviderID != plan.ID || parent.IsCustom || parent.BaseURL != endpoint || parent.ActiveKeyGroupID != groupID || parent.ActiveKeyID != selectedKeyID {
					t.Fatal("upgrade/restore changed supplier identity, endpoint or active key")
				}
				var model orm.UserModelProviderGroupModel
				if err := db.Take(&model, "id = ?", modelID).Error; err != nil || model.UserModelProviderID != providerID || model.UserModelProviderGroupID != groupID || model.ProviderName != "OpenAI" || model.MaxInputTokens == nil || *model.MaxInputTokens != "262144" {
					t.Fatalf("upgrade/restore changed model UUID, ownership, adapter or context: %v", err)
				}
				var selected string
				if err := raw.QueryRow("SELECT user_model_provider_group_model_id FROM user_selected_models WHERE user_id=$1 AND model_type='llm'", owner).Scan(&selected); err != nil || selected != modelID {
					t.Fatalf("default model UUID changed: %v", err)
				}
				var pinned, snapshot string
				if err := raw.QueryRow("SELECT chat_model_id,chat_model_snapshot FROM conversations WHERE id=$1", conversationID).Scan(&pinned, &snapshot); err != nil || pinned != modelID || !strings.Contains(snapshot, modelID) || !strings.Contains(snapshot, groupID) {
					t.Fatalf("conversation model UUID/snapshot changed: %v", err)
				}
				if key, err := modelprovider.ResolveProviderAPIKey(t.Context(), db, groupID); err != nil || key != "fixture-upgrade-second" {
					t.Fatalf("second active key lost: %v", err)
				}
			}
			assertReferences()
			var migrated orm.UserModelProviderGroup
			if err := db.Take(&migrated, "id = ?", groupID).Error; err != nil || migrated.APIKey != "" || migrated.APIKeyCiphertext != string(ciphertext) || migrated.CredentialVersion != 1 || migrated.CredentialRevision != 7 {
				t.Fatalf("upgrade modified the existing encrypted credential: %v", err)
			}
			if decrypted, err := modelprovider.ResolveAPIKey(migrated.APIKey, migrated.APIKeyCiphertext); err != nil || decrypted != pool {
				t.Fatalf("upgraded pool did not decrypt: %v", err)
			}
			credential, err := (modelprovider.CredentialBackupSource{DB: db}).LoadCredential(t.Context(), groupID)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(credential.APIKey)
			if credential.ProviderCatalogKey != plan.Name || credential.ActiveKeyIndex == nil || *credential.ActiveKeyIndex != 1 {
				t.Fatal("backup lost rebound service identity or second-key selection")
			}
			encoded, err := credentialvault.EncodeProviderCredentialPayload(credential)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(encoded)
			dek := bytes.Repeat([]byte{0x29}, 32)
			aad := []byte("fixture-upgrade-backup")
			nonce, encryptedBackup, err := credentialvault.EncryptPayload(rand.Reader, dek, aad, encoded)
			if err != nil || bytes.Contains(encryptedBackup, []byte("fixture-upgrade-")) {
				t.Fatal("backup encryption failed", err)
			}
			decoded, err := credentialvault.DecryptPayload(dek, nonce, aad, encryptedBackup)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(decoded)
			// Reconstruct only decrypted wire fields, rather than passing the
			// source credential object directly to the persistence boundary.
			var wire struct {
				CatalogKey  string `json:"provider_catalog_key"`
				DisplayName string `json:"display_name"`
				BaseURL     string `json:"base_url"`
				APIKey      string `json:"api_key"`
				Fields      struct {
					ActiveKeyIndex *int            `json:"active_key_index"`
					RequestConfig  json.RawMessage `json:"request_config"`
				} `json:"provider_specific_fields"`
			}
			if err := json.Unmarshal(decoded, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Fields.ActiveKeyIndex == nil || *wire.Fields.ActiveKeyIndex != 1 {
				t.Fatal("decrypted backup did not retain the second-key selection")
			}
			// Change the local choice after taking the backup. A restore that
			// ignores active_key_index must fail instead of passing by retaining
			// the already-selected second key during reconciliation.
			firstKeyID := fmt.Sprintf("%x", sha256.Sum256([]byte(groupID+"\x00fixture-upgrade-first")))
			activation, err := json.Marshal(map[string]string{"group_id": groupID, "key_id": firstKeyID})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPut, "/model-providers/active-key", bytes.NewReader(activation))
			request.Header.Set("X-User-Id", owner)
			request = mux.SetURLVars(request, map[string]string{"model_provider_id": providerID})
			recorder := httptest.NewRecorder()
			modelprovider.SetActiveProviderKey(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("change local active key: status %d: %s", recorder.Code, recorder.Body.String())
			}
			if key, err := modelprovider.ResolveProviderAPIKey(t.Context(), db, groupID); err != nil || key != "fixture-upgrade-first" {
				t.Fatalf("local active key did not change before restore: %v", err)
			}
			var drifted orm.UserModelProviderGroup
			if err := db.Take(&drifted, "id = ?", groupID).Error; err != nil || drifted.CredentialRevision <= migrated.CredentialRevision {
				t.Fatal("active-key change did not advance local credential revision", err)
			}
			keys, err := credentialvault.NewLocalKeyManager(&migrationCredentialKeyStore{values: make(map[string][]byte)}, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(modelprovider.SetCredentialKeyManager(keys))
			sink, err := modelprovider.NewCredentialRestoreSink(db, keys, owner, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			scope := credentialvault.AccountScope{CloudIssuer: "https://upgrade-cloud.example.test", CloudAccountID: "00000000-0000-7000-8000-000000000805"}
			restored := credentialvault.RestoredCredential{RecordID: "00000000-0000-7000-8000-000000000806", VaultID: "00000000-0000-7000-8000-000000000807", Revision: 3, ETag: strings.Repeat("e", 64), Provider: credentialvault.ProviderCredential{ProviderCatalogKey: wire.CatalogKey, DisplayName: wire.DisplayName, BaseURL: wire.BaseURL, APIKey: []byte(wire.APIKey), ActiveKeyIndex: wire.Fields.ActiveKeyIndex, RequestConfig: string(wire.Fields.RequestConfig)}}
			defer clear(restored.Provider.APIKey)
			binding := credentialvault.CredentialBinding{ID: "upgrade-binding", CloudIssuer: scope.CloudIssuer, CloudAccountID: scope.CloudAccountID, VaultID: restored.VaultID, CloudRecordID: restored.RecordID, LocalProviderGroupID: groupID, LastCloudRevision: 2, LastLocalCredentialRevision: migrated.CredentialRevision, BackupState: string(credentialvault.BackupSucceeded), CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&binding).Error; err != nil {
				t.Fatal(err)
			}
			if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictReplaceLocal}, now); err != nil {
				t.Fatal(err)
			}
			assertReferences()
			var final orm.UserModelProviderGroup
			if err := db.Take(&final, "id = ?", groupID).Error; err != nil || final.CredentialVersion != 2 || final.CredentialRevision != drifted.CredentialRevision+1 || final.APIKey != "" || final.APIKeyCiphertext == string(ciphertext) || strings.Contains(final.APIKeyCiphertext, "fixture-upgrade-") {
				t.Fatalf("restore did not replace the bound group with encrypted V2 credentials: %v", err)
			}
			if decrypted, err := modelprovider.ResolveAPIKey(final.APIKey, final.APIKeyCiphertext); err != nil || decrypted != pool {
				t.Fatalf("restored pool did not decrypt: %v", err)
			}
			var groups int64
			if err := db.Model(&orm.UserModelProviderGroup{}).Where("create_user_id = ?", base.CreateUserID).Count(&groups).Error; err != nil || groups != 1 {
				t.Fatal("restore duplicated the saved connection", err)
			}
		})
	}
}
