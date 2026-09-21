package orm

import "time"

// EvolutionModelValidation is written only by trusted offline validation tooling.
// A connection check or a client-supplied capability flag is not evidence.
type EvolutionModelValidation struct {
	ModelRef          string    `gorm:"column:model_ref;type:varchar(160);primaryKey"`
	ValidationVersion string    `gorm:"column:validation_version;type:varchar(64);not null"`
	EvidenceID        string    `gorm:"column:evidence_id;type:varchar(255);not null"`
	Passed            bool      `gorm:"column:passed;not null;default:false"`
	VerifiedAt        time.Time `gorm:"column:verified_at;not null"`
	ExpiresAt         time.Time `gorm:"column:expires_at;not null"` // Legacy storage field; no longer expires admission or reports.
}

func (EvolutionModelValidation) TableName() string { return "evolution_model_validations" }
