package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/skillv2/remotefs"
	"lazymind/core/skillv2/taskguard"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestCancelDeepOrganizeRestoresSourcesBetweenMutations(t *testing.T) {
	for _, stop := range []string{"rename", "rename_write", "merge_trash", "rename_write_trash_metadata", "rename_conflict", "rename_write_trash_conflict", "rename_committed", "rename_legacy"} {
		t.Run(stop, func(t *testing.T) {
			db := testutil.NewTestDB(t)
			withHandlerDB(t, db)
			ctx := context.Background()
			before := map[string]orm.SkillV2Skill{}
			for _, id := range []string{"a", "b", "other"} {
				testutil.SeedSkillWithRevision(t, db, id, "rev_"+id)
				if err := db.Table("skills").Where("id = ?", id).Updates(map[string]any{"category": "internal", "skill_name": id, "relative_root": "internal/" + id}).Error; err != nil {
					t.Fatal(err)
				}
				content := testutil.SkillMD(id, "original "+id)
				if err := db.Table("skill_blobs").Where("hash = ?", "h_skill_rev_"+id).Update("content", []byte(content)).Error; err != nil {
					t.Fatal(err)
				}
				var row orm.SkillV2Skill
				if err := db.Where("id = ?", id).Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				before[id] = row
				testutil.MustCreate(t, db, &orm.SkillSearchIndex{SkillID: id, OwnerUserID: "user_001", HeadRevisionID: "rev_" + id, Content: "original index " + id, UpdatedAt: testutil.TimeFixture()})
			}
			testutil.MustCreate(t, db, &orm.SkillMarketInstall{MarketItemID: "market_b", UserID: "user_001", SkillID: "b", CreatedAt: testutil.TimeFixture(), UpdatedAt: testutil.TimeFixture()})
			task, err := createSkillOrganizeReservation(ctx, db.DB, "user_001", "org_deep_cancel")
			if err != nil {
				t.Fatal(err)
			}
			if err := noteSkillOrganizeAccepted(ctx, db.DB, task.ID, "org_deep_run", skillOrganizeSubmitRequest{RequestID: "org_deep_cancel", Mode: "deep", Skills: []string{"skills/internal/a", "skills/internal/b"}}, []string{"a", "b"}); err != nil {
				t.Fatal(err)
			}
			testutil.MustCreate(t, db, &orm.SkillReviewStats{ID: "org_deep_run", RequestID: "org_deep_cancel", UserID: "user_001", Status: "organize_apply", StartedAt: "2026-09-29", Summary: `{}`})
			h := remotefs.NewHandler(remotefs.HandlerDeps{DB: db.DB, BlobStore: remotefs.NewBlobStore(db.DB, remotefs.NewLocalObjectStore(t.TempDir()))})
			mutate := func(handler http.HandlerFunc, method, suffix, body string) {
				t.Helper()
				rec := httptest.NewRecorder()
				handler(rec, httptest.NewRequest(method, "/remote-fs?user_id=user_001&task_id=org_deep_cancel"+suffix, strings.NewReader(body)))
				if rec.Code != http.StatusOK {
					t.Fatalf("mutation=%d %s", rec.Code, rec.Body)
				}
			}
			target := "a"
			if strings.HasPrefix(stop, "rename") {
				mutate(h.Move, http.MethodPost, "", `{"from":"skills/internal/a","to":"skills/internal/merged"}`)
				target = "merged"
			}
			fallback := strings.HasSuffix(stop, "conflict") || stop == "rename_committed" || stop == "rename_legacy"
			if stop != "rename" && (!fallback || strings.Contains(stop, "write")) {
				mutate(h.Content, http.MethodPut, "&path=skills/internal/"+target+"/SKILL.md", testutil.SkillMD(target, "merged a and b"))
			}
			if strings.Contains(stop, "trash") {
				mutate(h.Trash, http.MethodPost, "", `{"path":"skills/internal/b"}`)
			}
			if strings.Contains(stop, "metadata") {
				req := httptest.NewRequest(http.MethodPost, "/internal/skills:metadata:update", strings.NewReader(fmt.Sprintf(`{"task_id":"org_deep_cancel","updates":[{"skill_key":"internal/%s","field":"merged","tags":["new"],"aliases":["new"],"keywords":["new"]}]}`, target)))
				req.Header.Set("X-User-Id", "user_001")
				rec := httptest.NewRecorder()
				InternalMetadataUpdate(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("metadata=%d %s", rec.Code, rec.Body)
				}
			}
			if strings.HasSuffix(stop, "conflict") {
				// Another creator can reclaim the original name before cancel.
				testutil.SeedSkillWithRevision(t, db, "replacement", "rev_replacement")
				if err := db.Table("skills").Where("id = ?", "replacement").Updates(map[string]any{"category": "internal", "skill_name": "a", "relative_root": "internal/a"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if stop == "rename_committed" {
				if err := db.Table("skills").Where("id = ?", "a").Update("head_revision_id", "review_committed_head").Error; err != nil {
					t.Fatal(err)
				}
			}
			if stop == "rename_legacy" {
				if err := db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", task.ID).UpdateColumn("result_json", json.RawMessage(`{}`)).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := cancelSkillOrganizeReservation(ctx, db.DB, "user_001", "org_deep_cancel"); err != nil {
				t.Fatal(err)
			}
			if fallback {
				var skill orm.SkillV2Skill
				if err := db.Where("id = ?", "a").Take(&skill).Error; err != nil {
					t.Fatal(err)
				}
				if skill.IsEnabled || skill.CallMode != "manual" {
					t.Fatalf("unresolved identity exposed to runtime: %#v", skill)
				}
				var draft orm.SkillV2Draft
				if err := db.Where("skill_id = ?", "a").Take(&draft).Error; err != nil {
					t.Fatal(err)
				}
				if draft.TaskID != "" || draft.DraftStatus != "pending_confirm" {
					t.Fatalf("draft not released for review: %#v", draft)
				}
				var blobContent []byte
				if err := db.Table("skill_draft_entries AS e").Select("b.content").Joins("JOIN skill_blobs b ON b.hash = e.blob_hash").Where("e.skill_id = ? AND e.path = ?", "a", "SKILL.md").Row().Scan(&blobContent); err != nil {
					t.Fatal(err)
				}
				body := "original a"
				if strings.Contains(stop, "write") {
					body = "merged a and b"
				}
				if !strings.Contains(string(blobContent), "name: merged") || !strings.Contains(string(blobContent), body) {
					t.Fatalf("lost coherent original draft: %s", blobContent)
				}
				if strings.Contains(stop, "trash") {
					var source orm.SkillV2Skill
					if err := db.Where("id = ?", "b").Take(&source).Error; err != nil {
						t.Fatal(err)
					}
					if source.DeletedAt == nil || source.HeadRevisionID == nil || *source.HeadRevisionID != "rev_b" {
						t.Fatalf("source no longer recoverable or partial rollback leaked: %#v", source)
					}
					var content []byte
					if err := db.Table("skill_blobs").Select("content").Where("hash = ?", "h_skill_rev_b").Row().Scan(&content); err != nil || string(content) != testutil.SkillMD("b", "original b") {
						t.Fatalf("B content lost: %s %v", content, err)
					}
				}
				var cancelled orm.SkillReviewStats
				if err := db.Where("id = ?", "org_deep_run").Take(&cancelled).Error; err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(cancelled.Summary, `"pending_review":true`) {
					t.Fatalf("missing recovery details: %s", cancelled.Summary)
				}
				decision, err := taskguard.EvaluateSkillOperation(ctx, db.DB, nil, taskguard.SkillOperationRequest{UserID: "user_001", SkillIDs: []string{"other"}, Operation: taskguard.TriggerSkillOrganize})
				if err != nil || !decision.Allowed {
					t.Fatalf("OTHER organize blocked: %#v %v", decision, err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"code":0,"data":{"cancelled":false}}`) }))
				t.Cleanup(server.Close)
				t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
				req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize:cancel?requestid=org_deep_cancel", nil)
				req.Header.Set("X-User-Id", "user_001")
				rec := httptest.NewRecorder()
				CancelSkillOrganize(rec, req)
				var response struct {
					Data struct {
						PendingReview bool `json:"pending_review"`
					} `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || !response.Data.PendingReview || rec.Code != http.StatusOK {
					t.Fatalf("cancel response hides retained changes: %d %s %v", rec.Code, rec.Body, err)
				}
				return
			}
			for _, id := range []string{"a", "b", "other"} {
				var after orm.SkillV2Skill
				if err := db.Where("id = ?", id).Take(&after).Error; err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before[id], after) {
					t.Errorf("source %s changed after cancellation: before=%#v after=%#v", id, before[id], after)
				}
				read := httptest.NewRecorder()
				h.Content(read, httptest.NewRequest(http.MethodGet, "/remote-fs?user_id=user_001&task_id=reader&path=skills/internal/"+id+"/SKILL.md", nil))
				if read.Code != http.StatusOK || read.Body.String() != testutil.SkillMD(id, "original "+id) {
					t.Errorf("original %s inaccessible: %d %s", id, read.Code, read.Body)
				}
				var index orm.SkillSearchIndex
				if err := db.Where("skill_id = ?", id).Take(&index).Error; err != nil || index.Content != "original index "+id {
					t.Errorf("original index %s not restored: %#v %v", id, index, err)
				}
			}
			if n := testutil.CountRows(t, db, "skill_market_installs", "skill_id = ?", "b"); n != 1 {
				t.Errorf("B's install association lost")
			}
			if n := testutil.CountRows(t, db, "skill_draft_entries", "skill_id IN ?", []string{"a", "b"}); n != 0 {
				t.Errorf("partial drafts remain: %d", n)
			}
			for _, ids := range [][]string{{"a", "b"}, {"other"}} {
				decision, err := taskguard.EvaluateSkillOperation(ctx, db.DB, nil, taskguard.SkillOperationRequest{UserID: "user_001", SkillIDs: ids, Operation: taskguard.TriggerSkillOrganize})
				if err != nil || !decision.Allowed {
					t.Errorf("next organize blocked: %#v %v", decision, err)
				}
			}
		})
	}
}

func TestCancelSkillOrganizeReleasesReservationAndFencesLateWrites(t *testing.T) {
	db := testutil.NewTestDB(t)
	withHandlerDB(t, db)
	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("skill%d", i)
		testutil.SeedSkillWithRevision(t, db, id, fmt.Sprintf("rev%d", i))
		setSkillOrganizeCategory(t, db, id, "internal", "internal/"+id)
		if err := db.Table("skills").Where("id = ?", id).Update("skill_name", id).Error; err != nil {
			t.Fatal(err)
		}
	}
	testutil.SeedTextBlob(t, db, "partial", "partial generated content")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "partial")
	if err := db.Table("skill_drafts").Where("skill_id = ?", "skill1").Update("task_id", "org_cancel").Error; err != nil {
		t.Fatal(err)
	}
	reservation, err := createSkillOrganizeReservation(context.Background(), db.DB, "user_001", "org_cancel")
	if err != nil {
		t.Fatal(err)
	}
	if err := noteSkillOrganizeAccepted(context.Background(), db.DB, reservation.ID, "org_cancel_run", skillOrganizeSubmitRequest{RequestID: "org_cancel", Skills: []string{"skills/internal/skill1", "skills/internal/skill2"}}, []string{"skill1", "skill2"}); err != nil {
		t.Fatal(err)
	}
	testutil.MustCreate(t, db, &orm.SkillReviewStats{ID: "org_cancel_run", RequestID: "org_cancel", UserID: "user_001", Status: "organize_plan", StartedAt: "2026-09-29", Summary: `{}`})
	// The algorithm may hang indefinitely. The notification context must abort
	// both the client wait and this server handler without a leaked goroutine.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		<-ctx.Done()
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize:cancel?requestid=org_cancel", nil)
		req.Header.Set("X-User-Id", "user_001")
		rec := httptest.NewRecorder()
		started := time.Now()
		CancelSkillOrganize(rec, req)
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("cancel waited for stalled algorithm: %s", elapsed)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("cancel=%d %s", rec.Code, rec.Body)
		}
	}
	var stats orm.SkillReviewStats
	if err := db.Where("id = ?", "org_cancel_run").Take(&stats).Error; err != nil {
		t.Fatal(err)
	}
	if stats.Status != "cancelled" {
		t.Fatalf("stats=%#v", stats)
	}
	reservation = orm.ResourceUpdateTask{ID: reservation.ID}
	if err := db.Where("id = ?", reservation.ID).Take(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != orm.ResourceUpdateTaskStatusSkipped || reservation.LockedUntil != nil {
		t.Fatalf("reservation=%#v", reservation)
	}
	if n := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); n != 0 {
		t.Fatalf("partial entries=%d", n)
	}
	decision, err := taskguard.EvaluateSkillOperation(context.Background(), db.DB, nil, taskguard.SkillOperationRequest{UserID: "user_001", SkillIDs: []string{"skill1", "skill2"}, Operation: taskguard.TriggerSkillOrganize})
	if err != nil || !decision.Allowed {
		t.Fatalf("retry admission=%#v %v", decision, err)
	}
	if _, err := createSkillOrganizeReservation(context.Background(), db.DB, "user_001", "org_next"); err != nil {
		t.Fatal(err)
	}
	testutil.MustCreate(t, db, &orm.SkillReviewStats{ID: "org_next_run", RequestID: "org_next", UserID: "user_001", Status: "organize_plan", StartedAt: "2026-09-30", Summary: `{}`})
	h := remotefs.NewHandler(remotefs.HandlerDeps{DB: db.DB, BlobStore: remotefs.NewBlobStore(db.DB, remotefs.NewLocalObjectStore(t.TempDir()))})
	write := httptest.NewRecorder()
	h.Content(write, httptest.NewRequest(http.MethodPut, "/remote_fs/file?path=skills/internal/skill1/SKILL.md&user_id=user_001&task_id=org_cancel", strings.NewReader("zombie")))
	if write.Code == http.StatusOK {
		t.Fatal("cancelled task wrote through new running task")
	}
	metadata := httptest.NewRecorder()
	metadataReq := httptest.NewRequest(http.MethodPost, "/internal/skills:metadata:update", strings.NewReader(`{"task_id":"org_cancel","updates":[{"skill_key":"internal/skill1","field":"zombie"}]}`))
	metadataReq.Header.Set("X-User-Id", "user_001")
	InternalMetadataUpdate(metadata, metadataReq)
	if metadata.Code == http.StatusOK {
		t.Fatal("cancelled task updated metadata")
	}
	nextWrite := httptest.NewRecorder()
	h.Content(nextWrite, httptest.NewRequest(http.MethodPut, "/remote_fs/file?path=skills/internal/skill1/SKILL.md&user_id=user_001&task_id=org_next", strings.NewReader("next task draft")))
	if nextWrite.Code != http.StatusOK {
		t.Fatalf("same skill retry=%d %s", nextWrite.Code, nextWrite.Body)
	}
	metadata = httptest.NewRecorder()
	metadataReq = httptest.NewRequest(http.MethodPost, "/internal/skills:metadata:update", strings.NewReader(`{"task_id":"org_next","updates":[{"skill_key":"internal/skill1","field":"next"}]}`))
	metadataReq.Header.Set("X-User-Id", "user_001")
	InternalMetadataUpdate(metadata, metadataReq)
	if metadata.Code != http.StatusOK {
		t.Fatalf("new metadata=%d %s", metadata.Code, metadata.Body)
	}
}

