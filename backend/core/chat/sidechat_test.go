package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/state"
	"lazymind/core/store"
)

func newSidechatTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newPromptTestDB(t)
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "sidechat-state.db"))
	if err != nil {
		t.Fatalf("open sidechat state store: %v", err)
	}
	store.Init(db.DB, nil, stateStore)
	t.Cleanup(func() {
		store.Init(nil, nil, nil)
		_ = stateStore.Close()
	})
	return db.DB
}

func sidechatTestConversation(t *testing.T, db *gorm.DB, id, userID, name string) orm.Conversation {
	t.Helper()
	now := time.Now().UTC()
	conversation := orm.Conversation{
		ID: id, DisplayName: name, ChannelID: "default", ChatExecutor: ChatExecutorLazyMind,
		ThinkingDepth: "high",
		BaseModel: orm.BaseModel{
			CreateUserID: userID, CreateUserName: userID, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation %s: %v", id, err)
	}
	return conversation
}

func sidechatTestHistory(t *testing.T, db *gorm.DB, id, conversationID string, seq int, query, result, status string) orm.ChatHistory {
	t.Helper()
	now := time.Now().UTC()
	history := orm.ChatHistory{
		ID: id, ConversationID: conversationID, Seq: seq, RawContent: query, Content: query,
		Result: result, RunStatus: status,
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatalf("create history %s: %v", id, err)
	}
	return history
}

func sidechatTestUploadedFile(t *testing.T, db *gorm.DB, id, userID, datasetID, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create attachment directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("source attachment"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	ext, _ := json.Marshal(map[string]any{
		"stored_path": path, "stored_name": filepath.Base(path), "original_filename": filepath.Base(path),
	})
	now := time.Now().UTC()
	if err := db.Create(&orm.UploadedFile{
		UploadFileID: id, DatasetID: datasetID, Status: "UPLOADED", Ext: ext,
		BaseModel: orm.BaseModel{
			CreateUserID: userID, CreateUserName: userID, CreatedAt: now, UpdatedAt: now,
		},
	}).Error; err != nil {
		t.Fatalf("create uploaded file: %v", err)
	}
}

func sidechatTestTempUploadSession(t *testing.T, db *gorm.DB, id, userID, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create temp attachment directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("source attachment"), 0o600); err != nil {
		t.Fatalf("write temp attachment: %v", err)
	}
	ext, _ := json.Marshal(map[string]any{
		"upload_id": id, "upload_scope": "TEMP", "stored_name": filepath.Base(path),
	})
	now := time.Now().UTC()
	if err := db.Create(&orm.UploadSession{
		UploadID: id, UploadState: "UPLOADED", Ext: ext,
		BaseModel: orm.BaseModel{
			CreateUserID: userID, CreateUserName: userID, CreatedAt: now, UpdatedAt: now,
		},
	}).Error; err != nil {
		t.Fatalf("create temp upload session: %v", err)
	}
}

func sidechatRequest(method, target, userID, body string, vars map[string]string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-Id", userID)
	request.Header.Set("X-User-Name", userID)
	return mux.SetURLVars(request, vars)
}

func TestResolveSidechatSourceCutsOffDuplicateSequenceByTimeAndID(t *testing.T) {
	db := newSidechatTestDB(t)
	parent := sidechatTestConversation(t, db, "parent-duplicate-seq", "user-1", "Parent")
	baseTime := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
	createHistory := func(id string, seq int, createdAt time.Time, filePath string) {
		t.Helper()
		ext, _ := json.Marshal(map[string]any{"input": []any{
			map[string]any{"input_type": "file", "uri": filePath},
		}})
		history := orm.ChatHistory{
			ID: id, ConversationID: parent.ID, Seq: seq, RawContent: id, Content: id,
			Result: "answer " + id, RunStatus: "completed", Ext: ext,
			TimeMixin: orm.TimeMixin{CreateTime: createdAt, UpdateTime: createdAt},
		}
		if err := db.Create(&history).Error; err != nil {
			t.Fatalf("create duplicate-seq history %s: %v", id, err)
		}
	}

	createHistory("history-previous-seq", 1, baseTime.Add(-time.Minute), "/files/previous.pdf")
	createHistory("history-a-before", 2, baseTime, "/files/before.pdf")
	createHistory("history-m-source", 2, baseTime, "/files/source.pdf")
	createHistory("history-z-after-id", 2, baseTime, "/files/after-id.pdf")
	createHistory("history-b-after-time", 2, baseTime.Add(time.Second), "/files/after-time.pdf")

	source, histories, err := resolveSidechatSource(context.Background(), db, parent.ID, createSidechatRequest{
		SourceHistoryID: "history-m-source",
	})
	if err != nil {
		t.Fatalf("resolve explicit duplicate-seq source: %v", err)
	}
	if source == nil || source.ID != "history-m-source" {
		t.Fatalf("resolved source=%#v", source)
	}
	wantIDs := []string{"history-previous-seq", "history-a-before", "history-m-source"}
	if len(histories) != len(wantIDs) {
		t.Fatalf("snapshot histories=%#v, want ids=%#v", histories, wantIDs)
	}
	for index, wantID := range wantIDs {
		if histories[index].ID != wantID {
			t.Fatalf("snapshot history[%d]=%q, want %q", index, histories[index].ID, wantID)
		}
	}
	files := sidechatSourceFileRefs(histories)
	if strings.Join(files, ",") != "/files/previous.pdf,/files/before.pdf,/files/source.pdf" {
		t.Fatalf("snapshot files=%#v", files)
	}
}

func TestSnapshotSidechatContextDoesNotCrossExactSequenceBoundaryViaSummary(t *testing.T) {
	db := newSidechatTestDB(t)
	parent := sidechatTestConversation(t, db, "parent-summary-boundary", "user-1", "Parent")
	parent.Ext = json.RawMessage(`{"model_context":{"summary_text":"later same-sequence content","covered_through_seq":2,"version":1}}`)
	if err := db.Model(&orm.Conversation{}).Where("id = ?", parent.ID).Update("ext", parent.Ext).Error; err != nil {
		t.Fatalf("save parent summary: %v", err)
	}
	source := sidechatTestHistory(t, db, "history-summary-source", parent.ID, 2, "source question", "source answer", "completed")

	snapshot, err := snapshotSidechatContext(
		context.Background(), db, parent.ID, []orm.ChatHistory{source}, "", doc.DatasetCatalogCaller{UserID: "user-1"},
	)
	if err != nil {
		t.Fatalf("snapshot sidechat context: %v", err)
	}
	if strings.Contains(string(snapshot), "later same-sequence content") {
		t.Fatalf("snapshot crossed exact source boundary: %s", snapshot)
	}
	if !strings.Contains(string(snapshot), "source question") || !strings.Contains(string(snapshot), "source answer") {
		t.Fatalf("snapshot lost source history: %s", snapshot)
	}
}

func TestSidechatCreationFreezesContextAndServerPolicy(t *testing.T) {
	db := newSidechatTestDB(t)
	uploadRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
	seedAvailableChatModel(t, db, "user-1", "provider-1", "group-1", "model-1", "Provider", "Group", "Model", "llm", true, "secret-key")
	seedSelectedChatModel(t, db, "user-1", "model-1", false)

	workflowEnabled := true
	subagentEnabled := true
	parent := sidechatTestConversation(t, db, "parent-1", "user-1", "Parent")
	if err := db.Model(&orm.Conversation{}).Where("id = ?", parent.ID).Updates(map[string]any{
		"search_config":   json.RawMessage(`{"dataset_ids":["dataset-1"]}`),
		"enable_plugin":   workflowEnabled,
		"enable_subagent": subagentEnabled,
		"chat_executor":   "codex",
	}).Error; err != nil {
		t.Fatalf("configure parent: %v", err)
	}
	parentHistory := sidechatTestHistory(t, db, "parent-history-1", parent.ID, 1, "parent question", "parent answer", "completed")
	sourcePath := filepath.Join(uploadRoot, "tmp", "users", "user-1", "files", "upload_source", "source.pdf")
	sidechatTestTempUploadSession(t, db, "upload_source", "user-1", sourcePath)
	parentHistory.Ext, _ = json.Marshal(map[string]any{"input": []any{
		map[string]any{"input_type": "text", "text": "parent question"},
		map[string]any{"input_type": "file", "uri": sourcePath},
	}})
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", parentHistory.ID).Update("ext", parentHistory.Ext).Error; err != nil {
		t.Fatalf("attach parent file: %v", err)
	}
	sidechatTestHistory(t, db, "parent-history-running", parent.ID, 2, "unfinished question", "partial progress", "running")

	request := sidechatRequest(http.MethodPost, "/api/core/conversations/parent-1/sidechat", "user-1", `{"selected_text":"quoted reference","thinking_depth":"max"}`, map[string]string{"parent_id": parent.ID})
	recorder := httptest.NewRecorder()
	CreateSidechat(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create sidechat status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), sourcePath) {
		t.Fatalf("create sidechat response leaked source path: %s", recorder.Body.String())
	}
	var payload struct {
		Conversation map[string]any `json:"conversation"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	childID, _ := payload.Conversation["id"].(string)
	if childID == "" {
		t.Fatalf("missing child id in response: %s", recorder.Body.String())
	}

	var child orm.Conversation
	if err := db.Where("id = ?", childID).Take(&child).Error; err != nil {
		t.Fatalf("load child: %v", err)
	}
	if !child.IsEphemeral || child.ParentConversationID == nil || *child.ParentConversationID != parent.ID || child.RelationType != conversationRelationSidechat {
		t.Fatalf("unexpected child relation: %#v", child)
	}
	if child.SourceSeq == nil || *child.SourceSeq != 1 || child.SourceHistoryID == nil || *child.SourceHistoryID != "parent-history-1" {
		t.Fatalf("default source should skip running history: id=%v seq=%v", child.SourceHistoryID, child.SourceSeq)
	}
	if child.EnableWorkflow == nil || *child.EnableWorkflow || child.EnableSubagent == nil || *child.EnableSubagent || child.ChatExecutor != ChatExecutorLazyMind {
		t.Fatalf("sidechat did not force basic capabilities: %#v", child)
	}
	if child.ThinkingDepth != "max" {
		t.Fatalf("sidechat did not snapshot current thinking depth: %q", child.ThinkingDepth)
	}
	if child.ChatModelMode == nil || *child.ChatModelMode != chatModelModeFixed || child.ChatModelID == nil || *child.ChatModelID != "model-1" || child.ChatModelVersion != 1 {
		t.Fatalf("legacy parent did not resolve stable model binding: %#v", child)
	}
	if strings.Contains(string(child.ChatModelSnapshot), "secret-key") {
		t.Fatalf("model snapshot leaked credentials: %s", child.ChatModelSnapshot)
	}
	var sourceSnapshot conversationSourceContextSnapshot
	if err := json.Unmarshal(child.SourceContext, &sourceSnapshot); err != nil ||
		len(sourceSnapshot.FileRefs["0"]) != 1 ||
		sourceSnapshot.FileRefs["0"][0].UploadID != "upload_source" ||
		sourceSnapshot.FileRefs["0"][0].Scope != doc.ChatSourceAttachmentScopeTemp {
		t.Fatalf("source attachment identity was not frozen: snapshot=%#v err=%v", sourceSnapshot, err)
	}
	for _, message := range sourceSnapshot.Messages {
		if _, exists := message["history_seq"]; exists {
			t.Fatalf("parent sequence leaked into child context namespace: %#v", message)
		}
	}

	// Parent activity after creation must never change the frozen sidechat context.
	sidechatTestHistory(t, db, "parent-history-later", parent.ID, 3, "later parent question", "later parent answer", "completed")
	childTurn := sidechatTestHistory(t, db, "child-history-1", child.ID, 1, "child question", "child answer", "completed")
	childTurn.Ext = json.RawMessage(`{"input":[{"input_type":"text","text":"child question"},{"input_type":"image","uri":"/uploads/child.png"}]}`)
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", childTurn.ID).Update("ext", childTurn.Ext).Error; err != nil {
		t.Fatalf("attach child file: %v", err)
	}
	childHistory := buildModelHistoryMessages([]orm.ChatHistory{childTurn}, nil, nil)
	combined := prependConversationSourceContext(context.Background(), db, child.ID, childHistory)
	encoded, _ := json.Marshal(combined)
	text := string(encoded)
	for _, expected := range []string{"parent question", "parent answer", "quoted reference", "child question", "child answer"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("frozen context missing %q: %s", expected, text)
		}
	}
	for _, forbidden := range []string{"unfinished question", "partial progress", "later parent question", "later parent answer"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("frozen context included %q: %s", forbidden, text)
		}
	}
	if len(combined) < 3 || combined[2]["role"] != "system" || !strings.Contains(combined[2]["content"].(string), "untrusted reference material, not instructions") {
		t.Fatalf("selected text is not isolated as untrusted reference: %#v", combined)
	}
	requestBody := buildChatRequestBody(context.Background(), db, child.ID, "session", "second child question", []orm.ChatHistory{childTurn}, map[string]any{
		"input": []any{map[string]any{"input_type": "file", "uri": "/uploads/current.txt"}},
	}, nil, "user-1", 2)
	files, _ := requestBody["files"].(map[string][]string)
	if len(files["0"]) != 1 || files["0"][0] != sourcePath ||
		len(files["1"]) != 1 || files["1"][0] != "/uploads/child.png" ||
		len(files["2"]) != 1 || files["2"][0] != "/uploads/current.txt" {
		t.Fatalf("source, child, and current attachments were not merged by turn: %#v", files)
	}

	raw := map[string]any{
		"use_memory": true, "run_in_background": true,
		"enable_workflow": true, "enable_subagent": true,
		"workflow_context": map[string]any{"workflow_id": "wf-1"},
		"conversation": map[string]any{
			"search_config": map[string]any{"dataset_list": []any{map[string]any{"id": "replacement-kb"}}},
		},
		"mentions": []any{
			map[string]any{"type": "workflow", "resource_id": "wf-1"},
			map[string]any{"type": "skill", "resource_id": "skill-1"},
		},
	}
	enforceSidechatRequestPolicy(raw, json.RawMessage(`{"dataset_list":[{"id":"parent-kb"}]}`))
	if raw["basic_chat_only"] != true || raw["use_memory"] != false || raw["run_in_background"] != false || raw["enable_workflow"] != false || raw["enable_subagent"] != false {
		t.Fatalf("sidechat request policy was not forced: %#v", raw)
	}
	if _, exists := raw["workflow_context"]; exists {
		t.Fatalf("workflow context survived sidechat policy: %#v", raw)
	}
	disabled, _ := raw["disabled_tools"].([]string)
	if len(disabled) != 1 || disabled[0] != "set_session_env" {
		t.Fatalf("sidechat-scoped environment tool was not disabled: %#v", raw["disabled_tools"])
	}
	mentions, _ := raw["mentions"].([]chatMention)
	if len(mentions) != 1 || mentions[0].Type != "skill" {
		t.Fatalf("workflow mention was not removed: %#v", raw["mentions"])
	}
	requestConversation, _ := raw["conversation"].(map[string]any)
	inheritedSearchConfig, _ := requestConversation["search_config"].(map[string]any)
	if got := datasetIDsFromSearchConfig(inheritedSearchConfig); !reflect.DeepEqual(got, []string{"parent-kb"}) {
		t.Fatalf("sidechat request replaced inherited knowledge bases: %#v", got)
	}

	// An explicit source that is still running is rejected instead of freezing progress.
	request = sidechatRequest(http.MethodPost, "/api/core/conversations/parent-1/sidechat", "user-1", `{"source_history_id":"parent-history-running"}`, map[string]string{"parent_id": parent.ID})
	recorder = httptest.NewRecorder()
	CreateSidechat(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("running source status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	// Every child request revalidates the frozen source before the upstream call.
	if err := db.Model(&orm.UploadSession{}).Where("upload_id = ?", "upload_source").
		Update("deleted_at", time.Now().UTC()).Error; err != nil {
		t.Fatalf("expire source upload: %v", err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", child.ID).
		Update("search_config", json.RawMessage(`{}`)).Error; err != nil {
		t.Fatalf("clear unrelated search config: %v", err)
	}
	request = sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{
		"conversation_id":"`+child.ID+`","stream":true,
		"input":[{"input_type":"text","text":"continue"}]
	}`, nil)
	recorder = httptest.NewRecorder()
	ChatConversations(recorder, request)
	if recorder.Code != http.StatusConflict || strings.Contains(recorder.Body.String(), sourcePath) {
		t.Fatalf("stale source status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSidechatSourceAttachmentRevalidationRejectsUnauthorizedReferences(t *testing.T) {
	t.Run("legacy dataset path with current read access", func(t *testing.T) {
		db := newSidechatTestDB(t)
		uploadRoot := t.TempDir()
		t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
		scanServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"dataset_id":"dataset-legacy","exists":false,"allowed":false}]}`))
		}))
		defer scanServer.Close()
		t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", scanServer.URL)

		now := time.Now().UTC()
		if err := db.Create(&orm.Dataset{
			ID: "dataset-legacy", TenantID: "tenant-1",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now,
			},
		}).Error; err != nil {
			t.Fatalf("create legacy dataset: %v", err)
		}
		path := filepath.Join(uploadRoot, "tenants", "tenant-1", "datasets", "dataset-legacy", "docs", "files", "legacy", "source.pdf")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create legacy source directory: %v", err)
		}
		if err := os.WriteFile(path, []byte("legacy dataset source"), 0o600); err != nil {
			t.Fatalf("write legacy source: %v", err)
		}

		resolved, err := doc.ValidateChatSourceAttachments(
			context.Background(), db, doc.DatasetCatalogCaller{UserID: "user-1"},
			[]doc.ChatSourceAttachmentReference{{Path: path}},
		)
		if err != nil || len(resolved) != 1 || resolved[0].DatasetID != "dataset-legacy" ||
			resolved[0].Scope != doc.ChatSourceAttachmentScopeDataset {
			t.Fatalf("resolve legacy dataset source=%#v err=%v", resolved, err)
		}
	})

	t.Run("foreign temporary upload", func(t *testing.T) {
		db := newSidechatTestDB(t)
		uploadRoot := t.TempDir()
		t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
		path := filepath.Join(uploadRoot, "tmp", "users", "user-2", "files", "upload_foreign", "private.pdf")
		sidechatTestUploadedFile(t, db, "upload_foreign", "user-2", "", path)
		parent := sidechatTestConversation(t, db, "source-parent-foreign", "user-1", "Parent")
		child := sidechatTestConversation(t, db, "source-child-foreign", "user-1", "Child")
		snapshot, _ := json.Marshal(conversationSourceContextSnapshot{
			Files: map[string][]string{"0": {path}},
		})
		if err := db.Model(&orm.Conversation{}).Where("id = ?", child.ID).Updates(map[string]any{
			"parent_conversation_id": parent.ID,
			"relation_type":          conversationRelationSidechat,
			"source_context":         json.RawMessage(snapshot),
		}).Error; err != nil {
			t.Fatalf("configure child: %v", err)
		}
		if err := db.Where("id = ?", child.ID).Take(&child).Error; err != nil {
			t.Fatalf("reload child: %v", err)
		}

		err := validateSidechatSourceAttachments(
			context.Background(), db, child, "user-1", doc.DatasetCatalogCaller{UserID: "user-1"},
		)
		if !errors.Is(err, doc.ErrChatSourceAttachmentForbidden) || strings.Contains(err.Error(), path) {
			t.Fatalf("foreign temp validation error=%v", err)
		}
	})

	t.Run("revoked dataset source access", func(t *testing.T) {
		db := newSidechatTestDB(t)
		uploadRoot := t.TempDir()
		t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
		scanServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-User-ID"); got != "user-1" {
				t.Errorf("scan caller user=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"dataset_id":"dataset-source","exists":true,"allowed":false}]}`))
		}))
		defer scanServer.Close()
		t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", scanServer.URL)

		now := time.Now().UTC()
		if err := db.Create(&orm.Dataset{
			ID: "dataset-source", TenantID: "tenant-1",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now,
			},
		}).Error; err != nil {
			t.Fatalf("create dataset: %v", err)
		}
		path := filepath.Join(uploadRoot, "tenants", "tenant-1", "datasets", "dataset-source", "docs", "files", "upload_dataset", "source.pdf")
		sidechatTestUploadedFile(t, db, "upload_dataset", "dataset-uploader", "dataset-source", path)
		parent := sidechatTestConversation(t, db, "source-parent-dataset", "user-1", "Parent")
		child := sidechatTestConversation(t, db, "source-child-dataset", "user-1", "Child")
		snapshot, _ := json.Marshal(conversationSourceContextSnapshot{
			Files: map[string][]string{"0": {path}},
			FileRefs: map[string][]doc.ChatSourceAttachmentReference{"0": {{
				UploadFileID: "upload_dataset", DatasetID: "dataset-source", Scope: doc.ChatSourceAttachmentScopeDataset,
			}}},
		})
		if err := db.Model(&orm.Conversation{}).Where("id = ?", child.ID).Updates(map[string]any{
			"parent_conversation_id": parent.ID,
			"relation_type":          conversationRelationSidechat,
			"source_context":         json.RawMessage(snapshot),
		}).Error; err != nil {
			t.Fatalf("configure child: %v", err)
		}

		request := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{
			"conversation_id":"`+child.ID+`","stream":true,
			"input":[{"input_type":"text","text":"continue"}]
		}`, nil)
		recorder := httptest.NewRecorder()
		ChatConversations(recorder, request)
		if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Body.String(), path) {
			t.Fatalf("revoked dataset source status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestSidechatRetainListDetailAndDiscard(t *testing.T) {
	db := newSidechatTestDB(t)
	parent := sidechatTestConversation(t, db, "parent-2", "user-1", "Parent two")
	sidechatTestHistory(t, db, "parent-2-history", parent.ID, 1, "source question", "source answer", "completed")
	child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{SourceSeq: intPtr(1)})
	if err != nil {
		t.Fatalf("create sidechat: %v", err)
	}

	listRecorder := httptest.NewRecorder()
	ListConversations(listRecorder, sidechatRequest(http.MethodGet, "/api/core/conversations?page_size=100", "user-1", "", nil))
	if strings.Contains(listRecorder.Body.String(), child.ID) {
		t.Fatalf("ephemeral child leaked into default list: %s", listRecorder.Body.String())
	}

	// Busy and empty sidechats cannot be retained.
	now := time.Now().UTC()
	if err := db.Create(&orm.ExternalChatRun{
		ID: "run-busy", RequestID: "request-busy", ConversationID: child.ID, HistoryID: "history-busy",
		Provider: "codex", ActorUserID: "user-1", Status: "running", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create busy run: %v", err)
	}
	retainRequest := sidechatRequest(http.MethodPost, "/api/core/conversations/"+child.ID+"/retain", "user-1", "", map[string]string{"child_id": child.ID})
	retainRecorder := httptest.NewRecorder()
	RetainSidechat(retainRecorder, retainRequest)
	if retainRecorder.Code != http.StatusConflict {
		t.Fatalf("busy retain status=%d body=%s", retainRecorder.Code, retainRecorder.Body.String())
	}
	if err := db.Where("id = ?", "run-busy").Delete(&orm.ExternalChatRun{}).Error; err != nil {
		t.Fatalf("delete busy run: %v", err)
	}
	retainRecorder = httptest.NewRecorder()
	RetainSidechat(retainRecorder, sidechatRequest(http.MethodPost, "/api/core/conversations/"+child.ID+"/retain", "user-1", "", map[string]string{"child_id": child.ID}))
	if retainRecorder.Code != http.StatusConflict {
		t.Fatalf("empty retain status=%d body=%s", retainRecorder.Code, retainRecorder.Body.String())
	}

	// A failed first attempt must not make the sidechat impossible to retain once
	// a later turn succeeds. The title comes from the first completed question.
	sidechatTestHistory(t, db, "child-2-failed-history", child.ID, 1, "failed question", "partial answer", "failed")
	longQuestion := strings.Repeat("侧聊标题", 12)
	sidechatTestHistory(t, db, "child-2-history", child.ID, 2, longQuestion, "child answer", "completed")
	oldParentTime := time.Now().UTC().Add(-time.Hour)
	if err := db.Model(&orm.Conversation{}).Where("id = ?", parent.ID).Update("updated_at", oldParentTime).Error; err != nil {
		t.Fatalf("age parent: %v", err)
	}
	retainRecorder = httptest.NewRecorder()
	RetainSidechat(retainRecorder, sidechatRequest(http.MethodPost, "/api/core/conversations/"+child.ID+"/retain", "user-1", "", map[string]string{"child_id": child.ID}))
	if retainRecorder.Code != http.StatusOK {
		t.Fatalf("retain status=%d body=%s", retainRecorder.Code, retainRecorder.Body.String())
	}
	var retainPayload struct {
		Conversation map[string]any `json:"conversation"`
	}
	if err := json.Unmarshal(retainRecorder.Body.Bytes(), &retainPayload); err != nil || retainPayload.Conversation["id"] != child.ID {
		t.Fatalf("retain response does not match direct OpenAPI payload: body=%s err=%v", retainRecorder.Body.String(), err)
	}
	var retained orm.Conversation
	if err := db.Where("id = ?", child.ID).Take(&retained).Error; err != nil {
		t.Fatalf("load retained child: %v", err)
	}
	if retained.IsEphemeral || retained.EphemeralExpiresAt != nil || len([]rune(retained.DisplayName)) != maxRetainedSidechatTitleRunes+1 {
		t.Fatalf("unexpected retained child: %#v", retained)
	}
	if strings.HasPrefix(retained.DisplayName, "failed question") {
		t.Fatalf("retained title used a failed attempt: %q", retained.DisplayName)
	}
	var touchedParent orm.Conversation
	if err := db.Where("id = ?", parent.ID).Take(&touchedParent).Error; err != nil || !touchedParent.UpdatedAt.After(oldParentTime) {
		t.Fatalf("parent recency was not touched: updated=%v err=%v", touchedParent.UpdatedAt, err)
	}

	listRecorder = httptest.NewRecorder()
	ListConversations(listRecorder, sidechatRequest(http.MethodGet, "/api/core/conversations?page_size=100", "user-1", "", nil))
	var listPayload struct {
		Conversations []map[string]any `json:"conversations"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, item := range listPayload.Conversations {
		if item["conversation_id"] != child.ID {
			continue
		}
		found = true
		if item["relation_type"] != conversationRelationSidechat || item["parent_conversation_id"] != parent.ID || item["parent_display_name"] != parent.DisplayName {
			t.Fatalf("list missing compact relation metadata: %#v", item)
		}
		if _, exists := item["source_context"]; exists {
			t.Fatalf("list returned full source_context: %#v", item)
		}
		for _, key := range []string{"selected_text", "source_history_id", "source_seq"} {
			if _, exists := item[key]; exists {
				t.Fatalf("list returned expanded relation field %q: %#v", key, item)
			}
		}
	}
	if !found {
		t.Fatalf("retained child missing from list: %s", listRecorder.Body.String())
	}

	detailRecorder := httptest.NewRecorder()
	GetConversationDetail(detailRecorder, sidechatRequest(http.MethodGet, "/api/core/conversations/conversations/"+child.ID, "user-1", "", map[string]string{"name": "conversations/" + child.ID}))
	if detailRecorder.Code != http.StatusOK || !strings.Contains(detailRecorder.Body.String(), "source_context") || !strings.Contains(detailRecorder.Body.String(), "source question") || strings.Contains(detailRecorder.Body.String(), "/uploads/source.pdf") {
		t.Fatalf("detail missing frozen context: status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}

	discard, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{SourceSeq: intPtr(1)})
	if err != nil {
		t.Fatalf("create discardable sidechat: %v", err)
	}
	discardRecorder := httptest.NewRecorder()
	DiscardSidechat(discardRecorder, sidechatRequest(http.MethodDelete, "/api/core/conversations/"+discard.ID+"/sidechat", "user-1", "", map[string]string{"child_id": discard.ID}))
	if discardRecorder.Code != http.StatusNoContent {
		t.Fatalf("discard status=%d body=%s", discardRecorder.Code, discardRecorder.Body.String())
	}
	if err := db.Unscoped().Where("id = ?", discard.ID).Take(&orm.Conversation{}).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("discarded sidechat still exists: %v", err)
	}
}

