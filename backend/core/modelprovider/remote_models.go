package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const remoteModelsTimeout = 20 * time.Second

var (
	errRemoteModelsHostNotAllowed = errors.New("group base_url host is not allowed")
	remoteModelsDialer            = &net.Dialer{Timeout: 10 * time.Second}
	remoteModelsLookupIP          = lookupRemoteModelsIPs
	remoteModelsDialContext       = defaultRemoteModelsDial
	remoteModelsHTTPClient        = newRemoteModelsHTTPClient()
)

func defaultRemoteModelsDial(ctx context.Context, network, address string) (net.Conn, error) {
	return remoteModelsDialer.DialContext(ctx, network, address)
}

// remoteModelsAllowPrivateHosts is test-only so httptest.Server (loopback) can run.
var remoteModelsAllowPrivateHosts bool

func lookupRemoteModelsIPs(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func newRemoteModelsHTTPClient() *http.Client {
	return &http.Client{
		Timeout: remoteModelsTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: dialRemoteModels,
		},
	}
}

func dialRemoteModels(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := resolveRemoteModelsDialIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := remoteModelsDialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, errRemoteModelsHostNotAllowed
	}
	return nil, lastErr
}

func resolveRemoteModelsDialIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedRemoteModelsIP(ip) {
			return nil, errRemoteModelsHostNotAllowed
		}
		return []net.IP{ip}, nil
	}
	ips, err := remoteModelsLookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	allowed := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if !isBlockedRemoteModelsIP(ip) {
			allowed = append(allowed, ip)
		}
	}
	if len(allowed) == 0 {
		return nil, errRemoteModelsHostNotAllowed
	}
	return allowed, nil
}

type remoteGroupModelItem struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	ModelType      string   `json:"model_type"`
	Input          []string `json:"input"`
	Output         []string `json:"output"`
	MaxInputTokens *string  `json:"max_input_tokens,omitempty"`
	Added          bool     `json:"added"`
}

type remoteGroupModelsResponse struct {
	URL    string                 `json:"url"`
	Models []remoteGroupModelItem `json:"models"`
}

type openaiModelListResponse struct {
	Data   []openaiModelListEntry `json:"data"`
	Models []openaiModelListEntry `json:"models"`
}

