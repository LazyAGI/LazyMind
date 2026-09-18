package credentialvault

import (
	"context"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"lazymind/core/cloudclient"
)

type AccessTokenSource interface {
	AccessToken(context.Context, time.Duration) (string, error)
}

type BackupCloud interface {
	Origin() string
	GetCurrentAccount(context.Context, string) (cloudclient.Account, error)
	EnableCredentialVault(context.Context, string) (cloudclient.CredentialVaultSummary, string, error)
	GetCredentialVaultKeyManifest(context.Context, string) (cloudclient.SignedCredentialKeyManifest, error)
	RegisterCredentialVaultMember(context.Context, string, cloudclient.CredentialVaultMemberRegistration) (cloudclient.CredentialVaultMember, error)
	PutCredentialVaultRecord(context.Context, string, cloudclient.CredentialVaultRecordEnvelope, cloudclient.CredentialVaultWriteCondition) (cloudclient.CredentialVaultRecordEnvelope, string, error)
}

type ProviderCredential struct {
	LocalProviderGroupID string
	CredentialRevision   int64
	ProviderCatalogKey   string
	DisplayName          string
	BaseURL              string
	APIKey               []byte `json:"-"`
}

type ProviderCredentialSource interface {
	LoadCredential(context.Context, string) (ProviderCredential, error)
}

type BackupStatus struct {
	Available       bool       `json:"available"`
	ReasonCode      string     `json:"reason_code,omitempty"`
	Enabled         bool       `json:"enabled"`
	BackedUp        int64      `json:"backed_up"`
	Pending         int64      `json:"pending"`
	Failed          int64      `json:"failed"`
	LastSucceededAt *time.Time `json:"last_succeeded_at,omitempty"`
}

type BackupService struct {
	repository *Repository
	keys       *LocalKeyManager
	manifest   *ManifestCache
	tokens     AccessTokenSource
	cloud      BackupCloud
	source     ProviderCredentialSource
	random     io.Reader
	now        func() time.Time
	processMu  sync.Mutex
}

type BackupServiceDeps struct {
	Repository *Repository
	Keys       *LocalKeyManager
	Manifest   *ManifestCache
	Tokens     AccessTokenSource
	Cloud      BackupCloud
	Source     ProviderCredentialSource
	Random     io.Reader
	Now        func() time.Time
}

