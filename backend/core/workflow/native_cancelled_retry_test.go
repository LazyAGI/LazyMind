package workflow

import (
	"lazymind/core/common/orm"
	"net/http"
	"testing"
)

func TestNativeAdvanceRetriesCancelledAttempt(t *testing.T) {
	for _, status := range []string{"cancelled", "canceled"} {
		t.Run(status, func(t *testing.T) {
			db, hash := setupBatchTransitionSession(t)
			prior, err := CreateSessionStep(t.Context(), db.DB, "batch-session", "branch_b", "cancelled-task", 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&prior).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			w, data := runBatchTransition(t, db, hash, "advance", []map[string]any{
				{"target_step_id": "branch_b", "task_id": "replacement-task", "user_input": "Continue after interruption"},
			})
			if w.Code != http.StatusOK || data["accepted"] != true {
				t.Fatalf("cancelled native advance rejected: status=%d body=%s", w.Code, w.Body.String())
			}
			if err := db.First(&prior, "id = ?", prior.ID).Error; err != nil {
				t.Fatal(err)
			}
			if prior.Validity != "stale" {
				t.Fatalf("prior attempt retained authority: %+v", prior)
			}
			var next orm.WorkflowSessionStep
			if err := db.Where("task_id = ?", "replacement-task").First(&next).Error; err != nil {
				t.Fatal(err)
			}
			if next.Attempt != 2 || next.Validity != "effective" {
				t.Fatalf("invalid replacement: %+v", next)
			}
		})
	}
}
