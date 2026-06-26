// Package orm text。
// text：text migrations/*.sql，Starttext migrate.RunUp() text。text Model text all_models.go text，text dbmigrate migrate text DDL。

package orm

import (
	"encoding/json"
	"time"
)

// ----- ACL text -----

// VisibilityModel text（text kb）text。
type VisibilityModel struct {
	ID         int64  `gorm:"primaryKey;autoIncrement"`
	ResourceID string `gorm:"column:resource_id;type:varchar(255);index"`
	Level      string `gorm:"column:level;type:varchar(32)"`
}

func (VisibilityModel) TableName() string { return "acl_visibility" }

// ACLModel ACL text。
type ACLModel struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	ResourceType string     `gorm:"column:resource_type;type:varchar(32);index:idx_acl_resource,priority:1"`
	ResourceID   string     `gorm:"column:resource_id;type:varchar(255);index:idx_acl_resource,priority:2"`
	GranteeType  string     `gorm:"column:grantee_type;type:varchar(32)"`
	TargetID     string     `gorm:"column:target_id;type:varchar(255)"`
	Permission   string     `gorm:"column:permission;type:varchar(32)"`
	CreatedBy    string     `gorm:"column:created_by;type:varchar(255)"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	ExpiresAt    *time.Time `gorm:"column:expires_at"`
}

func (ACLModel) TableName() string { return "acl_rows" }

// KBModel Knowledge basetext。
type KBModel struct {
	ID         string `gorm:"primaryKey;column:id;type:varchar(64)"`
	Name       string `gorm:"column:name;type:varchar(255)"`
	OwnerID    string `gorm:"column:owner_id;type:varchar(255)"`
	Visibility string `gorm:"column:visibility;type:varchar(32)"`
}

func (KBModel) TableName() string { return "acl_kbs" }

// ACLGroupModel User grouptext。
type ACLGroupModel struct {
	ID   string `gorm:"primaryKey;column:id;type:varchar(255)"`
	Name string `gorm:"column:name;type:varchar(255);not null;default:''"`
}

func (ACLGroupModel) TableName() string { return "acl_groups" }

// UserGroupModel Usertext。
type UserGroupModel struct {
	UserID  string `gorm:"primaryKey;column:user_id;type:varchar(255)"`
	GroupID string `gorm:"primaryKey;column:group_id;type:varchar(255)"`
}

func (UserGroupModel) TableName() string { return "acl_user_groups" }

// ----- Chat / Prompt text -----

type Prompt struct {
	ID      string `gorm:"column:id;type:varchar(64);primaryKey"`
	Name    string `gorm:"column:name;type:varchar(255);not null"`
	Content string `gorm:"column:content;type:text;not null"`

	BaseModel
}

func (Prompt) TableName() string { return "prompts" }

type DefaultPrompt struct {
	ID         int    `gorm:"column:id;primaryKey;autoIncrement"`
	PromptID   string `gorm:"column:prompt_id;type:varchar(64);not null"`
	PromptName string `gorm:"column:prompt_name;type:varchar(255);not null"`

	BaseModel
}

func (DefaultPrompt) TableName() string { return "default_prompts" }

type UserDisabledTool struct {
	ID             int64      `gorm:"column:id;primaryKey;autoIncrement"`
	ToolName       string     `gorm:"column:tool_name;type:varchar(255);not null;uniqueIndex:uk_user_disabled_tools_user_tool,priority:2"`
	CreateUserID   string     `gorm:"column:create_user_id;type:varchar(255);not null;uniqueIndex:uk_user_disabled_tools_user_tool,priority:1"`
	CreateUserName string     `gorm:"column:create_user_name;type:varchar(255);not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt      *time.Time `gorm:"column:deleted_at"`
}

func (UserDisabledTool) TableName() string { return "user_disabled_tools" }

