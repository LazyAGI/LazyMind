package knowledge_market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/knowledge_market/download"
	"lazymind/core/log"
	"lazymind/core/store"
)

const (
	updateJobType    = doc.MarketUpdateJobType
	updateAllJobType = doc.MarketUpdateAllJobType
)

// updateJobPayload carries everything the async update job needs. Force is
// reserved so a future admin/UI path can skip the change check and re-import.
type updateJobPayload struct {
	MarketItemID string `json:"market_item_id"`
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name"`
	Revision     string `json:"revision,omitempty"`
	Force        bool   `json:"force,omitempty"`
}

// updateAllJobPayload is the one-click update-all batch payload.
type updateAllJobPayload struct {
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
}

// MarketUpdate enqueues a background update job for one installed official
// knowledge base. A conflict with an in-flight install/update of the same item
// is rejected with 409.
func MarketUpdate(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	marketItemID := strings.TrimSpace(common.PathVar(r, "market_item_id"))
	userID := strings.TrimSpace(common.UserID(r))
	userName := strings.TrimSpace(common.UserName(r))
	if marketItemID == "" || userID == "" {
		common.ReplyErr(w, "market_item_id and X-User-Id are required", http.StatusBadRequest)
		return
	}
	install, err := loadInstall(r, db, userID, marketItemID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if install == nil {
		common.ReplyErr(w, "knowledge base is not installed", http.StatusNotFound)
		return
	}
	var item orm.KnowledgeMarketItem
	if err := db.WithContext(r.Context()).Where("id = ? AND status = ?", marketItemID, "published").Take(&item).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	// One task at a time per item (install/update in flight => 409).
	active, err := doc.HasActiveMarketJob(r.Context(), db, userID, marketItemID)
	if err != nil {
		common.ReplyErr(w, "query active market jobs failed", http.StatusInternalServerError)
		return
	}
	if active {
		common.ReplyErr(w, "knowledge base task is running, retry later", http.StatusConflict)
		return
	}
	job, err := asyncjob.Enqueue(r.Context(), db, asyncjob.EnqueueRequest{
		JobType:        updateJobType,
		ResourceType:   "knowledge_market_item",
		ResourceID:     marketItemID,
		IdempotencyKey: "kb_update:" + marketItemID + ":" + userID,
		Payload: updateJobPayload{
			MarketItemID: marketItemID,
			UserID:       userID,
			UserName:     userName,
			Revision:     item.PackageRevision,
		},
		MaxAttempts:    2,
		CreateUserID:   userID,
		CreateUserName: userName,
		// A single update must re-run on every click: a previously succeeded
		// job is retired and a fresh job is created instead of replaying the
		// old result.
		SkipSucceeded: true,
	})
	if err != nil {
		common.ReplyErr(w, "enqueue update job failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"job_id": job.ID, "state": "pending"})
}

// MarketUpdateAll enqueues the one-click update-all batch. A second batch for
// the same user is rejected with 409.
func MarketUpdateAll(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(common.UserID(r))
	userName := strings.TrimSpace(common.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing x-user-id", http.StatusBadRequest)
		return
	}
	active, err := doc.HasActiveMarketBatch(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query active update batch failed", http.StatusInternalServerError)
		return
	}
	if active {
		common.ReplyErr(w, "knowledge base update task is running, retry later", http.StatusConflict)
		return
	}
	job, err := asyncjob.Enqueue(r.Context(), db, asyncjob.EnqueueRequest{
		JobType:        updateAllJobType,
		ResourceType:   "knowledge_market_user",
		ResourceID:     userID,
		IdempotencyKey: "kb_update_all:" + userID,
		Payload: updateAllJobPayload{
			UserID:   userID,
			UserName: userName,
		},
		MaxAttempts:    2,
		CreateUserID:   userID,
		CreateUserName: userName,
		// One-click update must re-run on every click: a previously succeeded
		// batch is retired and a fresh job (and a fresh check) is created.
		SkipSucceeded: true,
	})
	if err != nil {
		common.ReplyErr(w, "enqueue update-all job failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"job_id": job.ID, "state": "pending"})
}

// HandleUpdateJob runs the update pipeline with strategy A (clear then
// import): download the new package, compare against the installed snapshot,
// clear the old documents, import the new ones and persist the new snapshot.
// On failure the old snapshot and installed_at stay untouched, the install row
// is marked failed and a retry starts from a clean dataset.
func HandleUpdateJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	payload, err := decodeUpdatePayload(job.PayloadJSON)
	if err != nil {
		return asyncjob.Result{ErrorCode: "invalid_payload"}, err
	}
	db := store.DB()
	if db == nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, errors.New("store not initialized")
	}

	// Guard: the install may have been removed by an uninstall while this job
	// was queued; never recreate data for a removed install.
	install, err := loadMarketInstallRow(ctx, db, payload.UserID, payload.MarketItemID)
	if err != nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
	}
	if install == nil || strings.TrimSpace(install.DatasetID) == "" {
		return skippedUpdateResult("not_installed")
	}
	var ds orm.Dataset
	if err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", install.DatasetID).Take(&ds).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return skippedUpdateResult("not_installed")
		}
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
	}

	var item orm.KnowledgeMarketItem
	if err := db.WithContext(ctx).Where("id = ? AND status = ?", payload.MarketItemID, "published").Take(&item).Error; err != nil {
		return asyncjob.Result{ErrorCode: "market_item_not_found"}, err
	}
	packageURL := strings.TrimSpace(item.PackageURL)
	if packageURL == "" {
		return failUpdate(ctx, db, payload, errors.New("knowledge base has no package url"))
	}

	oldCfg := decodeInstallConfig(install)
	isGit := strings.HasSuffix(strings.ToLower(packageURL), ".git")
	hasDocs, err := datasetHasDocuments(ctx, db, ds.ID)
	if err != nil {
		return failUpdate(ctx, db, payload, fmt.Errorf("check dataset documents failed: %w", err))
	}

	// Git packages can decide "has update" without downloading: compare the
	// remote commit with the installed baseline. A legacy install without a
	// commit is always refreshed in full. An empty dataset is always refreshed
	// too: a previous failed update may have cleared the documents while the
	// snapshot still matches the remote content.
	if isGit && !payload.Force && hasDocs {
		remote, err := download.RemoteRevision(ctx, packageURL, payload.Revision)
		if err != nil {
			return failUpdate(ctx, db, payload, fmt.Errorf("check remote revision failed: %w", err))
		}
		if oldCfg.Commit != "" && remote == oldCfg.Commit {
			return noChangeUpdateResult()
		}
	}

	if err := setInstallState(ctx, db, payload.MarketItemID, payload.UserID, orm.InstallStateDownloading, ds.ID, "", nil); err != nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
	}
	if reporter != nil {
		_ = reporter.SetProgress(ctx, 0, 3)
	}

	// Download the package into a job-local temp dir under the upload root so
	// the parsing service can read the same filesystem.
	tmpRoot := filepath.Join(doc.UploadRoot(), "market", "tmp", job.ID)
	if err := os.RemoveAll(tmpRoot); err != nil {
		return failUpdate(ctx, db, payload, err)
	}
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return failUpdate(ctx, db, payload, err)
	}
	defer os.RemoveAll(tmpRoot)

	downloadProgress := newMarketDownloadProgress(ctx, reporter)
	var progress download.ProgressFunc
	if downloadProgress != nil {
		progress = downloadProgress.Report
	}
	files, err := download.Fetch(ctx, packageURL, payload.Revision, tmpRoot, progress)
	if err != nil {
		return failUpdate(ctx, db, payload, fmt.Errorf("download package failed: %w", err))
	}
	if len(files) == 0 {
		return failUpdate(ctx, db, payload, errors.New("package contains no files"))
	}

	// Direct-link packages compare the downloaded sha256 set against the
	// installed snapshot; identical content means no update (only when the
	// dataset still has documents, see the git fast-path above).
	if !isGit && !payload.Force && hasDocs && sameFileSnapshot(files, oldCfg.Files) {
		_ = resetInstallStateOnly(ctx, db, payload.MarketItemID, payload.UserID, orm.InstallStateDone)
		return noChangeUpdateResult()
	}
	if reporter != nil {
		_ = reporter.SetProgress(ctx, 1, 3)
	}

	// Strategy A: clear the old documents first, then import the new ones.
	removed, err := doc.ClearMarketDatasetDocuments(ctx, &ds)
	if err != nil {
		return failUpdate(ctx, db, payload, fmt.Errorf("clear old documents failed: %w", err))
	}
	if err := setInstallState(ctx, db, payload.MarketItemID, payload.UserID, orm.InstallStateImporting, ds.ID, "", nil); err != nil {
		return failUpdate(ctx, db, payload, err)
	}
	if reporter != nil {
		_ = reporter.SetProgress(ctx, 2, 3)
	}

	// Apply the same source-specific conversion as the install path so updates
	// re-import only the documents the catalog source actually provides.
	importFiles, err := materializeMarketFiles(ctx, item, files, tmpRoot)
	if err != nil {
		return failUpdate(ctx, db, payload, fmt.Errorf("materialize source files failed: %w", err))
	}
	if len(importFiles) == 0 {
		return failUpdate(ctx, db, payload, errors.New("source adapter produced no documents"))
	}
	importResult, err := doc.ImportMarketFiles(ctx, &ds, payload.UserID, payload.UserName, importFiles)
	if err != nil {
		// Remove partially imported documents so a failed update leaves the
		// knowledge base empty instead of containing only a subset of files.
		if _, cleanupErr := doc.ClearMarketDatasetDocuments(ctx, &ds); cleanupErr != nil {
			log.Logger.Error().
				Err(cleanupErr).
				Str("market_item_id", payload.MarketItemID).
				Str("dataset_id", ds.ID).
				Msg("clear partial market import failed")
		}
		return failUpdate(ctx, db, payload, fmt.Errorf("import files failed: %w", err))
	}

	// Persist the new snapshot; installed_at is refreshed by the done state so
	// "last updated" reflects the real successful update time.
	snapshot := installConfig{
		Revision: payload.Revision,
		Commit:   oldCfg.Commit,
		TaskIDs:  importResult.TaskIDs,
		Files:    make([]fileSnapshot, 0, len(files)),
	}
	for _, f := range files {
		snapshot.Files = append(snapshot.Files, fileSnapshot{Path: f.Path, Size: f.Size, SHA256: f.SHA256})
	}
	if isGit {
		if commit, commitErr := download.LocalCommit(ctx, tmpRoot); commitErr != nil {
			log.Logger.Warn().Err(commitErr).Str("market_item_id", payload.MarketItemID).Msg("resolve local git commit failed")
		} else {
			snapshot.Commit = commit
		}
	}
	if err := setInstallState(ctx, db, payload.MarketItemID, payload.UserID, orm.InstallStateDone, ds.ID, item.Version, &snapshot); err != nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
	}
	if reporter != nil {
		_ = reporter.SetProgress(ctx, 3, 3)
	}
	resultJSON, _ := json.Marshal(map[string]any{
		"dataset_id": ds.ID,
		"submitted":  importResult.Submitted,
		"updated":    true,
		"removed":    removed,
	})
	return asyncjob.Result{ResultJSON: resultJSON}, nil
}

