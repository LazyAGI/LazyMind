package knowledge_market

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

// Probe only configured internal services. A short cache bounds list polling;
// an unknown endpoint is never treated as a healthy executor.
var marketHealthCache = struct {
	sync.Mutex
	entries map[string]healthEntry
}{entries: make(map[string]healthEntry)}

type healthEntry struct {
	state   string
	expires time.Time
}

var marketHealthClient = &http.Client{Timeout: time.Second, Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func marketHealth(ctx context.Context, endpoint, path string) string {
	if strings.TrimSpace(endpoint) == "" {
		return "unknown"
	}
	url := strings.TrimRight(endpoint, "/") + path
	marketHealthCache.Lock()
	defer marketHealthCache.Unlock()
	if entry, ok := marketHealthCache.entries[url]; ok && time.Now().Before(entry.expires) {
		return entry.state
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	state := "unavailable"
	if err == nil {
		resp, requestErr := marketHealthClient.Do(req)
		if requestErr == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				state = "available"
			}
		}
	}
	if ctx.Err() != nil {
		return "unknown"
	}
	if len(marketHealthCache.entries) > 64 {
		marketHealthCache.entries = make(map[string]healthEntry)
	}
	marketHealthCache.entries[url] = healthEntry{state, time.Now().Add(2 * time.Second)}
	return state
}

func marketWorkerHealth(ctx context.Context) string {
	return marketHealth(ctx, os.Getenv("LAZYMIND_DOCUMENT_WORKER_URL"), "/ready")
}
func marketCancelHealth(ctx context.Context) string {
	return marketHealth(ctx, os.Getenv("LAZYMIND_DOCUMENT_SERVICE_URL"), "/v1/ready")
}
func marketDraining(job orm.AsyncJob) bool {
	return job.Status == "canceled" && job.LockedBy != "" && job.LockUntil != nil && job.LockUntil.After(time.Now())
}

func marketLatest(ctx context.Context, db *gorm.DB, job orm.AsyncJob) bool {
	var count int64
	err := db.WithContext(ctx).Model(&orm.AsyncJob{}).Where("create_user_id = ? AND resource_id = ? AND job_type IN ? AND (created_at > ? OR (created_at = ? AND id > ?))", job.CreateUserID, job.ResourceID, marketJobTypes, job.CreatedAt, job.CreatedAt, job.ID).Count(&count).Error
	return err == nil && count == 0
}

func marketDisplay(job orm.AsyncJob, parse parseProgressInfo) string {
	if parse.State == "unknown" || parse.Unknown > 0 {
		return "unknown"
	}
	if job.Status == "pending" {
		return "pending"
	}
	if job.Status == "running" || marketDraining(job) || parse.Parsing > 0 {
		return "processing"
	}
	if parse.Pending > 0 {
		return "pending"
	}
	if parse.Canceled > 0 {
		if parse.Canceled == parse.Total {
			return "canceled"
		}
		return "partial_canceled"
	}
	if parse.Failed > 0 {
		if parse.Failed == parse.Total {
			return "failed"
		}
		return "partial_failed"
	}
	if parse.Total > 0 && parse.Done == parse.Total {
		return "done"
	}
	if job.Status == "failed" {
		return "failed"
	}
	if job.Status == "canceled" {
		return "canceled"
	}
	return "done"
}

func addMarketControl(r *http.Request, db *gorm.DB, data map[string]any, job orm.AsyncJob, parse parseProgressInfo) {
	state := marketDisplay(job, parse)
	terminal := state != "unknown" && state != "pending" && state != "processing"
	worker := "unknown"
	if !terminal || parse.Failed > 0 || job.Status == "failed" {
		worker = marketWorkerHealth(r.Context())
	}
	// A worker outage does not stop a Go download, nor alter historical results.
	if !terminal && state != "unknown" && job.Status != "pending" && job.Status != "running" && !marketDraining(job) {
		if worker == "unavailable" {
			state = "blocked"
		} else if worker == "unknown" {
			state = "unknown"
		}
	}
	latest := marketLatest(r.Context(), db, job)
	canCancel := false
	if latest && job.JobType != updateAllJobType {
		canCancel = job.Status == "pending" || job.Status == "running"
		if !canCancel && state != "unknown" && !marketDraining(job) && parse.Pending > 0 {
			canCancel = marketCancelHealth(r.Context()) == "available"
		}
	}
	data["display_state"], data["can_cancel"], data["can_delete"] = state, canCancel, terminal
	data["can_retry"] = terminal && latest && worker == "available" && (parse.Failed > 0 || job.Status == "failed")
}