func NewBackupService(deps BackupServiceDeps) (*BackupService, error) {
	if deps.Repository == nil || deps.Keys == nil || deps.Manifest == nil || deps.Tokens == nil || deps.Cloud == nil || deps.Source == nil || deps.Random == nil {
		return nil, ErrLocalSecureStoreUnavailable
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &BackupService{
		repository: deps.Repository, keys: deps.Keys, manifest: deps.Manifest, tokens: deps.Tokens,
		cloud: deps.Cloud, source: deps.Source, random: deps.Random, now: deps.Now,
	}, nil
}

func (service *BackupService) Status(ctx context.Context) (BackupStatus, error) {
	scope, _, _, err := service.cloudScope(ctx)
	if err != nil {
		return BackupStatus{ReasonCode: "cloud_session_required"}, nil
	}
	account, err := service.repository.Account(ctx, scope)
	if errors.Is(err, ErrLocalKeyNotFound) {
		return BackupStatus{Available: true}, nil
	}
	if err != nil {
		return BackupStatus{}, err
	}
	backedUp, pending, failed, err := service.repository.Counts(ctx, scope)
	if err != nil {
		return BackupStatus{}, err
	}
	return BackupStatus{
		Available: true, Enabled: account.BackupEnabled, BackedUp: backedUp,
		Pending: pending, Failed: failed, LastSucceededAt: account.LastSucceededAt,
	}, nil
}

func (service *BackupService) Enable(ctx context.Context) (BackupStatus, error) {
	scope, token, _, err := service.cloudScope(ctx)
	if err != nil {
		return BackupStatus{}, err
	}
	vault, vaultETag, err := service.cloud.EnableCredentialVault(ctx, token)
	if err != nil {
		return BackupStatus{}, err
	}
	if replacement, err := service.cloud.GetCredentialVaultKeyManifest(ctx, token); err == nil {
		if err := service.manifest.Replace(replacement.Payload, replacement.Signature); err != nil {
			return BackupStatus{}, err
		}
	}
	key, err := service.manifest.ActiveKey(vault.KeyShardID)
	if err != nil || key.KeyID != vault.ActiveKeyID {
		return BackupStatus{}, ErrInvalidContract
	}
	now := service.now().UTC()
	account, accountErr := service.repository.Account(ctx, scope)
	if errors.Is(accountErr, ErrLocalKeyNotFound) {
		signingKey, err := service.keys.SigningKey(ctx, scope)
		if err != nil {
			return BackupStatus{}, err
		}
		clientMemberKeyBytes := make([]byte, 32)
		if _, err := io.ReadFull(service.random, clientMemberKeyBytes); err != nil {
			return BackupStatus{}, ErrLocalSecureStoreUnavailable
		}
		clientMemberKey := base64.RawURLEncoding.EncodeToString(clientMemberKeyBytes)
		member, err := service.cloud.RegisterCredentialVaultMember(ctx, token, cloudclient.CredentialVaultMemberRegistration{
			ClientMemberKey: clientMemberKey, SigningPublicKey: signingKey.Public().(ed25519.PublicKey), SigningKeyVersion: 1,
		})
		clear(signingKey)
		if err != nil {
			return BackupStatus{}, err
		}
		account = VaultAccount{
			ID: uuid.NewString(), CloudIssuer: scope.CloudIssuer, CloudAccountID: scope.CloudAccountID,
			VaultID: vault.VaultID, VaultMemberID: member.VaultMemberID, ClientMemberKey: clientMemberKey,
			SigningKeyVersion: member.SigningKeyVersion, KeyShardID: vault.KeyShardID, ActiveKeyID: vault.ActiveKeyID,
			BackupEnabled: true, VaultETag: vaultETag, RecordCount: vault.RecordCount, CreatedAt: now, UpdatedAt: now,
		}
	} else if accountErr != nil {
		return BackupStatus{}, accountErr
	} else {
		if account.VaultID != vault.VaultID {
			return BackupStatus{}, ErrLocalConflict
		}
		account.BackupEnabled, account.KeyShardID, account.ActiveKeyID = true, vault.KeyShardID, vault.ActiveKeyID
		account.VaultETag, account.RecordCount, account.UpdatedAt = vaultETag, vault.RecordCount, now
	}
	if err := service.repository.SaveAccount(ctx, account); err != nil {
		return BackupStatus{}, err
	}
	if err := service.repository.EnqueueExistingCredentials(ctx, scope, now); err != nil {
		return BackupStatus{}, err
	}
	return service.Status(ctx)
}

func (service *BackupService) Disable(ctx context.Context) (BackupStatus, error) {
	scope, _, _, err := service.cloudScope(ctx)
	if err != nil {
		return BackupStatus{}, err
	}
	if err := service.repository.SetBackupEnabled(ctx, scope, false, service.now().UTC()); err != nil {
		return BackupStatus{}, err
	}
	return service.Status(ctx)
}

func (service *BackupService) ProcessDue(ctx context.Context) error {
	service.processMu.Lock()
	defer service.processMu.Unlock()
	scope, token, _, err := service.cloudScope(ctx)
	if err != nil {
		return err
	}
	account, err := service.repository.Account(ctx, scope)
	if err != nil || !account.BackupEnabled {
		return err
	}
	item, found, err := service.repository.ClaimDue(ctx, scope, service.now().UTC())
	if err != nil || !found {
		return err
	}
	if item.Operation != BackupUpsert {
		return service.repository.Discard(ctx, item.ID)
	}
	credential, err := service.source.LoadCredential(ctx, item.LocalProviderGroupID)
	if err != nil {
		return service.retry(ctx, item, 3080011, err)
	}
	if len(credential.APIKey) == 0 {
		_ = service.repository.Discard(ctx, item.ID)
		return nil
	}
	defer clear(credential.APIKey)
	return service.upload(ctx, scope, token, account, item, credential)
}

func (service *BackupService) upload(ctx context.Context, scope AccountScope, token string, account VaultAccount, item BackupOutboxItem, credential ProviderCredential) error {
	binding, bindingErr := service.repository.Binding(ctx, scope, credential.LocalProviderGroupID)
	created := errors.Is(bindingErr, ErrLocalKeyNotFound)
	if bindingErr != nil && !created {
		return service.retry(ctx, item, 3080011, bindingErr)
	}
	if created {
		binding = CredentialBinding{
			ID: uuid.NewString(), CloudIssuer: scope.CloudIssuer, CloudAccountID: scope.CloudAccountID,
			VaultID: account.VaultID, CloudRecordID: uuid.NewString(), LocalProviderGroupID: credential.LocalProviderGroupID,
			BackupState: string(BackupPending), CreatedAt: service.now().UTC(),
		}
	}
	cloudRevision := binding.LastCloudRevision + 1
	aad := RecordAAD{
		ProtocolVersion: ProtocolVersion, CloudIssuer: scope.CloudIssuer, CloudAccountID: scope.CloudAccountID,
		VaultID: account.VaultID, RecordID: binding.CloudRecordID, Revision: cloudRevision,
		KeyID: account.ActiveKeyID, PayloadType: "provider-credential",
	}
	canonicalAAD, err := CanonicalAAD(aad)
	if err != nil {
		return service.retry(ctx, item, 3080006, err)
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version": 1, "provider_catalog_key": credential.ProviderCatalogKey,
		"display_name": credential.DisplayName, "base_url": credential.BaseURL,
		"credential_type": "api_key", "api_key": string(credential.APIKey),
		"provider_specific_fields": map[string]any{},
	})
	if err != nil || len(payload) > MaximumPlaintextLength {
		return service.retry(ctx, item, 3080006, ErrInvalidContract)
	}
	defer clear(payload)
	dek := make([]byte, 32)
	if _, err := io.ReadFull(service.random, dek); err != nil {
		return service.retry(ctx, item, 3080011, err)
	}
	defer clear(dek)
	nonce, ciphertext, err := EncryptPayload(service.random, dek, canonicalAAD, payload)
	if err != nil {
		return service.retry(ctx, item, 3080006, err)
	}
	manifestKey, err := service.manifest.ActiveKey(account.KeyShardID)
	if err != nil || manifestKey.KeyID != account.ActiveKeyID {
		return service.retry(ctx, item, 3080003, ErrInvalidContract)
	}
	publicKey, err := parseManifestRSAKey(manifestKey)
	if err != nil {
		return service.retry(ctx, item, 3080003, err)
	}
	wrappedDEK, err := WrapDEK(service.random, publicKey, dek, canonicalAAD)
	if err != nil {
		return service.retry(ctx, item, 3080006, err)
	}
	signingKey, err := service.keys.SigningKey(ctx, scope)
	if err != nil {
		return service.retry(ctx, item, 3080011, err)
	}
	envelope := RecordEnvelope{
		AAD: aad, Nonce: nonce, Ciphertext: ciphertext, WrappedDEK: wrappedDEK,
		SigningMemberID: account.VaultMemberID, SigningKeyVersion: account.SigningKeyVersion,
	}
	envelope.Signature, err = SignRecord(signingKey, envelope)
	clear(signingKey)
	if err != nil {
		return service.retry(ctx, item, 3080006, err)
	}
	wire := cloudclient.CredentialVaultRecordEnvelope{
		VaultID: account.VaultID, RecordID: binding.CloudRecordID, Revision: cloudRevision,
		KeyID: account.ActiveKeyID, CryptoSuite: CredentialCryptoSuite, Nonce: nonce,
		AAD: cloudclient.CredentialRecordAAD{
			ProtocolVersion: aad.ProtocolVersion, CloudIssuer: aad.CloudIssuer, CloudAccountID: aad.CloudAccountID,
			VaultID: aad.VaultID, RecordID: aad.RecordID, Revision: aad.Revision, KeyID: aad.KeyID, PayloadType: aad.PayloadType,
		},
		Ciphertext: ciphertext, WrappedDEK: wrappedDEK, SigningMemberID: account.VaultMemberID,
		SigningKeyVersion: account.SigningKeyVersion, Signature: envelope.Signature,
	}
	condition := cloudclient.CredentialVaultWriteCondition{Create: created, ExpectedETag: binding.LastETag}
	_, etag, err := service.cloud.PutCredentialVaultRecord(ctx, token, wire, condition)
	if err != nil {
		var cloudErr *cloudclient.CloudError
		if errors.As(err, &cloudErr) && cloudErr.HTTPStatus == http.StatusPreconditionFailed {
			_ = service.repository.Conflict(ctx, item.ID, 3080005, service.now().UTC())
			return ErrLocalConflict
		}
		code := 3080011
		if errors.As(err, &cloudErr) && cloudErr.Code > 0 {
			code = cloudErr.Code
		}
		return service.retry(ctx, item, code, err)
	}
	now := service.now().UTC()
	binding.LastCloudRevision, binding.LastLocalCredentialRevision = cloudRevision, credential.CredentialRevision
	binding.LastETag, binding.BackupState, binding.UpdatedAt = etag, string(BackupSucceeded), now
	return service.repository.Complete(ctx, scope, item, binding, now)
}