func TestSidechatNestingAuthSettingsAndFamilyLifecycle(t *testing.T) {
	db := newSidechatTestDB(t)
	parent := sidechatTestConversation(t, db, "parent-3", "user-1", "Parent three")
	sidechatTestHistory(t, db, "parent-3-history", parent.ID, 1, "source", "answer", "completed")
	child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{})
	if err != nil {
		t.Fatalf("create sidechat: %v", err)
	}

	_, _, err = createSidechatConversation(context.Background(), db, "user-1", "user-1", child.ID, createSidechatRequest{})
	if !errors.Is(err, errSidechatNestingLimit) {
		t.Fatalf("nested sidechat error=%v", err)
	}
	_, _, err = createSidechatConversation(context.Background(), db, "user-2", "user-2", parent.ID, createSidechatRequest{})
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-user sidechat error=%v", err)
	}
	ephemeralParent := sidechatTestConversation(t, db, "ephemeral-parent", "user-1", "Ephemeral parent")
	if err := db.Model(&orm.Conversation{}).Where("id = ?", ephemeralParent.ID).Update("is_ephemeral", true).Error; err != nil {
		t.Fatalf("mark ephemeral parent: %v", err)
	}
	_, _, err = createSidechatConversation(context.Background(), db, "user-1", "user-1", ephemeralParent.ID, createSidechatRequest{})
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("ephemeral parent sidechat error=%v", err)
	}
	promoteRecorder := httptest.NewRecorder()
	PromoteConversation(promoteRecorder, sidechatRequest(
		http.MethodPost, "/api/core/conversations/"+child.ID+":promote", "user-1", "",
		map[string]string{"conversation_id": child.ID},
	))
	if promoteRecorder.Code != http.StatusConflict {
		t.Fatalf("generic sidechat promote status=%d body=%s", promoteRecorder.Code, promoteRecorder.Body.String())
	}
	var stillEphemeral orm.Conversation
	if err := db.Where("id = ?", child.ID).Take(&stillEphemeral).Error; err != nil || !stillEphemeral.IsEphemeral {
		t.Fatalf("generic promote changed sidechat: %#v err=%v", stillEphemeral, err)
	}

	settingsRecorder := httptest.NewRecorder()
	PatchConversationSettings(settingsRecorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+child.ID+"/settings", "user-1", `{"thinking_depth":"max"}`, map[string]string{"conversation_id": child.ID}))
	if settingsRecorder.Code != http.StatusOK {
		t.Fatalf("update depth status=%d body=%s", settingsRecorder.Code, settingsRecorder.Body.String())
	}
	var updated orm.Conversation
	if err := db.Where("id = ?", child.ID).Take(&updated).Error; err != nil || updated.ThinkingDepth != "max" {
		t.Fatalf("thinking depth not saved: depth=%q err=%v", updated.ThinkingDepth, err)
	}
	settingsRecorder = httptest.NewRecorder()
	PatchConversationSettings(settingsRecorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+child.ID+"/settings", "user-1", `{"workflow_mode":"auto"}`, map[string]string{"conversation_id": child.ID}))
	if settingsRecorder.Code != http.StatusConflict {
		t.Fatalf("workflow mode update status=%d body=%s", settingsRecorder.Code, settingsRecorder.Body.String())
	}
	if err := db.Where("id = ?", child.ID).Take(&updated).Error; err != nil || updated.WorkflowMode == nil || *updated.WorkflowMode != "dynamic" {
		t.Fatalf("workflow mode changed: mode=%v err=%v", updated.WorkflowMode, err)
	}
	settingsRecorder = httptest.NewRecorder()
	PatchConversationSettings(settingsRecorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+child.ID+"/settings", "user-1", `{"enable_workflow":true}`, map[string]string{"conversation_id": child.ID}))
	if settingsRecorder.Code != http.StatusConflict {
		t.Fatalf("workflow enable status=%d body=%s", settingsRecorder.Code, settingsRecorder.Body.String())
	}

	// Retain the child so normal lifecycle lists include it, then verify root
	// operations move the whole family while child-only folder moves are rejected.
	sidechatTestHistory(t, db, "child-3-history", child.ID, 1, "child question", "child answer", "completed")
	if err := db.Model(&orm.Conversation{}).Where("id = ?", child.ID).Updates(map[string]any{"is_ephemeral": false, "ephemeral_expires_at": nil}).Error; err != nil {
		t.Fatalf("retain child fixture: %v", err)
	}
	archiveRecorder := httptest.NewRecorder()
	ArchiveConversation(archiveRecorder, sidechatRequest(http.MethodPost, "/api/core/conversations/"+parent.ID+":archive", "user-1", `{}`, map[string]string{"conversation_id": parent.ID}))
	if archiveRecorder.Code != http.StatusOK {
		t.Fatalf("archive family status=%d body=%s", archiveRecorder.Code, archiveRecorder.Body.String())
	}
	for _, id := range []string{parent.ID, child.ID} {
		var conversation orm.Conversation
		if err := db.Where("id = ?", id).Take(&conversation).Error; err != nil || conversation.ArchivedAt == nil {
			t.Fatalf("conversation %s not archived with family: %#v err=%v", id, conversation, err)
		}
	}
	childArchiveRecorder := httptest.NewRecorder()
	ArchiveConversation(childArchiveRecorder, sidechatRequest(http.MethodPost, "/api/core/conversations/"+child.ID+":archive", "user-1", `{}`, map[string]string{"conversation_id": child.ID}))
	if childArchiveRecorder.Code != http.StatusConflict {
		t.Fatalf("child archive status=%d body=%s", childArchiveRecorder.Code, childArchiveRecorder.Body.String())
	}
	unarchiveRecorder := httptest.NewRecorder()
	UnarchiveConversation(unarchiveRecorder, sidechatRequest(http.MethodPost, "/api/core/conversations/"+parent.ID+":unarchive", "user-1", "", map[string]string{"conversation_id": parent.ID}))
	if unarchiveRecorder.Code != http.StatusOK {
		t.Fatalf("unarchive family status=%d body=%s", unarchiveRecorder.Code, unarchiveRecorder.Body.String())
	}
	if err := db.Model(&orm.Conversation{}).Where("id IN ?", []string{parent.ID, child.ID}).
		Updates(map[string]any{"archived_at": time.Now().UTC()}).Error; err != nil {
		t.Fatalf("rearchive family for ensure test: %v", err)
	}
	if _, _, err := ensureConversation(context.Background(), db, child.ID, "", nil, nil, "user-1", "user-1", false, "", nil, nil); err != nil {
		t.Fatalf("ensure archived child: %v", err)
	}
	for _, id := range []string{parent.ID, child.ID} {
		var conversation orm.Conversation
		if err := db.Where("id = ?", id).Take(&conversation).Error; err != nil || conversation.ArchivedAt != nil {
			t.Fatalf("conversation %s not restored with family by ensure: %#v err=%v", id, conversation, err)
		}
	}

	if err := archiveConversation(context.Background(), db, parent.ID, "user-1"); err != nil {
		t.Fatalf("trash family: %v", err)
	}
	for _, id := range []string{parent.ID, child.ID} {
		var conversation orm.Conversation
		if err := db.Where("id = ?", id).Take(&conversation).Error; err != nil || conversation.DeletedAt == nil {
			t.Fatalf("conversation %s not trashed with family: %#v err=%v", id, conversation, err)
		}
	}
	restoreRecorder := httptest.NewRecorder()
	RestoreConversation(restoreRecorder, sidechatRequest(http.MethodPost, "/api/core/conversations/"+parent.ID+":restore", "user-1", "", map[string]string{"conversation_id": parent.ID}))
	if restoreRecorder.Code != http.StatusOK {
		t.Fatalf("restore family status=%d body=%s", restoreRecorder.Code, restoreRecorder.Body.String())
	}
}

