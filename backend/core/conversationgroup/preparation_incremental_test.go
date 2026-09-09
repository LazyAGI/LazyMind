package conversationgroup

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

type preparationFixture struct{}

func (preparationFixture) Freeze(context.Context, *gorm.DB, orm.Conversation) (OpeningPreparation, error) {
	return OpeningPreparation{}, nil
}
func (preparationFixture) Resolve(context.Context, *gorm.DB, string, json.RawMessage, map[string]any) (algo.OpeningTaskResult, error) {
	return algo.OpeningTaskResult{Status: "succeeded", Output: algo.OpeningDescription{Summary: "处理工作", IntentStatus: "ready"}}, nil
}
func (preparationFixture) Persist(context.Context, *gorm.DB, orm.Conversation, json.RawMessage, algo.OpeningTaskResult) error {
	return nil
}

func TestPreparationWritesScaleAndResume(t *testing.T) {
	previous := openingPreparer
	openingPreparer = preparationFixture{}
	t.Cleanup(func() { openingPreparer = previous })
	measurements := []int{}
	for _, n := range []int{1000, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.AsyncJob{})
			now := time.Now().UTC()
			until := now.Add(time.Hour)
			job := asyncjob.Job{ID: "j", AttemptCount: 1}
			if err := db.Create(&orm.AsyncJob{ID: "j", Status: "running", JobType: organizerJobType, AttemptCount: 1, LockUntil: &until, NextRunAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(organizerPreparation{Version: 2, Total: n, Current: 1})
			run := orm.ConversationOrganizerRun{ID: "r", UserID: "u", ProtocolVersion: 2, Status: "running", JobID: "j", PreparationJSON: raw, SnapshotJSON: json.RawMessage(`{"id":"r","conversations":[],"groups":[]}`), ModelConfigJSON: json.RawMessage(`{}`)}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			conversations := []orm.Conversation{}
			items := []orm.ConversationOrganizerSnapshotItem{}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("c%05d", i)
				conversations = append(conversations, orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u"}})
				row := orm.ConversationOrganizerSnapshotItem{RunID: "r", UserID: "u", ConversationID: id, Ordinal: i, PreparationStatus: "pending", FrozenInput: json.RawMessage(`{"input":"frozen"}`)}
				if i == 0 {
					row.PreparationStatus = "done"
					row.Summary = "已完成摘要"
				}
				items = append(items, row)
			}
			if err := db.CreateInBatches(conversations, 100).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.CreateInBatches(items, 100).Error; err != nil {
				t.Fatal(err)
			}
			totalBytes, updates := 0, 0
			if err := db.Callback().Update().Before("gorm:update").Register("measure_preparation", func(tx *gorm.DB) {
				if values, ok := tx.Statement.Dest.(map[string]any); ok {
					if raw, ok := values["preparation_json"].([]byte); ok {
						totalBytes += len(raw)
						updates++
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := prepareOrganizer(t.Context(), db.DB, &run, job, map[string]any{}); err != nil {
				t.Fatal(err)
			}
			var snapshot organizerSnapshot
			if err := json.Unmarshal(run.SnapshotJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Conversations) != n || snapshot.Conversations[0].Summary != "已完成摘要" || updates != n {
				t.Fatalf("resume lost data: items=%d writes=%d", len(snapshot.Conversations), updates)
			}
			// Re-enter after sealing: no model work or checkpoint writes are repeated.
			if err := prepareOrganizer(t.Context(), db.DB, &run, job, map[string]any{}); err != nil {
				t.Fatal(err)
			}
			if updates != n {
				t.Fatal("sealed preparation repeated")
			}
			measurements = append(measurements, totalBytes)
			t.Logf("conversations=%d preparation_json_bytes=%d writes=%d", n, totalBytes, updates)
		})
	}
	if len(measurements) == 2 && measurements[1] > measurements[0]*12 {
		t.Fatalf("superlinear preparation writes: %v", measurements)
	}
}
