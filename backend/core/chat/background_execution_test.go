package chat

import (
	"encoding/json"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func scopedBackgroundTestTask(t *testing.T, db *gorm.DB, id, historyID, runID string) orm.TaskCenterTask {
	t.Helper()
	progress, _ := json.Marshal(map[string]any{"keep": "preserved", "_background_execution": map[string]any{"version": 1, "parts": []any{map[string]any{"history_id": historyID, "run_id": runID}}}})
	task := orm.TaskCenterTask{ID: id, UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", ProgressJSON: progress}
	if err := taskcenter.CreateTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	return task
}

func rejectBackgroundExecutionNotices(t *testing.T, db *gorm.DB) func() {
	t.Helper()
	trigger := `CREATE TRIGGER reject_execution_notice BEFORE INSERT ON task_notifications BEGIN SELECT RAISE(ABORT, 'synthetic notification storage failure'); END`
	if db.Dialector.Name() == "postgres" {
		if err := db.Exec(`CREATE FUNCTION reject_execution_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic notification storage failure'; END $$`).Error; err != nil {
			t.Fatal(err)
		}
		trigger = `CREATE TRIGGER reject_execution_notice BEFORE INSERT ON task_notifications FOR EACH ROW EXECUTE FUNCTION reject_execution_notice()`
	}
	if err := db.Exec(trigger).Error; err != nil {
		t.Fatal(err)
	}
	return func() {
		drop := "DROP TRIGGER reject_execution_notice"
		if db.Dialector.Name() == "postgres" {
			drop += " ON task_notifications"
		}
		if err := db.Exec(drop).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackgroundExecutionRetainsFailureAcrossLaterRuns(t *testing.T) {
	for _, executor := range []string{"native", "external"} {
		for _, next := range []string{"background", "foreground", "regenerate"} {
			t.Run(executor+"/"+next, func(t *testing.T) {
				_, db := newExternalChatTestApplication(t)
				first := scopedBackgroundTestTask(t, db, "first", "history", "run")
				restore := rejectBackgroundExecutionNotices(t, db)
				finishBackgroundNotificationTest(t, db, executor, "failed", time.Now().UTC())
				var history orm.ChatHistory
				if err := db.First(&history, "id = ?", "history").Error; err != nil {
					t.Fatal(err)
				}
				originalAt := taskcenter.NotificationHistoryCompletedAt(history.CreateTime, history.UpdateTime, history.Ext)
				if err := db.First(&first, "id = ?", first.ID).Error; err != nil {
					t.Fatal(err)
				}
				if first.Status != "running" {
					t.Fatalf("projection must roll back: %s", first.Status)
				}
				var progress map[string]any
				if err := json.Unmarshal(first.ProgressJSON, &progress); err != nil {
					t.Fatal(err)
				}
				scope := progress["_background_execution"].(map[string]any)
				part := scope["parts"].([]any)[0].(map[string]any)
				if part["status"] != "failed" || part["completed_at"] == nil {
					t.Errorf("terminal evidence lost on notice rejection: %s", first.ProgressJSON)
				}
				if progress["keep"] != "preserved" {
					t.Fatal("unrelated progress lost")
				}
				restore()
				nextID := "next-history"
				target := chatPersistTarget{Seq: 2}
				if next == "regenerate" {
					nextID = "history"
					target = chatPersistTarget{Seq: 1, IsRegeneration: true, Existing: &history}
				}
				if next == "background" {
					scopedBackgroundTestTask(t, db, "second", nextID, "next-run")
				}
				if err := registerForkableHistoryRun(t.Context(), db, first.ConversationID, nextID, "next-run", "next", target, nil); err != nil {
					t.Fatal(err)
				}
				if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
					t.Fatal(err)
				}
				if err := db.First(&first, "id = ?", first.ID).Error; err != nil {
					t.Fatal(err)
				}
				if first.Status != "failed" || first.FinishedAt == nil || !first.FinishedAt.Equal(originalAt) {
					t.Fatalf("later generating run changed original failure/time: %#v (want %v)", first, originalAt)
				}
				if !persistImmediateRunTerminal(t.Context(), db, first.ConversationID, nextID, "next", "next-run", target, nil, &RunTerminal{Status: "completed", Reason: "normal"}) {
					t.Fatal("next terminal not persisted")
				}
				var notices []orm.TaskNotification
				if err := db.Where("task_id = ?", first.ID).Find(&notices).Error; err != nil {
					t.Fatal(err)
				}
				if len(notices) != 1 || notices[0].Event != "failed" {
					t.Fatalf("original failure notice changed: %#v", notices)
				}
				if next == "background" {
					var second orm.TaskCenterTask
					if err := db.First(&second, "id = ?", "second").Error; err != nil {
						t.Fatal(err)
					}
					if second.Status != "succeeded" {
						t.Fatalf("next execution did not independently succeed: %#v", second)
					}
				}
			})
		}
	}
}

func TestBackgroundExecutionExternalReplayAdmission(t *testing.T) {
	for _, origin := range []string{"background", "foreground"} {
		t.Run(origin, func(t *testing.T) {
			app, db := newExternalChatTestApplication(t)
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			t.Chdir(t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "tool_groups": []any{}, "data": map[string]any{"items": []any{}}})
			}))
			defer server.Close()
			for _, key := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
				t.Setenv(key, server.URL)
			}
			if err := db.Model(&orm.Conversation{}).Where("id = ?", "conversation-1").Updates(map[string]any{"chat_executor": ChatExecutorCodex, "is_task_conv": true}).Error; err != nil {
				t.Fatal(err)
			}
			requestKey := externalChatRequestKey("user-1", "same-request")
			runID, historyID := externalChatIdentity(requestKey)
			request := func() *httptest.ResponseRecorder {
				req := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{"conversation_id":"conversation-1","query":"question","stream":true,"run_in_background":true}`, nil)
				req.Header.Set("Idempotency-Key", "same-request")
				response := httptest.NewRecorder()
				ChatConversations(response, req)
				return response
			}
			if origin == "background" {
				if err := app.reportHost(t.Context(), "user-1", ChatExecutorCodex, "host", true, true, ""); err != nil {
					t.Fatal(err)
				}
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- request() }()
				deadline := time.Now().Add(5 * time.Second)
				var lease string
				for time.Now().Before(deadline) {
					job, err := app.claim(t.Context(), "user-1", ChatExecutorCodex, "host")
					if err != nil {
						t.Fatal(err)
					}
					if job != nil {
						lease = job.LeaseToken
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if lease == "" {
					select {
					case rec := <-done:
						t.Fatalf("initial admission ended before run: %d %s", rec.Code, rec.Body.String())
					default:
						t.Fatal("initial run not admitted")
					}
				}
				for _, event := range []externalChatEvent{{EventID: "message", Type: "message", Text: "answer"}, {EventID: "terminal", Type: "completed"}} {
					if _, err := app.appendEvent(t.Context(), "user-1", runID, "host", lease, event); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case rec := <-done:
					if rec.Code != http.StatusOK {
						t.Fatalf("initial admission: %d %s", rec.Code, rec.Body.String())
					}
				case <-time.After(5 * time.Second):
					t.Fatal("initial stream did not finish")
				}
			} else {
				now := time.Now().UTC()
				if err := db.Create(&orm.ExternalChatRun{ID: runID, RequestID: requestKey, HistoryID: historyID, ConversationID: "conversation-1", Provider: ChatExecutorCodex, ActorUserID: "user-1", Query: "question", Sequence: 1, Status: "completed", HistoryExt: json.RawMessage(`{}`)}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.ChatHistory{ID: historyID, ConversationID: "conversation-1", Seq: 1, RunID: runID, RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				rec := request()
				if rec.Code != http.StatusOK {
					t.Fatalf("replay admission: %d %s", rec.Code, rec.Body.String())
				}
			}
			var tasks []orm.TaskCenterTask
			if err := db.Where("conversation_id = ?", "conversation-1").Find(&tasks).Error; err != nil {
				t.Fatal(err)
			}
			var notices []orm.TaskNotification
			if err := db.Find(&notices).Error; err != nil {
				t.Fatal(err)
			}
			if origin == "foreground" {
				if len(tasks) != 0 || len(notices) != 0 {
					t.Fatalf("replay promoted old foreground execution into a stranded task: %#v %#v", tasks, notices)
				}
				return
			}
			if len(tasks) != 1 || len(tasks[0].ID) > 36 || tasks[0].Status != "succeeded" || tasks[0].FinishedAt == nil || len(notices) != 1 {
				t.Fatalf("replay duplicated/stranded completion or violated schema: tasks=%#v notices=%#v", tasks, notices)
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", historyID).Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			if !tasks[0].FinishedAt.Equal(taskcenter.NotificationHistoryCompletedAt(history.CreateTime, history.UpdateTime, history.Ext)) {
				t.Fatal("replay changed original terminal time")
			}
		})
	}
}

func TestBackgroundExecutionTrustedNativeAdmission(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Chdir(t.TempDir())
	foreign := orm.TaskCenterTask{ID: "foreign", UserID: "other", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", ProgressJSON: taskcenter.NewBackgroundExecutionProgress(nil)}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/stream" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "tool_groups": []any{}, "data": map[string]any{"items": []any{}}})
			return
		}
		var payload LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"text": "answer"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(payload.Conversation.RunID, true)}})
	}))
	defer server.Close()
	for _, key := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
		t.Setenv(key, server.URL)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "conversation-1").Update("is_task_conv", true).Error; err != nil {
		t.Fatal(err)
	}
	req := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{"conversation_id":"conversation-1","query":"question","stream":true,"run_in_background":true,"_background_task_id":"foreign","background_task_id":"foreign"}`, nil)
	response := httptest.NewRecorder()
	ChatConversations(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("native admission: %d %s", response.Code, response.Body.String())
	}
	var own orm.TaskCenterTask
	if err := db.Where("user_id = ?", "user-1").Take(&own).Error; err != nil {
		t.Fatal(err)
	}
	var history orm.ChatHistory
	if err := db.Where("conversation_id = ?", "conversation-1").Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	var progress struct {
		Execution struct {
			Parts []taskcenter.BackgroundExecutionPart `json:"parts"`
		} `json:"_background_execution"`
	}
	if err := json.Unmarshal(own.ProgressJSON, &progress); err != nil {
		t.Fatal(err)
	}
	if own.Status != "succeeded" || len(progress.Execution.Parts) != 1 || progress.Execution.Parts[0].HistoryID != history.ID || progress.Execution.Parts[0].RunID != history.RunID {
		t.Fatalf("admission did not bind own execution: %#v %s", own, own.ProgressJSON)
	}
	if err := db.First(&foreign, "id = ?", foreign.ID).Error; err != nil {
		t.Fatal(err)
	}
	if foreign.Status != "running" || string(foreign.ProgressJSON) != string(taskcenter.NewBackgroundExecutionProgress(nil)) {
		t.Fatalf("client bound foreign task: %#v", foreign)
	}
}

func TestBackgroundExecutionDualRegeneratedPrimary(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	completedAt := time.Date(2026, time.October, 19, 8, 0, 0, 123456789, time.UTC)
	old := orm.ChatHistory{ID: "history", ConversationID: "conversation-1", Seq: 1, RunID: "old", RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: completedAt, UpdateTime: completedAt}}
	oldProgress, _ := json.Marshal(map[string]any{"_background_execution": map[string]any{"version": 1, "parts": []any{map[string]any{"history_id": old.ID, "run_id": old.RunID}}}})
	previousTask := orm.TaskCenterTask{ID: "previous", UserID: "user-1", ConversationID: old.ConversationID, TaskType: "background_chat", Status: "running", CreatedAt: old.CreateTime.Add(-time.Minute), ProgressJSON: oldProgress}
	if err := db.Create(&previousTask).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	// Compare against the stored timestamp: SQLite preserves nanoseconds,
	// while PostgreSQL stores timestamps at microsecond precision.
	if err := db.First(&old, "id = ?", old.ID).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.TaskCenterTask{ID: "dual", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", ProgressJSON: taskcenter.NewBackgroundExecutionProgress(nil)}
	if err := taskcenter.CreateTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"text": "answer"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(payload.Conversation.RunID, true)}})
	}))
	defer server.Close()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat", nil)
	handleStreamChat(rec, req, db, nil, server.URL, map[string]any{"query": "question", "run_id": "primary"}, old.ConversationID, "question", chatPersistTarget{HistoryID: old.ID, Seq: 1, Existing: &old, IsRegeneration: true, BackgroundTaskID: task.ID}, true, json.RawMessage(`{}`))
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "succeeded" || task.FinishedAt == nil {
		t.Fatalf("dual primary mirrored in single table stranded task: %#v response=%d %s", task, rec.Code, rec.Body.String())
	}
	var notices []orm.TaskNotification
	if err := db.Where("task_id = ?", task.ID).Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Fatalf("dual completion notices=%#v", notices)
	}
	if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&previousTask, "id = ?", previousTask.ID).Error; err != nil {
		t.Fatal(err)
	}
	if previousTask.Status != "succeeded" || previousTask.FinishedAt == nil || !previousTask.FinishedAt.Equal(old.UpdateTime) {
		t.Fatalf("dual takeover erased previous execution evidence: %#v", previousTask)
	}
}

