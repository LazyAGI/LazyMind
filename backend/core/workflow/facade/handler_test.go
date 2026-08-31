package facade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/subagent"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

func TestInputResourceImportAndBindingPinsStableRevision(t *testing.T) {
	h, db := testHandler(t)
	if err := db.Exec(`INSERT INTO plugin_sessions(id, create_user_id) VALUES ('s1','owner')`).Error; err != nil {
		t.Fatal(err)
	}
	content := []byte("stable requirements")
	sum := sha256.Sum256(content)
	body, _ := json.Marshal(map[string]any{
		"contract_version": ContractVersion, "name": "requirements.txt", "mime_type": "text/plain",
		"size": len(content), "content_hash": "sha256:" + hex.EncodeToString(sum[:]),
		"content_base64": base64.StdEncoding.EncodeToString(content),
	})
	w := httptest.NewRecorder()
	h.ImportInputResource(w, request(http.MethodPost, "/workflow-input-resources", "owner", body))
	if w.Code != http.StatusOK {
		t.Fatalf("import=%d %s", w.Code, w.Body.String())
	}
	var resource struct {
		ResourceID  string `json:"resource_id"`
		ContentHash string `json:"content_hash"`
		Revision    int64  `json:"revision"`
	}
	encoded, _ := json.Marshal(decodeEnvelope(t, w).Data)
	if err := json.Unmarshal(encoded, &resource); err != nil {
		t.Fatal(err)
	}
	bindBody, _ := json.Marshal(map[string]any{
		"material_id": "requirements", "resource_type": "file", "resource_id": resource.ResourceID,
		"resource_revision": resource.Revision, "content_hash": resource.ContentHash, "command_id": "cmd-bind",
	})
	bindRequest := mux.SetURLVars(request(http.MethodPost, "/workflow-sessions/s1/input-bindings", "owner", bindBody), map[string]string{"session_id": "s1"})
	bound := httptest.NewRecorder()
	h.BindInput(bound, bindRequest)
	if bound.Code != http.StatusOK {
		t.Fatalf("bind=%d %s", bound.Code, bound.Body.String())
	}
	var count int64
	if err := db.Table("workflow_input_bindings").Where("workflow_session_id = ? AND resource_id = ?", "s1", resource.ResourceID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("binding count=%d err=%v", count, err)
	}
	if bytes.Contains(encoded, []byte("content_base64")) || bytes.Contains(encoded, []byte("/tmp/")) {
		t.Fatalf("Host-private data leaked: %s", encoded)
	}
}

