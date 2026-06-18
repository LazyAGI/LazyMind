package algo

type SkillReviewRequest struct {
	RequestID       string         `json:"requestid"`
	UserID          string         `json:"user_id"`
	StartTime       string         `json:"start_time"`
	EndTime         string         `json:"end_time"`
	PendingSkillIDs []string       `json:"pending_skill_ids,omitempty"`
	ModelConfigs    map[string]any `json:"model_configs,omitempty"`
}

type SkillReviewResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data SkillReviewData `json:"data"`
}

type SkillReviewData struct {
	Status    string `json:"status"`
	RequestID string `json:"requestid"`
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
