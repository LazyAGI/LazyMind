package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func TestForkSynchronousCapacityAndConcurrentCopies(t *testing.T) {
	db, c, items, _ := forkFixture(t, 1)
	template := items[0]
	for i := 2; i <= maxForkTurns; i++ {
		h := template
		h.ID = fmt.Sprintf("capacity-%04d", i)
		h.Seq = i
		h.Result = strings.Repeat("x", 5000)
		items = append(items, h)
	}
	if err := db.CreateInBatches(items[1:], 50).Error; err != nil {
		t.Fatal(err)
	}
	artifacts := make([]orm.ConversationArtifact, 200)
	for i := range artifacts {
		artifacts[i] = orm.ConversationArtifact{ID: fmt.Sprintf("capacity-artifact-%04d", i), ConversationID: c.ID, HistoryID: template.ID, Filename: "note.txt", Slot: "note", ContentType: "text", Value: json.RawMessage(`{"text":"formed output"}`), CreateUserID: "u1", CreatedAt: template.CreateTime}
	}
	if err := db.CreateInBatches(artifacts, 50).Error; err != nil {
		t.Fatal(err)
	}
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	started := time.Now()
	p, err := buildForkPreview(context.Background(), db, caller, c, items)
	if err != nil {
		t.Fatal(err)
	}
	request := forkCreateRequest{SourceHistoryID: items[len(items)-1].ID, ExpectedPrefixRevision: p.PrefixRevision}
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			result, err := createConversationFork(ctx, db, caller, c.ID, fmt.Sprintf("capacity-%d", i), request)
			if err == nil && result.InheritedTurnCount != maxForkTurns {
				err = fmt.Errorf("truncated to %d", result.InheritedTurnCount)
			}
			done <- err
		}(i)
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	var rows int64
	if err := db.Model(&orm.ChatHistory{}).Where("conversation_id <> ?", c.ID).Count(&rows).Error; err != nil || rows != 2*maxForkTurns {
		t.Fatalf("copies=%d err=%v", rows, err)
	}
	var bytes int
	for _, h := range items {
		bytes += len(h.RawContent) + len(h.Content) + len(h.Result) + len(h.Ext) + len(h.RetrievalResult)
	}
	t.Logf("dialect=%s turns=%d history_bytes=%d artifacts=%d concurrent_creates=2 preview_and_create_elapsed=%s", db.Dialector.Name(), len(items), bytes, len(artifacts), time.Since(started))
	extra := template
	extra.ID = "capacity-overflow"
	extra.Seq = maxForkTurns + 1
	if err := db.Create(&extra).Error; err != nil {
		t.Fatal(err)
	}
	_, _, err = loadForkPrefix(context.Background(), db, "u1", c.ID, extra.ID)
	requireForkCode(t, err, "FORK_TOO_LARGE")
	if err := db.Model(&items[0]).Update("result", strings.Repeat("x", maxForkBytes+1)).Error; err != nil {
		t.Fatal(err)
	}
	_, _, err = loadForkPrefix(context.Background(), db, "u1", c.ID, items[0].ID)
	requireForkCode(t, err, "FORK_TOO_LARGE")
}

