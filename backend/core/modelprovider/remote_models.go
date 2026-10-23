package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
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
	"lazymind/core/modelsettings"
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
	FeatureSupport *modelsettings.FeatureFlags `json:"feature_support,omitempty"`
	ModelSettings  *ModelSettings              `json:"model_settings,omitempty"`
	ID             string                      `json:"id"`
	Name           string                      `json:"name"`
	ModelType      string                      `json:"model_type"`
	SubTypes       []string                    `json:"sub_types,omitempty"`
	Input          []string                    `json:"input"`
	Output         []string                    `json:"output"`
	MaxInputTokens *string                     `json:"max_input_tokens,omitempty"`
	Added          bool                        `json:"added"`
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
	FeatureSupport      *modelsettings.FeatureFlags `json:"feature_support"`
	ToolCall            *bool                       `json:"tool_call"`
	Reasoning           *bool                       `json:"reasoning"`
	SupportedParameters json.RawMessage             `json:"supported_parameters"`
	ID                  string                      `json:"id"`
	Name                string                      `json:"name"`
	Model               string                      `json:"model"`
	Root                json.RawMessage             `json:"root"`
	ModelType           string                      `json:"model_type"`
	TaskType            string                      `json:"task_type"`
	Task                string                      `json:"task"`
	Type                string                      `json:"type"`
	ContextLength       int                         `json:"context_length"`
	MaxModelLen         int                         `json:"max_model_len"`
	InputModalities     []string                    `json:"input_modalities"`
	OutputModalities    []string                    `json:"output_modalities"`
	// DiscoverySubTypes is supplied by trusted category-specific requests, never
	// accepted from an arbitrary compatible endpoint's response body.
	DiscoverySubTypes []string `json:"-"`
	Architecture      struct {
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

func modelsListURL(providerName, baseURL string) (string, error) {
	listURL, err := ProviderModelsListURL(providerName, baseURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(listURL)
	if err != nil {
		return "", err
	}
	if err := validateRemoteModelsURL(parsed); err != nil {
		return "", err
	}
	return listURL, nil
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
	runtimeConfig, err := ResolveProviderRuntimeConfiguration(r.Context(), db, group.ID)
	if err != nil {
		if errors.Is(err, ErrProviderKeyDisabled) || errors.Is(err, ErrProviderKeyUnverified) {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		} else {
			common.ReplyErr(w, "resolve enabled api key failed", http.StatusInternalServerError)
		}
		return
	}
	baseURL, apiKey := runtimeConfig.BaseURL, runtimeConfig.APIKey
	listURL, err := modelsListURL(RuntimeProviderName(parent), baseURL)
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

	discoveryContext, cancelDiscovery := context.WithTimeout(r.Context(), remoteModelsTimeout)
	defer cancelDiscovery()
	req, err := http.NewRequestWithContext(discoveryContext, http.MethodGet, listURL, nil)
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
	applyProviderRequestAdvanced(req, runtimeConfig.Advanced)

	entries, err := fetchRemoteModelEntries(req)
	if err != nil {
		replyRemoteModelsFailure(w, err)
		return
	}
	if isSiliconFlowSupplier(r.Context(), db, parent) {
		entries, err = listSiliconFlowModelCategories(req, entries)
		if err != nil {
			replyRemoteModelsFailure(w, err)
			return
		}
	}
	var existing []orm.UserModelProviderGroupModel
	if err := db.WithContext(r.Context()).
		Where("user_model_provider_group_id = ? AND create_user_id = ? AND deleted_at IS NULL", group.ID, userID).
		Find(&existing).Error; err != nil {
		common.ReplyErr(w, "query group models failed", http.StatusInternalServerError)
		return
	}
	added := make(map[string]orm.UserModelProviderGroupModel, len(existing))
	for _, row := range existing {
		added[strings.ToLower(strings.TrimSpace(row.Name))] = row
	}

	metadataProvider := RuntimeProviderName(parent)
	if isVLLMSupplier(r.Context(), db, parent) {
		metadataProvider = "VLLM"
	} else if isSiliconFlowSupplier(r.Context(), db, parent) {
		metadataProvider = "SiliconFlow"
	}
	models := make([]remoteGroupModelItem, 0, len(entries))
	for _, entry := range entries {
		item := remoteModelItem(entry, metadataProvider, recognition)
		if row, ok := added[strings.ToLower(item.ID)]; ok {
			item.Added = true
			item.ModelSettings = modelSettingsResponse(row.ModelSettings)
			if item.ModelSettings != nil && isChatTechnicalType(row.ModelType) {
				inheritMissingModelFeatureSupport(item.ModelSettings, item.FeatureSupport)
				item.FeatureSupport = item.ModelSettings.FeatureSupport
			}
		}
		models = append(models, item)
	}

	common.ReplyOK(w, remoteGroupModelsResponse{URL: listURL, Models: models})
}

func replyRemoteModelsFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, errRemoteModelsHostNotAllowed) {
		common.ReplyErr(w, errRemoteModelsHostNotAllowed.Error(), http.StatusBadRequest)
		return
	}
	reason := "connection_error"
	var failure interface{ FailureCode() string }
	if errors.As(err, &failure) {
		reason = safeConnectionReason(failure.FailureCode())
	}
	common.ReplyErrWithData(w, "list remote models failed", map[string]string{"reason": reason}, http.StatusBadGateway)
}

