package skill

type childSkillInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Content     string   `json:"content"`
	FileExt     string   `json:"file_ext"`
	AutoEvo     bool     `json:"auto_evo"`
}

type createSkillRequest struct {
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Category        string            `json:"category"`
	ParentSkillID   string            `json:"parent_skill_id"`
	ParentSkillName string            `json:"parent_skill_name"`
	Tags            []string          `json:"tags"`
	Content         string            `json:"content"`
	FileExt         string            `json:"file_ext"`
	AutoEvo         bool              `json:"auto_evo"`
	IsEnabled       *bool             `json:"is_enabled"`
	Children        []childSkillInput `json:"children"`
}

type updateSkillRequest struct {
	Name            *string   `json:"name"`
	Description     *string   `json:"description"`
	Category        *string   `json:"category"`
	ParentSkillID   *string   `json:"parent_skill_id"`
	ParentSkillName *string   `json:"parent_skill_name"`
	Tags            *[]string `json:"tags"`
	Content         *string   `json:"content"`
	FileExt         *string   `json:"file_ext"`
	AutoEvo         *bool     `json:"auto_evo"`
	IsEnabled       *bool     `json:"is_enabled"`
}

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

type shareSkillRequest struct {
	TargetUserIDs  []string `json:"target_user_ids"`
	TargetGroupIDs []string `json:"target_group_ids"`
	Message        string   `json:"message"`
}