type MultiAnswersSwitch struct {
	ID     int32 `gorm:"column:id;primaryKey;autoIncrement"`
	Status int32 `gorm:"column:status;not null;default:0"`

	BaseModel
}

func (MultiAnswersSwitch) TableName() string { return "multi_answers_switches" }

type Conversation struct {
	ID            string          `gorm:"column:id;type:varchar(36);primaryKey"`
	DisplayName   string          `gorm:"column:display_name;type:varchar(255)"`
	ChannelID     string          `gorm:"column:channel_id;type:varchar(36);not null;default:default"`
	SearchConfig  json.RawMessage `gorm:"column:search_config;type:json"`
	ApplicationID string          `gorm:"column:application_id;type:varchar(64);default:''"`
	Ext           json.RawMessage `gorm:"column:ext;type:json"`
	Model         string          `gorm:"column:model;type:varchar(64);default:''"`
	Models        json.RawMessage `gorm:"column:models;type:json"`
	ChatTimes     int32           `gorm:"column:chat_times;not null;default:0"`
	// Plugin/subagent mode overrides at conversation level (NULL falls back to user_chat_settings).
	EnablePlugin   *bool   `gorm:"column:enable_plugin"`
	PluginMode     *string `gorm:"column:plugin_mode;type:varchar(16)"`
	EnableSubagent *bool   `gorm:"column:enable_subagent"`
	// IsTaskConv marks conversations created by the scheduler or task center (not user-initiated).
	IsTaskConv bool `gorm:"column:is_task_conv;not null;default:false"`

	BaseModel
}

func (Conversation) TableName() string { return "conversations" }

type ChatHistory struct {
	ID              string          `gorm:"column:id;type:varchar(36);primaryKey"`
	Seq             int             `gorm:"column:seq;not null"`
	ConversationID  string          `gorm:"column:conversation_id;type:varchar(36);index;not null"`
	RawContent      string          `gorm:"column:raw_content;type:text"`
	RetrievalResult json.RawMessage `gorm:"column:retrieval_result;type:json"`
	Content         string          `gorm:"column:content;type:text"`
	Result          string          `gorm:"column:result;type:text"`
	FeedBack        int             `gorm:"column:feed_back;default:0"`
	Reason          string          `gorm:"column:reason;type:varchar(255)"`
	ExpectedAnswer  string          `gorm:"column:expected_answer;type:text"`
	Ext             json.RawMessage `gorm:"column:ext;type:json"`
	Version         string          `gorm:"column:version;type:varchar(128);default:2.3"`
	ToolCallTurns   int             `gorm:"column:tool_call_turns;not null;default:0;check:chk_chat_histories_tool_call_turns_non_negative,tool_call_turns >= 0"`

	TimeMixin
}

func (ChatHistory) TableName() string { return "chat_histories" }

type MultiAnswersChatHistory struct {
	ID              string          `gorm:"column:id;type:varchar(36);primaryKey"`
	Seq             int             `gorm:"column:seq;not null"`
	ConversationID  string          `gorm:"column:conversation_id;type:varchar(36);index;not null"`
	RawContent      string          `gorm:"column:raw_content;type:text"`
	RetrievalResult json.RawMessage `gorm:"column:retrieval_result;type:json"`
	Content         string          `gorm:"column:content;type:text"`
	Result          string          `gorm:"column:result;type:text"`
	ToolCallTurns   int             `gorm:"column:tool_call_turns;not null;default:0;check:chk_multi_answers_chat_histories_tool_call_turns_non_negative,tool_call_turns >= 0"`
	FeedBack        int             `gorm:"column:feed_back;default:0"`
	Reason          string          `gorm:"column:reason;type:varchar(255)"`
	Ext             json.RawMessage `gorm:"column:ext;type:json"`
	Endpoint        string          `gorm:"column:endpoint;type:varchar(512)"`

	TimeMixin
}

func (MultiAnswersChatHistory) TableName() string { return "multi_answers_chat_histories" }
