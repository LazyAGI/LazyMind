package orm

import "time"

// UserGitHubStarPrompt stores invitation history, not proof of GitHub writes.
type UserGitHubStarPrompt struct {
	UserID          string     `gorm:"column:user_id;type:varchar(255);primaryKey"`
	PromptedTaskIDs string     `gorm:"column:prompted_task_ids;type:text;not null;default:'[]'"`
	LastPromptAt    *time.Time `gorm:"column:last_prompt_at"`
	Supported       bool       `gorm:"column:supported;not null;default:false"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null"`
}

func (UserGitHubStarPrompt) TableName() string { return "user_github_star_prompts" }
