package orm

import "time"

type UserReadingPolicy struct {
	UserID     string    `gorm:"primaryKey;column:user_id;type:varchar(255)"`
	PolicyJSON string    `gorm:"column:policy_json;type:text;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null"`
}

func (UserReadingPolicy) TableName() string { return "user_reading_policies" }