func MarketCancelTask(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	owner := strings.TrimSpace(common.UserID(r))
	if owner == "" {
		common.ReplyAppErr(w, common.NewAppError(401, common.ErrCodeUnauthorized, "Authentication required"))
		return
	}
	var job orm.AsyncJob
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND job_type IN ?", common.PathVar(r, "job_id"), owner, marketJobTypes).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "knowledge market task not found", 404)
		} else {
			replyServiceError(w, err)
		}
		return
	}
	if job.JobType == updateAllJobType || !marketLatest(r.Context(), db, job) {
		marketConflict(w)
		return
	}
	install, err := loadInstall(r, db, owner, job.ResourceID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	own, parse := taskProgress(r, db, job, install)
	datasetID := ""
	if own != nil {
		datasetID = own.DatasetID
	}
	if datasetID == "" && install != nil {
		datasetID = install.DatasetID
	}
	if datasetID != "" && !doc.RequireMarketDatasetPermission(w, r, datasetID) {
		return
	}
	if parse.State == "unknown" && job.Status != "pending" && job.Status != "running" {
		marketConflict(w)
		return
	}
	if job.Status == "pending" || job.Status == "running" {
		// Retain the execution lease until the handler finishes its cleanup.
		err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			var item orm.KnowledgeMarketItem
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&item, "id = ?", job.ResourceID).Error; err != nil {
				return err
			}
			if !marketLatest(r.Context(), tx, job) {
				return errMarketBusy
			}
			var current orm.AsyncJob
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&current, "id = ? AND create_user_id = ?", job.ID, owner).Error; err != nil {
				return err
			}
			if current.Status != "pending" && current.Status != "running" {
				return errMarketBusy
			}
			updates := map[string]any{"status": "canceled", "error_code": "canceled", "error_message": "", "finished_at": time.Now().UTC()}
			if current.Status == "pending" {
				data, _ := json.Marshal(map[string]any{"reason": "stopped", "dataset_id": datasetID})
				updates["result_json"] = json.RawMessage(data)
			}
			return tx.Model(&current).Updates(updates).Error
		})
		if err != nil {
			replyServiceError(w, err)
			return
		}
		common.ReplyOK(w, map[string]any{"job_id": job.ID, "canceled": 0, "running": parse.Parsing, "unknown": parse.Unknown, "stop_requested": true})
		return
	}
	var previousStop struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(job.ResultJSON, &previousStop)
	if job.Status == "canceled" && previousStop.Reason == "stopped" {
		common.ReplyOK(w, map[string]any{"job_id": job.ID, "canceled": 0, "running": 0, "unknown": 0, "stop_requested": true})
		return
	}
	if own == nil || own.DatasetID == "" {
		marketConflict(w)
		return
	}
	cfg := decodeInstallConfig(own)
	// taskProgress may return a frozen snapshot with no live file scope.
	if len(cfg.TaskIDs) == 0 {
		marketConflict(w)
		return
	}
	files, err := doc.MarketTaskStates(r.Context(), db, own.DatasetID, cfg.TaskIDs)
	if err != nil {
		marketConflict(w)
		return
	}
	needsCancel := false
	for _, file := range files {
		if file.State == "WAITING" {
			needsCancel = true
		}
	}
	if needsCancel && marketCancelHealth(r.Context()) != "available" {
		marketUnavailable(w)
		return
	}
	canceled, running, unknown := 0, 0, len(cfg.TaskIDs)-len(files)
	for _, file := range files {
		if r.Context().Err() != nil {
			unknown++
			continue
		}
		state := file.State
		if state == "WAITING" {
			state = doc.CancelMarketFile(r, file)
		}
		switch state {
		case "CANCELED":
			canceled++
		case "WORKING":
			running++
		case "SUCCESS", "FAILED":
		default:
			unknown++
		}
	}
	common.ReplyOK(w, map[string]any{"job_id": job.ID, "canceled": canceled, "running": running, "unknown": unknown})
}

func marketConflict(w http.ResponseWriter) {
	common.ReplyAppErr(w, common.NewAppError(409, common.ErrCodeConflict, "Task state does not allow this operation"))
}
func marketUnavailable(w http.ResponseWriter) {
	common.ReplyAppErr(w, common.NewAppError(503, common.ErrCodeBadGateway, "Document processing service is unavailable"))
}