func TestCancelBeforeOrganizeAcceptanceDominatesLateAcknowledgments(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	testutil.MustCreate(t, db, &orm.SkillReviewStats{ID: "org_early", RequestID: "org_early", UserID: "other_user", Status: "organize_plan", StartedAt: "2026-09-29", Summary: `{}`})
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "user_draft", "user content")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "user_draft")
	task, err := createSkillOrganizeReservation(ctx, db.DB, "user_001", "org_early")
	if err != nil {
		t.Fatal(err)
	}
	if err := cancelSkillOrganizeReservation(ctx, db.DB, "user_001", "org_early"); err != nil {
		t.Fatal(err)
	}
	if n := testutil.CountRows(t, db, "skill_review_stats", "id = ? AND userid = ? AND status = ?", "org_early", "other_user", "organize_plan"); n != 1 {
		t.Fatal("cancellation changed another user's run")
	}
	// A late acknowledgment and then a failed submission must not alter the
	// cancelled request or point it away from its cancellation tombstone.
	_ = noteSkillOrganizeAccepted(ctx, db.DB, task.ID, "org_early_late", skillOrganizeSubmitRequest{RequestID: "org_early"}, nil)
	_ = finishSkillOrganizeReservation(ctx, db.DB, task.ID, orm.ResourceUpdateTaskStatusFailed, "", fmt.Errorf("late failure"))
	var row orm.ResourceUpdateTask
	if err := db.Where("id = ?", task.ID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != orm.ResourceUpdateTaskStatusSkipped || row.ResultID != "" || row.ErrorCode != "skill_organize_cancelled" {
		t.Fatalf("late acknowledgment changed cancellation: %#v", row)
	}
	if n := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); n != 1 {
		t.Fatal("cancellation deleted an unowned draft")
	}
	// Simulate an old algorithm binary inserting a different row after cancel.
	testutil.MustCreate(t, db, &orm.SkillReviewStats{ID: "org_early_late", RequestID: "org_early", UserID: "user_001", Status: "organize_apply", StartedAt: "2026-09-30", Summary: `{}`})
	decision, err := taskguard.EvaluateSkillOperation(ctx, db.DB, nil, taskguard.SkillOperationRequest{UserID: "user_001", Operation: taskguard.TriggerSkillReview})
	if err != nil || !decision.Allowed {
		t.Fatalf("cancelled logical request blocked next task: %#v %v", decision, err)
	}
	if _, err := createSkillOrganizeReservation(ctx, db.DB, "user_001", "org_after_early"); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitSkillOrganizeForwardsCoreManagedFields(t *testing.T) {
	oldCaller := skillOrganizeCaller
	oldLoader := skillOrganizeLoadModelConfig
	oldDB := store.DB()
	t.Cleanup(func() {
		skillOrganizeCaller = oldCaller
		skillOrganizeLoadModelConfig = oldLoader
		store.Init(oldDB, nil, nil)
	})

	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	testutil.SeedSkillWithRevision(t, db, "skill3", "rev3")
	setSkillOrganizeCategory(t, db, "skill1", "internal", "internal/论文精读")
	setSkillOrganizeCategory(t, db, "skill2", "external", "external/论文精读-skill2")
	setSkillOrganizeCategory(t, db, "skill3", "internal", "internal/第二技能")
	testutil.SeedTextBlob(t, db, "external_draft_hash", "external draft")
	testutil.SeedDraftEntry(t, db, "skill2", "SKILL.md", "upsert", "file", "external_draft_hash")
	store.Init(db.DB, nil, nil)

	var captured algo.SkillOrganizeRequest
	skillOrganizeLoadModelConfig = func(_ context.Context, _ *gorm.DB, userID string) (map[string]any, error) {
		if userID != "user_001" {
			t.Fatalf("load model config user_id = %q", userID)
		}
		return map[string]any{"llm": map[string]any{"model": "m"}}, nil
	}
	skillOrganizeCaller = func(_ context.Context, req algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
		captured = req
		return &algo.SkillOrganizeResponse{
			Code: 0,
			Data: algo.SkillOrganizeData{
				Status:    "pending",
				RequestID: req.RequestID,
				TaskID:    "org_smoke_20260707183512345678",
			},
		}, http.StatusOK, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(`{
		"requestid": "org_smoke",
		"mode": "deep",
		"user_id": "ignored",
		"skills": [" /skills/internal/论文精读/ ", "skills/external/论文精读-skill2", "skills/internal/第二技能"],
		"fs_base_url": "http://frontend-should-not-win",
		"artifact_dir": "tmp/a-skill-org",
		"model_configs": {"llm": {"api_key": "frontend-should-not-win"}}
	}`))
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()

	SubmitSkillOrganize(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if captured.UserID != "user_001" || captured.RequestID != "org_smoke" {
		t.Fatalf("unexpected forwarded request identity: %#v", captured)
	}
	if strings.Join(captured.Skills, ",") != "internal/论文精读,internal/第二技能" {
		t.Fatalf("unexpected forwarded skills: %#v", captured.Skills)
	}
	if captured.Mode != "deep" {
		t.Fatalf("mode=%q, want deep", captured.Mode)
	}
	if captured.ArtifactDir != "tmp/a-skill-org" {
		t.Fatalf("artifact_dir = %q", captured.ArtifactDir)
	}
	if _, ok := captured.ModelConfigs["llm"]; !ok {
		t.Fatalf("expected core-loaded model config, got %#v", captured.ModelConfigs)
	}
	var reservation orm.ResourceUpdateTask
	if err := db.Where("task_type = ?", orm.ResourceUpdateTaskTypeOrganizeSkill).Take(&reservation).Error; err != nil {
		t.Fatalf("load organize reservation: %v", err)
	}
	if reservation.Status != orm.ResourceUpdateTaskStatusRunning {
		t.Fatalf("organize reservation status = %q, want running", reservation.Status)
	}
	if reservation.ResultID != "org_smoke_20260707183512345678" {
		t.Fatalf("organize reservation result_id = %q", reservation.ResultID)
	}

	var out common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := out.Data.(map[string]any)
	if !ok || data["taskid"] != "org_smoke_20260707183512345678" || data["status"] != "pending" {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestSubmitSkillOrganizePrefersEvolutionLLM(t *testing.T) {
	oldCaller := skillOrganizeCaller
	oldLoader := skillOrganizeLoadModelConfig
	oldResolve := skillOrganizeResolveChatLLM
	oldDB := store.DB()
	t.Cleanup(func() {
		skillOrganizeCaller = oldCaller
		skillOrganizeLoadModelConfig = oldLoader
		skillOrganizeResolveChatLLM = oldResolve
		store.Init(oldDB, nil, nil)
	})

	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	setSkillOrganizeCategory(t, db, "skill1", "internal", "internal/a")
	setSkillOrganizeCategory(t, db, "skill2", "internal", "internal/b")
	store.Init(db.DB, nil, nil)

	skillOrganizeLoadModelConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"evo_llm": map[string]any{"source": "openai", "model": "evo-model"}}, nil
	}
	skillOrganizeResolveChatLLM = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		t.Fatal("chat fallback must not be used when evo_llm is configured")
		return nil, nil
	}
	var captured algo.SkillOrganizeRequest
	skillOrganizeCaller = func(_ context.Context, req algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
		captured = req
		return &algo.SkillOrganizeResponse{
			Code: 0,
			Data: algo.SkillOrganizeData{Status: "pending", RequestID: req.RequestID, TaskID: "org_overlay_task"},
		}, http.StatusOK, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(`{
		"requestid": "org_overlay",
		"mode": "light",
		"skills": ["skills/internal/a", "skills/internal/b"]
	}`))
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()
	SubmitSkillOrganize(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	llm, _ := captured.ModelConfigs["llm"].(map[string]any)
	if llm["model"] != "evo-model" || llm["source"] != "openai" {
		t.Fatalf("llm overlay = %#v", captured.ModelConfigs)
	}
	if _, ok := captured.ModelConfigs["evo_llm"]; !ok {
		t.Fatalf("expected evo_llm to remain, got %#v", captured.ModelConfigs)
	}
}

func TestSubmitSkillOrganizeFallsBackToChatLLM(t *testing.T) {
	oldCaller := skillOrganizeCaller
	oldLoader := skillOrganizeLoadModelConfig
	oldResolve := skillOrganizeResolveChatLLM
	oldDB := store.DB()
	t.Cleanup(func() {
		skillOrganizeCaller = oldCaller
		skillOrganizeLoadModelConfig = oldLoader
		skillOrganizeResolveChatLLM = oldResolve
		store.Init(oldDB, nil, nil)
	})

	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	setSkillOrganizeCategory(t, db, "skill1", "internal", "internal/a")
	setSkillOrganizeCategory(t, db, "skill2", "internal", "internal/b")
	store.Init(db.DB, nil, nil)

	skillOrganizeLoadModelConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"embed_main": map[string]any{"source": "openai", "model": "embed"}}, nil
	}
	skillOrganizeResolveChatLLM = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"source": "openai", "model": "chat-default", "base_url": "http://chat/v1/"}, nil
	}
	var captured algo.SkillOrganizeRequest
	skillOrganizeCaller = func(_ context.Context, req algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
		captured = req
		return &algo.SkillOrganizeResponse{
			Code: 0,
			Data: algo.SkillOrganizeData{Status: "pending", RequestID: req.RequestID, TaskID: "org_fallback_task"},
		}, http.StatusOK, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(`{
		"requestid": "org_fallback",
		"mode": "light",
		"skills": ["skills/internal/a", "skills/internal/b"]
	}`))
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()
	SubmitSkillOrganize(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	llm, _ := captured.ModelConfigs["llm"].(map[string]any)
	if llm["model"] != "chat-default" {
		t.Fatalf("chat fallback llm = %#v", captured.ModelConfigs)
	}
}

