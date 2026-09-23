package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lazymind/core/artifact"
	"lazymind/core/common/orm"
	"lazymind/core/subagent"
)

func newArtifactTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, &orm.ConversationArtifact{})
}

// assertStoredArtifactValue compares the value read back from the database
// semantically. PostgreSQL normalizes jsonb formatting, so byte-level
// comparison against a compact literal is driver-dependent.
func assertStoredArtifactValue(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotV, wantV map[string]any
	if err := json.Unmarshal(got, &gotV); err != nil {
		t.Fatalf("decode stored value: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantV); err != nil {
		t.Fatalf("decode want value: %v", err)
	}
	if !reflect.DeepEqual(gotV, wantV) {
		t.Fatalf("stored value = %s, want %s", got, want)
	}
}

func TestPersistConversationArtifactBindsAuthoritativeTurn(t *testing.T) {
	db := newArtifactTestDB(t)
	event := &ArtifactCreatedEvent{
		ArtifactID:  "09f9027d-9338-4e38-9674-238acf7ae173",
		Filename:    "result.txt",
		ContentType: "text",
		Value:       json.RawMessage(`{"text":"hello"}`),
	}

	dto, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", event,
	)
	if err != nil {
		t.Fatalf("persist artifact: %v", err)
	}
	if dto.ConversationID != "conversation-1" || dto.HistoryID != "history-1" {
		t.Fatalf("artifact was not bound to the current turn: %#v", dto)
	}

	var stored orm.ConversationArtifact
	if err := db.First(&stored, "id = ?", event.ArtifactID).Error; err != nil {
		t.Fatalf("load stored artifact: %v", err)
	}
	if stored.CreateUserID != "user-1" || stored.Filename != "result.txt" {
		t.Fatalf("unexpected stored artifact: %#v", stored)
	}
}

func TestPersistConversationArtifactRejectsInvalidOrDuplicateInput(t *testing.T) {
	db := newArtifactTestDB(t)
	valid := &ArtifactCreatedEvent{
		ArtifactID:  "bd27e81e-3767-4fc2-a6b6-9270633ce646",
		Filename:    "result.json",
		ContentType: "json",
		Value:       json.RawMessage(`{"data":{"ok":true}}`),
	}
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", valid,
	); err != nil {
		t.Fatalf("persist valid artifact: %v", err)
	}
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", valid,
	); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}

	invalid := *valid
	invalid.ArtifactID = "84e68a57-766a-4b3a-bd2f-f8f1b4d52354"
	invalid.Filename = "../escape.txt"
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", &invalid,
	); err == nil {
		t.Fatal("expected unsafe filename to be rejected")
	}
}

func TestPersistConversationArtifactReplacesSameTurnArtifactWhenRequested(t *testing.T) {
	db := newArtifactTestDB(t)
	artifactID := "1bcd90de-5867-4d9a-ae4b-645a1e0a9bb2"
	first := &ArtifactCreatedEvent{
		ArtifactID: artifactID, Filename: "result.txt", ContentType: "text",
		Value: json.RawMessage(`{"text":"first"}`), ReplaceExisting: true,
	}
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", first,
	); err != nil {
		t.Fatalf("persist first replaceable artifact: %v", err)
	}
	second := &ArtifactCreatedEvent{
		ArtifactID: artifactID, Filename: "result.txt", ContentType: "text",
		Value: json.RawMessage(`{"text":"second"}`), ReplaceExisting: true,
	}
	dto, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", second,
	)
	if err != nil {
		t.Fatalf("replace artifact: %v", err)
	}
	if string(dto.Value) != `{"text":"second"}` {
		t.Fatalf("replacement response value = %s", dto.Value)
	}

	var count int64
	if err := db.Model(&orm.ConversationArtifact{}).Where("id = ?", artifactID).Count(&count).Error; err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if count != 1 {
		t.Fatalf("artifact count = %d, want 1", count)
	}
	var stored orm.ConversationArtifact
	if err := db.First(&stored, "id = ?", artifactID).Error; err != nil {
		t.Fatalf("load replaced artifact: %v", err)
	}
	assertStoredArtifactValue(t, stored.Value, `{"text":"second"}`)
}

