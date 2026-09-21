package orm

import "time"

// SkillRecording is a durable conversation card. Source frames are erased after generation.
type SkillRecording struct {
	Evidence       string    `json:"-"`
	Attempt        int       `json:"-"`
	ID             string    `json:"id" gorm:"primaryKey"`
	UserID         string    `json:"-"`
	ConversationID string    `json:"conversation_id"`
	SkillID        string    `json:"skill_id"`
	Status         string    `json:"status"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Error          string    `json:"error"`
	Frames         string    `json:"-"`
	Notes          string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (SkillRecording) TableName() string { return "skill_recordings" }
