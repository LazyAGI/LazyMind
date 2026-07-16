package orm

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

const (
	SkillReviewStatsStatusPending        = "pending"
	SkillReviewStatsStatusReviewDraft    = "review_draft"
	SkillReviewStatsStatusReviewCluster  = "review_cluster"
	SkillReviewStatsStatusReviewMiner    = "review_miner"
	SkillReviewStatsStatusReviewSolution = "review_solution"
	SkillReviewStatsStatusReviewApply    = "review_apply"
	SkillReviewStatsStatusOrganizePlan   = "organize_plan"
	SkillReviewStatsStatusOrganizeDraft  = "organize_draft"
	SkillReviewStatsStatusOrganizeApply  = "organize_apply"
	SkillReviewStatsStatusCompleted      = "completed"
	SkillReviewStatsStatusSkipped        = "skipped"
	SkillReviewStatsStatusFailed         = "failed"
)

var skillReviewStatsActiveStatuses = []string{
	SkillReviewStatsStatusPending,
	SkillReviewStatsStatusReviewDraft,
	SkillReviewStatsStatusReviewCluster,
	SkillReviewStatsStatusReviewMiner,
	SkillReviewStatsStatusReviewSolution,
	SkillReviewStatsStatusReviewApply,
	SkillReviewStatsStatusOrganizePlan,
	SkillReviewStatsStatusOrganizeDraft,
	SkillReviewStatsStatusOrganizeApply,
}

// SkillReviewStatsActiveScope selects known algorithm execution stages. A
// successful terminal row closes the logical request even if an older Core
// version retried the same requestid and left a later stage row behind.
func SkillReviewStatsActiveScope(db *gorm.DB) *gorm.DB {
	return db.
		Where("skill_review_stats.status IN ?", skillReviewStatsActiveStatuses).
		Where(`NOT EXISTS (
			SELECT 1
			FROM skill_review_stats AS terminal_stats
			WHERE terminal_stats.userid = skill_review_stats.userid
			  AND terminal_stats.requestid = skill_review_stats.requestid
			  AND terminal_stats.status IN ?
		)`, []string{SkillReviewStatsStatusCompleted, SkillReviewStatsStatusSkipped})
}

type SkillReviewResult struct {
	ID           string    `gorm:"column:id;type:text;primaryKey"`
	SkillName    string    `gorm:"column:skill_name;type:text;not null"`
	Type         string    `gorm:"column:type;type:text;not null"`
	ReviewStatus string    `gorm:"column:review_status;type:text;not null;default:'pending'"`
	UserID       string    `gorm:"column:userid;type:text;not null"`
	RequestID    string    `gorm:"column:requestid;type:text;not null"`
	SkillContent string    `gorm:"column:skill_content;type:text;not null"`
	Summary      string    `gorm:"column:summary;type:text"`
	Time         time.Time `gorm:"column:time;not null"`
}

func (SkillReviewResult) TableName() string { return "skill_review_results" }

type MemoryReviewResult struct {
	ID            string          `gorm:"column:id;type:text;primaryKey"`
	UserID        string          `gorm:"column:user_id;type:text;not null;default:''"`
	Target        string          `gorm:"column:target;type:text;not null"`
	SessionID     string          `gorm:"column:session_id;type:text;not null"`
	SourceContent string          `gorm:"column:source_content;type:text;not null;default:''"`
	Content       string          `gorm:"column:content;type:text;not null;default:''"`
	Operations    json.RawMessage `gorm:"column:operations;type:jsonb;not null;default:'[]'"`
	State         string          `gorm:"column:state;type:text;not null;default:'success'"`
	ReviewStatus  string          `gorm:"column:review_status;type:text;not null;default:'pending'"`
	Time          time.Time       `gorm:"column:time;not null"`
}

func (MemoryReviewResult) TableName() string { return "memory_review" }
