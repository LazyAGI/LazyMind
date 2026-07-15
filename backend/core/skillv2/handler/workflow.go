package handler

import (
	"fmt"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/modelconfig"
	skillfs "lazymind/core/skillv2/fs"
	skillrevision "lazymind/core/skillv2/revision"
	skillservice "lazymind/core/skillv2/service"
)

type generateSkillRequest struct {
	UserInstruct string `json:"user_instruct"`
}

type generateSkillResponse struct {
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	DraftPath          string `json:"draft_path"`
	Outdated           bool   `json:"outdated"`
}

type draftPreviewResponse struct {
	SkillID            string `json:"skill_id"`
	ReviewResultID     string `json:"review_result_id"`
	ReviewStatus       string `json:"review_status"`
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	CurrentContent     string `json:"current_content"`
	DraftContent       string `json:"draft_content"`
	Diff               string `json:"diff"`
	Outdated           bool   `json:"outdated"`
}

type skillMDFrontmatter struct {
	Name        string `yaml:"name"`
	Category    string `yaml:"category"`
	Description string `yaml:"description"`
}

func Generate(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	var req generateSkillRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.UserInstruct = strings.TrimSpace(req.UserInstruct)
	if req.UserInstruct == "" {
		replyError(w, "user_instruct required", http.StatusBadRequest)
		return
	}

	status, err := newRevisionService(db).DraftStatus(r.Context(), skillrevision.DraftStatusRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	refType := "head"
	if status.HasUncommittedDraft {
		refType = "draft"
	}
	base, err := newSkillService(db).ReadFile(r.Context(), skillservice.FileRef{SkillID: skillID, RefType: refType, Path: "SKILL.md"})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	var row orm.SkillV2Skill
	if err := db.WithContext(r.Context()).Where("id = ?", skillID).Take(&row).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), db, userID)
	if err != nil {
		replyError(w, "load llm config failed", http.StatusInternalServerError)
		return
	}
	generated, err := algo.GenerateSkill(r.Context(), algo.SkillGenerateRequest{
		Content:      ensureSkillMDFrontmatter(base.Content, row),
		UserInstruct: req.UserInstruct,
		LLMConfig:    llmConfig,
	})
	if err != nil {
		replyError(w, "skill generate failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if strings.TrimSpace(generated) == "" {
		replyError(w, "skill generate returned empty content", http.StatusBadGateway)
		return
	}
	generated = ensureSkillMDFrontmatter(generated, row)
	if status.DraftVersion <= 0 {
		replyError(w, "skill draft not initialized", http.StatusInternalServerError)
		return
	}
	if _, err := newDraftFS(db).WriteText(r.Context(), skillfs.WriteTextRequest{
		SkillID:              skillID,
		Path:                 "SKILL.md",
		Content:              generated,
		ExpectedDraftVersion: status.DraftVersion,
		UserID:               userID,
	}); err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, generateSkillResponse{
		DraftStatus:        "pending_confirm",
		DraftSourceVersion: row.Version,
		DraftPath:          "SKILL.md",
		Outdated:           false,
	})
}

func DraftPreview(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	status, err := newRevisionService(db).DraftStatus(r.Context(), skillrevision.DraftStatusRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if !status.HasUncommittedDraft {
		replyError(w, "skill draft not found", http.StatusNotFound)
		return
	}
	current, err := newSkillService(db).ReadFile(r.Context(), skillservice.FileRef{SkillID: skillID, RefType: "head", Path: "SKILL.md"})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	draft, err := newSkillService(db).ReadFile(r.Context(), skillservice.FileRef{SkillID: skillID, RefType: "draft", Path: "SKILL.md"})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	diff, err := evolution.BuildContentDiff(current.Content, draft.Content)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	var row orm.SkillV2Skill
	if err := db.WithContext(r.Context()).Where("id = ?", skillID).Take(&row).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, draftPreviewResponse{
		SkillID:            skillID,
		ReviewStatus:       "pending_confirm",
		DraftStatus:        "pending_confirm",
		DraftSourceVersion: row.Version,
		CurrentContent:     current.Content,
		DraftContent:       draft.Content,
		Diff:               diff,
		Outdated:           false,
	})
}

func Confirm(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	status, err := newRevisionService(db).DraftStatus(r.Context(), skillrevision.DraftStatusRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if !status.HasUncommittedDraft {
		replyError(w, "skill draft not found", http.StatusNotFound)
		return
	}
	if _, err := newRevisionService(db).CommitDraft(r.Context(), skillrevision.CommitDraftRequest{
		SkillID:      skillID,
		UserID:       userID,
		DraftVersion: status.DraftVersion,
	}); err != nil {
		replyServiceError(w, err)
		return
	}
	detail, err := newSkillService(db).GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, skillDetailDTO(detail))
}

func Discard(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	if _, err := newSkillService(db).DiscardDraft(r.Context(), skillservice.DiscardDraftRequest{SkillID: skillID, UserID: userID}); err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"discarded": true})
}

func ensureSkillMDFrontmatter(content string, row orm.SkillV2Skill) string {
	meta, body, ok := parseSkillMDFrontmatter(content)
	if ok && strings.TrimSpace(meta.Name) != "" && strings.TrimSpace(meta.Category) != "" && strings.TrimSpace(meta.Description) != "" {
		return content
	}
	if !ok {
		body = strings.TrimSpace(content)
	}
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = firstNonEmpty(row.SkillName, row.ID)
	}
	if strings.TrimSpace(meta.Category) == "" {
		meta.Category = strings.TrimSpace(row.Category)
	}
	if strings.TrimSpace(meta.Description) == "" {
		meta.Description = firstNonEmpty(row.Description, row.SkillName, row.ID)
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("# %s", firstNonEmpty(meta.Name, row.SkillName, row.ID))
	}
	frontmatter, err := yaml.Marshal(meta)
	if err != nil {
		return content
	}
	return fmt.Sprintf("---\n%s---\n%s\n", string(frontmatter), strings.TrimSpace(body))
}

func parseSkillMDFrontmatter(content string) (skillMDFrontmatter, string, bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return skillMDFrontmatter{}, "", false
	}
	rest := strings.TrimPrefix(normalized, "---\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return skillMDFrontmatter{}, "", false
	}
	yamlPart := rest[:idx]
	body := strings.TrimPrefix(rest[idx+len("\n---"):], "\n")
	var meta skillMDFrontmatter
	if err := yaml.Unmarshal([]byte(yamlPart), &meta); err != nil {
		return skillMDFrontmatter{}, "", false
	}
	return meta, body, true
}
