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
	UserID         string         `json:"user_id"`
	Target         string         `json:"target"`
	SessionID      string         `json:"session_id"`
	History        any            `json:"history,omitempty"`
	CurrentContent string         `json:"current_content"`
	LLMConfig      map[string]any `json:"llm_config,omitempty"`
}

type MemoryReviewResponse struct {
	Code int              `json:"code"`
	Msg  string           `json:"msg"`
	Data MemoryReviewData `json:"data"`
}

type MemoryReviewData struct {
	UserID    string `json:"user_id"`
	Target    string `json:"target"`
	SessionID string `json:"session_id"`
	Submitted bool   `json:"submitted"`
	ResultID  string `json:"result_id,omitempty"`
}
