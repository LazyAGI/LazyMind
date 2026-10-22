package modelprovider

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

func TestCredentialBackupIncludesPortableActiveKeySelection(t *testing.T) {
	for _, selected := range []int{1, -1} {
		name := "second"
		if selected < 0 {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, "fixture-first\nfixture-second")
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			groupID, keyID := group.ID, groupKeyID(group.ID, "fixture-second")
			if selected < 0 {
				groupID, keyID = "", ""
			}
			if err := db.Model(&parent).Updates(map[string]any{"protocol_name": "Fixture Provider", "active_key_initialized": true, "active_key_group_id": groupID, "active_key_id": keyID}).Error; err != nil {
				t.Fatal(err)
			}
			credential, err := (CredentialBackupSource{DB: db}).LoadCredential(t.Context(), group.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(credential.APIKey)
			encoded, err := credentialvault.EncodeProviderCredentialPayload(credential)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(encoded)
			dek, aad := bytes.Repeat([]byte{0x4b}, 32), []byte("selection-backup")
			nonce, ciphertext, err := credentialvault.EncryptPayload(rand.Reader, dek, aad, encoded)
			if err != nil {
				t.Fatal(err)
			}
			plaintext, err := credentialvault.DecryptPayload(dek, nonce, aad, ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(plaintext)
			var payload struct {
				CatalogKey  string                     `json:"provider_catalog_key"`
				DisplayName string                     `json:"display_name"`
				BaseURL     string                     `json:"base_url"`
				APIKey      string                     `json:"api_key"`
				Fields      map[string]json.RawMessage `json:"provider_specific_fields"`
			}
			if err := json.Unmarshal(plaintext, &payload); err != nil {
				t.Fatal(err)
			}
			var index int
			if raw, ok := payload.Fields["active_key_index"]; !ok || json.Unmarshal(raw, &index) != nil || index != selected {
				t.Fatal("backup lost the supplier selection")
			}
			// The vault package separately exercises its strict payload decoder.
			// Pass these decrypted wire fields across the actual persistence boundary.
			restored := customRestorePayload("00000000-0000-7000-8000-000000000401", payload.DisplayName, payload.BaseURL, payload.APIKey)
			restored.Provider.ProviderCatalogKey, restored.Provider.ActiveKeyIndex = payload.CatalogKey, &index
			sink, target, scope := newCredentialRestoreSinkFixture(t)
			if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}, time.Now()); err != nil {
				t.Fatal(err)
			}
			var local orm.UserModelProviderGroup
			if err := target.Take(&local).Error; err != nil {
				t.Fatal(err)
			}
			if local.ID == group.ID {
				t.Fatal("fixture did not restore into a new group")
			}
			key, err := ResolveProviderAPIKey(t.Context(), target, local.ID)
			if selected < 0 {
				if !errors.Is(err, ErrProviderKeyDisabled) {
					t.Fatal("restoring a disabled supplier enabled a key")
				}
			} else if err != nil || key != "fixture-second" {
				t.Fatal("restoring a supplier selected the wrong key")
			}
			if err := sink.ActivateTemporary(t.Context(), scope, []credentialvault.RestoredCredential{restored}, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sink.ClearTemporary(t.Context(), scope) })
			temporary, found := sink.TemporaryCredential(restored.RecordID)
			defer clear(temporary.APIKey)
			if selected < 0 {
				if found {
					t.Fatal("temporary restore exposed a disabled supplier")
				}
			} else if !found || string(temporary.APIKey) != "fixture-second" {
				t.Fatal("temporary restore exposed an inactive key or the full pool")
			}
		})
	}
}

func TestLegacyRestoreKeepsFirstSavedKeyCompatibility(t *testing.T) {
	sink, target, scope := newCredentialRestoreSinkFixture(t)
	restored := customRestorePayload("00000000-0000-7000-8000-000000000401", "Legacy", "https://legacy.example/v1", "fixture-first\nfixture-second")
	if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var group orm.UserModelProviderGroup
	if err := target.Take(&group).Error; err != nil {
		t.Fatal(err)
	}
	if key, err := ResolveProviderAPIKey(t.Context(), target, group.ID); err != nil || key != "fixture-first" {
		t.Fatal("legacy trusted restore lost first-key compatibility")
	}
	if err := sink.ActivateTemporary(t.Context(), scope, []credentialvault.RestoredCredential{restored}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.ClearTemporary(t.Context(), scope) })
	temporary, found := sink.TemporaryCredential(restored.RecordID)
	defer clear(temporary.APIKey)
	if !found || string(temporary.APIKey) != "fixture-first" {
		t.Fatal("legacy temporary restore lost first-key compatibility")
	}
}