func TestSidechatPreservesExplicitAutoModelBinding(t *testing.T) {
	db := newSidechatTestDB(t)
	parent := sidechatTestConversation(t, db, "parent-auto", "user-1", "Auto parent")
	mode := chatModelModeAuto
	if err := db.Model(&orm.Conversation{}).Where("id = ?", parent.ID).Updates(map[string]any{
		"chat_model_mode": mode, "chat_model_id": nil, "chat_model_version": int64(4),
	}).Error; err != nil {
		t.Fatalf("configure auto parent: %v", err)
	}
	sidechatTestHistory(t, db, "parent-auto-history", parent.ID, 1, "source", "answer", "completed")
	child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{})
	if err != nil {
		t.Fatalf("create auto sidechat: %v", err)
	}
	if child.ChatModelMode == nil || *child.ChatModelMode != chatModelModeAuto || child.ChatModelID != nil || child.ChatModelVersion != 4 {
		t.Fatalf("auto binding was not preserved: %#v", child)
	}
}

func TestSidechatDoesNotRecordConversationIdleMemoryActivity(t *testing.T) {
	db := newSidechatTestDB(t)
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	parent := sidechatTestConversation(t, db, "memory-parent", "user-1", "Parent")
	parentID := parent.ID
	createRelated := func(id, relation string, ephemeral bool) {
		t.Helper()
		conversation := sidechatTestConversation(t, db, id, "user-1", id)
		if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
			"parent_conversation_id": &parentID,
			"relation_type":          relation,
			"is_ephemeral":           ephemeral,
		}).Error; err != nil {
			t.Fatalf("configure related conversation %s: %v", id, err)
		}
	}
	createRelated("memory-sidechat-temp", conversationRelationSidechat, true)
	createRelated("memory-sidechat-retained", conversationRelationSidechat, false)
	createRelated("memory-fork", conversationRelationFork, false)

	now := time.Now().UTC()
	for _, conversationID := range []string{"memory-sidechat-temp", "memory-sidechat-retained", parent.ID, "memory-fork"} {
		recordConversationIdleActivity(
			context.Background(), db, stateStore, conversationID, "user-1", "history-"+conversationID,
			"user message", "assistant message", now,
		)
	}

	for _, conversationID := range []string{"memory-sidechat-temp", "memory-sidechat-retained"} {
		var count int64
		if err := db.Model(&orm.ConversationIdleEvent{}).Where("session_id = ?", conversationID).Count(&count).Error; err != nil {
			t.Fatalf("count sidechat idle events: %v", err)
		}
		if count != 0 {
			t.Fatalf("sidechat %s recorded %d idle Memory events", conversationID, count)
		}
	}
	for _, conversationID := range []string{parent.ID, "memory-fork"} {
		var count int64
		if err := db.Model(&orm.ConversationIdleEvent{}).Where("session_id = ?", conversationID).Count(&count).Error; err != nil {
			t.Fatalf("count ordinary idle events: %v", err)
		}
		if count != 1 {
			t.Fatalf("ordinary/fork conversation %s recorded %d idle events, want 1", conversationID, count)
		}
	}
}

