package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

type evolutionTransport func(*http.Request) (*http.Response, error)

func TestResumeThreadOwnershipAndActiveTaskGuard(t *testing.T) {
	for _, scenario := range []string{"resume", "other-owner", "other-active-task", "rejected"} {
		t.Run(scenario, func(t *testing.T) {
			db := newAgentTestDB(t)
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			owner := "u"
			if scenario == "other-owner" {
				owner = "someone-else"
			}
			if err := db.DB.Create(&orm.AgentThread{ThreadID: "t", CreateUserID: owner, Status: "paused", ThreadPayload: "{}"}).Error; err != nil {
				t.Fatal(err)
			}
			if scenario == "other-active-task" {
				if err := db.DB.Create(&orm.AgentUserActiveThread{UserID: "u", ThreadID: "other", Status: userActiveThreadStatusActive, LeaseUntil: time.Now()}).Error; err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" {
					calls++
					if r.URL.Path != "/threads/t/resume" {
						t.Errorf("wrong endpoint: %s", r.URL.Path)
					}
					var payload map[string]any
					_ = json.NewDecoder(r.Body).Decode(&payload)
					if payload["command_id"] != "resume-1" || len(payload) != 1 {
						t.Errorf("wrong control payload: %+v", payload)
					}
					if scenario == "rejected" {
						w.WriteHeader(http.StatusConflict)
						_, _ = w.Write([]byte(`{"error":"not paused"}`))
						return
					}
					_, _ = w.Write([]byte(`{"status":"accepted"}`))
					return
				}
				_, _ = w.Write([]byte(`{"status":"running","runtime_status":"running"}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)
			r := mux.SetURLVars(httptest.NewRequest("POST", "/agent/threads/t/resume", strings.NewReader(`{"command_id":"resume-1","llm_config":{"model":"forged"}}`)), map[string]string{"thread_id": "t"})
			r.Header.Set("X-User-Id", "u")
			w := httptest.NewRecorder()
			// Exercise the action policy before adding the public route.
			postThreadAction(w, r, "resume")
			if scenario == "other-owner" || scenario == "other-active-task" {
				if calls != 0 || w.Code < 400 {
					t.Fatalf("guard bypassed: calls=%d status=%d", calls, w.Code)
				}
				return
			}
			if calls != 1 {
				t.Fatalf("resume calls=%d", calls)
			}
			if scenario == "rejected" {
				if w.Code != 409 {
					t.Fatalf("rejection lost: %d", w.Code)
				}
				return
			}
			var active orm.AgentUserActiveThread
			if err := db.DB.First(&active, "user_id = ?", "u").Error; err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || active.ThreadID != "t" || active.Status != userActiveThreadStatusActive {
				t.Fatalf("resume not tracked: %d %+v", w.Code, active)
			}
		})
	}
}

func (f evolutionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCreateThreadPersistsAfterBrowserDisconnects(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, role := range []string{"llm", "evo_llm", "embed_main"} {
		seedAgentRuntimeModelConfig(t, db, "u", role)
	}
	ctx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = evolutionTransport(func(r *http.Request) (*http.Response, error) {
		disconnect()
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"thread_id":"accepted-before-navigation","status":"created"}`))}, nil
	})
	r := httptest.NewRequest("POST", "/agent/threads", strings.NewReader(`{"mode":"interactive"}`)).WithContext(ctx)
	r.Header.Set("X-User-Id", "u")
	w := httptest.NewRecorder()
	CreateThread(w, r)
	if w.Code != 200 {
		t.Fatalf("accepted create lost: %d %s", w.Code, w.Body.String())
	}
	var thread orm.AgentThread
	if err := db.DB.First(&thread, "thread_id = ?", "accepted-before-navigation").Error; err != nil {
		t.Fatal(err)
	}
	var active orm.AgentUserActiveThread
	if err := db.DB.First(&active, "user_id = ?", "u").Error; err != nil {
		t.Fatal(err)
	}
	if active.ThreadID != thread.ThreadID || active.Status != userActiveThreadStatusActive {
		t.Fatalf("cannot recover accepted thread: %+v", active)
	}
}

