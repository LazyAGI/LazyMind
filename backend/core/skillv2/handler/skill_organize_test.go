package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

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
	if reservation.Status != orm.ResourceUpdateTaskStatusDone {
		t.Fatalf("organize reservation status = %q, want done", reservation.Status)
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
