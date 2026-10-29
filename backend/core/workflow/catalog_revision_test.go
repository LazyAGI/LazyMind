package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
)

func TestWorkflowCatalogReadsPinnedRevisionInsteadOfLatestHead(t *testing.T) {
	db := newHandlerTestDB(t)
	seedCatalogWorkflow(t, db, "versioned-ui")
	// Publishing changes the head, never the configuration bound to an older run.
	body := []byte("id: versioned-ui\nname: Version Two\nslots:\n  - {id: review, type: text}\nui:\n  tabs:\n    - {id: review, label: Review, slots: [{id: review}]}\n    - {id: result, label: Result, slots: []}\ni18n:\n  zh-CN:\n    tabs:\n      review: {label: 审核}\n")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.WorkflowRevision{ID: "revision-v2", WorkflowResourceID: "resource-versioned-ui", RevisionNo: 2, TreeHash: hash, CreatedAt: now},
		&orm.WorkflowBlob{Hash: hash, Size: int64(len(body)), Content: body, CreatedAt: now},
		&orm.WorkflowRevisionEntry{RevisionID: "revision-v2", Path: "workflow.yaml", EntryType: "file", BlobHash: &hash, Size: int64(len(body))},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.WorkflowResource{}).Where("id = ?", "resource-versioned-ui").Updates(map[string]any{"head_revision_id": "revision-v2", "version": 2}).Error; err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, revision, language, wantName, wantTabs string
	}{
		{"old run", "revision-versioned-ui", "en-US", "Test Workflow", "result"},
		{"new run", "revision-v2", "en-US", "Version Two", "review,result"},
		{"new localized run", "revision-v2", "zh-CN", "Version Two", "review,result"},
		{"catalog without pin", "", "en-US", "Version Two", "review,result"},
		{"old localized run after reading new", "revision-versioned-ui", "zh-CN", "测试工作流", "result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/workflows/versioned-ui?revision_id="+tc.revision, nil)
			req = mux.SetURLVars(req, map[string]string{"workflow_id": "versioned-ui"})
			req.Header.Set("X-User-Id", "user-1")
			req.Header.Set("Accept-Language", tc.language)
			rec := httptest.NewRecorder()
			GetWorkflowInfo(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var spec struct {
				Name string `json:"name"`
				UI   struct {
					Tabs []struct{ ID, Label string } `json:"tabs"`
				} `json:"ui"`
				Raw string `json:"workflow_yaml_raw"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, tab := range spec.UI.Tabs {
				ids = append(ids, tab.ID)
			}
			if spec.Name != tc.wantName || strings.Join(ids, ",") != tc.wantTabs {
				t.Fatalf("wrong revision: name=%q tabs=%v", spec.Name, ids)
			}
			if tc.revision == "revision-v2" && spec.Raw != string(body) {
				t.Fatal("raw files must come from the same pinned revision as the UI")
			}
			if tc.language == "zh-CN" && tc.revision == "revision-v2" && spec.UI.Tabs[0].Label != "审核" {
				t.Fatalf("pinned revision was not localized: %#v", spec.UI.Tabs)
			}
		})
	}
}

func TestWorkflowCatalogDoesNotFallbackForUnavailablePinnedRevision(t *testing.T) {
	db := newHandlerTestDB(t)
	seedCatalogWorkflow(t, db, "protected-ui")
	if err := db.Model(&orm.WorkflowResource{}).Where("id = ?", "resource-protected-ui").Update("owner_user_id", "owner").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.WorkflowResource{ID: "other-resource", WorkflowRef: "user:other-ui", WorkflowID: "other-ui", RelativeRoot: "user/other-ui", OwnerUserID: "owner", OwnerScope: "user", SourceType: "user", Status: "active", HeadRevisionID: "other-revision"},
		&orm.WorkflowRevision{ID: "other-revision", WorkflowResourceID: "other-resource", RevisionNo: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ owner, workflow, revision string }{
		{"owner", "protected-ui", "missing-revision"},
		{"owner", "protected-ui", "other-revision"},
		{"another-user", "protected-ui", "revision-protected-ui"},
		{"owner", "another-workflow", "revision-protected-ui"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/workflows/"+tc.workflow+"?revision_id="+tc.revision, nil)
		req = mux.SetURLVars(req, map[string]string{"workflow_id": tc.workflow})
		req.Header.Set("X-User-Id", tc.owner)
		rec := httptest.NewRecorder()
		GetWorkflowInfo(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("request %#v: status=%d body=%s", tc, rec.Code, rec.Body.String())
		}
	}
}
