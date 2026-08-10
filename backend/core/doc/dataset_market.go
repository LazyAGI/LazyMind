package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	corestore "lazymind/core/store"
)

// defaultMarketAlgoID is the algorithm bound to personal datasets created by
// official knowledge base installs. It matches the system-wide default used by
// the manual create flow and scan-control-plane cloud sync (general_algo).
const defaultMarketAlgoID = "general_algo"

const defaultMarketAlgoName = "General"

// MarketInstallJobType is the async job type of the official knowledge base
// install pipeline. It is the single source used both when enqueueing installs
// (knowledge_market package) and when resetting install jobs after an
// uninstall, so the kb_install:{item}:{user} idempotency key stays in sync.
const MarketInstallJobType = "knowledge_market_install"

// MarketUpdateJobType is the async job type of a single official knowledge
// base update; MarketUpdateAllJobType is the one-click batch that only checks
// every installed item and spawns per-item update jobs. Both are part of the
// in-flight conflict checks shared by install/update/uninstall.
const (
	MarketUpdateJobType    = "knowledge_market_update"
	MarketUpdateAllJobType = "knowledge_market_update_all"
)

// activeMarketJobStatuses are the async job statuses that hold an in-flight
// lock on a knowledge market item for the current user.
var activeMarketJobStatuses = []string{"pending", "running"}

// batchCheckInstalledMarketDatasets reports which of the given dataset IDs are
// personal datasets created by the current user's official knowledge base
// installs. The install row's dataset_id is written as soon as the dataset is
// created (importing stage) and is kept on later states, so any residual
// dataset is attributed to the official source.
func batchCheckInstalledMarketDatasets(ctx context.Context, userID string, datasetIDs []string) map[string]bool {
	result := make(map[string]bool, len(datasetIDs))
	if strings.TrimSpace(userID) == "" || len(datasetIDs) == 0 {
		return result
	}
	want := make(map[string]struct{}, len(datasetIDs))
	for _, id := range datasetIDs {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = struct{}{}
		}
	}
	if len(want) == 0 {
		return result
	}
	db := corestore.DB()
	if db == nil {
		return result
	}
	var rows []struct {
		DatasetID string `gorm:"column:dataset_id"`
	}
	if err := db.WithContext(ctx).
		Model(&orm.KnowledgeMarketInstall{}).
		Select("dataset_id").
		Where("user_id = ? AND dataset_id != ''", userID).
		Find(&rows).Error; err != nil {
		log.Logger.Warn().Err(err).Str("user_id", userID).Msg("query market installs failed")
		return result
	}
	for _, row := range rows {
		if _, ok := want[row.DatasetID]; ok {
			result[row.DatasetID] = true
		}
	}
	return result
}

// findMarketInstallByDataset returns the market install row linking the given
// user's dataset to an official knowledge base, plus whether the dataset is an
// official install at all. The knowledge_market_installs row is the single
// source of truth for official installs (datasets carry no source column), so
// it is also the basis for isolating the official uninstall path from regular
// dataset deletion.
func findMarketInstallByDataset(ctx context.Context, db *gorm.DB, userID, datasetID string) (*orm.KnowledgeMarketInstall, bool, error) {
	userID = strings.TrimSpace(userID)
	datasetID = strings.TrimSpace(datasetID)
	if userID == "" || datasetID == "" {
		return nil, false, nil
	}
	var row orm.KnowledgeMarketInstall
	err := db.WithContext(ctx).
		Where("user_id = ? AND dataset_id = ?", userID, datasetID).
		Take(&row).Error
	if isRecordNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("query market install failed: %w", err)
	}
	return &row, true, nil
}

// HasActiveMarketJob reports whether the user has any in-flight market job
// touching the given item: an install/update of the item itself, or a
// one-click update-all batch scoped to the user. It is the single source for
// the 409 conflict checks (install/update/uninstall) so a stale install_state
// never blocks recovery: when no job is actually running the item is safe to
// delete and reinstall.
func HasActiveMarketJob(ctx context.Context, db *gorm.DB, userID, marketItemID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	marketItemID = strings.TrimSpace(marketItemID)
	if userID == "" || marketItemID == "" {
		return false, nil
	}
	var count int64
	err := db.WithContext(ctx).
		Model(&orm.AsyncJob{}).
		Where("create_user_id = ? AND status IN ? AND ((job_type IN ? AND resource_id = ?) OR (job_type = ? AND resource_id = ?))",
			userID,
			activeMarketJobStatuses,
			[]string{MarketInstallJobType, MarketUpdateJobType},
			marketItemID,
			MarketUpdateAllJobType,
			userID,
		).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("query active market jobs failed: %w", err)
	}
	return count > 0, nil
}

