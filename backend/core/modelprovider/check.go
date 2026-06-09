package modelprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
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
const cloudServiceCheckTimeout = 30 * time.Second

const (
	minerUCheckFileName        = "lazymind-key-check.pdf"
	paddleOCRDefaultModel      = "PaddleOCR-VL-1.6"
	paddleOCROptionalPayload   = `{"useDocOrientationClassify":false,"useDocUnwarping":false,"useChartRecognition":true}`
	paddleOCRAcceptedNoFileMsg = "PaddleOCR API key accepted"
	minerUAcceptedMsg          = "MinerU API key accepted"
	tavilyAcceptedMsg          = "Tavily API key accepted"
	bingSearchAcceptedMsg      = "Bing Search API key accepted"
	googleSearchAcceptedMsg    = "Google Custom Search API key accepted"
	bochaSearchAcceptedMsg     = "Bocha Search API key accepted"
	sciverseSearchAcceptedMsg  = "Sciverse API key accepted"
)

var sciverseDefaultMetaFields = []string{
	"title",
	"doi",
	"doc_id",
	"abstract",
	"author",
	"publication_published_year",
	"publication_venue_name_unified",
}

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

func doProviderGroupCheck(ctx context.Context, category, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	if category == "ocr" && isSupportedOCRCloudProvider(providerName) {
		return doOCRCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	}
	if usesSearchCloudServiceCheck(category, providerName) {
		return doSearchCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	}
	return doCheck(ctx, category, providerName, baseURL, apiKey)
}

func isSupportedOCRCloudProvider(providerName string) bool {
	switch normalizeProviderName(providerName) {
	case "mineru", "paddleocr":
		return true
	default:
		return false
	}
}

func isSupportedSearchCloudProvider(providerName string) bool {
	switch normalizeProviderName(providerName) {
	case "tavily", "bing", "bingsearch", "google", "googlesearch", "googlecustomsearch", "bocha", "bochasearch", "sciverse", "sciversesearch":
		return true
	default:
		return false
	}
}

func usesSearchCloudServiceCheck(category, providerName string) bool {
	if category == "search" {
		return true
	}
	return category == "datasource" && isSupportedSearchCloudProvider(providerName)
}

func shouldVerifyCloudServiceOnSave(category, providerName string) bool {
	return usesSearchCloudServiceCheck(category, providerName) || category == "ocr" && isSupportedOCRCloudProvider(providerName)
}

func doOCRCloudServiceCheck(ctx context.Context, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	switch normalizeProviderName(providerName) {
	case "mineru":
		return doMinerUCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	case "paddleocr":
		return doPaddleOCRCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	default:
		return &modelCheckResponse{Success: false, Message: "unsupported OCR cloud service"}, nil
	}
}

func doSearchCloudServiceCheck(ctx context.Context, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	switch normalizeProviderName(providerName) {
	case "tavily":
		return doTavilySearchCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	case "bing", "bingsearch":
		return doBingSearchCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	case "google", "googlesearch", "googlecustomsearch":
		return doGoogleCustomSearchCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	case "bocha", "bochasearch":
		return doBochaSearchCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	case "sciverse", "sciversesearch":
		return doSciverseSearchCloudServiceCheck(ctx, providerName, baseURL, apiKey)
	default:
		return &modelCheckResponse{Success: false, Message: "unsupported search cloud service"}, nil
	}
}

func doMinerUCloudServiceCheck(ctx context.Context, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	payload := map[string]any{
		"files": []map[string]string{
			{"name": minerUCheckFileName},
		},
		"model_version": "vlm",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal mineru check body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, minerUFileURLBatchEndpoint(baseURL), bytes.NewReader(body))
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey)}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	return doCloudServiceCheckRequest(req, providerName, baseURL, apiKey, minerUAcceptedMsg)
}

func doPaddleOCRCloudServiceCheck(ctx context.Context, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", paddleOCRDefaultModel); err != nil {
		return nil, fmt.Errorf("write paddleocr model field: %w", err)
	}
	if err := writer.WriteField("optionalPayload", paddleOCROptionalPayload); err != nil {
		return nil, fmt.Errorf("write paddleocr optional payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close paddleocr multipart body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(baseURL), &body)
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey)}, nil
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "bearer "+apiKey)

	return doCloudServiceCheckRequest(req, providerName, baseURL, apiKey, paddleOCRAcceptedNoFileMsg)
}

