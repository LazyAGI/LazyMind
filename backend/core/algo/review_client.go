package algo

import (
	"context"
	"encoding/json"
	"fmt"

	"lazymind/core/common"
)

func ReviewSkill(ctx context.Context, req SkillReviewRequest) (*SkillReviewResponse, int, error) {
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

func postReviewJSON(ctx context.Context, path string, req any, out any) (int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("marshal review request: %w", err)
	}
	respBytes, status, err := common.HTTPPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), path), "application/json", body)
	if err != nil {
		return status, err
	}
	if status != 200 {
		return status, fmt.Errorf("review endpoint returned HTTP %d", status)
	}
	if len(respBytes) == 0 {
		return status, fmt.Errorf("review endpoint returned empty body")
	}
	if err := json.Unmarshal(respBytes, out); err != nil {
		return status, fmt.Errorf("unmarshal review response: %w", err)
	}
	return status, nil
}
