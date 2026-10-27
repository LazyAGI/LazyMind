package doc

import (
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func TestProcessingPublicationRejectsDeletedMovedAndChangedOwnership(t *testing.T) {
	for _, change := range []string{"deleted document", "deleted dataset", "deleted task", "moved document", "version changed"} {
		t.Run(change, func(t *testing.T) {
			db := newDocumentTestDB(t)
			now := time.Now().UTC()
			seedDocumentServiceDatasetWithKBAndAlgo(t, db, "dataset", "dataset", "general_algo", "owner", now)
			document := orm.Document{ID: "doc", DatasetID: "dataset", ResourceVersion: 1, DisplayName: "source.txt", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
			task := orm.Task{ID: "task", DocID: "doc", DatasetID: "dataset", BaseModel: document.BaseModel}
			if err := db.Create(&document).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			switch change {
			case "deleted document":
				db.Model(&orm.Document{}).Where("id = ?", document.ID).Update("deleted_at", now)
			case "deleted dataset":
				db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("deleted_at", now)
			case "deleted task":
				db.Model(&task).Update("deleted_at", now)
			case "moved document":
				db.Model(&orm.Document{}).Where("id = ?", document.ID).Update("dataset_id", "other")
			case "version changed":
				db.Model(&orm.Document{}).Where("id = ?", document.ID).Update("resource_version", 2)
			}
			result, err := bindExternalBatchAddResults("dataset", []orm.Task{task}, []orm.Document{document}, []documentExt{{StoredPath: "/source.txt"}}, []addResultItem{{CoreTaskID: "task", DocID: "external", TaskID: "external-task"}})
			if err == nil || len(result) != 0 {
				t.Fatalf("stale publication reported success: %+v %v", result, err)
			}
			var stored orm.Task
			db.Where("id = ?", "task").Take(&stored)
			if stored.LazyllmTaskID != "" {
				t.Fatal("stale task published")
			}
		})
	}
}
