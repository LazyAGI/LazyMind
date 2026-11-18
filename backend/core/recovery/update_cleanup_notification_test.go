package recovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestUpdateCleanupWaitsForCommittedDeletionNotification(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	t.Setenv("LAZYMIND_AGENTIC_WORKSPACE", t.TempDir())
	db := orm.MigrateAllModelsForTest(t)
	seedTrashedConversation(t, db, "expired-notify", time.Now().UTC().Add(-time.Second))
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs []string `json:"conversation_ids"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat/session-env:clear" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body.IDs) != 1 || body.IDs[0] != "expired-notify" {
			t.Error("unexpected cleanup fixture request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	defer server.Close()
	defer unblock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := Start(ctx, db.DB, time.Hour)
	defer func() {
		cancel()
		unblock()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("cleanup fixture did not finish")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("committed deletion did not issue its notification")
	}
	var count int64
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "expired-notify").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("notification preceded deletion commit: count=%d err=%v", count, err)
	}
	cancel()
	select {
	case <-done:
		t.Fatal("cleanup completed while its committed deletion notification was blocked")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not complete after notification finished")
	}
}