func TestPersistConversationArtifactReplacesAcrossTurns(t *testing.T) {
	db := newArtifactTestDB(t)
	event := &ArtifactCreatedEvent{
		ArtifactID: "917b73ea-53fb-4ad2-ad19-5a0546cb062f",
		Filename:   "result.txt", ContentType: "text",
		Value: json.RawMessage(`{"text":"first"}`), ReplaceExisting: true,
	}
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", event,
	); err != nil {
		t.Fatalf("persist first artifact: %v", err)
	}
	event.Value = json.RawMessage(`{"text":"other turn"}`)
	dto, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-2", "user-1", event,
	)
	if err != nil {
		t.Fatalf("replace artifact from another turn: %v", err)
	}
	if dto.HistoryID != "history-1" {
		t.Fatalf("replacement history = %q, want history-1", dto.HistoryID)
	}

	var stored orm.ConversationArtifact
	if err := db.First(&stored, "id = ?", event.ArtifactID).Error; err != nil {
		t.Fatalf("load replaced artifact: %v", err)
	}
	if stored.HistoryID != "history-1" {
		t.Fatalf("stored replacement = history %q, value %s", stored.HistoryID, stored.Value)
	}
	assertStoredArtifactValue(t, stored.Value, `{"text":"other turn"}`)
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-2", "history-3", "user-1", event,
	); err == nil || !strings.Contains(err.Error(), "scope mismatch") {
		t.Fatalf("expected cross-conversation replacement error, got %v", err)
	}
}

func TestPersistConversationArtifactUsesCharacterLimitsForUnicode(t *testing.T) {
	db := newArtifactTestDB(t)
	caption := strings.Repeat("说明", 1000)
	event := &ArtifactCreatedEvent{
		ArtifactID:  "67cd1254-bb2a-4d14-ac70-4e6913c2b245",
		Filename:    strings.Repeat("文", 100) + ".txt",
		ContentType: "text",
		Value:       json.RawMessage(`{"text":"内容"}`),
		Caption:     &caption,
	}

	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", event,
	); err != nil {
		t.Fatalf("valid Unicode metadata should be accepted: %v", err)
	}
}

func TestPersistConversationFileArtifactValidatesSharedWorkspace(t *testing.T) {
	db := newArtifactTestDB(t)
	workspace := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", workspace)
	artifactID := "da41e7e1-c085-447b-af51-6f89490c393a"
	root := conversationArtifactFileRoot("user-1", "conversation-1", artifactID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create artifact directory: %v", err)
	}
	path := filepath.Join(root, "report.docx")
	if err := os.WriteFile(path, []byte("docx"), 0o644); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}
	value, _ := json.Marshal(map[string]any{
		"filename": "report.docx", "path": path, "size": 999,
	})
	event := &ArtifactCreatedEvent{
		ArtifactID: artifactID, Filename: "report.docx", ContentType: "file", Value: value,
	}

	dto, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", event,
	)
	if err != nil {
		t.Fatalf("persist file artifact: %v", err)
	}
	var responseValue map[string]any
	if err := json.Unmarshal(dto.Value, &responseValue); err != nil {
		t.Fatalf("decode response value: %v", err)
	}
	if responseValue["url"] == nil || responseValue["path"] != nil {
		t.Fatalf("response did not replace the storage path with a signed URL: %#v", responseValue)
	}
	var stored orm.ConversationArtifact
	if err := db.First(&stored, "id = ?", artifactID).Error; err != nil {
		t.Fatalf("load stored file artifact: %v", err)
	}
	var storedValue map[string]any
	if err := json.Unmarshal(stored.Value, &storedValue); err != nil {
		t.Fatalf("decode canonical value: %v", err)
	}
	expectedStoredPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve expected stored path: %v", err)
	}
	if storedValue["size"] != float64(4) || storedValue["path"] != expectedStoredPath {
		t.Fatalf("file metadata was not canonicalized: %#v", storedValue)
	}
}

