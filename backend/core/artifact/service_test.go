package artifact

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func v2TestDB(t *testing.T) *orm.DB {
	t.Helper()
	db := orm.MigrateTestDB(t,
		&orm.ArtifactV2{}, &orm.ArtifactBlob{}, &orm.ArtifactRevision{},
		&orm.ArtifactHead{}, &orm.ArtifactBinding{}, &orm.ArtifactDependency{},
		&orm.ArtifactIdempotency{}, &orm.ArtifactEventOutbox{},
	)
	var triggerErr error
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TEST_DB_DRIVER")), orm.DriverPostgres) {
		triggerErr = db.Exec(`CREATE OR REPLACE FUNCTION artifact_revisions_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'artifact revision payload is immutable';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS artifact_revisions_no_update ON artifact_revisions;
CREATE TRIGGER artifact_revisions_no_update
BEFORE UPDATE ON artifact_revisions
FOR EACH ROW EXECUTE PROCEDURE artifact_revisions_immutable();`).Error
	} else {
		triggerErr = db.Exec(`CREATE TRIGGER IF NOT EXISTS artifact_revisions_no_update
BEFORE UPDATE ON artifact_revisions
BEGIN
  SELECT RAISE(ABORT, 'artifact revision payload is immutable');
END;`).Error
	}
	if triggerErr != nil {
		t.Fatalf("create immutable revision trigger: %v", triggerErr)
	}
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_artifacts_owner_logical_key
ON artifacts (tenant_id, owner_user_id, logical_key)
WHERE deleted_at IS NULL AND logical_key IS NOT NULL AND logical_key != ''`).Error
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	return db
}

func TestCommitRevisionCreatesPublishedHeadAndIsIdempotent(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	req := CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "report", Title: "report.md",
		IdempotencyKey: "run/1", InlineJSON: []byte(`{"text":"v1"}`), ContentType: "text",
		Channel:  ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "legacy-1", Role: RoleOutput}},
	}
	first, err := svc.CommitRevision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionNo != 1 {
		t.Fatalf("revision_no=%d", first.RevisionNo)
	}
	again, err := svc.CommitRevision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if again.RevisionID != first.RevisionID {
		t.Fatal("idempotent retry created a new revision")
	}
	req.InlineJSON = []byte(`{"text":"v2"}`)
	req.IdempotencyKey = "run/2"
	req.BaseRevisionID = first.RevisionID
	req.ExpectedHeadVer = first.HeadVersion
	second, err := svc.CommitRevision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if second.RevisionNo != 2 {
		t.Fatalf("revision_no=%d", second.RevisionNo)
	}
	revs, _, err := svc.ListRevisions(context.Background(), "u1", first.ArtifactID)
	if err != nil || len(revs) != 2 {
		t.Fatalf("revisions=%d err=%v", len(revs), err)
	}
	if !bytes.Contains(revs[0].InlineJSON, []byte("v1")) {
		t.Fatal("first revision payload changed")
	}
}

func TestCommitRevisionConcurrentSameIdempotencyKey(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	req := CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "notes", Title: "notes.txt",
		IdempotencyKey: "same-concurrent", InlineJSON: []byte(`{"text":"a"}`), ContentType: "text",
	}
	var (
		wg    sync.WaitGroup
		views [2]*RevisionView
		errs  [2]error
	)
	start := make(chan struct{})
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			views[i], errs[i] = svc.CommitRevision(context.Background(), req)
		}()
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errs=%v %v", errs[0], errs[1])
	}
	if views[0] == nil || views[1] == nil || views[0].RevisionID != views[1].RevisionID {
		t.Fatalf("views=%#v %#v", views[0], views[1])
	}
}

func TestCommitRevisionRejectsIdempotencyConflict(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	req := CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "notes", Title: "notes.txt",
		IdempotencyKey: "same", InlineJSON: []byte(`{"text":"a"}`), ContentType: "text",
	}
	if _, err := svc.CommitRevision(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.InlineJSON = []byte(`{"text":"b"}`)
	if _, err := svc.CommitRevision(context.Background(), req); err != ErrIdempotencyConflict {
		t.Fatalf("err=%v", err)
	}
}

func TestCommitRevisionCASConflict(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	base := CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "cas", Title: "cas.txt",
		InlineJSON: []byte(`{"text":"base"}`), ContentType: "text",
	}
	first, err := svc.CommitRevision(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := svc.CommitRevision(context.Background(), CommitRequest{
				TenantID: "t1", OwnerUserID: "u1", ArtifactID: first.ArtifactID,
				LogicalKey: "cas", Title: "cas.txt",
				BaseRevisionID: first.RevisionID, ExpectedHeadVer: first.HeadVersion,
				InlineJSON: []byte(`{"text":"` + string(rune('a'+n)) + `"}`), ContentType: "text",
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	conflicts := 0
	ok := 0
	for err := range errs {
		if err == nil {
			ok++
			continue
		}
		if err == ErrRevisionConflict {
			conflicts++
			continue
		}
		t.Fatalf("unexpected err %v", err)
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("ok=%d conflicts=%d", ok, conflicts)
	}
}

func TestRevisionPayloadIsImmutable(t *testing.T) {
	db := v2TestDB(t)
	svc := New(db.DB)
	view, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "lock", Title: "lock.txt",
		InlineJSON: []byte(`{"text":"keep"}`), ContentType: "text",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := db.Model(&orm.ArtifactRevision{}).Where("id = ?", view.RevisionID).Update("content_hash", "tampered")
	if res.Error == nil {
		t.Fatal("expected immutable revision update to fail")
	}
}

func TestMoveHeadRestoresPublishedRevision(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "restore", Title: "restore.txt",
		InlineJSON: []byte(`{"text":"old"}`), ContentType: "text",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", ArtifactID: first.ArtifactID, LogicalKey: "restore",
		Title: "restore.txt", BaseRevisionID: first.RevisionID, ExpectedHeadVer: first.HeadVersion,
		InlineJSON: []byte(`{"text":"new"}`), ContentType: "text",
	})
	if err != nil {
		t.Fatal(err)
	}
	head, err := svc.MoveHead(context.Background(), "u1", first.ArtifactID, ChannelPublished, first.RevisionID, second.HeadVersion)
	if err != nil {
		t.Fatal(err)
	}
	if head.RevisionID != first.RevisionID {
		t.Fatal("published head did not move")
	}
	revs, _, _ := svc.ListRevisions(context.Background(), "u1", first.ArtifactID)
	if !bytes.Contains(revs[1].InlineJSON, []byte("new")) {
		t.Fatal("later revision payload changed during restore")
	}
}

func TestGetRevisionDeniesOtherOwner(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	view, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "acl", Title: "acl.txt",
		InlineJSON: []byte(`{"text":"secret"}`), ContentType: "text",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetRevision(context.Background(), "other", view.RevisionID); err != ErrAccessDenied {
		t.Fatalf("err=%v", err)
	}
}

func TestSignRevisionURLNeverReturnsRawStoragePath(t *testing.T) {
	db := v2TestDB(t)
	svc := New(db.DB)
	view, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "signed-url", Title: "report.bin",
		Content: []byte("private bytes"), ContentType: "file",
	})
	if err != nil {
		t.Fatal(err)
	}
	var revision orm.ArtifactRevision
	if err := db.Where("id = ?", view.RevisionID).Take(&revision).Error; err != nil {
		t.Fatal(err)
	}
	const unsupportedStoragePath = "/private/unpublished/report.bin"
	if err := db.Model(&orm.ArtifactBlob{}).Where("id = ?", revision.BlobID).Update("storage_key", unsupportedStoragePath).Error; err != nil {
		t.Fatal(err)
	}
	url, _, err := SignRevisionURL(context.Background(), svc, "u1", view.RevisionID)
	if err != ErrNotFound {
		t.Fatalf("err=%v, want %v", err, ErrNotFound)
	}
	if url == unsupportedStoragePath {
		t.Fatal("raw storage path was exposed")
	}
}

func TestPutBlobRejectsHashMismatchAndSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	_, err := PutBlob("t1", "text/plain", strings.NewReader("abc"), "sha256:deadbeef", 3)
	if err != ErrBlobHashMismatch {
		t.Fatalf("err=%v", err)
	}
	ref, err := PutBlob("t1", "text/plain", strings.NewReader("hello"), "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBlob(ref); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBlob(BlobRef{TenantID: "t1", SHA256: "00", StorageKey: outside}); err != ErrAccessDenied {
		t.Fatalf("err=%v", err)
	}
}

func TestBlobsAreTenantScoped(t *testing.T) {
	svc := New(v2TestDB(t).DB)
	a, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "tenant-a", OwnerUserID: "a", LogicalKey: "shared-name", Title: "x.bin",
		Content: []byte("same-bytes"), ContentType: "file",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "tenant-b", OwnerUserID: "b", LogicalKey: "shared-name", Title: "x.bin",
		Content: []byte("same-bytes"), ContentType: "file",
	})
	if err != nil {
		t.Fatal(err)
	}
	left, leftArt, _ := svc.GetRevision(context.Background(), "a", a.RevisionID)
	right, rightArt, _ := svc.GetRevision(context.Background(), "b", b.RevisionID)
	if left.BlobID == right.BlobID {
		t.Fatal("blob primary key leaked across tenants")
	}
	if leftArt.TenantID == rightArt.TenantID {
		t.Fatal("tenants merged")
	}
}

func TestRestorePublishedMovesBothHeadsAndLegacyValue(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := v2TestDB(t)
	if err := db.AutoMigrate(&orm.ConversationArtifact{}); err != nil {
		t.Fatal(err)
	}
	svc := New(db.DB)
	legacy := orm.ConversationArtifact{
		ID: "legacy-1", ConversationID: "c1", HistoryID: "h1", Filename: "notes.txt",
		Slot: "notes.txt", ContentType: "text", Value: json.RawMessage(`{"text":"v2"}`), CreateUserID: "u1",
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:c1:notes", Title: "notes.txt",
		InlineJSON: []byte(`{"text":"v1"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "legacy-1", Role: RoleOutput}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", ArtifactID: first.ArtifactID, LogicalKey: "conv:c1:notes",
		Title: "notes.txt", InlineJSON: []byte(`{"text":"v2"}`), ContentType: "text",
		Channel: ChannelPublished, BaseRevisionID: first.RevisionID, ExpectedHeadVer: first.HeadVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestorePublished(context.Background(), "u1", first.ArtifactID, first.RevisionID, 0); err != ErrRevisionConflict {
		t.Fatalf("restore without CAS version = %v", err)
	}
	if _, err := svc.RestorePublished(context.Background(), "u1", first.ArtifactID, first.RevisionID, second.HeadVersion); err != nil {
		t.Fatal(err)
	}
	published, err := svc.Head(context.Background(), first.ArtifactID, ChannelPublished)
	if err != nil || published.RevisionID != first.RevisionID {
		t.Fatalf("published head = %#v err=%v", published, err)
	}
	current, err := svc.Head(context.Background(), first.ArtifactID, ChannelCurrent)
	if err != nil || current.RevisionID != first.RevisionID {
		t.Fatalf("current head = %#v err=%v", current, err)
	}
	var stored orm.ConversationArtifact
	if err := db.First(&stored, "id = ?", "legacy-1").Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored.Value), "v2") {
		t.Fatalf("restore must not rewrite legacy source-of-truth bytes: %s", stored.Value)
	}
	proj := EnrichLegacyDTO(context.Background(), svc, "u1", "legacy-1")
	if !strings.Contains(string(proj.InlineJSON), "v1") {
		t.Fatalf("projection should overlay restored revision, got %#v", proj)
	}
}