func (service *BackupService) retry(ctx context.Context, item BackupOutboxItem, code int, cause error) error {
	updated, err := item.Retry(service.now().UTC(), code)
	if err == nil {
		err = service.repository.Retry(ctx, updated)
	}
	if err != nil {
		return err
	}
	return cause
}

func (service *BackupService) cloudScope(ctx context.Context) (AccountScope, string, cloudclient.Account, error) {
	token, err := service.tokens.AccessToken(ctx, 30*time.Second)
	if err != nil {
		return AccountScope{}, "", cloudclient.Account{}, err
	}
	account, err := service.cloud.GetCurrentAccount(ctx, token)
	if err != nil {
		return AccountScope{}, "", cloudclient.Account{}, err
	}
	return AccountScope{CloudIssuer: service.cloud.Origin(), CloudAccountID: account.ID}, token, account, nil
}

func parseManifestRSAKey(key ShardPublicKey) (*rsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(key.PublicKey)
	if err != nil {
		return nil, ErrInvalidContract
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, ErrInvalidContract
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok || publicKey.N == nil || publicKey.N.BitLen() != 3072 || publicKey.E != 65537 {
		return nil, ErrInvalidContract
	}
	return publicKey, nil
}

func (service *BackupService) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = service.ProcessDue(ctx)
		}
	}
}

var _ BackupCloud = (*cloudclient.Client)(nil)