func TestSubmitSkillOrganizeFiltersNonInternalSkills(t *testing.T) {
	oldCaller := skillOrganizeCaller
	oldLoader := skillOrganizeLoadModelConfig
	oldDB := store.DB()
	t.Cleanup(func() {
		skillOrganizeCaller = oldCaller
		skillOrganizeLoadModelConfig = oldLoader
		store.Init(oldDB, nil, nil)
	})

	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	setSkillOrganizeCategory(t, db, "skill1", "internal", "internal/another-skill")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill2").Updates(map[string]any{
		"category":      "internal",
		"relative_root": "internal/generated-skill",
	}).Error; err != nil {
		t.Fatalf("mark internal skill: %v", err)
	}
	store.Init(db.DB, nil, nil)

	skillOrganizeLoadModelConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"llm": map[string]any{"model": "m"}}, nil
	}
	var captured algo.SkillOrganizeRequest
	skillOrganizeCaller = func(_ context.Context, req algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
		captured = req
		return &algo.SkillOrganizeResponse{Code: 0, Data: algo.SkillOrganizeData{
			Status: "pending", RequestID: req.RequestID, TaskID: "org_filtered_task",
		}}, http.StatusOK, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(`{
		"requestid":"org_filtered", "mode":"deep",
		"skills":["skills/creative/art_style","skills/vcs/git-guide","skills/internal/another-skill","skills/external/downloaded","skills/internal/generated-skill"]
	}`))
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()

	SubmitSkillOrganize(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Join(captured.Skills, ",") != "internal/another-skill,internal/generated-skill" {
		t.Fatalf("forwarded skills = %#v, want only internal skills", captured.Skills)
	}
}

