package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	tests := []struct {
		material string
		binding  map[string]any
		want     string
	}{{"brief", map[string]any{"source_type": "input_resource", "source_id": resource.ID}, "brief"},
		{"draft", map[string]any{"source_type": "artifact", "source_revision_id": "revision-1"}, `{"ok":true}`}}
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

func TestRemoteHandlerAcceptsDeclaredOptionalArtifactAndRejectsUnknown(t *testing.T) {
	value := AttemptContext{AttemptID: "attempt-1", SessionID: "session-1", StepID: "step-1",
		DeclaredOutputs: []string{"optional"}, RequiredOutputs: nil}
	handler, _, claim := remoteHandlerFixture(t, value)
	sink := &recordingArtifactSink{}
	handler.Artifacts = sink
	for _, test := range []struct {
		slot   string
		status int
	}{{"optional", http.StatusOK}, {"unknown", http.StatusUnprocessableEntity}} {
		rec := remoteHandlerRequest(handler.SaveArtifact, http.MethodPost, "/artifacts", value.AttemptID,
			claim.LeaseToken, map[string]any{"slot": test.slot, "content_type": "text", "value": map[string]any{"v": 1}})
		if rec.Code != test.status {
			t.Fatalf("slot=%s status=%d body=%s", test.slot, rec.Code, rec.Body.String())
		}
	}
	if len(sink.values) != 1 || sink.values[0].Slot != "optional" || sink.values[0].Seq != 1 {
		t.Fatalf("saved=%#v", sink.values)
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
