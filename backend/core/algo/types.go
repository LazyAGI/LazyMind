package algo

type RewriteRequest struct {
	TaskType        string                  `json:"task_type"`
	Content         string                  `json:"content"`
	UserInstruct    string                  `json:"user_instruct"`
	LLMConfig       map[string]any          `json:"llm_config"`
	FullContent     string                  `json:"full_content,omitempty"`
	SelectionRanges []RewriteSelectionRange `json:"selection_ranges,omitempty"`
}

type RewriteSelectionRange struct {
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Content string `json:"content"`
}

type SkillGenerateRequest struct {
	Content      string
	UserInstruct string
	LLMConfig    map[string]any
}

type PolishGenerateRequest struct {
	Content      string
	UserInstruct string
	LLMConfig    map[string]any
}

type LearningGenerateRequest struct {
	Content      string
	UserInstruct string
	LLMConfig    map[string]any
}