func TestSubmitSkillOrganizeRejectsSingleInternalSkill(t *testing.T) {
	oldCaller := skillOrganizeCaller
	oldDB := store.DB()
	t.Cleanup(func() {
		skillOrganizeCaller = oldCaller
		store.Init(oldDB, nil, nil)
	})

	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	setSkillOrganizeCategory(t, db, "skill1", "internal", "internal/only-skill")
	store.Init(db.DB, nil, nil)

	called := false
	skillOrganizeCaller = func(_ context.Context, _ algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
		called = true
		return nil, 0, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(`{
		"requestid": "org_single_internal", "mode":"deep",
		"skills": ["skills/internal/only-skill"]
	}`))
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()

	SubmitSkillOrganize(rec, req)

	if rec.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "skill_organize_insufficient_internal_skills") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	var count int64
	if err := db.Model(&orm.ResourceUpdateTask{}).
		Where("task_type = ?", orm.ResourceUpdateTaskTypeOrganizeSkill).
		Count(&count).Error; err != nil {
		t.Fatalf("count organize reservations: %v", err)
	}
	if count != 0 {
		t.Fatalf("organize reservation count = %d, want 0", count)
	}
}

func TestSubmitSkillOrganizeRejectsRequestWithoutInternalSkills(t *testing.T) {
	oldCaller := skillOrganizeCaller
	oldDB := store.DB()
	t.Cleanup(func() {
		skillOrganizeCaller = oldCaller
		store.Init(oldDB, nil, nil)
	})

	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	setSkillOrganizeCategory(t, db, "skill1", "external", "external/论文精读")
	store.Init(db.DB, nil, nil)

	called := false
	skillOrganizeCaller = func(_ context.Context, _ algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
		called = true
		return nil, 0, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(`{
		"requestid": "org_external_only", "mode":"deep",
		"skills": ["skills/external/论文精读"]
	}`))
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()

	SubmitSkillOrganize(rec, req)

	if rec.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "skill_organize_no_internal_skills") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	var count int64
	if err := db.Model(&orm.ResourceUpdateTask{}).
		Where("task_type = ?", orm.ResourceUpdateTaskTypeOrganizeSkill).
		Count(&count).Error; err != nil {
		t.Fatalf("count organize reservations: %v", err)
	}
	if count != 0 {
		t.Fatalf("organize reservation count = %d, want 0", count)
	}
}

