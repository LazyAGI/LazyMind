package resourcechange

import "time"

const MaxVersionsPerResource = 30

const (
	ChangeSourceDirectSave     = "direct_save"
	ChangeSourceDraftConfirm   = "draft_confirm"
	ChangeSourceReviewAccept   = "review_accept"
	ChangeSourceAutoApply      = "auto_apply"
	ChangeSourceInternalDirect = "internal_direct"
)

const (
	SourceRefTypeSkillReviewResult = "skill_review_results"
	SourceRefTypeMemoryReview      = "memory_review"
)

type Source struct {
	ChangeSource  string
	SourceRefType string
	SourceRefID   string
	ChangedAt     time.Time
}

type ContentChange struct {
	ResourceType  string
	ResourceID    string
	UserID        string
	FromVersion   int64
	ToVersion     int64
	BeforeContent string
	AfterContent  string
	Source        Source
}

type VersionChangeSummary struct {
	ChangeSource  string    `json:"change_source"`
	SourceRefType string    `json:"source_ref_type"`
	SourceRefID   string    `json:"source_ref_id"`
	ChangedAt     time.Time `json:"changed_at"`
}