// HandleUpdateAllJob runs the one-click batch: it only checks every installed
// item and spawns an independent update job per item that actually changed;
// unchanged items are skipped and nothing is written here.
func HandleUpdateAllJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	payload, err := decodeUpdateAllPayload(job.PayloadJSON)
	if err != nil {
		return asyncjob.Result{ErrorCode: "invalid_payload"}, err
	}
	db := store.DB()
	if db == nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, errors.New("store not initialized")
	}

	var rows []orm.KnowledgeMarketInstall
	if err := db.WithContext(ctx).
		Where("user_id = ? AND install_state = ?", payload.UserID, string(orm.InstallStateDone)).
		Find(&rows).Error; err != nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
	}

	total := int64(len(rows))
	checked := int64(0)
	var updated, skipped []string
	for _, row := range rows {
		checked++
		if reporter != nil {
			_ = reporter.SetProgress(ctx, checked, total)
		}
		marketItemID := strings.TrimSpace(row.MarketItemID)
		if marketItemID == "" {
			continue
		}
		var item orm.KnowledgeMarketItem
		if err := db.WithContext(ctx).Where("id = ? AND status = ?", marketItemID, "published").Take(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				skipped = append(skipped, marketItemID)
				continue
			}
			return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
		}
		hasUpdate, checkErr := marketItemHasUpdate(ctx, &item, &row)
		if checkErr != nil {
			log.Logger.Warn().Err(checkErr).Str("market_item_id", marketItemID).Str("user_id", payload.UserID).Msg("update-all: check item failed, skipping")
			skipped = append(skipped, marketItemID)
			continue
		}
		if !hasUpdate {
			continue
		}
		if _, err := asyncjob.Enqueue(ctx, db, asyncjob.EnqueueRequest{
			JobType:        updateJobType,
			ResourceType:   "knowledge_market_item",
			ResourceID:     marketItemID,
			IdempotencyKey: "kb_update:" + marketItemID + ":" + payload.UserID,
			Payload: updateJobPayload{
				MarketItemID: marketItemID,
				UserID:       payload.UserID,
				UserName:     payload.UserName,
				Revision:     item.PackageRevision,
			},
			MaxAttempts:    2,
			CreateUserID:   payload.UserID,
			CreateUserName: payload.UserName,
			// Same policy as MarketUpdate: never reuse a previously succeeded
			// update job, so a fresh job actually runs the update.
			SkipSucceeded: true,
		}); err != nil {
			return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
		}
		updated = append(updated, marketItemID)
	}

	resultJSON, _ := json.Marshal(map[string]any{"checked": len(rows), "updated": updated, "skipped": skipped})
	return asyncjob.Result{ResultJSON: resultJSON}, nil
}

