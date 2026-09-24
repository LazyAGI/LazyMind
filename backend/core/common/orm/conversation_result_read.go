package orm

// ConversationResultRead acknowledges exactly one result for its owner.
type ConversationResultRead struct {
	UserID          string `gorm:"type:varchar(255);primaryKey"`
	ConversationID  string `gorm:"type:varchar(36);primaryKey"`
	TerminalVersion string `gorm:"type:varchar(64);primaryKey"`
}

func (ConversationResultRead) TableName() string { return "conversation_result_reads" }

// ConversationResultReadState records the one-time historical baseline.
type ConversationResultReadState struct {
	ID          int  `gorm:"primaryKey;autoIncrement:false"`
	Initialized bool `gorm:"not null;default:false"`
}

func (ConversationResultReadState) TableName() string { return "conversation_result_read_state" }
