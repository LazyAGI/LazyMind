package resourceupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/currentmemory"
	"lazymind/core/store"
)

func seedOrganizerIndex(t *testing.T, db *gorm.DB, user string, chars int, now time.Time) *PreferenceOrganizerState {
	t.Helper()
	document := currentmemory.PreferenceDocument{}
	for i := 0; i < 10; i++ {
		document.Preferences = append(document.Preferences, currentmemory.PreferenceItem{
			Name: fmt.Sprintf("pref.%02d", i), Summary: "p", Ref: fmt.Sprintf("references/pref-%02d.md", i),
			CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339),
		})
	}
	remaining := chars - currentmemory.BuildPreferenceProjectionState(document, 1000).FullProjectionChars
	for i := range document.Preferences {
		add := min(remaining, 99)
		if add < 0 {
			t.Fatal("test projection target is too small")
		}
		document.Preferences[i].Summary += strings.Repeat("界", add)
		remaining -= add
	}
	if remaining != 0 {
		t.Fatal("test projection target is too large")
	}
	content, err := currentmemory.RenderPreferences(document)
	if err != nil {
		t.Fatal(err)
	}
	err = currentmemory.NewRepository(db).UpsertEntry(t.Context(), orm.MemoryCurrentEntry{
		UserID: user, Path: currentmemory.PreferencePath, EntryType: currentmemory.EntryFile,
		Content: content, Size: int64(len(content)), Mime: "application/yaml", FileType: "yaml", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadPreferenceOrganizerState(t.Context(), db, user, 1000)
	if err != nil || state.FullProjectionChars != chars {
		t.Fatalf("seed state=%+v err=%v", state, err)
	}
	return state
}

func organizerTestWorker(t *testing.T, db *gorm.DB, call func(context.Context, algo.PreferenceOrganizerRequest) (*algo.PreferenceOrganizerResponse, int, error)) *Worker {
	t.Helper()
	w := NewWorker(db, Config{MaxAttempts: 1}, "organizer-test")
	w.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) { return nil, nil }
	w.callers.PreferenceOrganizer = call
	return w
}

func TestScannerReconcilesExistingPreferencesAfterBudgetReduction(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	seedOrganizerIndex(t, db, "existing-user-without-conversation", 1258, time.Now().UTC())
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "5000")
	scanner := NewScanner(db, Config{}, "scanner")
	result, err := scanner.RunOnce(t.Context())
	if err != nil || result.PreferenceTasksCreated != 0 {
		t.Fatalf("old budget: %+v %v", result, err)
	}
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	// A fresh Scanner is the startup path; no new preference write occurs.
	scanner = NewScanner(db, Config{}, "restarted-scanner")
	result, err = scanner.RunOnce(t.Context())
	if err != nil || result.PreferenceTasksCreated != 1 {
		t.Fatalf("new budget: %+v %v", result, err)
	}
	result, err = scanner.RunOnce(t.Context())
	if err != nil || result.PreferenceTasksCreated != 0 {
		t.Fatalf("duplicate scan: %+v %v", result, err)
	}
}

