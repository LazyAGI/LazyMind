package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"lazymind/core/workflow/controlstore"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/document"
)

var unsafeArtifactFilename = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeArtifactPathPart(value string) string {
	value = unsafeArtifactFilename.ReplaceAllString(strings.TrimSpace(value), "_")
	value = strings.Trim(value, "._-")
	if value == "" {
		return "unknown"
	}
	return value
}

func isPublicArtifactReference(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") ||
		strings.HasPrefix(value, "https://") ||
		strings.HasPrefix(value, "data:") ||
		strings.HasPrefix(value, "/static-files/") ||
		strings.HasPrefix(value, "/api/core/static-files/")
}

// RemoteHandler is the wire boundary used by out-of-process Host Executors.
// It deliberately exposes no database handles or Host model configuration.
type RemoteHandler struct {
	Finish    func(context.Context, string, string, string, Completion) error
	DB        *gorm.DB
	Attempts  *attempt.Service
	Contexts  ContextLoader
	Artifacts ArtifactSink
}

type remoteEnvelope struct {
	ContractVersion string         `json:"contract_version"`
	OK              bool           `json:"ok"`
	Data            any            `json:"data,omitempty"`
	Error           map[string]any `json:"error,omitempty"`
}

type attemptInputReadError struct {
	Status  int
	Code    string
	Message string
}

func remoteReply(w http.ResponseWriter, status int, data any, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	value := remoteEnvelope{ContractVersion: attempt.ContractVersion, OK: code == "", Data: data}
	if code != "" {
		value.Error = map[string]any{"code": code, "message": message}
	}
	_ = json.NewEncoder(w).Encode(value)
}

func remoteTokenOK(r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN"))
	if expected == "" {
		// Local development remains usable, while non-loopback deployments are
		// expected to set a per-runtime random token in service endpoints.
		host := r.RemoteAddr
		return strings.HasPrefix(host, "127.0.0.1:") || strings.HasPrefix(host, "[::1]:") || host == ""
	}
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return len(provided) == len(expected) && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (h RemoteHandler) authorize(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !remoteTokenOK(r) {
		remoteReply(w, http.StatusUnauthorized, nil, "EXECUTOR_UNAUTHORIZED", "invalid Executor credential")
		return "", false
	}
	token := strings.TrimSpace(r.Header.Get("X-Workflow-Lease-Token"))
	if token == "" {
		remoteReply(w, http.StatusUnauthorized, nil, "LEASE_TOKEN_REQUIRED", "lease token is required")
		return "", false
	}
	if err := h.Attempts.ValidateLease(r.Context(), mux.Vars(r)["attempt_id"], token); err != nil {
		remoteReply(w, http.StatusConflict, nil, attempt.CodeLeaseLost, "attempt lease is no longer valid")
		return "", false
	}
	return token, true
}

func (h RemoteHandler) Context(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	value, err := h.Contexts.LoadAttemptContext(r.Context(), mux.Vars(r)["attempt_id"])
	if err != nil {
		remoteReply(w, 503, nil, "ATTEMPT_CONTEXT_FAILED", err.Error())
		return
	}
	remoteReply(w, 200, value, "", "")
}

func (h RemoteHandler) Input(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	ctx, err := h.Contexts.LoadAttemptContext(r.Context(), mux.Vars(r)["attempt_id"])
	if err != nil {
		remoteReply(w, 503, nil, "ATTEMPT_CONTEXT_FAILED", err.Error())
		return
	}
	materialID := mux.Vars(r)["material_id"]
	rawBinding, ok := ctx.Inputs[materialID]
	if !ok {
		remoteReply(w, 404, nil, "ATTEMPT_INPUT_NOT_FOUND", "input is not bound to this Attempt")
		return
	}
	bindings := make([]map[string]any, 0, 1)
	isList := false
	switch value := rawBinding.(type) {
	case map[string]any:
		bindings = append(bindings, value)
	case []map[string]any:
		isList = true
		bindings = append(bindings, value...)
	case []any:
		isList = true
		for _, item := range value {
			if binding, valid := item.(map[string]any); valid {
				bindings = append(bindings, binding)
			}
		}
	}
	if len(bindings) == 0 {
		remoteReply(w, 404, nil, "ATTEMPT_INPUT_NOT_FOUND", "input binding has an invalid shape")
		return
	}
	items := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		item, readErr := h.readAttemptInput(r.Context(), materialID, binding)
		if readErr != nil {
			remoteReply(w, readErr.Status, nil, readErr.Code, readErr.Message)
			return
		}
		items = append(items, item)
	}
	if !isList && len(items) == 1 {
		remoteReply(w, 200, items[0], "", "")
		return
	}
	remoteReply(w, 200, map[string]any{"material_id": materialID, "items": items}, "", "")
}

