package cloudclient

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var credentialRestoreUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type CredentialVaultRecordSummary struct {
	RecordID        string `json:"record_id"`
	Revision        int64  `json:"revision"`
	KeyID           string `json:"key_id"`
	CryptoSuite     string `json:"crypto_suite"`
	SigningMemberID string `json:"signing_member_id"`
	UpdatedAt       string `json:"updated_at"`
	ETagVersion     int64  `json:"etag_version"`
	ETag            string `json:"etag"`
}

type CredentialVaultRecordPage struct {
	Items      []CredentialVaultRecordSummary `json:"items"`
	NextCursor *string                        `json:"next_cursor,omitempty"`
}

type CredentialRestoreRecordRequest struct {
	RecordID string `json:"record_id"`
	Revision int64  `json:"revision"`
}

type CredentialRestoreRequest struct {
	Mode                   string                           `json:"mode"`
	Records                []CredentialRestoreRecordRequest `json:"records"`
	RecipientPublicKey     []byte                           `json:"recipient_public_key"`
	RecipientPublicKeyHash string                           `json:"recipient_public_key_hash"`
}

type CredentialRestoreResultItem struct {
	RecordID          string              `json:"record_id"`
	Revision          int64               `json:"revision"`
	Nonce             []byte              `json:"nonce"`
	AAD               CredentialRecordAAD `json:"aad"`
	Ciphertext        []byte              `json:"ciphertext"`
	DeviceWrappedDEK  []byte              `json:"device_wrapped_dek"`
	SigningMemberID   string              `json:"signing_member_id"`
	SigningKeyVersion int                 `json:"signing_key_version"`
	Signature         []byte              `json:"signature"`
}

type CredentialRestoreResult struct {
	Items []CredentialRestoreResultItem `json:"items"`
}

type CredentialRestoreOperation struct {
	OperationID string                   `json:"operation_id"`
	Status      string                   `json:"status"`
	ExpiresAt   string                   `json:"expires_at"`
	CreatedAt   string                   `json:"created_at"`
	CompletedAt *string                  `json:"completed_at,omitempty"`
	FailureCode *string                  `json:"failure_code,omitempty"`
	Result      *CredentialRestoreResult `json:"result,omitempty"`
}

func (c *Client) ListCredentialVaultRecords(ctx context.Context, accessToken, cursor string, pageSize int) (CredentialVaultRecordPage, error) {
	if err := validateBearer(accessToken); err != nil {
		return CredentialVaultRecordPage{}, err
	}
	if len(cursor) > 2048 || pageSize < 1 || pageSize > 100 {
		return CredentialVaultRecordPage{}, errors.New("invalid credential vault record query")
	}
	endpoint, err := url.Parse(c.resolve("/v1/credential-vault/records"))
	if err != nil {
		return CredentialVaultRecordPage{}, err
	}
	query := endpoint.Query()
	query.Set("page_size", strconv.Itoa(pageSize))
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return CredentialVaultRecordPage{}, err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return CredentialVaultRecordPage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return CredentialVaultRecordPage{}, decodeCloudError(response)
	}
	var page CredentialVaultRecordPage
	if err := decodeCredentialRestoreJSON(response, &page); err != nil {
		return CredentialVaultRecordPage{}, fmt.Errorf("decode LazyMind Cloud credential record page: %w", err)
	}
	if len(page.Items) > 100 || page.NextCursor != nil && len(*page.NextCursor) > 2048 {
		return CredentialVaultRecordPage{}, errors.New("LazyMind Cloud returned an invalid credential record page")
	}
	seen := make(map[string]struct{}, len(page.Items))
	for _, item := range page.Items {
		if !validCredentialVaultRecordSummary(item) {
			return CredentialVaultRecordPage{}, errors.New("LazyMind Cloud returned an invalid credential record summary")
		}
		if _, found := seen[item.RecordID]; found {
			return CredentialVaultRecordPage{}, errors.New("LazyMind Cloud returned duplicate credential records")
		}
		seen[item.RecordID] = struct{}{}
	}
	return page, nil
}

