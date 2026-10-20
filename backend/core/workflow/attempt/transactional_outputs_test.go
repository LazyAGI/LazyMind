package attempt

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"testing"
)

func TestDeclaredAtomicOutputsKeepLastSuccessOnFailure(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			service, db := testService(t)
			if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowRevision{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowRevision{ID: "r", CompiledGraph: json.RawMessage(`{"runtime":{"transactional_outputs":true}}`)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowSession{ID: "s", WorkflowID: "arbitrary-package", WorkflowRevisionID: "r", StateVersion: 1}).Error; err != nil {
				t.Fatal(err)
			}
			queue(t, service, "a", "s", "arbitrary-step")
			claim, err := service.Claim(t.Context(), "worker")
			if err != nil {
				t.Fatal(err)
			}
			rows := []orm.WorkflowSlotRevision{
				{ID: "old", SessionID: "s", SlotID: "body", Slot: "body", Revision: 1, Selected: true, Validity: "effective", ChangeSource: "human"},
				{ID: "new", SessionID: "s", SlotID: "body", Slot: "body", Revision: 2, Selected: false, Validity: "effective", ProducerAttemptID: "a"},
			}
			if err := db.Create(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "new").Update("selected", false).Error; err != nil {
				t.Fatal(err)
			}
			var selected orm.WorkflowSlotRevision
			if err := db.Where("selected = ?", true).First(&selected).Error; err != nil || selected.ID != "old" {
				t.Fatalf("%+v %v", selected, err)
			}
			if err := service.Terminal(t.Context(), "a", claim.LeaseToken, status, "", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			selected = orm.WorkflowSlotRevision{}
			if err := db.Where("selected = ?", true).First(&selected).Error; err != nil {
				t.Fatal(err)
			}
			expected := "old"
			if status == "succeeded" {
				expected = "new"
			}
			if selected.ID != expected {
				t.Fatalf("selected %s want %s", selected.ID, expected)
			}
			if err := service.Terminal(t.Context(), "a", claim.LeaseToken, status, "", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			var count int64
			db.Model(&orm.WorkflowSlotRevision{}).Where("selected = ?", true).Count(&count)
			if count != 1 {
				t.Fatal(count)
			}
		})
	}
}

func TestUndeclaredAtomicOutputsLeaveLegacySelectionAlone(t *testing.T) {
	_, db := testService(t)
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&orm.WorkflowSession{ID: "legacy", WorkflowID: "product_solution_delivery"})
	db.Create(&orm.WorkflowSlotRevision{ID: "output", SessionID: "legacy", SlotID: "body", Slot: "body", Selected: true, Validity: "effective", ProducerAttemptID: "a"})
	if err := finishTransactionalOutputs(t.Context(), db, orm.WorkflowSessionStep{ID: "a", SessionID: "legacy"}, "failed"); err != nil {
		t.Fatal(err)
	}
	var output orm.WorkflowSlotRevision
	db.First(&output, "id = ?", "output")
	if !output.Selected || output.Validity != "effective" {
		t.Fatalf("undeclared package changed: %+v", output)
	}
}
