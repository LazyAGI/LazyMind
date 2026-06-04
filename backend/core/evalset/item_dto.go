package evalset

import "time"

type EvalSetItemResponse struct {
	ID                string    `json:"id"`
	EvalSetID         string    `json:"eval_set_id"`
	ShardID           string    `json:"shard_id"`
	CaseID            string    `json:"case_id"`
	Question          string    `json:"question"`
	GroundTruth       string    `json:"ground_truth"`
	QuestionType      string    `json:"question_type"`
	GenerateReason    string    `json:"generate_reason"`
	KeyPoints         string    `json:"key_points"`
	ReferenceChunkIDs string    `json:"reference_chunk_ids"`
	ReferenceContext  string    `json:"reference_context"`
	ReferenceDoc      string    `json:"reference_doc"`
	ReferenceDocIDs   string    `json:"reference_doc_ids"`
	IsDeleted         bool      `json:"is_deleted"`
	Source            string    `json:"source"`
	SourceSessionID   string    `json:"source_session_id"`
	SourceHistoryID   string    `json:"source_history_id"`
	CreatedBy         string    `json:"created_by"`
	CreatedByName     string    `json:"created_by_name"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ListEvalSetItemsResponse struct {
	Items    []EvalSetItemResponse `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

type CreateEvalSetItemRequest struct {
	CaseID            string `json:"case_id"`
	Question          string `json:"question"`
	GroundTruth       string `json:"ground_truth"`
	QuestionType      string `json:"question_type"`
	GenerateReason    string `json:"generate_reason"`
	KeyPoints         string `json:"key_points"`
	ReferenceChunkIDs string `json:"reference_chunk_ids"`
	ReferenceContext  string `json:"reference_context"`
	ReferenceDoc      string `json:"reference_doc"`
	ReferenceDocIDs   string `json:"reference_doc_ids"`
	IsDeleted         *bool  `json:"is_deleted"`
}

type UpdateEvalSetItemRequest struct {
	CaseID            *string `json:"case_id"`
	Question          *string `json:"question"`
	GroundTruth       *string `json:"ground_truth"`
	QuestionType      *string `json:"question_type"`
	GenerateReason    *string `json:"generate_reason"`
	KeyPoints         *string `json:"key_points"`
	ReferenceChunkIDs *string `json:"reference_chunk_ids"`
	ReferenceContext  *string `json:"reference_context"`
	ReferenceDoc      *string `json:"reference_doc"`
	ReferenceDocIDs   *string `json:"reference_doc_ids"`
	IsDeleted         *bool   `json:"is_deleted"`
}

type BatchDeleteEvalSetItemsRequest struct {
	ItemIDs []string `json:"item_ids"`
}

type DeleteEvalSetItemResponse struct {
	Deleted bool `json:"deleted"`
}

type BatchDeleteEvalSetItemsResponse struct {
	DeletedCount int64 `json:"deleted_count"`
}

type ListEvalSetItemsQuery struct {
	Keyword      string `query:"keyword"`
	QuestionType string `query:"question_type"`
	Source       string `query:"source"`
	Page         int    `query:"page"`
	PageSize     int    `query:"page_size"`
	OrderBy      string `query:"order_by"`
}

type EvalSetItemPathParams struct {
	EvalSetID string `path:"eval_set_id"`
	ItemID    string `path:"item_id"`
}