func TestBoundAndSaveCopyRestoreApplyExplicitSelection(t *testing.T) {
	for _, resolution := range []credentialvault.ConflictResolution{credentialvault.ConflictReplaceLocal, credentialvault.ConflictSaveCopy} {
		t.Run(string(resolution), func(t *testing.T) {
			sink, target, scope := newCredentialRestoreSinkFixture(t)
			restored := customRestorePayload("00000000-0000-7000-8000-000000000401", "Supplier", "https://supplier.example/v1", "fixture-first\nfixture-second")
			initial := map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}
			if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, initial, time.Now()); err != nil {
				t.Fatal(err)
			}
			for _, index := range []int{1, -1} {
				restored.Provider.ActiveKeyIndex, restored.Revision = &index, restored.Revision+1
				if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: resolution}, time.Now()); err != nil {
					t.Fatal(err)
				}
				var binding credentialvault.CredentialBinding
				if err := target.Take(&binding, "cloud_record_id = ?", restored.RecordID).Error; err != nil {
					t.Fatal(err)
				}
				key, err := ResolveProviderAPIKey(t.Context(), target, binding.LocalProviderGroupID)
				if index < 0 {
					if !errors.Is(err, ErrProviderKeyDisabled) {
						t.Fatal("explicit disable was lost on restore")
					}
				} else if err != nil || key != "fixture-second" {
					t.Fatal("explicit selection was lost on restore")
				}
			}
		})
	}
}

type selectionBackupOutbox struct {
	ID                                                string `gorm:"primaryKey"`
	CloudIssuer, CloudAccountID, LocalProviderGroupID string
	LocalCredentialRevision                           int64
	Operation, BackupState                            string
	AttemptCount                                      int
	NextAttemptAt                                     time.Time
	LastErrorCode                                     int
	CreatedAt, UpdatedAt                              time.Time
}

func (selectionBackupOutbox) TableName() string { return "credential_backup_outbox" }

func TestActiveKeyMutationsAdvanceEncryptedRevisionAndDeduplicateBackup(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "fixture-first\nfixture-second")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&credentialvault.VaultAccount{}, &selectionBackupOutbox{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&credentialvault.VaultAccount{ID: "account", CloudIssuer: "https://cloud.example", CloudAccountID: "cloud-user", BackupEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"success":true}`)) }))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	previous := group.CredentialRevision
	for _, key := range []string{"fixture-second", ""} {
		groupID, keyID := group.ID, groupKeyID(group.ID, key)
		if key == "" {
			groupID, keyID = "", ""
		}
		body, _ := json.Marshal(activeProviderKeyRequest{GroupID: groupID, KeyID: keyID})
		req := httptest.NewRequest(http.MethodPut, "/active", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID})
		rec := httptest.NewRecorder()
		SetActiveProviderKey(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("activation status=%d", rec.Code)
		}
		if err := db.Take(&group, "id = ?", group.ID).Error; err != nil {
			t.Fatal(err)
		}
		if group.CredentialRevision <= previous {
			t.Fatal("selection mutation did not advance credential revision")
		}
		previous = group.CredentialRevision
		if pool, err := apiKeyForGroup(db, &group); err != nil || pool != "fixture-first\nfixture-second" {
			t.Fatal("selection mutation broke encrypted credentials")
		}
		var queued []selectionBackupOutbox
		if err := db.Find(&queued).Error; err != nil {
			t.Fatal(err)
		}
		if len(queued) != 1 || queued[0].LocalCredentialRevision != previous || queued[0].BackupState != "pending" {
			t.Fatal("selection backup not queued at the latest revision")
		}
	}
	var count int64
	db.Model(&orm.UserModelProviderGroup{}).Count(&count)
	if count != 1 {
		t.Fatal("selection mutation duplicated credential storage")
	}
}