func TestRuntimeCancellationAndPendingCleanupAreNotTerminal(t *testing.T) {
	for _, upstream := range []*evoThread{
		{ThreadID: "t", Status: "running", RuntimeStatus: "cancelling", CleanupPending: true},
		{ThreadID: "t", Status: "failed", RuntimeStatus: "failed", CleanupPending: true},
	} {
		status := threadFlowStatusFromEvo(upstream)
		item := threadResponse{}
		applyThreadFlowStatus(&item, status)
		if item.RuntimeStatus != upstream.RuntimeStatus || !item.CleanupPending || item.StatusSource != "live" {
			t.Fatalf("runtime state lost: %+v", item)
		}
		if isTerminalThreadFlowStatus(status) || !isThreadFlowRunning(status) {
			t.Fatal("cleanup released active task protection")
		}
	}
}

func TestThreadStatusFreshnessSurvivesUpstreamFailure(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.DB.Create(&orm.AgentThread{ThreadID: "t", CreateUserID: "u", Status: "created", ThreadPayload: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	upstreamOK := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !upstreamOK {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"thread_id":"t","status":"running","current_step":"eval"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)
	read := func() threadResponse {
		r := mux.SetURLVars(httptest.NewRequest("GET", "/agent/threads/t", nil), map[string]string{"thread_id": "t"})
		r.Header.Set("X-User-Id", "u")
		w := httptest.NewRecorder()
		GetThread(w, r)
		if w.Code != 200 {
			t.Fatalf("get: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Data struct {
				Thread threadResponse `json:"thread"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data.Thread
	}
	live := read()
	if live.StatusSource != "live" || live.Status != "running" || live.ObservedAt == nil {
		t.Fatalf("not live: %+v", live)
	}
	var stored orm.AgentThread
	if err := db.DB.First(&stored, "thread_id = ?", "t").Error; err != nil {
		t.Fatal(err)
	}
	upstreamOK = false
	cached := read()
	if cached.StatusSource != "cached" || cached.ObservedAt == nil || !cached.ObservedAt.Equal(*stored.StatusObservedAt) {
		t.Fatalf("cache observation changed: %+v", cached)
	}
	if err := reconcileThreadFlowStatus(db.DB, "t", &threadFlowStatusResponse{CurrentStep: "analysis"}); err != nil {
		t.Fatal(err)
	}
	cached = read()
	if cached.ObservedAt == nil || !cached.ObservedAt.Equal(*stored.StatusObservedAt) {
		t.Fatal("step-only update changed the last successful status observation")
	}
}

func TestCancelDoesNotReleaseActiveTaskUntilTerminalConfirmation(t *testing.T) {
	for _, status := range []string{"running", "unavailable", "canceled", "ended"} {
		t.Run(status, func(t *testing.T) {
			db := newAgentTestDB(t)
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			for _, row := range []any{&orm.AgentThread{ThreadID: "t", CreateUserID: "u", Status: "running", ThreadPayload: "{}"}, &orm.AgentUserActiveThread{UserID: "u", ThreadID: "t", Status: userActiveThreadStatusActive, LeaseUntil: time.Now()}} {
				if err := db.DB.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" {
					var command map[string]any
					_ = json.NewDecoder(r.Body).Decode(&command)
					if command["command_id"] != "retry-same-id" {
						t.Error("command id not forwarded")
					}
					_, _ = w.Write([]byte(`{"status":"accepted"}`))
					return
				}
				if status == "unavailable" {
					w.WriteHeader(503)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"thread_id": "t", "status": status})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)
			r := mux.SetURLVars(httptest.NewRequest("POST", "/agent/threads/t/cancel", strings.NewReader(`{"command_id":"retry-same-id"}`)), map[string]string{"thread_id": "t"})
			r.Header.Set("X-User-Id", "u")
			w := httptest.NewRecorder()
			CancelThread(w, r)
			if w.Code != 200 {
				t.Fatalf("cancel %d %s", w.Code, w.Body.String())
			}
			var active orm.AgentUserActiveThread
			if err := db.DB.First(&active, "user_id = ?", "u").Error; err != nil {
				t.Fatal(err)
			}
			want := userActiveThreadStatusActive
			if status == "canceled" || status == "ended" {
				want = userActiveThreadStatusFinished
			}
			if active.Status != want {
				t.Fatalf("status=%s want=%s", active.Status, want)
			}
		})
	}
}

func TestCreateThreadUsesSelectedModelWithoutChangingDefault(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, role := range []string{"llm", "evo_llm", "embed_main"} {
		seedAgentRuntimeModelConfig(t, db, "u", role)
	}
	var original orm.UserModelProviderGroupModel
	if err := db.DB.First(&original, "id = ?", "model-evo-llm").Error; err != nil {
		t.Fatal(err)
	}
	alternate := original
	alternate.ID = "alternate"
	alternate.Name = "gpt-4o"
	var alternateGroup orm.UserModelProviderGroup
	if err := db.DB.First(&alternateGroup, "id = ?", original.UserModelProviderGroupID).Error; err != nil {
		t.Fatal(err)
	}
	alternateGroup.ID = "alternate-group"
	if err := db.DB.Create(&alternateGroup).Error; err != nil {
		t.Fatal(err)
	}
	alternate.UserModelProviderGroupID = alternateGroup.ID
	if err := db.DB.Create(&alternate).Error; err != nil {
		t.Fatal(err)
	}
	selection := db.DB.Model(&orm.UserSelectedModel{}).Where("user_id = ? AND model_type = ?", "u", "evo_llm")
	if err := selection.Update("user_model_provider_group_model_id", alternate.ID).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err := modelconfig.LoadEvolutionModels(context.Background(), db.DB, "u")
	if err != nil || catalog.ConfiguredDefault == nil {
		t.Fatalf("catalog %v", err)
	}
	ref := catalog.ConfiguredDefault.ModelRef
	if err := selection.Update("user_model_provider_group_model_id", original.ID).Error; err != nil {
		t.Fatal(err)
	}
	// A broken default must not prevent an explicitly selected valid model.
	if err := db.DB.Model(&orm.UserModelProviderGroup{}).Where("id = ?", original.UserModelProviderGroupID).Update("api_key_ciphertext", "invalid-test-ciphertext").Error; err != nil {
		t.Fatal(err)
	}
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"thread_id":"selected-task","status":"created"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)
	body, _ := json.Marshal(map[string]any{"evo_model_ref": ref, "mode": "interactive", "llm_config": map[string]any{"evo_llm": map[string]string{"model": "forged"}}, "model_at_creation": map[string]string{"display_name": "forged", "api_key": "forged-secret"}})
	r := httptest.NewRequest("POST", "/agent/threads", strings.NewReader(string(body)))
	r.Header.Set("X-User-Id", "u")
	w := httptest.NewRecorder()
	CreateThread(w, r)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	config := received["llm_config"].(map[string]any)
	if config["evo_llm"].(map[string]any)["model"] != "gpt-4o" || config["llm"].(map[string]any)["model"] != "gpt-llm" {
		t.Fatal("wrong per-task model injection")
	}
	var saved orm.AgentThread
	if err := db.DB.First(&saved, "thread_id = ?", "selected-task").Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saved.ThreadPayload, "sk-") || strings.Contains(saved.ThreadPayload, "forged") || strings.Contains(saved.ThreadPayload, "llm_config") {
		t.Fatalf("private or client model config saved: %s", saved.ThreadPayload)
	}
	if !strings.Contains(saved.ThreadPayload, "model_at_creation") || !strings.Contains(saved.ThreadPayload, "gpt-4o") {
		t.Fatal("creation summary missing")
	}
	var selected orm.UserSelectedModel
	if err := db.DB.Where("user_id = ? AND model_type = ?", "u", "evo_llm").First(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if selected.UserModelProviderGroupModelID != original.ID {
		t.Fatal("default was changed")
	}
}
