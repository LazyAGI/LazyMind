package documentdirectory

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"strings"
)

func TestHTTPObservationIdleWorkerAndNextConversation(t *testing.T) {
	db := testDB(t)
	must(t, db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}))
	oldDB, oldLazy, oldState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, db, nil)
	defer store.Init(oldDB, oldLazy, oldState)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-internal")
	must(t, db.Create(&orm.Conversation{ID: "c1", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error)
	must(t, db.Create(&orm.ChatHistory{ID: "h1", ConversationID: "c1", RunID: "r1", RunStatus: "generating", RawContent: "A contracts"}).Error)
	router := mux.NewRouter()
	router.HandleFunc("/internal/conversations/{conversation_id}/document-directories", InternalHandler)
	send := func(conv, uid, token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/internal/conversations/"+conv+"/document-directories", bytes.NewReader(encoded(body)))
		req.Header.Set("X-User-Id", uid)
		req.Header.Set("X-LazyMind-Internal-Token", token)
		r := httptest.NewRecorder()
		router.ServeHTTP(r, req)
		return r
	}
	c := sample("/a")
	c.Excerpt = "unique-observed-body"
	obs := InternalRequest{Command: Command{Operation: "observations"}, HistoryID: "h1", RunID: "r1", Observations: []Observation{{CallID: "read", Source: "local", Status: "success", Operation: "read", Candidates: []Candidate{c}}}}
	if got := send("c1", "u", "wrong", obs); got.Code != 401 {
		t.Fatal(got.Code)
	}
	if got := send("c1", "other", "test-internal", obs); got.Code != 404 {
		t.Fatal(got.Code)
	}
	if got := send("c1", "u", "test-internal", obs); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	second := c
	second.Excerpt = "another-window-for-followup"
	readAt := time.Now().UTC()
	second.ReadAt, second.ReadState = &readAt, "partial"
	second.Locator = encoded(map[string]any{"path": "/a", "offset": 100, "end_line": 150})
	must(t, db.Model(&orm.ChatHistory{}).Where("id=?", "h1").Update("raw_content", "A followup").Error)
	obs.Observations = []Observation{{CallID: "read-second", Source: "local", Status: "success", Operation: "read", Candidates: []Candidate{second}}}
	if got := send("c1", "u", "test-internal", obs); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	obs.Observations[0].CallID = "same-window-again"
	if got := send("c1", "u", "test-internal", obs); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	must(t, db.Model(&orm.ChatHistory{}).Where("id=?", "h1").Update("run_status", "completed").Error)
	if got := send("c1", "u", "test-internal", obs); got.Code != 404 {
		t.Fatal("stale run callback accepted")
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req modelRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		if config, ok := req.LLMConfig["llm"].(map[string]any); !ok || config["model"] != "conversation-model" {
			t.Error("organizer did not receive the source chat model")
		}
		p := Proposal{}
		if req.Mode == "route" {
			body := string(encoded(req.Input))
			if strings.Count(body, c.Excerpt) != 1 || strings.Count(body, second.Excerpt) != 1 || !strings.Contains(body, "resource_keys") || !strings.Contains(body, "A contracts") || !strings.Contains(body, "A followup") {
				t.Error("route duplicated body evidence or lost question/resource association")
			}
			var payload struct {
				Input     struct{ Observations []struct{ ID, Query string } } `json:"input"`
				Resources map[string]struct {
					Evidence []struct {
						Excerpt string
						IDs     []string `json:"observation_ids"`
					}
				} `json:"resources"`
			}
			must(t, json.Unmarshal(encoded(req.Input), &payload))
			queries := map[string]string{}
			for _, o := range payload.Input.Observations {
				queries[o.ID] = o.Query
			}
			for _, e := range payload.Resources[resourceKey(c)].Evidence {
				want := "A contracts"
				if e.Excerpt == second.Excerpt {
					want = "A followup"
				}
				for _, id := range e.IDs {
					if queries[id] != want {
						t.Error("evidence attached to the wrong question")
					}
				}
			}
			p.Targets = []Target{{Name: "A", Intent: "A contracts", ResourceKeys: []string{resourceKey(c)}}}
		} else {
			p.Assignments = []Assignment{{ResourceKey: resourceKey(c), Reason: "contract candidate"}}
		}
		_ = json.NewEncoder(w).Encode(modelResponse{Status: "succeeded", Output: p, Usage: map[string]any{"model_calls": 1}})
	}))
	defer model.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", model.URL)
	oldLoader := LoadChatModel
	LoadChatModel = func(_ context.Context, _ *gorm.DB, uid, cid string) (map[string]any, error) {
		if uid != "u" || cid != "c1" {
			t.Errorf("wrong source conversation: %q %q", uid, cid)
		}
		return map[string]any{"llm": map[string]any{"model": "conversation-model"}}, nil
	}
	defer func() { LoadChatModel = oldLoader }()
	now := time.Now().UTC()
	event := orm.ConversationIdleEvent{ID: "e", EventID: "e", UserID: "u", ConversationID: "c1", Status: "waiting", DueAt: now.Add(-time.Second), LastActivityAt: now}
	must(t, db.Create(&event).Error)
	must(t, QueueIdle(context.Background(), db, event))
	RegisterJobs()
	ctx, cancel := context.WithCancel(context.Background())
	runner := asyncjob.Start(ctx, db, asyncjob.Options{JobTypes: []string{PlanJob, OrganizeJob}, SerializeResources: true, Concurrency: 2, PollInterval: 10 * time.Millisecond, LockTTL: time.Minute})
	defer func() { cancel(); <-runner.Done() }()
	var dirs []orm.DocumentDirectory
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		dirs, err = List(ctx, db, "u")
		must(t, err)
		if len(dirs) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(dirs) != 1 {
		t.Fatal("idle batch did not produce committed directory")
	}
	d, err := Get(ctx, db, "u", dirs[0].ID)
	must(t, err)
	if len(d.Entries) != 1 || d.Entries[0].Confirmed {
		t.Fatal("candidate state lost")
	}
	must(t, db.Create(&orm.Conversation{ID: "c2", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error)
	must(t, db.Create(&orm.ChatHistory{ID: "h2", ConversationID: "c2", RunID: "r2", RunStatus: "generating"}).Error)
	req := InternalRequest{Command: Command{Operation: "get", DirectoryID: dirs[0].ID}, HistoryID: "h2", RunID: "r2"}
	if got := send("c2", "u", "test-internal", req); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	using, err := activeUse(db, "u", dirs[0].ID)
	must(t, err)
	if !using {
		t.Fatal("live Main Chat use not protected")
	}
}