func TestNormalizeSkillOrganizeRequestAddsTaskModePrefix(t *testing.T) {
	normalized, err := normalizeSkillOrganizeRequest(skillOrganizeSubmitRequest{
		RequestID: "request-1",
		Skills:    []string{"skills/cat/skill"},
	})
	if err != nil {
		t.Fatalf("normalize request: %v", err)
	}
	if normalized.RequestID != "org_request-1" {
		t.Fatalf("requestid = %q, want org_request-1", normalized.RequestID)
	}
}

func TestSkillMaintenanceAdmissionAllowsOnlyOneActiveReservation(t *testing.T) {
	db := testutil.NewTestDB(t)
	now := time.Now().UTC()
	start := make(chan struct{})
	errs := make(chan error, 2)

	go func() {
		<-start
		errs <- db.Create(&orm.ResourceUpdateTask{
			ID:           "review-reservation",
			TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
			ResourceType: orm.ResourceUpdateResourceTypeSkill,
			UserID:       "user-1",
			TriggerType:  orm.ResourceUpdateTriggerTypeManual,
			TriggerID:    "review-reservation",
			Status:       orm.ResourceUpdateTaskStatusPending,
			NextRunAt:    now,
			CreatedAt:    now,
			UpdatedAt:    now,
		}).Error
	}()
	go func() {
		<-start
		_, err := createSkillOrganizeReservation(context.Background(), db.DB, "user-1", "org-reservation")
		errs <- err
	}()
	close(start)

	successes := 0
	for range 2 {
		if err := <-errs; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("active reservation successes = %d, want 1", successes)
	}
	var count int64
	if err := db.Model(&orm.ResourceUpdateTask{}).
		Where("user_id = ? AND task_type IN ? AND status IN ?", "user-1",
			[]string{orm.ResourceUpdateTaskTypeGenerateReview, orm.ResourceUpdateTaskTypeOrganizeSkill},
			[]string{orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning}).
		Count(&count).Error; err != nil {
		t.Fatalf("count active reservations: %v", err)
	}
	if count != 1 {
		t.Fatalf("active reservation count = %d, want 1", count)
	}
}