func TestSidechatNextRequestIsMutuallyExclusiveAndIdempotent(t *testing.T) {
	db := newSidechatTestDB(t)
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	store.Init(db, nil, stateStore)

	seedAvailableChatModel(
		t, db, "user-1", "provider-sidechat", "group-sidechat", "model-sidechat",
		"Provider", "Group", "Model", "llm", true, "secret-key",
	)
	seedSelectedChatModel(t, db, "user-1", "model-sidechat", false)
	parent := sidechatTestConversation(t, db, "parent-request-guard", "user-1", "Parent")
	sidechatTestHistory(t, db, "parent-request-guard-history", parent.ID, 1, "source", "answer", "completed")
	child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{})
	if err != nil {
		t.Fatalf("create sidechat: %v", err)
	}
	originalScanClient := localFSScanHTTPClient
	localFSScanHTTPClient = &http.Client{Transport: chatModelRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"items":[],"total":0}`)),
			Request:    request,
		}, nil
	})}
	t.Cleanup(func() { localFSScanHTTPClient = originalScanClient })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.invalid")

	var upstreamCalls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var upstreamRequest LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		if upstreamCalls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Algorithm-Id", "sidechat-test")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "success", "data": map[string]any{"text": "answer"},
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "success", "data": map[string]any{"runtime_event": map[string]any{
				"schema_version": 1,
				"event_id":       "event-finished",
				"run_id":         upstreamRequest.Conversation.RunID,
				"type":           RuntimeEventRunFinished,
				"data": map[string]any{
					"status": "completed", "reason": "normal", "partial_output": false,
				},
			}},
		})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	send := func(action, clientRequestID, question string) *httptest.ResponseRecorder {
		payload := map[string]any{
			"action":          action,
			"conversation_id": child.ID,
			"stream":          true,
			"input": []any{
				map[string]any{"input_type": "text", "text": question},
			},
		}
		if clientRequestID != "" {
			payload["client_request_id"] = clientRequestID
		}
		body, _ := json.Marshal(payload)
		request := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", string(body), nil)
		recorder := httptest.NewRecorder()
		ChatConversations(recorder, request)
		return recorder
	}

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- send(chatActionNext, "request-first", "first question") }()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first sidechat request did not reach upstream")
	}

	concurrent := send(chatActionNext, "request-second", "concurrent question")
	if concurrent.Code != http.StatusConflict || !strings.Contains(concurrent.Body.String(), errSidechatRequestBusy.Error()) {
		t.Fatalf("concurrent status=%d body=%s", concurrent.Code, concurrent.Body.String())
	}
	missingAction := send("", "", "missing action question")
	if missingAction.Code != http.StatusConflict || !strings.Contains(missingAction.Body.String(), errSidechatRequestBusy.Error()) {
		t.Fatalf("missing action status=%d body=%s", missingAction.Code, missingAction.Body.String())
	}
	regeneration := send(chatActionRegeneration, "request-regen-busy", "first question")
	if regeneration.Code != http.StatusConflict || !strings.Contains(regeneration.Body.String(), errSidechatRequestBusy.Error()) {
		t.Fatalf("regeneration status=%d body=%s", regeneration.Code, regeneration.Body.String())
	}
	close(releaseFirst)
	var first *httptest.ResponseRecorder
	select {
	case first = <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first sidechat request did not finish")
	}
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}

	replay := send(chatActionNext, "request-first", "first question")
	if replay.Code != http.StatusConflict || !strings.Contains(replay.Body.String(), "already accepted") {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	regeneration = send(chatActionRegeneration, "request-regen", "first question")
	if regeneration.Code != http.StatusOK {
		t.Fatalf("regeneration status=%d body=%s", regeneration.Code, regeneration.Body.String())
	}
	regenerationReplay := send(chatActionRegeneration, "request-regen", "first question")
	if regenerationReplay.Code != http.StatusConflict || !strings.Contains(regenerationReplay.Body.String(), "already accepted") {
		t.Fatalf("regeneration replay status=%d body=%s", regenerationReplay.Code, regenerationReplay.Body.String())
	}
	if got := upstreamCalls.Load(); got != 2 {
		t.Fatalf("upstream calls=%d, want 2", got)
	}
	var historyCount int64
	if err := db.Model(&orm.ChatHistory{}).Where("conversation_id = ?", child.ID).Count(&historyCount).Error; err != nil {
		t.Fatalf("count child histories: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("child histories=%d, want 1", historyCount)
	}
}

func TestExpiredSidechatCleanupSkipsActiveRequest(t *testing.T) {
	db := newSidechatTestDB(t)
	parent := sidechatTestConversation(t, db, "parent-expired-cleanup", "user-1", "Parent")
	child := sidechatTestConversation(t, db, "child-expired-cleanup", "user-1", "Child")
	past := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&orm.Conversation{}).Where("id = ?", child.ID).Updates(map[string]any{
		"parent_conversation_id": parent.ID,
		"relation_type":          conversationRelationSidechat,
		"is_ephemeral":           true,
		"ephemeral_expires_at":   past,
	}).Error; err != nil {
		t.Fatalf("configure expired sidechat: %v", err)
	}
	guard, err := acquireSidechatNextRequestGuard(context.Background(), store.State(), child.ID, "")
	if err != nil {
		t.Fatalf("acquire active request guard: %v", err)
	}
	purged, failed := PurgeExpiredConversationTrash(context.Background(), db, time.Now().UTC())
	if purged != 0 || failed != 0 {
		t.Fatalf("cleanup while active purged=%d failed=%d", purged, failed)
	}
	if err := db.Where("id = ?", child.ID).Take(&orm.Conversation{}).Error; err != nil {
		t.Fatalf("active expired sidechat was removed: %v", err)
	}
	guard.Release()
	purged, failed = PurgeExpiredConversationTrash(context.Background(), db, time.Now().UTC())
	if purged != 1 || failed != 0 {
		t.Fatalf("cleanup after release purged=%d failed=%d", purged, failed)
	}
	if err := db.Where("id = ?", child.ID).Take(&orm.Conversation{}).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expired sidechat still exists: %v", err)
	}
}

func TestParentPurgeDoesNotPartiallyDeleteWhenAChildIsBusy(t *testing.T) {
	db := newSidechatTestDB(t)
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	store.Init(db, nil, stateStore)

	parent := sidechatTestConversation(t, db, "purge-parent", "user-1", "Parent")
	parentID := parent.ID
	for _, id := range []string{"purge-child-a", "purge-child-z"} {
		child := sidechatTestConversation(t, db, id, "user-1", id)
		if err := db.Model(&orm.Conversation{}).Where("id = ?", child.ID).Updates(map[string]any{
			"parent_conversation_id": parentID,
			"relation_type":          conversationRelationSidechat,
			"is_ephemeral":           false,
		}).Error; err != nil {
			t.Fatalf("configure child %s: %v", id, err)
		}
	}
	if err := archiveConversation(context.Background(), db, parent.ID, "user-1"); err != nil {
		t.Fatalf("trash family: %v", err)
	}
	busyGuard, err := acquireSidechatNextRequestGuard(context.Background(), stateStore, "purge-child-z", "")
	if err != nil {
		t.Fatalf("acquire busy child guard: %v", err)
	}
	defer busyGuard.Release()

	if err := purgeConversation(db, parent.ID, "user-1"); !errors.Is(err, errSidechatRequestBusy) {
		t.Fatalf("purge busy family error=%v", err)
	}
	for _, id := range []string{parent.ID, "purge-child-a", "purge-child-z"} {
		if err := db.Where("id = ?", id).Take(&orm.Conversation{}).Error; err != nil {
			t.Fatalf("busy family member %s was partially purged: %v", id, err)
		}
	}
	probe, err := acquireSidechatNextRequestGuard(context.Background(), stateStore, "purge-child-a", "")
	if err != nil {
		t.Fatalf("earlier child guard was not released: %v", err)
	}
	probe.Release()
}

func TestPurgeLeavesWorkspaceUntouchedWhenDatabaseDeleteRollsBack(t *testing.T) {
	db := newSidechatTestDB(t)
	workspace := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", workspace)
	conversation := sidechatTestConversation(t, db, "purge-rollback", "user-1", "Rollback")
	sidechatTestHistory(t, db, "purge-rollback-history", conversation.ID, 1, "question", "answer", "completed")
	if err := archiveConversation(context.Background(), db, conversation.ID, "user-1"); err != nil {
		t.Fatalf("trash conversation: %v", err)
	}
	artifactRoot := conversationArtifactConversationRoot("user-1", conversation.ID)
	if err := os.MkdirAll(artifactRoot, 0o755); err != nil {
		t.Fatalf("create artifact root: %v", err)
	}
	artifactPath := filepath.Join(artifactRoot, "result.txt")
	if err := os.WriteFile(artifactPath, []byte("preserve on rollback"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER fail_sidechat_history_delete
		BEFORE DELETE ON chat_histories
		WHEN OLD.conversation_id = 'purge-rollback'
		BEGIN SELECT RAISE(ABORT, 'forced purge rollback'); END`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if err := purgeConversation(db, conversation.ID, "user-1"); err == nil {
		t.Fatal("purge unexpectedly succeeded")
	}
	if err := db.Where("id = ?", conversation.ID).Take(&orm.Conversation{}).Error; err != nil {
		t.Fatalf("conversation did not roll back: %v", err)
	}
	if err := db.Where("id = ?", "purge-rollback-history").Take(&orm.ChatHistory{}).Error; err != nil {
		t.Fatalf("history did not roll back: %v", err)
	}
	if contents, err := os.ReadFile(artifactPath); err != nil || string(contents) != "preserve on rollback" {
		t.Fatalf("artifact workspace was not restored: contents=%q err=%v", contents, err)
	}
	if matches, err := filepath.Glob(artifactRoot + ".purging-*"); err != nil || len(matches) != 0 {
		t.Fatalf("quarantine remained after rollback: matches=%v err=%v", matches, err)
	}
}

