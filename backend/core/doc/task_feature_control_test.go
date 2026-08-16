package doc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestStartTasksRejectsNewParsingWhenDocumentParsingIsPaused(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id":                          "u1",
		"task_center_enabled":              true,
		"skills_enabled":                   true,
		"workflows_enabled":                true,
		"mcp_enabled":                      true,
		"document_parsing_enabled":         false,
		"created_at":                       now,
		"updated_at":                       now,
		"chat_preference_notice_dismissed": false,
		"developer_mode_active":            false,
		"accepted_user_agreement_version":  "",
	}).Error; err != nil {
		t.Fatalf("seed user settings: %v", err)
	}
	if err := db.Create(&orm.Task{
		ID:          "parse-paused",
		DatasetID:   "dataset-1",
		TaskType:    string(TaskTypeParse),
		DisplayName: "report.pdf",
		BaseModel: orm.BaseModel{
			CreateUserID: "u1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("seed parse task: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/datasets/dataset-1/tasks:start", nil)
	req.Header.Set("X-User-Id", "u1")
	results, err := startTasksInternal(req, "dataset-1", []string{"parse-paused"})
	if err == nil {
		t.Fatal("expected paused parsing to reject the submission")
	}
	if len(results) != 1 || results[0].Status != "FAILED" || results[0].SubmitStatus != "REJECTED" ||
		!strings.Contains(results[0].Message, "document parsing is paused") {
		t.Fatalf("unexpected paused parsing result: %#v err=%v", results, err)
	}
}
