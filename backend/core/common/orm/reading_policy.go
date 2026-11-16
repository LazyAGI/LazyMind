package orm

import "time"

type UserReadingPolicy struct {
	UserID     string    `gorm:"primaryKey;column:user_id;type:varchar(255)"`
	PolicyJSON string    `gorm:"column:policy_json;type:text;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null"`
}

func (UserReadingPolicy) TableName() string { return "user_reading_policies" }

// ReadingUsage records the first automatic work on a paper, independently for
// parsing and study. A retry never changes ChargedAt or consumes another day.
type ReadingUsage struct {
	UserID       string    `gorm:"primaryKey;column:user_id;type:varchar(255)"`
	Category     string    `gorm:"primaryKey;column:category;type:varchar(32)"`
	SourceSHA256 string    `gorm:"primaryKey;column:source_sha256;type:varchar(64)"`
	ChargedAt    time.Time `gorm:"column:charged_at;not null;index:idx_automatic_reading_usage_day"`
}

func (ReadingUsage) TableName() string { return "reading_usage" }