func TestBackgroundExecutionDualWaitsForOwnParts(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	progress, _ := json.Marshal(map[string]any{"keep": "yes", "_background_execution": map[string]any{"version": 1, "parts": []any{map[string]any{"history_id": "primary", "run_id": "primary-run"}, map[string]any{"history_id": "secondary", "run_id": "secondary-run"}}}})
	task := orm.TaskCenterTask{ID: "dual", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", ProgressJSON: progress}
	if err := taskcenter.CreateTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	firstAt := time.Now().UTC().Truncate(time.Microsecond)
	secondAt := firstAt.Add(time.Second)
	for _, h := range []orm.MultiAnswersChatHistory{
		{ID: "primary", ConversationID: task.ConversationID, Seq: 1, RunID: "primary-run", RunStatus: "generating", TimeMixin: orm.TimeMixin{CreateTime: firstAt, UpdateTime: firstAt}},
		{ID: "secondary", ConversationID: task.ConversationID, Seq: 1, RunID: "secondary-run", RunStatus: "generating", TimeMixin: orm.TimeMixin{CreateTime: firstAt, UpdateTime: firstAt}},
		{ID: "foreign", ConversationID: task.ConversationID, Seq: 1, RunID: "foreign-run", RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: secondAt, UpdateTime: secondAt}},
	} {
		if err := db.Create(&h).Error; err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := updateOwnedMultiAnswerHistory(t.Context(), db, "primary", "primary-run", map[string]any{"run_status": "failed", "update_time": firstAt}); err != nil || !ok {
		t.Fatalf("primary: %v %v", ok, err)
	}
	if err := taskcenter.FinalizeBackgroundExecution(t.Context(), db, task.ConversationID, "foreign", "foreign-run"); err != nil {
		t.Fatal(err)
	}
	if ok, err := updateOwnedMultiAnswerHistory(t.Context(), db, "secondary", "stale-run", map[string]any{"run_status": "completed", "update_time": secondAt}); err != nil || ok {
		t.Fatalf("stale secondary accepted: %v %v", ok, err)
	}
	if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" {
		t.Fatalf("foreign part completed dual: %#v", task)
	}
	restore := rejectBackgroundExecutionNotices(t, db)
	if ok, err := updateOwnedMultiAnswerHistory(t.Context(), db, "secondary", "secondary-run", map[string]any{"run_status": "cancelled", "update_time": secondAt}); err != nil || !ok {
		t.Fatalf("secondary: %v %v", ok, err)
	}
	if err := taskcenter.FinalizeBackgroundExecution(t.Context(), db, task.ConversationID, "primary", "primary-run"); err == nil {
		t.Fatal("notice outage hidden")
	}
	restore()
	// Same-owner terminal replay cannot replace either accepted result or time.
	if ok, err := updateOwnedMultiAnswerHistory(t.Context(), db, "primary", "primary-run", map[string]any{"run_status": "completed", "update_time": secondAt.Add(time.Hour)}); err != nil || !ok {
		t.Fatalf("replay: %v %v", ok, err)
	}
	if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "failed" || task.FinishedAt == nil || !task.FinishedAt.Equal(secondAt) {
		t.Fatalf("dual lost failure precedence/last own part time: %#v want %v", task, secondAt)
	}
	var notices []orm.TaskNotification
	if err := db.Where("task_id = ?", task.ID).Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Event != "failed" {
		t.Fatalf("dual notices: %#v", notices)
	}
}

