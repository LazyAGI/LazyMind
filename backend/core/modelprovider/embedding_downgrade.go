package modelprovider

import (
	"net/http"
	"strings"
	"time"

	"lazymind/core/common/orm"

	"gorm.io/gorm"
)

const confirmIndexedDowngradeParam = "confirm_indexed_downgrade"

func isTextEmbeddingModelType(modelType string) bool {
	switch strings.ToLower(strings.TrimSpace(modelType)) {
	case "embed", "embedding", "embed_main":
		return true
	default:
		return false
	}
}

func indexedDowngradeConfirmed(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get(confirmIndexedDowngradeParam)), "true")
}

// downgradeIndexedDatasets preserves all parsed, chunked, and vector artifacts.
// It only lowers the owner's declared capability ceiling before an embedding
// model is removed.
func downgradeIndexedDatasets(tx *gorm.DB, userID string, now time.Time) (int64, error) {
	result := tx.Model(&orm.Dataset{}).
		Where("create_user_id = ? AND processing_level = ? AND deleted_at IS NULL", userID, "indexed").
		Updates(map[string]any{
			"processing_level":    "chunked",
			"processing_revision": gorm.Expr("processing_revision + ?", 1),
			"transition_status":   "idle",
			"updated_at":          now,
		})
	return result.RowsAffected, result.Error
}