func TestSidechatRequestGuardReleaseKeepsNewOwnerLock(t *testing.T) {
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	defer stateStore.Close()

	guard, err := acquireSidechatNextRequestGuard(context.Background(), stateStore, "child-owner-check", "")
	if err != nil {
		t.Fatalf("acquire sidechat guard: %v", err)
	}
	newOwner := []byte("new-owner-token")
	if err := stateStore.Set(context.Background(), guard.lockKey, newOwner, time.Minute); err != nil {
		t.Fatalf("replace lock owner: %v", err)
	}
	guard.Release()
	current, err := stateStore.Get(context.Background(), guard.lockKey)
	if err != nil || string(current) != string(newOwner) {
		t.Fatalf("new owner lock was removed: value=%q err=%v", current, err)
	}
}

func TestSidechatRequestReplayMarkerStartsAfterAcceptance(t *testing.T) {
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	defer stateStore.Close()

	guard, err := acquireSidechatNextRequestGuard(context.Background(), stateStore, "child-acceptance", "request-1")
	if err != nil {
		t.Fatalf("acquire preflight guard: %v", err)
	}
	guard.Release()

	retryGuard, err := acquireSidechatNextRequestGuard(context.Background(), stateStore, "child-acceptance", "request-1")
	if err != nil {
		t.Fatalf("preflight failure poisoned request id: %v", err)
	}
	if err := retryGuard.MarkAccepted(context.Background()); err != nil {
		t.Fatalf("mark request accepted: %v", err)
	}
	retryGuard.Release()

	if _, err := acquireSidechatNextRequestGuard(context.Background(), stateStore, "child-acceptance", "request-1"); !errors.Is(err, errSidechatRequestReplay) {
		t.Fatalf("accepted request replay error=%v", err)
	}
}