func (h RemoteHandler) readAttemptInput(
	ctx context.Context,
	materialID string,
	binding map[string]any,
) (map[string]any, *attemptInputReadError) {
	notFound := func(message string) (map[string]any, *attemptInputReadError) {
		return nil, &attemptInputReadError{
			Status: http.StatusNotFound, Code: "ATTEMPT_INPUT_NOT_FOUND", Message: message,
		}
	}
	resourceID, _ := binding["source_id"].(string)
	if binding["source_type"] == "artifact" {
		var revision orm.WorkflowSlotRevision
		if err := h.DB.WithContext(ctx).Where("id = ?", binding["source_revision_id"]).First(&revision).Error; err != nil {
			// Older bindings pin the revision in material_revision_id/source_id.
			_ = h.DB.WithContext(ctx).Where("id = ?", resourceID).First(&revision).Error
		}
		if revision.ID == "" {
			return notFound("artifact revision was not found")
		}
		var artifact orm.WorkflowHumanArtifact
		if revision.HumanArtifactID == nil || h.DB.WithContext(ctx).Where("id = ?", *revision.HumanArtifactID).First(&artifact).Error != nil {
			return notFound("artifact value was not found")
		}
		expectedHash, _ := binding["content_hash"].(string)
		if expectedHash = strings.TrimSpace(expectedHash); expectedHash != "" {
			actualHash := fmt.Sprintf("sha256:%x", sha256.Sum256(artifact.Value))
			if actualHash != expectedHash {
				return nil, &attemptInputReadError{
					Status: http.StatusConflict, Code: "ATTEMPT_INPUT_CHANGED",
					Message: "artifact input changed after the Attempt was bound",
				}
			}
		}
		if artifact.ContentType == "file" || artifact.ContentType == "image" {
			var file struct {
				Filename string `json:"filename"`
				Path     string `json:"path"`
			}
			if json.Unmarshal(artifact.Value, &file) != nil || file.Path == "" {
				return notFound("artifact file path was not found")
			}
			// Web-found images and public static-file references are durable artifact
			// values, not paths in Core's filesystem. Return their metadata unchanged
			// so the Host can use the public URL without Core performing an unsafe
			// server-side download. Core-owned uploaded files still take the binary
			// materialization path below.
			if isPublicArtifactReference(file.Path) {
				return map[string]any{"material_id": materialID,
					"resource_id": revision.ID, "revision": revision.Revision, "name": revision.Slot + ".json",
					"mime_type": "application/json", "size": len(artifact.Value),
					"content_base64": base64.StdEncoding.EncodeToString(artifact.Value)}, nil
			}
			content, readErr := os.ReadFile(file.Path)
			if readErr != nil {
				return notFound("artifact file was not found")
			}
			name := file.Filename
			if name == "" {
				name = filepath.Base(file.Path)
			}
			mediaType := mime.TypeByExtension(filepath.Ext(name))
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			return map[string]any{"material_id": materialID,
				"resource_id": revision.ID, "revision": revision.Revision, "name": name,
				"mime_type": mediaType, "size": len(content),
				"content_base64": base64.StdEncoding.EncodeToString(content)}, nil
		}
		// Editor revisions store Markdown inline. Export the document bytes, not
		// the persistence envelope, so file consumers retain the .md contract.
		contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(artifact.ContentType, ";", 2)[0]))
		if contentType == "text/markdown" || contentType == "markdown" {
			content, failure := document.ReadContent(artifact.Value, artifact.ContentType, func() (bool, error) { return false, nil })
			var markdown string
			if failure != nil || content == nil || content.Representation != "markdown" || json.Unmarshal(content.Value, &markdown) != nil {
				return nil, &attemptInputReadError{Status: http.StatusUnprocessableEntity,
					Code: "ATTEMPT_INPUT_INVALID", Message: "Markdown artifact input is invalid"}
			}
			return map[string]any{"material_id": materialID,
				"resource_id": revision.ID, "revision": revision.Revision, "name": revision.Slot + ".md",
				"mime_type": "text/markdown", "size": len(markdown),
				"content_base64": base64.StdEncoding.EncodeToString([]byte(markdown))}, nil
		}
		return map[string]any{"material_id": materialID,
			"resource_id": revision.ID, "revision": revision.Revision, "name": revision.Slot + ".json",
			"mime_type": "application/json", "size": len(artifact.Value),
			"content_base64": base64.StdEncoding.EncodeToString(artifact.Value)}, nil
	}
	var resource orm.WorkflowInputResource
	if err := h.DB.WithContext(ctx).Where("id = ?", resourceID).First(&resource).Error; err != nil {
		return notFound("input resource was not found")
	}
	return map[string]any{"material_id": materialID, "resource_id": resource.ID,
		"revision": resource.Revision, "name": resource.Name, "mime_type": resource.MimeType,
		"size": resource.Size, "content_hash": resource.ContentHash,
		"content_base64": base64.StdEncoding.EncodeToString(resource.Content)}, nil
}

