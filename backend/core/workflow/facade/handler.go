package facade

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/workflow/artifactfile"
	workflowexecutor "lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

const ContractVersion = "workflow.v1"

type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type envelope struct {
	ContractVersion string `json:"contract_version"`
	RequestID       string `json:"request_id"`
	OK              bool   `json:"ok"`
	Data            any    `json:"result,omitempty"`
	Error           *Error `json:"error,omitempty"`
}

type Handler struct {
	Store      *workflowstore.Repository
	Hosts      *workflowexecutor.HostRegistry
	Projection http.Handler
}

func (h Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	pageSize := 0
	if value := strings.TrimSpace(r.URL.Query().Get("page_size")); value != "" {
		var err error
		pageSize, err = strconv.Atoi(value)
		if err != nil {
			fail(w, http.StatusUnprocessableEntity, "INVALID_SESSION_QUERY", "page_size must be an integer", false)
			return
		}
	}
	page, err := h.Store.ListExternalSessions(r.Context(), owner, workflowstore.SessionListQuery{
		Status: r.URL.Query().Get("status"), PageSize: pageSize, PageToken: r.URL.Query().Get("page_token"),
	})
	if errors.Is(err, workflowstore.ErrInvalidSessionQuery) {
		fail(w, http.StatusUnprocessableEntity, "INVALID_SESSION_QUERY", "status, page_size or page_token is invalid", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "WORKFLOW_SESSION_LIST_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: page})
}

func (h Handler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	items, err := h.Store.ListWorkflowPackages(r.Context(), owner)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "WORKFLOW_CATALOG_UNAVAILABLE", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{"workflows": items}})
}

func (h Handler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	value, err := h.Store.GetWorkflowPackage(
		r.Context(), owner, mux.Vars(r)["workflow_id"], r.URL.Query().Get("revision_id"),
	)
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "WORKFLOW_NOT_FOUND", "workflow or revision was not found", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "WORKFLOW_READ_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: value})
}