// HasActiveMarketBatch reports whether the user already has a running
// one-click update-all batch; used to reject a second batch with 409.
func HasActiveMarketBatch(ctx context.Context, db *gorm.DB, userID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, nil
	}
	var count int64
	err := db.WithContext(ctx).
		Model(&orm.AsyncJob{}).
		Where("job_type = ? AND resource_id = ? AND create_user_id = ? AND status IN ?",
			MarketUpdateAllJobType, userID, userID, activeMarketJobStatuses,
		).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("query active market update batch failed: %w", err)
	}
	return count > 0, nil
}

// resetMarketInstallInTx clears the install record of an official knowledge
// base after its personal dataset has been deleted (uninstall): it removes the
// install row and every install job of that item so the marketplace shows the
// item as not installed and a later install starts from a clean state (the
// kb_install:{item}:{user} idempotency key is released). It must run inside
// the same transaction that soft-deletes the dataset so uninstall is atomic.
func resetMarketInstallInTx(ctx context.Context, tx *gorm.DB, userID, marketItemID string) error {
	userID = strings.TrimSpace(userID)
	marketItemID = strings.TrimSpace(marketItemID)
	if userID == "" || marketItemID == "" {
		return nil
	}
	if err := tx.WithContext(ctx).
		Where("market_item_id = ? AND user_id = ?", marketItemID, userID).
		Delete(&orm.KnowledgeMarketInstall{}).Error; err != nil {
		return fmt.Errorf("delete market install failed: %w", err)
	}
	if err := tx.WithContext(ctx).
		Where("job_type IN ? AND resource_id = ? AND create_user_id = ?",
			[]string{MarketInstallJobType, MarketUpdateJobType}, marketItemID, userID).
		Delete(&orm.AsyncJob{}).Error; err != nil {
		return fmt.Errorf("delete market install/update jobs failed: %w", err)
	}
	log.Logger.Info().
		Str("market_item_id", marketItemID).
		Str("user_id", userID).
		Msg("market install reset after dataset delete")
	return nil
}

// DeleteMarketDataset removes a personal dataset created by a failed official
// knowledge base install: it deletes the KB on the algo service and
// soft-deletes the dataset row so the residual dataset no longer appears in
// the user's knowledge base list. It is a no-op when the dataset is unknown
// or already deleted.
func DeleteMarketDataset(ctx context.Context, userID, datasetID string) error {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil
	}
	db := corestore.DB()
	if db == nil {
		return fmt.Errorf("store not initialized")
	}
	var ds orm.Dataset
	if err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).Take(&ds).Error; err != nil {
		if isRecordNotFound(err) {
			return nil
		}
		return fmt.Errorf("query dataset failed: %w", err)
	}
	if userID != "" && ds.CreateUserID != userID {
		return fmt.Errorf("dataset %s does not belong to user %s", datasetID, userID)
	}

	// Delete the KB on the algo service (mirrors DeleteDataset).
	kbID := strings.TrimSpace(ds.KbID)
	if kbID == "" {
		kbID = ds.ID
	}
	kbURL := common.JoinURL(common.AlgoServiceEndpoint(), "/v1/kbs/"+kbID)
	if err := common.ApiDelete(ctx, kbURL, nil, nil, 10*time.Second); err != nil {
		return fmt.Errorf("kb service delete failed: %w", err)
	}

	// Soft-delete the dataset row and clean references.
	now := time.Now().UTC()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ds.DeletedAt = &now
		ds.UpdatedAt = now
		if err := tx.Save(&ds).Error; err != nil {
			return err
		}
		if err := cleanupEvalSetDatasetReferences(ctx, tx, datasetID, now); err != nil {
			return err
		}
		return tx.
			Where("create_user_id = ? AND dataset_id = ?", userID, datasetID).
			Delete(&orm.DefaultDataset{}).Error
	}); err != nil {
		return fmt.Errorf("delete dataset failed: %w", err)
	}
	log.Logger.Info().
		Str("dataset_id", datasetID).
		Str("user_id", userID).
		Msg("residual market dataset deleted after failed install")
	return nil
}

// marketKBDisplayName builds the algo-side KB display name for an official
// knowledge base install. It appends the unique dataset id so every install
// gets a distinct name: the algo service keeps a global unique index on
// display_name and soft-deletes KB rows asynchronously on uninstall, so a
// reinstall with the plain name would collide with the residual row. The
// core-side display_name (what the user sees) stays unchanged.
func marketKBDisplayName(userID, displayName, datasetID string) string {
	return algoDatasetDisplayName(userID, displayName) + "__" + datasetID
}

