package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
)

// Uses production handlers/async jobs and a live Chat process with the controlled
// OpenAI fixture. No user conversations or provider credentials are consumed.
func TestOrganizerProductPath(t *testing.T) {
	provider := os.Getenv("ORGANIZER_TEST_PROVIDER_URL")
	if provider == "" || os.Getenv("LAZYMIND_CHAT_SERVICE_URL") == "" {
		t.Skip("live Chat and fixture provider required")
	}
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	uid := "organizer-product-test"
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: uid, CreateUserName: uid, CreatedAt: now, UpdatedAt: now}
	cap := "64000"
	for _, row := range []any{
		&orm.UserModelProviderGroup{ID: "connection", UserModelProviderID: "provider", Name: "Fixture", BaseURL: provider, APIKey: "fixture", IsVerified: true, BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "model", UserModelProviderID: "provider", UserModelProviderGroupID: "connection", ProviderName: "OpenAI", Name: "fixture", ModelType: "llm", MaxInputTokens: &cap, BaseModel: base},
		&orm.UserSelectedModel{UserID: uid, UserName: uid, ModelKey: "llm", UserModelProviderGroupModelID: "model", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 53; i++ {
		id := fmt.Sprintf("product-%02d", i)
		conv := orm.Conversation{ID: id, DisplayName: "工作", TitleSource: "auto", ChatExecutor: "lazymind", BaseModel: base}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.ChatHistory{ID: "h-" + id, ConversationID: id, RawContent: "请帮我处理日常工作邮件", Seq: 1, RunStatus: "completed"}).Error; err != nil {
			t.Fatal(err)
		}
		// One item genuinely needs preparation; the rest reuse exact frozen summaries.
		if i > 0 {
			snap, err := loadGroupingTitleSnapshot(db.DB, conv)
			if err != nil {
				t.Fatal(err)
			}
			ids, _ := json.Marshal(snap.IDs)
			meta := orm.ConversationOpening{ConversationID: id, UserID: uid, Status: "done", WindowClosed: true, IntentStatus: "ready", Summary: "处理日常工作邮件", SourceHash: snap.Hash, EvidenceHash: snap.Evidence, InputJSON: snap.Input, SourceHistoryIDs: ids}
			if err := db.Create(&meta).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Create(&orm.Conversation{ID: "product-empty", DisplayName: "空会话", ChatExecutor: "lazymind", BaseModel: base}).Error; err != nil {
		t.Fatal(err)
	}
	conversationgroup.RegisterTitlePreparer(OrganizerTitlePreparer{})
	conversationgroup.RegisterAsyncJobs()
	invoke := func(handler http.HandlerFunc, method string, body any, vars map[string]string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/", bytes.NewReader(raw))
		r.Header.Set("X-User-Id", uid)
		r.Header.Set("X-User-Name", uid)
		r = mux.SetURLVars(r, vars)
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	start := func() orm.ConversationOrganizerRun {
		w := invoke(conversationgroup.StartOrganizer, "POST", map[string]any{}, nil)
		if w.Code != 202 {
			t.Fatalf("start %d %s", w.Code, w.Body)
		}
		var run orm.ConversationOrganizerRun
		if err := db.Where("user_id=? AND status='pending'", uid).Take(&run).Error; err != nil {
			t.Fatal(err)
		}
		return run
	}
	run := start()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runner := asyncjob.Start(ctx, db.DB, asyncjob.Options{WorkerID: "product-test", Concurrency: 1, PollInterval: 50 * time.Millisecond, LockTTL: 30 * time.Second, JobTypes: []string{"conversation_organize"}})
	defer func() { cancel(); <-runner.Done() }()
	wait := func(run *orm.ConversationOrganizerRun, status string) {
		for {
			if err := db.Where("id=?", run.ID).Take(run).Error; err != nil {
				t.Fatal(err)
			}
			if run.Status == status {
				return
			}
			if run.Status == "failed" {
				t.Fatalf("%s: %s", run.ErrorCode, run.ErrorMessage)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	wait(&run, "succeeded")
	var result struct {
		OrganizedCount    int               `json:"organized_count"`
		FreeCount         int               `json:"free_count"`
		UnassignedReasons map[string]string `json:"unassigned_reasons"`
	}
	if err := json.Unmarshal(run.ResultJSON, &result); err != nil {
		t.Fatal(err)
	}
	if result.OrganizedCount != 53 || result.FreeCount != 1 || result.UnassignedReasons["product-empty"] != "no_messages" {
		t.Fatalf("stored result counts: %+v", result)
	}
	var count int64
	db.Model(&orm.ConversationGroupMember{}).Where("source_run_id=?", run.ID).Count(&count)
	if count != 53 {
		t.Fatalf("grouped %d", count)
	}
	w := invoke(conversationgroup.CorrectOrganizerItem, "PATCH", map[string]any{"group_id": nil}, map[string]string{"run_id": run.ID, "conversation_id": "product-00"})
	if w.Code != 200 {
		t.Fatalf("correct %d %s", w.Code, w.Body)
	}
	for i := 0; i < 2; i++ {
		w = invoke(conversationgroup.UndoOrganizer, "POST", nil, map[string]string{"run_id": run.ID})
		if w.Code != 200 {
			t.Fatalf("undo %d %s", w.Code, w.Body)
		}
	}
	// Cancel a second frozen run before the worker starts; retry must retain the snapshot.
	cancel()
	<-runner.Done()
	second := start()
	w = invoke(conversationgroup.CancelOrganizer, "POST", nil, map[string]string{"run_id": second.ID})
	if w.Code != 200 {
		t.Fatalf("cancel %d %s", w.Code, w.Body)
	}
	w = invoke(conversationgroup.RetryOrganizer, "POST", nil, map[string]string{"run_id": second.ID})
	if w.Code != 202 && w.Code != 200 {
		t.Fatalf("retry %d %s", w.Code, w.Body)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 3*time.Minute)
	runner = asyncjob.Start(ctx, db.DB, asyncjob.Options{WorkerID: "product-test-retry", Concurrency: 1, PollInterval: 50 * time.Millisecond, LockTTL: 30 * time.Second, JobTypes: []string{"conversation_organize"}})
	wait(&second, "succeeded")
	if control := os.Getenv("ORGANIZER_TEST_CONTROL_URL"); control != "" {
		setDelay := func(seconds int) {
			response, err := http.Post(control+"/control", "application/json", strings.NewReader(fmt.Sprintf(`{"delay":%d}`, seconds)))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
		}
		calls := func() int {
			response, err := http.Get(control + "/stats")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var value struct {
				Calls int `json:"calls"`
			}
			if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
				t.Fatal(err)
			}
			return value.Calls
		}
		cancel()
		<-runner.Done()
		w = invoke(conversationgroup.UndoOrganizer, "POST", nil, map[string]string{"run_id": second.ID})
		if w.Code != 200 {
			t.Fatalf("second undo %d %s", w.Code, w.Body)
		}
		setDelay(10)
		defer setDelay(0)
		before := calls()
		third := start()
		ctx, cancel = context.WithTimeout(t.Context(), 3*time.Minute)
		runner = asyncjob.Start(ctx, db.DB, asyncjob.Options{WorkerID: "product-active-cancel", Concurrency: 1, PollInterval: 50 * time.Millisecond, LockTTL: 30 * time.Second, JobTypes: []string{"conversation_organize"}})
		deadline := time.Now().Add(20 * time.Second)
		for calls() == before {
			if time.Now().After(deadline) {
				t.Fatal("model request did not start")
			}
			time.Sleep(50 * time.Millisecond)
		}
		cancelStarted := time.Now()
		w = invoke(conversationgroup.CancelOrganizer, "POST", nil, map[string]string{"run_id": third.ID})
		if w.Code != 200 && w.Code != 202 {
			t.Fatalf("active cancel %d %s", w.Code, w.Body)
		}
		wait(&third, "canceled")
		if time.Since(cancelStarted) > 5*time.Second {
			t.Fatal("active cancellation waited for model completion")
		}
		setDelay(0)
		w = invoke(conversationgroup.RetryOrganizer, "POST", nil, map[string]string{"run_id": third.ID})
		if w.Code != 200 && w.Code != 202 {
			t.Fatalf("active retry %d %s", w.Code, w.Body)
		}
		wait(&third, "succeeded")
		t.Log("in-flight model call canceled and settled before retry")
	}
	t.Log("live Chat: preparation, 2 batches, apply, correction, repeated undo, cancel and retry passed")
}
