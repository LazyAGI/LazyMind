package credentialvault

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/cloudclient"
)

type restoreTestCloud struct {
	origin         string
	account        cloudclient.Account
	records        []cloudclient.CredentialVaultRecordSummary
	results        map[string]cloudclient.CredentialRestoreOperation
	createCalls    int
	cancelCalls    int
	createdBatches [][]cloudclient.CredentialRestoreRecordRequest
	tamper         func(*cloudclient.CredentialRestoreResultItem)
	payload        func(map[string]any)
}

func (cloud *restoreTestCloud) Origin() string { return cloud.origin }
func (cloud *restoreTestCloud) GetCurrentAccount(context.Context, string) (cloudclient.Account, error) {
	return cloud.account, nil
}
func (cloud *restoreTestCloud) ListCredentialVaultRecords(context.Context, string, string, int) (cloudclient.CredentialVaultRecordPage, error) {
	return cloudclient.CredentialVaultRecordPage{Items: append([]cloudclient.CredentialVaultRecordSummary(nil), cloud.records...)}, nil
}
func (cloud *restoreTestCloud) CreateCredentialRestore(_ context.Context, _ string, _ string, request cloudclient.CredentialRestoreRequest) (cloudclient.CredentialRestoreOperation, error) {
	cloud.createCalls++
	cloud.createdBatches = append(cloud.createdBatches, append([]cloudclient.CredentialRestoreRecordRequest(nil), request.Records...))
	parsed, err := x509.ParsePKIXPublicKey(request.RecipientPublicKey)
	recipient, ok := parsed.(*rsa.PublicKey)
	if err != nil || !ok || recipient.N.BitLen() != 3072 {
		return cloudclient.CredentialRestoreOperation{}, fmt.Errorf("invalid recipient key")
	}
	digest := sha256.Sum256(request.RecipientPublicKey)
	if request.RecipientPublicKeyHash != hex.EncodeToString(digest[:]) || len(request.Records) == 0 || len(request.Records) > 20 {
		return cloudclient.CredentialRestoreOperation{}, fmt.Errorf("invalid restore batch")
	}
	operationID := fmt.Sprintf("00000000-0000-7000-8000-%012d", cloud.createCalls+500)
	result := cloudclient.CredentialRestoreResult{Items: make([]cloudclient.CredentialRestoreResultItem, 0, len(request.Records))}
	for _, reference := range request.Records {
		keyID := ""
		for _, record := range cloud.records {
			if record.RecordID == reference.RecordID {
				keyID = record.KeyID
				break
			}
		}
		result.Items = append(result.Items, restoreResultFixture(reference, recipient, cloud.origin, cloud.account.ID, keyID, cloud.payload, cloud.tamper))
	}
	completed := "2026-08-26T10:00:02Z"
	cloud.results[operationID] = cloudclient.CredentialRestoreOperation{
		OperationID: operationID, Status: "succeeded", ExpiresAt: "2026-08-26T10:05:00Z",
		CreatedAt: "2026-08-26T10:00:00Z", CompletedAt: &completed, Result: &result,
	}
	return cloudclient.CredentialRestoreOperation{OperationID: operationID, Status: "pending", ExpiresAt: "2026-08-26T10:05:00Z", CreatedAt: "2026-08-26T10:00:00Z"}, nil
}
func (cloud *restoreTestCloud) GetCredentialRestore(_ context.Context, _ string, operationID string) (cloudclient.CredentialRestoreOperation, error) {
	operation, found := cloud.results[operationID]
	if !found {
		return cloudclient.CredentialRestoreOperation{}, fmt.Errorf("restore operation not found")
	}
	return operation, nil
}
func (cloud *restoreTestCloud) CancelCredentialRestore(context.Context, string, string) error {
	cloud.cancelCalls++
	return nil
}

type restoreTestSink struct {
	state          LocalRestoreState
	trusted        []RestoredCredential
	temporary      []RestoredCredential
	temporaryUntil time.Time
	clearCalls     int
}

func (sink *restoreTestSink) Inspect(context.Context, AccountScope, string) (LocalRestoreState, error) {
	return sink.state, nil
}
func (sink *restoreTestSink) PersistTrusted(_ context.Context, _ AccountScope, credentials []RestoredCredential, _ map[string]ConflictResolution, _ time.Time) error {
	sink.trusted = cloneRestoredCredentials(credentials)
	return nil
}
func (sink *restoreTestSink) ActivateTemporary(_ context.Context, _ AccountScope, credentials []RestoredCredential, expiresAt time.Time) error {
	sink.temporary, sink.temporaryUntil = cloneRestoredCredentials(credentials), expiresAt
	return nil
}
func (sink *restoreTestSink) ClearTemporary(context.Context, AccountScope) error {
	for index := range sink.temporary {
		clear(sink.temporary[index].Provider.APIKey)
	}
	sink.temporary = nil
	sink.clearCalls++
	return nil
}

