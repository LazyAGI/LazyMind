package doc

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

type scanSourceAccessByDatasetBatchRequest struct {
	DatasetIDs []string `json:"dataset_ids"`
	Action     string   `json:"action"`
}

type scanSourceAccessByDatasetBatchResponse struct {
	Items []scanSourceAccessByDatasetItem `json:"items"`
}

type scanSourceAccessByDatasetItem struct {
	DatasetID string `json:"dataset_id"`
	SourceID  string `json:"source_id,omitempty"`
	Exists    bool   `json:"exists"`
	Allowed   bool   `json:"allowed"`
}

func datasetAllowedByScanSource(r *http.Request, datasetID string, action string) bool {
	return datasetAllowedByScanSourceForCaller(r.Context(), datasetCatalogCallerFromRequest(r), datasetID, action)
}

func datasetAllowedByScanSourceForCaller(ctx context.Context, caller DatasetCatalogCaller, datasetID string, action string) bool {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return false
	}
	items, ok := scanSourceAccessByDatasetForCaller(ctx, caller, []string{datasetID}, action)
	if !ok {
		return true
	}
	item, ok := items[datasetID]
	return !ok || !item.Exists || item.Allowed
}

func filterDatasetsByScanSourceAccess(r *http.Request, datasets []orm.Dataset, action string) []orm.Dataset {
	return filterDatasetsByScanSourceAccessForCaller(r.Context(), datasetCatalogCallerFromRequest(r), datasets, action)
}

func filterDatasetsByScanSourceAccessForCaller(ctx context.Context, caller DatasetCatalogCaller, datasets []orm.Dataset, action string) []orm.Dataset {
	if len(datasets) == 0 {
		return datasets
	}
	datasetIDs := make([]string, 0, len(datasets))
	for _, ds := range datasets {
		if id := strings.TrimSpace(ds.ID); id != "" {
			datasetIDs = append(datasetIDs, id)
		}
	}
	items, ok := scanSourceAccessByDatasetForCaller(ctx, caller, datasetIDs, action)
	if !ok {
		return datasets
	}
	out := make([]orm.Dataset, 0, len(datasets))
	for _, ds := range datasets {
		item, ok := items[ds.ID]
		if !ok || !item.Exists || item.Allowed {
			out = append(out, ds)
		}
	}
	return out
}

func scanSourceAccessByDataset(r *http.Request, datasetIDs []string, action string) (map[string]scanSourceAccessByDatasetItem, bool) {
	return scanSourceAccessByDatasetForCaller(r.Context(), datasetCatalogCallerFromRequest(r), datasetIDs, action)
}

func scanSourceAccessByDatasetForCaller(ctx context.Context, caller DatasetCatalogCaller, datasetIDs []string, action string) (map[string]scanSourceAccessByDatasetItem, bool) {
	datasetIDs = uniqueNonEmptyStrings(datasetIDs)
	if len(datasetIDs) == 0 {
		return map[string]scanSourceAccessByDatasetItem{}, true
	}
	scanURL := common.JoinURL(common.ScanControlPlaneEndpoint(), "/api/scan/internal/source-access/by-dataset:batch")
	req := scanSourceAccessByDatasetBatchRequest{
		DatasetIDs: datasetIDs,
		Action:     scanSourceAccessAction(action),
	}
	var resp scanSourceAccessByDatasetBatchResponse
	if err := common.ApiPost(ctx, scanURL, req, scanSourceAccessHeadersForCaller(caller), &resp, 5*time.Second); err != nil {
		log.Logger.Warn().
			Err(err).
			Str("scan_url", scanURL).
			Str("action", req.Action).
			Int("dataset_count", len(datasetIDs)).
			Msg("scan source access check failed")
		return nil, false
	}
	items := make(map[string]scanSourceAccessByDatasetItem, len(resp.Items))
	for _, item := range resp.Items {
		if id := strings.TrimSpace(item.DatasetID); id != "" {
			item.DatasetID = id
			items[id] = item
		}
	}
	return items, true
}

func scanSourceAccessAction(action string) string {
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case strings.ToUpper(acl.PermissionDatasetWrite), strings.ToUpper(acl.PermWrite),
		strings.ToUpper(acl.PermissionDatasetUpload), strings.ToUpper(acl.PermUpload):
		return "write"
	case "DELETE":
		return "delete"
	default:
		return "read"
	}
}

func scanSourceAccessHeaders(r *http.Request) map[string]string {
	return scanSourceAccessHeadersForCaller(datasetCatalogCallerFromRequest(r))
}

func scanSourceAccessHeadersForCaller(caller DatasetCatalogCaller) map[string]string {
	headers := map[string]string{}
	add := func(name, value string) {
		if v := strings.TrimSpace(value); v != "" {
			headers[name] = v
		}
	}
	add("Authorization", caller.Authorization)
	add("X-User-ID", caller.UserID)
	add("X-Tenant-ID", caller.TenantID)
	add("X-User-Role", caller.UserRole)
	if token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")); token != "" {
		headers["X-LazyMind-Internal-Token"] = token
	}
	return headers
}

func datasetCatalogCallerFromRequest(r *http.Request) DatasetCatalogCaller {
	caller := DatasetCatalogCaller{}
	if r == nil {
		return caller
	}
	caller.Authorization = strings.TrimSpace(r.Header.Get("Authorization"))
	caller.UserID = strings.TrimSpace(r.Header.Get("X-User-ID"))
	if caller.UserID == "" {
		if userID := strings.TrimSpace(store.UserID(r)); userID != "" {
			caller.UserID = userID
		}
	}
	caller.TenantID = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	caller.UserRole = strings.TrimSpace(r.Header.Get("X-User-Role"))
	return caller
}

func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
