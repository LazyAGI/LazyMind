package vocabulary

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"strings"
	"testing"
	"time"
)

func TestCloudSelectionSavesLocallyWithAnkiDefault(t *testing.T) {
	s := testService(t)
	// No provider settings: historical default is Anki and it is offline.
	got, err := s.AddWord(context.Background(), "u", AddWordRequest{Provider: "local", Term: "evidence", Language: "en", Sentence: "The evidence is clear."})
	if err != nil {
		t.Fatal(err)
	}
	if got.Word.ID == "" || got.Word.Provider != "local" {
		t.Fatalf("word not saved locally: %+v", got)
	}
	var count int64
	s.db.Model(&Word{}).Where("owner_id = ?", "u").Count(&count)
	if count != 1 {
		t.Fatalf("local words=%d", count)
	}
}

type cloudTestStore map[string][]byte

func (s cloudTestStore) Load(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) ([]byte, error) {
	v, ok := s[scope.CloudAccountID+string(kind)]
	if !ok {
		return nil, credentialvault.ErrLocalKeyNotFound
	}
	return append([]byte(nil), v...), nil
}
func (s cloudTestStore) Save(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind, v []byte) error {
	s[scope.CloudAccountID+string(kind)] = append([]byte(nil), v...)
	return nil
}
func (s cloudTestStore) Delete(_ context.Context, scope credentialvault.AccountScope, kind credentialvault.LocalKeyKind) error {
	delete(s, scope.CloudAccountID+string(kind))
	return nil
}

type cloudFakeRemote struct {
	calls int
	fail  error
}

