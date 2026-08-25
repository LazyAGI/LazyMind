package orm

import "time"

type UserUIPreferences struct {
	UserID                        string    `gorm:"column:user_id;type:varchar(255);primaryKey"`
	ChatPreferenceNoticeDismissed bool      `gorm:"column:chat_preference_notice_dismissed;not null;default:false"`
	DeveloperModeActive           bool      `gorm:"column:developer_mode_active;not null;default:false"`
	AcceptedUserAgreementVersion  string    `gorm:"column:accepted_user_agreement_version;type:varchar(64);not null;default:''"`
	TaskCenterEnabled             bool      `gorm:"column:task_center_enabled;not null;default:true"`
	SkillsEnabled                 bool      `gorm:"column:skills_enabled;not null;default:true"`
	WorkflowsEnabled              bool      `gorm:"column:workflows_enabled;not null;default:true"`
	MCPEnabled                    bool      `gorm:"column:mcp_enabled;not null;default:true"`
	DocumentParsingEnabled        bool      `gorm:"column:document_parsing_enabled;not null;default:true"`
	CreatedAt                     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt                     time.Time `gorm:"column:updated_at;not null"`
}

func (UserUIPreferences) TableName() string { return "user_ui_preferences" }
