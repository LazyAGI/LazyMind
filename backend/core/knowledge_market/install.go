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
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

const installJobType = doc.MarketInstallJobType

// installJobPayload carries everything the async install job needs.
type installJobPayload struct {
	MarketItemID string `json:"market_item_id"`
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name"`
	Revision     string `json:"revision,omitempty"`
}

// installConfig is the runtime file snapshot persisted on the install row; it
// is the diff baseline for future update jobs.
type installConfig struct {
	Revision string         `json:"revision"`
	Commit   string         `json:"commit,omitempty"` // git HEAD; update diff baseline
	TaskIDs  []string       `json:"task_ids,omitempty"`
	Files    []fileSnapshot `json:"files"`
}

type fileSnapshot struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// MarketInstall enqueues a background install job for an official knowledge
// base. The job downloads the package, creates the personal dataset and
// submits every file to the parsing pipeline.
func MarketInstall(w http.ResponseWriter, r *http.Request) {
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
	var item orm.KnowledgeMarketItem
	if err := db.WithContext(r.Context()).Where("id = ? AND status = ?", marketItemID, "published").Take(&item).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	if strings.TrimSpace(item.PackageURL) == "" {
		common.ReplyErr(w, "knowledge base has no download package configured", http.StatusBadRequest)
		return
	}
	// Reject a second install/update while one is already in flight for the
	// same item (instead of silently reusing the queued job via the
	// idempotency key) so the frontend can surface the conflict.
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
		JobType:        installJobType,
		ResourceType:   "knowledge_market_item",
		ResourceID:     marketItemID,
		IdempotencyKey: "kb_install:" + marketItemID + ":" + userID,
		Payload: installJobPayload{
			MarketItemID: marketItemID,
			UserID:       userID,
			UserName:     userName,
			Revision:     item.PackageRevision,
		},
		MaxAttempts:    2,
		CreateUserID:   userID,
		CreateUserName: userName,
	})
	if err != nil {
		common.ReplyErr(w, "enqueue install job failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"job_id": job.ID, "state": "pending"})
}

// RegisterAsyncJobs registers the knowledge market background jobs.
func RegisterAsyncJobs() {
	asyncjob.Register(installJobType, HandleInstallJob)
	asyncjob.Register(updateJobType, HandleUpdateJob)
	asyncjob.Register(updateAllJobType, HandleUpdateAllJob)
}

// HandleInstallJob runs the install pipeline: download -> create dataset ->
// import files -> submit parse/vectorize tasks -> finish.
func HandleInstallJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	payload, err := decodeInstallPayload(job.PayloadJSON)
	if err != nil {
		return asyncjob.Result{ErrorCode: "invalid_payload"}, err
	}
	db := store.DB()
	if db == nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, errors.New("store not initialized")
	}

	var item orm.KnowledgeMarketItem
	if err := db.WithContext(ctx).Where("id = ? AND status = ?", payload.MarketItemID, "published").Take(&item).Error; err != nil {
		return asyncjob.Result{ErrorCode: "market_item_not_found"}, err
	}
	packageURL := strings.TrimSpace(item.PackageURL)
	if packageURL == "" {
		return asyncjob.Result{ErrorCode: "package_url_missing"}, errors.New("knowledge base has no package url")
	}

	if err := setInstallState(ctx, db, payload.MarketItemID, payload.UserID, orm.InstallStateDownloading, "", "", nil); err != nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
	}
	if reporter != nil {
		_ = reporter.SetProgress(ctx, 0, 2)
	}

	// Download the package into a job-local temp dir under the upload root so
	// the parsing service can read the same filesystem.
	tmpRoot := filepath.Join(doc.UploadRoot(), "market", "tmp", job.ID)
	if err := os.RemoveAll(tmpRoot); err != nil {
		return failInstall(ctx, db, payload, err)
	}
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return failInstall(ctx, db, payload, err)
	}
	defer os.RemoveAll(tmpRoot)

	downloadProgress := newMarketDownloadProgress(ctx, reporter)
	var progress download.ProgressFunc
	if downloadProgress != nil {
		progress = downloadProgress.Report
	}
	files, err := download.Fetch(ctx, packageURL, payload.Revision, tmpRoot, progress)
	if err != nil {
		return failInstall(ctx, db, payload, fmt.Errorf("download package failed: %w", err))
	}
	if len(files) == 0 {
		return failInstall(ctx, db, payload, errors.New("package contains no files"))
	}
	if reporter != nil {
		_ = reporter.SetProgress(ctx, 1, 2)
	}

	// Vectorizing needs the embedding model, so check readiness up front.
	ready, err := modelprovider.IsModelReady(ctx, db, payload.UserID, "embed_main")
	if err != nil {
		return failInstall(ctx, db, payload, fmt.Errorf("check embedding model failed: %w", err))
	}
	if !ready {
		return failInstall(ctx, db, payload, errors.New("embedding model is not ready"))
	}

	// Create the personal dataset; reuse the one recorded by a failed attempt.
	ds, err := doc.CreateMarketDataset(ctx, payload.UserID, payload.UserName, installDatasetID(ctx, db, payload), item.Name, item.Description, tagsFromItem(item))
	if err != nil {
		return failInstall(ctx, db, payload, fmt.Errorf("create dataset failed: %w", err))
	}
	if err := setInstallState(ctx, db, payload.MarketItemID, payload.UserID, orm.InstallStateImporting, ds.ID, "", nil); err != nil {
		return failInstall(ctx, db, payload, err)
	}

	// Convert the downloaded package into parser-ready documents. Adapter-based
	// sources only keep knowledge files; legacy sources keep every file.
	importFiles, err := materializeMarketFiles(ctx, item, files, tmpRoot)
	if err != nil {
		return failInstall(ctx, db, payload, fmt.Errorf("materialize source files failed: %w", err))
	}
	if len(importFiles) == 0 {
		return failInstall(ctx, db, payload, errors.New("source adapter produced no documents"))
	}
	importResult, err := doc.ImportMarketFiles(ctx, ds, payload.UserID, payload.UserName, importFiles)
	if err != nil {
		return failInstall(ctx, db, payload, fmt.Errorf("import files failed: %w", err))
	}

	// Finish: persist the file snapshot for later update diffs.
	snapshot := installConfig{
		Revision: payload.Revision,
		TaskIDs:  importResult.TaskIDs,
		Files:    make([]fileSnapshot, 0, len(files)),
	}
	for _, f := range files {
		snapshot.Files = append(snapshot.Files, fileSnapshot{Path: f.Path, Size: f.Size, SHA256: f.SHA256})
	}
	// Record the checked-out git commit as the update diff baseline; a legacy
	// install without it is refreshed in full on the first update.
	if strings.HasSuffix(strings.ToLower(packageURL), ".git") {
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
		_ = reporter.SetProgress(ctx, 2, 2)
	}
	resultJSON, _ := json.Marshal(map[string]any{"dataset_id": ds.ID, "submitted": importResult.Submitted})
	return asyncjob.Result{ResultJSON: resultJSON}, nil
}