func TestNormalizeSkillOrganizeRequestRejectsTooManySkills(t *testing.T) {
	skills := make([]string, maxSkillOrganizeSkills+1)
	for i := range skills {
		skills[i] = "skills/cat/skill_" + string(rune('a'+i))
	}
	_, err := normalizeSkillOrganizeRequest(skillOrganizeSubmitRequest{
		RequestID: "org_many",
		Skills:    skills,
	})
	if err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("expected too many skills error, got %v", err)
	}
}

func setSkillOrganizeCategory(t *testing.T, db *testutil.TestDB, skillID, category, relativeRoot string) {
	t.Helper()
	if err := db.Model(&testutil.SkillRow{}).
		Where("id = ?", skillID).
		Updates(map[string]any{"category": category, "relative_root": relativeRoot}).Error; err != nil {
		t.Fatalf("update skill category: %v", err)
	}
}

func TestNormalizeSkillOrganizeMode(t *testing.T) {
	for _, mode := range []string{"", "light", "deep", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			var req skillOrganizeSubmitRequest
			if err := json.Unmarshal([]byte(`{"requestid":"mode-test","skills":["skills/internal/demo"],"mode":"`+mode+`"}`), &req); err != nil {
				t.Fatal(err)
			}
			got, err := normalizeSkillOrganizeRequest(req)
			if mode == "invalid" {
				if err == nil {
					t.Fatal("invalid mode accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(got)
			var fields map[string]any
			_ = json.Unmarshal(payload, &fields)
			want := mode
			if want == "" {
				want = "light"
			}
			if fields["mode"] != want {
				t.Fatalf("mode=%v, want %s", fields["mode"], want)
			}
		})
	}
}

func TestSkillOrganizeModeScopeAndOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		paths      []string
		want       string
		status     int
	}{
		{"light includes builtin legacy and external", "light", []string{"skills/search/builtin", "skills/external/imported"}, "search/builtin,external/imported", http.StatusOK},
		{"light cannot access another user", "light", []string{"skills/search/builtin", "skills/internal/other"}, "", http.StatusNotFound},
		{"deep excludes builtin even with internal category", "deep", []string{"skills/internal/builtin", "skills/internal/own"}, "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldCaller, oldLoader, oldDB := skillOrganizeCaller, skillOrganizeLoadModelConfig, store.DB()
			t.Cleanup(func() {
				skillOrganizeCaller = oldCaller
				skillOrganizeLoadModelConfig = oldLoader
				store.Init(oldDB, nil, nil)
			})
			db := testutil.NewTestDB(t)
			for i, root := range []string{"search/builtin", "external/imported", "internal/builtin", "internal/own", "internal/other"} {
				id := fmt.Sprintf("scope-%d", i)
				testutil.SeedSkillWithRevision(t, db, id, "rev-"+id)
				setSkillOrganizeCategory(t, db, id, strings.Split(root, "/")[0], root)
				updates := map[string]any{}
				if strings.HasSuffix(root, "builtin") {
					updates["origin_builtin_skill_uid"] = "bsk_scope" + id
				}
				if strings.HasSuffix(root, "other") {
					updates["owner_user_id"] = "other-user"
				}
				if len(updates) > 0 {
					if err := db.Table("skills").Where("id = ?", id).Updates(updates).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			store.Init(db.DB, nil, nil)
			skillOrganizeLoadModelConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) { return map[string]any{}, nil }
			captured := ""
			skillOrganizeCaller = func(_ context.Context, req algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
				captured = strings.Join(req.Skills, ",")
				return &algo.SkillOrganizeResponse{Code: 0, Data: algo.SkillOrganizeData{Status: "pending", RequestID: req.RequestID, TaskID: "org_scope_task"}}, http.StatusOK, nil
			}
			body, _ := json.Marshal(map[string]any{"requestid": "scope", "mode": tc.mode, "skills": tc.paths})
			req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(string(body)))
			req.Header.Set("X-User-Id", "user_001")
			rec := httptest.NewRecorder()
			SubmitSkillOrganize(rec, req)
			if rec.Code != tc.status || captured != tc.want {
				t.Fatalf("status=%d want=%d skills=%q want=%q body=%s", rec.Code, tc.status, captured, tc.want, rec.Body.String())
			}
		})
	}
}

