package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestDeclaredHeadArtifactActionUsesCurrentRevision(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowResource{}, &orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	resource := orm.WorkflowResource{
		ID: "ppt", WorkflowRef: "builtin:ppt-workflow", WorkflowID: "ppt-workflow",
		OwnerScope: "builtin", SourceType: "builtin", RelativeRoot: "workflows/builtin/ppt-workflow",
		Name: "PPT", HeadRevisionID: "final", Version: 19, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{
		ID: "final", WorkflowResourceID: "ppt", RevisionNo: 19, TreeHash: "final-tree",
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	manifest := []byte("id: ppt-workflow\nartifact_actions:\n  rewrite_selection:\n    revision_policy: head\n")
	hash := "manifest-hash"
	if err := db.Create(&orm.WorkflowBlob{Hash: hash, Size: int64(len(manifest)), Mime: "text/yaml", FileType: "workflow", Content: manifest, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: "final", Path: "workflow.yaml", EntryType: "file", BlobHash: &hash, Size: int64(len(manifest)), Mime: "text/yaml", FileType: "workflow", Mode: 420}).Error; err != nil {
		t.Fatal(err)
	}
	target := &artifactActionTarget{db: db, session: &orm.WorkflowSession{
		WorkflowID: "ppt-workflow", WorkflowRef: "builtin:ppt-workflow",
		WorkflowRevisionID: "legacy", WorkflowTreeHash: "legacy-tree",
	}}
	got, err := resolveArtifactActionWorkflow(t.Context(), target, "rewrite_selection")
	if err != nil {
		t.Fatal(err)
	}
	if got.revisionID != "final" || got.treeHash != "final-tree" {
		t.Fatalf("expected declared head action revision, got %#v", got)
	}
}

func TestOtherArtifactActionsRemainPinned(t *testing.T) {
	target := &artifactActionTarget{session: &orm.WorkflowSession{
		WorkflowID: "writer-workflow", WorkflowRevisionID: "pinned", WorkflowTreeHash: "tree",
	}}
	got, err := resolveArtifactActionWorkflow(t.Context(), target, "rewrite_selection")
	if err != nil {
		t.Fatal(err)
	}
	if got.revisionID != "pinned" || got.treeHash != "tree" {
		t.Fatalf("non-PPT action must stay pinned, got %#v", got)
	}
}

func TestPortableConversionDoesNotRequireModelConfiguration(t *testing.T) {
	for _, format := range []string{"markdown", "latex", "text"} {
		if !isPortableDocumentConversion(artifactActionPreviewBody{
			Action: "convert_document", Input: map[string]any{"output_format": format},
		}) {
			t.Fatalf("portable format %q should not require a model", format)
		}
	}
	for _, body := range []artifactActionPreviewBody{
		{Action: "convert_document", Input: map[string]any{"output_format": "native"}},
		{Action: "convert_document", Input: map[string]any{}},
		{Action: "rewrite_selection", Input: map[string]any{"output_format": "text"}},
	} {
		if isPortableDocumentConversion(body) {
			t.Fatalf("other actions must preserve model configuration: %#v", body)
		}
	}
}

func TestArtifactActionRevisionErrors(t *testing.T) {
	db := orm.MigrateTestDB(t,
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{},
		&orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
	)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session", ConversationID: "conversation", WorkflowID: "writer-workflow",
		Status: SessionStatusActive, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{
		ID: "step-1", SessionID: "session", StepID: "write_document", Attempt: 1,
		TaskID: "task-1", Status: "completed", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	humanID := "human-2"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: "session", Slot: "draft_document", ContentType: "json",
		Value: json.RawMessage(`{"data":"draft"}`), DraftVersion: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-2", SessionID: "session", SlotID: "draft_document", Slot: "draft_document",
		Revision: 2, Selected: true, ChangeSource: "human", HumanArtifactID: &humanID,
		StepID: "write_document", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, phase := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"preview", PreviewArtifactAction},
		{"execute", ExecuteArtifactAction},
	} {
		for _, testCase := range []struct {
			name, body, wantCode string
			wantStatus           int
		}{
			{"missing", `{"action":"rewrite_selection","input":{}}`, "REVISION_REQUIRED", http.StatusBadRequest},
			{"stale", `{"action":"rewrite_selection","base_revision":1,"base_draft_version":1,"input":{}}`, "REVISION_CONFLICT", http.StatusConflict},
			{"missing_draft", `{"action":"rewrite_selection","base_revision":2,"input":{}}`, "DRAFT_VERSION_REQUIRED", http.StatusBadRequest},
			{"stale_draft", `{"action":"rewrite_selection","base_revision":2,"base_draft_version":2,"input":{}}`, "DRAFT_VERSION_CONFLICT", http.StatusConflict},
		} {
			t.Run(phase.name+"_"+testCase.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/artifact-action", strings.NewReader(testCase.body))
				req = mux.SetURLVars(req, map[string]string{
					"session_id": "session", "slot_id": "draft_document", "list_index": "-1",
				})
				recorder := httptest.NewRecorder()
				phase.handler(recorder, req)
				if recorder.Code != testCase.wantStatus || responseData(t, recorder)["code"] != testCase.wantCode {
					t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}