func TestGetProjectionAuthorizesBeforeCallingRuntimeProjection(t *testing.T) {
	h, db := testHandler(t)
	if err := db.Exec(`INSERT INTO plugin_sessions(id, create_user_id, conversation_id) VALUES ('s1','owner','conversation-1')`).Error; err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	h.Projection = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"active"}`))
	})
	call := func(owner, sessionID string) *httptest.ResponseRecorder {
		r := mux.SetURLVars(request(http.MethodGet, "/workflow-sessions/"+sessionID+"/projection", owner, nil),
			map[string]string{"session_id": sessionID})
		w := httptest.NewRecorder()
		h.GetProjection(w, r)
		return w
	}
	if got := call("other", "s1"); got.Code != http.StatusForbidden || decodeEnvelope(t, got).Error.Code != "PERMISSION_DENIED" {
		t.Fatalf("wrong owner=%d %s", got.Code, got.Body.String())
	}
	if got := call("owner", "missing"); got.Code != http.StatusNotFound || decodeEnvelope(t, got).Error.Code != "WORKFLOW_SESSION_NOT_FOUND" {
		t.Fatalf("missing=%d %s", got.Code, got.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("unauthorized requests reached projection: calls=%d", calls.Load())
	}
	if got := call("owner", "s1"); got.Code != http.StatusOK || got.Body.String() != `{"status":"active"}` {
		t.Fatalf("authorized=%d %s", got.Code, got.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("projection calls=%d", calls.Load())
	}
	scoped := mux.SetURLVars(request(http.MethodGet, "/workflow-sessions/s1/projection", "owner", nil),
		map[string]string{"session_id": "s1"})
	scoped = scoped.WithContext(workflowstore.WithConversationScope(scoped.Context(), "conversation-2"))
	denied := httptest.NewRecorder()
	h.GetProjection(denied, scoped)
	if denied.Code != http.StatusForbidden || decodeEnvelope(t, denied).Error.Code != "PERMISSION_DENIED" || calls.Load() != 1 {
		t.Fatalf("cross-conversation projection=%d %s calls=%d", denied.Code, denied.Body.String(), calls.Load())
	}
}

func TestListSessionsReturnsOnlyExternalAgentSessions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := workflowstore.New(db)
	if err := repo.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&[]orm.WorkflowSession{
		{ID: "external", ConversationID: "conversation-1", OriginHost: "external-agent", ControllerHost: "external-agent", WorkflowID: "writer", Status: "active", CreateUserID: "owner", CreatedAt: now, UpdatedAt: now},
		{ID: "other-conversation", ConversationID: "conversation-2", OriginHost: "external-agent", ControllerHost: "external-agent", WorkflowID: "image", Status: "active", CreateUserID: "owner", CreatedAt: now, UpdatedAt: now},
		{ID: "internal", OriginHost: "lazymind", ControllerHost: "lazymind", WorkflowID: "writer", Status: "active", CreateUserID: "owner", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := request(http.MethodGet, "/workflow-sessions?status=active&page_size=10", "owner", nil)
	r = r.WithContext(workflowstore.WithConversationScope(r.Context(), "conversation-1"))
	Handler{Store: repo}.ListSessions(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	encoded, _ := json.Marshal(decodeEnvelope(t, w).Data)
	if !bytes.Contains(encoded, []byte(`"session_id":"external"`)) ||
		bytes.Contains(encoded, []byte(`"session_id":"internal"`)) ||
		bytes.Contains(encoded, []byte(`"session_id":"other-conversation"`)) {
		t.Fatalf("session scope leaked: %s", encoded)
	}
}

func TestAdvanceStepWaitsForTerminalAttemptFromLegacyEnvelope(t *testing.T) {
	h, db := testHandler(t)
	if err := db.AutoMigrate(&orm.WorkflowSessionStep{}, &orm.WorkflowTransitionCommand{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO plugin_sessions(id, create_user_id) VALUES ('s1','owner')`).Error; err != nil {
		t.Fatal(err)
	}
	legacy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now().UTC()
		row := orm.WorkflowSessionStep{ID: "attempt-1", SessionID: "s1", StepID: "prompt",
			Attempt: 1, TaskID: "task-1", Status: "running", Validity: "effective",
			CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(80 * time.Millisecond)
			_ = db.Model(&orm.WorkflowSessionStep{}).Where("task_id = ?", "task-1").Updates(
				map[string]any{"status": "succeeded", "updated_at": time.Now().UTC()}).Error
		}()
		common.ReplyOK(w, map[string]any{"accepted": true, "session_id": "s1",
			"task_id": "task-1", "tasks": []map[string]any{{"step_id": "prompt", "task_id": "task-1"}}})
	})
	body := []byte(`{"contract_version":"workflow.v1","command_id":"wait-1","tool":"advance_step","session_id":"s1","expected_state_version":1,"steps":[{"step_id":"prompt"}]}`)
	r := mux.SetURLVars(request(http.MethodPost, "/workflow-sessions/s1:advance-step", "owner", body),
		map[string]string{"session_id": "s1"})
	w := httptest.NewRecorder()
	started := time.Now()
	h.Command(legacy)(w, r)
	if elapsed := time.Since(started); elapsed < 80*time.Millisecond {
		t.Fatalf("advance_step returned before terminal attempt: %s", elapsed)
	}
	wrapped := decodeEnvelope(t, w)
	encoded, _ := json.Marshal(wrapped.Data)
	if !bytes.Contains(encoded, []byte(`"attempt_status":"succeeded"`)) ||
		!bytes.Contains(encoded, []byte(`"step_id":"prompt"`)) {
		t.Fatalf("terminal execution result missing: %s", encoded)
	}
}