func TestAutomaticOrganizerAdmissionIsConcurrentAndUserScoped(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	now := time.Now().UTC()
	for _, user := range []string{"u1", "u2"} {
		seedOrganizerIndex(t, db, user, 1258, now)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := EnqueuePreferenceOrganizerIfNeeded(context.Background(), db, fmt.Sprintf("u%d", i%2+1), 1000, now)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&orm.ResourceUpdateTask{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("task count=%d err=%v", count, err)
	}
}

func TestScannerEventuallyChecksEveryUserAcrossBatches(t *testing.T) {
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	db := newResourceUpdateTestDB(t)
	seedOrganizerIndex(t, db, "user-000", 1258, time.Now().UTC())
	var original orm.MemoryCurrentEntry
	if err := db.Where("user_id = ?", "user-000").Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	entries := make([]orm.MemoryCurrentEntry, 0, 124)
	for i := 1; i < 125; i++ {
		entry := original
		entry.UserID = fmt.Sprintf("user-%03d", i)
		entries = append(entries, entry)
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(db, Config{}, "scanner")
	created := 0
	for i := 0; i < 3; i++ {
		result, err := scanner.RunOnce(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		created += result.PreferenceTasksCreated
	}
	if created != 125 {
		t.Fatalf("some users were starved or duplicated: created=%d", created)
	}
}

func TestOrganizerFinalStatePreventsRepeatedCallsAndExpiresOnChanges(t *testing.T) {
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	db := newResourceUpdateTestDB(t)
	initial := seedOrganizerIndex(t, db, "u1", 1258, time.Now().UTC().Add(-time.Minute))
	_, created, err := EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC())
	if err != nil || !created {
		t.Fatal(created, err)
	}
	// The queued snapshot must not be used as the execution snapshot.
	seedOrganizerIndex(t, db, "u1", 1200, time.Now().UTC().Add(-time.Second))
	calls := 0
	w := organizerTestWorker(t, db, func(_ context.Context, req algo.PreferenceOrganizerRequest) (*algo.PreferenceOrganizerResponse, int, error) {
		calls++
		var task orm.ResourceUpdateTask
		db.Where("id = ?", strings.TrimPrefix(req.TaskID, PreferenceOrganizerAlgorithmPrefix)).Take(&task)
		var request PreferenceOrganizerRequest
		if err := json.Unmarshal(task.RequestJSON, &request); err != nil {
			t.Fatal(err)
		}
		if request.ExecutionState.FullProjectionChars != 1200 || request.ExecutionState.ETag == initial.ETag || req.ExpectedMaxChars != 1000 {
			t.Fatalf("execution did not refresh snapshot: %+v", request)
		}
		final := seedOrganizerIndex(t, db, "u1", 1100, time.Now().UTC())
		return &algo.PreferenceOrganizerResponse{
			Status: "success", TaskID: req.TaskID, Outcome: "organized_with_remaining",
			Result: map[string]any{"target_reached": false, "final_state": final},
		}, http.StatusOK, nil
	})
	if result, err := w.RunOnce(t.Context()); err != nil || result.Done != 1 {
		t.Fatalf("worker: %+v %v", result, err)
	}
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	latest := func() preferenceOrganizerTaskResponse {
		req := httptest.NewRequest(http.MethodGet, "/memory/preferences:organize", nil)
		req.Header.Set("X-User-Id", "u1")
		rec := httptest.NewRecorder()
		GetLatestPreferenceOrganizer(rec, req)
		var body struct {
			Data preferenceOrganizerTaskResponse `json:"data"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatal(rec.Code, rec.Body.String())
		}
		return body.Data
	}
	if resp := latest(); resp.ResultValidity != "current" || resp.CurrentState.FullProjectionChars != 1100 {
		t.Fatalf("fresh result: %+v", resp)
	}
	for i := 0; i < 3; i++ {
		_, admitted, err := EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC())
		if err != nil || admitted {
			t.Fatal("unchanged over-budget result was re-enqueued", admitted, err)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "900")
	if resp := latest(); resp.ResultValidity != "stale" || resp.InvalidationReason != "budget_changed" {
		t.Fatalf("budget change: %+v", resp)
	}
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	// Even restoring identical content is a new document revision.
	seedOrganizerIndex(t, db, "u1", 1100, time.Now().UTC().Add(time.Second))
	if resp := latest(); resp.ResultValidity != "stale" || resp.InvalidationReason != "preference_changed" {
		t.Fatalf("later revision: %+v", resp)
	}
	_, created, err = EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC())
	if err != nil || !created {
		t.Fatal("new revision should enqueue", created, err)
	}
}

func TestFailedOrganizerDoesNotResetRetryLimitOnScan(t *testing.T) {
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	db := newResourceUpdateTestDB(t)
	seedOrganizerIndex(t, db, "u1", 1258, time.Now().UTC().Add(-time.Minute))
	if _, _, err := EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	w := organizerTestWorker(t, db, func(_ context.Context, req algo.PreferenceOrganizerRequest) (*algo.PreferenceOrganizerResponse, int, error) {
		return nil, http.StatusBadGateway, fmt.Errorf("upstream unavailable")
	})
	if result, err := w.RunOnce(t.Context()); err != nil || result.Failed != 1 {
		t.Fatalf("worker: %+v %v", result, err)
	}
	for i := 0; i < 3; i++ {
		if _, created, err := EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC()); err != nil || created {
			t.Fatal("failed attempt was automatically recreated", created, err)
		}
	}
	if _, created, err := EnqueuePreferenceOrganizer(t.Context(), db, "u1", "manual", "retry", time.Now().UTC()); err != nil || !created {
		t.Fatal("manual retry should remain available", created, err)
	}
}

func TestNoSafeChangesSettlesAnOverBudgetState(t *testing.T) {
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	db := newResourceUpdateTestDB(t)
	state := seedOrganizerIndex(t, db, "u1", 1258, time.Now().UTC())
	if _, _, err := EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	w := organizerTestWorker(t, db, func(_ context.Context, req algo.PreferenceOrganizerRequest) (*algo.PreferenceOrganizerResponse, int, error) {
		return &algo.PreferenceOrganizerResponse{Status: "success", TaskID: req.TaskID, Outcome: "no_safe_changes",
			Result: map[string]any{"target_reached": false, "final_state": state}}, http.StatusOK, nil
	})
	if result, err := w.RunOnce(t.Context()); err != nil || result.Done != 1 {
		t.Fatalf("worker: %+v %v", result, err)
	}
	if result, err := NewScanner(db, Config{}, "scanner").RunOnce(t.Context()); err != nil || result.PreferenceTasksCreated != 0 {
		t.Fatalf("no-safe-changes should remain settled: %+v %v", result, err)
	}
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "900")
	if result, err := NewScanner(db, Config{}, "scanner").RunOnce(t.Context()); err != nil || result.PreferenceTasksCreated != 1 {
		t.Fatalf("a different budget should admit a new attempt: %+v %v", result, err)
	}
}

func TestInitializationFailureDoesNotCreateAnAutomaticRetryLoop(t *testing.T) {
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	db := newResourceUpdateTestDB(t)
	seedOrganizerIndex(t, db, "u1", 1258, time.Now().UTC())
	if _, _, err := EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Create().Before("gorm:create").Register("test:initialization_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "memory_current_entries" {
			tx.AddError(fmt.Errorf("injected initialization failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	w := organizerTestWorker(t, db, func(context.Context, algo.PreferenceOrganizerRequest) (*algo.PreferenceOrganizerResponse, int, error) {
		t.Fatal("initialization failure reached Algorithm")
		return nil, 0, nil
	})
	if result, err := w.RunOnce(t.Context()); err != nil || result.Failed != 1 {
		t.Fatalf("worker: %+v %v", result, err)
	}
	if _, created, err := EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC()); err != nil || created {
		t.Fatal("initialization failure reset retry limit", created, err)
	}
}

func TestExpiredOrganizerCannotAttachCompletionToANewRun(t *testing.T) {
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	db := newResourceUpdateTestDB(t)
	state := seedOrganizerIndex(t, db, "u1", 1258, time.Now().UTC())
	if _, _, err := EnqueuePreferenceOrganizerIfNeeded(t.Context(), db, "u1", 1000, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(db, Config{}, "worker")
	old, err := w.claimPending(t.Context(), time.Now().UTC())
	if err != nil || len(old) != 1 {
		t.Fatal(old, err)
	}
	if err := db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", old[0].ID).Update("locked_until", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := w.recoverExpiredRunning(t.Context(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	current, err := w.claimPending(t.Context(), time.Now().UTC())
	if err != nil || len(current) != 1 || current[0].RunID == old[0].RunID {
		t.Fatal(current, err)
	}
	raw, _ := json.Marshal(map[string]any{"outcome": "no_safe_changes", "final_state": state})
	if err := w.finishTask(t.Context(), old[0], taskOutcome{Status: "done", ResultJSON: raw}); err == nil {
		t.Fatal("expired run was allowed to finish")
	}
	var saved orm.ResourceUpdateTask
	if err := db.Where("id = ?", current[0].ID).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != "running" || saved.RunID != current[0].RunID || len(saved.ResultJSON) != 0 {
		t.Fatalf("late completion contaminated new run: %+v", saved)
	}
}

func TestScannerIsolatesCorruptPreferencesAndHistoricalResults(t *testing.T) {
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	db := newResourceUpdateTestDB(t)
	now := time.Now().UTC()
	seedOrganizerIndex(t, db, "a-corrupt", 1258, now)
	if err := db.Model(&orm.MemoryCurrentEntry{}).Where("user_id = ?", "a-corrupt").Update("content", []byte("invalid: [")).Error; err != nil {
		t.Fatal(err)
	}
	seedOrganizerIndex(t, db, "b-healthy", 1258, now)
	history := orm.ResourceUpdateTask{ID: "history", UserID: "b-healthy", TaskType: orm.ResourceUpdateTaskTypeOrganizePreference,
		Status: orm.ResourceUpdateTaskStatusDone, ResultJSON: json.RawMessage(`{"outcome":"no_safe_changes"}`), CreatedAt: now.Add(-time.Hour)}
	insertTask(t, db, history)
	if resp := preferenceOrganizerResponseWithWaiting(t.Context(), db, history); resp.ResultValidity != "unknown" {
		t.Fatalf("unverified history: %+v", resp)
	}
	result, err := NewScanner(db, Config{}, "scanner").RunOnce(t.Context())
	if err != nil || result.PreferenceTasksCreated != 1 {
		t.Fatalf("healthy user was blocked: %+v %v", result, err)
	}
}
