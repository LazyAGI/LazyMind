package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var credentialVaultETagPattern = regexp.MustCompile(`^"([0-9a-f]{64})"$`)

type CredentialVaultSummary struct {
	VaultID     string `json:"vault_id"`
	Status      string `json:"status"`
	KeyShardID  int    `json:"key_shard_id"`
	ActiveKeyID string `json:"active_key_id"`
	RecordCount int64  `json:"record_count"`
	ETagVersion int64  `json:"etag_version"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type CredentialVaultMemberRegistration struct {
	ClientMemberKey   string `json:"client_member_key"`
	SigningPublicKey  []byte `json:"signing_public_key"`
	SigningKeyVersion int    `json:"signing_key_version"`
}

type CredentialVaultMember struct {
	VaultMemberID     string `json:"vault_member_id"`
	ClientMemberKey   string `json:"client_member_key"`
	SigningPublicKey  []byte `json:"signing_public_key"`
	SigningKeyVersion int    `json:"signing_key_version"`
	Status            string `json:"status"`
	CreatedAt         string `json:"created_at"`
}

type CredentialRecordAAD struct {
	ProtocolVersion string `json:"protocol_version"`
	CloudIssuer     string `json:"cloud_issuer"`
	CloudAccountID  string `json:"cloud_account_id"`
	VaultID         string `json:"vault_id"`
	RecordID        string `json:"record_id"`
	Revision        int64  `json:"revision"`
	KeyID           string `json:"key_id"`
	PayloadType     string `json:"payload_type"`
}

type CredentialVaultRecordEnvelope struct {
	VaultID           string              `json:"vault_id"`
	RecordID          string              `json:"record_id"`
	Revision          int64               `json:"revision"`
	KeyID             string              `json:"key_id"`
	CryptoSuite       string              `json:"crypto_suite"`
	Nonce             []byte              `json:"nonce"`
	AAD               CredentialRecordAAD `json:"aad"`
	Ciphertext        []byte              `json:"ciphertext"`
	WrappedDEK        []byte              `json:"wrapped_dek"`
	SigningMemberID   string              `json:"signing_member_id"`
	SigningKeyVersion int                 `json:"signing_key_version"`
	Signature         []byte              `json:"signature"`
}

type CredentialVaultWriteCondition struct {
	Create       bool
	ExpectedETag string
}

type SignedCredentialKeyManifest struct {
	Payload   []byte `json:"signed_payload"`
	Signature []byte `json:"signature"`
}

func (c *Client) EnableCredentialVault(ctx context.Context, accessToken string) (CredentialVaultSummary, string, error) {
	if err := validateBearer(accessToken); err != nil {
		return CredentialVaultSummary{}, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/credential-vault"), nil)
	if err != nil {
		return CredentialVaultSummary{}, "", err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return CredentialVaultSummary{}, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return CredentialVaultSummary{}, "", decodeCloudError(response)
	}
	var vault CredentialVaultSummary
	if err := decodeStrictJSON(response, &vault); err != nil {
		return CredentialVaultSummary{}, "", fmt.Errorf("decode LazyMind Cloud credential vault: %w", err)
	}
	etag, err := credentialVaultETag(response.Header.Get("ETag"))
	if err != nil || !validCredentialVaultSummary(vault) {
		return CredentialVaultSummary{}, "", errors.New("LazyMind Cloud returned an invalid credential vault")
	}
	return vault, etag, nil
}

func (c *Client) GetCredentialVaultKeyManifest(ctx context.Context, accessToken string) (SignedCredentialKeyManifest, error) {
	if err := validateBearer(accessToken); err != nil {
		return SignedCredentialKeyManifest{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/v1/credential-vault/key-manifest"), nil)
	if err != nil {
		return SignedCredentialKeyManifest{}, err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return SignedCredentialKeyManifest{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return SignedCredentialKeyManifest{}, decodeCloudError(response)
	}
	var manifest SignedCredentialKeyManifest
	if err := decodeStrictJSON(response, &manifest); err != nil {
		return SignedCredentialKeyManifest{}, fmt.Errorf("decode LazyMind Cloud credential manifest: %w", err)
	}
	if len(manifest.Payload) == 0 || len(manifest.Payload) > 256*1024 || len(manifest.Signature) != 64 {
		return SignedCredentialKeyManifest{}, errors.New("LazyMind Cloud returned an invalid credential manifest")
	}
	return manifest, nil
}

func (c *Client) RegisterCredentialVaultMember(ctx context.Context, accessToken string, registration CredentialVaultMemberRegistration) (CredentialVaultMember, error) {
	if err := validateBearer(accessToken); err != nil {
		return CredentialVaultMember{}, err
	}
	if len(registration.ClientMemberKey) < 32 || len(registration.ClientMemberKey) > 128 ||
		len(registration.SigningPublicKey) != 32 || registration.SigningKeyVersion < 1 {
		return CredentialVaultMember{}, errors.New("invalid credential vault member registration")
	}
	body, err := json.Marshal(registration)
	if err != nil {
		return CredentialVaultMember{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/credential-vault/devices"), bytes.NewReader(body))
	if err != nil {
		return CredentialVaultMember{}, err
	}
	setCloudHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return CredentialVaultMember{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return CredentialVaultMember{}, decodeCloudError(response)
	}
	var member CredentialVaultMember
	if err := decodeStrictJSON(response, &member); err != nil {
		return CredentialVaultMember{}, fmt.Errorf("decode LazyMind Cloud credential vault member: %w", err)
	}
	if !isSafeCloudID(member.VaultMemberID) || member.Status != "active" || member.ClientMemberKey != registration.ClientMemberKey ||
		len(member.SigningPublicKey) != 32 || !bytes.Equal(member.SigningPublicKey, registration.SigningPublicKey) ||
		member.SigningKeyVersion != registration.SigningKeyVersion {
		return CredentialVaultMember{}, errors.New("LazyMind Cloud returned an invalid credential vault member")
	}
	return member, nil
}

func (c *Client) PutCredentialVaultRecord(ctx context.Context, accessToken string, envelope CredentialVaultRecordEnvelope, condition CredentialVaultWriteCondition) (CredentialVaultRecordEnvelope, string, error) {
	if err := validateBearer(accessToken); err != nil {
		return CredentialVaultRecordEnvelope{}, "", err
	}
	if !validCredentialVaultEnvelope(envelope) || condition.Create == (strings.TrimSpace(condition.ExpectedETag) != "") {
		return CredentialVaultRecordEnvelope{}, "", errors.New("invalid credential vault record")
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return CredentialVaultRecordEnvelope{}, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.resolve("/v1/credential-vault/records/"+envelope.RecordID), bytes.NewReader(body))
	if err != nil {
		return CredentialVaultRecordEnvelope{}, "", err
	}
	setCloudHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	wantStatus := http.StatusOK
	if condition.Create {
		request.Header.Set("If-None-Match", "*")
		wantStatus = http.StatusCreated
	} else {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(condition.ExpectedETag) {
			return CredentialVaultRecordEnvelope{}, "", errors.New("invalid credential vault ETag")
		}
		request.Header.Set("If-Match", `"`+condition.ExpectedETag+`"`)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return CredentialVaultRecordEnvelope{}, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		return CredentialVaultRecordEnvelope{}, "", decodeCloudError(response)
	}
	var stored CredentialVaultRecordEnvelope
	if err := decodeStrictJSON(response, &stored); err != nil {
		return CredentialVaultRecordEnvelope{}, "", fmt.Errorf("decode LazyMind Cloud credential record: %w", err)
	}
	etag, err := credentialVaultETag(response.Header.Get("ETag"))
	if err != nil || !validCredentialVaultEnvelope(stored) || stored.RecordID != envelope.RecordID || stored.VaultID != envelope.VaultID {
		return CredentialVaultRecordEnvelope{}, "", errors.New("LazyMind Cloud returned an invalid credential record")
	}
	return stored, etag, nil
}

func credentialVaultETag(value string) (string, error) {
	match := credentialVaultETagPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 {
		return "", errors.New("credential vault response omitted a strong ETag")
	}
	return match[1], nil
}

func validCredentialVaultSummary(vault CredentialVaultSummary) bool {
	return isSafeCloudID(vault.VaultID) && vault.Status == "active" && vault.KeyShardID >= 0 && vault.KeyShardID < 64 &&
		strings.TrimSpace(vault.ActiveKeyID) != "" && len(vault.ActiveKeyID) <= 128 && vault.RecordCount >= 0 && vault.ETagVersion > 0
}

func validCredentialVaultEnvelope(record CredentialVaultRecordEnvelope) bool {
	return isSafeCloudID(record.VaultID) && isSafeCloudID(record.RecordID) && record.Revision > 0 &&
		record.KeyID != "" && len(record.KeyID) <= 128 && record.CryptoSuite == "AES-256-GCM+RSA-3072-OAEP-SHA256+Ed25519" &&
		len(record.Nonce) == 12 && len(record.Ciphertext) >= 17 && len(record.Ciphertext) <= 16*1024+16 &&
		len(record.WrappedDEK) == 384 && isSafeCloudID(record.SigningMemberID) && record.SigningKeyVersion > 0 && len(record.Signature) == 64 &&
		record.AAD.ProtocolVersion == "lazymind-credential-vault/v1" && record.AAD.VaultID == record.VaultID &&
		record.AAD.RecordID == record.RecordID && record.AAD.Revision == record.Revision && record.AAD.KeyID == record.KeyID &&
		record.AAD.PayloadType == "provider-credential" && strings.TrimSpace(record.AAD.CloudIssuer) != "" && isSafeCloudID(record.AAD.CloudAccountID)
}