func doTavilySearchCloudServiceCheck(ctx context.Context, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	payload := map[string]any{
		"query":       "lazymind key check",
		"max_results": 1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal tavily check body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilySearchEndpoint(baseURL), bytes.NewReader(body))
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey)}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	return doSearchServiceCheckRequest(req, providerName, baseURL, apiKey, tavilyAcceptedMsg)
}

func doBingSearchCloudServiceCheck(ctx context.Context, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	endpoint, err := addQueryParams(bingSearchEndpoint(baseURL), url.Values{
		"q":              []string{"lazymind key check"},
		"count":          []string{"1"},
		"responseFilter": []string{"Webpages"},
	})
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey)}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey)}, nil
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", apiKey)

	return doSearchServiceCheckRequest(req, providerName, baseURL, apiKey, bingSearchAcceptedMsg)
}

func doGoogleCustomSearchCloudServiceCheck(ctx context.Context, providerName, baseURL, credential string) (*modelCheckResponse, error) {
	apiKey, searchEngineID := splitGoogleCustomSearchCredential(credential)
	if apiKey == "" || searchEngineID == "" {
		return &modelCheckResponse{
			Success: false,
			Message: "Google Custom Search requires API Key and Search Engine ID",
			Source:  providerName,
			URL:     baseURL,
		}, nil
	}
	endpoint, err := addQueryParams(googleCustomSearchEndpoint(baseURL), url.Values{
		"key": []string{apiKey},
		"cx":  []string{searchEngineID},
		"q":   []string{"lazymind key check"},
		"num": []string{"1"},
	})
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), credential), Source: providerName, URL: baseURL}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), credential), Source: providerName, URL: baseURL}, nil
	}

	return doSearchServiceCheckRequest(req, providerName, baseURL, credential, googleSearchAcceptedMsg)
}

func doBochaSearchCloudServiceCheck(ctx context.Context, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	payload := map[string]any{
		"query": "lazymind key check",
		"count": 1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal bocha check body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bochaSearchEndpoint(baseURL), bytes.NewReader(body))
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey)}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	return doSearchServiceCheckRequest(req, providerName, baseURL, apiKey, bochaSearchAcceptedMsg)
}

func doSciverseSearchCloudServiceCheck(ctx context.Context, providerName, baseURL, apiKey string) (*modelCheckResponse, error) {
	payload := map[string]any{
		"fields":    sciverseDefaultMetaFields,
		"page":      1,
		"query":     "lazymind key check",
		"page_size": 1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal sciverse check body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sciverseMetaSearchEndpoint(baseURL), bytes.NewReader(body))
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey)}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	return doSearchServiceCheckRequest(req, providerName, baseURL, apiKey, sciverseSearchAcceptedMsg)
}

func minerUFileURLBatchEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/file-urls/batch") {
		return base
	}
	return common.JoinURL(base, "file-urls/batch")
}

func tavilySearchEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/search") {
		return base
	}
	return common.JoinURL(base, "search")
}

func bingSearchEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/v7.0/search") {
		return base
	}
	return common.JoinURL(base, "v7.0/search")
}

func googleCustomSearchEndpoint(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func bochaSearchEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/v1/web-search") {
		return base
	}
	return common.JoinURL(base, "v1/web-search")
}

func sciverseMetaSearchEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/meta-search") {
		return base
	}
	return common.JoinURL(base, "meta-search")
}

func splitGoogleCustomSearchCredential(credential string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(credential), "|", 2)
	if len(parts) != 2 {
		return strings.TrimSpace(credential), ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func addQueryParams(rawURL string, values url.Values) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for key, entries := range values {
		for _, entry := range entries {
			query.Add(key, entry)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func doSearchServiceCheckRequest(req *http.Request, providerName, baseURL, apiKey, successMessage string) (*modelCheckResponse, error) {
	resp, err := (&http.Client{Timeout: cloudServiceCheckTimeout}).Do(req)
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey), Source: providerName, URL: baseURL}, nil
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey), Source: providerName, URL: baseURL}, nil
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return &modelCheckResponse{Success: true, Message: successMessage, Source: providerName, URL: baseURL}, nil
	}

	message := cloudServiceCheckFailureMessage(resp.StatusCode, respBytes, apiKey)
	return &modelCheckResponse{Success: false, Message: message, Source: providerName, URL: baseURL}, nil
}