type openaiModelListEntry struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Model            string   `json:"model"`
	ModelType        string   `json:"model_type"`
	TaskType         string   `json:"task_type"`
	Task             string   `json:"task"`
	Type             string   `json:"type"`
	ContextLength    int      `json:"context_length"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Architecture     struct {
		Input  []string `json:"input_modalities"`
		Output []string `json:"output_modalities"`
	} `json:"architecture"`
}

func remoteModelID(entry openaiModelListEntry) string {
	for _, value := range []string{entry.ID, entry.Name, entry.Model} {
		if name := strings.TrimSpace(value); name != "" {
			return name
		}
	}
	return ""
}

func parseRemoteModelEntries(raw []byte) []openaiModelListEntry {
	var parsed openaiModelListResponse
	_ = json.Unmarshal(raw, &parsed)
	entries := parsed.Data
	if len(entries) == 0 {
		entries = parsed.Models
	}
	if len(entries) == 0 {
		for _, id := range parseRemoteModelIDs(raw) {
			entries = append(entries, openaiModelListEntry{ID: id})
		}
	}
	seen := make(map[string]bool)
	result := make([]openaiModelListEntry, 0, len(entries))
	for _, entry := range entries {
		id := remoteModelID(entry)
		key := strings.ToLower(id)
		if id == "" || seen[key] {
			continue
		}
		seen[key] = true
		entry.ID = id
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].ID) < strings.ToLower(result[j].ID) })
	return result
}

func modelsListURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid group base_url")
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) == 1 && segments[0] == "" {
		segments = nil
	}
	if n := len(segments); n > 0 && strings.EqualFold(segments[n-1], "models") {
		parsed.Path = "/" + strings.Join(segments, "/")
	} else if n := len(segments); n > 0 && strings.EqualFold(segments[n-1], "v1") {
		parsed.Path = "/" + strings.Join(segments, "/") + "/models"
	} else {
		v1Index := -1
		for i, segment := range segments {
			if strings.EqualFold(segment, "v1") {
				v1Index = i
				break
			}
		}
		if v1Index >= 0 {
			parsed.Path = "/" + strings.Join(segments[:v1Index+1], "/") + "/models"
		} else if len(segments) == 0 {
			parsed.Path = "/v1/models"
		} else {
			parsed.Path = "/" + strings.Join(segments, "/") + "/v1/models"
		}
	}
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	if err := validateRemoteModelsURL(parsed); err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func validateRemoteModelsURL(parsed *url.URL) error {
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("group base_url must use http or https")
	}
	if parsed.User != nil {
		return errors.New("group base_url must not include credentials")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return errors.New("invalid group base_url")
	}
	if isBlockedRemoteModelsHost(host) {
		return errRemoteModelsHostNotAllowed
	}
	return nil
}

func isBlockedRemoteModelsHost(host string) bool {
	lower := strings.ToLower(host)
	if lower == "metadata.google.internal" || strings.HasSuffix(lower, ".metadata.google.internal") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isBlockedRemoteModelsIP(ip)
	}
	return false
}

func isBlockedRemoteModelsIP(ip net.IP) bool {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip.IsLoopback() {
		return !remoteModelsAllowPrivateHosts
	}
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 169 && ip4[1] == 254 {
		return true
	}
	return ip.Equal(net.ParseIP("fd00:ec2::254"))
}

func parseRemoteModelIDs(raw []byte) []string {
	var parsed openaiModelListResponse
	if err := json.Unmarshal(raw, &parsed); err == nil {
		entries := parsed.Data
		if len(entries) == 0 {
			entries = parsed.Models
		}
		ids := make([]string, 0, len(entries))
		for _, entry := range entries {
			id := strings.TrimSpace(entry.ID)
			if id == "" {
				id = strings.TrimSpace(entry.Name)
			}
			if id == "" {
				id = strings.TrimSpace(entry.Model)
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return uniqueSortedIDs(ids)
		}
	}

	var ids []string
	if err := json.Unmarshal(raw, &ids); err == nil {
		return uniqueSortedIDs(ids)
	}
	return nil
}

func uniqueSortedIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// ListRemoteGroupModels fetches OpenAI-compatible /v1/models from the group Base URL.
func ListRemoteGroupModels(w http.ResponseWriter, r *http.Request) {
	listRemoteGroupModels(w, r, upstreamModelRecognition.Load())
}

func listRemoteGroupModels(w http.ResponseWriter, r *http.Request, recognition *upstreamModelRecognitionIndex) {
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
	parentID := strings.TrimSpace(mux.Vars(r)["model_provider_id"])
	groupID := strings.TrimSpace(mux.Vars(r)["group_id"])
	if parentID == "" || groupID == "" {
		common.ReplyErr(w, "missing model_provider_id or group_id", http.StatusBadRequest)
		return
	}

	var parent orm.UserModelProvider
	err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", parentID, userID).
		Take(&parent).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "model provider not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query model provider failed", http.StatusInternalServerError)
		return
	}
	if !parent.HasCapability("has_models") {
		common.ReplyErr(w, "this provider does not support models", http.StatusBadRequest)
		return
	}

	var group orm.UserModelProviderGroup
	err = db.WithContext(r.Context()).
		Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, parent.ID, userID).
		Take(&group).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
		return
	}

	// A new keyless custom service can expose its catalog before a connection
	// check. Discovery does not verify it or loosen runtime admission.
	baseURL, apiKey, err := resolveProviderRuntimeCredential(r.Context(), db, group.ID, true)
	if err != nil {
		if errors.Is(err, ErrProviderKeyDisabled) || errors.Is(err, ErrProviderKeyUnverified) {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		} else {
			common.ReplyErr(w, "resolve enabled api key failed", http.StatusInternalServerError)
		}
		return
	}
	listURL, err := modelsListURL(baseURL)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	parsedListURL, err := url.Parse(listURL)
	if err != nil || validateRemoteModelsURL(parsedListURL) != nil {
		common.ReplyErr(w, errRemoteModelsHostNotAllowed.Error(), http.StatusBadRequest)
		return
	}
	if LazyLLMSource(RuntimeProviderName(parent)) == "openrouter" {
		query := parsedListURL.Query()
		query.Set("output_modalities", "all")
		parsedListURL.RawQuery = query.Encode()
		listURL = parsedListURL.String()
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, listURL, nil)
	if err != nil {
		common.ReplyErr(w, "build remote models request failed", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "application/json")
	if LazyLLMSource(RuntimeProviderName(parent)) == "claude" {
		req.Header.Set("anthropic-version", "2023-06-01")
		if apiKey != "" {
			req.Header.Set("x-api-key", apiKey)
		}
	} else if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := remoteModelsHTTPClient.Do(req)
	if err != nil {
		if errors.Is(err, errRemoteModelsHostNotAllowed) {
			common.ReplyErr(w, errRemoteModelsHostNotAllowed.Error(), http.StatusBadRequest)
			return
		}
		common.ReplyErr(w, "list remote models failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		common.ReplyErr(w, fmt.Sprintf("list remote models failed: HTTP %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	entries := parseRemoteModelEntries(body)
	var existing []orm.UserModelProviderGroupModel
	if err := db.WithContext(r.Context()).
		Where("user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL", group.ID, userID).
		Find(&existing).Error; err != nil {
		common.ReplyErr(w, "query group models failed", http.StatusInternalServerError)
		return
	}
	added := make(map[string]struct{}, len(existing))
	for _, row := range existing {
		added[strings.ToLower(strings.TrimSpace(row.Name))] = struct{}{}
	}

	models := make([]remoteGroupModelItem, 0, len(entries))
	for _, entry := range entries {
		item := remoteModelItem(entry, RuntimeProviderName(parent), recognition)
		if _, ok := added[strings.ToLower(item.ID)]; ok {
			item.Added = true
		}
		models = append(models, item)
	}

	common.ReplyOK(w, remoteGroupModelsResponse{URL: listURL, Models: models})
}

func remoteModelItem(entry openaiModelListEntry, providerName string, recognition *upstreamModelRecognitionIndex) remoteGroupModelItem {
	input, output := entry.Architecture.Input, entry.Architecture.Output
	if len(input) == 0 {
		input = entry.InputModalities
	}
	if len(output) == 0 {
		output = entry.OutputModalities
	}
	modelType := classifyUpstreamModel("", input, output)
	if len(output) == 0 {
		declared := ""
		for _, value := range []string{entry.ModelType, entry.TaskType, entry.Task, entry.Type} {
			if declared = strings.TrimSpace(value); declared != "" {
				break
			}
		}
		if declared != "" {
			modelType = classifyUpstreamModel(declared, input, output)
			if modelType == "" {
				// Preserve explicit unknown tasks so downstream consumers do not
				// mistake them for ID-only entries and reclassify from the catalog.
				modelType = normalizedDeclaredModelType(declared)
			}
		} else {
			modelType = classifyUpstreamModel(recognition.modelType(providerName, entry.ID), input, nil)
		}
	}
	item := remoteGroupModelItem{
		ID: entry.ID, Name: entry.ID, ModelType: modelType,
		Input: append([]string{}, input...), Output: append([]string{}, output...),
	}
	hasInputLimit := modelType == "llm" || modelType == "vlm" || modelType == "embed" || modelType == "multimodal_embedding" || modelType == "rerank"
	if entry.ContextLength > 0 && hasInputLimit {
		tokens := formatContextTokens(entry.ContextLength)
		item.MaxInputTokens = &tokens
	} else if hasInputLimit {
		if tokens, ok := lookupMaxInputTokens(entry.ID, modelType); ok {
			item.MaxInputTokens = &tokens
		}
	}
	return item
}