func testHandler(t *testing.T) (Handler, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := workflowstore.New(db)
	if err := repo.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE plugin_sessions (id TEXT PRIMARY KEY, create_user_id TEXT NOT NULL, conversation_id TEXT NOT NULL DEFAULT '')`).Error; err != nil {
		t.Fatal(err)
	}
	return Handler{Store: repo}, db
}

func request(method, path, owner string, body []byte) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("X-User-Id", owner)
	r.Header.Set("Workflow-Contract-Version", ContractVersion)
	return r
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) envelope {
	t.Helper()
	var got envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return got
}

func TestPrepareHTTPRejectsUnknownPublicWorkflow(t *testing.T) {
	h, _ := testHandler(t)
	body := []byte(`{"workflow_id":"writer","idempotency_key":"same","input_bindings":{"source":"r1"}}`)
	recorder := httptest.NewRecorder()
	h.Prepare(recorder, request(http.MethodPost, "/workflow-preparations", "owner", body))
	if recorder.Code != http.StatusNotFound || decodeEnvelope(t, recorder).Error.Code != "WORKFLOW_NOT_FOUND" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMissingExternalInputsSkipsOptionalExternalMaterials(t *testing.T) {
	graph := preparationGraph{MaterialProducers: map[string]struct {
		Kind     string `json:"kind"`
		Optional bool   `json:"optional"`
	}{
		"required_source":    {Kind: "external"},
		"uploaded_materials": {Kind: "external", Optional: true},
		"generated":          {Kind: "step"},
	}}
	missing := missingExternalInputs(graph, map[string]any{})
	if len(missing) != 1 || missing[0] != "required_source" {
		t.Fatalf("missing inputs = %#v", missing)
	}
}

func TestMissingExternalInputsUsesRequiredExpressions(t *testing.T) {
	graph := preparationGraph{
		MaterialProducers: map[string]struct {
			Kind     string `json:"kind"`
			Optional bool   `json:"optional"`
		}{
			"research_topic": {Kind: "external"},
			"word_target":    {Kind: "external"},
			"reference_file": {Kind: "external"},
		},
		InputExpressions: map[string]graphengine.Expression{
			"generate_outline": {All: []graphengine.Expression{
				{Material: "research_topic"}, {Material: "word_target"},
			}},
		},
	}
	missing := missingExternalInputs(graph, map[string]any{"research_topic": "bound"})
	if len(missing) != 1 || missing[0] != "word_target" {
		t.Fatalf("optional-only reference_file must not block preparation: %#v", missing)
	}
}

func TestPrepareHTTPReturnsFlatPublicContractOnCreateAndReplay(t *testing.T) {
	h, _ := testHandler(t)
	body := []byte(`{"workflow_id":"writer","idempotency_key":"same","input_bindings":{}}`)
	prepared, _, err := h.Store.Prepare(t.Context(), "owner", "same", "writer", ContractVersion,
		body, json.RawMessage(`{"status":"ready","workflow_ref":"builtin:writer","workflow_revision":"rev-1","missing_inputs":[],"warnings":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.Prepare(w, request(http.MethodPost, "/workflow-preparations", "owner", body))
		if w.Code != http.StatusOK {
			t.Fatalf("prepare replay=%d body=%s", w.Code, w.Body.String())
		}
		encoded, _ := json.Marshal(decodeEnvelope(t, w).Data)
		var got map[string]any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if got["preparation_id"] != prepared.ID || got["status"] != "ready" || got["workflow_revision"] != "rev-1" {
			t.Fatalf("public preparation contract is not flat: %s", encoded)
		}
		if _, leaked := got["response"]; leaked {
			t.Fatalf("persistence envelope leaked into public contract: %s", encoded)
		}
	}
}

