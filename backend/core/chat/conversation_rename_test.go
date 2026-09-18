package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/externalcontext"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
)

func renameConversationRequest(id, user, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/core/conversations/"+id+"/title", strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"name": id})
	req.Header.Set("X-User-Id", user)
	rec := httptest.NewRecorder()
	RenameConversation(rec, req)
	return rec
}

func TestRenameConversationPersistsAcrossViewsWithoutChangingActivity(t *testing.T) {
	for _, work := range []bool{false, true} {
		t.Run(fmt.Sprintf("work=%t", work), func(t *testing.T) {
			db := newPromptTestDB(t).DB
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			before := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
			rank := int64(7)
			conv := orm.Conversation{ID: "rename", DisplayName: "old-conversation-title", IsTaskConv: work, TitleSource: "auto", PinnedAt: &before, HistoryOrder: &rank,
				BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: before, UpdatedAt: before}}
			if err := db.Create(&conv).Error; err != nil {
				t.Fatal(err)
			}
			if err := taskcenter.CreateTask(context.Background(), db, &orm.TaskCenterTask{UserID: "u1", ConversationID: conv.ID, Title: &conv.DisplayName, TaskType: "background_chat", Status: "succeeded"}); err != nil {
				t.Fatal(err)
			}
			res := renameConversationRequest(conv.ID, "u1", `{"display_name":"  新会话名称  ","title_revision":0}`)
			if res.Code != http.StatusOK {
				t.Fatalf("rename: %d %s", res.Code, res.Body.String())
			}
			var after orm.Conversation
			if err := db.First(&after, "id = ?", conv.ID).Error; err != nil {
				t.Fatal(err)
			}
			if after.DisplayName != "新会话名称" || after.TitleSource != "user" || after.TitleRevision != 1 || !after.CreatedAt.Equal(before) || !after.UpdatedAt.Equal(before) || !after.PinnedAt.Equal(before) || *after.HistoryOrder != rank {
				t.Fatalf("rename changed unexpected fields: %+v", after)
			}
			detailReq := httptest.NewRequest(http.MethodGet, "/api/core/conversations/rename:detail", nil)
			detailReq = mux.SetURLVars(detailReq, map[string]string{"name": conv.ID})
			detailReq.Header.Set("X-User-Id", "u1")
			detailRec := httptest.NewRecorder()
			GetConversationDetail(detailRec, detailReq)
			var detail struct {
				Conversation struct {
					DisplayName string `json:"display_name"`
					Revision    int64  `json:"title_revision"`
				} `json:"conversation"`
			}
			if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil || detailRec.Code != http.StatusOK || detail.Conversation.DisplayName != "新会话名称" || detail.Conversation.Revision != 1 {
				t.Fatalf("persisted detail: %s %v", detailRec.Body.String(), err)
			}
			// New HTTP requests use persisted data, without relying on browser state.
			for _, keyword := range []string{"新会话名称", "old-conversation-title"} {
				for _, handler := range []struct {
					name, path string
					fn         http.HandlerFunc
				}{
					{"history", "/api/core/conversations?keyword=", ListConversations},
					{"tasks", "/task-center/tasks?keyword=", taskcenter.ListTasks},
				} {
					req := httptest.NewRequest(http.MethodGet, handler.path+url.QueryEscape(keyword), nil)
					req.Header.Set("X-User-Id", "u1")
					rec := httptest.NewRecorder()
					handler.fn(rec, req)
					if rec.Code != http.StatusOK {
						t.Fatalf("%s: %d %s", handler.name, rec.Code, rec.Body.String())
					}
					var payload struct {
						Total     int `json:"total"`
						TotalSize int `json:"total_size"`
						Data      struct {
							Total int `json:"total"`
						} `json:"data"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					total := payload.Total + payload.TotalSize + payload.Data.Total
					want := 0
					if keyword == "新会话名称" {
						want = 1
					}
					if total != want {
						t.Fatalf("%s keyword=%s: %s", handler.name, keyword, rec.Body.String())
					}
				}
			}
			if rec := renameConversationRequest(conv.ID, "u1", `{"display_name":"过时修改","title_revision":0}`); rec.Code != http.StatusConflict {
				t.Fatalf("stale rename: %d", rec.Code)
			}
		})
	}
}

func TestRenameConversationValidatesInputAndOwnership(t *testing.T) {
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.Create(&orm.Conversation{ID: "rename", DisplayName: "Original", BaseModel: orm.BaseModel{CreateUserID: "u1"}}).Error; err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{`, `{"display_name":"  ","title_revision":0}`, `{"display_name":"New"}`, `{"display_name":"New","title_revision":null}`, `{"display_name":"New","title_revision":-1}`, fmt.Sprintf(`{"display_name":%q,"title_revision":0}`, strings.Repeat("字", 256))} {
		if rec := renameConversationRequest("rename", "u1", body); rec.Code != http.StatusBadRequest {
			t.Errorf("invalid request accepted: %s: %d", body, rec.Code)
		}
	}
	for _, user := range []string{"u2", ""} {
		if rec := renameConversationRequest("rename", user, `{"display_name":"Unauthorized","title_revision":0}`); rec.Code < 400 {
			t.Errorf("owner check: %q %d", user, rec.Code)
		}
	}
	var original orm.Conversation
	db.First(&original, "id = ?", "rename")
	if original.DisplayName != "Original" || original.TitleRevision != 0 {
		t.Fatalf("invalid request changed record: %+v", original)
	}
	if rec := renameConversationRequest("rename", "u1", fmt.Sprintf(`{"display_name":%q,"title_revision":0}`, "  "+strings.Repeat("😀", 255)+"  ")); rec.Code != http.StatusOK {
		t.Fatalf("unicode boundary: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRenameConversationSurvivesExternalSyncAndProjectCatalog(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	ctx := context.Background()
	service := externalcontext.New(db)
	session := externalcontext.NativeSession{ThreadID: "renamed-thread", ProjectKey: "project", ProjectName: "Project", DisplayName: "Original", NativeUpdated: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), Turns: []externalcontext.NativeTurn{{ID: "turn", User: "Question", Assistant: "Answer"}}}
	if _, err := service.SyncSessionCatalog(ctx, "u1", "codex", "host-1", []externalcontext.NativeSession{session}, true); err != nil {
		t.Fatal(err)
	}
	binding, err := service.BindNativeSession(ctx, "u1", "codex", "host-1", session.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := renameConversationRequest(binding.ConversationID, "u1", `{"display_name":"人工标题","title_revision":0}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	session.DisplayName = "Changed native title"
	if _, err := service.SyncSessionCatalog(ctx, "u1", "codex", "host-1", []externalcontext.NativeSession{session}, false); err != nil {
		t.Fatal(err)
	}
	var after orm.Conversation
	db.First(&after, "id = ?", binding.ConversationID)
	if after.DisplayName != "人工标题" || after.TitleSource != "user" {
		t.Fatalf("sync overwrote manual title: %+v", after)
	}
	page, err := service.ListNativeSessions(ctx, "u1", "codex", 0, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].DisplayName != "人工标题" {
		t.Fatalf("project catalog: %+v %v", page, err)
	}
}
