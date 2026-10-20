package documentdirectory

import (
	"context"
	"encoding/json"
	"lazymind/core/common/orm"
	"testing"
)

func TestSharedObservationWaitsForAllInventories(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	source := encoded(map[string]string{"provider": "local", "namespace": "host-a", "root": "/"})
	for _, id := range []string{"A", "B"} {
		must(t, db.Create(&orm.DocumentFolderInventory{ID: id, UserID: "u", ConversationID: "conv", Status: "scanning", SourceJSON: source, StateJSON: encoded(map[string]int{"node_format_version": 2})}).Error)
	}
	a, shared, outside := sample("/a"), sample("/shared"), sample("/outside")
	for _, c := range []Candidate{a, shared} {
		must(t, db.Create(&orm.DocumentFolderNode{ID: c.ResourceID, InventoryID: "A", NodeKey: c.ResourceID, ResourceKey: resourceKey(c), CandidateJSON: encoded(c)}).Error)
	}
	var obs Observation
	must(t, json.Unmarshal(encoded(map[string]any{"call_id": "read", "inventory_id": "A", "inventory_ids": []string{"A", "B"}, "candidates": []Candidate{a, shared, outside}}), &obs))
	for i := 0; i < 2; i++ {
		must(t, SaveObservations(ctx, db, "u", "conv", "h", "r", "q", []Observation{obs}))
	}
	var known, ordinary int64
	must(t, db.Model(&orm.DocumentObservation{}).Where("batch_id=?", "inv:A").Count(&known).Error)
	must(t, db.Model(&orm.DocumentObservation{}).Where("batch_id=?", "").Count(&ordinary).Error)
	if known != 1 || ordinary != 0 {
		t.Fatalf("known=%d ordinary=%d", known, ordinary)
	}
	va, _ := inventory(db, "u", "A")
	va.Status = "complete"
	must(t, db.Save(&va).Error)
	must(t, reconcileFolderObservations(db, va))
	must(t, db.Model(&orm.DocumentObservation{}).Where("batch_id=?", "").Count(&ordinary).Error)
	if ordinary != 0 {
		t.Fatal("released before B completed")
	}
	must(t, db.Create(&orm.DocumentFolderNode{ID: "B-shared", InventoryID: "B", NodeKey: "shared", ResourceKey: resourceKey(shared), CandidateJSON: encoded(shared)}).Error)
	vb, _ := inventory(db, "u", "B")
	vb.Status = "complete"
	must(t, db.Save(&vb).Error)
	must(t, reconcileFolderObservations(db, vb))
	must(t, reconcileFolderObservations(db, vb))
	var rows []orm.DocumentObservation
	must(t, db.Find(&rows).Error)
	if len(rows) != 3 {
		t.Fatalf("want A, B, outside: %+v", rows)
	}
	for _, r := range rows {
		var got Observation
		must(t, json.Unmarshal(r.PayloadJSON, &got))
		switch r.BatchID {
		case "inv:A":
			if len(got.Candidates) != 2 {
				t.Fatal(got)
			}
		case "inv:B":
			if len(got.Candidates) != 1 || got.Candidates[0].ResourceID != "/shared" {
				t.Fatal(got)
			}
		case "":
			if len(got.Candidates) != 1 || got.Candidates[0].ResourceID != "/outside" {
				t.Fatal(got)
			}
		default:
			t.Fatal(r.BatchID)
		}
	}
	for _, field := range []string{"user", "conversation", "account"} {
		t.Run(field, func(t *testing.T) {
			foreign := vb
			foreign.ID = "foreign-" + field
			if field == "user" {
				foreign.UserID = "other"
			}
			if field == "conversation" {
				foreign.ConversationID = "other"
			}
			if field == "account" {
				foreign.SourceJSON = encoded(map[string]string{"provider": "local", "namespace": "other"})
			}
			must(t, db.Create(&foreign).Error)
			var bad Observation
			must(t, json.Unmarshal(encoded(map[string]any{"call_id": "bad-" + field, "inventory_ids": []string{foreign.ID}, "candidates": []Candidate{a}}), &bad))
			if SaveObservations(ctx, db, "u", "conv", "h", "r", "q", []Observation{bad}) == nil {
				t.Fatal("foreign scope accepted")
			}
		})
	}
}

func TestLegacyUnsubmittedInventoryCannotCheckpointOrSubmit(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	v := orm.DocumentFolderInventory{ID: "legacy", UserID: "u", ConversationID: "conv", Status: "complete", Version: 1, SourceJSON: encoded(map[string]string{"provider": "local", "root": "/"}), StateJSON: encoded(map[string]any{})}
	must(t, db.Create(&v).Error)
	for _, operation := range []string{"folder_checkpoint", "folder_submit"} {
		if _, err := folderOperation(ctx, db, "u", "conv", Command{Operation: operation, Name: "old"}, FolderRequest{InventoryID: v.ID, Version: 1, Status: "scanning", State: encoded(map[string]any{})}); err == nil {
			t.Fatal("legacy inventory accepted", operation)
		}
	}
	if _, err := folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_page"}, FolderRequest{InventoryID: v.ID}); err != nil {
		t.Fatal(err)
	}
}
