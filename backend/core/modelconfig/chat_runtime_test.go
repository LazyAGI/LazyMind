package modelconfig

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"lazymind/core/modelprovider"
)

func seedChatRuntimeModel(t *testing.T, db *gorm.DB, owner, id string, verified, shared bool) {
	t.Helper()
	for _, row := range []any{
		&orm.UserModelProvider{ID: "provider-" + id, Name: "OpenAI", BaseModel: orm.BaseModel{CreateUserID: owner}},
		&orm.UserModelProviderGroup{ID: "group-" + id, UserModelProviderID: "provider-" + id, BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-key", IsVerified: verified, CredentialRevision: 1, BaseModel: orm.BaseModel{CreateUserID: owner}},
		&orm.UserModelProviderGroupModel{ID: id, UserModelProviderID: "provider-" + id, UserModelProviderGroupID: "group-" + id, ProviderName: "OpenAI", Name: id, ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: owner}},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if shared {
		if err := db.Create(&orm.UserSelectedModel{UserID: owner, ModelKey: "llm", UserModelProviderGroupModelID: id, Share: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
}
func TestChatRuntimeKeepsKeylessDefaultCandidate(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedChatRuntimeModel(t, db, "user", "qwen", false, false)
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-qwen").Update("api_key", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserSelectedModel{UserID: "user", ModelKey: "llm", UserModelProviderGroupModelID: "qwen"}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := LoadChatModelCandidates(t.Context(), db, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].CandidateID != "own:qwen" {
		t.Fatal("keyless default model was excluded from chat candidates")
	}
	config := result.Candidates[0].LLMConfig["llm"].(map[string]any)
	if config["skip_auth"] != true || config["api_key"] != "" {
		t.Fatal("keyless chat candidate did not preserve unauthenticated access")
	}
}

func TestChatRuntimeCandidatesRespectOwnerSharingAndRevocation(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedChatRuntimeModel(t, db, "user", "own", true, false)
	seedChatRuntimeModel(t, db, "owner", "shared", true, true)
	seedChatRuntimeModel(t, db, "private-owner", "private", true, false)
	seedChatRuntimeModel(t, db, "user", "unverified", false, false)
	// A different user's selected row cannot publish the private owner's model.
	if err := db.Create(&orm.UserSelectedModel{UserID: "forger", ModelKey: "llm", UserModelProviderGroupModelID: "private", Share: true}).Error; err != nil {
		t.Fatal(err)
	}
	first, err := LoadChatModelCandidates(t.Context(), db, "user")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, candidate := range first.Candidates {
		ids[candidate.CandidateID] = true
	}
	if len(ids) != 2 || !ids["own:own"] || !ids["shared:shared"] {
		t.Fatalf("unauthorized candidate identities: %v", ids)
	}
	if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "owner").Update("share", false).Error; err != nil {
		t.Fatal(err)
	}
	second, err := LoadChatModelCandidates(t.Context(), db, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Candidates) != 1 || second.Candidates[0].CandidateID != "own:own" || len(first.Candidates) != 2 {
		t.Fatal("a later execution retained a revoked shared candidate or mutated an earlier pool")
	}
}
func TestChatRuntimeSkipsBrokenChatCredentialsButPreservesOtherRoleFailures(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	for _, id := range []string{"a", "b", "c"} {
		seedChatRuntimeModel(t, db, "user", id, true, false)
	}
	for _, id := range []string{"a", "b"} {
		if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-"+id).Update("api_key_ciphertext", "fixture-invalid-envelope").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.UserSelectedModel{UserID: "user", ModelKey: "llm", UserModelProviderGroupModelID: "a"}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := LoadChatRuntimeConfig(t.Context(), db, "user")
	if err != nil {
		t.Fatalf("broken chat credential blocked an alternative: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].CandidateID != "own:c" {
		t.Fatal("invalid candidate prevented the next valid candidate")
	}
	if _, err := LoadLLMConfig(t.Context(), db, "user"); err == nil {
		t.Fatal("legacy LoadLLMConfig error semantics changed")
	}
	if err := db.Create(&orm.UserSelectedModel{UserID: "user", ModelKey: "embed_main", UserModelProviderGroupModelID: "b"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChatRuntimeConfig(t.Context(), db, "user"); err == nil {
		t.Fatal("a non-chat credential failure was suppressed")
	}
}
func TestChatRuntimeWindowsUseStoredDeclarationAndKeepUnknown(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	for _, id := range []string{"known", "unknown"} {
		seedChatRuntimeModel(t, db, "user", id, true, false)
	}
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "known").Update("max_input_tokens", "32K").Error; err != nil {
		t.Fatal(err)
	}
	result, err := LoadChatModelCandidates(t.Context(), db, "user")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range result.Candidates {
		if candidate.ModelID == "known" {
			if candidate.MaxInputTokens == nil || *candidate.MaxInputTokens != 32768 {
				t.Fatal("declared window was lost")
			}
		} else {
			if candidate.MaxInputTokens != nil || candidate.LLMConfig["llm"].(map[string]any)["max_input_tokens"] != nil {
				t.Fatal("unknown window became the legacy 128K default")
			}
		}
	}
}

type countingRuntimeKeyStore struct {
	key   []byte
	reads int
}

func (store *countingRuntimeKeyStore) Load(context.Context, credentialvault.AccountScope, credentialvault.LocalKeyKind) ([]byte, error) {
	store.reads++
	return append([]byte(nil), store.key...), nil
}
func (*countingRuntimeKeyStore) Save(context.Context, credentialvault.AccountScope, credentialvault.LocalKeyKind, []byte) error {
	return nil
}
func (*countingRuntimeKeyStore) Delete(context.Context, credentialvault.AccountScope, credentialvault.LocalKeyKind) error {
	return nil
}
func TestChatRuntimeDecryptsSharedGroupOnceAcrossRolesAndCandidates(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedChatRuntimeModel(t, db, "user", "a", true, false)
	for _, id := range []string{"b", "embed"} {
		kind := "llm"
		if id == "embed" {
			kind = "embed"
		}
		if err := db.Create(&orm.UserModelProviderGroupModel{ID: id, UserModelProviderID: "provider-a", UserModelProviderGroupID: "group-a", ProviderName: "OpenAI", Name: id, ModelType: kind, BaseModel: orm.BaseModel{CreateUserID: "user"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.UserSelectedModel{UserID: "user", ModelKey: "embed_main", UserModelProviderGroupModelID: "embed"}).Error; err != nil {
		t.Fatal(err)
	}
	key := []byte(strings.Repeat("k", 32))
	encrypted, err := credentialvault.EncryptLocalCredential(rand.Reader, key, credentialvault.LocalCredentialAAD{SchemaVersion: 1, LocalProviderGroup: "group-a", CredentialRevision: 1}, []byte("fixture-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(map[string]any{"version": encrypted.Version, "scope": credentialvault.AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: "user"}, "aad": credentialvault.LocalCredentialAAD{SchemaVersion: 1, LocalProviderGroup: "group-a", CredentialRevision: 1}, "nonce": encrypted.Nonce, "ciphertext": encrypted.Ciphertext})
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Updates(map[string]any{"api_key": "", "api_key_ciphertext": string(envelope)}).Error; err != nil {
		t.Fatal(err)
	}
	secureStore := &countingRuntimeKeyStore{key: key}
	manager, err := credentialvault.NewLocalKeyManager(secureStore, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(modelprovider.SetCredentialKeyManager(manager))
	result, err := LoadChatRuntimeConfig(t.Context(), db, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 2 || result.Config["embed_main"] == nil || secureStore.reads != 1 {
		t.Fatalf("credential reads=%d candidates=%d; expected one parse shared by the existing role and both candidates", secureStore.reads, len(result.Candidates))
	}
	if _, err := LoadChatRuntimeConfig(t.Context(), db, "user"); err != nil {
		t.Fatal(err)
	}
	if secureStore.reads != 2 {
		t.Fatal("credential reuse escaped its execution lifetime")
	}
}
func TestChatRuntimeCloudCandidatesRequireExplicitSelection(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	provider := &CloudRuntimeProvider{Session: runtimeTestTokens{}, Client: &runtimeTestClient{}}
	SetRuntimeProvider(provider)
	modelprovider.SetCloudCatalogProvider(provider)
	t.Cleanup(func() { SetRuntimeProvider(nil); modelprovider.SetCloudCatalogProvider(nil) })
	first, err := LoadChatModelCandidates(t.Context(), db, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Models) != 1 || len(first.Candidates) != 0 {
		t.Fatal("whole Cloud catalog became automatic candidates")
	}
	if err := db.Create(&orm.UserSelectedCloudModel{UserID: "user", ModelType: "llm", PublicModelKey: "cloud-text"}).Error; err != nil {
		t.Fatal(err)
	}
	second, err := LoadChatModelCandidates(t.Context(), db, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Candidates) != 1 || second.Candidates[0].CandidateID != "cloud:cloud-text" {
		t.Fatal("explicit Cloud selection was not prepared")
	}
}

func TestChatRuntimeCredentialHeaderSafety(t *testing.T) {
	for _, test := range []struct {
		name, key, want string
		usable          bool
	}{
		{name: "single", key: "fixture-single", want: "fixture-single", usable: true},
		{name: "outer whitespace", key: " \tfixture-single \r\n", want: "fixture-single", usable: true},
		{name: "access secret pair", key: "fixture-access:fixture-secret", want: "fixture-access:fixture-secret", usable: true},
		{name: "line feed", key: "fixture-first\nfixture-second"},
		{name: "carriage return", key: "fixture-first\rfixture-second"},
		{name: "CRLF", key: "fixture-first\r\nfixture-second"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			seedChatRuntimeModel(t, db, "user", "a", true, false)
			seedChatRuntimeModel(t, db, "user", "d", true, false)
			if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Update("api_key", test.key).Error; err != nil {
				t.Fatal(err)
			}
			result, err := LoadChatModelCandidates(t.Context(), db, "user")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, candidate := range result.Candidates {
				if candidate.ModelID == "a" {
					found = true
					if test.usable && candidate.LLMConfig["llm"].(map[string]any)["api_key"] != test.want {
						t.Fatal("candidate credential was not normalized without splitting")
					}
				}
			}
			if found != test.usable {
				t.Fatalf("candidate usability=%t, want %t", found, test.usable)
			}
			want := 1
			if test.usable {
				want = 2
			}
			if len(result.Candidates) != want {
				t.Fatal("credential filtering removed the other valid connection")
			}
			var group orm.UserModelProviderGroup
			if err := db.Where("id = ?", "group-a").Take(&group).Error; err != nil {
				t.Fatal(err)
			}
			if !group.IsVerified || group.APIKey != test.key {
				t.Fatal("request preparation changed stored credential validity or content")
			}
		})
	}
}
func TestChatRuntimeEncryptedCredentialIsNormalizedOncePerGroup(t *testing.T) {
	for _, test := range []struct {
		name, key, want string
		usable          bool
	}{
		{name: "trim encrypted single", key: " \r\nfixture-single\t ", want: "fixture-single", usable: true},
		{name: "reject encrypted multiline", key: "fixture-first\nfixture-second"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			seedChatRuntimeModel(t, db, "user", "a", true, false)
			seedChatRuntimeModel(t, db, "user", "d", true, false)
			if err := db.Create(&orm.UserModelProviderGroupModel{ID: "b", UserModelProviderID: "provider-a", UserModelProviderGroupID: "group-a", ProviderName: "OpenAI", Name: "b", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "user"}}).Error; err != nil {
				t.Fatal(err)
			}
			key := []byte(strings.Repeat("k", 32))
			aad := credentialvault.LocalCredentialAAD{SchemaVersion: 1, LocalProviderGroup: "group-a", CredentialRevision: 1}
			encrypted, err := credentialvault.EncryptLocalCredential(rand.Reader, key, aad, []byte(test.key))
			if err != nil {
				t.Fatal(err)
			}
			envelope, _ := json.Marshal(map[string]any{"version": encrypted.Version, "scope": credentialvault.AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: "user"}, "aad": aad, "nonce": encrypted.Nonce, "ciphertext": encrypted.Ciphertext})
			if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Updates(map[string]any{"api_key": "", "api_key_ciphertext": string(envelope)}).Error; err != nil {
				t.Fatal(err)
			}
			secureStore := &countingRuntimeKeyStore{key: key}
			manager, err := credentialvault.NewLocalKeyManager(secureStore, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(modelprovider.SetCredentialKeyManager(manager))
			result, err := LoadChatModelCandidates(t.Context(), db, "user")
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, candidate := range result.Candidates {
				if candidate.ProviderGroupID == "group-a" {
					found++
					if test.usable && candidate.LLMConfig["llm"].(map[string]any)["api_key"] != test.want {
						t.Fatal("decrypted candidate retained surrounding whitespace")
					}
				}
			}
			want := 0
			if test.usable {
				want = 2
			}
			if found != want {
				t.Fatalf("prepared group models=%d, want %d", found, want)
			}
			for _, model := range result.Models {
				if model.ID == "a" {
					_ = result.PrepareCandidate(t.Context(), model)
				}
			}
			if secureStore.reads != 1 {
				t.Fatalf("group credential parsed %d times", secureStore.reads)
			}
			if len(result.Candidates) != want+1 {
				t.Fatal("the independent valid connection was not preserved")
			}
		})
	}
}