func TestSidechatRetainAndDiscardRejectActiveRequestGuard(t *testing.T) {
	db := newSidechatTestDB(t)
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	store.Init(db, nil, stateStore)

	parent := sidechatTestConversation(t, db, "parent-mutation-guard", "user-1", "Parent")
	child := sidechatTestConversation(t, db, "child-mutation-guard", "user-1", "Child")
	if err := db.Model(&orm.Conversation{}).Where("id = ?", child.ID).Updates(map[string]any{
		"parent_conversation_id": parent.ID,
		"relation_type":          conversationRelationSidechat,
		"is_ephemeral":           true,
	}).Error; err != nil {
		t.Fatalf("configure child: %v", err)
	}
	sidechatTestHistory(t, db, "child-mutation-history", child.ID, 1, "question", "answer", "completed")

	guard, err := acquireSidechatNextRequestGuard(context.Background(), stateStore, child.ID, "")
	if err != nil {
		t.Fatalf("acquire active request guard: %v", err)
	}
	defer guard.Release()

	retainRecorder := httptest.NewRecorder()
	RetainSidechat(retainRecorder, sidechatRequest(
		http.MethodPost, "/api/core/conversations/"+child.ID+"/retain", "user-1", "",
		map[string]string{"child_id": child.ID},
	))
	if retainRecorder.Code != http.StatusConflict {
		t.Fatalf("retain status=%d body=%s", retainRecorder.Code, retainRecorder.Body.String())
	}

	discardRecorder := httptest.NewRecorder()
	DiscardSidechat(discardRecorder, sidechatRequest(
		http.MethodDelete, "/api/core/conversations/"+child.ID+"/sidechat", "user-1", "",
		map[string]string{"child_id": child.ID},
	))
	if discardRecorder.Code != http.StatusConflict {
		t.Fatalf("discard status=%d body=%s", discardRecorder.Code, discardRecorder.Body.String())
	}
}

func intPtr(value int) *int { return &value }
