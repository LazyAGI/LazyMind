package orm

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// InvocationResultJSON preserves JSON output while accepting the text values
// returned by SQLite and byte slices returned by PostgreSQL.
type InvocationResultJSON json.RawMessage

func (value *InvocationResultJSON) Scan(src any) error {
	switch src := src.(type) {
	case nil:
		*value = InvocationResultJSON(`{}`)
	case string:
		*value = append((*value)[:0], src...)
	case []byte:
		*value = append((*value)[:0], src...)
	default:
		return fmt.Errorf("scan JSON from unsupported type %T", src)
	}
	return nil
}

func (value InvocationResultJSON) Value() (driver.Value, error) {
	if len(value) == 0 {
		return "{}", nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("invalid JSON value")
	}
	return string(value), nil
}

func (value InvocationResultJSON) MarshalJSON() ([]byte, error) {
	if len(value) == 0 {
		return []byte(`{}`), nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("invalid JSON value")
	}
	return value, nil
}

// ExternalCapabilityGrant is an explicit, user-owned authorization for one
// external Agent to use one LazyMind model or MCP tool. Absence of a row is a
// denial; capabilities are never shared implicitly.
type ExternalCapabilityGrant struct {
	ID             string    `gorm:"column:id;type:varchar(64);primaryKey" json:"id"`
	OwnerUserID    string    `gorm:"column:owner_user_id;type:varchar(255);not null;uniqueIndex:uk_external_capability_grant,priority:1;index" json:"-"`
	Agent          string    `gorm:"column:agent;type:varchar(64);not null;uniqueIndex:uk_external_capability_grant,priority:2;index" json:"agent"`
	CapabilityType string    `gorm:"column:capability_type;type:varchar(16);not null;uniqueIndex:uk_external_capability_grant,priority:3" json:"capability_type"`
	CapabilityID   string    `gorm:"column:capability_id;type:varchar(128);not null;uniqueIndex:uk_external_capability_grant,priority:4;index" json:"capability_id"`
	Enabled        bool      `gorm:"column:enabled;not null;default:false" json:"enabled"`
	CreatedAt      time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ExternalCapabilityGrant) TableName() string { return "external_capability_grants" }

// ExternalCapabilityInvocation records model/tool usage without storing
// provider credentials or request bodies. ResultJSON contains only the
// bounded, recursively redacted result preview produced by the gateway.
type ExternalCapabilityInvocation struct {
	ID             string               `gorm:"column:id;type:varchar(80);primaryKey" json:"id"`
	OwnerUserID    string               `gorm:"column:owner_user_id;type:varchar(255);not null;index:idx_external_capability_invocations_owner_started,priority:1" json:"-"`
	Agent          string               `gorm:"column:agent;type:varchar(64);not null;index" json:"agent"`
	InvocationID   string               `gorm:"column:invocation_id;type:varchar(80);not null;default:'';index" json:"invocation_id,omitempty"`
	CapabilityType string               `gorm:"column:capability_type;type:varchar(16);not null;index" json:"capability_type"`
	CapabilityID   string               `gorm:"column:capability_id;type:varchar(128);not null;index" json:"capability_id"`
	CapabilityName string               `gorm:"column:capability_name;type:varchar(512);not null" json:"capability_name"`
	Status         string               `gorm:"column:status;type:varchar(32);not null;index" json:"status"`
	UsageJSON      json.RawMessage      `gorm:"column:usage_json;type:json;not null" json:"usage"`
	ResultJSON     InvocationResultJSON `gorm:"column:result_json;type:json;not null" json:"result"`
	ErrorCode      string               `gorm:"column:error_code;type:varchar(64);not null;default:''" json:"error_code,omitempty"`
	ErrorMessage   string               `gorm:"column:error_message;type:text;not null;default:''" json:"error_message,omitempty"`
	StartedAt      time.Time            `gorm:"column:started_at;not null;index:idx_external_capability_invocations_owner_started,priority:2,sort:desc" json:"started_at"`
	FinishedAt     *time.Time           `gorm:"column:finished_at" json:"finished_at,omitempty"`
	CreatedAt      time.Time            `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt      time.Time            `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ExternalCapabilityInvocation) TableName() string {
	return "external_capability_invocations"
}