// marketItemHasUpdate reports whether the item's remote content differs from
// the installed snapshot: git compares commits, direct links download the
// package and compare the sha256 set. A legacy install without a commit
// baseline is always considered outdated (full refresh).
func marketItemHasUpdate(ctx context.Context, item *orm.KnowledgeMarketItem, install *orm.KnowledgeMarketInstall) (bool, error) {
	if item == nil || install == nil {
		return false, nil
	}
	cfg := decodeInstallConfig(install)
	if strings.TrimSpace(install.DatasetID) != "" {
		hasDocs, err := datasetHasDocuments(ctx, store.DB(), install.DatasetID)
		if err != nil {
			return false, err
		}
		if !hasDocs {
			// The dataset is empty (e.g. a failed update cleared the docs):
			// always schedule a full refresh even when content matches.
			return true, nil
		}
	}
	packageURL := strings.TrimSpace(item.PackageURL)
	if strings.HasSuffix(strings.ToLower(packageURL), ".git") {
		if cfg.Commit == "" {
			return true, nil
		}
		remote, err := download.RemoteRevision(ctx, packageURL, item.PackageRevision)
		if err != nil {
			return false, err
		}
		return remote != cfg.Commit, nil
	}
	tmpRoot, err := os.MkdirTemp("", "market-update-check-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmpRoot)
	files, err := download.Fetch(ctx, packageURL, item.PackageRevision, tmpRoot, nil)
	if err != nil {
		return false, err
	}
	return !sameFileSnapshot(files, cfg.Files), nil
}

// decodeUpdatePayload validates the single-update job payload.
func decodeUpdatePayload(raw json.RawMessage) (updateJobPayload, error) {
	var payload updateJobPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("decode update payload: %w", err)
	}
	payload.MarketItemID = strings.TrimSpace(payload.MarketItemID)
	payload.UserID = strings.TrimSpace(payload.UserID)
	payload.Revision = strings.TrimSpace(payload.Revision)
	if payload.MarketItemID == "" || payload.UserID == "" {
		return payload, errors.New("invalid update payload")
	}
	return payload, nil
}

