package algo

type SkillReviewRequest struct {
	RequestID    string         `json:"requestid"`
	UserID       string         `json:"user_id,omitempty"`
	SessionIDs   []string       `json:"session_ids"`
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
	TaskID                     string         `json:"task_id"`
	UserID                     string         `json:"user_id"`
	ConversationID             string         `json:"conversation_id"`
	ConversationLastActiveAtMS int64          `json:"conversation_last_active_at_ms"`
	History                    any            `json:"history"`
	LLMConfig                  map[string]any `json:"llm_config"`
}

type MemoryReviewResponse struct {
	Status    string             `json:"status"`
	TaskID    string             `json:"task_id"`
	Outcome   string             `json:"outcome"`
	Retryable bool               `json:"retryable"`
	Error     *MemoryReviewError `json:"error,omitempty"`
}

type MemoryReviewError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