func doCloudServiceCheckRequest(req *http.Request, providerName, baseURL, apiKey, successMessage string) (*modelCheckResponse, error) {
	resp, err := (&http.Client{Timeout: cloudServiceCheckTimeout}).Do(req)
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey), Source: providerName, URL: baseURL}, nil
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return &modelCheckResponse{Success: false, Message: safeCheckMessage(err.Error(), apiKey), Source: providerName, URL: baseURL}, nil
	}
	if cloudServiceCheckAccepted(resp.StatusCode, respBytes) {
		return &modelCheckResponse{Success: true, Message: successMessage, Source: providerName, URL: baseURL}, nil
	}

	message := cloudServiceCheckFailureMessage(resp.StatusCode, respBytes, apiKey)
	return &modelCheckResponse{Success: false, Message: message, Source: providerName, URL: baseURL}, nil
}

func cloudServiceCheckAccepted(statusCode int, respBytes []byte) bool {
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return true
	}
	if statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity {
		return !looksLikeAuthFailure(respBytes)
	}
	return false
}

func looksLikeAuthFailure(respBytes []byte) bool {
	text := strings.ToLower(strings.TrimSpace(string(respBytes)))
	if text == "" {
		return false
	}
	authSignals := []string{
		"unauthorized",
		"forbidden",
		"invalid token",
		"invalid api key",
		"invalid apikey",
		"api key invalid",
		"token invalid",
		"authentication",
		"authorization",
	}
	for _, signal := range authSignals {
		if strings.Contains(text, signal) {
			return true
		}
	}
	return false
}

func cloudServiceCheckFailureMessage(statusCode int, respBytes []byte, apiKey string) string {
	message := extractCloudServiceMessage(respBytes)
	if message == "" {
		message = fmt.Sprintf("cloud service returned HTTP %d", statusCode)
	}
	return safeCheckMessage(message, apiKey)
}

func extractCloudServiceMessage(respBytes []byte) string {
	body := strings.TrimSpace(string(respBytes))
	if body == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal(respBytes, &payload); err != nil {
		return body
	}
	return extractCloudServiceMessageValue(payload)
}

func extractCloudServiceMessageValue(value any) string {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"message", "msg", "error", "detail", "reason", "errorMsg"} {
			if raw, ok := v[key]; ok {
				if text := extractCloudServiceMessageValue(raw); text != "" {
					return text
				}
			}
		}
		for _, raw := range v {
			if text := extractCloudServiceMessageValue(raw); text != "" {
				return text
			}
		}
	case []any:
		for _, raw := range v {
			if text := extractCloudServiceMessageValue(raw); text != "" {
				return text
			}
		}
	case string:
		return strings.TrimSpace(v)
	case float64, bool, int, int64, uint64:
		return fmt.Sprint(v)
	}
	return ""
}

func safeCheckMessage(message, apiKey string) string {
	const maxLen = 240
	text := strings.TrimSpace(strings.Join(strings.Fields(message), " "))
	if apiKey != "" {
		text = strings.ReplaceAll(text, apiKey, "[api_key]")
	}
	if text == "" {
		text = "cloud service verification failed"
	}
	if len(text) > maxLen {
		return text[:maxLen] + "..."
	}
	return text
}

func normalizeProviderName(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return -1
	}, value)
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
	if source == "" || urlStr == "" {
		common.ReplyErr(w, "provider_name and base_url are required", http.StatusBadRequest)
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
	var group orm.UserModelProviderGroup
	if err := db.WithContext(r.Context()).
		Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parentID, userID).
		Take(&group).Error; err != nil {
		common.ReplyErr(w, "group not found", http.StatusNotFound)
		return
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(group.APIKey)
	}
	apiKeyRequired := isAPIKeyRequiredForBaseURL(r.Context(), db, parent.DefaultModelProviderID, urlStr)
	if apiKey == "" && apiKeyRequired {
		common.ReplyErr(w, "api_key is required when using the default base_url", http.StatusBadRequest)
		return
	}
	if apiKey == "" {
		if !req.DryRun {
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
		common.ReplyOK(w, CheckModelProviderData{Success: true})
		return
	}

	checkStart := time.Now()
	algo, err := doProviderGroupCheck(r.Context(), parent.Category, source, urlStr, apiKey)
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
