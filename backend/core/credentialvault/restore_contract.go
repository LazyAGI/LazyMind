package credentialvault

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lazymind/core/cloudclient"
)

var ErrRestoreOperationNotFound = errors.New("credential restore operation was not found")

const TemporaryRestoreAbsoluteTTL = 8 * time.Hour

const (
	RestoreTTL            = 5 * time.Minute
	MaximumRestoreRecords = 20
)

type RestoreMode string

const (
	RestoreTrustedDevice RestoreMode = "trusted_device"
	RestoreTemporary     RestoreMode = "temporary"
)

type ConflictResolution string

const (
	ConflictFail         ConflictResolution = "fail"
	ConflictReplaceLocal ConflictResolution = "replace_local"
	ConflictSaveCopy     ConflictResolution = "save_copy"
)

type RestoreSelection struct {
	RecordID   string             `json:"record_id"`
	Revision   int64              `json:"revision"`
	Resolution ConflictResolution `json:"resolution"`
}

type RestoreCommand struct {
	Mode       RestoreMode        `json:"mode"`
	Selections []RestoreSelection `json:"records"`
}

type RestoreDiscovery struct {
	Available              bool                   `json:"available"`
	ReasonCode             string                 `json:"reason_code,omitempty"`
	RequiresExplicitAction bool                   `json:"requires_explicit_action"`
	Records                []RestoreRecordSummary `json:"records"`
	ActiveOperation        *LocalRestoreOperation `json:"active_operation,omitempty"`
}

