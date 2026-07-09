package handler

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/modelconfig"
)

const (
	maxSkillOrganizeSkills = 20
	skillOrganizeBaseDir   = "skills"
)

var (
	skillOrganizeCaller          = algo.OrganizeSkill
	skillOrganizeLoadModelConfig = modelconfig.LoadLLMConfig
)

type skillOrganizeSubmitRequest struct {
	RequestID   string   `json:"requestid"`
	Skills      []string `json:"skills"`
	ArtifactDir string   `json:"artifact_dir,omitempty"`
}

type skillOrganizeSubmitResponse struct {
	Status    string `json:"status"`
	RequestID string `json:"requestid"`
	TaskID    string `json:"taskid"`
}

func SubmitSkillOrganize(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req skillOrganizeSubmitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	normalized, err := normalizeSkillOrganizeRequest(req)
	if err != nil {
		replyError(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, status, err := submitSkillOrganize(r.Context(), db, userID, normalized)
	if err != nil {
		replyError(w, fmt.Sprintf("skill organize call failed: %v", err), http.StatusBadGateway)
		return
	}
	if status != http.StatusOK || resp == nil || resp.Code != 0 || resp.Data.Status != "running" ||
		resp.Data.RequestID != normalized.RequestID || strings.TrimSpace(resp.Data.TaskID) == "" {
		replyError(w, "skill organize returned unexpected response", http.StatusBadGateway)
		return
	}

	common.ReplyOK(w, skillOrganizeSubmitResponse{
		Status:    resp.Data.Status,
		RequestID: resp.Data.RequestID,
		TaskID:    resp.Data.TaskID,
	})
}

func submitSkillOrganize(ctx context.Context, db *gorm.DB, userID string, req skillOrganizeSubmitRequest) (*algo.SkillOrganizeResponse, int, error) {
	modelConfigs, err := skillOrganizeLoadModelConfig(ctx, db, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("load model configs: %w", err)
	}
	return skillOrganizeCaller(ctx, algo.SkillOrganizeRequest{
		RequestID:    req.RequestID,
		UserID:       userID,
		Skills:       req.Skills,
		FSBaseURL:    common.CoreSelfEndpoint(),
		ArtifactDir:  req.ArtifactDir,
		ModelConfigs: modelConfigs,
	})
}

func normalizeSkillOrganizeRequest(req skillOrganizeSubmitRequest) (skillOrganizeSubmitRequest, error) {
	req.RequestID = strings.TrimSpace(req.RequestID)
	if req.RequestID == "" {
		return req, fmt.Errorf("requestid is required")
	}
	req.ArtifactDir = strings.TrimSpace(req.ArtifactDir)
	if len(req.Skills) == 0 {
		return req, fmt.Errorf("skills is required")
	}
	if len(req.Skills) > maxSkillOrganizeSkills {
		return req, fmt.Errorf("skills must not exceed %d items", maxSkillOrganizeSkills)
	}

	seen := make(map[string]struct{}, len(req.Skills))
	normalized := make([]string, 0, len(req.Skills))
	for _, raw := range req.Skills {
		skillPath, err := normalizeSkillOrganizePath(raw)
		if err != nil {
			return req, err
		}
		if _, ok := seen[skillPath]; ok {
			return req, fmt.Errorf("duplicate skill path: %s", skillPath)
		}
		seen[skillPath] = struct{}{}
		normalized = append(normalized, skillPath)
	}
	req.Skills = normalized
	return req, nil
}

func normalizeSkillOrganizePath(raw string) (string, error) {
	value := strings.Trim(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", fmt.Errorf("skill path is required")
	}
	if strings.Contains(value, `\`) || strings.Contains(value, "//") {
		return "", fmt.Errorf("invalid skill path: %s", raw)
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("invalid skill path: %s", raw)
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) != 3 || parts[0] != skillOrganizeBaseDir || parts[1] == "" || parts[2] == "" {
		return "", fmt.Errorf("skill path must be skills/<category>/<skill_name>")
	}
	return cleaned, nil
}
