package orm

import (
	"strings"
	"time"
)

// DefaultModelProvider is the built-in catalog of AI model providers (name, description, default base URL).
type DefaultModelProvider struct {
	ID           string     `gorm:"column:id;type:varchar(64);primaryKey"`
	Name         string     `gorm:"column:name;type:varchar(255);not null;uniqueIndex:uk_default_model_providers_name"`
	Description  string     `gorm:"column:description;type:text;not null"`
	BaseURL      string     `gorm:"column:base_url;type:varchar(1024);not null;default:''"`
	Category     string     `gorm:"column:category;type:varchar(64);not null;default:'model'"`
	Capabilities string     `gorm:"column:capabilities;type:varchar(512);not null;default:'multi_group,custom_base_url,has_models'"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (DefaultModelProvider) TableName() string { return "default_model_providers" }

// DefaultModel is a built-in model row (model name, type) under a DefaultModelProvider.
// ProviderName redundantly stores the provider display name (matches default_model_providers.name) for list UIs without joining.
// ModelType stores runtime_models.yaml role keys such as llm, embed_main, vlm (column model_type in DB; SQL keyword "type" avoided).
type DefaultModel struct {
	ID                     string     `gorm:"column:id;type:varchar(64);primaryKey"`
	DefaultModelProviderID string     `gorm:"column:default_model_provider_id;type:varchar(64);not null;uniqueIndex:uk_default_models_provider_name,priority:1"`
	ProviderName           string     `gorm:"column:provider_name;type:varchar(255);not null;default:''"`
	Name                   string     `gorm:"column:name;type:varchar(512);not null;uniqueIndex:uk_default_models_provider_name,priority:2"`
	ModelType              string     `gorm:"column:model_type;type:varchar(64);not null"`
	CreatedAt              time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt              time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt              *time.Time `gorm:"column:deleted_at"`
}

func (DefaultModel) TableName() string { return "default_models" }

// UserModelProvider is a per-user copy of catalog providers (seeded from DefaultModelProvider).
// DefaultModelProviderID is the DefaultModelProvider.ID the row was copied from.
type UserModelProvider struct {
	ID                     string `gorm:"column:id;type:varchar(64);primaryKey"`
	DefaultModelProviderID string `gorm:"column:default_model_provider_id;type:varchar(64);not null"`
	Name                   string `gorm:"column:name;type:varchar(255);not null"`
	Description            string `gorm:"column:description;type:text;not null"`
	BaseURL                string `gorm:"column:base_url;type:varchar(1024);not null;default:''"`
	Category               string `gorm:"column:category;type:varchar(64);not null;default:'model'"`
	Capabilities           string `gorm:"column:capabilities;type:varchar(512);not null;default:'multi_group,custom_base_url,has_models'"`
	BaseModel
}

// HasCapability reports whether the provider has the given capability flag.
func (p *UserModelProvider) HasCapability(cap string) bool {
	for _, c := range strings.Split(p.Capabilities, ",") {
		if strings.TrimSpace(c) == cap {
			return true
		}
	}
	return false
}

func (UserModelProvider) TableName() string { return "user_model_providers" }

// UserModelProviderGroup is a connection group under a user-scoped model provider (name, base URL, API key).
type UserModelProviderGroup struct {
	ID                  string `gorm:"column:id;type:varchar(64);primaryKey"`
	UserModelProviderID string `gorm:"column:user_model_provider_id;type:varchar(64);not null;index:idx_user_model_provider_groups_parent"`
	Name                string `gorm:"column:name;type:varchar(255);not null"`
	BaseURL             string `gorm:"column:base_url;type:varchar(1024);not null"`
	APIKey              string `gorm:"column:api_key;type:text;not null"`
	IsVerified          bool   `gorm:"column:is_verified;type:boolean;not null;default:false"`
	BaseModel
}

func (UserModelProviderGroup) TableName() string { return "user_model_provider_groups" }

// UserModelProviderGroupModel is a user-scoped model row under a connection group (often seeded from DefaultModel).
// ProviderName denormalizes user_model_providers.name; connection group display name comes from user_model_provider_groups.
type UserModelProviderGroupModel struct {
	ID                       string `gorm:"column:id;type:varchar(64);primaryKey"`
	UserModelProviderID      string `gorm:"column:user_model_provider_id;type:varchar(64);not null;index:idx_user_model_provider_group_models_provider"`
	UserModelProviderGroupID string `gorm:"column:user_model_provider_group_id;type:varchar(64);not null;uniqueIndex:uk_user_model_provider_group_models_group_name,priority:1"`
	ProviderName             string `gorm:"column:provider_name;type:varchar(255);not null;default:''"`
	Name                     string `gorm:"column:name;type:varchar(512);not null;uniqueIndex:uk_user_model_provider_group_models_group_name,priority:2"`
	ModelType                string `gorm:"column:model_type;type:varchar(64);not null"`
	IsDefault                bool   `gorm:"column:is_default;type:boolean;not null;default:false"`
	BaseModel
}

func (UserModelProviderGroupModel) TableName() string { return "user_model_provider_group_models" }

// UserSelectedProvider records which provider group a user has selected for a given category (ocr, search, etc.).
// Symmetric to UserSelectedModel but at the group level (no model list involved).
type UserSelectedProvider struct {
	ID                       int64     `gorm:"column:id;primaryKey;autoIncrement"`
	UserID                   string    `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:uk_user_selected_providers_user_category,priority:1"`
	UserName                 string    `gorm:"column:user_name;type:varchar(255);not null;default:''"`
	Category                 string    `gorm:"column:category;type:varchar(64);not null;uniqueIndex:uk_user_selected_providers_user_category,priority:2"`
	UserModelProviderGroupID string    `gorm:"column:user_model_provider_group_id;type:varchar(64);not null"`
	Share                    bool      `gorm:"column:share;type:boolean;not null;default:false"`
	CreatedAt                time.Time `gorm:"column:created_at;not null"`
	UpdatedAt                time.Time `gorm:"column:updated_at;not null"`
}

func (UserSelectedProvider) TableName() string { return "user_selected_providers" }
