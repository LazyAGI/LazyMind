package credentialvault

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/cloudclient"
	"lazymind/core/common/orm"
)

type backupTestTokens struct{ token string }

func (tokens backupTestTokens) AccessToken(context.Context, time.Duration) (string, error) {
	return tokens.token, nil
}

type backupTestSource struct{ credential ProviderCredential }

func (source backupTestSource) LoadCredential(context.Context, string) (ProviderCredential, error) {
	credential := source.credential
	credential.APIKey = append([]byte(nil), credential.APIKey...)
	return credential, nil
}

type backupTestCloud struct {
	origin            string
	account           cloudclient.Account
	vault             cloudclient.CredentialVaultSummary
	manifest          cloudclient.SignedCredentialKeyManifest
	member            cloudclient.CredentialVaultMember
	putErr            error
	putEnvelope       cloudclient.CredentialVaultRecordEnvelope
	putCondition      cloudclient.CredentialVaultWriteCondition
	memberPrivateSeen bool
}

func (cloud *backupTestCloud) Origin() string { return cloud.origin }
func (cloud *backupTestCloud) GetCurrentAccount(context.Context, string) (cloudclient.Account, error) {
	return cloud.account, nil
}
func (cloud *backupTestCloud) EnableCredentialVault(context.Context, string) (cloudclient.CredentialVaultSummary, string, error) {
	return cloud.vault, strings.Repeat("a", 64), nil
}
func (cloud *backupTestCloud) GetCredentialVaultKeyManifest(context.Context, string) (cloudclient.SignedCredentialKeyManifest, error) {
	return cloud.manifest, nil
}
func (cloud *backupTestCloud) RegisterCredentialVaultMember(_ context.Context, _ string, registration cloudclient.CredentialVaultMemberRegistration) (cloudclient.CredentialVaultMember, error) {
	cloud.memberPrivateSeen = bytes.Contains(bytes.ToLower(registration.SigningPublicKey), []byte("private"))
	member := cloud.member
	member.ClientMemberKey, member.SigningPublicKey, member.SigningKeyVersion = registration.ClientMemberKey, registration.SigningPublicKey, registration.SigningKeyVersion
	return member, nil
}
func (cloud *backupTestCloud) PutCredentialVaultRecord(_ context.Context, _ string, envelope cloudclient.CredentialVaultRecordEnvelope, condition cloudclient.CredentialVaultWriteCondition) (cloudclient.CredentialVaultRecordEnvelope, string, error) {
	cloud.putEnvelope, cloud.putCondition = envelope, condition
	if cloud.putErr != nil {
		return cloudclient.CredentialVaultRecordEnvelope{}, "", cloud.putErr
	}
	return envelope, strings.Repeat("b", 64), nil
}