// decodeInstallPayload validates the job payload.
func decodeInstallPayload(raw json.RawMessage) (installJobPayload, error) {
	var payload installJobPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("decode install payload: %w", err)
	}
	payload.MarketItemID = strings.TrimSpace(payload.MarketItemID)
	payload.UserID = strings.TrimSpace(payload.UserID)
	payload.Revision = strings.TrimSpace(payload.Revision)
	if payload.MarketItemID == "" || payload.UserID == "" {
		return payload, errors.New("invalid install payload")
	}
	return payload, nil
}

// failInstall marks the install row failed and returns a handler result.
func failInstall(ctx context.Context, db *gorm.DB, payload installJobPayload, err error) (asyncjob.Result, error) {
	// Log the real failure reason: the job-row error_message may be lost when
	// the process exits between this write and the runner's job transition.
	log.Logger.Error().
		Err(err).
		Str("market_item_id", payload.MarketItemID).
		Str("user_id", payload.UserID).
		Msg("knowledge market install failed")
	// A failed install should not leave a residual personal dataset behind.
	// Delete the dataset created by this (or a previous retried) attempt and
	// clear the install link so a retry starts from a clean state. Cleanup
	// errors are logged only: the install is still marked failed.
	if datasetID := installDatasetID(ctx, db, payload); datasetID != "" {
		if delErr := doc.CleanupMarketDataset(ctx, payload.UserID, datasetID); delErr != nil {
			log.Logger.Error().
				Err(delErr).
				Str("market_item_id", payload.MarketItemID).
				Str("user_id", payload.UserID).
				Str("dataset_id", datasetID).
				Msg("cleanup residual market dataset failed")
		} else {
			clearInstallDatasetID(ctx, db, payload)
		}
	}
	_ = setInstallState(ctx, db, payload.MarketItemID, payload.UserID, orm.InstallStateFailed, "", "", nil)
	return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
}

