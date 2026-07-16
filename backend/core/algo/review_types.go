package algo

type SkillReviewRequest struct {
	RequestID    string         `json:"requestid"`
	UserID       string         `json:"user_id,omitempty"`
	StartTime    string         `json:"start_time"`
	EndTime      string         `json:"end_time"`
	MinUserTurns int            `json:"min_user_turns,omitempty"`
	MinToolTurns int            `json:"min_tool_turns,omitempty"`
	ModelConfigs map[string]any `json:"model_configs"`
}

type SkillReviewResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data SkillReviewData `json:"data"`
}

type SkillReviewData struct {
	Status    string `json:"status"`
	RequestID string `json:"requestid"`
	TaskID    string `json:"taskid,omitempty"`
}

type SkillOrganizeRequest struct {
	RequestID    string         `json:"requestid"`
	UserID       string         `json:"user_id"`
	Skills       []string       `json:"skills"`
	ArtifactDir  string         `json:"artifact_dir,omitempty"`
	ModelConfigs map[string]any `json:"model_configs,omitempty"`
}

type SkillOrganizeResponse struct {
	Code int               `json:"code"`
	Msg  string            `json:"msg"`
	Data SkillOrganizeData `json:"data"`
}

type SkillOrganizeData struct {
	Status    string `json:"status"`
	RequestID string `json:"requestid"`
	TaskID    string `json:"taskid"`
}

type MemoryReviewRequest struct {
	UserID    string         `json:"user_id"`
	History   any            `json:"history"`
	Memory    string         `json:"memory"`
	User      string         `json:"user"`
	LLMConfig map[string]any `json:"llm_config"`
}

type MemoryReviewResponse struct {
	Status string `json:"status"`
}
