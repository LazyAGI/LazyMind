package orm

import "time"

// LocalWorkspace stores authorization metadata for a user-selected local
// directory. It never stores file contents or directory listings.
type LocalWorkspace struct {
	ID                string     `gorm:"column:id;type:varchar(64);primaryKey"`
	CreateUserID      string     `gorm:"column:create_user_id;type:varchar(255);not null;index:idx_local_workspaces_user_recent,priority:1"`
	DisplayName       string     `gorm:"column:display_name;type:varchar(255);not null"`
	CanonicalPath     string     `gorm:"column:canonical_path;type:text;not null"`
	DirectoryIdentity string     `gorm:"column:directory_identity;type:varchar(512);not null"`
	Status            string     `gorm:"column:status;type:varchar(32);not null;index:idx_local_workspaces_user_recent,priority:2"`
	Version           int64      `gorm:"column:version;not null;default:1"`
	Source            string     `gorm:"column:source;type:varchar(32);not null"`
	ReadPolicy        string     `gorm:"column:read_policy;type:varchar(32);not null;default:allow"`
	WritePolicy       string     `gorm:"column:write_policy;type:varchar(32);not null;default:allow"`
	AuthorizedAt      time.Time  `gorm:"column:authorized_at;not null"`
	LastUsedAt        time.Time  `gorm:"column:last_used_at;not null;index:idx_local_workspaces_user_recent,priority:3,sort:desc"`
	RevokedAt         *time.Time `gorm:"column:revoked_at"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null"`
}

func (LocalWorkspace) TableName() string { return "local_workspaces" }

// ConversationWorkspaceBinding is immutable for the lifetime of a
// conversation. Revocation is derived from LocalWorkspace.Status so it affects
// every bound task immediately without rewriting historical bindings.
type ConversationWorkspaceBinding struct {
	ConversationID    string         `gorm:"column:conversation_id;type:varchar(36);primaryKey"`
	WorkspaceID       string         `gorm:"column:workspace_id;type:varchar(64);not null;index"`
	PermissionMode    string         `gorm:"column:permission_mode;type:varchar(32);not null;default:ask_as_needed"`
	PermissionVersion int64          `gorm:"column:permission_version;not null;default:1"`
	CreatedAt         time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time      `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP"`
	Conversation      Conversation   `gorm:"foreignKey:ConversationID;references:ID;constraint:OnDelete:CASCADE"`
	Workspace         LocalWorkspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:RESTRICT"`
}

func (ConversationWorkspaceBinding) TableName() string {
	return "conversation_workspace_bindings"
}
