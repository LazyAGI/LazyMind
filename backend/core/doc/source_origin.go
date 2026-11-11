package doc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

type datasetSourceOrigin struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Basis   string `json:"basis"`
}

func readDatasetSourceOrigin(info json.RawMessage) datasetSourceOrigin {
	var value struct {
		Origin datasetSourceOrigin `json:"source_origin"`
	}
	if json.Unmarshal(info, &value) != nil || value.Origin.Version != 1 ||
		!slices.Contains([]string{"none", "scan"}, value.Origin.Kind) ||
		!slices.Contains([]string{"created", "learned"}, value.Origin.Basis) {
		return datasetSourceOrigin{}
	}
	return value.Origin
}

func mergeDatasetSourceOrigin(info json.RawMessage, kind, basis string) (json.RawMessage, error) {
	values := map[string]json.RawMessage{}
	if len(info) > 0 {
		if err := json.Unmarshal(info, &values); err != nil {
			return nil, err
		}
	}
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	values["source_origin"] = mustJSON(datasetSourceOrigin{Version: 1, Kind: kind, Basis: basis})
	return json.Marshal(values)
}

func createdDatasetSourceInfo(scan bool) json.RawMessage {
	kind := "none"
	if scan {
		kind = "scan"
	}
	info, _ := mergeDatasetSourceOrigin(nil, kind, "created")
	return info
}

// Only creation provenance is learned; account, tenant and ACL checks stay live.
func learnDatasetSourceOrigin(ctx context.Context, db *gorm.DB, ds *orm.Dataset, item scanSourceAccessByDatasetItem) {
	if readDatasetSourceOrigin(ds.DatasetInfo).Kind != "" || !item.learnable {
		return
	}
	kind := "none"
	if item.Exists {
		kind = "scan"
	}
	info, err := mergeDatasetSourceOrigin(ds.DatasetInfo, kind, "learned")
	if err != nil {
		return
	}
	result := db.WithContext(ctx).Model(&orm.Dataset{}).
		Where("id = ? AND resource_version = ? AND deleted_at IS NULL", ds.ID, ds.ResourceVersion).
		Updates(map[string]any{"dataset_info": info, "resource_version": gorm.Expr("resource_version + 1")})
	if result.Error != nil {
		log.Logger.Warn().Err(result.Error).Str("dataset_id", ds.ID).Msg("learn dataset source origin failed")
		return
	}
	if result.RowsAffected == 1 {
		ds.DatasetInfo, ds.ResourceVersion = info, ds.ResourceVersion+1
	}
}

func ordinarySourceDatasetIDs(ctx context.Context, ids []string) map[string]bool {
	ordinary := map[string]bool{}
	if store.DB() == nil || len(ids) == 0 {
		return ordinary
	}
	var rows []orm.Dataset
	if store.DB().WithContext(ctx).Select("id", "dataset_info").Where("id IN ? AND deleted_at IS NULL", ids).Find(&rows).Error == nil {
		for _, row := range rows {
			if readDatasetSourceOrigin(row.DatasetInfo).Kind == "none" {
				ordinary[row.ID] = true
			}
		}
	}
	return ordinary
}

func scanDatasetCreation(r *http.Request, tags []string, key string) (bool, error) {
	if hint := r.Header.Get("X-LazyMind-Source-Origin"); hint != "" {
		expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
		provided := strings.TrimSpace(r.Header.Get("X-LazyMind-Internal-Token"))
		if hint != "scan" || expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			return false, resourceErr("FORBIDDEN", "invalid source origin credentials")
		}
		return true, nil
	}
	// Legacy hints can only retain source checks, never grant the ordinary shortcut.
	return slices.Contains(tags, "scan") || strings.HasPrefix(key, "create-source:"), nil
}
