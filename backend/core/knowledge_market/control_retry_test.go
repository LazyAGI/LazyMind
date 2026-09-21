package knowledge_market

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestMarketControlRetryLeavesSuccessfulAndCanceledFilesUntouched(t *testing.T) {
	newTaskTestRouter(t)
	db := store.DB()
	if err := db.AutoMigrate(&orm.Dataset{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Dataset{ID: "ds-control", KbID: "ds-control", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "user-a", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds-control", `{"task_ids":["success","canceled","failed"]}`, now)
	for id, state := range map[string]string{"success": "SUCCESS", "canceled": "CANCELED", "failed": "FAILED"} {
		insertTask(t, db, id, "ds-control", state)
	}
	var before []orm.Task
	if err := db.Where("id IN ?", []string{"success", "canceled"}).Order("id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"market_item_id":"law-cn","user_id":"user-a","retry_only":true}`)
	result, err := HandleUpdateJob(context.Background(), asyncjob.Job{ID: "retry-control", PayloadJSON: payload}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Submitted int `json:"submitted"`
	}
	if err := json.Unmarshal(result.ResultJSON, &response); err != nil {
		t.Fatal(err)
	}
	if response.Submitted != 1 {
		t.Errorf("failure-only retry submitted %d files, want 1", response.Submitted)
	}
	var after []orm.Task
	if err := db.Where("id IN ?", []string{"success", "canceled"}).Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("retry changed a successful or intentionally canceled file")
	}
}
