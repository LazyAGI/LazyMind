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
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	ModelType      string  `json:"model_type"`
	MaxInputTokens *string `json:"max_input_tokens,omitempty"`
	Added          bool    `json:"added"`
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
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model"`
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

func inferRemoteModelType(name string) string {
	for _, modelType := range []string{"llm", "vlm", "embed"} {
		if _, ok := lookupMaxInputTokens(name, modelType); ok {
			return modelType
		}
	}
	return ""
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

	apiKey, err := apiKeyForGroup(db.WithContext(r.Context()), &group)
	if err != nil {
		common.ReplyErr(w, "decode api key failed", http.StatusInternalServerError)
		return
	}
	keys := splitAPIKeys(apiKey)
	if len(keys) > 0 {
		apiKey = keys[0]
	}

	listURL, err := modelsListURL(group.BaseURL)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	parsedListURL, err := url.Parse(listURL)
	if err != nil || validateRemoteModelsURL(parsedListURL) != nil {
		common.ReplyErr(w, errRemoteModelsHostNotAllowed.Error(), http.StatusBadRequest)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, listURL, nil)
	if err != nil {
		common.ReplyErr(w, "build remote models request failed", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
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

	ids := parseRemoteModelIDs(body)
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

	models := make([]remoteGroupModelItem, 0, len(ids))
	for _, id := range ids {
		modelType := inferRemoteModelType(id)
		item := remoteGroupModelItem{
			ID:        id,
			Name:      id,
			ModelType: modelType,
			Added:     false,
		}
		if _, ok := added[strings.ToLower(id)]; ok {
			item.Added = true
		}
		if tokens, ok := lookupMaxInputTokens(id, modelType); ok {
			item.MaxInputTokens = &tokens
		}
		models = append(models, item)
	}

	common.ReplyOK(w, remoteGroupModelsResponse{URL: listURL, Models: models})
}
