package readonlyorm

// LazyLLMDocParseStateRow is the parser's current task snapshot for one
// document in one knowledge base. Core reads this table; LazyLLM owns it.
type LazyLLMDocParseStateRow struct {
	DocID         string  `gorm:"column:doc_id;primaryKey"`
	KbID          string  `gorm:"column:kb_id;primaryKey"`
	Status        string  `gorm:"column:status"`
	CurrentTaskID string  `gorm:"column:current_task_id"`
	LastErrorCode *string `gorm:"column:last_error_code"`
	LastErrorMsg  *string `gorm:"column:last_error_msg"`
}

func (LazyLLMDocParseStateRow) TableName() string {
	return Table(LazyLLMSchema(), "lazyllm_doc_parse_state")
}
