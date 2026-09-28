-- +migrate Dialect postgres
ALTER TABLE user_schedules ADD COLUMN notification_config TEXT;
ALTER TABLE user_schedules ADD COLUMN notification_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE task_center_tasks ADD COLUMN notification_config TEXT;
ALTER TABLE task_center_tasks ADD COLUMN notification_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE user_notification_preferences (
    user_id VARCHAR(255) PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    revision BIGINT NOT NULL DEFAULT 1,
    defaults TEXT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE TABLE task_notifications (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    task_id VARCHAR(36) NOT NULL,
    schedule_id VARCHAR(36) NOT NULL,
    event_id VARCHAR(64) NOT NULL,
    event VARCHAR(16) NOT NULL,
    channel VARCHAR(16) NOT NULL,
    account_id VARCHAR(256) NOT NULL DEFAULT '',
    recipient_id VARCHAR(256) NOT NULL DEFAULT '',
    config_revision BIGINT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    content VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    reason VARCHAR(64) NOT NULL DEFAULT '',
    gateway_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX idx_task_notifications_task ON task_notifications(task_id);
CREATE INDEX idx_task_notifications_pending ON task_notifications(user_id, status, created_at);
CREATE TABLE desktop_notification_receipts (
    notification_id VARCHAR(64) NOT NULL,
    device_id VARCHAR(256) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL,
    reason VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (notification_id, device_id)
);

-- +migrate Dialect sqlite
ALTER TABLE user_schedules ADD COLUMN notification_config TEXT;
ALTER TABLE user_schedules ADD COLUMN notification_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE task_center_tasks ADD COLUMN notification_config TEXT;
ALTER TABLE task_center_tasks ADD COLUMN notification_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE user_notification_preferences (
    user_id VARCHAR(255) PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    revision BIGINT NOT NULL DEFAULT 1,
    defaults TEXT NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE task_notifications (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    task_id VARCHAR(36) NOT NULL,
    schedule_id VARCHAR(36) NOT NULL,
    event_id VARCHAR(64) NOT NULL,
    event VARCHAR(16) NOT NULL,
    channel VARCHAR(16) NOT NULL,
    account_id VARCHAR(256) NOT NULL DEFAULT '',
    recipient_id VARCHAR(256) NOT NULL DEFAULT '',
    config_revision BIGINT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    content VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    reason VARCHAR(64) NOT NULL DEFAULT '',
    gateway_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX idx_task_notifications_task ON task_notifications(task_id);
CREATE INDEX idx_task_notifications_pending ON task_notifications(user_id, status, created_at);
CREATE TABLE desktop_notification_receipts (
    notification_id VARCHAR(64) NOT NULL,
    device_id VARCHAR(256) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL,
    reason VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    PRIMARY KEY (notification_id, device_id)
);
