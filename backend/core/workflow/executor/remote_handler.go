package executor

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/attempt"
)

// RemoteHandler is the wire boundary used by out-of-process Host Executors.
// It deliberately exposes no database handles or Host model configuration.
type RemoteHandler struct {
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
	binding, ok := ctx.Inputs[mux.Vars(r)["material_id"]].(map[string]any)
	if !ok {
		remoteReply(w, 404, nil, "ATTEMPT_INPUT_NOT_FOUND", "input is not bound to this Attempt")
		return
	}
	resourceID, _ := binding["source_id"].(string)
	if binding["source_type"] == "artifact" {
		var revision orm.WorkflowSlotRevision
		if err := h.DB.WithContext(r.Context()).Where("id = ?", binding["source_revision_id"]).First(&revision).Error; err != nil {
			// Older bindings pin the revision in material_revision_id/source_id.
			_ = h.DB.WithContext(r.Context()).Where("id = ?", resourceID).First(&revision).Error
		}
		if revision.ID == "" {
			remoteReply(w, 404, nil, "ATTEMPT_INPUT_NOT_FOUND", "artifact revision was not found")
			return
		}
		var artifact orm.WorkflowHumanArtifact
		if revision.HumanArtifactID == nil || h.DB.WithContext(r.Context()).Where("id = ?", *revision.HumanArtifactID).First(&artifact).Error != nil {
			remoteReply(w, 404, nil, "ATTEMPT_INPUT_NOT_FOUND", "artifact value was not found")
			return
		}
		remoteReply(w, 200, map[string]any{"material_id": mux.Vars(r)["material_id"],
			"resource_id": revision.ID, "revision": revision.Revision, "name": revision.Slot + ".json",
			"mime_type": "application/json", "size": len(artifact.Value),
			"content_base64": base64.StdEncoding.EncodeToString(artifact.Value)}, "", "")
		return
	}
	var resource orm.WorkflowInputResource
	if err := h.DB.WithContext(r.Context()).Where("id = ?", resourceID).First(&resource).Error; err != nil {
		remoteReply(w, 404, nil, "ATTEMPT_INPUT_NOT_FOUND", "input resource was not found")
		return
	}
	remoteReply(w, 200, map[string]any{"material_id": mux.Vars(r)["material_id"], "resource_id": resource.ID,
		"revision": resource.Revision, "name": resource.Name, "mime_type": resource.MimeType,
		"size": resource.Size, "content_hash": resource.ContentHash,
		"content_base64": base64.StdEncoding.EncodeToString(resource.Content)}, "", "")
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
	if err := h.Artifacts.Save(r.Context(), ctx, body); err != nil {
		remoteReply(w, 503, nil, "ARTIFACT_WRITE_FAILED", err.Error())
		return
	}
	remoteReply(w, 200, map[string]any{"saved": true, "slot": body.Slot, "seq": body.Seq}, "", "")
}

type remoteTerminalRequest struct {
	LeaseToken string          `json:"lease_token"`
	Result     json.RawMessage `json:"result"`
}

func (h RemoteHandler) Complete(w http.ResponseWriter, r *http.Request) {
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
