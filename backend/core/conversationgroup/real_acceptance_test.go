package conversationgroup

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// This opt-in acceptance uses the production snapshot handler, async runner,
// Algorithm HTTP endpoint and a real user-selected model. Private data and
// service addresses are supplied by the caller and are never committed.
func TestOrganizerRealModelAcceptance(t *testing.T) {
	input := os.Getenv("ORGANIZER_ACCEPTANCE_INPUT")
	if input == "" {
		t.Skip("set ORGANIZER_ACCEPTANCE_INPUT and model/service environment to run")
	}
	modelURL, model := os.Getenv("ORGANIZER_ACCEPTANCE_MODEL_URL"), os.Getenv("ORGANIZER_ACCEPTANCE_MODEL")
	apiKey := "acceptance"
	if path := os.Getenv("ORGANIZER_ACCEPTANCE_API_KEY_FILE"); path != "" {
		secret, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		apiKey = string(bytes.TrimSpace(secret))
	}
	if modelURL == "" || model == "" || os.Getenv("LAZYMIND_CHAT_SERVICE_URL") == "" {
		t.Fatal("model URL, model and chat service URL are required")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var items []snapshotConversation
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("acceptance input is empty")
	}
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	const uid = "organizer-real-acceptance"
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: uid, CreateUserName: uid, CreatedAt: now, UpdatedAt: now}
	cap := "262144"
	if override := os.Getenv("ORGANIZER_ACCEPTANCE_CONTEXT"); override != "" {
		cap = override
	}
	for _, row := range []any{
		&orm.UserModelProviderGroup{ID: "acceptance-connection", UserModelProviderID: "acceptance-provider", Name: "Acceptance", BaseURL: modelURL, APIKey: apiKey, IsVerified: true, BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "acceptance-model", UserModelProviderID: "acceptance-provider", UserModelProviderGroupID: "acceptance-connection", ProviderName: "OpenAI", Name: model, ModelType: "llm", MaxInputTokens: &cap, BaseModel: base},
		&orm.UserSelectedModel{UserID: uid, UserName: uid, ModelKey: "llm", UserModelProviderGroupModelID: "acceptance-model", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed := func(item snapshotConversation, index int) {
		t.Helper()
		b := base
		b.CreatedAt = now.Add(time.Duration(index) * time.Millisecond)
		if err := db.Create(&orm.Conversation{ID: item.ID, DisplayName: item.Title, ChatExecutor: "lazymind", BaseModel: b}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.ConversationOpening{ConversationID: item.ID, UserID: uid, Summary: item.Summary, Status: "done", IntentStatus: "ready", MetadataRevision: 1, InputJSON: json.RawMessage(`{}`), SourceHistoryIDs: json.RawMessage(`[]`), SourceHash: "acceptance", EvidenceHash: "acceptance", GeneratorVersion: "acceptance", UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, item := range items {
		seed(item, i)
	}
	want := len(items)
	var formal *orm.ConversationGroup
	if path := os.Getenv("ORGANIZER_ACCEPTANCE_FORMAL"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Groups []struct {
				ID, Name, Description string
				Members               []string
			} `json:"formal_groups"`
		}
		if err := json.Unmarshal(raw, &fixture); err != nil {
			t.Fatal(err)
		}
		for _, group := range fixture.Groups {
			g := orm.ConversationGroup{ID: group.ID, UserID: uid, Name: group.Name, NormalizedName: normalizeName(group.Name), Scope: group.Description, Version: 1, CreatedBy: CreatedByUser, CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&g).Error; err != nil {
				t.Fatal(err)
			}
			formal = &g
			for _, id := range group.Members {
				if err := db.Create(&orm.ConversationGroupMember{ConversationID: id, GroupID: g.ID, UserID: uid, Revision: 1, Source: CreatedByUser, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
				want--
			}
		}
	}
	invoke := func(handler http.HandlerFunc, method string, body any, vars map[string]string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest(method, "/", bytes.NewReader(raw))
		request.Header.Set("X-User-Id", uid)
		request.Header.Set("X-User-Name", uid)
		request = mux.SetURLVars(request, vars)
		response := httptest.NewRecorder()
		handler(response, request)
		return response
	}
	started := time.Now()
	response := invoke(StartOrganizer, http.MethodPost, map[string]any{}, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start status %d: %s", response.Code, response.Body.String())
	}
	var run orm.ConversationOrganizerRun
	if err := db.Where("user_id=?", uid).Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.ProgressTotal != int64(want) {
		t.Fatalf("snapshot %d; want %d", run.ProgressTotal, want)
	}
	seed(snapshotConversation{ID: "created-after-snapshot", Title: "新会话", Summary: "点击整理后创建的会话"}, len(items)+1)
	RegisterAsyncJobs()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	runner := asyncjob.Start(ctx, db.DB, asyncjob.Options{WorkerID: "real-acceptance", Concurrency: 1, PollInterval: 100 * time.Millisecond, LockTTL: 30 * time.Second, JobTypes: []string{organizerJobType}})
	defer func() { cancel(); <-runner.Done() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for run.Status != "succeeded" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
		if err := db.Where("id=?", run.ID).Take(&run).Error; err != nil {
			t.Fatal(err)
		}
		if run.Status == "failed" || run.Status == "canceled" {
			t.Fatalf("run %s: %s %s", run.Status, run.ErrorCode, run.ErrorMessage)
		}
	}
	var lateCount int64
	db.Model(&orm.ConversationGroupMember{}).Where("conversation_id=?", "created-after-snapshot").Count(&lateCount)
	if lateCount != 0 {
		t.Fatal("new conversation included in frozen run")
	}
	var groups []orm.ConversationGroup
	db.Where("user_id=?", uid).Find(&groups)
	var members []orm.ConversationGroupMember
	db.Where("user_id=?", uid).Find(&members)
	counts := map[string]int{}
	for _, member := range members {
		counts[member.GroupID]++
	}
	for _, group := range groups {
		if group.CreatedBy == CreatedByOrganizer && (counts[group.ID] < 3 || group.Scope == "") {
			t.Fatalf("invalid automatic group %s", group.ID)
		}
	}
	if formal != nil {
		var after orm.ConversationGroup
		db.Where("id=?", formal.ID).Take(&after)
		if after.Name != formal.Name || after.Scope != formal.Scope || after.Version != formal.Version {
			t.Fatal("formal group metadata changed")
		}
	}
	result := runDTO(ctx, db.DB, run, true)
	if output := os.Getenv("ORGANIZER_ACCEPTANCE_OUTPUT"); output != "" {
		artifact := map[string]any{"elapsed_seconds": time.Since(started).Seconds(), "model": model, "snapshot_count": want, "run": result, "groups": groups, "members": members, "checkpoint": json.RawMessage(run.CheckpointJSON)}
		raw, err := json.MarshalIndent(artifact, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if address := os.Getenv("ORGANIZER_ACCEPTANCE_BROWSER_ADDR"); address != "" {
		// Optional browser session against these same production group handlers.
		// Authentication/gateway and unrelated APIs are outside this fixture.
		router := mux.NewRouter()
		api := router.PathPrefix("/api/core").Subrouter()
		api.HandleFunc("/conversation-groups", ListGroups).Methods("GET")
		api.HandleFunc("/conversation-groups", CreateGroup).Methods("POST")
		api.HandleFunc("/conversation-groups/{group_id}", GetGroup).Methods("GET")
		api.HandleFunc("/conversation-groups/{group_id}", UpdateGroup).Methods("PATCH")
		api.HandleFunc("/conversation-groups/{group_id}", DeleteGroup).Methods("DELETE")
		api.HandleFunc("/conversation-groups/{group_id}/conversations", AddMember).Methods("POST")
		api.HandleFunc("/conversation-groups/{group_id}/conversations/{conversation_id}", RemoveMember).Methods("DELETE")
		api.HandleFunc("/conversation-organizer-runs:latest", GetLatestOrganizer).Methods("GET")
		api.HandleFunc("/conversation-organizer-runs", StartOrganizer).Methods("POST")
		api.HandleFunc("/conversation-organizer-runs/{run_id}:cancel", CancelOrganizer).Methods("POST")
		api.HandleFunc("/conversation-organizer-runs/{run_id}:retry", RetryOrganizer).Methods("POST")
		api.HandleFunc("/conversation-organizer-runs/{run_id}:undo", UndoOrganizer).Methods("POST")
		api.HandleFunc("/conversation-organizer-runs/{run_id}/items/{conversation_id}", CorrectOrganizerItem).Methods("PATCH")
		api.HandleFunc("/conversation-organizer-runs/{run_id}", GetOrganizer).Methods("GET")
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(router)
		server.Listener = listener
		server.Start()
		defer server.Close()
		t.Logf("browser fixture ready at %s, release by creating ORGANIZER_ACCEPTANCE_BROWSER_RELEASE", server.URL)
		release := os.Getenv("ORGANIZER_ACCEPTANCE_BROWSER_RELEASE")
		if release == "" {
			t.Fatal("browser release path is required")
		}
		for {
			if _, err := os.Stat(release); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
	}
	// Exercise result-panel correction and idempotent undo through the handlers.
	for _, member := range members {
		if member.SourceRunID != run.ID {
			continue
		}
		r := invoke(CorrectOrganizerItem, http.MethodPatch, map[string]any{"group_id": nil}, map[string]string{"run_id": run.ID, "conversation_id": member.ConversationID})
		if r.Code != 200 {
			t.Fatalf("correction %d: %s", r.Code, r.Body.String())
		}
		break
	}
	for i := 0; i < 2; i++ {
		r := invoke(UndoOrganizer, http.MethodPost, map[string]any{}, map[string]string{"run_id": run.ID})
		if r.Code != 200 {
			t.Fatalf("undo %d: %s", r.Code, r.Body.String())
		}
	}
	var residual int64
	db.Model(&orm.ConversationGroupMember{}).Where("source_run_id=?", run.ID).Count(&residual)
	if residual != 0 {
		t.Fatalf("undo left %d run members", residual)
	}
	t.Logf("real model applied snapshot=%d groups=%d in %.1fs; correction and repeated undo succeeded", want, len(groups), time.Since(started).Seconds())
}