// clearInstallDatasetID clears the dataset link on the install row after the
// residual dataset has been deleted.
func clearInstallDatasetID(ctx context.Context, db *gorm.DB, payload installJobPayload) {
	_ = db.WithContext(ctx).Model(&orm.KnowledgeMarketInstall{}).
		Where("market_item_id = ? AND user_id = ?", payload.MarketItemID, payload.UserID).
		Updates(map[string]any{"dataset_id": "", "updated_at": time.Now().UTC()}).Error
}

// setInstallState upserts the per-user install row with the given state. A
// manual upsert (instead of ON CONFLICT) keeps JSON columns portable across
// SQLite and PostgreSQL. It is shared by the install and update pipelines
// (marketItemID/userID instead of a concrete payload type).
func setInstallState(ctx context.Context, db *gorm.DB, marketItemID, userID string, state orm.InstallState, datasetID, installedVersion string, cfg *installConfig) error {
	now := time.Now().UTC()
	installedVersion = strings.TrimSpace(installedVersion)
	updates := map[string]any{
		"install_state": string(state),
		"updated_at":    now,
	}
	if datasetID != "" {
		updates["dataset_id"] = datasetID
	}
	if state == orm.InstallStateDone {
		updates["installed_at"] = now
		if installedVersion != "" {
			updates["installed_version"] = installedVersion
		}
	}
	if cfg != nil {
		b, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		updates["config"] = json.RawMessage(b)
	}

	var existing orm.KnowledgeMarketInstall
	err := db.WithContext(ctx).Where("market_item_id = ? AND user_id = ?", marketItemID, userID).Take(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row := orm.KnowledgeMarketInstall{
			MarketItemID:     marketItemID,
			UserID:           userID,
			InstalledVersion: installedVersion,
			DatasetID:        datasetID,
			InstallState:     string(state),
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if state == orm.InstallStateDone {
			row.InstalledAt = &now
		}
		// Always set config explicitly so sqlite does not fall back to the
		// column default and trigger a RETURNING scan of the JSON column.
		b, marshalErr := json.Marshal(cfg)
		if marshalErr != nil {
			return marshalErr
		}
		row.Config = json.RawMessage(b)
		return db.WithContext(ctx).Create(&row).Error
	}
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Model(&existing).Updates(updates).Error
}

// installDatasetID returns the dataset recorded on a previous attempt, if any.
func installDatasetID(ctx context.Context, db *gorm.DB, payload installJobPayload) string {
	var row orm.KnowledgeMarketInstall
	if err := db.WithContext(ctx).Where("market_item_id = ? AND user_id = ?", payload.MarketItemID, payload.UserID).Take(&row).Error; err == nil {
		return strings.TrimSpace(row.DatasetID)
	}
	return ""
}

// tagsFromItem decodes the catalog tags of a market item.
func tagsFromItem(item orm.KnowledgeMarketItem) []string {
	var tags []string
	_ = json.Unmarshal(item.Tags, &tags)
	return tags
}
