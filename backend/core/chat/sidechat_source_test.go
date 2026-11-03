package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestUpdateSidechatSourcePreservesConversationAndHistory(t *testing.T) {
	for _, retained := range []bool{false, true} {
		name := "ephemeral"
		if retained {
			name = "retained"
		}
		t.Run(name, func(t *testing.T) {
			db := newSidechatTestDB(t)
			parent := sidechatTestConversation(t, db, "update-source-parent", "user-1", "Parent")
			first := sidechatTestHistory(t, db, "update-first", parent.ID, 1, "first source question", "first source answer", "completed")
			second := sidechatTestHistory(t, db, "update-second", parent.ID, 2, "second source question", "second source answer", "completed")
			child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{
				SourceHistoryID: first.ID, SelectedText: "first quote",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(child).Updates(map[string]any{
				"display_name": "Existing side conversation", "thinking_depth": "max",
				"search_config":       json.RawMessage(`{"dataset_list":[{"id":"preserved-kb"}]}`),
				"chat_model_mode":     "fixed",
				"chat_model_id":       "preserved-model",
				"chat_model_version":  7,
				"chat_model_snapshot": json.RawMessage(`{"model_id":"preserved-model"}`),
			}).Error; err != nil {
				t.Fatal(err)
			}
			childTurn := sidechatTestHistory(t, db, "preserved-child-turn", child.ID, 1, "existing child question", "existing child answer", "completed")
			if retained {
				if err := db.Model(child).Updates(map[string]any{"is_ephemeral": false, "ephemeral_expires_at": nil}).Error; err != nil {
					t.Fatal(err)
				}
			}
			var before orm.Conversation
			if err := db.First(&before, "id = ?", child.ID).Error; err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			UpdateSidechatSource(recorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+child.ID+"/sidechat", "user-1",
				`{"source_history_id":"update-second","source_seq":2,"selected_text":"second quote"}`, map[string]string{"child_id": child.ID}))
			if recorder.Code != http.StatusOK {
				t.Fatalf("update status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var payload struct {
				Conversation struct {
					ID           string `json:"id"`
					SelectedText string `json:"selected_text"`
					IsEphemeral  bool   `json:"is_ephemeral"`
				} `json:"conversation"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || payload.Conversation.ID != child.ID || payload.Conversation.SelectedText != "second quote" || payload.Conversation.IsEphemeral == retained {
				t.Fatalf("invalid source update response: %s err=%v", recorder.Body.String(), err)
			}
			var after orm.Conversation
			if err := db.First(&after, "id = ?", child.ID).Error; err != nil {
				t.Fatal(err)
			}
			if after.SourceHistoryID == nil || *after.SourceHistoryID != second.ID || after.SourceSeq == nil || *after.SourceSeq != 2 || after.SourceSelectedText != "second quote" {
				t.Fatalf("source fields not updated: %#v", after)
			}
			before.SourceHistoryID, before.SourceSeq = after.SourceHistoryID, after.SourceSeq
			before.SourceSelectedText, before.SourceContext, before.UpdatedAt = after.SourceSelectedText, after.SourceContext, after.UpdatedAt
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source update changed other conversation fields")
			}
			var histories []orm.ChatHistory
			if err := db.Where("conversation_id = ?", child.ID).Find(&histories).Error; err != nil || len(histories) != 1 || histories[0].ID != childTurn.ID || histories[0].RawContent != childTurn.RawContent || histories[0].Result != childTurn.Result {
				t.Fatalf("source update changed child history: %#v err=%v", histories, err)
			}
			body := buildChatRequestBody(context.Background(), db, child.ID, "session", "next child question", histories, map[string]any{}, nil, "user-1", 2)
			if err := applyConversationSourceRuntimeContext(context.Background(), db, "user-1", body); err != nil {
				t.Fatal(err)
			}
			if runtime := buildLazyChatRequest(body).Runtime; runtime.SourceReference != "second quote" || runtime.ToolPolicy != "sidechat_readonly" {
				t.Fatalf("updated source did not reach model runtime: %#v", runtime)
			}
			encoded, _ := json.Marshal(body["history"])
			for _, content := range []string{"second source question", "second source answer", "existing child question", "existing child answer"} {
				if !strings.Contains(string(encoded), content) {
					t.Fatalf("model history missing %q: %s", content, encoded)
				}
			}
			var count int64
			if err := db.Model(&orm.Conversation{}).Count(&count).Error; err != nil || count != 2 {
				t.Fatalf("source update replaced the child: count=%d err=%v", count, err)
			}
		})
	}
}

func TestUpdateSidechatSourceRejectsInvalidOwnerAndSource(t *testing.T) {
	db := newSidechatTestDB(t)
	parent := sidechatTestConversation(t, db, "source-validation-parent", "user-1", "Parent")
	first := sidechatTestHistory(t, db, "valid-source", parent.ID, 1, "source question", "source answer", "completed")
	sidechatTestHistory(t, db, "running-source", parent.ID, 2, "running question", "partial answer", "running")
	other := sidechatTestConversation(t, db, "other-source-parent", "user-1", "Other parent")
	sidechatTestHistory(t, db, "other-parent-source", other.ID, 1, "other question", "other answer", "completed")
	child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{SourceHistoryID: first.ID, SelectedText: "original quote"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, userID, childID, body string
		wantStatus                  int
	}{
		{"wrong owner", "user-2", child.ID, `{"selected_text":"replacement"}`, http.StatusNotFound},
		{"ordinary conversation", "user-1", parent.ID, `{"selected_text":"replacement"}`, http.StatusNotFound},
		{"source from another parent", "user-1", child.ID, `{"source_history_id":"other-parent-source","selected_text":"replacement"}`, http.StatusBadRequest},
		{"mismatched sequence", "user-1", child.ID, `{"source_history_id":"valid-source","source_seq":2}`, http.StatusBadRequest},
		{"unsettled source without excerpt", "user-1", child.ID, `{"source_history_id":"running-source"}`, http.StatusConflict},
		{"invalid sequence", "user-1", child.ID, `{"source_seq":0}`, http.StatusBadRequest},
		{"settings cannot be updated", "user-1", child.ID, `{"thinking_depth":"low"}`, http.StatusBadRequest},
		{"unknown field", "user-1", child.ID, `{"source_context":[]}`, http.StatusBadRequest},
		{"oversized excerpt", "user-1", child.ID, `{"selected_text":"` + strings.Repeat("字", maxSidechatSelectedTextRunes+1) + `"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			UpdateSidechatSource(recorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+tc.childID+"/sidechat", tc.userID, tc.body, map[string]string{"child_id": tc.childID}))
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			var unchanged orm.Conversation
			if err := db.First(&unchanged, "id = ?", child.ID).Error; err != nil || unchanged.SourceSelectedText != "original quote" || unchanged.SourceHistoryID == nil || *unchanged.SourceHistoryID != first.ID {
				t.Fatalf("rejected update changed source: %#v err=%v", unchanged, err)
			}
		})
	}
}

func TestUpdateSidechatSourceRejectsActiveRequestGuard(t *testing.T) {
	db := newSidechatTestDB(t)
	parent := sidechatTestConversation(t, db, "source-busy-parent", "user-1", "Parent")
	sidechatTestHistory(t, db, "source-busy-history", parent.ID, 1, "source question", "source answer", "completed")
	child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{SelectedText: "original quote"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := acquireSidechatNextRequestGuard(context.Background(), store.State(), child.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	recorder := httptest.NewRecorder()
	UpdateSidechatSource(recorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+child.ID+"/sidechat", "user-1", `{"selected_text":"new quote"}`, map[string]string{"child_id": child.ID}))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("busy update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	guard.Release()
	recorder = httptest.NewRecorder()
	UpdateSidechatSource(recorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+child.ID+"/sidechat", "user-1", `{"selected_text":"new quote"}`, map[string]string{"child_id": child.ID}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("idle update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateSidechatSourceValidatesInheritedAttachments(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name, uploadOwner, wantStatus := "foreign", "user-2", http.StatusForbidden
		if missing {
			name, uploadOwner, wantStatus = "missing", "user-1", http.StatusConflict
		}
		t.Run(name, func(t *testing.T) {
			db := newSidechatTestDB(t)
			uploadRoot := t.TempDir()
			t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
			parent := sidechatTestConversation(t, db, "source-file-parent", "user-1", "Parent")
			first := sidechatTestHistory(t, db, "source-file-first", parent.ID, 1, "first question", "first answer", "completed")
			child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{SourceHistoryID: first.ID, SelectedText: "original quote"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(uploadRoot, "tmp", "users", uploadOwner, "files", "source-file", "source.pdf")
			sidechatTestUploadedFile(t, db, "source-file", uploadOwner, "", path)
			if missing {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			second := sidechatTestHistory(t, db, "source-file-second", parent.ID, 2, "second question", "second answer", "completed")
			ext, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"input_type": "file", "uri": path}}})
			if err := db.Model(&second).Update("ext", json.RawMessage(ext)).Error; err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			UpdateSidechatSource(recorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+child.ID+"/sidechat", "user-1", `{"source_history_id":"source-file-second","selected_text":"new quote"}`, map[string]string{"child_id": child.ID}))
			if recorder.Code != wantStatus {
				t.Fatalf("attachment update status=%d want=%d body=%s", recorder.Code, wantStatus, recorder.Body.String())
			}
			var unchanged orm.Conversation
			if err := db.First(&unchanged, "id = ?", child.ID).Error; err != nil || unchanged.SourceSelectedText != "original quote" {
				t.Fatalf("invalid attachment update changed source: %#v err=%v", unchanged, err)
			}
		})
	}
}