func TestRemoveConversationArtifactFilesAlsoRemovesAgentWorkspace(t *testing.T) {
	publishedRoot := t.TempDir()
	agentRoot := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", publishedRoot)
	t.Setenv("LAZYMIND_AGENTIC_WORKSPACE", agentRoot)

	userID := "user-1"
	conversationID := "conversation-1"
	roots := []string{
		conversationArtifactConversationRoot(userID, conversationID),
		conversationAgentWorkspaceRoots(userID, conversationID)[0],
	}
	for _, root := range roots {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("create conversation workspace: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "marker"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write conversation workspace marker: %v", err)
		}
	}
	unrelated := filepath.Join(agentRoot, conversationArtifactFileDirectory, "unrelated")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatalf("create unrelated workspace: %v", err)
	}

	if err := removeConversationArtifactFiles(userID, conversationID); err != nil {
		t.Fatalf("remove conversation files: %v", err)
	}
	for _, root := range roots {
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("conversation workspace still exists: %s", root)
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated workspace was removed: %v", err)
	}
}

func TestArtifactScopeHashMatchesAlgorithmContract(t *testing.T) {
	got := artifactScopeHash("user-1")
	const want = "c6c289e49e9c05b2145860387b73bcb1"
	if got != want {
		t.Fatalf("artifact scope hash mismatch: got %q, want %q", got, want)
	}
	const legacyWant = "c6c289e49e9c05b2145860387b73bcb18df43fb09a1e4a4a9713c76c88bb541b"
	if legacy := legacyArtifactScopeHash("user-1"); legacy != legacyWant {
		t.Fatalf("legacy artifact scope hash mismatch: got %q, want %q", legacy, legacyWant)
	}
}

func TestCanonicalConversationFileValueAcceptsLegacyScopeHash(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", workspace)
	artifactID := "da41e7e1-c085-447b-af51-6f89490c393a"
	root := legacyConversationArtifactFileRoot("user-1", "conversation-1", artifactID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create legacy artifact directory: %v", err)
	}
	path := filepath.Join(root, "legacy.docx")
	if err := os.WriteFile(path, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy artifact: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{
		"filename": "legacy.docx", "path": path, "size": 999,
	})

	canonical, err := canonicalConversationFileValue(
		"user-1", "conversation-1", artifactID, "legacy.docx", raw,
	)
	if err != nil {
		t.Fatalf("canonicalize legacy artifact: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatalf("decode canonical legacy artifact: %v", err)
	}
	expectedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve expected legacy artifact path: %v", err)
	}
	if value["size"] != float64(len("legacy")) || value["path"] != expectedPath {
		t.Fatalf("unexpected canonical legacy artifact: %#v", value)
	}
}

func TestConversationArtifactResponseValueSignsLegacyScopeHash(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", workspace)
	t.Setenv("LAZYMIND_FILE_URL_SIGN_SECRET", "artifact-test-secret")
	artifactID := "da41e7e1-c085-447b-af51-6f89490c393a"
	root := legacyConversationArtifactFileRoot("user-1", "conversation-1", artifactID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create legacy artifact directory: %v", err)
	}
	path := filepath.Join(root, "legacy.docx")
	if err := os.WriteFile(path, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy artifact: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{
		"filename": "legacy.docx", "path": path, "size": len("legacy"),
	})
	artifact := orm.ConversationArtifact{
		ID: artifactID, Filename: "legacy.docx", ContentType: "file", Value: raw,
	}

	signed := conversationArtifactResponseValue("user-1", "conversation-1", artifact)
	var value map[string]any
	if err := json.Unmarshal(signed, &value); err != nil {
		t.Fatalf("decode signed legacy artifact: %v", err)
	}
	if _, exposed := value["path"]; exposed {
		t.Fatalf("legacy server path must not be exposed: %#v", value)
	}
	url, _ := value["url"].(string)
	wantPrefix := "/static-files/subagent/chat-artifacts/" +
		legacyArtifactScopeHash("user-1") + "/" +
		legacyArtifactScopeHash("conversation-1") + "/" + artifactID + "/legacy.docx?"
	if !strings.HasPrefix(url, wantPrefix) {
		t.Fatalf("legacy artifact URL = %q, want prefix %q", url, wantPrefix)
	}
}

