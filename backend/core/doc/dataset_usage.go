package doc

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

const (
	datasetUsageInternalTokenHeader = "X-LazyMind-Internal-Token"
	datasetUsageInternalTokenEnv    = "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"
	maxDatasetUsageBatchDatasetIDs  = 500
)

type datasetUsageBatchRequest struct {
	UserID     string   `json:"user_id"`
	DatasetIDs []string `json:"dataset_ids"`
}

type datasetUsageItem struct {
	UsageCount int64      `json:"usage_count"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

type datasetUsageBatchResponse struct {
	UsageMap map[string]datasetUsageItem `json:"usage_map"`
}

// InternalBatchDatasetUsage returns per-user dataset usage counters for the scan
// control plane. It is a service-to-service endpoint protected by the shared
// internal token; the caller is trusted to pass the target user_id explicitly.
func InternalBatchDatasetUsage(w http.ResponseWriter, r *http.Request) {
	if !requireDatasetUsageInternalToken(w, r) {
		return
	}
	var req datasetUsageBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		common.ReplyErr(w, "user_id is required", http.StatusBadRequest)
		return
	}
	datasetIDs := uniqueNonEmptyStrings(req.DatasetIDs)
	if len(datasetIDs) > maxDatasetUsageBatchDatasetIDs {
		common.ReplyErr(w, "too many dataset_ids", http.StatusBadRequest)
		return
	}

	usageMap := make(map[string]datasetUsageItem, len(datasetIDs))
	for _, datasetID := range datasetIDs {
		usageMap[datasetID] = datasetUsageItem{}
	}
	if len(datasetIDs) > 0 {
		var states []orm.DatasetUserState
		if err := corestore.DB().
			WithContext(r.Context()).
			Where("create_user_id = ? AND dataset_id IN ? AND deleted_at IS NULL", userID, datasetIDs).
			Find(&states).Error; err != nil {
			common.ReplyErr(w, "query dataset usage failed", http.StatusInternalServerError)
			return
		}
		for _, state := range states {
			usageMap[state.DatasetID] = datasetUsageItem{
				UsageCount: state.UsageCount,
				LastUsedAt: state.LastUsedAt,
			}
		}
	}

	common.ReplyJSON(w, datasetUsageBatchResponse{UsageMap: usageMap})
}

func requireDatasetUsageInternalToken(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv(datasetUsageInternalTokenEnv))
	provided := strings.TrimSpace(r.Header.Get(datasetUsageInternalTokenHeader))
	if expected == "" || provided == "" ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		common.ReplyErr(w, "internal token required", http.StatusUnauthorized)
		return false
	}
	return true
}