func TestForkLifecycleDoesNotFollowSourceArchiveTrashOrRestore(t *testing.T) {
	db, c, _, request := forkFixture(t, 2)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	result, err := createConversationFork(context.Background(), db, caller, c.ID, "lifecycle", request)
	if err != nil {
		t.Fatal(err)
	}
	branchID := result.Conversation["conversation_id"].(string)
	call := func(id, action, body string, handler http.HandlerFunc) {
		t.Helper()
		w := httptest.NewRecorder()
		handler(w, sidechatRequest(http.MethodPost, "/conversations/"+id+":"+action, "u1", body, map[string]string{"name": id + ":" + action}))
		if w.Code != 200 {
			t.Fatalf("%s=%d %s", action, w.Code, w.Body.String())
		}
	}
	call(c.ID, "archive", `{"folder_id":"unfiled"}`, ArchiveConversation)
	var branch orm.Conversation
	db.Where("id = ?", branchID).Take(&branch)
	if branch.ArchivedAt != nil || branch.DeletedAt != nil {
		t.Fatal("branch followed source archive")
	}
	call(c.ID, "batchDelete", fmt.Sprintf(`{"conversation_ids":[%q]}`, c.ID), BatchDeleteConversations)
	db.Where("id = ?", branchID).Take(&branch)
	if branch.DeletedAt != nil {
		t.Fatal("branch followed source batch trash")
	}
	call(branchID, "batchDelete", fmt.Sprintf(`{"conversation_ids":[%q]}`, branchID), BatchDeleteConversations)
	call(c.ID, "restore", `{}`, RestoreConversation)
	db.Where("id = ?", branchID).Take(&branch)
	if branch.DeletedAt == nil {
		t.Fatal("branch followed source restore")
	}
	call(branchID, "restore", `{}`, RestoreConversation)
	db.Where("id = ?", branchID).Take(&branch)
	// Clear reused nullable fields before loading a restored row.
	branch = orm.Conversation{}
	db.Where("id = ?", branchID).Take(&branch)
	if branch.DeletedAt != nil || branch.ParentConversationID != nil {
		t.Fatal("branch cannot restore independently")
	}
	_, copied, err := loadForkPrefix(context.Background(), db, "u1", branchID, result.ForkOrigin.SourceHistoryID)
	if err == nil || len(copied) != 0 {
		t.Fatal("source IDs should not address branch histories")
	}
	var last orm.ChatHistory
	db.Where("conversation_id = ?", branchID).Order("seq DESC").Take(&last)
	var all []orm.ChatHistory
	db.Where("conversation_id = ?", branchID).Order("seq").Find(&all)
	revision, _ := forkPrefixRevision(all)
	nested, err := createConversationFork(context.Background(), db, caller, branchID, "fork-of-fork", forkCreateRequest{SourceHistoryID: last.ID, ExpectedPrefixRevision: revision})
	if err != nil {
		t.Fatal(err)
	}
	if nested.ForkOrigin.SourceConversationID != branchID {
		t.Fatal("fork-of-fork lost direct source")
	}
	if err := archiveConversation(context.Background(), db, branchID, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := purgeConversation(db, branchID, "u1"); err != nil {
		t.Fatal(err)
	}
	_, err = createConversationFork(context.Background(), db, caller, c.ID, "lifecycle", request)
	requireForkCode(t, err, "FORK_RESULT_UNAVAILABLE")
}

func TestForkArtifactLimitCountsOnlyOutputsInThePrefix(t *testing.T) {
	db, c, items, _ := forkFixture(t, 1)
	artifacts := make([]orm.ConversationArtifact, 202)
	for i := range artifacts {
		created := items[0].UpdateTime.Add(time.Hour)
		if i == 201 {
			created = items[0].CreateTime
		}
		artifacts[i] = orm.ConversationArtifact{ID: fmt.Sprintf("artifact-%04d", i), ConversationID: c.ID, HistoryID: items[0].ID, Filename: "note.txt", Slot: "note", ContentType: "text", Value: json.RawMessage(`{"text":"output"}`), CreateUserID: "u1", CreatedAt: created}
	}
	if err := db.CreateInBatches(artifacts, 50).Error; err != nil {
		t.Fatal(err)
	}
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	p, err := buildForkPreview(context.Background(), db, caller, c, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AttachmentsSummary) != 1 {
		t.Fatal("eligible artifact was truncated by later outputs")
	}
	if err := db.Model(&orm.ConversationArtifact{}).Where("conversation_id = ?", c.ID).Update("created_at", items[0].CreateTime).Error; err != nil {
		t.Fatal(err)
	}
	_, err = buildForkPreview(context.Background(), db, caller, c, items)
	requireForkCode(t, err, "FORK_TOO_LARGE")
}

func TestForkUnavailableModelRequiresExplicitReplacement(t *testing.T) {
	db, c, items, request := forkFixture(t, 1)
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-fork").Update("is_verified", false).Error; err != nil {
		t.Fatal(err)
	}
	seedAvailableChatModel(t, db, "u1", "replacement-provider", "replacement-group", "replacement-model", "Replacement", "Replacement", "B", "llm", true, "fake-key")
	p, err := buildForkPreview(context.Background(), db, caller, c, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ConfigIssues) != 1 || p.ConfigIssues[0].Field != "model" {
		t.Fatalf("missing model issue: %#v", p.ConfigIssues)
	}
	_, err = createConversationFork(context.Background(), db, caller, c.ID, "replacement", request)
	requireForkCode(t, err, "MODEL_UNAVAILABLE")
	request.ReplacementModel = &initialChatModelSelection{Mode: "fixed", ModelID: "replacement-model"}
	result, err := createConversationFork(context.Background(), db, caller, c.ID, "replacement", request)
	if err != nil {
		t.Fatal(err)
	}
	var branch orm.Conversation
	db.Where("id = ?", result.Conversation["conversation_id"]).Take(&branch)
	if branch.ChatModelID == nil || *branch.ChatModelID != "replacement-model" {
		t.Fatal("replacement model ignored")
	}
	request.ReplacementModel.ModelID = "model-fork"
	_, err = createConversationFork(context.Background(), db, caller, c.ID, "replacement", request)
	requireForkCode(t, err, "IDEMPOTENCY_CONFLICT")
}

func TestForkArtifactBytesAtLimitAreCopiedAndOverflowIsRejected(t *testing.T) {
	db, c, items, _ := forkFixture(t, 1)
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	id := newConversationID()
	path := filepath.Join(conversationArtifactFileRoot("u1", c.ID, id), "snapshot.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxForkArtifactBytes); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": path})
	if err := db.Create(&orm.ConversationArtifact{ID: id, ConversationID: c.ID, HistoryID: items[0].ID, Filename: "snapshot.bin", Slot: "snapshot", ContentType: "file", Value: raw, CreateUserID: "u1", CreatedAt: items[0].CreateTime}).Error; err != nil {
		t.Fatal(err)
	}
	caller := doc.DatasetCatalogCaller{UserID: "u1"}
	started := time.Now()
	p, err := buildForkPreview(context.Background(), db, caller, c, items)
	if err != nil {
		t.Fatal(err)
	}
	result, err := createConversationFork(context.Background(), db, caller, c.ID, "max-file", forkCreateRequest{SourceHistoryID: items[0].ID, ExpectedPrefixRevision: p.PrefixRevision})
	if err != nil {
		t.Fatal(err)
	}
	var copied orm.ConversationArtifact
	db.Where("conversation_id = ?", result.Conversation["conversation_id"]).Take(&copied)
	var value map[string]any
	_ = json.Unmarshal(copied.Value, &value)
	info, err := os.Stat(value["path"].(string))
	if err != nil || info.Size() != maxForkArtifactBytes {
		t.Fatalf("copied file: %v %v", info, err)
	}
	t.Logf("dialect=%s artifact_bytes=%d preview_and_copy_elapsed=%s", db.Dialector.Name(), info.Size(), time.Since(started))
	if err := os.Truncate(path, maxForkArtifactBytes+1); err != nil {
		t.Fatal(err)
	}
	_, err = buildForkPreview(context.Background(), db, caller, c, items)
	requireForkCode(t, err, "FORK_TOO_LARGE")
}
