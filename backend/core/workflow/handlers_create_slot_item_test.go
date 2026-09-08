package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
)

func TestCreateSlotItemAfterRewindUsesHistoricalRevisionMetadata(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatalf("migrate human artifacts: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-rewound-list", ConversationID: "conversation-1", WorkflowID: "ppt-workflow",
		Status: SessionStatusWaiting, CurrentStepID: "generate_backgrounds",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{
		ID: "step-generate-backgrounds-2", SessionID: "session-rewound-list",
		StepID: "generate_backgrounds", Attempt: 2, TaskID: "task-backgrounds-2",
		Status: StepStatusRunning, Validity: "effective", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create current step: %v", err)
	}
	oldIndex := 0
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "background-revision-1", SessionID: "session-rewound-list", SlotID: "background_images",
		Revision: 1, ListIndex: &oldIndex, Selected: false, Validity: "stale",
		ChangeSource: "ai", Slot: "background_images", StepID: "generate_backgrounds",
		Attempt: 1, CreatedAt: now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("create stale revision: %v", err)
	}
	// GORM applies the model's selected=true default when a false zero-value is
	// inserted, so mirror the rewind transition explicitly.
	if err := db.Model(&orm.WorkflowSlotRevision{}).
		Where("id = ?", "background-revision-1").
		Update("selected", false).Error; err != nil {
		t.Fatalf("deselect stale revision: %v", err)
	}
	if err := db.Create(&orm.WorkflowSlotOrder{
		SessionID: "session-rewound-list", SlotID: "background_images",
		OrderList: json.RawMessage(`[0]`), UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create slot order: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/workflow-sessions/session-rewound-list/slots/background_images/items",
		jsonBody(`{"value":{"path":"/tmp/page_002_background.png"},"content_type":"image","caption":"page 2","insert_before":2}`),
	)
	req = mux.SetURLVars(req, map[string]string{
		"session_id": "session-rewound-list", "slot_id": "background_images",
	})
	rec := httptest.NewRecorder()
	CreateSlotItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create item after rewind: got %d, body=%s", rec.Code, rec.Body.String())
	}

	var selected orm.WorkflowSlotRevision
	if err := db.Where(
		"session_id = ? AND slot_id = ? AND selected = ?",
		"session-rewound-list", "background_images", true,
	).First(&selected).Error; err != nil {
		t.Fatalf("load inserted revision: %v", err)
	}
	if selected.ListIndex == nil || *selected.ListIndex != 1 {
		t.Fatalf("inserted list_index = %v, want 1", selected.ListIndex)
	}
	if selected.StepID != "generate_backgrounds" || selected.Attempt != 2 {
		t.Fatalf("inserted origin = %s attempt %d, want current generate_backgrounds attempt 2", selected.StepID, selected.Attempt)
	}

	order, err := GetSlotOrder(req.Context(), db.DB, "session-rewound-list", "background_images")
	if err != nil || order == nil {
		t.Fatalf("load slot order: order=%v err=%v", order, err)
	}
	var orderList []int
	if err := json.Unmarshal(order.OrderList, &orderList); err != nil {
		t.Fatalf("decode slot order: %v", err)
	}
	if len(orderList) != 2 || orderList[0] != 0 || orderList[1] != 1 {
		t.Fatalf("slot order = %v, want [0 1]", orderList)
	}
}
