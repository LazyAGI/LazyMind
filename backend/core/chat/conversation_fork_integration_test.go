package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func requireForkCode(t *testing.T, err error, code string) {
	t.Helper()
	var problem *forkError
	if !errors.As(err, &problem) || problem.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestForkFirstActualRequestUsesNodeConfigurationAndOnlyItsPrefix(t *testing.T) {
	db, c, items, _ := forkFixture(t, 3)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	seedAvailableChatModel(t, db, "u1", "provider-global", "group-global", "model-global", "Global", "Global", "Global", "llm", true, "fake-global-key")
	seedSelectedChatModel(t, db, "u1", "model-global", false)
	items[1].Ext = mergeConversationConfigSnapshot(nil, map[string]any{chatModelRouteBodyKey: &chatModelRoute{Mode: "auto", ModelID: "model-fork", ModelName: "A"}, "thinking_depth": "high", "enable_workflow": false, "enable_subagent": false, "use_memory": false, "reasoning": false, "mode": "manual", "llm_config": map[string]any{"llm": map[string]any{"max_input_tokens": "4096"}}})
	if err := db.Model(&items[1]).Update("ext", items[1].Ext).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&c).Updates(map[string]any{"thinking_depth": "low", "ext": json.RawMessage(`{"intent_context":{"goal":"FUTURE_INTENT"},"model_context":{"summary_text":"FUTURE_SUMMARY","covered_through_seq":3}}`)}).Error; err != nil {
		t.Fatal(err)
	}
	revision, _ := forkPrefixRevision(items[:2])
	result, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "first-request", forkCreateRequest{SourceHistoryID: items[1].ID, ExpectedPrefixRevision: revision})
	if err != nil {
		t.Fatal(err)
	}
	id := result.Conversation["conversation_id"].(string)
	captured := make(chan LazyChatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/scan/sources" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
			return
		}
		var request LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		captured <- request
		encoder := json.NewEncoder(w)
		_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"text": "branch answer"}})
		_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "completed", Reason: "normal"})}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", server.URL)
	recorder := httptest.NewRecorder()
	Chat(recorder, sidechatRequest(http.MethodPost, "/api/core/chat", "u1", fmt.Sprintf(`{"conversation_id":%q,"query":"continue branch","stream":false}`, id), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case request := <-captured:
		wantHistory := []ChatMessage{
			{Role: "user", Content: "q1", HistorySeq: 1},
			{Role: "assistant", Content: "a1", HistorySeq: 1},
			{Role: "user", Content: "q2", HistorySeq: 2},
			{Role: "assistant", Content: "a2", HistorySeq: 2},
		}
		if len(request.Message.History) != len(wantHistory) {
			t.Fatalf("fork history = %#v, want %#v", request.Message.History, wantHistory)
		}
		for i, want := range wantHistory {
			if got := request.Message.History[i]; got != want {
				t.Fatalf("fork history[%d] = %#v, want %#v", i, got, want)
			}
		}
		raw, _ := json.Marshal(request)
		if request.Runtime.ThinkingDepth != "high" || request.Runtime.Reasoning || request.Conversation.Mode != "manual" || request.Runtime.LLMConfig["llm"].(map[string]any)["max_input_tokens"] != "4096" || request.Personalization.UseMemory || strings.Contains(string(raw), "FUTURE_") || strings.Contains(string(raw), "model-global") {
			t.Fatalf("wrong fork request: %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("no upstream request")
	}
	var newTurn orm.ChatHistory
	if err := db.Where("conversation_id = ? AND seq = 3", id).Take(&newTurn).Error; err != nil {
		t.Fatal(err)
	}
	config := forkConfigFromHistory(newTurn)
	if config.Model == nil || config.Model.Mode != "auto" || config.Model.ModelID != "model-fork" || config.ThinkingDepth != "high" {
		t.Fatalf("wrong persisted config: %#v", config)
	}
	var unchanged orm.ChatHistory
	db.Where("id = ?", items[2].ID).Take(&unchanged)
	if unchanged.Result != "a3" {
		t.Fatal("source turn changed")
	}
}

func TestForkAttachmentsRemainReadableButRevokedPayloadIsNotSent(t *testing.T) {
	db, c, items, _ := forkFixture(t, 1)
	uploadRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
	path := filepath.Join(uploadRoot, "tmp", "users", "u1", "files", "upload-fork", "note.txt")
	sidechatTestTempUploadSession(t, db, "upload-fork", "u1", path)
	var ext map[string]any
	_ = json.Unmarshal(items[0].Ext, &ext)
	ext["input"] = []any{map[string]any{"input_type": "file", "uri": path, "input_base64": "DERIVED_DATA"}}
	items[0].Ext = marshalChatHistoryExt(ext)
	items[0].Content = "EXTRACTED_TEXT"
	items[0].Result = "ordinary answer <tool_result>RETRIEVED_FRAGMENT</tool_result>"
	items[0].RetrievalResult = json.RawMessage(`[{"content":"RETRIEVED_FRAGMENT"}]`)
	if err := db.Model(&items[0]).Updates(map[string]any{"ext": items[0].Ext, "content": items[0].Content, "result": items[0].Result, "retrieval_result": items[0].RetrievalResult}).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := buildForkPreview(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.AttachmentsSummary) != 1 || preview.AttachmentsSummary[0].Status != "available" {
		t.Fatalf("attachment unavailable: %#v", preview.AttachmentsSummary)
	}
	result, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "attachments", forkCreateRequest{SourceHistoryID: items[0].ID, ExpectedPrefixRevision: preview.PrefixRevision})
	if err != nil {
		t.Fatal(err)
	}
	var copied []orm.ChatHistory
	db.Where("conversation_id = ?", result.Conversation["conversation_id"]).Find(&copied)
	if err := db.Model(&orm.UploadSession{}).Where("upload_id = ?", "upload-fork").Update("create_user_id", "another-user").Error; err != nil {
		t.Fatal(err)
	}
	projected, err := revalidateForkHistoryAttachments(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, copied)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(projected)
	if strings.Contains(string(encoded), "EXTRACTED_TEXT") || strings.Contains(string(encoded), "RETRIEVED_FRAGMENT") || strings.Contains(string(encoded), "DERIVED_DATA") || len(filesPerTurnMap(projected, nil, 2)) != 0 || !strings.Contains(projected[0].Result, "ordinary answer") {
		t.Fatalf("revoked data in model projection: %s", encoded)
	}
	if !strings.Contains(copied[0].Result, "ordinary answer") || !strings.Contains(copied[0].Result, "RETRIEVED_FRAGMENT") {
		t.Fatal("readable stored transcript mutated")
	}
	readable, err := refreshForkAttachmentsForRead(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, copied)
	if err != nil {
		t.Fatal(err)
	}
	if readable[0].Result != copied[0].Result || !strings.Contains(string(readable[0].Ext), `"fork_unavailable":true`) || !strings.Contains(string(readable[0].Ext), `"filename":"note.txt"`) {
		t.Fatal("read projection lost transcript or attachment placeholder")
	}
	// Database failures cannot be classified as missing attachments.
	if err := db.Migrator().DropTable(&orm.UploadSession{}); err != nil {
		t.Fatal(err)
	}
	if _, err := revalidateForkHistoryAttachments(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, copied); err == nil {
		t.Fatal("dependency failure silently downgraded")
	}
}

func TestForkArtifactCopySurvivesSourcePurgeAndRollbackRemovesPreparedFiles(t *testing.T) {
	db, c, items, _ := forkFixture(t, 1)
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	id := newConversationID()
	path := filepath.Join(conversationArtifactFileRoot("u1", c.ID, id), "report.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("immutable report"), 0600); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{"filename": "report.txt", "path": path, "size": 16})
	if err := db.Create(&orm.ConversationArtifact{ID: id, ConversationID: c.ID, HistoryID: items[0].ID, Filename: "report.txt", Slot: "report.txt", ContentType: "file", Value: value, CreateUserID: "u1", CreatedAt: items[0].CreateTime}).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := buildForkPreview(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c, items)
	if err != nil {
		t.Fatal(err)
	}
	request := forkCreateRequest{SourceHistoryID: items[0].ID, ExpectedPrefixRevision: preview.PrefixRevision}
	callback := "test:fork_artifact_failure"
	db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "conversation_fork_requests" {
			tx.AddError(errors.New("injected artifact rollback"))
		}
	})
	_, err = createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "file-rollback", request)
	if err == nil {
		t.Fatal("injected failure ignored")
	}
	db.Callback().Create().Remove(callback)
	root := filepath.Dir(conversationArtifactConversationRoot("u1", c.ID))
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("unpublished files leaked: %v %v", entries, err)
	}
	result, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "file-ok", request)
	if err != nil {
		t.Fatal(err)
	}
	branchID := result.Conversation["conversation_id"].(string)
	var artifact orm.ConversationArtifact
	if err := db.Where("conversation_id = ?", branchID).Take(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	var copiedValue map[string]any
	_ = json.Unmarshal(artifact.Value, &copiedValue)
	if artifact.ID == id || artifact.HistoryID == items[0].ID || copiedValue["path"] == path {
		t.Fatal("artifact identity or writable path shared")
	}
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := archiveConversation(context.Background(), db, c.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := purgeConversation(db, c.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(copiedValue["path"].(string))
	if err != nil || string(content) != "immutable report" {
		t.Fatalf("source purge removed branch artifact: %s %v", content, err)
	}
	var count int64
	db.Model(&orm.ConversationForkOrigin{}).Where("conversation_id = ?", branchID).Count(&count)
	if count != 1 {
		t.Fatal("source purge removed provenance")
	}
	var branch orm.Conversation
	db.Where("id = ?", branchID).Take(&branch)
	var branchHistory []orm.ChatHistory
	db.Where("conversation_id = ?", branchID).Order("seq").Find(&branchHistory)
	nestedPreview, err := buildForkPreview(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, branch, branchHistory)
	if err != nil {
		t.Fatal(err)
	}
	if len(nestedPreview.AttachmentsSummary) != 1 {
		t.Fatal("fork of fork lost inherited artifact")
	}
	nested, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, branchID, "nested-artifact", forkCreateRequest{SourceHistoryID: branchHistory[0].ID, ExpectedPrefixRevision: nestedPreview.PrefixRevision})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.ConversationArtifact{}).Where("conversation_id = ?", nested.Conversation["conversation_id"]).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("nested artifacts=%d err=%v", count, err)
	}
}