func TestSubmitSkillOrganizeRejectsHiddenMarketplaceSource(t *testing.T) {
	for _, mode := range []string{"light", "deep"} {
		t.Run(mode, func(t *testing.T) {
			oldCaller, oldLoader := skillOrganizeCaller, skillOrganizeLoadModelConfig
			t.Cleanup(func() {
				skillOrganizeCaller = oldCaller
				skillOrganizeLoadModelConfig = oldLoader
			})
			db := testutil.NewTestDB(t)
			for _, id := range []string{"installed", "market-source"} {
				testutil.SeedSkillWithRevision(t, db, id, "rev-"+id)
				setSkillOrganizeCategory(t, db, id, "internal", "internal/"+id)
			}
			testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
				ID: "market-item", SourceSkillID: "market-source", Status: "published",
				CreatedAt: testutil.TimeFixture(), UpdatedAt: testutil.TimeFixture(),
			})
			withHandlerDB(t, db)
			skillOrganizeLoadModelConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
				return map[string]any{}, nil
			}
			called := false
			skillOrganizeCaller = func(_ context.Context, req algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
				called = true
				return &algo.SkillOrganizeResponse{Code: 0, Data: algo.SkillOrganizeData{
					Status: "pending", RequestID: req.RequestID, TaskID: "org_market_task",
				}}, http.StatusOK, nil
			}
			body := fmt.Sprintf(`{"requestid":"market-source","mode":%q,"skills":["skills/internal/installed","skills/internal/market-source"]}`, mode)
			req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(body))
			req.Header.Set("X-User-Id", "user_001")
			rec := httptest.NewRecorder()
			SubmitSkillOrganize(rec, req)
			if rec.Code != http.StatusNotFound || called {
				t.Fatalf("status=%d called=%v body=%s; hidden market source must be unavailable", rec.Code, called, rec.Body.String())
			}
			var reservations int64
			if err := db.Model(&orm.ResourceUpdateTask{}).Where("task_type = ?", orm.ResourceUpdateTaskTypeOrganizeSkill).Count(&reservations).Error; err != nil {
				t.Fatal(err)
			}
			if reservations != 0 {
				t.Fatalf("created %d organize reservations for hidden market source", reservations)
			}
		})
	}
}