func cloneRestoredCredentials(input []RestoredCredential) []RestoredCredential {
	result := append([]RestoredCredential(nil), input...)
	for index := range result {
		result[index].Provider.APIKey = append([]byte(nil), input[index].Provider.APIKey...)
	}
	return result
}

func restoreResultFixture(reference cloudclient.CredentialRestoreRecordRequest, recipient *rsa.PublicKey, issuer, accountID, keyID string, mutatePayload func(map[string]any), tamper func(*cloudclient.CredentialRestoreResultItem)) cloudclient.CredentialRestoreResultItem {
	aad := RecordAAD{
		ProtocolVersion: ProtocolVersion, CloudIssuer: issuer, CloudAccountID: accountID,
		VaultID: "00000000-0000-7000-8000-000000000201", RecordID: reference.RecordID,
		Revision: reference.Revision, KeyID: keyID, PayloadType: "provider-credential",
	}
	canonicalAAD, _ := CanonicalAAD(aad)
	dek := bytes.Repeat([]byte{byte(reference.Revision)}, 32)
	payloadValue := map[string]any{
		"schema_version": 1, "provider_catalog_key": "fixture-provider", "display_name": "Fixture Provider",
		"base_url": "https://provider.example.test", "credential_type": "api_key",
		"api_key": "fixture-restored-api-key", "provider_specific_fields": map[string]any{},
	}
	if mutatePayload != nil {
		mutatePayload(payloadValue)
	}
	payload, _ := json.Marshal(payloadValue)
	nonce, ciphertext, _ := EncryptPayload(rand.Reader, dek, canonicalAAD, payload)
	wrapped, _ := rsa.EncryptOAEP(sha256.New(), rand.Reader, recipient, dek, canonicalAAD)
	item := cloudclient.CredentialRestoreResultItem{
		RecordID: reference.RecordID, Revision: reference.Revision, Nonce: nonce,
		AAD: cloudclient.CredentialRecordAAD{
			ProtocolVersion: aad.ProtocolVersion, CloudIssuer: aad.CloudIssuer, CloudAccountID: aad.CloudAccountID,
			VaultID: aad.VaultID, RecordID: aad.RecordID, Revision: aad.Revision, KeyID: aad.KeyID, PayloadType: aad.PayloadType,
		},
		Ciphertext: ciphertext, DeviceWrappedDEK: wrapped,
		SigningMemberID: "00000000-0000-7000-8000-000000000402", SigningKeyVersion: 1,
		Signature: bytes.Repeat([]byte{0x44}, 64),
	}
	if tamper != nil {
		tamper(&item)
	}
	clear(dek)
	clear(payload)
	return item
}