func (h RemoteHandler) SaveArtifact(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var body Artifact
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&body) != nil || body.Slot == "" {
		remoteReply(w, 422, nil, "INVALID_ARTIFACT", "slot and value are required")
		return
	}
	if body.Seq < 1 {
		body.Seq = 1
	}
	ctx, err := h.Contexts.LoadAttemptContext(r.Context(), mux.Vars(r)["attempt_id"])
	if err != nil {
		remoteReply(w, 503, nil, "ATTEMPT_CONTEXT_FAILED", err.Error())
		return
	}
	declared := false
	outputs := ctx.DeclaredOutputs
	if len(outputs) == 0 {
		outputs = ctx.RequiredOutputs
	}
	for _, slot := range outputs {
		if slot == body.Slot {
			declared = true
			break
		}
	}
	if !declared {
		remoteReply(w, 422, nil, "OUTPUT_SLOT_UNDECLARED", "artifact slot is not declared by the step")
		return
	}
	if err := validateDeclaredArtifactType(ctx, body); err != nil {
		remoteReply(w, 422, nil, "OUTPUT_TYPE_MISMATCH", err.Error())
		return
	}
	ctx.ExecutionHandle = r.Header.Get("X-Workflow-Lease-Token")
	if err := h.Artifacts.Save(r.Context(), ctx, body); err != nil {
		remoteReply(w, 503, nil, "ARTIFACT_WRITE_FAILED", err.Error())
		return
	}
	remoteReply(w, 200, map[string]any{"saved": true, "slot": body.Slot, "seq": body.Seq}, "", "")
}

type remoteArtifactFileRequest struct {
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
}