func TestForkAnchoredHistoryPagesAreContiguousAndOwned(t *testing.T) {
	db, c, _, _ := forkFixture(t, 105)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	read := func(query, user string) (map[string]any, int) {
		r := sidechatRequest(http.MethodGet, "/api/core/conversations/"+c.ID+":history?"+query, user, "", map[string]string{"name": "conversations/" + c.ID})
		w := httptest.NewRecorder()
		GetConversationHistory(w, r)
		var payload map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &payload)
		return payload, w.Code
	}
	p, status := read("anchor_history_id=source-0040", "u1")
	if status != 200 {
		t.Fatalf("anchor status=%d %#v", status, p)
	}
	rows := p["history"].([]any)
	if len(rows) != 20 || rows[0].(map[string]any)["seq"] != float64(49) || rows[19].(map[string]any)["seq"] != float64(30) {
		t.Fatalf("wrong window %#v", p)
	}
	for _, direction := range []string{"older", "newer"} {
		seen := map[string]bool{}
		token := p[direction+"_page_token"].(string)
		for token != "" {
			next, code := read("anchor_page_token="+url.QueryEscape(token), "u1")
			if code != 200 {
				t.Fatalf("page status=%d %#v", code, next)
			}
			for _, raw := range next["history"].([]any) {
				row := raw.(map[string]any)
				id := row["id"].(string)
				if seen[id] {
					t.Fatal("duplicate history")
				}
				seen[id] = true
			}
			token = next[direction+"_page_token"].(string)
		}
		want := 29
		if direction == "newer" {
			want = 56
		}
		if len(seen) != want {
			t.Fatalf("%s count=%d want=%d", direction, len(seen), want)
		}
	}
	if _, code := read("anchor_history_id=source-0040", "other-user"); code != 404 {
		t.Fatalf("cross user status=%d", code)
	}
}

