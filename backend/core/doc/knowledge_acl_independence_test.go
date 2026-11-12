package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

// Scan remains a source provider; its decision is deliberately hostile to every
// knowledge operation. Real isolated Core storage/ACL and original binding are used.
func aclIndependenceFixture(t *testing.T, offline bool) (*orm.DB, *int) {
	t.Helper()
	t.Setenv("LAZYMIND_RAG_ENABLED", "0")
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "acl-independence-token")
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.ACLModel{}, &orm.KBModel{}, &orm.UserGroupModel{}, &orm.ACLGroupModel{}, &orm.UserUIPreferences{}, &orm.UploadSession{}); err != nil {
		t.Fatal(err)
	}
	acl.InitStore(db)
	for _, id := range []string{"source", "target"} {
		seedDatasetCatalogDataset(t, db, id, "owner", id, "", []string{"scan"}, time.Now().UTC())
		if err := db.Model(&orm.Dataset{}).Where("id = ?", id).Updates(map[string]any{
			"tenant_id": "tenant-a", "processing_level": ProcessingLevelStored,
			"dataset_info": json.RawMessage(`{"existing":"preserved"}`),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	permissionCalls := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/scan/internal/source-access/by-dataset:batch" {
			permissionCalls++
			if offline {
				return nil, errors.New("Scan permission service offline")
			}
			return testJSONResponse(200, `{"items":[{"dataset_id":"source","source_id":"another-owner-source","exists":true,"allowed":false},{"dataset_id":"target","source_id":"another-owner-target","exists":true,"allowed":false}]}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/groups/internal") {
			return testJSONResponse(200, `{"groups":[]}`), nil
		}
		if r.URL.Path == "/api/scan/internal/sources/by-datasets" {
			return testJSONResponse(200, `{"source_map":{"source":true,"target":true}}`), nil
		}
		if strings.HasPrefix(r.URL.Path, "/api/scan/internal/sources/by-dataset/") {
			return testJSONResponse(200, `{"source":{"source_id":"another-owner-source"}}`), nil
		}
		t.Errorf("unexpected dependency: %s %s", r.Method, r.URL.Path)
		return testJSONResponse(503, `{}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return db, &permissionCalls
}

func aclIndependenceCaller(user string) DatasetCatalogCaller {
	return DatasetCatalogCaller{UserID: user, TenantID: "tenant-a", Permissions: []string{"document.read", "document.write"}}
}

func aclIndependenceGrant(t *testing.T, db *orm.DB, dataset, user, permission string) {
	t.Helper()
	if err := db.Create(&orm.ACLModel{ResourceType: acl.ResourceTypeDB, ResourceID: dataset,
		GranteeType: acl.GranteeUser, TargetID: user, Permission: permission}).Error; err != nil {
		t.Fatal(err)
	}
}

func aclIndependenceUpload(t *testing.T, user, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "manual.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte("manual original independent from Scan")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/datasets/source/tasks:batchUpload", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-User-Id", user)
	r.Header.Set("X-Tenant-Id", tenant)
	r = mux.SetURLVars(r, map[string]string{"dataset": "source"})
	w := httptest.NewRecorder()
	BatchUploadTasks(w, r)
	return w
}

func TestKnowledgeACLManualUploadDoesNotAskScan(t *testing.T) {
	for _, offline := range []bool{false, true} {
		for _, user := range []string{"owner", "contributor"} {
			t.Run(user+map[bool]string{false: "/source-denied", true: "/source-offline"}[offline], func(t *testing.T) {
				db, calls := aclIndependenceFixture(t, offline)
				aclIndependenceGrant(t, db, "source", "contributor", acl.PermissionDatasetUpload)
				w := aclIndependenceUpload(t, user, "tenant-a")
				if w.Code != 200 {
					t.Fatalf("Core-authorized manual upload rejected: %d %s", w.Code, w.Body.String())
				}
				var document orm.Document
				if err := db.Where("dataset_id = ? AND deleted_at IS NULL", "source").Take(&document).Error; err != nil {
					t.Fatal(err)
				}
				var task orm.Task
				if err := db.Where("doc_id = ?", document.ID).Take(&task).Error; err != nil {
					t.Fatal(err)
				}
				var ext documentExt
				if err := json.Unmarshal(document.Ext, &ext); err != nil {
					t.Fatal(err)
				}
				if data, err := os.ReadFile(ext.StoredPath); err != nil || string(data) != "manual original independent from Scan" {
					t.Fatalf("original lost: %q %v", data, err)
				}
				if task.CreateUserID != user || document.CreateUserID != user || *calls != 0 {
					t.Fatalf("wrong owner or external permission dependency: document=%s task=%s Scan=%d", document.CreateUserID, task.CreateUserID, *calls)
				}
			})
		}
	}
}

func TestKnowledgeACLRejectedUploadHasNoSideEffects(t *testing.T) {
	for _, tc := range []struct{ user, tenant string }{{"stranger", "tenant-a"}, {"owner", "another-tenant"}} {
		t.Run(tc.user+"/"+tc.tenant, func(t *testing.T) {
			db, calls := aclIndependenceFixture(t, false)
			w := aclIndependenceUpload(t, tc.user, tc.tenant)
			if w.Code < 400 {
				t.Errorf("Core denied upload returned success: %d", w.Code)
			}
			for _, model := range []any{&orm.Document{}, &orm.Task{}, &orm.UploadedFile{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Errorf("Core denial wrote %T: %d rows", model, count)
				}
			}
			if *calls != 0 {
				t.Errorf("Core denial delegated to Scan %d times", *calls)
			}
		})
	}
}

func TestKnowledgeACLOriginalBindingKeepsGroupSnapshotWithoutScan(t *testing.T) {
	db, calls := aclIndependenceFixture(t, false)
	for _, row := range []any{
		&orm.ACLGroupModel{ID: "uploaders"},
		&orm.UserGroupModel{UserID: "group-member", GroupID: "uploaders"},
		&orm.ACLModel{ResourceType: acl.ResourceTypeDB, ResourceID: "source", GranteeType: acl.GranteeGroup, TargetID: "uploaders", Permission: acl.PermissionDatasetUpload},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc, err := NewOriginalService(OriginalServiceDeps{DB: db.DB, SubmitProcessing: func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error) {
		return StartTaskResult{Status: "STARTED", Message: "document stored without parsing"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	caller := aclIndependenceCaller("group-member")
	ref, err := svc.AcquireOriginal(t.Context(), caller, AcquireOriginalRequest{Filename: "group.txt", Reader: strings.NewReader("group-scoped original")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	bound, err := svc.ImportOriginal(ctx, caller, ImportOriginalRequest{FileRef: ref.FileRef, DatasetID: "source"})
	if err != nil {
		t.Fatalf("group-authorized binding failed: %v", err)
	}
	if bound.Document.ID == "" || bound.Task.ID == "" || bound.Document.CreateUserID != caller.UserID || *calls != 0 {
		t.Fatalf("invalid group-authorized binding: %+v Scan=%d", bound, *calls)
	}
}

func TestKnowledgeACLCatalogActionsAndAttachmentUseCorePermissions(t *testing.T) {
	db, calls := aclIndependenceFixture(t, false)
	aclIndependenceGrant(t, db, "source", "contributor", acl.PermissionDatasetUpload)
	service := mustDatasetCatalogService(t, db.DB)
	caller := aclIndependenceCaller("contributor")
	listed, err := service.ListDatasets(t.Context(), DatasetListRequest{UserID: caller.UserID, Caller: caller, Limit: 20})
	if err != nil || len(listed.Datasets) != 1 || listed.Datasets[0].DatasetID != "source" {
		t.Errorf("Core-visible library hidden by Scan: %+v %v", listed, err)
	}
	current, err := service.GetDataset(t.Context(), DatasetGetRequest{UserID: caller.UserID, Caller: caller, DatasetID: "source"})
	if err != nil {
		t.Errorf("Core-visible detail rejected by Scan: %v", err)
	}
	if !slices.Contains(current.AllowedActions, "upload") || slices.Contains(current.AllowedActions, "write") || slices.Contains(current.AllowedActions, "delete") {
		t.Errorf("actions do not match Core upload ACL: %v", current.AllowedActions)
	}
	path := filepath.Join(buildDatasetDocFileDir("tenant-a", "source", "", "attachment"), "note.txt")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("source-backed library attachment"), 0600); err != nil {
		t.Fatal(err)
	}
	refs := []ChatSourceAttachmentReference{{Path: path, DatasetID: "source", Scope: ChatSourceAttachmentScopeDataset}}
	if _, err = ValidateChatSourceAttachments(t.Context(), db.DB, caller, refs); err != nil {
		t.Errorf("Core-authorized attachment rejected by Scan: %v", err)
	}
	if _, err = ValidateChatSourceAttachments(t.Context(), db.DB, aclIndependenceCaller("stranger"), refs); err == nil {
		t.Error("attachment bypassed Core ACL")
	}
	var dataset orm.Dataset
	if err = db.Where("id = ?", "source").Take(&dataset).Error; err != nil {
		t.Fatal(err)
	}
	if string(dataset.DatasetInfo) != `{"existing":"preserved"}` {
		t.Errorf("knowledge access still maintains origin cache: %s", dataset.DatasetInfo)
	}
	if *calls != 0 {
		t.Errorf("catalog/attachment invoked Scan permissions %d times", *calls)
	}
}

func TestKnowledgeACLMoveAndDeleteKeepSourceAndTargetACL(t *testing.T) {
	for _, tc := range []struct {
		name, source, target string
		allowed              bool
	}{
		{"allowed", acl.PermissionDatasetWrite, acl.PermissionDatasetUpload, true},
		{"source read-only", acl.PermissionDatasetRead, acl.PermissionDatasetUpload, false},
		{"target read-only", acl.PermissionDatasetWrite, acl.PermissionDatasetRead, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, calls := aclIndependenceFixture(t, false)
			aclIndependenceGrant(t, db, "source", "contributor", tc.source)
			aclIndependenceGrant(t, db, "target", "contributor", tc.target)
			folder := orm.Document{ID: "moving", DatasetID: "source", DisplayName: "manual-folder", DocumentType: "FOLDER", ResourceVersion: 1, Tags: []byte(`[]`), Ext: []byte(`{}`)}
			if err := db.Create(&folder).Error; err != nil {
				t.Fatal(err)
			}
			originals, err := NewOriginalService(OriginalServiceDeps{DB: db.DB})
			if err != nil {
				t.Fatal(err)
			}
			svc, err := NewResourceService(ResourceServiceDeps{DB: db.DB, Originals: originals})
			if err != nil {
				t.Fatal(err)
			}
			op := ResourceOperationContext{Caller: aclIndependenceCaller("contributor")}
			result, err := svc.Move(t.Context(), op, MoveResourcesRequest{Sources: []ResourceRef{{Kind: "folder", KnowledgeBaseID: "source", FolderID: folder.ID}}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}})
			if (err == nil) != tc.allowed {
				t.Errorf("move Core ACL outcome: allowed=%t result=%+v error=%v", tc.allowed, result, err)
			}
			var targetCount int64
			if e := db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "target").Count(&targetCount).Error; e != nil {
				t.Fatal(e)
			}
			if !tc.allowed && targetCount != 0 {
				t.Errorf("denied move wrote %d target documents", targetCount)
			}
			if tc.allowed && err == nil {
				if targetCount != 1 {
					t.Fatalf("move did not create target folder: %d", targetCount)
				}
				ref := result.Items[0].ResourceRef
				if _, err = svc.Delete(t.Context(), op, DeleteResourcesRequest{Targets: []ResourceRef{ref}}); err == nil {
					t.Error("upload-only target collaborator deleted folder")
				}
				op.Caller = aclIndependenceCaller("owner")
				deleted, e := svc.Delete(t.Context(), op, DeleteResourcesRequest{Targets: []ResourceRef{ref}})
				if e != nil || len(deleted.Items) != 1 || deleted.Items[0].ResourceStatus != "deleted" {
					t.Errorf("owner deletion rejected by Scan: %+v %v", deleted, e)
				}
			}
			if *calls != 0 {
				t.Errorf("resource ACL delegated to Scan %d times", *calls)
			}
		})
	}
}

func TestKnowledgeACLNoRAGCleanupDistinguishesNativeAndKnownIndexHistory(t *testing.T) {
	for _, history := range []string{"native", "live-external-id", "deleted-external-id"} {
		t.Run(history, func(t *testing.T) {
			db, _ := aclIndependenceFixture(t, false)
			path := filepath.Join(buildDatasetDocFileDir("tenant-a", "source", "", "native-upload"), "native.txt")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("native original"), 0600); err != nil {
				t.Fatal(err)
			}
			document := orm.Document{ID: "native-doc", DatasetID: "source", DisplayName: "native.txt", DocumentType: "TXT", FileID: "native-upload", ResourceVersion: 1, Tags: []byte(`[]`), Ext: mustJSON(documentExt{StoredPath: path, StoredName: "native.txt", FileSize: 15})}
			if history == "live-external-id" {
				document.LazyllmDocID = "known-remote-document"
			}
			upload := orm.UploadedFile{UploadFileID: "native-upload", DatasetID: "source", TenantID: "tenant-a", DocumentID: document.ID, Status: UploadedFileStateBound,
				Ext: mustJSON(uploadedFileExt{StoredPath: path, StoredName: "native.txt", FileSize: 15})}
			for _, row := range []any{&document, &upload} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if history == "deleted-external-id" {
				now := time.Now().UTC()
				if err := db.Create(&orm.Document{ID: "historical", DatasetID: "source", DisplayName: "previous.pdf", DocumentType: "PDF", LazyllmDocID: "known-deleted-remote-document", Tags: []byte(`[]`), Ext: []byte(`{}`), BaseModel: orm.BaseModel{DeletedAt: &now}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Allow the old source check to reach cleanup, isolating this regression
			// from the separately covered knowledge ACL change.
			previous := http.DefaultTransport
			algoCalls := 0
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/scan/internal/source-access/by-dataset:batch" {
					return testJSONResponse(200, `{"items":[{"dataset_id":"source","exists":true,"allowed":true}]}`), nil
				}
				if strings.HasPrefix(r.URL.Path, "/v1/kbs/") || r.URL.Path == "/v1/docs/delete" {
					algoCalls++
					return nil, errors.New("optional RAG service is not installed")
				}
				return previous.RoundTrip(r)
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			svc, err := NewResourceService(ResourceServiceDeps{DB: db.DB})
			if err != nil {
				t.Fatal(err)
			}
			op := ResourceOperationContext{Caller: aclIndependenceCaller("owner"), OperationKey: "delete-" + history}
			result, err := svc.Delete(t.Context(), op, DeleteResourcesRequest{Targets: []ResourceRef{{Kind: "knowledge_base", KnowledgeBaseID: "source"}}})
			if err != nil || len(result.Items) != 1 || result.Items[0].ResourceStatus != "deleted" {
				t.Fatalf("authorized Core deletion failed: %+v %v", result, err)
			}
			var active int64
			if err = db.Model(&orm.Dataset{}).Where("id = ? AND deleted_at IS NULL", "source").Count(&active).Error; err != nil {
				t.Fatal(err)
			}
			if active != 0 {
				t.Error("Core dataset remains visible after deletion")
			}
			item := result.Items[0]
			if history == "native" {
				if item.CleanupStatus != "submitted" || item.Error != nil || algoCalls != 0 {
					t.Errorf("native cleanup depends on absent RAG: %+v Algo=%d", item, algoCalls)
				}
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Errorf("native original was not removed: %v", e)
				}
			} else if item.CleanupStatus != "failed" || item.Error == nil || item.Error.Code != "CLEANUP_FAILED" {
				t.Errorf("known external history was falsely reported cleaned: %+v", item)
			}
			var receipt orm.ResourceOperationReceipt
			if err = db.Where("operation_key = ?", op.OperationKey).Take(&receipt).Error; err != nil {
				t.Fatal(err)
			}
			var saved ResourceOperationResult
			if err = json.Unmarshal(receipt.Result, &saved); err != nil {
				t.Fatal(err)
			}
			if len(saved.Items) != 1 || saved.Items[0].CleanupStatus != item.CleanupStatus {
				t.Errorf("cleanup outcome was not retained in receipt: %s", receipt.Result)
			}
		})
	}
}
