package chat

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestSaveChatExportIdempotentAndOwned(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}, &orm.ConversationArtifact{})
	ctx := context.Background()
	conv := orm.Conversation{ID: "conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := &ChatExportSnapshot{Content: "报告😀", Exports: []ChatExport{{Title: "报告", Filename: "报告.md", ContentType: "text/markdown", Start: 0, End: 4}}}
	exports := finalizeChatExports(snapshot, "conv", "history", "run")
	history := orm.ChatHistory{ID: "history", ConversationID: "conv", RunID: "run", RunStatus: "completed", Result: snapshot.Content, Ext: withChatExports(nil, exports)}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.ConversationArtifact{}).Count(&count)
	if count != 0 {
		t.Fatal("metadata created an artifact")
	}
	req := CreateChatExportRequest{HistoryID: history.ID, ExportID: exports[0].ExportID, Filename: "报告.md", ContentType: "text/markdown", Content: "报告😀"}
	first, created, err := saveChatExport(ctx, db.DB, "owner", "conv", req)
	if err != nil || !created {
		t.Fatalf("save: %v %v", created, err)
	}
	req.Content = "retry must not overwrite"
	second, created, err := saveChatExport(ctx, db.DB, "owner", "conv", req)
	if err != nil || created || first.ArtifactID != second.ArtifactID {
		t.Fatalf("retry: %v %v", created, err)
	}
	assertStoredArtifactValue(t, second.Value, `{"text":"报告😀","chat_export":true}`)
	for _, change := range []func(*CreateChatExportRequest){
		func(r *CreateChatExportRequest) { r.HistoryID = "other" },
		func(r *CreateChatExportRequest) { r.ExportID = "other" },
		func(r *CreateChatExportRequest) { r.Filename = "../bad.md" },
		func(r *CreateChatExportRequest) { r.ContentType = "file" },
		func(r *CreateChatExportRequest) { r.Content = strings.Repeat("a", maxConversationArtifactBytes) },
	} {
		bad := req
		change(&bad)
		if _, _, err := saveChatExport(ctx, db.DB, "owner", "conv", bad); err == nil {
			t.Fatalf("accepted invalid request: %+v", bad.HistoryID)
		}
	}
	if _, _, err := saveChatExport(ctx, db.DB, "other", "conv", req); !errors.Is(err, errChatExportUnavailable) {
		t.Fatalf("ownership: %v", err)
	}
	db.Model(&orm.ConversationArtifact{}).Count(&count)
	if count != 1 {
		t.Fatalf("artifact count=%d", count)
	}
	regenerated := finalizeChatExports(snapshot, "conv", "history", "new-run")
	if regenerated[0].ExportID == exports[0].ExportID {
		t.Fatal("regeneration reused export identity")
	}
	db.Model(&history).Update("ext", withChatExports(nil, regenerated))
	if _, _, err := saveChatExport(ctx, db.DB, "owner", "conv", req); !errors.Is(err, errChatExportUnavailable) {
		t.Fatalf("stale export: %v", err)
	}
	req.ExportID = regenerated[0].ExportID
	req.Content = "新版报告"
	if _, created, err := saveChatExport(ctx, db.DB, "owner", "conv", req); err != nil || !created {
		t.Fatalf("save regenerated export: created=%v err=%v", created, err)
	}
	var previous orm.ConversationArtifact
	if err := db.First(&previous, "id = ?", exports[0].ExportID).Error; err != nil {
		t.Fatal(err)
	}
	assertStoredArtifactValue(t, previous.Value, `{"text":"报告😀","chat_export":true}`)
	db.Model(&orm.ConversationArtifact{}).Count(&count)
	if count != 2 {
		t.Fatalf("regeneration should retain both files, count=%d", count)
	}
}