func TestBackgroundExecutionGateUsesOriginalCompletion(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	task := scopedBackgroundTestTask(t, db, "first", "history", "run")
	restore := rejectBackgroundExecutionNotices(t, db)
	finishBackgroundNotificationTest(t, db, "native", "failed", time.Now().UTC())
	restore()
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", "history").Error; err != nil {
		t.Fatal(err)
	}
	originalAt := taskcenter.NotificationHistoryCompletedAt(history.CreateTime, history.UpdateTime, history.Ext)
	preferences := func(method string, body any) map[string]any {
		raw, _ := json.Marshal(body)
		req := sidechatRequest(method, "/notification-preferences", "user-1", string(raw), nil)
		rec := httptest.NewRecorder()
		taskcenter.NotificationPreferences(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("gate change: %d %s", rec.Code, rec.Body.String())
		}
		var response struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Data
	}
	prefs := preferences(http.MethodGet, nil)
	prefs = preferences(http.MethodPatch, map[string]any{"revision": prefs["revision"], "enabled": false, "confirm_running_task_ids": []string{"first"}})
	preferences(http.MethodPatch, map[string]any{"revision": prefs["revision"], "enabled": true})
	if err := registerForkableHistoryRun(t.Context(), db, task.ConversationID, "later", "later-run", "later", chatPersistTarget{Seq: 2}, nil); err != nil {
		t.Fatal(err)
	}
	if !persistImmediateRunTerminal(t.Context(), db, task.ConversationID, "later", "later", "later-run", chatPersistTarget{Seq: 2}, nil, &RunTerminal{Status: "completed", Reason: "normal"}) {
		t.Fatal("later terminal")
	}
	if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	var notice orm.TaskNotification
	if err := db.Where("task_id = ?", task.ID).Take(&notice).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "failed" || task.FinishedAt == nil || !task.FinishedAt.Equal(originalAt) || notice.Status != "skipped" || notice.Reason != "NOTIFICATIONS_DISABLED" {
		t.Fatalf("old completion crossed reopened gate: task=%#v notice=%#v", task, notice)
	}
}
