package doc

import (
	"context"
	"fmt"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"testing"
	"time"
)

func TestDocumentVisibilityRequiresLegacyBackfillAndKeepsTombstones(t *testing.T) {
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	if err := db.AutoMigrate(&readonlyorm.LazyLLMKBDocRow{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "dataset", "kb", "general_algo", "owner", now)
	for _, id := range []string{"legacy", "deleted"} {
		if err := db.Create(&readonlyorm.LazyLLMDocRow{DocID: id, Filename: id + ".pdf", Path: "/original/" + id, UploadStatus: "SUCCESS", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&readonlyorm.LazyLLMKBDocRow{KbID: "kb", DocID: id, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	tombstone := orm.Document{ID: "core-deleted", LazyllmDocID: "deleted", DatasetID: "dataset", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now, DeletedAt: &now}}
	if err := db.Create(&tombstone).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewDocumentVisibilityService(db.DB, db.DB)
	refs := []DocumentVisibilityRef{{KnowledgeID: "kb", DocumentID: "legacy"}, {KnowledgeID: "kb", DocumentID: "deleted"}, {KnowledgeID: "kb", DocumentID: "unknown"}}
	caller := DatasetCatalogCaller{UserID: "owner"}
	if _, err := svc.Filter(context.Background(), caller, refs); err == nil {
		t.Fatal("fence enabled before backfill")
	}
	if err := svc.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	allowed, err := svc.Filter(context.Background(), caller, refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed) != 1 || allowed[0].DocumentID != "legacy" {
		t.Fatalf("wrong active ownership: %+v", allowed)
	}
	if err := svc.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.Document{}).Where("dataset_id = ? AND lazyllm_doc_id = ?", "dataset", "deleted").Count(&count)
	if count != 1 {
		t.Fatal("backfill resurrected deleted source")
	}
}
func TestDocumentVisibilityBackfillRejectsAmbiguousTenantOwnership(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&readonlyorm.LazyLLMKBDocRow{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, id := range []string{"one", "two"} {
		seedDocumentServiceDatasetWithKBAndAlgo(t, db, id, "same-kb", "general_algo", id, now)
		db.Model(&orm.Dataset{}).Where("id = ?", id).Update("tenant_id", id)
	}
	svc := NewDocumentVisibilityService(db.DB, db.DB)
	if err := svc.Backfill(context.Background()); err == nil {
		t.Fatal("ambiguous tenant mapping inferred")
	}
}

func TestDocumentVisibilityRejectsLatePublicationAcrossRestart(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		t.Run(fmt.Sprint(initialized), func(t *testing.T) {
			db := newDocumentTestDB(t)
			installOriginalSourceTransport(t)
			if err := db.AutoMigrate(&readonlyorm.LazyLLMKBDocRow{}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			seedDocumentServiceDatasetWithKBAndAlgo(t, db, "dataset", "kb", "general_algo", "owner", now)
			if initialized {
				if err := NewDocumentVisibilityService(db.DB, db.DB).Backfill(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			document := orm.Document{ID: "core-original", DatasetID: "dataset", ResourceVersion: 1, DisplayName: "secret.txt", Ext: mustJSON(documentExt{StoredPath: "/original/secret.txt"}), BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
			task := orm.Task{ID: "task", DocID: document.ID, DatasetID: "dataset", BaseModel: document.BaseModel}
			if err := db.Create(&document).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			// External add started, then Core delete commits before the add response.
			db.Model(&orm.Document{}).Where("id = ?", document.ID).Update("deleted_at", now)
			if err := db.Create(&readonlyorm.LazyLLMDocRow{DocID: "late-external", Filename: "secret.txt", Path: "/original/secret.txt", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&readonlyorm.LazyLLMKBDocRow{KbID: "kb", DocID: "late-external", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := bindExternalBatchAddResults("dataset", []orm.Task{task}, []orm.Document{document}, []documentExt{{StoredPath: "/original/secret.txt"}}, []addResultItem{{CoreTaskID: task.ID, DocID: "late-external", TaskID: "external-task"}}); err == nil {
				t.Fatal("late publication accepted")
			}

			var rejected int64
			if err := db.Model(&orm.RejectedDocumentPublication{}).Where("dataset_id = ? AND external_doc_id = ?", "dataset", "late-external").Count(&rejected).Error; err != nil || rejected != 1 {
				t.Fatalf("rejection was lost on rollback: %d %v", rejected, err)
			}
			restarted := NewDocumentVisibilityService(db.DB, db.DB)
			if err := restarted.Backfill(context.Background()); err != nil {
				t.Fatal(err)
			}
			allowed, err := restarted.Filter(context.Background(), DatasetCatalogCaller{UserID: "owner"}, []DocumentVisibilityRef{{KnowledgeID: "kb", DocumentID: "late-external"}})
			if err != nil || len(allowed) != 0 {
				t.Fatalf("late publication resurrected: %+v %v", allowed, err)
			}
		})
	}
}

func TestDocumentVisibilityCompletedBackfillNeverAdmitsNewUnknownAssociations(t *testing.T) {
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	if err := db.AutoMigrate(&readonlyorm.LazyLLMKBDocRow{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "dataset", "kb", "general_algo", "owner", now)
	if err := NewDocumentVisibilityService(db.DB, db.DB).Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	db.Create(&readonlyorm.LazyLLMDocRow{DocID: "orphan", Filename: "orphan.txt", Path: "/orphan", CreatedAt: now, UpdatedAt: now})
	db.Create(&readonlyorm.LazyLLMKBDocRow{KbID: "kb", DocID: "orphan", CreatedAt: now, UpdatedAt: now})
	svc := NewDocumentVisibilityService(db.DB, db.DB)
	if err := svc.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	refs, err := svc.Filter(context.Background(), DatasetCatalogCaller{UserID: "owner"}, []DocumentVisibilityRef{{KnowledgeID: "kb", DocumentID: "orphan"}})
	if err != nil || len(refs) != 0 {
		t.Fatalf("new orphan admitted: %+v %v", refs, err)
	}
}

func TestDocumentVisibilityFailedBackfillRetryKeepsOriginalBoundary(t *testing.T) {
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	if err := db.AutoMigrate(&readonlyorm.LazyLLMKBDocRow{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "dataset", "kb", "general_algo", "owner", now)
	db.Create(&readonlyorm.LazyLLMKBDocRow{KbID: "kb", DocID: "historical", CreatedAt: now, UpdatedAt: now})
	if err := NewDocumentVisibilityService(db.DB, db.DB).Backfill(context.Background()); err == nil {
		t.Fatal("missing original accepted")
	}
	for _, id := range []string{"historical", "late"} {
		db.Create(&readonlyorm.LazyLLMDocRow{DocID: id, Filename: id + ".txt", Path: "/" + id, CreatedAt: now, UpdatedAt: now})
	}
	db.Create(&readonlyorm.LazyLLMKBDocRow{KbID: "kb", DocID: "late", CreatedAt: now, UpdatedAt: now})
	svc := NewDocumentVisibilityService(db.DB, db.DB)
	if err := svc.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	allowed, err := svc.Filter(context.Background(), DatasetCatalogCaller{UserID: "owner"}, []DocumentVisibilityRef{{KnowledgeID: "kb", DocumentID: "historical"}, {KnowledgeID: "kb", DocumentID: "late"}})
	if err != nil || len(allowed) != 1 || allowed[0].DocumentID != "historical" {
		t.Fatalf("retry extended historical boundary: %+v %v", allowed, err)
	}
}