// CreateMarketDataset creates a personal dataset for an installed official
// knowledge base. It mirrors the CreateDataset handler flow (algo KB creation,
// dataset row and ACL memberships) without an HTTP request. A non-empty
// datasetID reuses a previously created dataset (retry after partial failure).
func CreateMarketDataset(ctx context.Context, userID, userName, datasetID, displayName, description string, tags []string) (*orm.Dataset, error) {
	displayName = strings.TrimSpace(displayName)
	userID = strings.TrimSpace(userID)
	if displayName == "" || userID == "" {
		return nil, fmt.Errorf("display_name and user are required")
	}
	if hasReservedDatasetDisplayNamePrefix(displayName) {
		return nil, fmt.Errorf("dataset name uses reserved prefix")
	}

	db := corestore.DB()
	if db == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	// Reuse the dataset recorded by a previous partial attempt when it exists.
	if datasetID != "" {
		var existing orm.Dataset
		err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).Take(&existing).Error
		if err == nil {
			return &existing, nil
		}
		if !isRecordNotFound(err) {
			return nil, fmt.Errorf("query dataset failed: %w", err)
		}
	}
	if datasetID == "" {
		datasetID = newDatasetID()
	}

	var existed int64
	if err := db.WithContext(ctx).
		Model(&orm.Dataset{}).
		Where("create_user_id = ? AND display_name = ? AND deleted_at IS NULL", userID, displayName).
		Count(&existed).Error; err != nil {
		return nil, fmt.Errorf("query datasets failed: %w", err)
	}
	if existed > 0 {
		return nil, fmt.Errorf("dataset name already exists")
	}

	// 1) Create the KB on the algo service (same contract as the handler).
	kbURL := common.JoinURL(common.AlgoServiceEndpoint(), "/v1/kbs")
	req := kbCreateRequest{
		KbID:        datasetID,
		DisplayName: marketKBDisplayName(userID, displayName, datasetID),
		OwnerID:     userID,
		AlgoID:      defaultMarketAlgoID,
		Meta:        map[string]any{"tags": tags},
	}
	if description != "" {
		req.Description = &description
	}
	var kbResp map[string]any
	if err := common.ApiPost(ctx, kbURL, req, nil, &kbResp, 10*time.Second); err != nil {
		return nil, fmt.Errorf("kb service create failed: %w", err)
	}
	kbID := datasetID
	if v, ok := kbResp["kb_id"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			kbID = strings.TrimSpace(s)
		}
	}
	if v, ok := kbResp["data"]; ok && kbID == datasetID {
		if m, ok := v.(map[string]any); ok {
			if vv, ok := m["kb_id"]; ok {
				if s, ok := vv.(string); ok && strings.TrimSpace(s) != "" {
					kbID = strings.TrimSpace(s)
				}
			}
		}
	}

	// 2) Persist the dataset row.
	now := time.Now().UTC()
	parsers := fetchParsersByAlgoID(ctx, defaultMarketAlgoID)
	extBytes, _ := json.Marshal(map[string]any{
		"tags":      tags,
		"algo_id":   defaultMarketAlgoID,
		"algo_name": defaultMarketAlgoName,
		"parsers":   parsers,
	})
	ds := &orm.Dataset{
		ID:                     datasetID,
		KbID:                   kbID,
		DisplayName:            displayName,
		Desc:                   description,
		ResourceUID:            datasetID,
		DatasetInfo:            json.RawMessage(`{}`),
		EmbeddingModel:         "default",
		EmbeddingModelProvider: "default",
		Type:                   uint8(1),
		Ext:                    extBytes,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.WithContext(ctx).Create(ds).Error; err != nil {
		return nil, fmt.Errorf("create dataset failed: %w", err)
	}
	if st := acl.GetStore(); st != nil {
		st.EnsureKB(kbID, displayName, userID)
		ensureDatasetCreatorMember(st, datasetID, userID)
	}
	log.Logger.Info().
		Str("dataset_id", datasetID).
		Str("kb_id", kbID).
		Str("user_id", userID).
		Str("display_name", displayName).
		Msg("market dataset created")
	return ds, nil
}

// isRecordNotFound reports whether err is a gorm record-not-found error.
func isRecordNotFound(err error) bool {
	return err != nil && err.Error() == gorm.ErrRecordNotFound.Error()
}
