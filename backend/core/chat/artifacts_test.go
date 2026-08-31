package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
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