func (f *cloudFakeRemote) ListWordbooks(context.Context) ([]CloudWordbook, error) {
	return []CloudWordbook{{ID: "book", Name: "test book"}}, nil
}
func (f *cloudFakeRemote) CreateWordbook(context.Context, string) (CloudWordbook, error) {
	return CloudWordbook{ID: "book", Name: "test book"}, nil
}
func (f *cloudFakeRemote) AddWord(context.Context, string, string, bool) error {
	f.calls++
	return f.fail
}
func setupCloudService(t *testing.T) (*Service, *cloudFakeRemote) {
	t.Helper()
	s := testService(t)
	keys, err := credentialvault.NewLocalKeyManager(cloudTestStore{}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetCloudCredentialKeyManager(keys))
	remote := &cloudFakeRemote{}
	s.remoteClient = func(string, string) (cloudRemote, error) { return remote, nil }
	return s, remote
}
func saveTestCloud(t *testing.T, s *Service, owner, provider string) CloudConnection {
	t.Helper()
	c, err := s.SaveCloudConnection(context.Background(), owner, provider, CloudConnectionInput{Token: "secret-token", Enabled: true, TargetID: "book"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func runTestCloud(t *testing.T, s *Service, op CloudOperation) (asyncjob.Result, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"operation_id": op.ID})
	return s.runCloudOperation(context.Background(), asyncjob.Job{ID: op.JobID, PayloadJSON: raw, CreateUserID: op.OwnerID, AttemptCount: 1, MaxAttempts: 8})
}
func TestCloudLocalSaveQueuesBothAndRetriesWithoutDuplicates(t *testing.T) {
	s, remote := setupCloudService(t)
	saveTestCloud(t, s, "u", "eudic")
	saveTestCloud(t, s, "u", "maimemo")
	req := AddWordRequest{Provider: "local", Term: "evidence", Language: "en", Sentence: "The evidence is clear."}
	first, err := s.AddWord(context.Background(), "u", req)
	if err != nil || first.Word.ID == "" || !first.CloudQueued {
		t.Fatalf("%+v %v", first, err)
	}
	if remote.calls != 0 {
		t.Fatal("save performed remote I/O")
	}
	second, err := s.AddWord(context.Background(), "u", req)
	if err != nil || second.Word.ID != first.Word.ID {
		t.Fatalf("duplicate %+v %v", second, err)
	}
	ops, err := s.CloudOperations(context.Background(), "u")
	if err != nil || len(ops) != 2 {
		t.Fatalf("operations %+v %v", ops, err)
	}
	remote.fail = &CloudClientError{Code: "request_failed", Retryable: true}
	if _, err = runTestCloud(t, s, ops[0]); err == nil {
		t.Fatal("expected failed delivery")
	}
	var saved Word
	if s.db.First(&saved, "id = ?", first.Word.ID).Error != nil {
		t.Fatal("offline sync lost local word")
	}
	remote.fail = nil
	for _, op := range ops {
		if _, err = runTestCloud(t, s, op); err != nil {
			t.Fatal(err)
		}
	}
	calls := remote.calls
	for _, op := range ops {
		if _, err = runTestCloud(t, s, op); err != nil {
			t.Fatal(err)
		}
	}
	if remote.calls != calls {
		t.Fatal("replayed successful remote operation")
	}
	if n, err := s.BackfillCloud(context.Background(), "u", "eudic"); err != nil || n != 0 {
		t.Fatalf("backfill=%d %v", n, err)
	}
}
func TestCloudCredentialsAreEncryptedScopedAndNeverReturned(t *testing.T) {
	s, _ := setupCloudService(t)
	c := saveTestCloud(t, s, "u", "eudic")
	if strings.Contains(c.TokenCipher, "secret-token") {
		t.Fatal("plaintext credential")
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "secret-token") || strings.Contains(string(raw), "cipher") {
		t.Fatal("credential exposed")
	}
	c.OwnerID = "other"
	if _, err := decryptCloudToken(context.Background(), c); err == nil {
		t.Fatal("cross-owner decrypt")
	}
	other, err := s.CloudConnections(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range other {
		if c.Configured {
			t.Fatal("cross-owner connection")
		}
	}
	if _, err := s.cloudClientFor(context.Background(), "other", "eudic", ""); err == nil {
		t.Fatal("cross-owner token access")
	}
}
func TestCloudTokenRotationAndDisconnectCancelOldJobs(t *testing.T) {
	s, remote := setupCloudService(t)
	old := saveTestCloud(t, s, "u", "eudic")
	_, err := s.AddWord(context.Background(), "u", AddWordRequest{Provider: "local", Term: "word", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	ops, _ := s.CloudOperations(context.Background(), "u")
	next := saveTestCloud(t, s, "u", "eudic")
	if next.ID == old.ID {
		t.Fatal("token replacement reused destination identity")
	}
	if _, err := runTestCloud(t, s, ops[0]); err != nil {
		t.Fatal(err)
	}
	if remote.calls != 0 {
		t.Fatal("old job sent to new account")
	}
	if n, err := s.BackfillCloud(context.Background(), "u", "eudic"); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if err := s.DisconnectCloud(context.Background(), "u", "eudic"); err != nil {
		t.Fatal(err)
	}
	ops, _ = s.CloudOperations(context.Background(), "u")
	for _, op := range ops {
		if _, err := runTestCloud(t, s, op); err != nil {
			t.Fatal(err)
		}
	}
	if remote.calls != 0 {
		t.Fatal("disconnected operation sent")
	}
}
func TestCloudDisabledConnectionDoesNotQueueAndTokenBlankRetains(t *testing.T) {
	s, _ := setupCloudService(t)
	c := saveTestCloud(t, s, "u", "eudic")
	updated, err := s.SaveCloudConnection(context.Background(), "u", "eudic", CloudConnectionInput{Enabled: false, TargetID: "book"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != c.ID || updated.TokenCipher != c.TokenCipher {
		t.Fatal("blank replaced credential")
	}
	got, err := s.AddWord(context.Background(), "u", AddWordRequest{Provider: "local", Term: "word", Language: "en"})
	if err != nil || got.CloudQueued {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err = s.BackfillCloud(context.Background(), "u", "eudic"); err == nil {
		t.Fatal("disabled backfill")
	}
}
func TestCloudOutboxFailureRollsBackLocalSave(t *testing.T) {
	s, _ := setupCloudService(t)
	saveTestCloud(t, s, "u", "eudic")
	if err := s.db.Migrator().DropTable(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWord(context.Background(), "u", AddWordRequest{Provider: "local", Term: "word", Language: "en"}); err == nil {
		t.Fatal("expected queue storage failure")
	}
	var count int64
	s.db.Model(&Word{}).Count(&count)
	if count != 0 {
		t.Fatal("partial word without durable operation")
	}
}
func TestCloudDeletedWordIsNeverSent(t *testing.T) {
	s, remote := setupCloudService(t)
	saveTestCloud(t, s, "u", "eudic")
	got, err := s.AddWord(context.Background(), "u", AddWordRequest{Provider: "local", Term: "word", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	s.db.Delete(&Word{}, "id = ?", got.Word.ID)
	ops, _ := s.CloudOperations(context.Background(), "u")
	if _, err := runTestCloud(t, s, ops[0]); err != nil {
		t.Fatal(err)
	}
	if remote.calls != 0 {
		t.Fatal("deleted word sent")
	}
}

func TestCloudRetryFencesPreviousRunningExecution(t *testing.T) {
	s, remote := setupCloudService(t)
	saveTestCloud(t, s, "u", "eudic")
	_, err := s.AddWord(context.Background(), "u", AddWordRequest{Provider: "local", Term: "word", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	ops, _ := s.CloudOperations(context.Background(), "u")
	old := ops[0]
	// Handler has terminalized its operation, runner hasn't finalized old job yet.
	s.db.Model(&orm.AsyncJob{}).Where("id = ?", old.JobID).Update("status", "running")
	s.db.Model(&CloudOperation{}).Where("id = ?", old.ID).Update("status", "failed")
	if n, err := s.BackfillCloud(context.Background(), "u", "eudic"); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	var current CloudOperation
	s.db.First(&current, "id = ?", old.ID)
	if current.JobID == old.JobID {
		t.Fatal("retry reused previous execution")
	}
	if err := s.finishCloudOperation(context.Background(), old, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	s.db.First(&current, "id = ?", old.ID)
	if current.Status != "pending" {
		t.Fatal("old attempt overwrote retry")
	}
	if _, err := runTestCloud(t, s, old); err != nil {
		t.Fatal(err)
	}
	if remote.calls != 0 {
		t.Fatal("old execution sent")
	}
	if _, err := runTestCloud(t, s, current); err != nil {
		t.Fatal(err)
	}
	if remote.calls != 1 {
		t.Fatal("new execution did not send")
	}
}
func TestCloudRateLimitDefersDurably(t *testing.T) {
	s, remote := setupCloudService(t)
	saveTestCloud(t, s, "u", "eudic")
	_, err := s.AddWord(context.Background(), "u", AddWordRequest{Provider: "local", Term: "word", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	ops, _ := s.CloudOperations(context.Background(), "u")
	remote.fail = &CloudClientError{Code: "rate_limited", Retryable: true, RetryAfter: time.Hour}
	result, err := runTestCloud(t, s, ops[0])
	if err == nil || !result.DeferAttempt || time.Until(result.RetryAt) < 59*time.Minute {
		t.Fatalf("%+v %v", result, err)
	}
	var op CloudOperation
	s.db.First(&op, "id = ?", ops[0].ID)
	if op.Status != "pending" {
		t.Fatal("cooldown failed operation")
	}
}
func TestCloudSharedRemoteAccountUsesSameResource(t *testing.T) {
	s, _ := setupCloudService(t)
	saveTestCloud(t, s, "u", "maimemo")
	saveTestCloud(t, s, "other", "maimemo")
	for _, owner := range []string{"u", "other"} {
		if _, err := s.AddWord(context.Background(), owner, AddWordRequest{Provider: "local", Term: "word", Language: "en"}); err != nil {
			t.Fatal(err)
		}
	}
	var jobs []orm.AsyncJob
	s.db.Find(&jobs)
	if len(jobs) != 2 || jobs[0].ResourceID == "" || jobs[0].ResourceID != jobs[1].ResourceID {
		t.Fatalf("resources %+v", jobs)
	}
	if strings.Contains(jobs[0].ResourceID, "secret-token") {
		t.Fatal("secret in job")
	}
}

func TestCloudExpiredJobCanBeRetriedAfterCrash(t *testing.T) {
	s, _ := setupCloudService(t)
	saveTestCloud(t, s, "u", "eudic")
	if _, err := s.AddWord(context.Background(), "u", AddWordRequest{Provider: "local", Term: "word", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	ops, _ := s.CloudOperations(context.Background(), "u")
	old := ops[0]
	// Recovery exhausts a stale job after process death before the handler records its result.
	s.db.Model(&orm.AsyncJob{}).Where("id = ?", old.JobID).Update("status", "failed")
	rows, err := s.CloudConnections(context.Background(), "u")
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Failed != 1 || rows[0].Pending != 0 {
		t.Fatalf("unreconciled crash: %+v", rows[0])
	}
	if n, err := s.BackfillCloud(context.Background(), "u", "eudic"); err != nil || n != 1 {
		t.Fatalf("retry=%d %v", n, err)
	}
}
