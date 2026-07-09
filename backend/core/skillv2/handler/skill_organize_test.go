package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
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

	t.Setenv("LAZYMIND_CORE_SELF_URL", "http://core.test")
	db := testutil.NewTestDB(t)
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
				Status:    "running",
				RequestID: req.RequestID,
				TaskID:    "org_smoke_20260707183512345678",
			},
		}, http.StatusOK, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(`{
		"requestid": "org_smoke",
		"user_id": "ignored",
		"skills": [" /skills/research/source-triage/ "],
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
	if strings.Join(captured.Skills, ",") != "skills/research/source-triage" {
		t.Fatalf("unexpected forwarded skills: %#v", captured.Skills)
	}
	if captured.FSBaseURL != "http://core.test" {
		t.Fatalf("fs_base_url = %q", captured.FSBaseURL)
	}
	if captured.ArtifactDir != "tmp/a-skill-org" {
		t.Fatalf("artifact_dir = %q", captured.ArtifactDir)
	}
	if _, ok := captured.ModelConfigs["llm"]; !ok {
		t.Fatalf("expected core-loaded model config, got %#v", captured.ModelConfigs)
	}

	var out common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := out.Data.(map[string]any)
	if !ok || data["taskid"] != "org_smoke_20260707183512345678" || data["status"] != "running" {
		t.Fatalf("unexpected response: %#v", out)
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