// decodeUpdateAllPayload validates the batch job payload.
func decodeUpdateAllPayload(raw json.RawMessage) (updateAllJobPayload, error) {
	var payload updateAllJobPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("decode update-all payload: %w", err)
	}
	payload.UserID = strings.TrimSpace(payload.UserID)
	if payload.UserID == "" {
		return payload, errors.New("invalid update-all payload")
	}
	return payload, nil
}

// failUpdate marks the update failed while keeping the old snapshot and
// installed_at: strategy A clears the documents first, so after a failed
// update the knowledge base is empty, the failure stays visible and a retry
// (or uninstall) remains possible.
func failUpdate(ctx context.Context, db *gorm.DB, payload updateJobPayload, err error) (asyncjob.Result, error) {
	log.Logger.Error().
		Err(err).
		Str("market_item_id", payload.MarketItemID).
		Str("user_id", payload.UserID).
		Msg("knowledge market update failed")
	_ = setInstallState(ctx, db, payload.MarketItemID, payload.UserID, orm.InstallStateFailed, "", "", nil)
	return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
}

// noChangeUpdateResult reports an update check that found nothing to update.
func noChangeUpdateResult() (asyncjob.Result, error) {
	resultJSON, _ := json.Marshal(map[string]any{"updated": false, "reason": "no_change"})
	return asyncjob.Result{ResultJSON: resultJSON}, nil
}