// UploadArtifactFile moves a Host-local output into Core's durable upload root.
// Both the Task Center artifact and Workflow slot revision can then reference
// the returned path without duplicating the file contents.
func (h RemoteHandler) UploadArtifactFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	ctx, err := h.Contexts.LoadAttemptContext(r.Context(), mux.Vars(r)["attempt_id"])
	if err != nil {
		remoteReply(w, http.StatusServiceUnavailable, nil, "ATTEMPT_CONTEXT_FAILED", err.Error())
		return
	}
	var body remoteArtifactFileRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 48<<20)).Decode(&body); err != nil {
		remoteReply(w, http.StatusUnprocessableEntity, nil, "INVALID_ARTIFACT_FILE", "filename and content_base64 are required")
		return
	}
	content, err := base64.StdEncoding.DecodeString(body.ContentBase64)
	if err != nil || len(content) == 0 {
		remoteReply(w, http.StatusUnprocessableEntity, nil, "INVALID_ARTIFACT_FILE", "content_base64 must contain file data")
		return
	}
	filename := unsafeArtifactFilename.ReplaceAllString(filepath.Base(strings.TrimSpace(body.Filename)), "_")
	if filename == "" || filename == "." {
		filename = "artifact.bin"
	}
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	filename = base + "-" + digest[:16] + ext
	dir := filepath.Join(doc.UploadRoot(), "workflow-artifacts",
		safeArtifactPathPart(ctx.SessionID), safeArtifactPathPart(ctx.AttemptID))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		remoteReply(w, http.StatusServiceUnavailable, nil, "ARTIFACT_FILE_WRITE_FAILED", err.Error())
		return
	}
	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		temp, createErr := os.CreateTemp(dir, ".artifact-*")
		if createErr != nil {
			remoteReply(w, http.StatusServiceUnavailable, nil, "ARTIFACT_FILE_WRITE_FAILED", createErr.Error())
			return
		}
		tempPath := temp.Name()
		if _, writeErr := temp.Write(content); writeErr != nil {
			_ = temp.Close()
			_ = os.Remove(tempPath)
			remoteReply(w, http.StatusServiceUnavailable, nil, "ARTIFACT_FILE_WRITE_FAILED", writeErr.Error())
			return
		}
		if closeErr := temp.Close(); closeErr != nil {
			_ = os.Remove(tempPath)
			remoteReply(w, http.StatusServiceUnavailable, nil, "ARTIFACT_FILE_WRITE_FAILED", closeErr.Error())
			return
		}
		if renameErr := os.Rename(tempPath, path); renameErr != nil {
			_ = os.Remove(tempPath)
			remoteReply(w, http.StatusServiceUnavailable, nil, "ARTIFACT_FILE_WRITE_FAILED", renameErr.Error())
			return
		}
	} else if err != nil {
		remoteReply(w, http.StatusServiceUnavailable, nil, "ARTIFACT_FILE_WRITE_FAILED", err.Error())
		return
	}
	remoteReply(w, http.StatusOK, map[string]any{"path": path, "size": len(content)}, "", "")
}

type remoteTerminalRequest struct {
	ErrorCode  string          `json:"error_code"`
	LeaseToken string          `json:"lease_token"`
	Result     json.RawMessage `json:"result"`
}

func (h RemoteHandler) Complete(w http.ResponseWriter, r *http.Request) {
	if h.completeControlled(w, r, "succeeded") {
		return
	}
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var body remoteTerminalRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		remoteReply(w, 422, nil, "INVALID_REQUEST", "invalid completion result")
		return
	}
	ctx, err := h.Contexts.LoadAttemptContext(r.Context(), mux.Vars(r)["attempt_id"])
	if err != nil {
		remoteReply(w, 503, nil, "ATTEMPT_CONTEXT_FAILED", err.Error())
		return
	}
	if err := h.ValidateCompletion(ctx); err != nil {
		remoteReply(w, 422, nil, "REQUIRED_OUTPUT_MISSING", err.Error())
		return
	}
	lease := r.Header.Get("X-Workflow-Lease-Token")
	if err := h.Attempts.Complete(r.Context(), ctx.AttemptID, lease, body.Result); err != nil {
		remoteReply(w, 409, nil, "ATTEMPT_TERMINAL_REJECTED", err.Error())
		return
	}
	remoteReply(w, 200, map[string]any{"attempt_status": "succeeded"}, "", "")
}

// ValidateCompletion is called by the terminal handler before accepting a
// remote success. Runtime, not the worker, is authoritative for required output.
func (h RemoteHandler) ValidateCompletion(ctx AttemptContext) error {
	return ValidateRequiredOutputs(context.Background(), h.DB, ctx)
}

