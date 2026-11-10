package orm

import "time"

// EmbeddingIndexState records the model identity of a shared text-vector space.
// Scope is currently "global"; keeping it explicit makes the boundary visible
// to every writer and allows a future storage layout to isolate spaces.
type EmbeddingIndexState struct {
	Scope              string    `gorm:"column:scope;type:varchar(255);primaryKey"`
	ActiveModelID      string    `gorm:"column:active_model_id;type:varchar(255);not null;default:''"`
	ActiveSource       string    `gorm:"column:active_source;type:varchar(16);not null;default:''"`
	ActiveModelName    string    `gorm:"column:active_model_name;type:varchar(255);not null;default:''"`
	ActiveProviderName string    `gorm:"column:active_provider_name;type:varchar(255);not null;default:''"`
	Signature          string    `gorm:"column:signature;type:varchar(255);not null;default:''"`
	Phase              string    `gorm:"column:phase;type:varchar(32);not null;default:'ready'"`
	Generation         int64     `gorm:"column:generation;not null;default:1"`
	TargetModelID      string    `gorm:"column:target_model_id;type:varchar(255);not null;default:''"`
	TargetSource       string    `gorm:"column:target_source;type:varchar(16);not null;default:''"`
	TargetSignature    string    `gorm:"column:target_signature;type:varchar(255);not null;default:''"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null"`
}

func (EmbeddingIndexState) TableName() string { return "embedding_index_states" }
