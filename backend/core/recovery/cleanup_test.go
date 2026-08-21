package recovery

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func seedTrashedConversation(t *testing.T, db *orm.DB, id string, expiresAt time.Time) {
	t.Helper()
	now := time.Now().UTC()
	deletedAt := now.Add(-time.Hour)
	if err := db.Create(&orm.Conversation{
		ID:             id,
		DisplayName:    id,
		ChannelID:      "default",
		TrashExpiresAt: &expiresAt,
		BaseModel: orm.BaseModel{
			CreateUserID: "cleanup-user",
			CreatedAt:    now,
			UpdatedAt:    now,
			DeletedAt:    &deletedAt,
		},
	}).Error; err != nil {
		t.Fatalf("seed trashed conversation: %v", err)
	}
}

func waitForConversationCount(t *testing.T, db *orm.DB, id string, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := db.Model(&orm.Conversation{}).Where("id = ?", id).Count(&count).Error; err != nil {
			t.Fatalf("count conversation: %v", err)
		}
		if count == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("conversation %s did not reach count %d", id, want)
}

func TestStartRunsCleanupAtStartupAndOnInterval(t *testing.T) {
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	db := orm.MigrateAllModelsForTest(t)
	now := time.Now().UTC()
	seedTrashedConversation(t, db, "expired-at-start", now.Add(-time.Second))
	seedTrashedConversation(t, db, "not-expired", now.Add(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Start(ctx, db.DB, 20*time.Millisecond)
	waitForConversationCount(t, db, "expired-at-start", 0)
	waitForConversationCount(t, db, "not-expired", 1)

	seedTrashedConversation(t, db, "expired-on-tick", time.Now().UTC().Add(-time.Second))
	waitForConversationCount(t, db, "expired-on-tick", 0)
}
