package orm

import (
	"encoding/json"
	"time"
)

type ExternalDatabaseConnection struct {
	ID             string          `gorm:"column:id;type:varchar(64);primaryKey"`
	DisplayName    string          `gorm:"column:display_name;type:varchar(255);not null"`
	Description    string          `gorm:"column:description;type:text;not null;default:''"`
	DBType         string          `gorm:"column:db_type;type:varchar(32);not null"`
	Host           string          `gorm:"column:host;type:varchar(255);not null"`
	Port           int             `gorm:"column:port;not null"`
	DatabaseName   string          `gorm:"column:database_name;type:varchar(255);not null"`
	Username       string          `gorm:"column:username;type:varchar(255);not null"`
	PasswordJSON   json.RawMessage `gorm:"column:password_json;type:json;not null"`
	OptionsJSON    json.RawMessage `gorm:"column:options_json;type:json;not null"`
	IsVerified     bool            `gorm:"column:is_verified;not null;default:false"`
	LastCheckedAt  *time.Time      `gorm:"column:last_checked_at"`
	LastCheckError string          `gorm:"column:last_check_error;type:text;not null;default:''"`
	BaseModel
}

func (ExternalDatabaseConnection) TableName() string { return "external_database_connections" }