func remoteModelItem(entry openaiModelListEntry, providerName string, recognition *upstreamModelRecognitionIndex) remoteGroupModelItem {
	input, output := entry.Architecture.Input, entry.Architecture.Output
	if len(input) == 0 {
		input = entry.InputModalities
	}
	if len(output) == 0 {
		output = entry.OutputModalities
	}
	isVLLM := normalizeProviderName(providerName) == "vllm"
	declared := ""
	for _, value := range []string{entry.ModelType, entry.TaskType, entry.Task, entry.Type} {
		if declared = strings.TrimSpace(value); declared != "" {
			break
		}
	}
	catalogType := ""
	if declared == "" && len(output) == 0 {
		candidate, found := recognition.lookupExact(providerName, entry.ID)
		root := ""
		if !found && isVLLM {
			root = vllmLoadedModelIdentity(entry.Root)
			if root != "" {
				candidate, found = recognition.lookupExact(providerName, root)
			}
		}
		fuzzy := !found
		if !found && root != "" {
			// Loaded weights are stronger evidence than a normalized served alias.
			candidate, found = recognition.lookupFuzzy(root)
		}
		if !found {
			candidate, _ = recognition.lookupFuzzy(entry.ID)
		}
		catalogType = candidate.resolvedType()
		if catalogType != "" && len(entry.DiscoverySubTypes) == 0 && (isVLLM || fuzzy) {
			if len(input) == 0 {
				input = candidate.input
			}
			output = candidate.output
		}
	}
	modelType := classifyUpstreamModel(catalogType, input, output)
	if len(output) == 0 && declared != "" {
		modelType = classifyUpstreamModel(declared, input, output)
		if modelType == "" {
			// Preserve explicit unknown tasks so downstream consumers do not
			// mistake them for ID-only entries and reclassify from the catalog.
			modelType = normalizedDeclaredModelType(declared)
		}
	}
	item := remoteGroupModelItem{
		ID: entry.ID, Name: entry.ID, ModelType: modelType,
		Input: append([]string{}, input...), Output: append([]string{}, output...),
	}
	if len(entry.DiscoverySubTypes) > 0 {
		item.SubTypes = append([]string(nil), entry.DiscoverySubTypes...)
		modelType = siliconFlowDiscoveredModelType(entry, input, output, recognition.modelType(providerName, entry.ID))
		item.ModelType = modelType
	}
	if isChatTechnicalType(item.ModelType) {
		item.FeatureSupport = remoteModelFeatureSupport(entry, providerName, recognition)
	}
	hasInputLimit := modelType == "llm" || modelType == "vlm" || modelType == "embed" || modelType == "multimodal_embedding" || modelType == "rerank"
	contextLength := entry.ContextLength
	if isVLLM && entry.MaxModelLen > 0 {
		contextLength = entry.MaxModelLen
	}
	if contextLength > 0 && (hasInputLimit || isVLLM) {
		tokens := formatContextTokens(contextLength)
		item.MaxInputTokens = &tokens
	} else if hasInputLimit {
		if tokens, ok := lookupMaxInputTokens(entry.ID, modelType); ok {
			item.MaxInputTokens = &tokens
		}
	}
	return item
}

func remoteModelFeatureSupport(entry openaiModelListEntry, providerName string, recognition *upstreamModelRecognitionIndex) *modelsettings.FeatureFlags {
	flags := modelsettings.FeatureFlags{}
	if known := recognition.featureSupport(providerName, entry.ID); known != nil {
		flags = *known
	}
	if normalizeProviderName(providerName) == "vllm" {
		if root := vllmLoadedModelIdentity(entry.Root); root != "" {
			if _, found := recognition.lookupExact(providerName, entry.ID); !found {
				if known := recognition.featureSupport(providerName, root); known != nil {
					flags = *known
				}
			}
		}
	}
	if entry.Architecture.Input != nil || entry.InputModalities != nil {
		input := entry.Architecture.Input
		if input == nil {
			input = entry.InputModalities
		}
		modalities := modalitySet(input)
		vision := modalities["image"] || modalities["video"]
		flags.Vision = &vision
	}
	var parameters []any
	if raw := strings.TrimSpace(string(entry.SupportedParameters)); strings.HasPrefix(raw, "[") && json.Unmarshal(entry.SupportedParameters, &parameters) == nil {
		hasTools, hasReasoning, valid := false, false, true
		for _, value := range parameters {
			parameter, ok := value.(string)
			if !ok {
				valid = false
				break
			}
			hasTools = hasTools || parameter == "tools"
			hasReasoning = hasReasoning || parameter == "reasoning" || parameter == "include_reasoning"
		}
		if valid {
			flags.ToolCalling, flags.Reasoning = &hasTools, &hasReasoning
		}
	}
	if entry.ToolCall != nil {
		flags.ToolCalling = entry.ToolCall
	}
	if entry.Reasoning != nil {
		flags.Reasoning = entry.Reasoning
	}
	if entry.FeatureSupport != nil {
		if entry.FeatureSupport.Vision != nil {
			flags.Vision = entry.FeatureSupport.Vision
		}
		if entry.FeatureSupport.ToolCalling != nil {
			flags.ToolCalling = entry.FeatureSupport.ToolCalling
		}
		if entry.FeatureSupport.Reasoning != nil {
			flags.Reasoning = entry.FeatureSupport.Reasoning
		}
	}
	if flags.Vision == nil && flags.ToolCalling == nil && flags.Reasoning == nil {
		return nil
	}
	return &flags
}

// vLLM's root is the loaded model path, not its public served-model alias.
// Treat it only as identity text: never open it, fetch it, or return it to clients.
func vllmLoadedModelIdentity(raw json.RawMessage) string {
	var root string
	if json.Unmarshal(raw, &root) != nil {
		return ""
	}
	root = strings.TrimSpace(root)
	if strings.ContainsAny(root, "?#\x00\r\n\\") || strings.Contains(root, "://") {
		return ""
	}
	return strings.TrimRight(root, "/")
}