type RestoreRecordSummary struct {
	RecordID  string    `json:"record_id"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

type LocalRestoreOperation struct {
	OperationID        string      `json:"operation_id"`
	Status             string      `json:"status"`
	Mode               RestoreMode `json:"mode"`
	TotalRecords       int         `json:"total_records"`
	CompletedRecords   int         `json:"completed_records"`
	ExpiresAt          time.Time   `json:"expires_at"`
	TemporaryExpiresAt *time.Time  `json:"temporary_expires_at,omitempty"`
	FailureCode        string      `json:"failure_code,omitempty"`
}

type RestoredCredential struct {
	RecordID string
	Revision int64
	VaultID  string
	ETag     string
	Provider ProviderCredential
}

type LocalRestoreState struct {
	Exists                 bool
	LocalProviderGroupID   string
	CurrentLocalRevision   int64
	LastBoundLocalRevision int64
	LastBoundCloudRevision int64
}

type RestoreSink interface {
	Inspect(context.Context, AccountScope, string) (LocalRestoreState, error)
	PersistTrusted(context.Context, AccountScope, []RestoredCredential, map[string]ConflictResolution, time.Time) error
	ActivateTemporary(context.Context, AccountScope, []RestoredCredential, time.Time) error
	ClearTemporary(context.Context, AccountScope) error
}

type RestoreCloud interface {
	Origin() string
	GetCurrentAccount(context.Context, string) (cloudclient.Account, error)
	ListCredentialVaultRecords(context.Context, string, string, int) (cloudclient.CredentialVaultRecordPage, error)
	CreateCredentialRestore(context.Context, string, string, cloudclient.CredentialRestoreRequest) (cloudclient.CredentialRestoreOperation, error)
	GetCredentialRestore(context.Context, string, string) (cloudclient.CredentialRestoreOperation, error)
	CancelCredentialRestore(context.Context, string, string) error
}

type RestoreServiceDeps struct {
	Tokens         AccessTokenSource
	Cloud          RestoreCloud
	Sink           RestoreSink
	Random         io.Reader
	Now            func() time.Time
	NewOperationID func() string
}

type restoreTask struct {
	operation        LocalRestoreOperation
	scope            AccountScope
	privateKey       *rsa.PrivateKey
	publicDER        []byte
	publicHash       string
	selections       []RestoreSelection
	resolutions      map[string]ConflictResolution
	recordETags      map[string]string
	recordKeyIDs     map[string]string
	vaultID          string
	batches          [][]RestoreSelection
	currentBatch     []RestoreSelection
	nextBatch        int
	batchNumber      int
	cloudOperationID string
	restored         []RestoredCredential
	terminal         bool
}

type RestoreService struct {
	deps            RestoreServiceDeps
	mu              sync.Mutex
	processMu       sync.Mutex
	operations      map[string]*restoreTask
	temporaryScopes map[AccountScope]struct{}
}

func NewRestoreService(deps RestoreServiceDeps) (*RestoreService, error) {
	if deps.Tokens == nil || deps.Cloud == nil || deps.Sink == nil || deps.Random == nil || deps.Now == nil || deps.NewOperationID == nil {
		return nil, ErrInvalidContract
	}
	return &RestoreService{deps: deps, operations: make(map[string]*restoreTask), temporaryScopes: make(map[AccountScope]struct{})}, nil
}

func (service *RestoreService) Discover(ctx context.Context) (RestoreDiscovery, error) {
	service.processMu.Lock()
	defer service.processMu.Unlock()
	service.prune()
	_, token, _, err := service.cloudScope(ctx)
	if err != nil {
		return RestoreDiscovery{}, err
	}
	records, err := service.listRecords(ctx, token)
	if err != nil {
		return RestoreDiscovery{}, err
	}
	discovery := make([]RestoreRecordSummary, 0, len(records))
	for _, record := range records {
		updatedAt, _ := time.Parse(time.RFC3339, record.UpdatedAt)
		discovery = append(discovery, RestoreRecordSummary{RecordID: record.RecordID, Revision: record.Revision, UpdatedAt: updatedAt})
	}
	var active *LocalRestoreOperation
	service.mu.Lock()
	for _, task := range service.operations {
		operation := task.operation
		active = &operation
		if !task.terminal {
			break
		}
	}
	service.mu.Unlock()
	return RestoreDiscovery{Available: true, RequiresExplicitAction: true, Records: discovery, ActiveOperation: active}, nil
}

func (service *RestoreService) Start(ctx context.Context, command RestoreCommand) (LocalRestoreOperation, error) {
	service.processMu.Lock()
	defer service.processMu.Unlock()
	if err := validRestoreCommand(command); err != nil {
		return LocalRestoreOperation{}, err
	}
	service.prune()
	service.mu.Lock()
	for _, task := range service.operations {
		if !task.terminal {
			service.mu.Unlock()
			return LocalRestoreOperation{}, ErrLocalConflict
		}
	}
	service.mu.Unlock()
	scope, token, _, err := service.cloudScope(ctx)
	if err != nil {
		return LocalRestoreOperation{}, err
	}
	cloudRecords, err := service.listRecords(ctx, token)
	if err != nil {
		return LocalRestoreOperation{}, err
	}
	available := make(map[string]int64, len(cloudRecords))
	recordETags := make(map[string]string, len(cloudRecords))
	recordKeyIDs := make(map[string]string, len(cloudRecords))
	for _, record := range cloudRecords {
		available[record.RecordID] = record.Revision
		recordETags[record.RecordID] = record.ETag
		recordKeyIDs[record.RecordID] = record.KeyID
	}
	resolutions := make(map[string]ConflictResolution, len(command.Selections))
	for _, selection := range command.Selections {
		if available[selection.RecordID] != selection.Revision {
			return LocalRestoreOperation{}, ErrLocalConflict
		}
		local, err := service.deps.Sink.Inspect(ctx, scope, selection.RecordID)
		if err != nil {
			return LocalRestoreOperation{}, err
		}
		if local.Exists && local.CurrentLocalRevision != local.LastBoundLocalRevision && selection.Resolution == ConflictFail {
			return LocalRestoreOperation{}, ErrLocalConflict
		}
		resolutions[selection.RecordID] = selection.Resolution
	}
	operationID := strings.TrimSpace(service.deps.NewOperationID())
	if !restoreUUID.MatchString(operationID) {
		return LocalRestoreOperation{}, ErrInvalidContract
	}
	privateKey, err := rsa.GenerateKey(service.deps.Random, 3072)
	if err != nil || privateKey.Validate() != nil || privateKey.E != 65537 {
		clearRSAPrivate(privateKey)
		return LocalRestoreOperation{}, ErrLocalSecureStoreUnavailable
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil || len(publicDER) != 422 {
		clearRSAPrivate(privateKey)
		return LocalRestoreOperation{}, ErrInvalidContract
	}
	digest := sha256.Sum256(publicDER)
	now := service.deps.Now().UTC()
	task := &restoreTask{
		operation: LocalRestoreOperation{OperationID: operationID, Status: "pending", Mode: command.Mode, TotalRecords: len(command.Selections), ExpiresAt: now.Add(RestoreTTL)},
		scope:     scope, privateKey: privateKey, publicDER: publicDER, publicHash: hex.EncodeToString(digest[:]),
		selections: append([]RestoreSelection(nil), command.Selections...), resolutions: resolutions,
		recordETags: recordETags, recordKeyIDs: recordKeyIDs,
		batches: buildRestoreBatches(command.Selections, recordKeyIDs),
	}
	if err := service.createNextBatch(ctx, token, task); err != nil {
		clearRestoreTask(task)
		return LocalRestoreOperation{}, err
	}
	service.mu.Lock()
	service.operations[operationID] = task
	service.mu.Unlock()
	return task.operation, nil
}

func (service *RestoreService) Advance(ctx context.Context, operationID string) (LocalRestoreOperation, error) {
	service.processMu.Lock()
	defer service.processMu.Unlock()
	service.prune()
	task, err := service.task(operationID)
	if err != nil {
		return LocalRestoreOperation{}, err
	}
	if task.terminal {
		return task.operation, nil
	}
	if task.privateKey == nil {
		return LocalRestoreOperation{}, ErrLocalKeyNotFound
	}
	scope, token, _, err := service.cloudScope(ctx)
	if err != nil {
		return LocalRestoreOperation{}, err
	}
	if scope != task.scope {
		return LocalRestoreOperation{}, ErrLocalConflict
	}
	cloudOperation, err := service.deps.Cloud.GetCredentialRestore(ctx, token, task.cloudOperationID)
	if err != nil {
		var cloudErr *cloudclient.CloudError
		if errors.As(err, &cloudErr) && cloudErr.Code == 3080008 {
			task.operation.Status = "expired"
			task.operation.FailureCode = "3080008"
			service.finishTask(task, service.deps.Now().UTC())
			return task.operation, nil
		}
		return LocalRestoreOperation{}, err
	}
	task.operation.ExpiresAt = parseRestoreTime(cloudOperation.ExpiresAt, task.operation.ExpiresAt)
	switch cloudOperation.Status {
	case "pending", "running":
		task.operation.Status = cloudOperation.Status
		return task.operation, nil
	case "failed", "expired", "canceled":
		task.operation.Status = cloudOperation.Status
		if cloudOperation.FailureCode != nil {
			task.operation.FailureCode = *cloudOperation.FailureCode
		}
		service.finishTask(task, service.deps.Now().UTC())
		return task.operation, nil
	case "succeeded":
		credentials, err := service.decryptBatch(task, cloudOperation)
		if err != nil {
			service.removeTask(task.operation.OperationID)
			clearRestoreTask(task)
			return LocalRestoreOperation{}, err
		}
		task.restored = append(task.restored, credentials...)
		task.operation.CompletedRecords = len(task.restored)
		if task.nextBatch < len(task.batches) {
			if err := service.createNextBatch(ctx, token, task); err != nil {
				service.removeTask(task.operation.OperationID)
				clearRestoreTask(task)
				return LocalRestoreOperation{}, err
			}
			return task.operation, nil
		}
		now := service.deps.Now().UTC()
		if task.operation.Mode == RestoreTrustedDevice {
			if err := service.deps.Sink.PersistTrusted(ctx, task.scope, task.restored, task.resolutions, now); err != nil {
				service.removeTask(task.operation.OperationID)
				clearRestoreTask(task)
				return LocalRestoreOperation{}, err
			}
		} else {
			expiresAt := now.Add(TemporaryRestoreAbsoluteTTL)
			if err := service.deps.Sink.ActivateTemporary(ctx, task.scope, task.restored, expiresAt); err != nil {
				service.removeTask(task.operation.OperationID)
				clearRestoreTask(task)
				return LocalRestoreOperation{}, err
			}
			task.operation.TemporaryExpiresAt = &expiresAt
			service.mu.Lock()
			service.temporaryScopes[task.scope] = struct{}{}
			service.mu.Unlock()
		}
		task.operation.Status = "succeeded"
		service.finishTask(task, now)
		return task.operation, nil
	default:
		return LocalRestoreOperation{}, ErrInvalidContract
	}
}

func (service *RestoreService) Cancel(ctx context.Context, operationID string) error {
	service.processMu.Lock()
	defer service.processMu.Unlock()
	task, err := service.task(operationID)
	if err != nil {
		return err
	}
	if task.terminal {
		return nil
	}
	scope, token, _, err := service.cloudScope(ctx)
	if err != nil {
		return err
	}
	if scope != task.scope {
		return ErrLocalConflict
	}
	if err := service.deps.Cloud.CancelCredentialRestore(ctx, token, task.cloudOperationID); err != nil {
		return err
	}
	task.operation.Status = "canceled"
	service.finishTask(task, service.deps.Now().UTC())
	return nil
}

func (service *RestoreService) ClearTemporary(ctx context.Context) error {
	service.processMu.Lock()
	defer service.processMu.Unlock()
	service.mu.Lock()
	scopes := make([]AccountScope, 0, len(service.temporaryScopes))
	for scope := range service.temporaryScopes {
		scopes = append(scopes, scope)
	}
	service.mu.Unlock()
	for _, scope := range scopes {
		if err := service.deps.Sink.ClearTemporary(ctx, scope); err != nil {
			return err
		}
		service.mu.Lock()
		delete(service.temporaryScopes, scope)
		service.mu.Unlock()
	}
	return nil
}

func (service *RestoreService) cloudScope(ctx context.Context) (AccountScope, string, cloudclient.Account, error) {
	token, err := service.deps.Tokens.AccessToken(ctx, 30*time.Second)
	if err != nil {
		return AccountScope{}, "", cloudclient.Account{}, err
	}
	account, err := service.deps.Cloud.GetCurrentAccount(ctx, token)
	if err != nil {
		return AccountScope{}, "", cloudclient.Account{}, err
	}
	if !restoreUUID.MatchString(account.ID) {
		return AccountScope{}, "", cloudclient.Account{}, ErrInvalidContract
	}
	scope := AccountScope{CloudIssuer: service.deps.Cloud.Origin(), CloudAccountID: account.ID}
	if !validAccountScope(scope) {
		return AccountScope{}, "", cloudclient.Account{}, ErrInvalidContract
	}
	return scope, token, account, nil
}

func (service *RestoreService) listRecords(ctx context.Context, token string) ([]cloudclient.CredentialVaultRecordSummary, error) {
	var records []cloudclient.CredentialVaultRecordSummary
	cursor := ""
	seenCursors := make(map[string]struct{})
	for {
		page, err := service.deps.Cloud.ListCredentialVaultRecords(ctx, token, cursor, 100)
		if err != nil {
			return nil, err
		}
		records = append(records, page.Items...)
		if len(records) > 1000 {
			return nil, ErrInvalidContract
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return records, nil
		}
		if _, found := seenCursors[*page.NextCursor]; found {
			return nil, ErrInvalidContract
		}
		seenCursors[*page.NextCursor] = struct{}{}
		cursor = *page.NextCursor
	}
}

func (service *RestoreService) createNextBatch(ctx context.Context, token string, task *restoreTask) error {
	if task.nextBatch >= len(task.batches) {
		return ErrInvalidContract
	}
	batch := append([]RestoreSelection(nil), task.batches[task.nextBatch]...)
	records := make([]cloudclient.CredentialRestoreRecordRequest, 0, len(batch))
	for _, selection := range batch {
		records = append(records, cloudclient.CredentialRestoreRecordRequest{RecordID: selection.RecordID, Revision: selection.Revision})
	}
	idempotencyKey := task.operation.OperationID + "-" + strconv.Itoa(task.batchNumber)
	operation, err := service.deps.Cloud.CreateCredentialRestore(ctx, token, idempotencyKey, cloudclient.CredentialRestoreRequest{
		Mode: string(task.operation.Mode), Records: records, RecipientPublicKey: task.publicDER, RecipientPublicKeyHash: task.publicHash,
	})
	if err != nil {
		return err
	}
	task.currentBatch, task.nextBatch, task.batchNumber = batch, task.nextBatch+1, task.batchNumber+1
	task.cloudOperationID = operation.OperationID
	task.operation.Status = operation.Status
	task.operation.ExpiresAt = parseRestoreTime(operation.ExpiresAt, task.operation.ExpiresAt)
	return nil
}

func buildRestoreBatches(selections []RestoreSelection, keyIDs map[string]string) [][]RestoreSelection {
	order := make([]string, 0)
	grouped := make(map[string][]RestoreSelection)
	for _, selection := range selections {
		keyID := keyIDs[selection.RecordID]
		if _, found := grouped[keyID]; !found {
			order = append(order, keyID)
		}
		grouped[keyID] = append(grouped[keyID], selection)
	}
	var batches [][]RestoreSelection
	for _, keyID := range order {
		items := grouped[keyID]
		for len(items) > 0 {
			count := min(MaximumRestoreRecords, len(items))
			batches = append(batches, append([]RestoreSelection(nil), items[:count]...))
			items = items[count:]
		}
	}
	return batches
}

func (service *RestoreService) decryptBatch(task *restoreTask, operation cloudclient.CredentialRestoreOperation) ([]RestoredCredential, error) {
	if operation.Result == nil || len(operation.Result.Items) != len(task.currentBatch) {
		return nil, ErrInvalidContract
	}
	requested := make(map[string]int64, len(task.currentBatch))
	for _, selection := range task.currentBatch {
		requested[selection.RecordID] = selection.Revision
	}
	credentials := make([]RestoredCredential, 0, len(operation.Result.Items))
	for _, item := range operation.Result.Items {
		if requested[item.RecordID] != item.Revision || item.AAD.CloudIssuer != task.scope.CloudIssuer || item.AAD.CloudAccountID != task.scope.CloudAccountID ||
			item.AAD.RecordID != item.RecordID || item.AAD.Revision != item.Revision || item.AAD.PayloadType != "provider-credential" ||
			item.AAD.KeyID != task.recordKeyIDs[item.RecordID] || task.vaultID != "" && item.AAD.VaultID != task.vaultID {
			clearRestoredCredentials(credentials)
			return nil, ErrInvalidContract
		}
		if task.vaultID == "" {
			task.vaultID = item.AAD.VaultID
		}
		aad := RecordAAD{
			ProtocolVersion: item.AAD.ProtocolVersion, CloudIssuer: item.AAD.CloudIssuer, CloudAccountID: item.AAD.CloudAccountID,
			VaultID: item.AAD.VaultID, RecordID: item.AAD.RecordID, Revision: item.AAD.Revision,
			KeyID: item.AAD.KeyID, PayloadType: item.AAD.PayloadType,
		}
		canonicalAAD, err := CanonicalAAD(aad)
		if err != nil {
			clearRestoredCredentials(credentials)
			return nil, err
		}
		dek, err := rsa.DecryptOAEP(sha256.New(), service.deps.Random, task.privateKey, item.DeviceWrappedDEK, canonicalAAD)
		if err != nil || len(dek) != RecordDEKSize {
			clear(dek)
			clearRestoredCredentials(credentials)
			return nil, ErrInvalidContract
		}
		plaintext, err := DecryptPayload(dek, item.Nonce, canonicalAAD, item.Ciphertext)
		clear(dek)
		if err != nil {
			clearRestoredCredentials(credentials)
			return nil, ErrInvalidContract
		}
		provider, err := decodeRestoredProvider(plaintext)
		clear(plaintext)
		if err != nil {
			clearRestoredCredentials(credentials)
			return nil, err
		}
		credentials = append(credentials, RestoredCredential{RecordID: item.RecordID, Revision: item.Revision, VaultID: item.AAD.VaultID, ETag: task.recordETags[item.RecordID], Provider: provider})
		delete(requested, item.RecordID)
	}
	if len(requested) != 0 {
		clearRestoredCredentials(credentials)
		return nil, ErrInvalidContract
	}
	return credentials, nil
}

func decodeRestoredProvider(plaintext []byte) (ProviderCredential, error) {
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var payload struct {
		SchemaVersion          int            `json:"schema_version"`
		ProviderCatalogKey     string         `json:"provider_catalog_key"`
		DisplayName            string         `json:"display_name"`
		BaseURL                string         `json:"base_url"`
		CredentialType         string         `json:"credential_type"`
		APIKey                 string         `json:"api_key"`
		ProviderSpecificFields map[string]any `json:"provider_specific_fields"`
	}
	if err := decoder.Decode(&payload); err != nil {
		return ProviderCredential{}, ErrInvalidContract
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ProviderCredential{}, ErrInvalidContract
	}
	providerKey := strings.TrimSpace(payload.ProviderCatalogKey)
	displayName := strings.TrimSpace(payload.DisplayName)
	baseURL := strings.TrimSpace(payload.BaseURL)
	apiKey := strings.TrimSpace(payload.APIKey)
	parsedURL, urlErr := url.Parse(baseURL)
	if payload.SchemaVersion != 1 || providerKey == "" || len(providerKey) > 128 || displayName == "" || len(displayName) > 255 ||
		len(baseURL) > 1024 || urlErr != nil || parsedURL.Host == "" || parsedURL.Scheme != "https" && parsedURL.Scheme != "http" ||
		payload.CredentialType != "api_key" || apiKey == "" || len(apiKey) > 512 || len(payload.ProviderSpecificFields) != 0 {
		payload.APIKey = ""
		return ProviderCredential{}, ErrInvalidContract
	}
	result := ProviderCredential{CredentialRevision: 1, ProviderCatalogKey: providerKey, DisplayName: displayName, BaseURL: baseURL, APIKey: []byte(apiKey)}
	payload.APIKey = ""
	return result, nil
}

func validRestoreCommand(command RestoreCommand) error {
	if command.Mode != RestoreTrustedDevice && command.Mode != RestoreTemporary || len(command.Selections) < 1 || len(command.Selections) > 1000 {
		return ErrInvalidContract
	}
	seen := make(map[string]struct{}, len(command.Selections))
	for _, selection := range command.Selections {
		if !restoreUUID.MatchString(selection.RecordID) || selection.Revision < 1 ||
			selection.Resolution != ConflictFail && selection.Resolution != ConflictReplaceLocal && selection.Resolution != ConflictSaveCopy {
			return ErrInvalidContract
		}
		if _, found := seen[selection.RecordID]; found {
			return ErrInvalidContract
		}
		seen[selection.RecordID] = struct{}{}
	}
	return nil
}

func (service *RestoreService) task(operationID string) (*restoreTask, error) {
	if !restoreUUID.MatchString(operationID) {
		return nil, ErrRestoreOperationNotFound
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	task, found := service.operations[operationID]
	if !found {
		return nil, ErrRestoreOperationNotFound
	}
	return task, nil
}

func (service *RestoreService) finishTask(task *restoreTask, now time.Time) {
	clearRSAPrivate(task.privateKey)
	task.privateKey = nil
	clear(task.publicDER)
	task.publicDER = nil
	clearRestoredCredentials(task.restored)
	task.restored = nil
	task.terminal = true
	task.operation.ExpiresAt = now.Add(5 * time.Minute)
}

func (service *RestoreService) removeTask(operationID string) {
	service.mu.Lock()
	delete(service.operations, operationID)
	service.mu.Unlock()
}

func (service *RestoreService) prune() {
	now := service.deps.Now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	for operationID, task := range service.operations {
		if task.terminal && !now.Before(task.operation.ExpiresAt) {
			delete(service.operations, operationID)
		}
	}
}

func clearRestoreTask(task *restoreTask) {
	if task == nil {
		return
	}
	clearRSAPrivate(task.privateKey)
	clear(task.publicDER)
	clearRestoredCredentials(task.restored)
}

func clearRestoredCredentials(credentials []RestoredCredential) {
	for index := range credentials {
		clear(credentials[index].Provider.APIKey)
		credentials[index].Provider.APIKey = nil
	}
}

func clearRSAPrivate(privateKey *rsa.PrivateKey) {
	if privateKey == nil {
		return
	}
	if privateKey.D != nil {
		privateKey.D.SetInt64(0)
	}
	for _, prime := range privateKey.Primes {
		if prime != nil {
			prime.SetInt64(0)
		}
	}
	privateKey.Primes = nil
	privateKey.Precomputed = rsa.PrecomputedValues{}
}

func parseRestoreTime(value string, fallback time.Time) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return fallback
	}
	return parsed
}

var restoreUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var _ RestoreCloud = (*cloudclient.Client)(nil)