func TestForkCheckpointSerializesRunAndDeletion(t *testing.T) {
	db, c, items, request := forkFixture(t, 1)
	if err := registerForkableHistoryRun(context.Background(), db, c.ID, items[0].ID, "new-run", "q", chatPersistTarget{HistoryID: items[0].ID, Seq: 1, IsRegeneration: true, Existing: &items[0]}, items[0].Ext); err != nil {
		t.Fatal(err)
	}
	_, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "running", request)
	requireForkCode(t, err, "SOURCE_NOT_SETTLED")
	if _, err := updateOwnedChatHistory(context.Background(), db, items[0].ID, "old-run", map[string]any{"run_status": "completed"}); err != nil {
		t.Fatal(err)
	}
	var history orm.ChatHistory
	db.Where("id = ?", items[0].ID).Take(&history)
	if history.RunStatus != "generating" {
		t.Fatal("old run overwrote new owner")
	}
	if err := archiveConversation(context.Background(), db, c.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	_, err = createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "deleted", request)
	requireForkCode(t, err, "SOURCE_UNAVAILABLE")
}

func TestForkCommitSerializesAgainstSourceRewrite(t *testing.T) {
	db, c, items, request := forkFixture(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reached, release := make(chan struct{}), make(chan struct{})
	callback := "test:fork_checkpoint_barrier"
	db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "conversation_fork_origins" {
			close(reached)
			select {
			case <-release:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		}
	})
	defer db.Callback().Create().Remove(callback)
	type outcome struct {
		result *forkResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, e := createConversationFork(ctx, db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "barrier", request)
		done <- outcome{r, e}
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("fork did not reach transaction barrier")
	}
	rewriting := make(chan struct{})
	rewritten := make(chan error, 1)
	go func() {
		close(rewriting)
		rewritten <- conversationCheckpoint(ctx, db, c.ID, func(tx *gorm.DB) error { return tx.Model(&items[0]).Update("result", "rewritten source").Error })
	}()
	<-rewriting
	close(release)
	created := <-done
	rewriteErr := <-rewritten
	if created.err != nil || rewriteErr != nil {
		t.Fatalf("fork=%v rewrite=%v", created.err, rewriteErr)
	}
	var branchTurn orm.ChatHistory
	if err := db.Where("conversation_id = ?", created.result.Conversation["conversation_id"]).Take(&branchTurn).Error; err != nil {
		t.Fatal(err)
	}
	if branchTurn.Result != "a1" {
		t.Fatal("rewrite interleaved with fork snapshot")
	}
	_, err := createConversationFork(ctx, db, doc.DatasetCatalogCaller{UserID: "u1"}, c.ID, "after-rewrite", request)
	requireForkCode(t, err, "SOURCE_CHANGED")
}