// skippedUpdateResult reports an update job that must not touch anything
// (e.g. the install was removed while the job was queued).
func skippedUpdateResult(reason string) (asyncjob.Result, error) {
	resultJSON, _ := json.Marshal(map[string]any{"updated": false, "skipped": true, "reason": reason})
	return asyncjob.Result{ResultJSON: resultJSON}, nil
}

// loadMarketInstallRow loads one install row by (user, item); nil when absent.
func loadMarketInstallRow(ctx context.Context, db *gorm.DB, userID, itemID string) (*orm.KnowledgeMarketInstall, error) {
	userID = strings.TrimSpace(userID)
	itemID = strings.TrimSpace(itemID)
	if userID == "" || itemID == "" {
		return nil, nil
	}
	var row orm.KnowledgeMarketInstall
	err := db.WithContext(ctx).Where("user_id = ? AND market_item_id = ?", userID, itemID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// datasetHasDocuments reports whether the dataset currently has any
// non-deleted documents. The update fast-path (no change) is only taken when
// the dataset is non-empty so a cleared dataset is always re-imported.
func datasetHasDocuments(ctx context.Context, db *gorm.DB, datasetID string) (bool, error) {
	if db == nil {
		return false, errors.New("store not initialized")
	}
	var count int64
	if err := db.WithContext(ctx).Model(&orm.Document{}).
		Where("dataset_id = ? AND deleted_at IS NULL", datasetID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// decodeInstallConfig decodes the snapshot JSON stored on an install row.
func decodeInstallConfig(install *orm.KnowledgeMarketInstall) installConfig {
	var cfg installConfig
	if install != nil {
		_ = json.Unmarshal(install.Config, &cfg)
	}
	return cfg
}

// sameFileSnapshot reports whether the downloaded file set matches the
// installed snapshot (path + size + sha256), i.e. the direct-link package
// content did not change.
func sameFileSnapshot(files []download.FetchedFile, old []fileSnapshot) bool {
	if len(files) != len(old) {
		return false
	}
	byPath := make(map[string]fileSnapshot, len(old))
	for _, f := range old {
		byPath[f.Path] = f
	}
	for _, f := range files {
		prev, ok := byPath[f.Path]
		if !ok || prev.SHA256 != f.SHA256 || prev.Size != f.Size {
			return false
		}
	}
	return true
}

// resetInstallStateOnly restores install_state after a no-change direct-link
// check without refreshing installed_at (nothing was actually updated).
func resetInstallStateOnly(ctx context.Context, db *gorm.DB, marketItemID, userID string, state orm.InstallState) error {
	return db.WithContext(ctx).Model(&orm.KnowledgeMarketInstall{}).
		Where("market_item_id = ? AND user_id = ?", marketItemID, userID).
		Updates(map[string]any{"install_state": string(state), "updated_at": time.Now().UTC()}).Error
}