func TestEnrichPinsForkBindingInsteadOfPublishedHead(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:src:notes", Title: "notes.txt",
		InlineJSON: []byte(`{"text":"v1"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "src-row", Role: RoleOutput, FollowHead: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", ArtifactID: first.ArtifactID, LogicalKey: "conv:src:notes",
		Title: "notes.txt", InlineJSON: []byte(`{"text":"v2"}`), ContentType: "text",
		Channel: ChannelPublished, BaseRevisionID: first.RevisionID, ExpectedHeadVer: first.HeadVersion,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "src-row", Role: RoleOutput, FollowHead: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindRevision(context.Background(), "u1", BindingSpec{
		ScopeType: ScopeLegacyRow, ScopeID: "child-row", Role: RoleOutput,
		RevisionID: first.RevisionID, FollowHead: false,
	}, first.ArtifactID); err != nil {
		t.Fatal(err)
	}
	src := EnrichLegacyDTO(context.Background(), svc, "u1", "src-row")
	if src.RevisionID == first.RevisionID || !strings.Contains(string(src.InlineJSON), "v2") {
		t.Fatalf("source projection should follow published head, got %#v", src)
	}
	child := EnrichLegacyDTO(context.Background(), svc, "u1", "child-row")
	if child.RevisionID != first.RevisionID || !strings.Contains(string(child.InlineJSON), "v1") {
		t.Fatalf("fork projection followed source head: %#v", child)
	}
}

func TestBindForkConversationCreatesIndependentArtifact(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:src:notes", Title: "notes.txt",
		InlineJSON: []byte(`{"text":"v1"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "src-row", Role: RoleOutput, FollowHead: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := BindForkConversation(context.Background(), svc, "u1", "src-row", "child-conv", "child-row"); err != nil {
		t.Fatal(err)
	}
	child := EnrichLegacyDTO(context.Background(), svc, "u1", "child-row")
	if child.V2ArtifactID == "" || child.V2ArtifactID == first.ArtifactID {
		t.Fatalf("fork child reused source artifact: %#v", child)
	}
	if _, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", ArtifactID: first.ArtifactID, LogicalKey: "conv:src:notes",
		Title: "notes.txt", InlineJSON: []byte(`{"text":"v2"}`), ContentType: "text",
		Channel: ChannelPublished, BaseRevisionID: first.RevisionID, ExpectedHeadVer: first.HeadVersion,
	}); err != nil {
		t.Fatal(err)
	}
	childAfter := EnrichLegacyDTO(context.Background(), svc, "u1", "child-row")
	if !strings.Contains(string(childAfter.InlineJSON), "v1") {
		t.Fatalf("child drifted with source: %#v", childAfter)
	}
}

