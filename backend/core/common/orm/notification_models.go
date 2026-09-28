package orm

import "time"

type UserNotificationPreferences struct {
	UserID    string    `gorm:"type:varchar(255);primaryKey" json:"-"`
	Enabled   bool      `gorm:"not null;default:true" json:"enabled"`
	Revision  int64     `gorm:"not null;default:1" json:"revision"`
	Defaults  RawJSON   `gorm:"type:text;not null" json:"defaults"`
	UpdatedAt time.Time `gorm:"not null" json:"-"`
}

// TaskNotification is a durable event handoff and desktop delivery record.
// External delivery attempts remain exclusively in the gateway's outbox.
type TaskNotification struct {
	ID             string    `gorm:"type:varchar(64);primaryKey" json:"notification_id"`
	UserID         string    `gorm:"type:varchar(255);not null;index:idx_task_notifications_pending,priority:1" json:"-"`
	TaskID         string    `gorm:"type:varchar(36);not null;index:idx_task_notifications_task" json:"task_id"`
	ScheduleID     string    `gorm:"type:varchar(36);not null" json:"schedule_id"`
	EventID        string    `gorm:"type:varchar(64);not null" json:"event_id"`
	Event          string    `gorm:"type:varchar(16);not null" json:"event"`
	Channel        string    `gorm:"type:varchar(16);not null" json:"channel"`
	AccountID      string    `gorm:"type:varchar(256);not null;default:''" json:"account_id,omitempty"`
	RecipientID    string    `gorm:"type:varchar(256);not null;default:''" json:"recipient_id,omitempty"`
	ConfigRevision int64     `gorm:"not null" json:"config_revision"`
	Title          string    `gorm:"type:text;not null" json:"title"`
	Body           string    `gorm:"type:text;not null" json:"body"`
	Content        string    `gorm:"type:varchar(16);not null" json:"content"`
	Status         string    `gorm:"type:varchar(16);not null;index:idx_task_notifications_pending,priority:2" json:"status"`
	Reason         string    `gorm:"type:varchar(64);not null;default:''" json:"reason,omitempty"`
	GatewayID      string    `gorm:"type:varchar(64);not null;default:''" json:"gateway_id,omitempty"`
	CreatedAt      time.Time `gorm:"not null;index:idx_task_notifications_pending,priority:3" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`
}

type DesktopNotificationReceipt struct {
	NotificationID string    `gorm:"type:varchar(64);primaryKey" json:"notification_id"`
	DeviceID       string    `gorm:"type:varchar(256);primaryKey" json:"device_id"`
	UserID         string    `gorm:"type:varchar(255);not null" json:"-"`
	Status         string    `gorm:"type:varchar(32);not null" json:"status"`
	Reason         string    `gorm:"type:varchar(64);not null;default:''" json:"reason,omitempty"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
}
