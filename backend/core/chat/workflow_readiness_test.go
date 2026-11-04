package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestInvalidWorkflowDoesNotCreateChatOrWorkRecords(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "work"}[background], func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{}, &orm.TaskCenterTask{}, &orm.WorkflowSession{}, &orm.WorkflowResource{}, &orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{})
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if err := db.Create(&orm.WorkflowResource{ID: "broken", WorkflowRef: "builtin:broken", WorkflowID: "broken", Name: "Broken", Status: "active", HeadRevisionID: "broken-revision"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowRevision{ID: "broken-revision", WorkflowResourceID: "broken", RevisionNo: 1}).Error; err != nil {
				t.Fatal(err)
			}
			mode := "false"
			if background {
				mode = "true"
			}
			body := `{"input":[{"input_type":"text","text":"Run broken"}],"run_in_background":` + mode + `,"mentions":[{"mention_id":"m","type":"workflow","resource_id":"builtin:broken","display_name":"Broken"}]}`
			req := httptest.NewRequest(http.MethodPost, "/api/core/chat/conversations", strings.NewReader(body))
			req.Header.Set("X-User-Id", "owner")
			rec := httptest.NewRecorder()
			ChatConversations(rec, req)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"ready":false`) {
				t.Fatalf("admission %d %s", rec.Code, rec.Body.String())
			}
			for _, model := range []any{&orm.Conversation{}, &orm.ChatHistory{}, &orm.TaskCenterTask{}, &orm.WorkflowSession{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("record created before admission: model=%T count=%d err=%v", model, count, err)
				}
			}
		})
	}
}