func TestPersistConversationFileArtifactRejectsForeignPath(t *testing.T) {
	db := newArtifactTestDB(t)
	workspace := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", workspace)
	foreign := filepath.Join(workspace, "another-conversation", "report.pdf")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatalf("create foreign directory: %v", err)
	}
	if err := os.WriteFile(foreign, []byte("pdf"), 0o644); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}
	value, _ := json.Marshal(map[string]any{
		"filename": "report.pdf", "path": foreign, "size": 3,
	})
	event := &ArtifactCreatedEvent{
		ArtifactID:  "22bdb08b-8459-43cd-99d4-5364aa50842c",
		Filename:    "report.pdf",
		ContentType: "file",
		Value:       value,
	}

	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", event,
	); err == nil || !strings.Contains(err.Error(), "outside its conversation workspace") {
		t.Fatalf("expected foreign path rejection, got %v", err)
	}
}

func TestListConversationArtifactsReadsHistoryCreateTime(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ChatHistory{})
	now := time.Now().UTC().Truncate(time.Second)
	history := orm.ChatHistory{
		ID:             "history-1",
		Seq:            1,
		ConversationID: "conversation-1",
		Ext:            json.RawMessage(`{"input":[]}`),
		TimeMixin:      orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}
	var got []orm.ChatHistory
	if err := db.Select("id, conversation_id, ext, create_time").
		Where("conversation_id = ?", "conversation-1").
		Order("seq ASC, create_time ASC, id ASC").
		Find(&got).Error; err != nil {
		t.Fatalf("list histories with create_time: %v", err)
	}
	if len(got) != 1 || got[0].ID != history.ID {
		t.Fatalf("histories = %#v, want history-1", got)
	}
}