// GetProjection adds owner and contract checks around the existing pure
// projection handler. Internal Runtime callers keep using the raw handler.
func (h Handler) GetProjection(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	if err := h.Store.AuthorizeSession(r.Context(), mux.Vars(r)["session_id"], owner); err != nil {
		if errors.Is(err, workflowstore.ErrNotFound) {
			fail(w, http.StatusNotFound, "WORKFLOW_SESSION_NOT_FOUND", "workflow session was not found", false)
		} else if errors.Is(err, workflowstore.ErrPermissionDenied) {
			fail(w, http.StatusForbidden, "PERMISSION_DENIED", "workflow session belongs to another owner", false)
		} else {
			fail(w, http.StatusServiceUnavailable, "WORKFLOW_PROJECTION_UNAVAILABLE", err.Error(), true)
		}
		return
	}
	if h.Projection == nil {
		fail(w, http.StatusServiceUnavailable, "WORKFLOW_PROJECTION_UNAVAILABLE", "Workflow projection handler is unavailable", true)
		return
	}
	h.Projection.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if wrapped, ok := value.(envelope); ok {
		wrapped.ContractVersion = ContractVersion
		wrapped.RequestID = "server-generated"
		wrapped.OK = wrapped.Error == nil
		value = wrapped
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeJSON(w, status, envelope{Error: &Error{Code: code, Message: message, Retryable: retryable}})
}

func writePreparation(w http.ResponseWriter, prepared workflowstore.Preparation) {
	var public map[string]any
	if err := json.Unmarshal(prepared.ResponseJSON, &public); err != nil {
		fail(w, http.StatusServiceUnavailable, "PREPARATION_READ_FAILED", "stored preparation plan is invalid", true)
		return
	}
	public["preparation_id"] = prepared.ID
	public["idempotency_key"] = prepared.IdempotencyKey
	public["created_at"] = prepared.CreatedAt
	public["updated_at"] = prepared.UpdatedAt
	writeJSON(w, http.StatusOK, envelope{Data: public})
}

func identityAndVersion(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner := strings.TrimSpace(r.Header.Get("X-User-Id"))
	if owner == "" {
		fail(w, http.StatusBadRequest, "IDENTITY_REQUIRED", "X-User-Id is required", false)
		return "", false
	}
	version := strings.TrimSpace(r.Header.Get("Workflow-Contract-Version"))
	if version == "" {
		version = ContractVersion
	}
	if version != ContractVersion {
		fail(w, http.StatusUnprocessableEntity, "CONTRACT_VERSION_UNSUPPORTED", "supported version is workflow.v1", false)
		return "", false
	}
	return owner, true
}

type prepareRequest struct {
	PreparationID  string         `json:"preparation_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	WorkflowID     string         `json:"workflow_id"`
	InputBindings  map[string]any `json:"input_bindings"`
	OriginHost     string         `json:"origin_host"`
	OriginRef      string         `json:"origin_ref"`
	ConversationID string         `json:"conversation_id"`
	ControllerHost string         `json:"controller_host"`
	RequestContext string         `json:"request_context"`
}

type preparationGraph struct {
	Nodes map[string]struct {
		Capabilities []string `json:"capabilities"`
		LegacyTools  []string `json:"legacy_tools"`
	} `json:"nodes"`
	MaterialProducers map[string]struct {
		Kind     string `json:"kind"`
		Optional bool   `json:"optional"`
	} `json:"material_producers"`
	InputExpressions map[string]graphengine.Expression `json:"input_expressions"`
}

func collectRequiredInputMaterials(expression graphengine.Expression, materials map[string]struct{}) {
	if expression.Material != "" {
		materials[expression.Material] = struct{}{}
	}
	for _, nested := range expression.All {
		collectRequiredInputMaterials(nested, materials)
	}
	for _, nested := range expression.Any {
		// Keep the existing conservative preparation contract for alternatives:
		// every candidate branch input must be available before the Session starts.
		collectRequiredInputMaterials(nested, materials)
	}
}

func missingExternalInputs(graph preparationGraph, inputBindings map[string]any) []string {
	required := map[string]struct{}{}
	if graph.InputExpressions == nil {
		// Backward compatibility for compiled graphs without expression metadata.
		for materialID, producer := range graph.MaterialProducers {
			if producer.Kind == "external" && !producer.Optional {
				required[materialID] = struct{}{}
			}
		}
	} else {
		for _, expression := range graph.InputExpressions {
			collectRequiredInputMaterials(expression, required)
		}
	}
	missing := make([]string, 0)
	for materialID := range required {
		producer, exists := graph.MaterialProducers[materialID]
		if !exists || producer.Kind != "external" || producer.Optional {
			continue
		}
		if _, exists := inputBindings[materialID]; !exists {
			missing = append(missing, materialID)
		}
	}
	sort.Strings(missing)
	return missing
}

type toolCommandRequest struct {
	ContractVersion      string `json:"contract_version"`
	CommandID            string `json:"command_id"`
	Tool                 string `json:"tool"`
	SessionID            string `json:"session_id"`
	ExpectedStateVersion *int64 `json:"expected_state_version"`
	RetryOrigin          string `json:"retry_origin"`
	Steps                []struct {
		StepID string `json:"step_id"`
	} `json:"steps"`
}

type importInputResourceRequest struct {
	ContractVersion string `json:"contract_version"`
	Name            string `json:"name"`
	MimeType        string `json:"mime_type"`
	Size            int64  `json:"size"`
	ContentHash     string `json:"content_hash"`
	ContentBase64   string `json:"content_base64"`
}

func (h Handler) ImportInputResource(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	var req importInputResourceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
		fail(w, 422, "INVALID_REQUEST", "invalid input resource", false)
		return
	}
	content, err := base64.StdEncoding.DecodeString(req.ContentBase64)
	if err != nil || req.Name == "" || req.MimeType == "" || int64(len(content)) != req.Size {
		fail(w, 422, "INVALID_INPUT_RESOURCE", "name, mime_type, size and content are required", false)
		return
	}
	sum := sha256.Sum256(content)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	if hash != req.ContentHash {
		fail(w, 422, "INPUT_HASH_MISMATCH", "content_hash does not match content", false)
		return
	}
	resource, _, err := h.Store.ImportInputResource(r.Context(), owner, req.Name, req.MimeType, hash, content)
	if err != nil {
		fail(w, 503, "INPUT_IMPORT_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, 200, envelope{Data: map[string]any{
		"resource_id": resource.ID, "name": resource.Name, "mime_type": resource.MimeType,
		"size": resource.Size, "content_hash": resource.ContentHash, "revision": resource.Revision,
	}})
}

func (h Handler) ReadInputResource(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	resource, err := h.Store.GetInputResource(r.Context(), owner, mux.Vars(r)["resource_id"])
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "INPUT_RESOURCE_NOT_FOUND", "input resource was not found", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "INPUT_RESOURCE_READ_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{
		"resource_id": resource.ID, "name": resource.Name, "mime_type": resource.MimeType,
		"size": resource.Size, "content_hash": resource.ContentHash, "revision": resource.Revision,
		"content_base64": base64.StdEncoding.EncodeToString(resource.Content),
	}})
}

func (h Handler) ListInputs(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	values, err := h.Store.ListInputBindings(r.Context(), owner, mux.Vars(r)["session_id"])
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "WORKFLOW_SESSION_NOT_FOUND", "workflow session was not found", false)
		return
	}
	if errors.Is(err, workflowstore.ErrPermissionDenied) {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "workflow session belongs to another owner", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "INPUT_BINDINGS_READ_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{"inputs": values}})
}

func (h Handler) ListArtifacts(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	values, err := h.Store.ListArtifacts(r.Context(), owner, mux.Vars(r)["session_id"])
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "WORKFLOW_SESSION_NOT_FOUND", "workflow session was not found", false)
		return
	}
	if errors.Is(err, workflowstore.ErrPermissionDenied) {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "workflow session belongs to another owner", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "ARTIFACT_LIST_FAILED", err.Error(), true)
		return
	}
	for index := range values {
		values[index].Value = artifactfile.Metadata(values[index].Value)
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{"artifacts": values}})
}

func (h Handler) ReadArtifact(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	value, err := h.Store.ReadArtifact(r.Context(), owner, mux.Vars(r)["artifact_id"])
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND", "artifact was not found", false)
		return
	}
	if errors.Is(err, workflowstore.ErrPermissionDenied) {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "artifact belongs to another owner", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "ARTIFACT_READ_FAILED", err.Error(), true)
		return
	}
	value.Value, err = artifactfile.Inline(value.Value)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "ARTIFACT_READ_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: value})
}

func (h Handler) PatchArtifact(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	var body struct {
		BaseRevision int             `json:"base_revision"`
		ContentType  string          `json:"content_type"`
		Value        json.RawMessage `json:"value"`
		Caption      *string         `json:"caption"`
		CommandID    string          `json:"command_id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.BaseRevision < 1 || len(body.Value) == 0 {
		fail(w, http.StatusUnprocessableEntity, "INVALID_ARTIFACT_PATCH", "base_revision and value are required", false)
		return
	}
	if body.ContentType == "" {
		body.ContentType = "json"
	}
	if body.CommandID == "" {
		body.CommandID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if body.CommandID == "" {
		fail(w, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REQUIRED", "command_id is required", false)
		return
	}
	value, err := h.Store.PatchArtifact(r.Context(), owner, mux.Vars(r)["artifact_id"],
		body.BaseRevision, body.ContentType, body.Value, body.Caption, body.CommandID)
	if errors.Is(err, workflowstore.ErrIdempotencyConflict) {
		fail(w, http.StatusConflict, "ARTIFACT_REVISION_CONFLICT", "artifact revision is no longer selected", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "ARTIFACT_PATCH_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: value})
}

func (h Handler) DeleteArtifact(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	var body struct {
		BaseRevision int    `json:"base_revision"`
		CommandID    string `json:"command_id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.BaseRevision < 1 {
		fail(w, http.StatusUnprocessableEntity, "INVALID_ARTIFACT_DELETE", "base_revision is required", false)
		return
	}
	if body.CommandID == "" {
		body.CommandID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if body.CommandID == "" {
		fail(w, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REQUIRED", "command_id is required", false)
		return
	}
	value, err := h.Store.DeleteArtifact(r.Context(), owner, mux.Vars(r)["artifact_id"],
		body.BaseRevision, body.CommandID)
	if errors.Is(err, workflowstore.ErrIdempotencyConflict) {
		fail(w, http.StatusConflict, "ARTIFACT_REVISION_CONFLICT", "artifact revision is no longer selected", false)
		return
	}
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND", "artifact was not found", false)
		return
	}
	if errors.Is(err, workflowstore.ErrPermissionDenied) {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "artifact belongs to another owner", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "ARTIFACT_DELETE_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: value})
}

func (h Handler) GetCommand(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	command, err := h.Store.CommandByID(r.Context(), owner, mux.Vars(r)["command_id"])
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "COMMAND_NOT_FOUND", "workflow command was not found", false)
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "COMMAND_READ_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: command})
}

func (h Handler) setStopped(w http.ResponseWriter, r *http.Request, stopped bool) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	commandID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if commandID == "" {
		var body struct {
			CommandID string `json:"command_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		commandID = strings.TrimSpace(body.CommandID)
	}
	if commandID == "" {
		fail(w, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REQUIRED", "command_id is required", false)
		return
	}
	version, err := h.Store.SetSessionStopped(r.Context(), owner, mux.Vars(r)["session_id"], commandID, stopped)
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "WORKFLOW_SESSION_NOT_FOUND", "workflow session was not found", false)
		return
	}
	if errors.Is(err, workflowstore.ErrPermissionDenied) {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "workflow session belongs to another owner", false)
		return
	}
	if err != nil {
		fail(w, http.StatusConflict, "LIFECYCLE_REJECTED", err.Error(), false)
		return
	}
	status := "active"
	if stopped {
		status = "stopped"
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{
		"session_id": mux.Vars(r)["session_id"], "status": status,
		"state_version": version, "command_id": commandID,
	}})
}

func (h Handler) StopWorkflow(w http.ResponseWriter, r *http.Request)   { h.setStopped(w, r, true) }
func (h Handler) ResumeWorkflow(w http.ResponseWriter, r *http.Request) { h.setStopped(w, r, false) }

func (h Handler) BindInput(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	var req struct {
		MaterialID       string `json:"material_id"`
		ResourceType     string `json:"resource_type"`
		ResourceID       string `json:"resource_id"`
		ResourceRevision int64  `json:"resource_revision"`
		ContentHash      string `json:"content_hash"`
		CommandID        string `json:"command_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MaterialID == "" || req.ResourceID == "" || req.CommandID == "" {
		fail(w, 422, "INVALID_INPUT_BINDING", "material_id, resource_id and command_id are required", false)
		return
	}
	binding := workflowstore.InputBinding{WorkflowSessionID: mux.Vars(r)["session_id"], MaterialID: req.MaterialID,
		ResourceType: req.ResourceType, ResourceID: req.ResourceID, ResourceRevision: req.ResourceRevision,
		ContentHash: req.ContentHash, CreatedByCommandID: req.CommandID, CreatedAt: time.Now().UTC()}
	if err := h.Store.BindInput(r.Context(), owner, binding); err != nil {
		if errors.Is(err, workflowstore.ErrNotFound) {
			fail(w, 404, "WORKFLOW_SESSION_NOT_FOUND", "workflow session was not found", false)
		} else if errors.Is(err, workflowstore.ErrPermissionDenied) {
			fail(w, 403, "PERMISSION_DENIED", "resource or session is not accessible", false)
		} else {
			fail(w, 409, "INPUT_BINDING_CONFLICT", err.Error(), false)
		}
		return
	}
	writeJSON(w, 200, envelope{Data: map[string]any{"bound": true}})
}

type validationError string

func (e validationError) Error() string { return string(e) }

func validateToolCommand(body []byte, pathSessionID string) error {
	var command toolCommandRequest
	if err := json.Unmarshal(body, &command); err != nil {
		return validationError("invalid JSON command")
	}
	if command.ContractVersion != ContractVersion {
		return validationError("contract_version must be workflow.v1")
	}
	if command.SessionID == "" || command.SessionID != pathSessionID {
		return validationError("session_id must match request path")
	}
	if command.Tool != "advance_step" && command.Tool != "advance_step_and_hand_off" {
		return validationError("unsupported workflow tool")
	}
	if command.ExpectedStateVersion == nil || *command.ExpectedStateVersion < 0 {
		return validationError("expected_state_version is required")
	}
	if len(command.Steps) == 0 {
		return validationError("at least one step is required")
	}
	for _, step := range command.Steps {
		if strings.TrimSpace(step.StepID) == "" {
			return validationError("step_id is required")
		}
	}
	return nil
}

func legacyTransitionBody(body []byte) ([]byte, error) {
	var public map[string]any
	if err := json.Unmarshal(body, &public); err != nil {
		return nil, err
	}
	steps, _ := public["steps"].([]any)
	targets := make([]any, 0, len(steps))
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		target := map[string]any{"target_step_id": step["step_id"]}
		for _, key := range []string{"task_id", "objective", "user_input", "runtime_instruction", "partial_indices"} {
			if value, ok := step[key]; ok {
				target[key] = value
			}
		}
		targets = append(targets, target)
	}
	if len(targets) > 1 {
		public["operation"] = "execute_batch"
	} else {
		public["operation"] = "advance"
	}
	public["targets"] = targets
	public["hand_off"] = public["tool"] == "advance_step_and_hand_off"
	delete(public, "steps")
	delete(public, "tool")
	delete(public, "contract_version")
	delete(public, "session_id")
	return json.Marshal(public)
}

func (h Handler) Prepare(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", err.Error(), false)
		return
	}
	var req prepareRequest
	if err := json.Unmarshal(body, &req); err != nil || req.WorkflowID == "" {
		fail(w, 422, "INVALID_REQUEST", "workflow_id is required", false)
		return
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if existing, err := h.Store.PreparationByKey(r.Context(), owner, key); err == nil {
		if !bytes.Equal(bytes.TrimSpace(existing.RequestJSON), bytes.TrimSpace(body)) {
			fail(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key was used with another payload", false)
			return
		}
		writePreparation(w, existing)
		return
	} else if !errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, http.StatusServiceUnavailable, "PREPARATION_STORE_FAILED", err.Error(), true)
		return
	}
	if key == "" {
		fail(w, 422, "IDEMPOTENCY_KEY_REQUIRED", "idempotency key is required", false)
		return
	}
	plan := json.RawMessage(`{"status":"ready"}`)
	if workflow, packageErr := h.Store.GetWorkflowPackage(r.Context(), owner, req.WorkflowID, ""); packageErr == nil {
		controllerHost := req.ControllerHost
		if controllerHost == "" {
			controllerHost = req.OriginHost
		}
		if controllerHost == "" {
			controllerHost = "lazymind"
		}
		if h.Hosts != nil {
			var graph preparationGraph
			_ = json.Unmarshal(workflow.CompiledGraph, &graph)
			capabilities, legacyTools := []string{}, []string{}
			for _, node := range graph.Nodes {
				capabilities = append(capabilities, node.Capabilities...)
				legacyTools = append(legacyTools, node.LegacyTools...)
			}
			if supported, missing := h.Hosts.Supports(controllerHost, capabilities, legacyTools); !supported {
				fail(w, http.StatusUnprocessableEntity, "HOST_CAPABILITY_MISSING",
					"selected Host cannot execute this Workflow", false)
				return
			} else if len(missing) > 0 {
				fail(w, http.StatusUnprocessableEntity, "HOST_CAPABILITY_MISSING",
					strings.Join(missing, ", "), false)
				return
			}
			missingInputs := missingExternalInputs(graph, req.InputBindings)
			if len(missingInputs) > 0 {
				plan, _ = json.Marshal(map[string]any{"status": "needs_input", "workflow_ref": workflow.WorkflowRef,
					"workflow_id": workflow.WorkflowID, "workflow_revision": workflow.RevisionID,
					"revision_no": workflow.RevisionNo, "tree_hash": workflow.TreeHash,
					"missing_inputs": missingInputs, "controller_host": controllerHost})
				goto prepared
			}
		}
		plan, _ = json.Marshal(map[string]any{
			"status": "ready", "workflow_ref": workflow.WorkflowRef, "workflow_id": workflow.WorkflowID,
			"workflow_revision": workflow.RevisionID, "revision_no": workflow.RevisionNo,
			"tree_hash": workflow.TreeHash, "warnings": []string{}, "missing_inputs": []string{},
			"origin_host": req.OriginHost, "origin_ref": req.OriginRef,
			"controller_host": controllerHost,
		})
	} else {
		fail(w, http.StatusNotFound, "WORKFLOW_NOT_FOUND", "workflow package was not found", false)
		return
	}
prepared:
	prepared, _, err := h.Store.Prepare(r.Context(), owner, key, req.WorkflowID, ContractVersion, body, plan)
	if err != nil {
		fail(w, 503, "PREPARATION_STORE_FAILED", err.Error(), true)
		return
	}
	writePreparation(w, prepared)
}

func (h Handler) Consume(w http.ResponseWriter, r *http.Request) {
	owner, ok := identityAndVersion(w, r)
	if !ok {
		return
	}
	id := mux.Vars(r)["preparation_id"]
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		fail(w, 422, "INVALID_REQUEST", "invalid consume request", false)
		return
	}
	if req.SessionID == "" {
		req.SessionID = common.GenerateID()
	}
	if len(req.SessionID) > 36 {
		fail(w, 422, "INVALID_REQUEST", "session_id must be at most 36 characters", false)
		return
	}
	prepared, _, err := h.Store.Consume(r.Context(), id, owner, req.SessionID)
	if errors.Is(err, workflowstore.ErrNotFound) {
		fail(w, 404, "PREPARATION_NOT_FOUND", "preparation not found", false)
		return
	}
	if errors.Is(err, workflowstore.ErrPermissionDenied) {
		fail(w, 403, "PERMISSION_DENIED", "preparation belongs to another owner", false)
		return
	}
	if err != nil {
		fail(w, 503, "PREPARATION_CONSUME_FAILED", err.Error(), true)
		return
	}
	var preparedResult map[string]any
	_ = json.Unmarshal(prepared.ResponseJSON, &preparedResult)
	if status, _ := preparedResult["status"].(string); status != "" && status != "ready" {
		fail(w, http.StatusConflict, "PREPARATION_NOT_READY", "preparation requires additional input", false)
		return
	}
	revisionID, _ := preparedResult["workflow_revision"].(string)
	if workflowPackage, packageErr := h.Store.GetWorkflowPackage(r.Context(), owner, prepared.WorkflowID, revisionID); packageErr == nil {
		var original prepareRequest
		_ = json.Unmarshal(prepared.RequestJSON, &original)
		conversationID := strings.TrimSpace(original.ConversationID)
		if conversationID != "" {
			if err := h.Store.AuthorizeConversation(r.Context(), conversationID, owner); err != nil {
				if errors.Is(err, workflowstore.ErrPermissionDenied) {
					fail(w, http.StatusForbidden, "PERMISSION_DENIED", "conversation is unavailable to this owner", false)
				} else {
					fail(w, http.StatusServiceUnavailable, "CONVERSATION_LOOKUP_FAILED", err.Error(), true)
				}
				return
			}
		} else if original.OriginHost == "lazymind" {
			conversationID = original.OriginRef
		}
		session, _, createErr := h.Store.CreateHostSession(r.Context(), owner, req.SessionID, conversationID,
			original.OriginHost, original.OriginRef, original.ControllerHost, workflowPackage)
		if createErr != nil {
			code := "SESSION_CREATE_FAILED"
			if errors.Is(createErr, workflowstore.ErrSessionConflict) {
				code = "WORKFLOW_SESSION_CONFLICT"
			}
			fail(w, http.StatusConflict, code, createErr.Error(), false)
			return
		}
		if strings.TrimSpace(original.RequestContext) != "" {
			intentJSON, _ := json.Marshal(map[string]string{"text": original.RequestContext})
			if intentErr := h.Store.UpdateSessionIntent(
				r.Context(), session.ID, string(intentJSON),
			); intentErr != nil {
				fail(w, http.StatusServiceUnavailable, "SESSION_INTENT_STORE_FAILED", intentErr.Error(), true)
				return
			}
			session.IntentContext = string(intentJSON)
		}
		for materialID, raw := range original.InputBindings {
			value, _ := raw.(map[string]any)
			resourceID, _ := value["resource_id"].(string)
			hash, _ := value["content_hash"].(string)
			revision, _ := value["revision"].(float64)
			if resourceID == "" {
				continue
			}
			binding := workflowstore.InputBinding{WorkflowSessionID: session.ID, MaterialID: materialID,
				ResourceType: "input_resource", ResourceID: resourceID, ResourceRevision: int64(revision),
				ContentHash: hash, CreatedByCommandID: "prepare:" + prepared.ID}
			if bindErr := h.Store.BindInput(r.Context(), owner, binding); bindErr != nil {
				fail(w, http.StatusConflict, "INPUT_BINDING_CONFLICT", bindErr.Error(), false)
				return
			}
		}
		writeJSON(w, http.StatusOK, envelope{Data: map[string]any{
			"workflow_session_id": session.ID, "session_id": session.ID, "status": session.Status,
			"state_version":    session.StateVersion,
			"event_stream_url": "/workflow-sessions/" + session.ID + "/events",
			"status_url":       "/workflow-sessions/" + session.ID + "/projection",
		}})
		return
	}
	fail(w, http.StatusNotFound, "WORKFLOW_NOT_FOUND", "prepared workflow package was not found", false)
}

func (h Handler) Command(delegate http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, ok := identityAndVersion(w, r)
		if !ok {
			return
		}
		sessionID := mux.Vars(r)["session_id"]
		if err := h.Store.AuthorizeSession(r.Context(), sessionID, owner); err != nil {
			if errors.Is(err, workflowstore.ErrNotFound) {
				fail(w, 404, "WORKFLOW_SESSION_NOT_FOUND", "workflow session was not found", false)
				return
			}
			fail(w, 403, "PERMISSION_DENIED", "workflow session belongs to another owner", false)
			return
		}
		commandID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			fail(w, 400, "INVALID_REQUEST", err.Error(), false)
			return
		}
		if commandID == "" {
			var payload struct {
				CommandID string `json:"command_id"`
			}
			_ = json.Unmarshal(body, &payload)
			commandID = payload.CommandID
		}
		if err := validateToolCommand(body, sessionID); err != nil {
			fail(w, http.StatusUnprocessableEntity, "INVALID_TRANSITION", err.Error(), false)
			return
		}
		if commandID == "" {
			fail(w, 422, "IDEMPOTENCY_KEY_REQUIRED", "command_id or Idempotency-Key is required", false)
			return
		}
		result, _, err := h.Store.Command(r.Context(), owner, sessionID, commandID, ContractVersion, body, func(_ *gorm.DB) (int, json.RawMessage, error) {
			legacyBody, err := legacyTransitionBody(body)
			if err != nil {
				return 0, nil, err
			}
			recorder := &capture{header: http.Header{}}
			clone := r.Clone(r.Context())
			clone.Body = io.NopCloser(bytes.NewReader(legacyBody))
			delegate.ServeHTTP(recorder, clone)
			return recorder.status, unwrapLegacyTransitionResponse(recorder.body.Bytes()), nil
		})
		if errors.Is(err, workflowstore.ErrIdempotencyConflict) {
			fail(w, 409, "IDEMPOTENCY_CONFLICT", "command id was used with another payload", false)
			return
		}
		if errors.Is(err, workflowstore.ErrPermissionDenied) {
			fail(w, 403, "PERMISSION_DENIED", "command belongs to another owner", false)
			return
		}
		if err != nil {
			fail(w, 503, "COMMAND_FAILED", err.Error(), true)
			return
		}
		var command toolCommandRequest
		_ = json.Unmarshal(body, &command)
		response := append(json.RawMessage(nil), result.ResponseJSON...)
		if result.HTTPStatus < 400 && command.Tool == "advance_step" {
			var value map[string]any
			_ = json.Unmarshal(response, &value)
			taskIDs := []string{}
			if taskID, _ := value["task_id"].(string); taskID != "" {
				taskIDs = append(taskIDs, taskID)
			}
			if tasks, _ := value["tasks"].([]any); len(tasks) > 0 {
				taskIDs = taskIDs[:0]
				for _, raw := range tasks {
					if item, ok := raw.(map[string]any); ok {
						if taskID, _ := item["task_id"].(string); taskID != "" {
							taskIDs = append(taskIDs, taskID)
						}
					}
				}
			}
			statuses, waitErr := h.Store.WaitTaskStatuses(r.Context(), sessionID, taskIDs)
			if waitErr != nil {
				fail(w, http.StatusGatewayTimeout, "ATTEMPT_WAIT_FAILED", waitErr.Error(), true)
				return
			}
			value["execution_mode"] = "synchronous"
			attemptStatuses := map[string]string{}
			attemptResults := make([]workflowstore.TaskAttemptStatus, 0, len(statuses))
			for _, status := range statuses {
				attemptStatuses[status.TaskID] = status.Status
				attemptResults = append(attemptResults, status)
			}
			value["attempt_statuses"] = attemptStatuses
			value["attempt_results"] = attemptResults
			if len(statuses) == 1 {
				for _, status := range statuses {
					automaticAttempts, countErr := h.Store.AutomaticAttemptCount(r.Context(), sessionID, status.StepID)
					if countErr != nil {
						fail(w, http.StatusInternalServerError, "RETRY_BUDGET_READ_FAILED", countErr.Error(), true)
						return
					}
					value["attempt_status"] = status.Status
					value["step_id"] = status.StepID
					value["attempt"] = status.Attempt
					value["automatic_attempts"] = automaticAttempts
					value["max_automatic_attempts"] = workflowstore.MaxAutomaticWorkflowStepAttempts
					value["automatic_retry_remaining"] = max(0, workflowstore.MaxAutomaticWorkflowStepAttempts-int(automaticAttempts))
					value["user_retry_available"] = true
				}
			}
			if h.Projection != nil {
				recorder := &capture{header: http.Header{}}
				clone := r.Clone(r.Context())
				h.Projection.ServeHTTP(recorder, clone)
				var projectionResponse map[string]any
				if json.Unmarshal(recorder.body.Bytes(), &projectionResponse) == nil {
					if data, ok := projectionResponse["data"].(map[string]any); ok {
						value["workflow_state"] = data
						if projection, ok := data["projection"].(map[string]any); ok {
							value["projection"] = projection
							value["ready_steps"] = projection["ready"]
							value["retryable_steps"] = projection["retryable"]
							value["rewindable_steps"] = projection["rewindable"]
							value["continue_steps"] = projection["continue"]
						}
					}
				}
			}
			response, _ = json.Marshal(value)
			_ = h.Store.UpdateCommandResponse(r.Context(), owner, commandID, result.HTTPStatus, response)
		}
		writeJSON(w, result.HTTPStatus, envelope{Data: response})
	}
}

// TransitionWorkflowSession still uses Core's historical {code,message,data}
// envelope internally. The public Workflow facade must persist and inspect the
// transition payload itself; otherwise task_id is hidden under data and the
// synchronous advance path mistakenly treats the accepted task list as empty.
func unwrapLegacyTransitionResponse(body []byte) json.RawMessage {
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &wrapped) == nil && len(wrapped.Data) > 0 && string(wrapped.Data) != "null" {
		return append(json.RawMessage(nil), wrapped.Data...)
	}
	return append(json.RawMessage(nil), body...)
}

type capture struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (c *capture) Header() http.Header    { return c.header }
func (c *capture) WriteHeader(status int) { c.status = status }
func (c *capture) Write(value []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	return c.body.Write(value)
}