func (h RemoteHandler) terminal(w http.ResponseWriter, r *http.Request, status string) {
	if !remoteTokenOK(r) {
		remoteReply(w, 401, nil, "EXECUTOR_UNAUTHORIZED", "invalid Executor credential")
		return
	}
	var body remoteTerminalRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&body); err != nil {
		remoteReply(w, 422, nil, "INVALID_REQUEST", "invalid terminal result")
		return
	}
	id := mux.Vars(r)["attempt_id"]
	row, err := h.Attempts.Attempt(r.Context(), id)
	if err != nil {
		remoteReply(w, 404, nil, attempt.CodeNotFound, "attempt was not found")
		return
	}
	var session orm.WorkflowSession
	if err := h.DB.WithContext(r.Context()).First(&session, "id = ?", row.SessionID).Error; err != nil {
		remoteReply(w, 503, nil, "SESSION_UNAVAILABLE", err.Error())
		return
	}
	lease := r.Header.Get("X-Workflow-Lease-Token")
	if lease == "" {
		lease = body.LeaseToken
	}
	if len(body.Result) == 0 {
		body.Result = json.RawMessage(`{}`)
	}
	if row.ExecutorHost != "lazymind" || lease == "" || row.LeaseToken != lease {
		remoteReply(w, 409, nil, "EXECUTOR_MISMATCH", "native execution ownership is required")
		return
	}
	var result Result
	decoder := json.NewDecoder(bytes.NewReader(body.Result))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		remoteReply(w, 422, nil, "INVALID_RESULT", err.Error())
		return
	}
	if h.Finish == nil {
		remoteReply(w, 503, nil, "CONTROL_FINALIZATION_REQUIRED", "execution completion is unavailable")
		return
	}
	if result.Summary == "" {
		result.Summary = result.Error
	}
	err = h.Finish(r.Context(), session.CreateUserID, session.ID, id, Completion{
		ExecutionHandle: lease, Outcome: status, ErrorCode: body.ErrorCode,
		Summary: result.Summary, ExecutorRef: result.ExecutorRef, Control: result.Control,
		PostStepCheckpoint: result.PostStepCheckpoint,
	})

	if err != nil {
		httpStatus, code := http.StatusServiceUnavailable, "ATTEMPT_TERMINAL_REJECTED"
		var rejection *controlstore.Error
		if errors.As(err, &rejection) {
			code, httpStatus = rejection.Code, rejection.HTTPStatus()
		} else if errors.Is(err, attempt.ErrLeaseLost) || errors.Is(err, attempt.ErrAlreadyTerminal) {
			httpStatus = http.StatusConflict
		}
		remoteReply(w, httpStatus, nil, code, err.Error())
		return
	}
	remoteReply(w, 200, map[string]any{"attempt_status": status}, "", "")
}

// Native sessions retain the existing terminal protocol. Only opted-in external
// sessions use review finalization when a step runs on the LazyMind executor.
func (h RemoteHandler) completeControlled(w http.ResponseWriter, r *http.Request, status string) bool {
	if h.Finish == nil {
		return false
	}
	if !remoteTokenOK(r) {
		return false
	}
	row, err := h.Attempts.Attempt(r.Context(), mux.Vars(r)["attempt_id"])
	if err != nil {
		return false
	}
	var session orm.WorkflowSession
	if err := h.DB.WithContext(r.Context()).First(&session, "id = ?", row.SessionID).Error; err != nil {
		remoteReply(w, 503, nil, "SESSION_UNAVAILABLE", err.Error())
		return true
	}
	if !controlstore.Controlled(session) {
		return false
	}
	h.terminal(w, r, status)
	return true
}
func (h RemoteHandler) Fail(w http.ResponseWriter, r *http.Request) {
	if !h.completeControlled(w, r, "failed") {
		(attempt.Handler{Service: h.Attempts}).Fail(w, r)
	}
}
func (h RemoteHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if !h.completeControlled(w, r, "cancelled") {
		(attempt.Handler{Service: h.Attempts}).Cancel(w, r)
	}
}