func TestConversationSubAgentArtifactsProjectOnlyPublishedV2Outputs(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := orm.MigrateTestDB(t, append(v2PersistModels(),
		&orm.SubAgentTask{}, &orm.SubAgentArtifact{},
	)...)
	ctx := context.Background()
	now := time.Now().UTC()
	ordinary := orm.SubAgentTask{
		ID: "task-published", ConversationID: "conversation-1", TriggerHistoryID: "history-1",
		AgentType: "research", Title: "Research", Mode: "auto", Status: subagent.StatusSucceeded,
		Params: json.RawMessage(`{}`), InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`),
		CreateUserID: "user-1", LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}
	workflow := ordinary
	workflow.ID, workflow.AgentType = "task-workflow", "workflow_step"
	running := ordinary
	running.ID, running.Status = "task-running", subagent.StatusRunning
	for _, task := range []orm.SubAgentTask{ordinary, workflow, running} {
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows := []orm.SubAgentArtifact{
		{ID: "row-published", TaskID: ordinary.ID, Slot: "report", ContentType: "text", Value: json.RawMessage(`{"text":"visible"}`), Seq: 1, CreatedAt: now},
		{ID: "row-hidden", TaskID: ordinary.ID, Slot: "hidden", ContentType: "text", Value: json.RawMessage(`{"text":"hidden"}`), Seq: 2, Hidden: true, CreatedAt: now},
		{ID: "row-workflow", TaskID: workflow.ID, Slot: "workflow", ContentType: "text", Value: json.RawMessage(`{"text":"workflow"}`), Seq: 1, CreatedAt: now},
		{ID: "row-running", TaskID: running.ID, Slot: "running", ContentType: "text", Value: json.RawMessage(`{"text":"running"}`), Seq: 1, CreatedAt: now},
	}
	for _, row := range rows {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := artifact.DualWriteSubAgent(ctx, artifact.New(db.DB), artifact.SubAgentSnapshot{
		TaskID: ordinary.ID, ConversationID: ordinary.ConversationID, TriggerHistoryID: ordinary.TriggerHistoryID,
		OwnerUserID: ordinary.CreateUserID, AgentType: ordinary.AgentType,
	}, artifact.SubAgentLegacyArtifact{ID: rows[0].ID, Slot: rows[0].Slot, ContentType: rows[0].ContentType, Value: rows[0].Value, Seq: rows[0].Seq}); err != nil {
		t.Fatal(err)
	}

	got := conversationSubAgentArtifacts(ctx, db.DB, "conversation-1", "user-1")
	if len(got) != 1 {
		t.Fatalf("projected artifacts = %#v, want one published ordinary SubAgent artifact", got)
	}
	if got[0].SourceType != "subagent" || got[0].ProducerID != ordinary.ID || got[0].V2ArtifactID == "" || got[0].RevisionCount != 1 {
		t.Fatalf("projected artifact = %#v", got[0])
	}
}

func TestConversationUserUploadArtifactsProjectsOnlyReadableFileInputs(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
	t.Setenv("LAZYMIND_FILE_URL_SIGN_SECRET", "artifact-test-secret")
	filePath := filepath.Join(uploadRoot, "tmp", "users", "user-1", "files", "upload-1", "brief.pdf")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("create upload directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("brief"), 0o644); err != nil {
		t.Fatalf("write upload: %v", err)
	}

	history := orm.ChatHistory{
		ID: "history-1",
		Ext: json.RawMessage(`{"input":[
			{"input_type":"text","text":"summarize this"},
			{"input_type":"file","uri":"` + filePath + `","filename":"brief.pdf"},
			{"input_type":"image","uri":"data:image/png;base64,abc"},
			{"input_type":"file","uri":"https://example.com/private.pdf"}
		]}`),
	}

	got := conversationUserUploadArtifacts("conversation-1", "user-1", []orm.ChatHistory{history})
	if len(got) != 1 {
		t.Fatalf("projected uploads = %#v, want one readable file", got)
	}
	if got[0].SourceType != "user_upload" || got[0].ProducerType != "user" || got[0].Filename != "brief.pdf" {
		t.Fatalf("unexpected upload projection: %#v", got[0])
	}
	if got[0].PublicationStatus != artifactPublicationInput {
		t.Fatalf("upload publication_status = %q, want %q", got[0].PublicationStatus, artifactPublicationInput)
	}
	var value map[string]any
	if err := json.Unmarshal(got[0].Value, &value); err != nil {
		t.Fatalf("decode upload response value: %v", err)
	}
	if _, exposed := value["path"]; exposed {
		t.Fatalf("upload projection exposed filesystem path: %#v", value)
	}
	url, _ := value["url"].(string)
	if !strings.HasPrefix(url, "/static-files/") {
		t.Fatalf("upload projection URL = %q, want signed static URL", url)
	}
}

func TestConversationUserUploadArtifactsRejectsForeignOwner(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
	t.Setenv("LAZYMIND_FILE_URL_SIGN_SECRET", "artifact-test-secret")
	filePath := filepath.Join(uploadRoot, "tmp", "users", "user-2", "files", "upload-2", "secret.pdf")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("create upload directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	history := orm.ChatHistory{
		ID:  "history-2",
		Ext: json.RawMessage(`{"input":[{"input_type":"file","uri":"` + filePath + `","filename":"secret.pdf"}]}`),
	}
	got := conversationUserUploadArtifacts("conversation-1", "user-1", []orm.ChatHistory{history})
	if len(got) != 0 {
		t.Fatalf("foreign upload was re-signed: %#v", got)
	}
}

func v2PersistModels() []any {
	return []any{
		&orm.ConversationArtifact{},
		&orm.ArtifactV2{}, &orm.ArtifactBlob{}, &orm.ArtifactRevision{},
		&orm.ArtifactHead{}, &orm.ArtifactBinding{}, &orm.ArtifactDependency{},
		&orm.ArtifactIdempotency{}, &orm.ArtifactEventOutbox{},
	}
}

func TestPersistConversationArtifactDualWritesLogicalKeyRevisions(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	db := orm.MigrateTestDB(t, v2PersistModels()...)
	_ = db.Exec(`CREATE TRIGGER IF NOT EXISTS artifact_revisions_no_update
BEFORE UPDATE ON artifact_revisions
BEGIN
  SELECT RAISE(ABORT, 'artifact revision payload is immutable');
END;`).Error
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_artifacts_owner_logical_key
ON artifacts (tenant_id, owner_user_id, logical_key)
WHERE deleted_at IS NULL AND logical_key IS NOT NULL AND logical_key != ''`).Error

	firstID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	first, err := persistConversationArtifact(context.Background(), db.DB, "c1", "h1", "u1", &ArtifactCreatedEvent{
		ArtifactID: firstID, Filename: "report.md", ContentType: "text",
		Value: json.RawMessage(`{"text":"one"}`), LogicalKey: "report", IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionCount != 1 || first.V2ArtifactID == "" {
		t.Fatalf("first dto = %#v", first)
	}
	replaced, err := persistConversationArtifact(context.Background(), db.DB, "c1", "h1", "u1", &ArtifactCreatedEvent{
		ArtifactID: firstID, Filename: "report.md", ContentType: "text",
		Value: json.RawMessage(`{"text":"two"}`), LogicalKey: "report", IdempotencyKey: "k2",
		ReplaceExisting: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.RevisionCount != 2 || replaced.Revision != 2 || replaced.HeadVersion == 0 {
		t.Fatalf("replaced dto = %#v", replaced)
	}
	sameName, err := persistConversationArtifact(context.Background(), db.DB, "c1", "h1", "u1", &ArtifactCreatedEvent{
		ArtifactID: otherID, Filename: "report.md", ContentType: "text",
		Value: json.RawMessage(`{"text":"other"}`), LogicalKey: "report-alt", IdempotencyKey: "k3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sameName.V2ArtifactID == first.V2ArtifactID {
		t.Fatal("different logical_key merged into one artifact")
	}
	var original orm.ArtifactRevision
	if err := db.Where("artifact_id = ? AND revision_no = 1", first.V2ArtifactID).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(original.InlineJSON), "one") {
		t.Fatalf("v1 payload = %s", original.InlineJSON)
	}
}

func TestPersistConversationArtifactRollsBackWhenV2Conflicts(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	db := orm.MigrateTestDB(t, v2PersistModels()...)
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_artifacts_owner_logical_key
ON artifacts (tenant_id, owner_user_id, logical_key)
WHERE deleted_at IS NULL AND logical_key IS NOT NULL AND logical_key != ''`).Error
	artifactID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if _, err := persistConversationArtifact(context.Background(), db.DB, "c1", "h1", "u1", &ArtifactCreatedEvent{
		ArtifactID: artifactID, Filename: "report.md", ContentType: "text",
		Value: json.RawMessage(`{"text":"one"}`), LogicalKey: "report", IdempotencyKey: "same-key",
	}); err != nil {
		t.Fatal(err)
	}
	replaced, err := persistConversationArtifact(context.Background(), db.DB, "c1", "h1", "u1", &ArtifactCreatedEvent{
		ArtifactID: artifactID, Filename: "report.md", ContentType: "text",
		Value: json.RawMessage(`{"text":"two"}`), LogicalKey: "report", IdempotencyKey: "same-key",
		ReplaceExisting: true,
	})
	if err != artifact.ErrIdempotencyConflict || replaced != nil {
		t.Fatalf("expected atomic rejection: dto=%#v err=%v", replaced, err)
	}
	var stored orm.ConversationArtifact
	if err := db.First(&stored, "id = ?", artifactID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored.Value), "one") {
		t.Fatalf("legacy changed despite rollback: %s", stored.Value)
	}
	listed := ConversationArtifactDTO{ArtifactID: artifactID, Value: stored.Value}
	enrichConversationArtifactDTO(context.Background(), db.DB, "u1", &listed)
	if listed.V2ArtifactID == "" || !strings.Contains(string(listed.Value), "one") {
		t.Fatalf("rollback lost the committed V2 binding: %#v", listed)
	}
}

func TestEnrichRestoredTextUsesProjectionContentType(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	db := orm.MigrateTestDB(t, v2PersistModels()...)
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_artifacts_owner_logical_key
ON artifacts (tenant_id, owner_user_id, logical_key)
WHERE deleted_at IS NULL AND logical_key IS NOT NULL AND logical_key != ''`).Error
	artifactID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	first, err := persistConversationArtifact(context.Background(), db.DB, "c1", "h1", "u1", &ArtifactCreatedEvent{
		ArtifactID: artifactID, Filename: "notes.txt", ContentType: "text",
		Value: json.RawMessage(`{"text":"v1"}`), LogicalKey: "notes", IdempotencyKey: "r1",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := artifact.New(db.DB)
	if _, err := svc.CommitRevision(context.Background(), artifact.CommitRequest{
		TenantID: "u1", OwnerUserID: "u1", ArtifactID: first.V2ArtifactID, LogicalKey: "conv:c1:notes",
		Title: "notes.bin", Content: []byte("binary-v2"), ContentType: "file", Channel: artifact.ChannelPublished,
		BaseRevisionID: first.RevisionID, ExpectedHeadVer: first.HeadVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestorePublished(context.Background(), "u1", first.V2ArtifactID, first.RevisionID, 2); err != nil {
		t.Fatal(err)
	}
	dto := ConversationArtifactDTO{
		ArtifactID: artifactID, ContentType: "file", Filename: "notes.bin", Name: "notes.bin",
		Value: json.RawMessage(`{"path":"/tmp/notes.bin","filename":"notes.bin"}`),
	}
	enrichConversationArtifactDTO(context.Background(), db.DB, "u1", &dto)
	if dto.ContentType != "text" || !strings.Contains(string(dto.Value), "v1") {
		t.Fatalf("restored projection mixed file metadata with text bytes: %#v", dto)
	}
}

func TestConversationSubAgentArtifactsFlagOffKeepsLegacyRows(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "")
	db := orm.MigrateTestDB(t, &orm.SubAgentTask{}, &orm.SubAgentArtifact{})
	now := time.Now().UTC()
	task := orm.SubAgentTask{
		ID: "task-1", ConversationID: "conversation-1", TriggerHistoryID: "history-1",
		AgentType: "research", Title: "Research", Mode: "auto", Status: subagent.StatusSucceeded,
		Params: json.RawMessage(`{}`), InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`),
		CreateUserID: "user-1", LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.SubAgentArtifact{
		ID: "row-1", TaskID: task.ID, Slot: "report", ContentType: "text",
		Value: json.RawMessage(`{"text":"visible"}`), Seq: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	got := conversationSubAgentArtifacts(context.Background(), db.DB, "conversation-1", "user-1")
	if len(got) != 1 || got[0].ArtifactID != "row-1" || got[0].V2ArtifactID != "" {
		t.Fatalf("flag-off projection = %#v", got)
	}
}

func TestConversationSubAgentArtifactsKeepUnmappedLegacyRows(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := orm.MigrateTestDB(t, append(v2PersistModels(),
		&orm.SubAgentTask{}, &orm.SubAgentArtifact{},
	)...)
	now := time.Now().UTC()
	task := orm.SubAgentTask{
		ID: "task-1", ConversationID: "conversation-1", TriggerHistoryID: "history-1",
		AgentType: "research", Title: "Research", Mode: "auto", Status: subagent.StatusSucceeded,
		Params: json.RawMessage(`{}`), InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`),
		CreateUserID: "user-1", LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.SubAgentArtifact{
		ID: "row-unmapped", TaskID: task.ID, Slot: "report", ContentType: "text",
		Value: json.RawMessage(`{"text":"legacy"}`), Seq: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	got := conversationSubAgentArtifacts(context.Background(), db.DB, "conversation-1", "user-1")
	if len(got) != 1 || got[0].ArtifactID != "row-unmapped" || got[0].V2ArtifactID != "" {
		t.Fatalf("unmapped dual-write row disappeared: %#v", got)
	}
}

func TestSameLogicalKeyDoesNotCrossConversations(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	db := orm.MigrateTestDB(t, v2PersistModels()...)
	_ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_artifacts_owner_logical_key
ON artifacts (tenant_id, owner_user_id, logical_key)
WHERE deleted_at IS NULL AND logical_key IS NOT NULL AND logical_key != ''`).Error
	left, err := persistConversationArtifact(context.Background(), db.DB, "c-left", "h1", "u1", &ArtifactCreatedEvent{
		ArtifactID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaa01", Filename: "report.md", ContentType: "text",
		Value: json.RawMessage(`{"text":"left"}`), LogicalKey: "report", IdempotencyKey: "left",
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := persistConversationArtifact(context.Background(), db.DB, "c-right", "h1", "u1", &ArtifactCreatedEvent{
		ArtifactID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaa02", Filename: "report.md", ContentType: "text",
		Value: json.RawMessage(`{"text":"right"}`), LogicalKey: "report", IdempotencyKey: "right",
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.V2ArtifactID == "" || left.V2ArtifactID == right.V2ArtifactID {
		t.Fatalf("conversations shared a V2 artifact: left=%#v right=%#v", left, right)
	}
	if left.LogicalKey != "report" || right.LogicalKey != "report" {
		t.Fatalf("display logical_key leaked scope prefix: left=%q right=%q", left.LogicalKey, right.LogicalKey)
	}
}
