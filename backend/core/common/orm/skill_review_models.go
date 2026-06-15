package orm

import (
	"encoding/json"
	"time"
)

type SkillReviewResult struct {
	ID           string    `gorm:"column:id;type:text;primaryKey"`
	SkillName    string    `gorm:"column:skill_name;type:text;not null"`
	Type         string    `gorm:"column:type;type:text;not null"`
	ReviewStatus string    `gorm:"column:review_status;type:text;not null;default:'pending'"`
	UserID       string    `gorm:"column:userid;type:text;not null;default:''"`
	RequestID    string    `gorm:"column:requestid;type:text;not null;default:''"`
	SkillContent string    `gorm:"column:skill_content;type:text;not null"`
	Summary      string    `gorm:"column:summary;type:text;not null;default:''"`
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