func (c *Client) CreateCredentialRestore(ctx context.Context, accessToken, idempotencyKey string, restore CredentialRestoreRequest) (CredentialRestoreOperation, error) {
	if err := validateBearer(accessToken); err != nil {
		return CredentialRestoreOperation{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 1 || len(idempotencyKey) > 128 || !validCredentialRestoreRequest(restore) {
		return CredentialRestoreOperation{}, errors.New("invalid credential restore request")
	}
	body, err := json.Marshal(restore)
	if err != nil {
		return CredentialRestoreOperation{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/credential-vault/restores"), bytes.NewReader(body))
	if err != nil {
		return CredentialRestoreOperation{}, err
	}
	setCloudHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return CredentialRestoreOperation{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return CredentialRestoreOperation{}, decodeCloudError(response)
	}
	var operation CredentialRestoreOperation
	if err := decodeCredentialRestoreJSON(response, &operation); err != nil {
		return CredentialRestoreOperation{}, fmt.Errorf("decode LazyMind Cloud credential restore: %w", err)
	}
	if !validCredentialRestoreOperation(operation) || operation.Status == "succeeded" && !validCredentialRestoreResult(operation.Result) {
		return CredentialRestoreOperation{}, errors.New("LazyMind Cloud returned an invalid credential restore operation")
	}
	return operation, nil
}

func (c *Client) GetCredentialRestore(ctx context.Context, accessToken, operationID string) (CredentialRestoreOperation, error) {
	if err := validateBearer(accessToken); err != nil {
		return CredentialRestoreOperation{}, err
	}
	if !credentialRestoreUUID.MatchString(operationID) {
		return CredentialRestoreOperation{}, errors.New("invalid credential restore operation ID")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/v1/credential-vault/restores/"+operationID), nil)
	if err != nil {
		return CredentialRestoreOperation{}, err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return CredentialRestoreOperation{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return CredentialRestoreOperation{}, decodeCloudError(response)
	}
	var operation CredentialRestoreOperation
	if err := decodeCredentialRestoreJSON(response, &operation); err != nil {
		return CredentialRestoreOperation{}, fmt.Errorf("decode LazyMind Cloud credential restore operation: %w", err)
	}
	if operation.OperationID != operationID || !validCredentialRestoreOperation(operation) || operation.Status == "succeeded" && !validCredentialRestoreResult(operation.Result) {
		return CredentialRestoreOperation{}, errors.New("LazyMind Cloud returned an invalid credential restore operation")
	}
	return operation, nil
}

func (c *Client) CancelCredentialRestore(ctx context.Context, accessToken, operationID string) error {
	if err := validateBearer(accessToken); err != nil {
		return err
	}
	if !credentialRestoreUUID.MatchString(operationID) {
		return errors.New("invalid credential restore operation ID")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.resolve("/v1/credential-vault/restores/"+operationID), nil)
	if err != nil {
		return err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return decodeCloudError(response)
	}
	return nil
}

func validCredentialVaultRecordSummary(record CredentialVaultRecordSummary) bool {
	if !credentialRestoreUUID.MatchString(record.RecordID) || record.Revision < 1 || record.KeyID == "" || len(record.KeyID) > 128 ||
		record.CryptoSuite != "AES-256-GCM+RSA-3072-OAEP-SHA256+Ed25519" || !credentialRestoreUUID.MatchString(record.SigningMemberID) || record.ETagVersion < 1 ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(record.ETag) {
		return false
	}
	_, err := time.Parse(time.RFC3339, record.UpdatedAt)
	return err == nil
}

func decodeCredentialRestoreJSON(response *http.Response, target any) error {
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("credential restore response contains trailing data")
	}
	return nil
}

func validCredentialRestoreRequest(restore CredentialRestoreRequest) bool {
	if restore.Mode != "trusted_device" && restore.Mode != "temporary" || len(restore.Records) < 1 || len(restore.Records) > 20 || len(restore.RecipientPublicKey) != 422 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(restore.RecipientPublicKeyHash) {
		return false
	}
	parsed, err := x509.ParsePKIXPublicKey(restore.RecipientPublicKey)
	publicKey, ok := parsed.(*rsa.PublicKey)
	if err != nil || !ok || publicKey.N == nil || publicKey.N.BitLen() != 3072 || publicKey.E != 65537 {
		return false
	}
	digest := sha256.Sum256(restore.RecipientPublicKey)
	if hex.EncodeToString(digest[:]) != restore.RecipientPublicKeyHash {
		return false
	}
	seen := make(map[string]struct{}, len(restore.Records))
	for _, record := range restore.Records {
		if !credentialRestoreUUID.MatchString(record.RecordID) || record.Revision < 1 {
			return false
		}
		if _, found := seen[record.RecordID]; found {
			return false
		}
		seen[record.RecordID] = struct{}{}
	}
	return true
}

func validCredentialRestoreOperation(operation CredentialRestoreOperation) bool {
	if !credentialRestoreUUID.MatchString(operation.OperationID) {
		return false
	}
	createdAt, createdErr := time.Parse(time.RFC3339, operation.CreatedAt)
	expiresAt, expiresErr := time.Parse(time.RFC3339, operation.ExpiresAt)
	if createdErr != nil || expiresErr != nil || !expiresAt.After(createdAt) || expiresAt.Sub(createdAt) > 5*time.Minute {
		return false
	}
	if operation.CompletedAt != nil {
		completedAt, err := time.Parse(time.RFC3339, *operation.CompletedAt)
		if err != nil || completedAt.Before(createdAt) || completedAt.After(expiresAt) {
			return false
		}
	}
	switch operation.Status {
	case "pending", "running":
		return operation.Result == nil
	case "succeeded":
		return operation.Result != nil && operation.CompletedAt != nil
	case "failed":
		return operation.Result == nil && operation.FailureCode != nil && len(*operation.FailureCode) > 0 && len(*operation.FailureCode) <= 96
	case "canceled", "expired":
		return operation.Result == nil
	default:
		return false
	}
}

func validCredentialRestoreResult(result *CredentialRestoreResult) bool {
	if result == nil || len(result.Items) < 1 || len(result.Items) > 20 {
		return false
	}
	seen := make(map[string]struct{}, len(result.Items))
	for _, item := range result.Items {
		if !credentialRestoreUUID.MatchString(item.RecordID) || item.Revision < 1 || len(item.Nonce) != 12 || len(item.Ciphertext) < 17 || len(item.Ciphertext) > 16*1024+16 ||
			len(item.DeviceWrappedDEK) != 384 || !credentialRestoreUUID.MatchString(item.SigningMemberID) || item.SigningKeyVersion < 1 || len(item.Signature) != 64 ||
			item.AAD.ProtocolVersion != "lazymind-credential-vault/v1" || item.AAD.RecordID != item.RecordID || item.AAD.Revision != item.Revision ||
			item.AAD.PayloadType != "provider-credential" || !credentialRestoreUUID.MatchString(item.AAD.CloudAccountID) || !credentialRestoreUUID.MatchString(item.AAD.VaultID) ||
			item.AAD.KeyID == "" || len(item.AAD.KeyID) > 128 || strings.TrimSpace(item.AAD.CloudIssuer) == "" {
			return false
		}
		if _, found := seen[item.RecordID]; found {
			return false
		}
		seen[item.RecordID] = struct{}{}
	}
	return true
}
