package modelprovider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

const modelProviderCheckTimeout = 5 * time.Minute

// recentVerifyCache stores sha256(group_id+base_url+api_key) → expiry time for dry_run results.
// Single-instance only; replace with Redis for multi-instance deployments.
var recentVerifyCache sync.Map

type checkModelProviderRequest struct {
	ProviderName string `json:"provider_name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	DryRun       bool   `json:"dry_run"`
}

// algoModelCheckBody matches the algorithm POST /api/model/check JSON contract (lazyllm.OnlineModule).
type algoModelCheckBody struct {
	Model  string `json:"model,omitempty"`
	Source string `json:"source"`
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

// modelCheckResponse mirrors the algorithm /api/model/check JSON (internal parse only).
type modelCheckResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Model   string `json:"model,omitempty"`
	Source  string `json:"source,omitempty"`
	URL     string `json:"url,omitempty"`
}

// CheckModelProviderData is the API response for a model check (mirrors algorithm fields we expose).
type CheckModelProviderData struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// doCheck calls the appropriate algorithm endpoint based on provider category and returns the result.
func doCheck(ctx context.Context, category, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	var checkEndpoint string
	switch category {
	case "ocr":
		checkEndpoint = "/api/ocr/check"
	case "search":
		checkEndpoint = "/api/search/check"
	default:
		checkEndpoint = "/api/model/check"
	}
	upstream := common.JoinURL(common.ChatServiceEndpoint(), checkEndpoint)
	body := algoModelCheckBody{
		Source: providerName,
		URL:    baseURL,
		APIKey: apiKey,
	}
	var result modelCheckResponse
	if err := common.ApiPost(ctx, upstream, body, nil, &result, modelProviderCheckTimeout); err != nil {
		return &result, err
	}
	return &result, nil
}

// verifyCheckCacheKey returns the sha256 hex key for the dry_run cache.
func verifyCheckCacheKey(groupID, baseURL, apiKey string) string {
	h := sha256.Sum256([]byte(groupID + "|" + baseURL + "|" + apiKey))
	return fmt.Sprintf("%x", h)
}

// CheckGroup proxies to the algorithm service for connectivity validation.
// Supports dry_run=true (test only, no DB write) and dry_run=false (test + mark is_verified=true).
func CheckGroup(w http.ResponseWriter, r *http.Request) {
	var req checkModelProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	source := strings.TrimSpace(req.ProviderName)
	urlStr := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	if source == "" || urlStr == "" || apiKey == "" {
		common.ReplyErr(w, "provider_name, base_url, and api_key are required", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	parentID := strings.TrimSpace(mux.Vars(r)["model_provider_id"])
	groupID := strings.TrimSpace(mux.Vars(r)["group_id"])
	if parentID == "" || groupID == "" {
		common.ReplyErr(w, "missing model_provider_id or group_id", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	// Load parent provider to determine category for routing.
	var parent orm.UserModelProvider
	if err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", parentID, userID).
		Take(&parent).Error; err != nil {
		common.ReplyErr(w, "model provider not found", http.StatusNotFound)
		return
	}

	checkStart := time.Now()
	algo, err := doCheck(r.Context(), parent.Category, source, urlStr, apiKey)
	if err != nil {
		log.Logger.Error().
			Err(err).
			Str("category", parent.Category).
			Str("provider_name", source).
			Str("base_url", urlStr).
			Str("user_id", userID).
			Dur("timeout", modelProviderCheckTimeout).
			Dur("elapsed", time.Since(checkStart)).
			Msg("model provider check failed")
		common.ReplyErrWithData(w, err.Error(), algo, http.StatusBadGateway)
		return
	}

	if req.DryRun {
		// dry_run: only test connectivity, do not write DB; cache result for 5 min.
		if algo.Success {
			cacheKey := verifyCheckCacheKey(groupID, urlStr, apiKey)
			recentVerifyCache.Store(cacheKey, time.Now().Add(5*time.Minute))
		}
		common.ReplyOK(w, CheckModelProviderData{Success: algo.Success, Message: algo.Message})
		return
	}

	if algo.Success {
		now := time.Now()
		tx := db.WithContext(r.Context()).
			Model(&orm.UserModelProviderGroup{}).
			Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parentID, userID).
			Updates(map[string]interface{}{
				"is_verified": true,
				"updated_at":  now,
			})
		if tx.Error != nil {
			common.ReplyErr(w, "update group verify status failed", http.StatusInternalServerError)
			return
		}
		if tx.RowsAffected == 0 {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
	}
	common.ReplyOK(w, CheckModelProviderData{Success: algo.Success, Message: algo.Message})
}
