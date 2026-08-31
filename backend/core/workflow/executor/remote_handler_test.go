package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/attempt"
)

type staticContextLoader struct{ value AttemptContext }

func (loader staticContextLoader) LoadAttemptContext(context.Context, string) (AttemptContext, error) {
	return loader.value, nil
}

type recordingArtifactSink struct{ values []Artifact }

func (sink *recordingArtifactSink) Save(_ context.Context, _ AttemptContext, value Artifact) error {
	sink.values = append(sink.values, value)
	return nil
}

func remoteHandlerFixture(t *testing.T, value AttemptContext) (RemoteHandler, *gorm.DB, attempt.Claim) {
	t.Helper()
	t.Setenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN", "test-token")
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{}, &orm.WorkflowEvent{},
		&orm.WorkflowInputResource{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	service := attempt.New(db, attempt.Config{LeaseDuration: time.Minute})
	if _, err := service.Queue(context.Background(), attempt.QueueRequest{AttemptID: value.AttemptID,
		SessionID: value.SessionID, StepID: value.StepID, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(context.Background(), "executor-test")
	if err != nil {
		t.Fatal(err)
	}
	return RemoteHandler{DB: db, Attempts: service, Contexts: staticContextLoader{value: value}}, db, claim
}

func remoteHandlerRequest(handler http.HandlerFunc, method, path, attemptID, lease string, body any) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer test-token")
	req = mux.SetURLVars(req, map[string]string{"attempt_id": attemptID})
	if lease != "" {
		req.Header.Set("X-Workflow-Lease-Token", lease)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestRemoteHandlerFencesContextReads(t *testing.T) {
	value := AttemptContext{AttemptID: "attempt-1", SessionID: "session-1", StepID: "step-1"}
	handler, _, claim := remoteHandlerFixture(t, value)
	for _, test := range []struct {
		name, lease string
		status      int
	}{{"missing", "", http.StatusUnauthorized}, {"stale", "stale", http.StatusConflict},
		{"owner", claim.LeaseToken, http.StatusOK}} {
		t.Run(test.name, func(t *testing.T) {
			rec := remoteHandlerRequest(handler.Context, http.MethodGet, "/context", value.AttemptID, test.lease, nil)
			if rec.Code != test.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRemoteHandlerReadsResourceAndArtifactInputs(t *testing.T) {
	value := AttemptContext{AttemptID: "attempt-1", SessionID: "session-1", StepID: "step-1", Inputs: map[string]any{}}
	handler, db, claim := remoteHandlerFixture(t, value)
	now := time.Now().UTC()
	resource := orm.WorkflowInputResource{ID: "resource-1", OwnerUserID: "user", Name: "brief.txt",
		MimeType: "text/plain", Size: 5, ContentHash: "hash", Revision: 2, Content: []byte("brief"), CreatedAt: now}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	humanID := "human-1"
	if err := db.Create(&orm.WorkflowHumanArtifact{ID: humanID, SessionID: value.SessionID, Slot: "draft",
		ContentType: "json", Value: json.RawMessage(`{"ok":true}`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{ID: "revision-1", SessionID: value.SessionID, SlotID: "draft",
		Revision: 3, Selected: true, HumanArtifactID: &humanID, Slot: "draft", StepID: "source", Attempt: 1,
		Validity: "effective", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(draftPath, []byte("# Durable draft"), 0o600); err != nil {
		t.Fatal(err)
	}
	draftID := "file-1"
	draftValue, _ := json.Marshal(map[string]any{"filename": "draft.md", "path": draftPath})
	if err := db.Create(&orm.WorkflowHumanArtifact{ID: draftID, SessionID: value.SessionID, Slot: "draft_document",
		ContentType: "file", Value: draftValue, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{ID: "revision-2", SessionID: value.SessionID, SlotID: "draft_document",
		Revision: 1, Selected: true, HumanArtifactID: &draftID, Slot: "draft_document", StepID: "write", Attempt: 1,
		Validity: "effective", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	imageID := "image-1"
	imageValue, _ := json.Marshal(map[string]any{
		"path": "https://images.example.test/subject.png", "caption": "web reference",
	})
	if err := db.Create(&orm.WorkflowHumanArtifact{ID: imageID, SessionID: value.SessionID, Slot: "material_images",
		ContentType: "image", Value: imageValue, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{ID: "revision-3", SessionID: value.SessionID, SlotID: "material_images",
		Revision: 1, Selected: true, HumanArtifactID: &imageID, Slot: "material_images", StepID: "collect", Attempt: 1,
		Validity: "effective", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		material string
		binding  map[string]any
		want     string
	}{{"brief", map[string]any{"source_type": "input_resource", "source_id": resource.ID}, "brief"},
		{"draft", map[string]any{"source_type": "artifact", "source_revision_id": "revision-1"}, `{"ok":true}`},
		{"draft_document", map[string]any{"source_type": "artifact", "source_revision_id": "revision-2"}, "# Durable draft"},
		{"material_images", map[string]any{"source_type": "artifact", "source_revision_id": "revision-3"}, "https://images.example.test/subject.png"}}
	for _, test := range tests {
		t.Run(test.material, func(t *testing.T) {
			ctx := value
			ctx.Inputs = map[string]any{test.material: test.binding}
			handler.Contexts = staticContextLoader{value: ctx}
			req := httptest.NewRequest(http.MethodGet, "/inputs/"+test.material, nil)
			req.Header.Set("Authorization", "Bearer test-token")
			req = mux.SetURLVars(req, map[string]string{"attempt_id": value.AttemptID, "material_id": test.material})
			req.Header.Set("X-Workflow-Lease-Token", claim.LeaseToken)
			rec := httptest.NewRecorder()
			handler.Input(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var envelope remoteEnvelope
			_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
			data := envelope.Data.(map[string]any)
			decoded, _ := base64.StdEncoding.DecodeString(data["content_base64"].(string))
			if !bytes.Contains(decoded, []byte(test.want)) {
				t.Fatalf("content=%q", decoded)
			}
		})
	}
}

func TestRemoteHandlerReadsEveryListArtifactInput(t *testing.T) {
	value := AttemptContext{AttemptID: "attempt-list", SessionID: "session-list", StepID: "step-list", Inputs: map[string]any{}}
	handler, db, claim := remoteHandlerFixture(t, value)
	now := time.Now().UTC()
	bindings := make([]map[string]any, 0, 2)
	for index, content := range []string{"first", "second"} {
		path := filepath.Join(t.TempDir(), content+".png")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		humanID := "human-list-" + content
		raw, _ := json.Marshal(map[string]any{"filename": content + ".png", "path": path})
		if err := db.Create(&orm.WorkflowHumanArtifact{ID: humanID, SessionID: value.SessionID,
			Slot: "images", ContentType: "image", Value: raw, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		revisionID := "revision-list-" + content
		listIndex := index
		if err := db.Create(&orm.WorkflowSlotRevision{ID: revisionID, SessionID: value.SessionID,
			SlotID: "images", Revision: index + 1, ListIndex: &listIndex, Selected: true,
			HumanArtifactID: &humanID, Slot: "images", StepID: "source", Attempt: 1,
			Validity: "effective", CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, map[string]any{"source_type": "artifact", "source_revision_id": revisionID})
	}
	value.Inputs["images"] = bindings
	handler.Contexts = staticContextLoader{value: value}
	req := httptest.NewRequest(http.MethodGet, "/inputs/images", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Workflow-Lease-Token", claim.LeaseToken)
	req = mux.SetURLVars(req, map[string]string{"attempt_id": value.AttemptID, "material_id": "images"})
	rec := httptest.NewRecorder()
	handler.Input(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope remoteEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
	items := envelope.Data.(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%v", items)
	}
	for index, want := range []string{"first", "second"} {
		item := items[index].(map[string]any)
		decoded, _ := base64.StdEncoding.DecodeString(item["content_base64"].(string))
		if string(decoded) != want {
			t.Fatalf("item %d content=%q", index, decoded)
		}
	}

	value.Inputs["images"] = bindings[:1]
	handler.Contexts = staticContextLoader{value: value}
	rec = httptest.NewRecorder()
	handler.Input(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("single-item list status=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
	items = envelope.Data.(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("single-item list lost cardinality: %v", envelope.Data)
	}
}

func TestRemoteHandlerAcceptsDeclaredOptionalArtifactAndRejectsUnknown(t *testing.T) {
	value := AttemptContext{AttemptID: "attempt-1", SessionID: "session-1", StepID: "step-1",
		DeclaredOutputs: []string{"optional"}, DeclaredOutputTypes: map[string]string{"optional": "file"}, RequiredOutputs: nil}
	handler, _, claim := remoteHandlerFixture(t, value)
	sink := &recordingArtifactSink{}
	handler.Artifacts = sink
	for _, test := range []struct {
		slot   string
		status int
	}{{"optional", http.StatusUnprocessableEntity}, {"unknown", http.StatusUnprocessableEntity}} {
		rec := remoteHandlerRequest(handler.SaveArtifact, http.MethodPost, "/artifacts", value.AttemptID,
			claim.LeaseToken, map[string]any{"slot": test.slot, "content_type": "text", "value": map[string]any{"v": 1}})
		if rec.Code != test.status {
			t.Fatalf("slot=%s status=%d body=%s", test.slot, rec.Code, rec.Body.String())
		}
	}
	accepted := remoteHandlerRequest(handler.SaveArtifact, http.MethodPost, "/artifacts", value.AttemptID,
		claim.LeaseToken, map[string]any{"slot": "optional", "content_type": "file", "value": map[string]any{"path": "/data/subagent/user/task/outline.md"}})
	if accepted.Code != http.StatusOK || len(sink.values) != 1 || sink.values[0].Slot != "optional" || sink.values[0].Seq != 1 {
		t.Fatalf("saved=%#v", sink.values)
	}
}

func TestRemoteHandlerRejectsTextSavedIntoImageSlot(t *testing.T) {
	value := AttemptContext{AttemptID: "attempt-image", SessionID: "session-image", StepID: "enhance",
		DeclaredOutputs:     []string{"enhanced_image_output"},
		DeclaredOutputTypes: map[string]string{"enhanced_image_output": "image"}}
	handler, _, claim := remoteHandlerFixture(t, value)
	sink := &recordingArtifactSink{}
	handler.Artifacts = sink

	rejected := remoteHandlerRequest(handler.SaveArtifact, http.MethodPost, "/artifacts", value.AttemptID,
		claim.LeaseToken, map[string]any{"slot": "enhanced_image_output", "content_type": "text",
			"value": map[string]any{"text": "BLOCKED: source image missing"}})

	if rejected.Code != http.StatusUnprocessableEntity || len(sink.values) != 0 {
		t.Fatalf("status=%d body=%s saved=%#v", rejected.Code, rejected.Body.String(), sink.values)
	}
}

func TestRemoteHandlerPersistsArtifactFileUnderCoreUploadRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	value := AttemptContext{AttemptID: "attempt-1", SessionID: "session-1", StepID: "step-1"}
	handler, _, claim := remoteHandlerFixture(t, value)
	rec := remoteHandlerRequest(handler.UploadArtifactFile, http.MethodPost, "/artifact-files",
		value.AttemptID, claim.LeaseToken, map[string]any{
			"filename": "../result.png", "content_base64": base64.StdEncoding.EncodeToString([]byte("png")),
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(root, "workflow-artifacts", "session-1", "attempt-1")
	if filepath.Dir(envelope.Data.Path) != wantDir {
		t.Fatalf("path=%q want directory %q", envelope.Data.Path, wantDir)
	}
	content, err := os.ReadFile(envelope.Data.Path)
	if err != nil || string(content) != "png" {
		t.Fatalf("persisted content=%q err=%v", content, err)
	}
}

func TestRemoteHandlerCompletionRequiresDurableOutputs(t *testing.T) {
	value := AttemptContext{AttemptID: "attempt-1", SessionID: "session-1", StepID: "step-1",
		DeclaredOutputs: []string{"report"}, RequiredOutputs: []string{"report"}}
	handler, db, claim := remoteHandlerFixture(t, value)
	body := map[string]any{"lease_token": claim.LeaseToken, "result": map[string]any{"summary": "done"}}
	rejected := remoteHandlerRequest(handler.Complete, http.MethodPost, "/complete", value.AttemptID, claim.LeaseToken, body)
	if rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSlotRevision{ID: "output-1", SessionID: value.SessionID, SlotID: "report",
		Revision: 1, Selected: true, Slot: "report", StepID: value.StepID, Attempt: 1, Validity: "effective",
		ProducerAttemptID: value.AttemptID, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	accepted := remoteHandlerRequest(handler.Complete, http.MethodPost, "/complete", value.AttemptID, claim.LeaseToken, body)
	if accepted.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	row, err := handler.Attempts.Attempt(context.Background(), value.AttemptID)
	if err != nil || row.Status != "succeeded" {
		t.Fatalf("attempt=%#v err=%v", row, err)
	}
}
