package doc

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

func TestStartMoveTasksWithoutRAG(t *testing.T) {
	for _, tc := range []struct {
		name, kind, destination, parent, permission string
		allowed                                     bool
	}{
		{"same library file", "FILE", "source", "destination", acl.PermissionDatasetWrite, true},
		{"same library folder", "FOLDER", "source", "destination", acl.PermissionDatasetWrite, true},
		{"cross library", "FOLDER", "target", "", acl.PermissionDatasetWrite, false},
		{"missing destination", "FOLDER", "source", "missing-folder", acl.PermissionDatasetWrite, false},
		{"self destination", "FOLDER", "source", "moving", acl.PermissionDatasetWrite, false},
		{"read only", "FOLDER", "source", "destination", acl.PermissionDatasetRead, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, permissionCalls := aclIndependenceFixture(t, false)
			aclIndependenceGrant(t, db, "source", "contributor", tc.permission)
			aclIndependenceGrant(t, db, "target", "contributor", acl.PermissionDatasetWrite)
			for _, row := range []orm.Document{
				{ID: "moving", DatasetID: "source", DisplayName: "moving", DocumentType: tc.kind, ResourceVersion: 1, Tags: []byte(`[]`), Ext: []byte(`{}`)},
				{ID: "destination", DatasetID: "source", DisplayName: "destination", DocumentType: "FOLDER", ResourceVersion: 1, Tags: []byte(`[]`), Ext: []byte(`{}`)},
			} {
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			task := orm.Task{ID: "move-task", DatasetID: "source", DocID: "moving", TaskType: string(TaskTypeMove), TargetDatasetID: tc.destination, TargetPID: tc.parent, Ext: mustJSON(taskExt{TaskState: string(TaskStateCreating)})}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/datasets/source/tasks:start", nil)
			req.Header.Set("X-User-Id", "contributor")
			req.Header.Set("X-Tenant-Id", "tenant-a")
			results, err := startTasksInternal(req, "source", []string{task.ID})
			if err != nil && tc.allowed {
				t.Fatalf("move rejected: %v; results=%+v", err, results)
			}
			if len(results) != 1 {
				t.Fatalf("missing task result: %+v", results)
			}
			result := results[0]
			if (result.Status == "STARTED") != tc.allowed {
				t.Fatalf("allowed=%t result=%+v", tc.allowed, result)
			}
			var row orm.Document
			if err := db.Where("id=?", "moving").Take(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.DatasetID != "source" || row.DeletedAt != nil {
				t.Fatalf("source unexpectedly removed: %+v", row)
			}
			if tc.allowed {
				if row.PID != tc.parent {
					t.Fatalf("move did not change parent: %q", row.PID)
				}
				if err := db.Where("id=?", task.ID).Take(&task).Error; err != nil {
					t.Fatal(err)
				}
				var ext taskExt
				if err := json.Unmarshal(task.Ext, &ext); err != nil {
					t.Fatal(err)
				}
				if ext.TaskState != string(TaskStateSucceeded) {
					t.Fatalf("task not completed: %+v", ext)
				}
			} else {
				if row.PID != "" {
					t.Fatalf("denied move changed parent: %q", row.PID)
				}
				if tc.name != "cross library" && strings.Contains(result.Message, "RAG component is required") {
					t.Fatalf("RAG gate masked actual validation: %+v", result)
				}
				if tc.name == "cross library" && !strings.Contains(result.Message, "RAG component is required") {
					t.Fatalf("cross-library gate bypassed: %+v", result)
				}
			}
			if *permissionCalls != 0 {
				t.Fatalf("unexpected Scan authorization calls: %d", *permissionCalls)
			}
		})
	}
}
