package algo

type RewriteRequest struct {
	TaskType     string         `json:"task_type"`
	Content      string         `json:"content"`
	UserInstruct string         `json:"user_instruct"`
	LLMConfig    map[string]any `json:"llm_config"`
}

type SkillGenerateRequest struct {
	Content      string
	UserInstruct string
	LLMConfig    map[string]any
}

type ManagedGenerateRequest struct {
	Content      string
	UserInstruct string
	LLMConfig    map[string]any
}

type PolishGenerateRequest struct {
	Content      string
	UserInstruct string
	LLMConfig    map[string]any
}