func TestExportSnapshotPersistsBeforeTerminalAndSurvivesHistory(t *testing.T) {
	db, stateStore := newRunDecisionStreamHarness(t)
	runID := "export-run"
	snapshot := ChatExportSnapshot{Content: "报告😀", Exports: []ChatExport{{Title: "报告", Filename: "报告.md", ContentType: "text/markdown", Start: 0, End: 4}}}
	terminal := completedRunEvent(runID, true)
	server := streamServer(t, runID,
		algorithmFrame(t, map[string]any{"text": "报告😀\n"}),
		algorithmFrame(t, map[string]any{"runtime_event": terminal, "export_snapshot": snapshot}),
	)
	defer server.Close()
	recorder := httptest.NewRecorder()
	streamSingleAnswer(context.Background(), context.Background(), recorder, recorder, db, stateStore,
		server.URL, map[string]any{"query": "question", "run_id": runID}, "conv-stream-decision", "question", "export-history",
		chatPersistTarget{HistoryID: "export-history", Seq: 1}, json.RawMessage(`{"other":"keep"}`))
	chunks := decodeChatChunkSSE(t, recorder.Body.String())
	sawExports := false
	for _, chunk := range chunks {
		if chunk.Exports != nil {
			sawExports = true
			if chunk.DeltaMode != ChatDeltaModeReplace || chunk.Delta != snapshot.Content || len(*chunk.Exports) != 1 {
				t.Fatalf("snapshot=%+v", chunk)
			}
		}
		if chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventRunFinished && !sawExports {
			t.Fatal("terminal preceded exports")
		}
	}
	if !sawExports {
		t.Fatal("no export snapshot")
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", "export-history").Error; err != nil {
		t.Fatal(err)
	}
	if history.Result != snapshot.Content || strings.Contains(buildAssistantHistoryContent(history), ":::export") {
		t.Fatalf("history=%q", history.Result)
	}
	if len(chatExportsFromExt(history.Ext)) != 1 || !strings.Contains(string(history.Ext), `"other":"keep"`) {
		t.Fatalf("ext=%s", history.Ext)
	}
	items := conversationHistoryResponseItems([]orm.ChatHistory{history})
	if _, ok := items[0]["exports"]; !ok {
		t.Fatal("exports missing on reload")
	}
}

func TestExportSnapshotCancelledHasNoSaveEntry(t *testing.T) {
	db, stateStore := newRunDecisionStreamHarness(t)
	runID := "cancel-export-run"
	snapshot := ChatExportSnapshot{Content: ":::export{title=\"报告\" filename=\"报告.md\"}\nunclosed", Exports: []ChatExport{}}
	server := streamServer(t, runID, algorithmFrame(t, map[string]any{"runtime_event": cancelledRunEvent(runID, true), "export_snapshot": snapshot}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	streamSingleAnswer(context.Background(), context.Background(), recorder, recorder, db, stateStore,
		server.URL, map[string]any{"query": "question", "run_id": runID}, "conv-stream-decision", "question", "cancel-export-history",
		chatPersistTarget{HistoryID: "cancel-export-history", Seq: 1}, nil)
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", "cancel-export-history").Error; err != nil {
		t.Fatal(err)
	}
	if history.Result != snapshot.Content || len(chatExportsFromExt(history.Ext)) != 0 {
		t.Fatalf("cancelled history=%+v", history)
	}
}

func TestConcurrentChatExportSaves(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{}, &orm.ConversationArtifact{})
	conv := orm.Conversation{ID: "concurrent-conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	exports := finalizeChatExports(&ChatExportSnapshot{Content: "body", Exports: []ChatExport{{Title: "report", Filename: "report.md", ContentType: "text/markdown", End: 4}}}, conv.ID, "h", "run")
	history := orm.ChatHistory{ID: "h", ConversationID: conv.ID, RunStatus: "completed", Ext: withChatExports(nil, exports)}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	req := CreateChatExportRequest{HistoryID: "h", ExportID: exports[0].ExportID, Filename: "report.md", ContentType: "text/markdown", Content: "body"}
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			_, _, err := saveChatExport(context.Background(), db.DB, "owner", conv.ID, req)
			results <- err
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent save: %v", err)
		}
	}
	var count int64
	db.Model(&orm.ConversationArtifact{}).Count(&count)
	if count != 1 {
		t.Fatalf("count=%d", count)
	}
}

func TestExportResumeUsesFinalReplacement(t *testing.T) {
	exports := []ChatExport{{Index: 0, ExportID: "saved", Start: 0, End: 4}}
	merged := mergeChunksToFirstChunk([]*ChatChunkResponse{
		{Delta: "unfinished stream\n"}, {Delta: "body", DeltaMode: ChatDeltaModeReplace, Exports: &exports},
		{RuntimeEvent: completedRunEvent("run", true)},
	})
	if merged.Delta != "body" || merged.Exports == nil || (*merged.Exports)[0].ExportID != "saved" {
		t.Fatalf("merged=%+v", merged)
	}
}

