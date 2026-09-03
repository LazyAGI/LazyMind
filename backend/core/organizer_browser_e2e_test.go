package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/currentmemory"
	"lazymind/core/episode"
	"lazymind/core/remotefs"
	"lazymind/core/resourceupdate"
	"lazymind/core/store"
)

// Opt-in fixture server for the real preference component and real Chat model.
// All memory and tasks live in the disposable test DB, never the user's DB.
func TestOrganizerBrowserFixture(t *testing.T) {
	addr := os.Getenv("ORGANIZER_BROWSER_FIXTURE_ADDR")
	if addr == "" {
		t.Skip("opt-in browser fixture")
	}
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	defer store.Init(nil, nil, nil)
	if err := episode.Initialize(db.DB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	user := "organizer-browser-fixture"
	repo := currentmemory.NewRepository(db.DB)
	if err := repo.EnsureInitialized(ctx, user); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items := []currentmemory.PreferenceItem{}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("pref.answer.%d", i)
		ref := fmt.Sprintf("answer-%d", i)
		summaries := []string{"默认使用中文回答。", "回答默认使用中文。", "本次排查临时把重试次数设置为 7；这不是长期偏好。"}
		item := currentmemory.PreferenceItem{Name: name, Summary: summaries[i-1], Ref: "references/" + ref + ".md", CreatedAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-01T00:00:00Z"}
		items = append(items, item)
		body := fmt.Sprintf("---\nname: %s\nsummary: %s\ncreated_at: '2026-09-01T00:00:00Z'\nupdated_at: '2026-09-01T00:00:00Z'\nsource:\n  kind: memory_review\n  conversation_id: fixture-conversation\n---\n\n## Application Scenarios\n默认回答。\n\n## Preference Details\n%s\n\n## Reason\n用户明确表达。\n", ref, item.Summary, item.Summary)
		if err := repo.UpsertEntry(ctx, orm.MemoryCurrentEntry{UserID: user, Path: currentmemory.ReferencesPath + "/" + ref + ".md", EntryType: "file", Content: []byte(body), Size: int64(len(body)), Mime: "text/markdown", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	body, err := currentmemory.RenderPreferences(currentmemory.PreferenceDocument{Preferences: items})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.UpsertEntry(ctx, orm.MemoryCurrentEntry{UserID: user, Path: currentmemory.PreferencePath, EntryType: "file", Content: body, Size: int64(len(body)), Mime: "text/yaml", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Start with one current Review and two pending Reviews, all genuine handlers.
	for i := 0; i < 3; i++ {
		request, _ := json.Marshal(map[string]any{"conversation_id": fmt.Sprintf("fixture-conversation-%d", i), "history": []map[string]string{{"role": "user", "content": "你好，请回复你好即可，没有要记住的信息。"}}})
		task := orm.ResourceUpdateTask{ID: fmt.Sprintf("fixture-review-%d", i), TaskType: orm.ResourceUpdateTaskTypeGenerateReview, ResourceType: orm.ResourceUpdateResourceTypeMemory, UserID: user, ResourceID: fmt.Sprintf("fixture-conversation-%d", i), TriggerType: "manual", TriggerID: fmt.Sprintf("fixture-trigger-%d", i), Status: "pending", RequestJSON: request, NextRunAt: now, LaneKey: resourceupdate.MemoryMaintenanceLaneKey(user), LanePriority: resourceupdate.MemoryReviewLanePriority, LaneOrderAt: now.Add(time.Duration(i) * time.Millisecond), CreatedAt: now, UpdatedAt: now}
		if err = db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := mux.NewRouter()
	cm := currentmemory.NewHandler(db.DB)
	fs := remotefs.NewHandler(db.DB)
	r.HandleFunc("/api/core/memory/preferences", cm.ListPreferences).Methods("GET")
	r.HandleFunc("/api/core/memory/preferences:organize", resourceupdate.GetLatestPreferenceOrganizer).Methods("GET")
	r.HandleFunc("/api/core/memory/preferences:organize", resourceupdate.SubmitPreferenceOrganizer).Methods("POST")
	r.HandleFunc("/api/core/memory/preferences:organize/{task_id}", resourceupdate.GetPreferenceOrganizer).Methods("GET")
	r.HandleFunc("/api/core/memory/preferences:order", cm.ReorderPreferences).Methods("PUT")
	r.HandleFunc("/api/core/memory/preferences/{name}", cm.GetPreference).Methods("GET")
	r.HandleFunc("/api/core/memory/preferences/{name}", cm.DeletePreference).Methods("DELETE")
	r.HandleFunc("/api/core/remote-fs/content", fs.Content).Methods("GET", "PUT")
	r.HandleFunc("/api/core/remote-fs/list", fs.List).Methods("GET")
	r.HandleFunc("/api/core/remote-fs/exists", fs.Exists).Methods("GET")
	r.HandleFunc("/api/core/remote-fs/info", fs.Info).Methods("GET")
	r.HandleFunc("/api/core/remote-fs/dir", fs.Dir).Methods("POST")
	r.HandleFunc("/api/core/remote-fs/path", fs.Delete).Methods("DELETE")
	r.HandleFunc("/api/core/internal/memory/episodes", episode.InternalCreate).Methods("POST")
	r.HandleFunc("/api/core/internal/memory/episodes", episode.InternalListByConversation).Methods("GET")
	r.HandleFunc("/api/core/internal/memory/episodes/{episode_id}", episode.InternalDelete).Methods("DELETE")
	r.HandleFunc("/__fixture/tasks", func(w http.ResponseWriter, r *http.Request) {
		var tasks []orm.ResourceUpdateTask
		db.Order("created_at ASC").Find(&tasks)
		json.NewEncoder(w).Encode(tasks)
	})
	r.HandleFunc("/__fixture/stop", func(w http.ResponseWriter, r *http.Request) { cancel() }).Methods("POST")
	server := &http.Server{Addr: addr}
	// Keep the router explicit so this opt-in fixture cannot be mistaken for production auth.
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { req.Header.Set("X-User-Id", user); r.ServeHTTP(w, req) })
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Log(err)
			cancel()
		}
	}()
	worker := resourceupdate.NewWorker(db.DB, resourceupdate.Config{WorkerLockTTL: 6 * time.Second, WorkerBatchSize: 10, MaxAttempts: 1}, "browser-fixture")
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				if _, err := worker.RunOnce(ctx); err != nil && ctx.Err() == nil {
					t.Log(err)
				}
			}
		}
	}()
	t.Log("fixture ready at", addr)
	select {
	case <-ctx.Done():
	case <-time.After(12 * time.Minute):
	}
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	server.Shutdown(shutdownCtx)
	<-workerDone
}
