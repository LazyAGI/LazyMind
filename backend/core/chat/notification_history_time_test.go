package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestNotificationHistoryEditsKeepOriginalCompletion(t *testing.T) {
	for _, mutation := range []string{"editable", "feedback", "dual_selection"} {
		t.Run(mutation, func(t *testing.T) {
			db := newChatNotificationTestDB(t).DB
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			completedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
			conversation := orm.Conversation{ID: "time-conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}
			if err := db.Create(&conversation).Error; err != nil {
				t.Fatal(err)
			}
			for _, task := range []orm.TaskCenterTask{
				{ID: "old-task", UserID: "owner", ConversationID: conversation.ID, TaskType: "background_chat", Status: "running", CreatedAt: completedAt.Add(-time.Minute)},
				{ID: "new-task", UserID: "owner", ConversationID: conversation.ID, TaskType: "background_chat", Status: "running", CreatedAt: completedAt.Add(time.Minute)},
			} {
				if task.ID == "old-task" && mutation == "dual_selection" {
					task.ProgressJSON, _ = json.Marshal(map[string]any{"_background_execution": map[string]any{"version": 1, "parts": []any{map[string]any{"history_id": "time-history", "run_id": "old-run-time-history"}, map[string]any{"history_id": "discarded-history", "run_id": "old-run-discarded-history"}}}})
				}
				if err := db.Create(&task).Error; err != nil {
					t.Fatal(err)
				}
			}
			history := orm.ChatHistory{ID: "time-history", ConversationID: conversation.ID, Seq: 1, RunID: "old-run", RunStatus: "completed", Result: "```editable\nold\n```", Ext: json.RawMessage(`{"keep":{"value":1}}`), TimeMixin: orm.TimeMixin{CreateTime: completedAt.Add(-time.Minute), UpdateTime: completedAt}}
			if mutation == "dual_selection" {
				for _, id := range []string{history.ID, "discarded-history"} {
					if err := db.Create(&orm.MultiAnswersChatHistory{ID: id, ConversationID: conversation.ID, Seq: history.Seq, RunID: "old-run-" + id, RunStatus: "completed", Result: history.Result, Ext: history.Ext, TimeMixin: history.TimeMixin}).Error; err != nil {
						t.Fatal(err)
					}
				}
			} else if err := db.Create(&history).Error; err != nil {
				t.Fatal(err)
			}
			prefs := historyTimePreferencesRequest(t, http.MethodGet, nil)
			historyTimePreferencesRequest(t, http.MethodPatch, map[string]any{"revision": prefs["revision"], "enabled": false, "confirm_running_task_ids": []string{"old-task", "new-task"}})
			prefs = historyTimePreferencesRequest(t, http.MethodGet, nil)
			historyTimePreferencesRequest(t, http.MethodPatch, map[string]any{"revision": prefs["revision"], "enabled": true})
			switch mutation {
			case "editable":
				historyTimeHandlerRequest(t, PatchEditableBlock, map[string]any{"conversation_id": conversation.ID, "history_id": history.ID, "base_content": "old", "content": "edited"})
			case "feedback":
				historyTimeHandlerRequest(t, FeedBackChatHistory, map[string]any{"history_id": history.ID, "type": "LIKE"})
			case "dual_selection":
				historyTimeHandlerRequest(t, SetChatHistory, map[string]any{"set_history_id": history.ID, "deleted_history_id": "discarded-history"})
			}
			if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
				t.Fatal(err)
			}
			var oldTask, newTask orm.TaskCenterTask
			if err := db.First(&oldTask, "id = ?", "old-task").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&newTask, "id = ?", "new-task").Error; err != nil {
				t.Fatal(err)
			}
			if newTask.Status != "running" || newTask.FinishedAt != nil {
				t.Fatalf("editing an old completion ended a newer task: %#v", newTask)
			}
			if oldTask.Status != "succeeded" || oldTask.FinishedAt == nil || !oldTask.FinishedAt.Equal(completedAt) {
				t.Fatalf("old task lost its original completion time: %#v want=%s", oldTask, completedAt)
			}
			var notice orm.TaskNotification
			if err := db.First(&notice, "task_id = ?", oldTask.ID).Error; err != nil {
				t.Fatal(err)
			}
			if notice.Status != "skipped" || notice.Reason != "NOTIFICATIONS_DISABLED" {
				t.Fatalf("closed-period completion replayed after %s: %#v", mutation, notice)
			}
			// A regenerated run on the same row must replace its old completion marker.
			if err := db.First(&history, "id = ?", history.ID).Error; err != nil {
				t.Fatal(err)
			}
			target := chatPersistTarget{HistoryID: history.ID, Seq: history.Seq, IsRegeneration: true, Existing: &history}
			if err := claimChatHistoryRun(t.Context(), db, history.ID, "new-run"); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"text":"new answer"}}`)
				_, _ = fmt.Fprintln(w, runFinishedFrame(t, "new-run"))
			}))
			defer server.Close()
			recorder := httptest.NewRecorder()
			streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, map[string]any{"query": "regenerate", "run_id": "new-run"}, conversation.ID, "regenerate", history.ID, target, history.Ext)
			if err := db.First(&newTask, "id = ?", newTask.ID).Error; err != nil {
				t.Fatal(err)
			}
			if newTask.Status != "succeeded" || newTask.FinishedAt == nil || !newTask.FinishedAt.After(completedAt) {
				t.Fatalf("regeneration did not finish new task: %#v", newTask)
			}
			notice = orm.TaskNotification{}
			if err := db.Where("task_id = ?", newTask.ID).Take(&notice).Error; err != nil {
				t.Fatal(err)
			}
			if notice.Status != "pending" {
				t.Fatalf("fresh regeneration notification was suppressed: %#v", notice)
			}
		})
	}
}

func historyTimeHandlerRequest(t *testing.T, handler http.HandlerFunc, body any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/history", bytes.NewReader(raw))
	req.Header.Set("X-User-Id", "owner")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("history mutation failed: %d %s", rec.Code, rec.Body.String())
	}
}

func historyTimePreferencesRequest(t *testing.T, method string, body any) map[string]any {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, "/notification-preferences", bytes.NewReader(raw))
	req.Header.Set("X-User-Id", "owner")
	rec := httptest.NewRecorder()
	taskcenter.NotificationPreferences(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preferences request failed: %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}

func TestNotificationHistoryTerminalReplayKeepsTimeForOwnedRun(t *testing.T) {
	for _, dual := range []bool{false, true} {
		t.Run(fmt.Sprint("dual=", dual), func(t *testing.T) {
			db := newChatNotificationTestDB(t).DB
			old := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
			if err := db.Create(&orm.Conversation{ID: "time-conv"}).Error; err != nil {
				t.Fatal(err)
			}
			history := orm.ChatHistory{ID: "time-history", ConversationID: "time-conv", RunID: "old-run", RunStatus: "completed", Ext: json.RawMessage(`{"keep":"value"}`), TimeMixin: orm.TimeMixin{CreateTime: old, UpdateTime: old}}
			if dual {
				if err := db.Create(&orm.MultiAnswersChatHistory{ID: history.ID, ConversationID: history.ConversationID, RunID: history.RunID, RunStatus: history.RunStatus, Ext: history.Ext, TimeMixin: history.TimeMixin}).Error; err != nil {
					t.Fatal(err)
				}
			} else if err := db.Create(&history).Error; err != nil {
				t.Fatal(err)
			}
			// Repeated terminal delivery uses the original terminal time even for legacy rows.
			update := map[string]any{"run_status": "completed", "update_time": time.Now().UTC()}
			var updated bool
			var err error
			if dual {
				updated, err = updateOwnedMultiAnswerHistory(t.Context(), db, history.ID, history.RunID, update)
			} else {
				updated, err = updateOwnedChatHistory(t.Context(), db, history.ID, history.RunID, update)
			}
			if err != nil || !updated {
				t.Fatalf("owned write: %v %v", updated, err)
			}
			if dual {
				var saved orm.MultiAnswersChatHistory
				if err := db.First(&saved, "id = ?", history.ID).Error; err != nil {
					t.Fatal(err)
				}
				history.Ext = saved.Ext
				history.UpdateTime = saved.UpdateTime
			} else if err := db.First(&history, "id = ?", history.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got := taskcenter.NotificationHistoryCompletedAt(history.CreateTime, history.UpdateTime, history.Ext); !got.Equal(old) {
				t.Fatalf("same-run replay shifted completion: %s want %s ext=%s", got, old, history.Ext)
			}
			// A stale run may neither replace the current owner's terminal nor its time.
			update = map[string]any{"run_status": "completed", "ext": json.RawMessage(`{"stale":true}`), "update_time": time.Now().UTC()}
			if dual {
				updated, err = updateOwnedMultiAnswerHistory(t.Context(), db, history.ID, "stale-run", update)
			} else {
				updated, err = updateOwnedChatHistory(t.Context(), db, history.ID, "stale-run", update)
			}
			if err != nil || updated {
				t.Fatalf("stale write accepted: %v %v", updated, err)
			}
		})
	}
}

func TestNotificationHistoryFeedbackDoesNotReplaceGeneratingMetadata(t *testing.T) {
	db := newChatNotificationTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.Create(&orm.Conversation{ID: "time-conv"}).Error; err != nil {
		t.Fatal(err)
	}
	history := orm.ChatHistory{ID: "time-history", ConversationID: "time-conv", RunID: "new-run", RunStatus: "generating", Ext: json.RawMessage(`{"new_run_field":"preserve"}`)}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	historyTimeHandlerRequest(t, FeedBackChatHistory, map[string]any{"history_id": history.ID, "type": "LIKE"})
	if err := db.First(&history, "id = ?", history.ID).Error; err != nil {
		t.Fatal(err)
	}
	if string(history.Ext) != `{"new_run_field":"preserve"}` {
		t.Fatalf("feedback rewrote active run metadata: %s", history.Ext)
	}
}

func TestNotificationHistoryFeedbackRechecksRunAfterRegenerationClaimsRow(t *testing.T) {
	db := newChatNotificationTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.Create(&orm.Conversation{ID: "time-conv"}).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	history := orm.ChatHistory{ID: "time-history", ConversationID: "time-conv", RunID: "old-run", RunStatus: "completed", Ext: json.RawMessage(`{"old_run_field":true}`), TimeMixin: orm.TimeMixin{CreateTime: old, UpdateTime: old}}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	read, resume := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(resume) })
	var reads atomic.Int32
	callback := "test:feedback-history-read"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "chat_histories" && reads.Add(1) == 1 {
			close(read)
			<-resume
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove(callback)
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/feedback", strings.NewReader(`{"history_id":"time-history","type":"LIKE"}`))
		rec := httptest.NewRecorder()
		FeedBackChatHistory(rec, req)
		response <- rec
	}()
	select {
	case <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("feedback did not read the old terminal")
	}
	if err := claimChatHistoryRun(t.Context(), db, history.ID, "new-run"); err != nil {
		t.Fatal(err)
	}
	newExt := json.RawMessage(`{"new_run_field":"preserve"}`)
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", history.ID).Update("ext", newExt).Error; err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(resume) })
	select {
	case rec := <-response:
		if rec.Code != http.StatusOK {
			t.Fatalf("feedback failed after run changed: %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("feedback did not resume")
	}
	if err := db.First(&history, "id = ?", history.ID).Error; err != nil {
		t.Fatal(err)
	}
	if history.RunID != "new-run" || history.RunStatus != "generating" || string(history.Ext) != string(newExt) {
		t.Fatalf("feedback overwrote the newer run: %#v", history)
	}
}

func TestNotificationHistoryImmediateTerminalReplayCannotFinishNewerTask(t *testing.T) {
	db := newChatNotificationTestDB(t).DB
	old := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if err := db.Create(&orm.Conversation{ID: "time-conv"}).Error; err != nil {
		t.Fatal(err)
	}
	history := orm.ChatHistory{ID: "time-history", ConversationID: "time-conv", Seq: 1, RunID: "old-run", RunStatus: "failed", TimeMixin: orm.TimeMixin{CreateTime: old, UpdateTime: old}}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.TaskCenterTask{ID: "new-task", UserID: "owner", ConversationID: history.ConversationID, TaskType: "background_chat", Status: "running", CreatedAt: old.Add(time.Minute)}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if !persistImmediateRunTerminal(t.Context(), db, history.ConversationID, history.ID, "question", history.RunID, chatPersistTarget{HistoryID: history.ID, Seq: 1}, history.Ext, &RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "upstream_request_failed"}) {
		t.Fatal("terminal replay did not persist")
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" || task.FinishedAt != nil {
		t.Fatalf("old-run terminal replay finished newer task: %#v", task)
	}
}

func TestNotificationHistoryNativeTerminalsPersistRunCompletion(t *testing.T) {
	for _, transport := range []string{"single", "nonstream", "dual", "immediate_failed", "prestream_failed"} {
		t.Run(transport, func(t *testing.T) {
			db := newChatNotificationTestDB(t).DB
			if err := db.Create(&orm.Conversation{ID: "time-conv"}).Error; err != nil {
				t.Fatal(err)
			}
			before := time.Now().UTC()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"text":"answer"}}`)
				_, _ = fmt.Fprintln(w, runFinishedFrame(t, request.Conversation.RunID))
			}))
			defer server.Close()
			recorder := httptest.NewRecorder()
			target := chatPersistTarget{HistoryID: "time-history", Seq: 1}
			ext := json.RawMessage(`{"keep":"value"}`)
			switch transport {
			case "single":
				streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, map[string]any{"query": "question", "run_id": "time-run"}, "time-conv", "question", target.HistoryID, target, ext)
			case "nonstream":
				handleNonStreamChat(recorder, t.Context(), db, nil, server.URL, map[string]any{"query": "question"}, "time-conv", "question", target, ext)
			case "dual":
				streamDualAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, map[string]any{"query": "question", "run_id": "time-run", "secondary_run_id": "second-run"}, "time-conv", "question", target.HistoryID, "second-history", target, ext)
			case "immediate_failed":
				if !persistImmediateRunTerminal(t.Context(), db, "time-conv", target.HistoryID, "question", "time-run", target, ext, &RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "upstream_request_failed"}) {
					t.Fatal("immediate terminal not persisted")
				}
			case "prestream_failed":
				if err := registerForkableHistoryRun(t.Context(), db, "time-conv", target.HistoryID, "time-run", "question", target, ext); err != nil {
					t.Fatal(err)
				}
				// This is the same terminal update used when cache setup fails before transport starts.
				if ok, err := updateOwnedChatHistory(t.Context(), db, target.HistoryID, "time-run", map[string]any{"run_status": "failed", "update_time": time.Now().UTC()}); err != nil || !ok {
					t.Fatalf("prestream failure: %v %v", ok, err)
				}
			}
			var histories []orm.ChatHistory
			if transport == "dual" {
				var dual []orm.MultiAnswersChatHistory
				if err := db.Find(&dual).Error; err != nil {
					t.Fatal(err)
				}
				for _, h := range dual {
					histories = append(histories, orm.ChatHistory{RunID: h.RunID, RunStatus: h.RunStatus, Ext: h.Ext, TimeMixin: h.TimeMixin})
				}
			} else if err := db.Find(&histories).Error; err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if transport == "dual" {
				wantCount = 2
			}
			if len(histories) != wantCount {
				t.Fatalf("terminal histories=%d want=%d response=%s", len(histories), wantCount, recorder.Body.String())
			}
			for _, h := range histories {
				var fields struct {
					Keep       string `json:"keep"`
					Completion struct {
						CompletedAt time.Time `json:"completed_at"`
						RunID       string    `json:"run_id"`
					} `json:"_notification_completion"`
				}
				if err := json.Unmarshal(h.Ext, &fields); err != nil {
					t.Fatal(err)
				}
				if !terminalHistoryStatus(h.RunStatus) || fields.Keep != "value" || fields.Completion.RunID != h.RunID || fields.Completion.CompletedAt.Before(before) || fields.Completion.CompletedAt.After(time.Now().UTC()) {
					t.Fatalf("terminal completion metadata missing or wrong: %#v ext=%s", h, h.Ext)
				}
			}
		})
	}
}
