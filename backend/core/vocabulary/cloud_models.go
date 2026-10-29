package vocabulary

import "time"

const CloudSyncJobType = "vocabulary_cloud_sync"

type CloudConnection struct {
	AccountKey  string    `json:"-"`
	ID          string    `json:"-" gorm:"primaryKey"`
	OwnerID     string    `json:"-" gorm:"uniqueIndex:idx_vocabulary_cloud_owner_provider"`
	Provider    string    `json:"provider" gorm:"uniqueIndex:idx_vocabulary_cloud_owner_provider"`
	TokenCipher string    `json:"-"`
	Enabled     bool      `json:"enabled"`
	TargetID    string    `json:"target_id"`
	TargetName  string    `json:"target_name"`
	AddToStudy  bool      `json:"add_to_study"`
	CreatedAt   time.Time `json:"-"`
	UpdatedAt   time.Time `json:"-"`
	Configured  bool      `json:"configured" gorm:"-"`
	Pending     int64     `json:"pending" gorm:"-"`
	Failed      int64     `json:"failed" gorm:"-"`
	Succeeded   int64     `json:"succeeded" gorm:"-"`
	LastError   string    `json:"last_error" gorm:"-"`
}

func (CloudConnection) TableName() string { return "vocabulary_cloud_connections" }

type CloudOperation struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	OwnerID      string    `json:"-"`
	ConnectionID string    `json:"-" gorm:"uniqueIndex:idx_vocabulary_cloud_word_target"`
	Provider     string    `json:"provider"`
	WordID       string    `json:"-" gorm:"uniqueIndex:idx_vocabulary_cloud_word_target"`
	Term         string    `json:"term"`
	TargetID     string    `json:"-" gorm:"uniqueIndex:idx_vocabulary_cloud_word_target"`
	AddToStudy   bool      `json:"-" gorm:"uniqueIndex:idx_vocabulary_cloud_word_target"`
	Status       string    `json:"status"`
	LastError    string    `json:"last_error"`
	JobID        string    `json:"-"`
	CreatedAt    time.Time `json:"-"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (CloudOperation) TableName() string { return "vocabulary_cloud_operations" }

type CloudConnectionInput struct {
	Token      string `json:"token"`
	Enabled    bool   `json:"enabled"`
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
	AddToStudy bool   `json:"add_to_study"`
}
