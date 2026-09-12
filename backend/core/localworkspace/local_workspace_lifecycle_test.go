package localworkspace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestRevokeCommitsBeforeStoppingBoundConversationsAndReportsFailures(t *testing.T) {
	db, grant := workspaceFixture(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil); SetStopConversationFunc(nil) })
	now := time.Now().UTC()
	for _, id := range []string{"task-1", "task-2"} {
		if err := db.Create(&orm.Conversation{ID: id, IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: id, WorkspaceID: grant.WorkspaceID, PermissionMode: PermissionAlwaysAsk, PermissionVersion: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var stopped []string
	SetStopConversationFunc(func(_ context.Context, userID, conversationID string) error {
		var status string
		if err := db.Model(&orm.LocalWorkspace{}).Select("status").Where("id = ?", grant.WorkspaceID).Scan(&status).Error; err != nil {
			t.Fatal(err)
		}
		if status != StatusRevoked {
			t.Fatalf("stop before commit: %s", status)
		}
		stopped = append(stopped, conversationID)
		if conversationID == "task-2" {
			return errors.New("stop failed")
		}
		return nil
	})
	request := httptest.NewRequest(http.MethodPost, "/local-workspaces/grant:revoke", strings.NewReader(`{"version":1}`))
	request.Header.Set("X-User-Id", "owner")
	request = mux.SetURLVars(request, map[string]string{"workspace_id": grant.WorkspaceID})
	response := httptest.NewRecorder()
	Revoke(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"stop_failed_count":1`) || !strings.Contains(response.Body.String(), `"affected_task_count":2`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	if len(stopped) != 2 {
		t.Fatalf("stopped=%v", stopped)
	}
}

func TestRevokeConflictDoesNotStop(t *testing.T) {
	db, grant := workspaceFixture(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil); SetStopConversationFunc(nil) })
	calls := 0
	SetStopConversationFunc(func(context.Context, string, string) error { calls++; return nil })
	request := httptest.NewRequest(http.MethodPost, "/local-workspaces/grant:revoke", strings.NewReader(`{"version":2}`))
	request.Header.Set("X-User-Id", "owner")
	request = mux.SetURLVars(request, map[string]string{"workspace_id": grant.WorkspaceID})
	response := httptest.NewRecorder()
	Revoke(response, request)
	if response.Code != 409 || calls != 0 {
		t.Fatalf("response=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}
