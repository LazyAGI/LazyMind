package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const modelProbeTimeout = 45 * time.Second
const modelProbeResponseLimit = 16 * 1024

// ModelProbeResponse contains public diagnostics only, never provider text,
// generated media, credentials, endpoint addresses or inference output.
type ModelProbeResponse struct {
	Status     string `json:"status" enum:"passed,failed,unsupported,accepted"`
	Code       string `json:"code" enum:"ok,accepted,unsupported_type,unsupported_provider,unknown_type,authentication_failed,permission_denied,model_not_found,rate_limited,quota_exceeded,invalid_request,timeout,connection_error,invalid_response,empty_response,upstream_error,configuration_changed,key_disabled,key_required,probe_in_progress"`
	ModelType  string `json:"model_type"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type modelProbeSnapshot struct {
	Model     string `json:"model"`
	ModelType string `json:"model_type"`
	Source    string `json:"source"`
	URL       string `json:"url"`
	APIKey    string `json:"api_key"`
	SkipAuth  bool   `json:"skip_auth"`
	Vision    bool   `json:"vision"`
}

var runningModelProbes sync.Map

// ProbeGroupModel tests one saved model without changing supplier verification,
// key selection, model metadata or default-role configuration.
func ProbeGroupModel(w http.ResponseWriter, r *http.Request) {
	probeGroupModel(w, r, modelProbeTimeout)
}

func probeGroupModel(w http.ResponseWriter, r *http.Request, timeout time.Duration) {
	started := time.Now()
	reply := func(result ModelProbeResponse) {
		result.ElapsedMS = time.Since(started).Milliseconds()
		common.ReplyOK(w, result)
	}
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		var empty struct{}
		if err := decoder.Decode(&empty); err != nil && !errors.Is(err, io.EOF) {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	snapshot, unavailable, err := loadProbeSnapshot(r.Context(), db, userID, vars["model_provider_id"], vars["group_id"], vars["model_id"])
	if err != nil {
		probeLookupError(w, err, "model not found")
		return
	}
	if unavailable != nil {
		reply(*unavailable)
		return
	}
	probeKey := userID + "\x00" + vars["model_id"]
	if _, busy := runningModelProbes.LoadOrStore(probeKey, struct{}{}); busy {
		reply(ModelProbeResponse{Status: "failed", Code: "probe_in_progress", ModelType: snapshot.ModelType})
		return
	}
	defer runningModelProbes.Delete(probeKey)

	result := executeModelProbe(r.Context(), snapshot, timeout)
	if result.Status == "passed" || result.Status == "accepted" {
		current, unavailable, err := loadProbeSnapshot(r.Context(), db, userID, vars["model_provider_id"], vars["group_id"], vars["model_id"])
		if err != nil || unavailable != nil || current != snapshot {
			result = ModelProbeResponse{Status: "failed", Code: "configuration_changed", ModelType: snapshot.ModelType}
		}
	}
	reply(result)
}

// loadProbeSnapshot holds the same parent-row lock as configuration writers only
// while reading. Endpoint, model and active key therefore belong to one saved
// configuration; the transaction is released before any network request.
func loadProbeSnapshot(ctx context.Context, db *gorm.DB, userID, providerID, groupID, modelID string) (modelProbeSnapshot, *ModelProbeResponse, error) {
	var snapshot modelProbeSnapshot
	var unavailable *ModelProbeResponse
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent orm.UserModelProvider
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", providerID, userID).Take(&parent).Error; err != nil {
			return err
		}
		var group orm.UserModelProviderGroup
		if err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parent.ID, userID).Take(&group).Error; err != nil {
			return err
		}
		var model orm.UserModelProviderGroupModel
		if err := tx.Where("id = ? AND user_model_provider_id = ? AND user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL", modelID, parent.ID, group.ID, userID).Take(&model).Error; err != nil {
			return err
		}
		modelType := normalizedDeclaredModelType(model.ModelType)
		reject := func(status, code string) {
			unavailable = &ModelProbeResponse{Status: status, Code: code, ModelType: modelType}
		}
		if !hasSharedProviderEndpoint(parent) {
			reject("unsupported", "unsupported_type")
			return nil
		}
		switch modelType {
		case "llm", "vlm", "embed", "multimodal_embedding", "rerank", "stt", "tts", "text2image", "text2video", "image_editing":
		default:
			reject("unsupported", "unknown_type")
			return nil
		}
		if validateSupplierModelType(parent, modelType) != nil {
			reject("unsupported", "unsupported_type")
			return nil
		}
		snapshot = modelProbeSnapshot{Model: model.Name, ModelType: modelType, Source: RuntimeProviderName(parent), URL: connectionBaseURL(parent, group.BaseURL), Vision: model.Vision || modelType == "vlm"}
		if isVLLMSupplier(ctx, tx, parent) && (normalizeProviderName(snapshot.Source) == "openai" || normalizeProviderName(snapshot.Source) == "vllm") {
			snapshot.Source = "VLLM"
		}
		key, code := readProbeCredential(ctx, tx, parent, snapshot.URL)
		if code != "" {
			reject("failed", code)
			return nil
		}
		snapshot.APIKey, snapshot.SkipAuth = key, key == ""
		return nil
	})
	return snapshot, unavailable, err
}

func probeLookupError(w http.ResponseWriter, err error, missing string) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, missing, http.StatusNotFound)
	} else {
		common.ReplyErr(w, "query model provider failed", http.StatusInternalServerError)
	}
}

// readProbeCredential deliberately avoids the runtime resolver's initialization
// and credential migration writes. An unverified active key may be tested; an
// explicitly disabled pool cannot silently fall back to its first key.
func readProbeCredential(ctx context.Context, db *gorm.DB, parent orm.UserModelProvider, baseURL string) (string, string) {
	var groups []orm.UserModelProviderGroup
	if err := db.WithContext(ctx).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", parent.ID, parent.CreateUserID).Order("created_at ASC, id ASC").Find(&groups).Error; err != nil {
		return "", "configuration_changed"
	}
	hasKeys := false
	for _, group := range groups {
		if strings.TrimSpace(group.APIKey) != "" || strings.TrimSpace(group.APIKeyCiphertext) != "" {
			hasKeys = true
		}
		if parent.ActiveKeyInitialized && (group.ID != parent.ActiveKeyGroupID || parent.ActiveKeyID == "") {
			continue
		}
		pool, err := readProbeKeyPool(group)
		if err != nil {
			return "", "configuration_changed"
		}
		keys := splitAPIKeys(pool)
		if !parent.ActiveKeyInitialized && len(keys) > 0 {
			return keys[0], ""
		}
		if parent.ActiveKeyInitialized {
			if key := keyMatchingID(group.ID, parent.ActiveKeyID, pool); key != "" {
				return key, ""
			}
		}
	}
	if hasKeys || parent.ActiveKeyID != "" || parent.ActiveKeyGroupID != "" {
		return "", "key_disabled"
	}
	if providerAPIKeyRequiredForBaseURL(ctx, db, parent, baseURL) {
		return "", "key_required"
	}
	return "", ""
}

func readProbeKeyPool(group orm.UserModelProviderGroup) (string, error) {
	if strings.TrimSpace(group.APIKeyCiphertext) == "" {
		return strings.TrimSpace(group.APIKey), nil
	}
	if group.CredentialVersion >= modelProviderCredentialVersion {
		return decryptModelProviderAPIKeyForGroup(group.CreateUserID, group.ID, max(group.CredentialRevision, 1), group.APIKeyCiphertext)
	}
	return decodeLegacyModelProviderCiphertext(group.APIKeyCiphertext)
}

// executeModelProbe has one bounded request and never logs or forwards raw
// upstream response/error text. The algorithm owns task-specific minimal input.
func executeModelProbe(ctx context.Context, snapshot modelProbeSnapshot, timeout time.Duration) ModelProbeResponse {
	result := ModelProbeResponse{Status: "failed", Code: "connection_error", ModelType: snapshot.ModelType}
	body, err := json.Marshal(snapshot)
	if err != nil {
		result.Code = "invalid_request"
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(common.ChatServiceEndpoint(), "/api/model/probe"), bytes.NewReader(body))
	if err != nil {
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		var timeoutErr interface{ Timeout() bool }
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			result.Code = "timeout"
		}
		return result
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Code, result.HTTPStatus = "upstream_error", response.StatusCode
		return result
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, modelProbeResponseLimit+1))
	if err != nil || len(raw) > modelProbeResponseLimit {
		result.Code = "invalid_response"
		var timeoutErr interface{ Timeout() bool }
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			result.Code = "timeout"
		}
		return result
	}
	var upstream ModelProbeResponse
	if json.Unmarshal(raw, &upstream) != nil || !validModelProbeOutcome(upstream.Status, upstream.Code) {
		result.Code = "invalid_response"
		return result
	}
	result.Status, result.Code = upstream.Status, upstream.Code
	if upstream.HTTPStatus >= 100 && upstream.HTTPStatus <= 599 {
		result.HTTPStatus = upstream.HTTPStatus
	}
	return result
}

func validModelProbeOutcome(status, code string) bool {
	switch status {
	case "passed":
		return code == "ok"
	case "accepted":
		return code == "accepted"
	case "unsupported":
		return code == "unsupported_type" || code == "unsupported_provider" || code == "unknown_type"
	case "failed":
		switch code {
		case "authentication_failed", "permission_denied", "model_not_found", "rate_limited", "quota_exceeded", "invalid_request", "timeout", "connection_error", "invalid_response", "empty_response", "upstream_error", "configuration_changed", "key_disabled", "key_required", "probe_in_progress":
			return true
		}
	}
	return false
}
