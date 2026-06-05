package evalset

import "time"

type CreateEvalSetRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	DatasetIDs  []string `json:"dataset_ids"`
	GroupID     string   `json:"group_id"`
}

type UpdateEvalSetRequest struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	DatasetIDs  *[]string `json:"dataset_ids"`
	GroupID     *string   `json:"group_id"`
}

type EvalSetResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	DatasetIDs    []string  `json:"dataset_ids"`
	DatasetNames  []string  `json:"dataset_names"`
	GroupID       string    `json:"group_id"`
	ShardID       string    `json:"shard_id"`
	ItemCount     int64     `json:"item_count"`
	CreatedBy     string    `json:"created_by"`
	CreatedByName string    `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Permissions   []string  `json:"permissions"`
}

type ListEvalSetsResponse struct {
	Items    []EvalSetResponse `json:"items"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

type ListEvalSetsQuery struct {
	Keyword    string   `query:"keyword"`
	DatasetIDs []string `query:"dataset_ids"`
	Page       int      `query:"page"`
	PageSize   int      `query:"page_size"`
}

type EvalSetPathParams struct {
	EvalSetID string `path:"eval_set_id"`
}

type DeleteEvalSetResponse struct {
	Deleted bool `json:"deleted"`
}

type DatasetOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type DatasetOptionsResponse struct {
	Items []DatasetOption `json:"items"`
}

type QuestionTypeOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type QuestionTypeOptionsResponse struct {
	Items []QuestionTypeOption `json:"items"`
}
