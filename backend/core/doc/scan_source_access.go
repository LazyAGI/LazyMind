package doc

import (
	"net/http"
	"strings"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

const scanSourceAccessTimeout = 3 * time.Second

type scanSourceAccessBatchRequest struct {
	DatasetIDs []string `json:"dataset_ids"`
	Action     string   `json:"action"`
}

type scanSourceAccessBatchResponse struct {
	Items []scanSourceAccessItem `json:"items"`
}

type scanSourceAccessItem struct {
	DatasetID string `json:"dataset_id"`
	SourceID  string `json:"source_id"`
	Exists    bool   `json:"exists"`
	Allowed   bool   `json:"allowed"`
}

func filterDatasetsByScanSourceAccess(r *http.Request, datasets []orm.Dataset, action string) []orm.Dataset {
	if len(datasets) == 0 || !shouldCheckScanSourceAccess(r) {
		return datasets
	}
	ids := make([]string, 0, len(datasets))
	for _, ds := range datasets {
		ids = append(ids, ds.ID)
	}
	items, err := scanSourceAccessByDataset(r, ids, action)
	if err != nil {
		log.Logger.Warn().Err(err).Msg("scan source access check failed; preserving core dataset ACL result")
		return datasets
	}
	out := make([]orm.Dataset, 0, len(datasets))
	for _, ds := range datasets {
		item, ok := items[ds.ID]
		if ok && item.Exists && !item.Allowed {
			continue
		}
		out = append(out, ds)
	}
	return out
}

func datasetAllowedByScanSource(r *http.Request, datasetID, action string) bool {
	if strings.TrimSpace(datasetID) == "" || !shouldCheckScanSourceAccess(r) {
		return true
	}
	items, err := scanSourceAccessByDataset(r, []string{datasetID}, action)
	if err != nil {
		log.Logger.Warn().Err(err).Str("dataset_id", datasetID).Msg("scan source access check failed; preserving core dataset ACL result")
		return true
	}
	item, ok := items[datasetID]
	return !ok || !item.Exists || item.Allowed
}

func scanSourceAccessByDataset(r *http.Request, datasetIDs []string, action string) (map[string]scanSourceAccessItem, error) {
	out := map[string]scanSourceAccessItem{}
	datasetIDs = uniqueScanSourceDatasetIDs(datasetIDs)
	if len(datasetIDs) == 0 {
		return out, nil
	}
	endpoint := common.JoinURL(common.ScanControlPlaneEndpoint(), "/api/scan/internal/source-access/by-dataset:batch")
	var resp scanSourceAccessBatchResponse
	if err := common.ApiPost(r.Context(), endpoint, scanSourceAccessBatchRequest{
		DatasetIDs: datasetIDs,
		Action:     scanSourceActionForDatasetAction(action),
	}, scanSourceRequestHeaders(r), &resp, scanSourceAccessTimeout); err != nil {
		return nil, err
	}
	for _, item := range resp.Items {
		datasetID := strings.TrimSpace(item.DatasetID)
		if datasetID == "" {
			continue
		}
		out[datasetID] = item
	}
	return out, nil
}

func scanSourceRequestHeaders(r *http.Request) map[string]string {
	headers := map[string]string{}
	if r == nil {
		return headers
	}
	if value := strings.TrimSpace(r.Header.Get("Authorization")); value != "" {
		headers["Authorization"] = value
	}
	if value := store.UserID(r); value != "" {
		headers["X-User-ID"] = value
	}
	if value := strings.TrimSpace(r.Header.Get("X-Tenant-ID")); value != "" {
		headers["X-Tenant-ID"] = value
	}
	if value := strings.TrimSpace(r.Header.Get("X-User-Role")); value != "" {
		headers["X-User-Role"] = value
	}
	return headers
}

func shouldCheckScanSourceAccess(r *http.Request) bool {
	return r != nil && strings.TrimSpace(r.Header.Get("Authorization")) != ""
}

func scanSourceActionForDatasetAction(action string) string {
	switch action {
	case acl.PermissionDatasetWrite, acl.PermissionDatasetUpload, acl.PermWrite, acl.PermUpload:
		return "write"
	default:
		return "read"
	}
}

func uniqueScanSourceDatasetIDs(datasetIDs []string) []string {
	out := make([]string, 0, len(datasetIDs))
	seen := make(map[string]struct{}, len(datasetIDs))
	for _, datasetID := range datasetIDs {
		datasetID = strings.TrimSpace(datasetID)
		if datasetID == "" {
			continue
		}
		if _, ok := seen[datasetID]; ok {
			continue
		}
		seen[datasetID] = struct{}{}
		out = append(out, datasetID)
	}
	return out
}