func newRestoreServiceFixture(t *testing.T, recordCount int, mode RestoreMode) (*RestoreService, *restoreTestCloud, *restoreTestSink, RestoreCommand) {
	t.Helper()
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	cloud := &restoreTestCloud{
		origin: "https://cloud.example.test", account: cloudclient.Account{ID: "00000000-0000-7000-8000-000000000101"},
		results: make(map[string]cloudclient.CredentialRestoreOperation),
	}
	command := RestoreCommand{Mode: mode}
	for index := 1; index <= recordCount; index++ {
		recordID := fmt.Sprintf("00000000-0000-7000-8000-%012d", index+400)
		cloud.records = append(cloud.records, cloudclient.CredentialVaultRecordSummary{
			RecordID: recordID, Revision: int64(index), KeyID: "credential-shard-17-v1",
			CryptoSuite: CredentialCryptoSuite, SigningMemberID: "00000000-0000-7000-8000-000000000402",
			UpdatedAt: now.Format(time.RFC3339), ETagVersion: int64(index), ETag: strings.Repeat("e", 64),
		})
		command.Selections = append(command.Selections, RestoreSelection{RecordID: recordID, Revision: int64(index), Resolution: ConflictFail})
	}
	sink := &restoreTestSink{}
	service, err := NewRestoreService(RestoreServiceDeps{
		Tokens: backupTestTokens{token: "fixture-access"}, Cloud: cloud, Sink: sink, Random: rand.Reader,
		Now: func() time.Time { return now }, NewOperationID: func() string { return "00000000-0000-7000-8000-000000000499" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, cloud, sink, command
}

func TestRestoreDiscoveryNeverStartsCloudRestoreWithoutExplicitUserAction(t *testing.T) {
	service, cloud, _, _ := newRestoreServiceFixture(t, 2, RestoreTrustedDevice)
	discovery, err := service.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover credential backups: %v", err)
	}
	if !discovery.Available || !discovery.RequiresExplicitAction || len(discovery.Records) != 2 || cloud.createCalls != 0 {
		t.Fatalf("unsafe restore discovery/create calls = %+v/%d", discovery, cloud.createCalls)
	}
}

func TestRestoreDiscoveryIsANonErrorStateWhenCloudIsNotConfigured(t *testing.T) {
	var handler *RestoreHandler
	recorder := httptest.NewRecorder()
	handler.Discover(recorder, httptest.NewRequest(http.MethodGet, "/credential-vault/restores", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Available  bool   `json:"available"`
			ReasonCode string `json:"reason_code"`
			Records    []any  `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Available || response.Data.ReasonCode != "cloud_session_required" || len(response.Data.Records) != 0 {
		t.Fatalf("unexpected signed-out discovery: %#v", response.Data)
	}
}

func TestOfflineSourcePCTrustedRestoreDecryptsAndPersistsAllRecordsAfterExplicitStart(t *testing.T) {
	service, cloud, sink, command := newRestoreServiceFixture(t, 2, RestoreTrustedDevice)
	started, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("start trusted restore: %v", err)
	}
	completed, err := service.Advance(context.Background(), started.OperationID)
	if err != nil {
		t.Fatalf("advance trusted restore: %v", err)
	}
	if completed.Status != "succeeded" || completed.CompletedRecords != 2 || cloud.createCalls != 1 || len(sink.trusted) != 2 || len(sink.temporary) != 0 {
		t.Fatalf("trusted restore outcome/cloud/sink = %+v/%d/%d/%d", completed, cloud.createCalls, len(sink.trusted), len(sink.temporary))
	}
	for _, restored := range sink.trusted {
		if string(restored.Provider.APIKey) != "fixture-restored-api-key" || restored.Provider.BaseURL != "https://provider.example.test" {
			t.Fatalf("restored provider = %+v", restored.Provider)
		}
	}
}

func TestRestoreOneUserActionBatchesMoreThanTwentyRecordsWithoutReusingACloudBatch(t *testing.T) {
	service, cloud, sink, command := newRestoreServiceFixture(t, 21, RestoreTrustedDevice)
	started, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("start 21-record restore: %v", err)
	}
	operation := started
	for attempts := 0; attempts < 3 && operation.Status != "succeeded"; attempts++ {
		operation, err = service.Advance(context.Background(), operation.OperationID)
		if err != nil {
			t.Fatalf("advance batched restore: %v", err)
		}
	}
	if operation.CompletedRecords != 21 || cloud.createCalls != 2 || len(sink.trusted) != 21 {
		t.Fatalf("batched restore = %+v creates=%d persisted=%d", operation, cloud.createCalls, len(sink.trusted))
	}
}

func TestTemporaryRestoreNeverPersistsAndClearsOnLockOrTimeout(t *testing.T) {
	service, _, sink, command := newRestoreServiceFixture(t, 1, RestoreTemporary)
	started, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("start temporary restore: %v", err)
	}
	completed, err := service.Advance(context.Background(), started.OperationID)
	if err != nil {
		t.Fatalf("advance temporary restore: %v", err)
	}
	if len(sink.trusted) != 0 || len(sink.temporary) != 1 || completed.TemporaryExpiresAt == nil ||
		completed.TemporaryExpiresAt.Sub(time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)) != TemporaryRestoreAbsoluteTTL {
		t.Fatalf("temporary restore persisted or has unsafe expiry: %+v trusted=%d temporary=%d", completed, len(sink.trusted), len(sink.temporary))
	}
	encoded, _ := json.Marshal(completed)
	if bytes.Contains(bytes.ToLower(encoded), []byte("api_key")) || bytes.Contains(encoded, []byte("fixture-restored-api-key")) {
		t.Fatalf("temporary operation exposed credential: %s", encoded)
	}
	if err := service.ClearTemporary(context.Background()); err != nil {
		t.Fatalf("clear temporary restore on lock: %v", err)
	}
	if len(sink.temporary) != 0 || sink.clearCalls != 1 {
		t.Fatalf("temporary restore was not cleared: count=%d clears=%d", len(sink.temporary), sink.clearCalls)
	}
}

func TestRestoreConflictAndTamperingFailClosedWithoutPartialPersistence(t *testing.T) {
	t.Run("both sides changed", func(t *testing.T) {
		service, cloud, sink, command := newRestoreServiceFixture(t, 1, RestoreTrustedDevice)
		sink.state = LocalRestoreState{Exists: true, CurrentLocalRevision: 9, LastBoundLocalRevision: 7, LastBoundCloudRevision: 6}
		if _, err := service.Start(context.Background(), command); !errorsIs(err, ErrLocalConflict) {
			t.Fatalf("conflicting restore error = %v", err)
		}
		if cloud.createCalls != 0 || len(sink.trusted) != 0 {
			t.Fatalf("conflict started Cloud restore or persisted data: %d/%d", cloud.createCalls, len(sink.trusted))
		}
	})
	for name, tamper := range map[string]func(*cloudclient.CredentialRestoreResultItem){
		"aad account": func(item *cloudclient.CredentialRestoreResultItem) {
			item.AAD.CloudAccountID = "00000000-0000-7000-8000-000000009999"
		},
		"ciphertext":         func(item *cloudclient.CredentialRestoreResultItem) { item.Ciphertext[0] ^= 0xff },
		"recipient wrapping": func(item *cloudclient.CredentialRestoreResultItem) { item.DeviceWrappedDEK[0] ^= 0xff },
	} {
		t.Run(name, func(t *testing.T) {
			service, cloud, sink, command := newRestoreServiceFixture(t, 1, RestoreTrustedDevice)
			cloud.tamper = tamper
			started, err := service.Start(context.Background(), command)
			if err != nil {
				t.Fatalf("start tampered restore: %v", err)
			}
			if _, err := service.Advance(context.Background(), started.OperationID); err == nil {
				t.Fatal("tampered restore result was accepted")
			}
			if len(sink.trusted) != 0 || len(sink.temporary) != 0 {
				t.Fatal("tampered restore partially persisted credentials")
			}
		})
	}
}

func TestRestoreConflictProceedsOnlyWithExplicitReplaceOrSaveCopy(t *testing.T) {
	for _, resolution := range []ConflictResolution{ConflictReplaceLocal, ConflictSaveCopy} {
		t.Run(string(resolution), func(t *testing.T) {
			service, cloud, sink, command := newRestoreServiceFixture(t, 1, RestoreTrustedDevice)
			sink.state = LocalRestoreState{Exists: true, CurrentLocalRevision: 9, LastBoundLocalRevision: 7, LastBoundCloudRevision: 6}
			command.Selections[0].Resolution = resolution
			started, err := service.Start(context.Background(), command)
			if err != nil {
				t.Fatalf("start explicitly resolved conflict: %v", err)
			}
			completed, err := service.Advance(context.Background(), started.OperationID)
			if err != nil {
				t.Fatalf("advance explicitly resolved conflict: %v", err)
			}
			if completed.Status != "succeeded" || cloud.createCalls != 1 || len(sink.trusted) != 1 {
				t.Fatalf("resolved conflict outcome = %+v creates=%d persisted=%d", completed, cloud.createCalls, len(sink.trusted))
			}
		})
	}
}

func TestRestoreRejectsUnsupportedOrOversizedCredentialPayloadsAfterValidAEAD(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"schema":          func(payload map[string]any) { payload["schema_version"] = 2 },
		"credential type": func(payload map[string]any) { payload["credential_type"] = "oauth" },
		"api key length":  func(payload map[string]any) { payload["api_key"] = strings.Repeat("k", 513) },
		"unknown field":   func(payload map[string]any) { payload["unreviewed_secret"] = "fixture" },
	} {
		t.Run(name, func(t *testing.T) {
			service, cloud, sink, command := newRestoreServiceFixture(t, 1, RestoreTrustedDevice)
			cloud.payload = mutate
			started, err := service.Start(context.Background(), command)
			if err != nil {
				t.Fatalf("start invalid-payload restore: %v", err)
			}
			if _, err := service.Advance(context.Background(), started.OperationID); err == nil {
				t.Fatal("invalid credential payload was accepted")
			}
			if len(sink.trusted) != 0 || len(sink.temporary) != 0 {
				t.Fatal("invalid credential payload was partially persisted")
			}
		})
	}
}

func TestRestoreLostRecipientPrivateKeyCannotDowngradeOrRecoverAfterCoreRestart(t *testing.T) {
	service, cloud, _, command := newRestoreServiceFixture(t, 1, RestoreTrustedDevice)
	started, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}
	restarted, _, _, _ := newRestoreServiceFixture(t, 1, RestoreTrustedDevice)
	if _, err := restarted.Advance(context.Background(), started.OperationID); err == nil {
		t.Fatal("new Core process recovered without the ephemeral recipient private key")
	}
	if err := service.Cancel(context.Background(), started.OperationID); err != nil {
		t.Fatalf("cancel abandoned restore: %v", err)
	}
	if cloud.cancelCalls != 1 || strings.Contains(fmt.Sprintf("%+v", started), "PRIVATE KEY") {
		t.Fatalf("abandoned restore cleanup/private material = %d/%+v", cloud.cancelCalls, started)
	}
}

func errorsIs(err, target error) bool {
	return err != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}
