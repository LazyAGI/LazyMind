package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/datasource"
)

const (
	defaultScanTenantID       = "tenant-demo"
	localFSScanPageSize       = 200
	localFSScanMaxPages       = 100
	localFSScanRequestTimeout = 10 * time.Second
)

var localFSScanHTTPClient = &http.Client{Timeout: localFSScanRequestTimeout}

type scanSourceListResponse struct {
	Items []scanSourceListItem `json:"items"`
	Total int                  `json:"total"`
}

type scanSourceListItem struct {
	SourceID string `json:"source_id"`
	Status   string `json:"status"`
}

type scanGetSourceResponse struct {
	Bindings []scanSourceBinding `json:"bindings"`
}

type scanSourceBinding struct {
	ConnectorType string `json:"connector_type"`
	TargetType    string `json:"target_type"`
	TargetRef     string `json:"target_ref"`
	Status        string `json:"status"`
	DeletedAt     any    `json:"deleted_at"`
}

func applyLocalFSPathsForChat(ctx context.Context, r *http.Request, db *gorm.DB, userID string, reqBody map[string]any) error {
	enabled, err := datasource.LoadLocalFSChatEnabled(ctx, db, userID)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	paths, err := loadLocalFSPathsForChat(ctx, r, userID)
	if err != nil {
		return err
	}
	reqBody["localfs_paths"] = paths
	return nil
}

func loadLocalFSPathsForChat(ctx context.Context, r *http.Request, userID string) ([]string, error) {
	sourceIDs, err := listReadableScanSourceIDs(ctx, r, userID)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	seen := map[string]struct{}{}
	for _, sourceID := range sourceIDs {
		bindings, err := getScanSourceBindings(ctx, r, userID, sourceID)
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			path := strings.TrimSpace(binding.TargetRef)
			if path == "" || !isActiveLocalFSBinding(binding) {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func listReadableScanSourceIDs(ctx context.Context, r *http.Request, userID string) ([]string, error) {
	sourceIDs := make([]string, 0)
	for page := 1; page <= localFSScanMaxPages; page += 1 {
		endpoint, err := scanControlPlaneURL("/api/scan/sources")
		if err != nil {
			return nil, err
		}
		query := endpoint.Query()
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(localFSScanPageSize))
		query.Set("status", "ACTIVE")
		endpoint.RawQuery = query.Encode()

		var payload scanSourceListResponse
		if err := doScanControlPlaneJSON(ctx, r, userID, endpoint.String(), &payload); err != nil {
			return nil, err
		}
		for _, item := range payload.Items {
			sourceID := strings.TrimSpace(item.SourceID)
			if sourceID == "" || !isActiveStatus(item.Status) {
				continue
			}
			sourceIDs = append(sourceIDs, sourceID)
		}
		if len(payload.Items) == 0 || page*localFSScanPageSize >= payload.Total {
			break
		}
	}
	return sourceIDs, nil
}

func getScanSourceBindings(ctx context.Context, r *http.Request, userID, sourceID string) ([]scanSourceBinding, error) {
	endpoint, err := scanControlPlaneURL("/api/scan/sources/" + url.PathEscape(sourceID))
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("include_bindings", "true")
	query.Set("include_summary", "false")
	endpoint.RawQuery = query.Encode()

	var payload scanGetSourceResponse
	if err := doScanControlPlaneJSON(ctx, r, userID, endpoint.String(), &payload); err != nil {
		return nil, err
	}
	return payload.Bindings, nil
}

func scanControlPlaneURL(path string) (*url.URL, error) {
	base := strings.TrimRight(common.ScanControlPlaneEndpoint(), "/")
	endpoint, err := url.Parse(base + path)
	if err != nil {
		return nil, err
	}
	return endpoint, nil
}

func doScanControlPlaneJSON(ctx context.Context, original *http.Request, userID, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-User-ID", strings.TrimSpace(userID))
	req.Header.Set("X-Tenant-ID", scanTenantIDFromRequest(original))
	if role := strings.TrimSpace(original.Header.Get("X-User-Role")); role != "" {
		req.Header.Set("X-User-Role", role)
	}
	if auth := strings.TrimSpace(original.Header.Get("Authorization")); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := localFSScanHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("scan-control-plane request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func scanTenantIDFromRequest(r *http.Request) string {
	if tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID")); tenantID != "" {
		return tenantID
	}
	return defaultScanTenantID
}

func isActiveLocalFSBinding(binding scanSourceBinding) bool {
	if binding.DeletedAt != nil || !isActiveStatus(binding.Status) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(binding.ConnectorType), "local_fs") ||
		strings.EqualFold(strings.TrimSpace(binding.TargetType), "local_path")
}

func isActiveStatus(status string) bool {
	status = strings.ToUpper(strings.TrimSpace(status))
	return status == "" || status == "ACTIVE"
}