func TestBindForkConversationSkipsFileListZip(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	if _, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "subagent/task/list", Title: "outputs.zip",
		InlineJSON: []byte(`{"paths":["a.txt","b.txt"]}`), ContentType: "file_list", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeSubAgentLegacyRow, ScopeID: "list-row", Role: RoleOutput, FollowHead: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := BindForkConversation(context.Background(), svc, "u1", "list-row", "child-conv", "child-a"); err != nil {
		t.Fatal(err)
	}
	child := EnrichLegacyDTO(context.Background(), svc, "u1", "child-a")
	if child.V2ArtifactID != "" {
		t.Fatalf("file_list fork bound zip onto child file: %#v", child)
	}
}

func TestBindForkConversationPinsSourceRevisionNotPublishedHead(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:src:report", Title: "report.txt",
		InlineJSON: []byte(`{"text":"v1"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "h1-row", Role: RoleOutput, FollowHead: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", ArtifactID: first.ArtifactID, LogicalKey: "conv:src:report",
		Title: "report.txt", InlineJSON: []byte(`{"text":"v2"}`), ContentType: "text",
		Channel: ChannelPublished, BaseRevisionID: first.RevisionID, ExpectedHeadVer: first.HeadVersion,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "h2-row", Role: RoleOutput, FollowHead: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := BindForkConversation(context.Background(), svc, "u1", "h1-row", "child-conv", "child-row"); err != nil {
		t.Fatal(err)
	}
	child := EnrichLegacyDTO(context.Background(), svc, "u1", "child-row")
	if !strings.Contains(string(child.InlineJSON), "v1") {
		t.Fatalf("historical fork copied published head: %#v", child)
	}
}

func TestBindForkConversationUsesLatestSourceBinding(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:src:notes", Title: "notes.txt",
		InlineJSON: []byte(`{"text":"v1"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "src-row", Role: RoleOutput, FollowHead: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", ArtifactID: first.ArtifactID, LogicalKey: "conv:src:notes",
		Title: "notes.txt", InlineJSON: []byte(`{"text":"v2"}`), ContentType: "text",
		Channel: ChannelPublished, BaseRevisionID: first.RevisionID, ExpectedHeadVer: first.HeadVersion,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "src-row", Role: RoleOutput, FollowHead: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := BindForkConversation(context.Background(), svc, "u1", "src-row", "child-conv", "child-row"); err != nil {
		t.Fatal(err)
	}
	child := EnrichLegacyDTO(context.Background(), svc, "u1", "child-row")
	if !strings.Contains(string(child.InlineJSON), "v2") {
		t.Fatalf("in-place update fork should copy latest source binding: %#v", child)
	}
}

func TestBindForkConversationPinsChildLegacyRowWhenChildAdvancesHead(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	if _, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:src:notes", Title: "notes.txt",
		InlineJSON: []byte(`{"text":"v1"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeLegacyRow, ScopeID: "src-row", Role: RoleOutput, FollowHead: false}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := BindForkConversation(context.Background(), svc, "u1", "src-row", "child-conv", "child-row"); err != nil {
		t.Fatal(err)
	}
	child := EnrichLegacyDTO(context.Background(), svc, "u1", "child-row")
	if _, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", ArtifactID: child.V2ArtifactID, LogicalKey: "conv:child-conv:notes",
		Title: "notes.txt", InlineJSON: []byte(`{"text":"v2"}`), ContentType: "text",
		Channel: ChannelPublished, BaseRevisionID: child.RevisionID, ExpectedHeadVer: child.HeadVersion,
		Bindings: []BindingSpec{{ScopeType: ScopeConversation, ScopeID: "child-conv", Role: RoleOutput, FollowHead: true}},
	}); err != nil {
		t.Fatal(err)
	}
	childAfter := EnrichLegacyDTO(context.Background(), svc, "u1", "child-row")
	if !strings.Contains(string(childAfter.InlineJSON), "v1") {
		t.Fatalf("forked historical row followed later child head: %#v", childAfter)
	}
}

func TestPurgeConversationOwnedHidesOwnerDownloads(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:c1:notes", Title: "notes.txt",
		InlineJSON: []byte(`{"text":"secret"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeConversation, ScopeID: "c1", Role: RoleOutput, FollowHead: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := PurgeConversationOwned(svc.DB, "u1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetRevision(context.Background(), "u1", first.RevisionID); err != ErrNotFound {
		t.Fatalf("purged revision still readable: %v", err)
	}
	if _, _, err := svc.ListRevisions(context.Background(), "u1", first.ArtifactID); err != ErrNotFound {
		t.Fatalf("purged artifact still listed: %v", err)
	}
	other, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:c2:notes", Title: "notes.txt",
		InlineJSON: []byte(`{"text":"keep"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeConversation, ScopeID: "c2", Role: RoleOutput, FollowHead: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetRevision(context.Background(), "u1", other.RevisionID); err != nil {
		t.Fatalf("unrelated conversation revision hidden: %v", err)
	}
}

func TestPurgeConversationOwnedRunsWhenV2FlagIsOff(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:c1:notes", Title: "notes.txt",
		InlineJSON: []byte(`{"text":"secret"}`), ContentType: "text", Channel: ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeConversation, ScopeID: "c1", Role: RoleOutput, FollowHead: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "")
	if err := PurgeConversationOwned(svc.DB, "u1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	if _, _, err := svc.GetRevision(context.Background(), "u1", first.RevisionID); err != ErrNotFound {
		t.Fatalf("flag-off purge left revision readable: %v", err)
	}
}

func TestPurgeConversationOwnedStopsStaticFileResign(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	t.Setenv("LAZYMIND_FILE_URL_SIGN_SECRET", "doc-test-secret")
	svc := New(v2TestDB(t).DB)
	first, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", LogicalKey: "conv:c1:notes", Title: "notes.pdf",
		Content: []byte("pdf-bytes"), ContentType: "file", MIMEType: "application/pdf",
		Channel:  ChannelPublished,
		Bindings: []BindingSpec{{ScopeType: ScopeConversation, ScopeID: "c1", Role: RoleOutput, FollowHead: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	url, _, err := SignRevisionURL(context.Background(), svc, "u1", first.RevisionID)
	if err != nil || url == "" {
		t.Fatalf("sign before purge: url=%q err=%v", url, err)
	}
	store.Init(svc.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	sign := func() map[string]string {
		body, _ := json.Marshal(map[string][]string{"paths": {url}})
		req := httptest.NewRequest(http.MethodPost, "/static-files:sign", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", "u1")
		rec := httptest.NewRecorder()
		doc.SignStaticFiles(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("sign status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			URLs map[string]string `json:"urls"`
			Data struct {
				URLs map[string]string `json:"urls"`
			} `json:"data"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
			t.Fatalf("decode sign body=%s", rec.Body.String())
		}
		if resp.Data.URLs != nil {
			return resp.Data.URLs
		}
		return resp.URLs
	}
	if sign()[url] == "" {
		t.Fatalf("live blob was not signed")
	}
	if err := PurgeConversationOwned(svc.DB, "u1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if got := sign(); got[url] != "" {
		t.Fatalf("purged blob was re-signed: %#v", got)
	}
}

func TestDualWriteMainChatSkipsUnreadableFile(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	DualWriteMainChat(context.Background(), svc, "c1", "h1", "u1", MainChatWrite{LogicalKey: "notes"}, orm.ConversationArtifact{
		ID: "file-1", Filename: "notes.bin", ContentType: "file",
		Value: json.RawMessage(`{"path":"/no/such/lazymind-file.bin","filename":"notes.bin"}`),
	})
	proj := EnrichLegacyDTO(context.Background(), svc, "u1", "file-1")
	if proj.V2ArtifactID != "" {
		t.Fatalf("unreadable file dual-write should skip, got %#v", proj)
	}
}

func TestDualWriteMainChatDropsBindingOnIdempotencyConflict(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	svc := New(v2TestDB(t).DB)
	row := orm.ConversationArtifact{
		ID: "file-1", Filename: "notes.txt", ContentType: "text",
		Value: json.RawMessage(`{"text":"v1"}`),
	}
	if err := DualWriteMainChat(context.Background(), svc, "c1", "h1", "u1", MainChatWrite{LogicalKey: "notes", IdempotencyKey: "same"}, row); err != nil {
		t.Fatal(err)
	}
	row.Value = json.RawMessage(`{"text":"v2"}`)
	if err := DualWriteMainChat(context.Background(), svc, "c1", "h1", "u1", MainChatWrite{LogicalKey: "notes", IdempotencyKey: "same"}, row); err != ErrIdempotencyConflict {
		t.Fatalf("err=%v", err)
	}
	proj := EnrichLegacyDTO(context.Background(), svc, "u1", "file-1")
	if proj.V2ArtifactID != "" {
		t.Fatalf("conflict should drop stale overlay, got %#v", proj)
	}
}

func TestCommitRevisionStoresEmptyFileAsBlob(t *testing.T) {
	db := v2TestDB(t)
	svc := New(db.DB)
	view, err := svc.CommitRevision(context.Background(), CommitRequest{
		TenantID: "t1", OwnerUserID: "u1", LogicalKey: "empty-file", Title: "empty.bin",
		Content: []byte{}, ContentType: "file", Channel: ChannelPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	var rev orm.ArtifactRevision
	if err := db.Where("id = ?", view.RevisionID).Take(&rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.BlobID == "" || rev.Size != 0 || len(rev.InlineJSON) != 0 {
		t.Fatalf("empty file should be a zero-byte blob, got %#v", rev)
	}
	if rev.ContentHash != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("content hash=%s", rev.ContentHash)
	}
}

func TestDualWriteMainChatStoresEmptyFileAsBlob(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := v2TestDB(t)
	svc := New(db.DB)
	path := filepath.Join(t.TempDir(), "empty.bin")
	if err := os.WriteFile(path, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{"path": path, "filename": "empty.bin"})
	DualWriteMainChat(context.Background(), svc, "c1", "h1", "u1", MainChatWrite{LogicalKey: "empty"}, orm.ConversationArtifact{
		ID: "file-empty", Filename: "empty.bin", ContentType: "file", Value: value,
	})
	proj := EnrichLegacyDTO(context.Background(), svc, "u1", "file-empty")
	if proj.V2ArtifactID == "" {
		t.Fatal("empty file dual-write skipped")
	}
	var rev orm.ArtifactRevision
	if err := db.Where("id = ?", proj.RevisionID).Take(&rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.BlobID == "" || rev.Size != 0 || strings.TrimSpace(string(rev.InlineJSON)) == "{}" {
		t.Fatalf("empty file dual-write stored inline JSON: %#v", rev)
	}
}

func TestDualWriteMainChatSkipsOversizedFile(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	prev := maxShadowBlobBytes
	maxShadowBlobBytes = 4
	t.Cleanup(func() { maxShadowBlobBytes = prev })
	svc := New(v2TestDB(t).DB)
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{"path": path, "filename": "big.bin"})
	if err := DualWriteMainChat(context.Background(), svc, "c1", "h1", "u1", MainChatWrite{LogicalKey: "big"}, orm.ConversationArtifact{
		ID: "file-big", Filename: "big.bin", ContentType: "file", Value: value,
	}); err != ErrShadowTooLarge {
		t.Fatalf("err=%v", err)
	}
	proj := EnrichLegacyDTO(context.Background(), svc, "u1", "file-big")
	if proj.V2ArtifactID != "" {
		t.Fatalf("oversized shadow write should skip, got %#v", proj)
	}
}
