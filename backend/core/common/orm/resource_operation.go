package orm

import (
	"encoding/json"
	"time"
)

// ResourceOperationReceipt is committed in the same transaction as resource changes.
// The key is scoped to tenant and user; a key reused with different input conflicts.
type ResourceOperationReceipt struct {
	TenantID      string          `gorm:"column:tenant_id;type:varchar(36);primaryKey"`
	UserID        string          `gorm:"column:user_id;type:varchar(255);primaryKey"`
	OperationKey  string          `gorm:"column:operation_key;type:varchar(255);primaryKey"`
	Action        string          `gorm:"column:action;type:varchar(64);not null"`
	ParameterHash string          `gorm:"column:parameter_hash;type:varchar(64);not null"`
	Result        json.RawMessage `gorm:"column:result;type:json;not null"`
	CreatedAt     time.Time       `gorm:"column:created_at;not null"`
}

func (ResourceOperationReceipt) TableName() string { return "resource_operation_receipts" }
