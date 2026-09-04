package algo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lazymind/core/common"
)

func ReviewSkill(ctx context.Context, req SkillReviewRequest) (*SkillReviewResponse, int, error) {
	if req.ModelConfigs == nil {
		req.ModelConfigs = map[string]any{}
	}
	var out SkillReviewResponse
	status, err := postReviewJSON(ctx, "/api/chat/skill_review", req, &out)
	if err != nil {
		return nil, status, err
	}
	return &out, status, nil
}

func OrganizeSkill(ctx context.Context, req SkillOrganizeRequest) (*SkillOrganizeResponse, int, error) {
	var out SkillOrganizeResponse
	status, err := postReviewJSON(ctx, "/api/chat/skill_organize", req, &out)
	if err != nil {
		return nil, status, err
	}
	return &out, status, nil
}

func ReviewMemory(ctx context.Context, req MemoryReviewRequest) (*MemoryReviewResponse, int, error) {
	var out MemoryReviewResponse
	status, err := postReviewJSON(ctx, "/api/chat/memory_review", req, &out)
	if err != nil {
		return nil, status, err
	}
	return &out, status, nil
}

func OrganizePreference(ctx context.Context, req PreferenceOrganizerRequest) (*PreferenceOrganizerResponse, int, error) {
	var out PreferenceOrganizerResponse
	status, err := postReviewJSON(ctx, "/api/chat/preference_organize", req, &out)
	if err != nil {
		return nil, status, err
	}
	return &out, status, nil
}

func postReviewJSON(ctx context.Context, path string, req any, out any) (int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("marshal review request: %w", err)
	}
	timeout := 10 * time.Minute
	if path == "/api/chat/preference_organize" {
		timeout = 30 * time.Minute
	}
	respBytes, status, err := common.HTTPPostWithTimeout(ctx, common.JoinURL(common.ChatServiceEndpoint(), path), "application/json", body, timeout)
	if err != nil {
		return status, err
	}
	if status != 200 {
		return status, fmt.Errorf("review endpoint returned HTTP %d: %s", status, respBytes)
	}
	if len(respBytes) == 0 {
		return status, fmt.Errorf("review endpoint returned empty body")
	}
	if err := json.Unmarshal(respBytes, out); err != nil {
		return status, fmt.Errorf("unmarshal review response: %w", err)
	}
	return status, nil
}

func IsMaintenanceBusy(err error) bool {
	return err != nil && strings.Contains(err.Error(), "maintenance_busy")
}
