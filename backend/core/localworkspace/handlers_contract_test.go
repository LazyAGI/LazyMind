package localworkspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestWorkspaceListAndBindingRemainOwnerScoped(t *testing.T) {
	db, grant := workspaceFixture(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	conversation := orm.Conversation{ID: "listed-task", IsTaskConv: true,
		BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: conversation.ID,
		WorkspaceID: grant.WorkspaceID, PermissionMode: PermissionAlwaysAsk,
		PermissionVersion: 3, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/local-workspaces?query=project", nil)
	request.Header.Set("X-User-Id", "owner")
	response := httptest.NewRecorder()
	List(response, request)
	var listed struct {
		Code int
		Data struct {
			Items []PublicWorkspace `json:"items"`
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || listed.Code != 0 || len(listed.Data.Items) != 1 ||
		listed.Data.Items[0].AffectedTaskCount != 1 {
		t.Fatalf("list=%d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/conversations/listed-task:workspace", nil)
	request.Header.Set("X-User-Id", "owner")
	request = mux.SetURLVars(request, map[string]string{"conversation_id": conversation.ID})
	response = httptest.NewRecorder()
	ConversationBinding(response, request)
	var binding struct {
		Code int
		Data struct {
			WorkspaceID       string `json:"workspace_id"`
			PermissionMode    string `json:"permission_mode"`
			PermissionVersion int64  `json:"permission_version"`
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || binding.Data.WorkspaceID != grant.WorkspaceID ||
		binding.Data.PermissionMode != PermissionAlwaysAsk || binding.Data.PermissionVersion != 3 {
		t.Fatalf("binding=%d %s", response.Code, response.Body.String())
	}

	if err := db.Model(&orm.LocalWorkspace{}).Where("id = ?", grant.WorkspaceID).
		Updates(map[string]any{"status": StatusRevoked, "version": 2, "revoked_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/conversations/listed-task:workspace", nil)
	request.Header.Set("X-User-Id", "owner")
	request = mux.SetURLVars(request, map[string]string{"conversation_id": conversation.ID})
	response = httptest.NewRecorder()
	ConversationBinding(response, request)
	var revoked struct {
		Code int
		Data struct {
			Status      string `json:"status"`
			WorkspaceID string `json:"workspace_id"`
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &revoked); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || revoked.Data.Status != StatusRevoked || revoked.Data.WorkspaceID != grant.WorkspaceID {
		t.Fatalf("revoked binding=%d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/conversations/listed-task:workspace", nil)
	request.Header.Set("X-User-Id", "other")
	request = mux.SetURLVars(request, map[string]string{"conversation_id": conversation.ID})
	response = httptest.NewRecorder()
	ConversationBinding(response, request)
	if response.Code != 404 || string(response.Body.Bytes()) == "" {
		t.Fatalf("other binding=%d %s", response.Code, response.Body.String())
	}
}