func TestCreateChatExportHTTPPublishesOnce(t *testing.T) {
	db, stateStore := newRunDecisionStreamHarness(t)
	if err := db.AutoMigrate(&orm.ConversationArtifact{}); err != nil {
		t.Fatal(err)
	}
	store.Init(db, nil, stateStore)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	convID := "conv-stream-decision"
	exports := finalizeChatExports(&ChatExportSnapshot{Content: "body", Exports: []ChatExport{{Title: "report", Filename: "report.md", ContentType: "text/markdown", End: 4}}}, convID, "http-history", "run")
	if err := db.Create(&orm.ChatHistory{ID: "http-history", ConversationID: convID, RunStatus: "completed", Ext: withChatExports(nil, exports)}).Error; err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(CreateChatExportRequest{HistoryID: "http-history", ExportID: exports[0].ExportID, Filename: "report.md", ContentType: "text/markdown", Content: "body"})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/conversations/"+convID+"/artifacts", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"conversation_id": convID})
		recorder := httptest.NewRecorder()
		CreateConversationArtifact(recorder, req)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), exports[0].ExportID) {
			t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
		}
	}
	events, err := stateStore.LRange(context.Background(), convEventsKey(convID), 0, -1)
	if err != nil || len(events) != 1 || !strings.Contains(events[0], "artifact_created") {
		t.Fatalf("events=%v err=%v", events, err)
	}
}

func TestDualAnswerExportsRemainIndependent(t *testing.T) {
	db, stateStore := newRunDecisionStreamHarness(t)
	if err := db.AutoMigrate(&orm.MultiAnswersChatHistory{}); err != nil {
		t.Fatal(err)
	}
	snapshot := ChatExportSnapshot{Content: "body", Exports: []ChatExport{{Title: "report", Filename: "report.md", ContentType: "text/markdown", End: 4}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(algorithmFrame(t, map[string]any{"text": "body\n"}) + "\n"))
		_, _ = w.Write([]byte(algorithmFrame(t, map[string]any{"runtime_event": completedRunEvent(request.Conversation.RunID, true), "export_snapshot": snapshot}) + "\n"))
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	streamDualAnswer(context.Background(), context.Background(), recorder, recorder, db, stateStore, server.URL,
		map[string]any{"query": "q", "run_id": "first-run", "secondary_run_id": "second-run"}, "conv-stream-decision", "q", "first", "second", chatPersistTarget{Seq: 1}, nil)
	var histories []orm.MultiAnswersChatHistory
	if err := db.Find(&histories).Error; err != nil {
		t.Fatal(err)
	}
	if len(histories) != 2 {
		t.Fatalf("histories=%d", len(histories))
	}
	ids := map[string]bool{}
	for _, h := range histories {
		exports := chatExportsFromExt(h.Ext)
		if h.Result != "body" || len(exports) != 1 {
			t.Fatalf("history=%+v", h)
		}
		ids[exports[0].ExportID] = true
	}
	if len(ids) != 2 {
		t.Fatal("candidate exports share an identity")
	}
}

func TestChatExportFilenameValidation(t *testing.T) {
	for _, filename := range []string{"报告.md", "报告.pdf", "报告", "报告.MD", "../报告.md"} {
		t.Run(filename, func(t *testing.T) {
			valid := filename == "报告.md"
			entry := ChatExport{Title: "报告", Filename: filename, ContentType: "text/markdown", End: 4, ExportID: "00000000-0000-4000-8000-000000000001"}
			finalized := finalizeChatExports(&ChatExportSnapshot{Content: "body", Exports: []ChatExport{entry}}, "conv", "history", "run")
			if (len(finalized) == 1) != valid {
				t.Fatalf("finalized=%+v, valid=%v", finalized, valid)
			}

			// Seed matching metadata directly so rejection cannot be caused by a
			// request/metadata mismatch or by the finalization filter.
			db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{}, &orm.ConversationArtifact{})
			if err := db.Create(&orm.Conversation{ID: "conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.ChatHistory{ID: "history", ConversationID: "conv", RunStatus: "completed", Ext: withChatExports(nil, []ChatExport{entry})}).Error; err != nil {
				t.Fatal(err)
			}
			_, created, err := saveChatExport(context.Background(), db.DB, "owner", "conv", CreateChatExportRequest{
				HistoryID: "history", ExportID: entry.ExportID, Filename: filename, ContentType: entry.ContentType, Content: "submitted body",
			})
			if valid {
				if err != nil || !created {
					t.Fatalf("valid filename: created=%v err=%v", created, err)
				}
			} else if !errors.Is(err, errChatExportInvalid) || created {
				t.Fatalf("invalid filename: created=%v err=%v", created, err)
			}
			var count int64
			if err := db.Model(&orm.ConversationArtifact{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if (count == 1) != valid || count > 1 {
				t.Fatalf("artifact count=%d, valid=%v", count, valid)
			}
		})
	}
}