func TestConsumeHTTPChecksOwnerAndConsumesExactlyOnce(t *testing.T) {
	h, _ := testHandler(t)
	p, _, err := h.Store.Prepare(t.Context(), "owner", "key", "writer", ContractVersion, json.RawMessage(`{}`), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	consume := func(owner, session string) *httptest.ResponseRecorder {
		r := request(http.MethodPost, "/workflow-preparations/"+p.ID+"/consume", owner, []byte(`{"session_id":"`+session+`"}`))
		r = mux.SetURLVars(r, map[string]string{"preparation_id": p.ID})
		w := httptest.NewRecorder()
		h.Consume(w, r)
		return w
	}
	denied := consume("other", "s1")
	if denied.Code != http.StatusForbidden || decodeEnvelope(t, denied).Error.Code != "PERMISSION_DENIED" {
		t.Fatalf("denied=%d %s", denied.Code, denied.Body.String())
	}
	if got := consume("owner", "s1"); got.Code != http.StatusNotFound {
		t.Fatalf("consume=%d %s", got.Code, got.Body.String())
	}
}

func TestConsumeHTTPRejectsOversizedSessionIDBeforePersistence(t *testing.T) {
	h, _ := testHandler(t)
	r := request(http.MethodPost, "/workflow-preparations/prep/consume", "owner",
		[]byte(`{"session_id":"1234567890123456789012345678901234567"}`))
	r = mux.SetURLVars(r, map[string]string{"preparation_id": "prep"})
	w := httptest.NewRecorder()
	h.Consume(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := decodeEnvelope(t, w).Error; got == nil || got.Code != "INVALID_REQUEST" {
		t.Fatalf("unexpected error: %#v", got)
	}
}

func TestConsumeRetriesSessionEventWithPreparedSessionAndIdempotentBindings(t *testing.T) {
	h, db := testHandler(t)
	if err := db.AutoMigrate(
		&orm.Conversation{},
		&orm.WorkflowSession{},
		&orm.WorkflowResource{},
		&orm.WorkflowRevision{},
		&orm.WorkflowRevisionEntry{},
		&orm.WorkflowBlob{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conversation-1", BaseModel: orm.BaseModel{
		CreateUserID: "owner", CreatedAt: now, UpdatedAt: now,
	}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowResource{
		ID: "workflow-resource-1", WorkflowRef: "builtin:writer", WorkflowID: "writer",
		OwnerUserID: "owner", OwnerScope: "owner", RelativeRoot: "writer", HeadRevisionID: "revision-1",
		Version: 1, Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{
		ID: "revision-1", WorkflowResourceID: "workflow-resource-1", RevisionNo: 1,
		TreeHash: "tree-1", GraphHash: "graph-1", GraphSchemaVersion: "3",
		CompiledGraph: json.RawMessage(`{"nodes":{}}`), CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	inputContent := []byte("source material")
	inputHashBytes := sha256.Sum256(inputContent)
	inputHash := "sha256:" + hex.EncodeToString(inputHashBytes[:])
	inputResource, _, err := h.Store.ImportInputResource(
		t.Context(), "owner", "source.txt", "text/plain", inputHash, inputContent,
	)
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, err := json.Marshal(map[string]any{
		"workflow_id":     "writer",
		"conversation_id": "conversation-1",
		"origin_host":     "lazymind",
		"origin_ref":      "conversation-1",
		"controller_host": "lazymind",
		"request_context": "draft carefully",
		"input_bindings": map[string]any{
			"source": map[string]any{
				"resource_id":  inputResource.ID,
				"content_hash": inputResource.ContentHash,
				"revision":     inputResource.Revision,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, _, err := h.Store.Prepare(
		t.Context(), "owner", "prepare-writer", "writer", ContractVersion, requestJSON,
		json.RawMessage(`{"status":"ready","workflow_revision":"revision-1"}`),
	)
	if err != nil {
		t.Fatal(err)
	}

	type publishedEvent struct {
		conversationID string
		eventType      string
		payload        map[string]any
	}
	var events []publishedEvent
	deliveryAttempts := 0
	subagent.EventHooks.RegisterConversationEventHook(func(
		_ context.Context, _ state.Store, conversationID, _ string, eventType string, payload map[string]any,
	) error {
		var session orm.WorkflowSession
		if err := db.First(&session, "id = ?", "session-1").Error; err != nil {
			t.Fatal(err)
		}
		if session.IntentContext != `{"text":"draft carefully"}` {
			t.Fatalf("event published before intent initialization: %q", session.IntentContext)
		}
		var bindingCount int64
		if err := db.Table("workflow_input_bindings").
			Where("workflow_session_id = ? AND resource_id = ?", "session-1", inputResource.ID).
			Count(&bindingCount).Error; err != nil {
			t.Fatal(err)
		}
		if bindingCount != 1 {
			t.Fatalf("event published before input binding initialization: count=%d", bindingCount)
		}
		events = append(events, publishedEvent{conversationID: conversationID, eventType: eventType, payload: payload})
		deliveryAttempts++
		if deliveryAttempts == 1 {
			return errors.New("conversation event store unavailable")
		}
		return nil
	})
	t.Cleanup(func() { subagent.EventHooks.RegisterConversationEventHook(nil) })

	consume := func(sessionID string) *httptest.ResponseRecorder {
		r := request(http.MethodPost, "/workflow-preparations/"+prepared.ID+"/consume", "owner",
			[]byte(`{"session_id":"`+sessionID+`"}`))
		r = mux.SetURLVars(r, map[string]string{"preparation_id": prepared.ID})
		w := httptest.NewRecorder()
		h.Consume(w, r)
		return w
	}
	first := consume("session-1")
	if first.Code != http.StatusServiceUnavailable || decodeEnvelope(t, first).Error.Code != "WORKFLOW_SESSION_EVENT_FAILED" {
		t.Fatalf("first consume: status=%d body=%s", first.Code, first.Body.String())
	}
	second := consume("different-session-id")
	if second.Code != http.StatusOK {
		t.Fatalf("retry consume: status=%d body=%s", second.Code, second.Body.String())
	}
	encodedResponse, _ := json.Marshal(decodeEnvelope(t, second).Data)
	if !bytes.Contains(encodedResponse, []byte(`"session_id":"session-1"`)) {
		t.Fatalf("retry did not reuse prepared session: %s", encodedResponse)
	}
	if len(events) != 2 {
		t.Fatalf("workflow_session_created attempts=%d, want 2: %#v", len(events), events)
	}
	wantPayload := map[string]any{
		"conversation_id": "conversation-1",
		"session_id":      "session-1",
		"workflow_id":     "writer",
		"status":          "active",
		"state_version":   int64(1),
	}
	for _, event := range events {
		if event.conversationID != "conversation-1" || event.eventType != "workflow_session_created" {
			t.Fatalf("unexpected event routing: %#v", event)
		}
		if !reflect.DeepEqual(event.payload, wantPayload) {
			t.Fatalf("payload=%#v, want %#v", event.payload, wantPayload)
		}
	}
	var sessionCount, bindingCount int64
	if err := db.Model(&orm.WorkflowSession{}).Count(&sessionCount).Error; err != nil || sessionCount != 1 {
		t.Fatalf("session count=%d err=%v", sessionCount, err)
	}
	if err := db.Table("workflow_input_bindings").
		Where("workflow_session_id = ?", "session-1").Count(&bindingCount).Error; err != nil || bindingCount != 1 {
		t.Fatalf("binding count after retry=%d err=%v", bindingCount, err)
	}
}

func TestCommandHTTPChecksVersionPermissionAndExecutesLegacyOnce(t *testing.T) {
	h, db := testHandler(t)
	if err := db.Exec(`INSERT INTO plugin_sessions(id, create_user_id) VALUES ('s1','owner')`).Error; err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	legacy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["operation"] != "advance" || payload["hand_off"] != false || len(payload["targets"].([]any)) != 1 {
			t.Errorf("legacy adapter payload=%#v", payload)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true,"state_version":2}`))
	})
	command := h.Command(legacy)
	body := []byte(`{"contract_version":"workflow.v1","command_id":"cmd-1","tool":"advance_step","session_id":"s1","expected_state_version":1,"steps":[{"step_id":"draft"}]}`)
	call := func(owner string, payload []byte) *httptest.ResponseRecorder {
		r := request(http.MethodPost, "/workflow-sessions/s1/commands", owner, payload)
		r = mux.SetURLVars(r, map[string]string{"session_id": "s1"})
		w := httptest.NewRecorder()
		command(w, r)
		return w
	}
	denied := call("other", body)
	if denied.Code != http.StatusForbidden || decodeEnvelope(t, denied).Error.Code != "PERMISSION_DENIED" {
		t.Fatalf("permission=%d %s", denied.Code, denied.Body.String())
	}
	for i := 0; i < 2; i++ {
		got := call("owner", body)
		if got.Code != http.StatusAccepted {
			t.Fatalf("legacy parity=%d %q", got.Code, got.Body.String())
		}
		wrapped := decodeEnvelope(t, got)
		encoded, _ := json.Marshal(wrapped.Data)
		if !bytes.Contains(encoded, []byte(`"state_version":2`)) || !wrapped.OK || wrapped.ContractVersion != ContractVersion {
			t.Fatalf("contract/legacy parity lost: %s", got.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("idempotent command executed %d times", calls.Load())
	}
	conflict := call("owner", bytes.Replace(body, []byte(`"draft"`), []byte(`"review"`), 1))
	if conflict.Code != http.StatusConflict || decodeEnvelope(t, conflict).Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
	badVersion := request(http.MethodPost, "/workflow-sessions/s1/commands", "owner", body)
	badVersion = mux.SetURLVars(badVersion, map[string]string{"session_id": "s1"})
	badVersion.Header.Set("Workflow-Contract-Version", "workflow.v2")
	w := httptest.NewRecorder()
	command(w, badVersion)
	if w.Code != http.StatusUnprocessableEntity || decodeEnvelope(t, w).Error.Code != "CONTRACT_VERSION_UNSUPPORTED" {
		t.Fatalf("version=%d %s", w.Code, w.Body.String())
	}
}

func TestCommandHTTPReportsMissingSessionInsteadOfPermissionDenied(t *testing.T) {
	h, _ := testHandler(t)
	command := h.Command(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("missing session must not reach transition handler")
	}))
	body := []byte(`{"contract_version":"workflow.v1","command_id":"cmd-1","tool":"advance_step","session_id":"missing","expected_state_version":1,"steps":[{"step_id":"prompt"}]}`)
	r := mux.SetURLVars(request(http.MethodPost, "/workflow-sessions/missing:advance-step", "owner", body), map[string]string{"session_id": "missing"})
	w := httptest.NewRecorder()
	command(w, r)
	if w.Code != http.StatusNotFound || decodeEnvelope(t, w).Error.Code != "WORKFLOW_SESSION_NOT_FOUND" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSessionFacadeEndpointsReportMissingSessionConsistently(t *testing.T) {
	h, _ := testHandler(t)
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		handler http.HandlerFunc
	}{
		{"inputs", http.MethodGet, "/workflow-sessions/missing/input-bindings", "", h.ListInputs},
		{"artifacts", http.MethodGet, "/workflow-sessions/missing/artifacts", "", h.ListArtifacts},
		{"stop", http.MethodPost, "/workflow-sessions/missing:stop", `{"command_id":"stop-1"}`, h.StopWorkflow},
		{"bind", http.MethodPost, "/workflow-sessions/missing/input-bindings", `{"material_id":"source","resource_id":"r1","command_id":"bind-1"}`, h.BindInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := mux.SetURLVars(request(tc.method, tc.path, "owner", []byte(tc.body)), map[string]string{"session_id": "missing"})
			w := httptest.NewRecorder()
			tc.handler(w, r)
			if w.Code != http.StatusNotFound || decodeEnvelope(t, w).Error.Code != "WORKFLOW_SESSION_NOT_FOUND" {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
