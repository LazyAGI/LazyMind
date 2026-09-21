package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		{"draft", map[string]any{"source_type": "artifact", "source_revision_id": "revision-1", "content_hash": ""}, `{"ok":true}`},
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

func TestRemoteHandlerRejectsChangedArtifactInput(t *testing.T) {
	value := AttemptContext{
		AttemptID: "attempt-hash", SessionID: "session-hash", StepID: "step-hash",
		Inputs: map[string]any{},
	}
	handler, db, claim := remoteHandlerFixture(t, value)
	now := time.Now().UTC()
	original := json.RawMessage(`{"text":"original"}`)
	sum := sha256.Sum256(original)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])
	humanID := "human-hash"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: value.SessionID, Slot: "draft", ContentType: "text",
		Value: original, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-hash", SessionID: value.SessionID, SlotID: "draft", Revision: 1,
		Selected: true, HumanArtifactID: &humanID, Slot: "draft", StepID: "source",
		Attempt: 1, Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	value.Inputs["draft"] = map[string]any{
		"source_type": "artifact", "source_revision_id": "revision-hash",
		"content_hash": wantHash,
	}
	handler.Contexts = staticContextLoader{value: value}
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/inputs/draft", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("X-Workflow-Lease-Token", claim.LeaseToken)
		req = mux.SetURLVars(req, map[string]string{
			"attempt_id": value.AttemptID, "material_id": "draft",
		})
		recorder := httptest.NewRecorder()
		handler.Input(recorder, req)
		return recorder
	}

	matching := request()
	if matching.Code != http.StatusOK {
		t.Fatalf("matching hash: status=%d body=%s", matching.Code, matching.Body.String())
	}
	var matchingEnvelope remoteEnvelope
	if err := json.Unmarshal(matching.Body.Bytes(), &matchingEnvelope); err != nil {
		t.Fatal(err)
	}
	matchingData := matchingEnvelope.Data.(map[string]any)
	matchingContent, err := base64.StdEncoding.DecodeString(matchingData["content_base64"].(string))
	if err != nil || !bytes.Equal(matchingContent, original) {
		t.Fatalf("matching content=%q err=%v, want raw original", matchingContent, err)
	}
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", humanID).
		Update("value", json.RawMessage(`{"text":"changed"}`)).Error; err != nil {
		t.Fatal(err)
	}
	recorder := request()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("changed input status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope remoteEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error["code"] != "ATTEMPT_INPUT_CHANGED" || envelope.Data != nil {
		t.Fatalf("changed input response=%#v", envelope)
	}
}

