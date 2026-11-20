package orm

import "time"

// PaperGuide owns personal revision pointers. ScopeKey is a hash of the complete
// owner/dataset/document/language tuple; it never contains credentials.
type PaperGuide struct {
	ID                  string `gorm:"primaryKey;size:80"`
	ScopeKey            string `gorm:"uniqueIndex;size:64"`
	OwnerID             string `gorm:"index;size:128"`
	DatasetID           string `gorm:"index;size:128"`
	DocumentID          string `gorm:"index;size:128"`
	Language            string `gorm:"size:8"`
	PublishedRevisionID string
	CandidateRevisionID string
	Epoch               int64
	ChangeVersion       int64
	Deleted             bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}
type PaperGuideRevision struct {
	ID              string `gorm:"primaryKey;size:80"`
	GuideID         string `gorm:"index;size:80"`
	JobID           string
	Epoch           int64
	IdempotencyKey  string
	SourceSHA256    string
	IRDigest        string
	SnapshotPath    string
	ModelJSON       string `gorm:"type:text"`
	Fingerprint     string
	Status          string
	Stage           string
	ErrorCode       string
	DependencyID    string
	WaitCount       int
	CallsUsed       int
	PlannedCalls    int
	ExtractCalls    int
	RecoveryCalls   int
	CorrectionCount int
	ActiveMillis    int64
	ChangeVersion   int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
type PaperGuideStep struct {
	ID         string `gorm:"primaryKey;size:80"`
	RevisionID string `gorm:"uniqueIndex:idx_paper_guide_step_key;size:80"`
	StepKey    string `gorm:"uniqueIndex:idx_paper_guide_step_key;size:160"`
	InputHash  string
	OutputJSON string `gorm:"type:text"`
	ReviewJSON string `gorm:"type:text"`
	Epoch      int64
	Version    int64
	Published  bool
	CreatedAt  time.Time
}
type PaperGuideProgress struct {
	OwnerID    string `gorm:"primaryKey;size:128"`
	RevisionID string `gorm:"primaryKey;size:80"`
	SectionKey string
	BlockID    string
	Offset     int
	Mode       string
	UpdatedAt  time.Time
}

func (PaperGuideProgress) TableName() string { return "paper_guide_progress" }
