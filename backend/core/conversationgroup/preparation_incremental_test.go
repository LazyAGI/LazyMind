package conversationgroup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

type interruptedPreparation struct {
	preparationFixture
	fail  bool
	sizes []int
}

func (p *interruptedPreparation) ResolveBatch(ctx context.Context, db *gorm.DB, uid string, inputs []json.RawMessage, config map[string]any) ([]algo.ConversationTitleResult, error) {
	p.sizes = append(p.sizes, len(inputs))
	return p.preparationFixture.ResolveBatch(ctx, db, uid, inputs, config)
}

func (p *interruptedPreparation) Persist(_ context.Context, _ *gorm.DB, conv orm.Conversation, _ json.RawMessage, _ algo.ConversationTitleResult) error {
	if p.fail && conv.ID == "c01" {
		return errors.New("injected persistence failure")
	}
	return nil
}

func TestPreparationBatchRollbackAndResume(t *testing.T) {
	previous := titlePreparer
	fixture := &interruptedPreparation{fail: true}
	titlePreparer = fixture
	t.Cleanup(func() { titlePreparer = previous })
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.AsyncJob{}, &orm.ConversationGroupMember{})
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	job := asyncjob.Job{ID: "j", AttemptCount: 1}
	if err := db.Create(&orm.AsyncJob{ID: "j", Status: "running", JobType: organizerJobType, AttemptCount: 1, LockUntil: &until, NextRunAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(organizerPreparation{Total: 21})
	run := orm.ConversationOrganizerRun{ID: "r", UserID: "u", Status: "running", Stage: "preparing", JobID: "j", PreparationJSON: raw, SnapshotJSON: json.RawMessage(`{"id":"r","conversations":[],"groups":[]}`), ModelConfigJSON: json.RawMessage(`{}`)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 21; i++ {
		id := fmt.Sprintf("c%02d", i)
		if err := db.Create(&orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: "r", UserID: "u", ConversationID: id, Ordinal: i, PreparationStatus: "pending", FrozenInput: json.RawMessage(`{}`)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareOrganizer(t.Context(), db.DB, &run, job, nil); err == nil {
		t.Fatal("expected persistence failure")
	}
	var done int64
	db.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=? AND preparation_status=?", run.ID, "done").Count(&done)
	if done != 0 {
		t.Fatalf("partial batch escaped rollback: %d", done)
	}
	if err := db.First(&run, "id=?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	progress := runDTO(t.Context(), db.DB, run, false)["progress"].(map[string]any)
	if progress["preparation_batch_current"] != 1 || progress["preparation_batch_completed"] != 0 || progress["preparation_batch_total"] != 2 {
		t.Fatalf("incorrect pending progress: %v", progress)
	}
	fixture.fail = false
	if err := prepareOrganizer(t.Context(), db.DB, &run, job, nil); err != nil {
		t.Fatal(err)
	}
	var preparation organizerPreparation
	if err := json.Unmarshal(run.PreparationJSON, &preparation); err != nil {
		t.Fatal(err)
	}
	if preparation.Current != 21 || preparation.BatchCurrent != 2 || preparation.BatchTotal != 2 || !preparation.Sealed || fmt.Sprint(fixture.sizes) != "[20 20 1]" {
		t.Fatalf("invalid resumed progress: %+v sizes=%v", preparation, fixture.sizes)
	}
}

type preparationFixture struct{}

func (preparationFixture) Freeze(context.Context, *gorm.DB, orm.Conversation) (TitlePreparation, error) {
	return TitlePreparation{}, nil
}
func (preparationFixture) ResolveBatch(_ context.Context, _ *gorm.DB, _ string, inputs []json.RawMessage, _ map[string]any) ([]algo.ConversationTitleResult, error) {
	if len(inputs) > 20 {
		return nil, fmt.Errorf("oversized batch: %d", len(inputs))
	}
	results := make([]algo.ConversationTitleResult, len(inputs))
	for i := range results {
		results[i] = algo.ConversationTitleResult{Status: "succeeded", Output: algo.ConversationTitle{Summary: "处理工作", IntentStatus: "provisional"}}
	}
	return results, nil
}
func (preparationFixture) Persist(context.Context, *gorm.DB, orm.Conversation, json.RawMessage, algo.ConversationTitleResult) error {
	return nil
}

func TestPreparationWritesScaleAndResume(t *testing.T) {
	previous := titlePreparer
	titlePreparer = preparationFixture{}
	t.Cleanup(func() { titlePreparer = previous })
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
			raw, _ := json.Marshal(organizerPreparation{Total: n, Current: 1})
			run := orm.ConversationOrganizerRun{ID: "r", UserID: "u", Status: "running", JobID: "j", PreparationJSON: raw, SnapshotJSON: json.RawMessage(`{"id":"r","conversations":[],"groups":[]}`), ModelConfigJSON: json.RawMessage(`{}`)}
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
			expectedWrites := (n-1+19)/20 + 1
			if len(snapshot.Conversations) != n || snapshot.Conversations[0].Summary != "已完成摘要" || updates != expectedWrites {
				t.Fatalf("resume lost data: items=%d writes=%d", len(snapshot.Conversations), updates)
			}
			// Re-enter after sealing: no model work or checkpoint writes are repeated.
			if err := prepareOrganizer(t.Context(), db.DB, &run, job, map[string]any{}); err != nil {
				t.Fatal(err)
			}
			if updates != expectedWrites {
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