func TestRemoteHandlerExportsEditedMarkdownAsDocument(t *testing.T) {
	markdown := "# 测试大纲\n\n## Arrival\n\nA quiet station.\n"
	for _, test := range []struct {
		name, contentType, value string
		wantStatus               int
	}{
		{"text envelope", "text/markdown", `{"text":` + strconv.Quote(markdown) + `}`, http.StatusOK},
		{"root string", "text/markdown", strconv.Quote(markdown), http.StatusOK},
		{"MIME parameters", "Text/Markdown; charset=utf-8", `{"text":` + strconv.Quote(markdown) + `}`, http.StatusOK},
		{"schema envelope", "text/markdown", `{"schema":"text/markdown","data":{"text":` + strconv.Quote(markdown) + `}}`, http.StatusOK},
		{"invalid text", "text/markdown", `{"text":123}`, http.StatusUnprocessableEntity},
		{"schema conflict", "text/markdown", `{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"test","blocks":[]}}`, http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := AttemptContext{AttemptID: "attempt-edited", SessionID: "session-edited", StepID: "write",
				DeclaredInputTypes: map[string]string{"outline_document": "file"}}
			handler, db, claim := remoteHandlerFixture(t, value)
			now := time.Now().UTC()
			path := filepath.Join(t.TempDir(), "outline_document.md")
			if err := os.WriteFile(path, []byte(markdown), 0o600); err != nil {
				t.Fatal(err)
			}
			original, _ := json.Marshal(map[string]any{"path": path, "filename": "outline_document.md"})
			for i, artifact := range []orm.WorkflowHumanArtifact{
				{ID: "original", ContentType: "file", Value: original},
				{ID: "edited", ContentType: test.contentType, Value: json.RawMessage(test.value)},
			} {
				artifact.SessionID, artifact.Slot, artifact.CreatedAt = value.SessionID, "outline_document", now
				if err := db.Create(&artifact).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.WorkflowSlotRevision{ID: artifact.ID, SessionID: value.SessionID,
					SlotID: "outline_document", Slot: "outline_document", Revision: i + 1,
					Selected: i == 1, HumanArtifactID: &artifact.ID, CreatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			read := func(revision, hash string) *httptest.ResponseRecorder {
				value.Inputs = map[string]any{"outline_document": map[string]any{
					"source_type": "artifact", "source_revision_id": revision, "content_hash": hash}}
				handler.Contexts = staticContextLoader{value: value}
				req := httptest.NewRequest(http.MethodGet, "/inputs/outline_document", nil)
				req.Header.Set("Authorization", "Bearer test-token")
				req.Header.Set("X-Workflow-Lease-Token", claim.LeaseToken)
				req = mux.SetURLVars(req, map[string]string{"attempt_id": value.AttemptID, "material_id": "outline_document"})
				rec := httptest.NewRecorder()
				handler.Input(rec, req)
				return rec
			}
			check := func(rec *httptest.ResponseRecorder, status int) {
				t.Helper()
				if rec.Code != status {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				var envelope remoteEnvelope
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if status != http.StatusOK {
					if envelope.OK || envelope.Data != nil || envelope.Error["code"] != "ATTEMPT_INPUT_INVALID" {
						t.Fatalf("invalid document response=%#v", envelope)
					}
					return
				}
				data := envelope.Data.(map[string]any)
				decoded, err := base64.StdEncoding.DecodeString(data["content_base64"].(string))
				if err != nil || string(decoded) != markdown || data["name"] != "outline_document.md" ||
					int(data["size"].(float64)) != len([]byte(markdown)) {
					t.Fatalf("document input=%#v decoded=%q err=%v", data, decoded, err)
				}
				if data["resource_id"] == "edited" && data["mime_type"] != "text/markdown" {
					t.Fatalf("edited Markdown MIME=%v", data["mime_type"])
				}
			}
			// Reading the original bound revision must still work after an edit is selected.
			check(read("original", ""), http.StatusOK)
			sum := sha256.Sum256([]byte(test.value))
			check(read("edited", "sha256:"+hex.EncodeToString(sum[:])), test.wantStatus)
			if test.wantStatus == http.StatusOK {
				if err := db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "edited").
					Update("value", json.RawMessage(`{"text":"changed"}`)).Error; err != nil {
					t.Fatal(err)
				}
				if rec := read("edited", "sha256:"+hex.EncodeToString(sum[:])); rec.Code != http.StatusConflict ||
					!strings.Contains(rec.Body.String(), "ATTEMPT_INPUT_CHANGED") {
					t.Fatalf("changed input status=%d body=%s", rec.Code, rec.Body.String())
				}
			}
		})
	}
}

func TestRemoteHandlerPreservesNonMarkdownArtifactEnvelopes(t *testing.T) {
	for _, test := range []struct{ name, contentType, value string }{
		{"text", "text", `{"text":"approved brief"}`},
		{"json", "json", `{"data":{"text":"not a Markdown document"}}`},
		{"writer IR", "json", `{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"test","blocks":[]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := AttemptContext{AttemptID: "attempt-input", SessionID: "session-input", StepID: "write"}
			handler, db, _ := remoteHandlerFixture(t, value)
			humanID := "human-input"
			if err := db.Create(&orm.WorkflowHumanArtifact{ID: humanID, SessionID: value.SessionID,
				Slot: "input", ContentType: test.contentType, Value: json.RawMessage(test.value)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowSlotRevision{ID: "revision-input", SessionID: value.SessionID,
				Slot: "input", HumanArtifactID: &humanID, Revision: 1}).Error; err != nil {
				t.Fatal(err)
			}
			result, failure := handler.readAttemptInput(context.Background(), "input", map[string]any{
				"source_type": "artifact", "source_revision_id": "revision-input"})
			if failure != nil {
				t.Fatal(failure)
			}
			decoded, err := base64.StdEncoding.DecodeString(result["content_base64"].(string))
			if err != nil || string(decoded) != test.value || result["name"] != "input.json" || result["mime_type"] != "application/json" {
				t.Fatalf("input=%#v decoded=%q err=%v", result, decoded, err)
			}
		})
	}
}

func TestRemoteHandlerPreservesHistoricalRootTextAttemptBytes(t *testing.T) {
	value := AttemptContext{
		AttemptID: "attempt-root-text", SessionID: "session-root-text", StepID: "step-root-text",
		Inputs: map[string]any{},
	}
	handler, db, claim := remoteHandlerFixture(t, value)
	now := time.Now().UTC()
	original := json.RawMessage(`"# Historical\n\\# literal hash"`)
	sum := sha256.Sum256(original)
	humanID := "human-root-text"
	revisionID := "revision-root-text"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: value.SessionID, Slot: "draft", ContentType: "text",
		Value: original, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: revisionID, SessionID: value.SessionID, SlotID: "draft", Revision: 1,
		Selected: true, HumanArtifactID: &humanID, Slot: "draft", StepID: "source",
		Attempt: 1, Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	value.Inputs["draft"] = map[string]any{
		"source_type": "artifact", "source_revision_id": revisionID,
		"content_hash": "sha256:" + hex.EncodeToString(sum[:]),
	}
	handler.Contexts = staticContextLoader{value: value}
	req := httptest.NewRequest(http.MethodGet, "/inputs/draft", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Workflow-Lease-Token", claim.LeaseToken)
	req = mux.SetURLVars(req, map[string]string{
		"attempt_id": value.AttemptID, "material_id": "draft",
	})
	recorder := httptest.NewRecorder()
	handler.Input(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("root text input: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope remoteEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope.Data.(map[string]any)
	content, err := base64.StdEncoding.DecodeString(data["content_base64"].(string))
	if err != nil || !bytes.Equal(content, original) {
		t.Fatalf("root text content=%q err=%v, want raw bytes %q", content, err, original)
	}
}

func TestRemoteHandlerRejectsChangedListArtifactMetadata(t *testing.T) {
	value := AttemptContext{
		AttemptID: "attempt-list-hash", SessionID: "session-list-hash", StepID: "step-list-hash",
		Inputs: map[string]any{},
	}
	handler, db, claim := remoteHandlerFixture(t, value)
	now := time.Now().UTC()
	bindings := make([]map[string]any, 0, 2)
	humanIDs := make([]string, 0, 2)
	for index, content := range []string{"first", "second"} {
		path := filepath.Join(t.TempDir(), content+".md")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"filename": content + ".md", "path": path})
		if err != nil {
			t.Fatal(err)
		}
		humanID := "human-list-hash-" + content
		humanIDs = append(humanIDs, humanID)
		if err := db.Create(&orm.WorkflowHumanArtifact{
			ID: humanID, SessionID: value.SessionID, Slot: "files", ContentType: "file",
			Value: raw, CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		listIndex := index
		revisionID := "revision-list-hash-" + content
		if err := db.Create(&orm.WorkflowSlotRevision{
			ID: revisionID, SessionID: value.SessionID, SlotID: "files", Revision: index + 1,
			ListIndex: &listIndex, Selected: true, HumanArtifactID: &humanID,
			Slot: "files", StepID: "source", Attempt: 1, Validity: "effective", CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		bindings = append(bindings, map[string]any{
			"source_type": "artifact", "source_revision_id": revisionID,
			"content_hash": "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	value.Inputs["files"] = bindings
	handler.Contexts = staticContextLoader{value: value}
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/inputs/files", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("X-Workflow-Lease-Token", claim.LeaseToken)
		req = mux.SetURLVars(req, map[string]string{
			"attempt_id": value.AttemptID, "material_id": "files",
		})
		recorder := httptest.NewRecorder()
		handler.Input(recorder, req)
		return recorder
	}
	if recorder := request(); recorder.Code != http.StatusOK {
		t.Fatalf("matching list: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second orm.WorkflowHumanArtifact
	if err := db.First(&second, "id = ?", humanIDs[1]).Error; err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(second.Value, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata["filename"] = "changed.md"
	changed, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", second.ID).
		Update("value", changed).Error; err != nil {
		t.Fatal(err)
	}
	recorder := request()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("changed list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope remoteEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error["code"] != "ATTEMPT_INPUT_CHANGED" || envelope.Data != nil {
		t.Fatalf("changed list response=%#v", envelope)
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
