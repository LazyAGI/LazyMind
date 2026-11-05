package algo

// Wire contract shared by workflow and registered structured tasks.
const llmTaskRunPath = "/api/chat/llm-task:run"

type llmTaskFile struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
}

type llmTaskInput struct {
	Text  string         `json:"text,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
	Files []llmTaskFile  `json:"files,omitempty"`
}

type llmTaskRequest struct {
	Mode           string           `json:"mode"`
	TaskType       string           `json:"task_type"`
	Instruction    string           `json:"instruction,omitempty"`
	Input          llmTaskInput     `json:"input"`
	Skills         []map[string]any `json:"skills,omitempty"`
	Tools          []map[string]any `json:"tools,omitempty"`
	ResponseFormat map[string]any   `json:"response_format,omitempty"`
	LLMConfig      map[string]any   `json:"llm_config,omitempty"`
	Options        map[string]any   `json:"options,omitempty"`
}