func newBackupServiceFixture(t *testing.T) (*BackupService, *Repository, *backupTestCloud, AccountScope) {
	t.Helper()
	now := time.Date(2026, 8, 26, 6, 20, 49, 0, time.UTC)
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&VaultAccount{}, &CredentialBinding{}, &backupOutboxRow{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	trustPublic, trustPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	shardPrivate, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	shardDER, err := x509.MarshalPKIXPublicKey(&shardPrivate.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://cloud.example.test"
	manifestPayload, err := json.Marshal(KeyManifest{SchemaVersion: 1, Issuer: origin, Keys: []ShardPublicKey{{
		KeyID: "credential-shard-17-v1", ShardID: 17, Algorithm: "RSA-3072-OAEP-SHA256",
		PublicKey: base64.StdEncoding.EncodeToString(shardDER), Status: "active",
		NotBefore: now.Add(-time.Hour).Format(time.RFC3339), NotAfter: now.Add(time.Hour).Format(time.RFC3339),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := cloudclient.SignedCredentialKeyManifest{Payload: manifestPayload, Signature: ed25519.Sign(trustPrivate, manifestPayload)}
	cache, err := NewManifestCache(trustPublic, origin, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	keyStore := newD2MemoryKeyStore()
	keys, err := NewLocalKeyManager(keyStore, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cloud := &backupTestCloud{
		origin:  origin,
		account: cloudclient.Account{ID: "00000000-0000-7000-8000-000000000101", Username: "fixture"},
		vault: cloudclient.CredentialVaultSummary{
			VaultID: "00000000-0000-7000-8000-000000000201", Status: "active", KeyShardID: 17,
			ActiveKeyID: "credential-shard-17-v1", ETagVersion: 1,
		},
		manifest: manifest,
		member: cloudclient.CredentialVaultMember{
			VaultMemberID: "00000000-0000-7000-8000-000000000203", Status: "active", CreatedAt: now.Format(time.RFC3339),
		},
	}
	repository := NewRepository(db)
	service, err := NewBackupService(BackupServiceDeps{
		Repository: repository, Keys: keys, Manifest: cache, Tokens: backupTestTokens{token: "fixture-access"},
		Cloud: cloud, Source: backupTestSource{credential: ProviderCredential{
			LocalProviderGroupID: "provider-group-a", CredentialRevision: 1, ProviderCatalogKey: "fixture-provider",
			DisplayName: "Fixture", BaseURL: "https://provider.example.test", APIKey: []byte("fixture-plaintext-api-key"),
		}}, Random: rand.Reader, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := AccountScope{CloudIssuer: origin, CloudAccountID: cloud.account.ID}
	return service, repository, cloud, scope
}

func TestBackupServiceEncryptsAndUploadsWithoutPersistingPlaintext(t *testing.T) {
	service, repository, cloud, scope := newBackupServiceFixture(t)
	ctx := context.Background()
	if _, err := service.Enable(ctx); err != nil {
		t.Fatalf("enable backup: %v", err)
	}
	if cloud.memberPrivateSeen {
		t.Fatal("member registration exposed private key material")
	}
	if err := EnqueueCredentialBackup(repository.db, "provider-group-a", 1, BackupUpsert, service.now()); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessDue(ctx); err != nil {
		t.Fatalf("process backup: %v", err)
	}
	if !cloud.putCondition.Create || bytes.Contains(cloud.putEnvelope.Ciphertext, []byte("fixture-plaintext-api-key")) || len(cloud.putEnvelope.WrappedDEK) != 384 {
		t.Fatalf("uploaded envelope/condition is unsafe: create=%t ciphertext=%d wrapped=%d", cloud.putCondition.Create, len(cloud.putEnvelope.Ciphertext), len(cloud.putEnvelope.WrappedDEK))
	}
	backedUp, pending, failed, err := repository.Counts(ctx, scope)
	if err != nil || backedUp != 1 || pending != 0 || failed != 0 {
		t.Fatalf("backup counts = %d/%d/%d err=%v", backedUp, pending, failed, err)
	}
	var outboxCount int64
	if err := repository.db.Model(&backupOutboxRow{}).Count(&outboxCount).Error; err != nil || outboxCount != 0 {
		t.Fatalf("outbox after success = %d err=%v", outboxCount, err)
	}
}

func TestBackupServiceCloudFailureKeepsReferenceOnlyRetry(t *testing.T) {
	service, repository, cloud, _ := newBackupServiceFixture(t)
	ctx := context.Background()
	if _, err := service.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueCredentialBackup(repository.db, "provider-group-a", 1, BackupUpsert, service.now()); err != nil {
		t.Fatal(err)
	}
	cloud.putErr = &cloudclient.CloudError{HTTPStatus: 503, Code: 3080011, RequestID: "fixture-request"}
	if err := service.ProcessDue(ctx); err == nil {
		t.Fatal("Cloud upload failure was reported as success")
	}
	var row backupOutboxRow
	if err := repository.db.Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(row)
	if row.BackupState != string(BackupFailed) || row.AttemptCount != 1 || bytes.Contains(encoded, []byte("fixture-plaintext-api-key")) {
		t.Fatalf("failed outbox row is unsafe: %s", encoded)
	}
}

var _ BackupCloud = (*backupTestCloud)(nil)
