package knowledge_market

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

// defaultInstallJobType is the async job type of the knowledge market install
// pipeline; the task endpoints default to it but accept a job_type override
// so future update/uninstall jobs reuse the same surface.
const defaultInstallJobType = "knowledge_market_install"

// installJobStatuses are the async job statuses accepted by the list filter.
var installJobStatuses = map[string]bool{
	"pending":   true,
	"running":   true,
	"succeeded": true,
	"failed":    true,
	"canceled":  true,
}

// MarketListInstallTasks returns the current user's background install tasks
// with the market item name/icon and the install-state enrichment needed by
// the "background tasks" panel.
func MarketListInstallTasks(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing x-user-id", http.StatusBadRequest)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !installJobStatuses[status] {
		common.ReplyErr(w, "invalid status", http.StatusBadRequest)
		return
	}
	jobType := strings.TrimSpace(r.URL.Query().Get("job_type"))
	if jobType == "" {
		jobType = defaultInstallJobType
	}

	if !isMarketJobType(jobType) {
		common.ReplyErr(w, "invalid job type", http.StatusBadRequest)
		return
	}

	base := db.WithContext(r.Context()).
		Model(&orm.AsyncJob{}).
		Where("job_type = ? AND create_user_id = ?", jobType, userID)
	if status != "" {
		base = base.Where("status = ?", status)
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	page := positiveQueryInt(r, "page", 1)
	pageSize := positiveQueryInt(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}

	var jobs []orm.AsyncJob
	if err := base.
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&jobs).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	items, err := buildTaskListItems(r, db, userID, jobs)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items, "page": page, "page_size": pageSize, "total": total})
}

// MarketGetInstallTask returns one background install task (polling target)
// including payload, result and attempt information. Ownership is enforced by
// filtering on the current user.
func MarketGetInstallTask(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing x-user-id", http.StatusBadRequest)
		return
	}
	jobID := strings.TrimSpace(common.PathVar(r, "job_id"))
	if jobID == "" {
		common.ReplyErr(w, "job_id is required", http.StatusBadRequest)
		return
	}

	// Any knowledge market job type (install/update/update-all) can be polled;
	// ownership is still enforced by filtering on the current user.
	var job orm.AsyncJob
	err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND job_type IN ?", jobID, userID, marketJobTypes).
		Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "knowledge market task not found", http.StatusNotFound)
		return
	}
	if err != nil {
		replyServiceError(w, err)
		return
	}

	item, err := loadMarketItemByID(r, db, job.ResourceID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	install, err := loadInstall(r, db, userID, job.ResourceID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	effectiveInstall, parse := taskProgress(r, db, job, install)
	stage, overall := installStageAndPercent(job, effectiveInstall, parse)
	data := taskItemDTO(job, item, effectiveInstall)
	data["attempt_count"] = job.AttemptCount
	data["max_attempts"] = job.MaxAttempts
	data["started_at"] = job.StartedAt
	data["updated_at"] = job.UpdatedAt
	data["payload"] = marketPayloadDTO(job.PayloadJSON)
	data["result"] = marketResultDTO(job.ResultJSON)
	data["stage"] = stage
	data["overall_percent"] = overall
	data["parse"] = parse
	addMarketControl(r, db, data, job, parse)
	common.ReplyOK(w, data)
}

// MarketListInstalls returns the current user's knowledge market installs with
// market item info, so the plaza cards and "my knowledge bases" tab can map
// each item to its install state. No pagination.
func MarketListInstalls(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing x-user-id", http.StatusBadRequest)
		return
	}

	var rows []orm.KnowledgeMarketInstall
	if err := db.WithContext(r.Context()).
		Where("user_id = ?", userID).
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	itemIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		itemIDs = append(itemIDs, row.MarketItemID)
	}
	itemByID, err := loadMarketItemsByIDs(r, db, itemIDs)
	if err != nil {
		replyServiceError(w, err)
		return
	}

	// In-flight jobs per item plus a running one-click batch let the frontend
	// render "installing/updating" from real activity and treat a stale
	// intermediate install_state without any active job as abnormal/reinstallable.
	activeJobs, batchRunning, err := activeMarketJobsForUser(r, db, userID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	installByItem := make(map[string]*orm.KnowledgeMarketInstall, len(rows))
	parseActiveByItem := make(map[string]bool, len(rows))
	for i := range rows {
		parse := parseProgress(r, db, &rows[i])
		effective := installWithEffectiveState(&rows[i], parse)
		installByItem[rows[i].MarketItemID] = effective
		parseActiveByItem[rows[i].MarketItemID] = effective != nil && effective.InstallState == string(orm.InstallStateVectorizing)
	}
	activeByItem := make(map[string]bool, len(activeJobs))
	for i := range activeJobs {
		job := activeJobs[i]
		if job.JobType == updateAllJobType || strings.TrimSpace(job.ResourceID) == "" {
			continue
		}
		if activeJobs[i].Status == string(asyncjob.StatusPending) || activeJobs[i].Status == string(asyncjob.StatusRunning) {
			activeByItem[job.ResourceID] = true
		}
	}
	items := make([]map[string]any, 0, len(rows))
	for i := range rows {
		row := rows[i]
		effective := installByItem[row.MarketItemID]
		installState := row.InstallState
		if effective != nil {
			installState = effective.InstallState
		}
		name, icon, domain := "", "", ""
		if item := itemByID[row.MarketItemID]; item != nil {
			name = item.Name
			icon = item.Icon
			domain = item.Domain
		}
		items = append(items, map[string]any{
			"market_item_id":    row.MarketItemID,
			"name":              name,
			"icon":              icon,
			"domain":            domain,
			"install_state":     installState,
			"installed_version": row.InstalledVersion,
			"dataset_id":        row.DatasetID,
			"installed_at":      row.InstalledAt,
			"updated_at":        row.UpdatedAt,
			"active":            parseActiveByItem[row.MarketItemID] || activeByItem[row.MarketItemID] || batchRunning,
		})
	}
	common.ReplyOK(w, map[string]any{"items": items, "total": len(items)})
}

// taskItemDTO builds the shared task representation used by list and detail.
// Version numbers are intentionally not exposed: the product only shows the
// install/update state and the last actual update time.
func taskItemDTO(job orm.AsyncJob, item *orm.KnowledgeMarketItem, install *orm.KnowledgeMarketInstall) map[string]any {
	datasetID, installState := "", ""
	if install != nil {
		datasetID = install.DatasetID
		installState = install.InstallState
	}
	name, icon := "", ""
	if item != nil {
		name = item.Name
		icon = item.Icon
	}
	return map[string]any{
		"job_id":         job.ID,
		"job_type":       job.JobType,
		"job_status":     job.Status,
		"install_state":  installState,
		"market_item_id": job.ResourceID,
		"name":           name,
		"icon":           icon,
		"progress":       map[string]any{"current": job.ProgressCurrent, "total": job.ProgressTotal},
		"dataset_id":     datasetID,
		"error_message":  job.ErrorMessage,
		"created_at":     job.CreatedAt,
		"finished_at":    job.FinishedAt,
	}
}

// buildTaskListItems enriches a page of jobs with item and install data in
// batch queries (no N+1).
func buildTaskListItems(r *http.Request, db *gorm.DB, userID string, jobs []orm.AsyncJob) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(jobs))
	if len(jobs) == 0 {
		return items, nil
	}
	itemIDs := make([]string, 0, len(jobs))
	for _, job := range jobs {
		itemIDs = append(itemIDs, job.ResourceID)
	}
	itemByID, err := loadMarketItemsByIDs(r, db, itemIDs)
	if err != nil {
		return nil, err
	}
	installByItem, err := loadInstallsByItemIDs(r, db, userID, itemIDs)
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		install := installByItem[jobs[i].ResourceID]
		effectiveInstall, parse := taskProgress(r, db, jobs[i], install)
		item := taskItemDTO(jobs[i], itemByID[jobs[i].ResourceID], effectiveInstall)
		item["stage"], item["overall_percent"] = installStageAndPercent(jobs[i], effectiveInstall, parse)
		item["parse"] = parse
		addMarketControl(r, db, item, jobs[i], parse)
		items = append(items, item)
	}
	return items, nil
}

// activeMarketJobsForUser returns the user's in-flight install/update jobs
// (including a one-click update-all batch) plus whether a batch is running.
func activeMarketJobsForUser(r *http.Request, db *gorm.DB, userID string) ([]orm.AsyncJob, bool, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, false, nil
	}
	var rows []orm.AsyncJob
	if err := db.WithContext(r.Context()).
		Where("create_user_id = ? AND status IN ? AND job_type IN ?",
			userID, []string{"pending", "running"}, []string{installJobType, updateJobType, updateAllJobType}).
		Find(&rows).Error; err != nil {
		return nil, false, err
	}
	batchRunning := false
	for i := range rows {
		if rows[i].JobType == updateAllJobType {
			batchRunning = true
		}
	}
	return rows, batchRunning, nil
}

var marketJobTypes = []string{installJobType, updateJobType, updateAllJobType}

func isMarketJobType(value string) bool {
	return value == installJobType || value == updateJobType || value == updateAllJobType
}

// A task owns the files it submitted. Never borrow another run's parse result.
// Legacy jobs can use the install config only while they are its latest run.
func taskProgress(r *http.Request, db *gorm.DB, job orm.AsyncJob, current *orm.KnowledgeMarketInstall) (*orm.KnowledgeMarketInstall, parseProgressInfo) {
	var result struct {
		DatasetID string                  `json:"dataset_id"`
		TaskIDs   []string                `json:"task_ids"`
		Failures  []doc.MarketFileFailure `json:"failures"`
		Parse     *parseProgressInfo      `json:"parse"`
		Reason    string                  `json:"reason"`
	}
	_ = json.Unmarshal(job.ResultJSON, &result)
	if result.Reason == "stopped" {
		return &orm.KnowledgeMarketInstall{DatasetID: result.DatasetID}, parseProgressInfo{State: "canceled"}
	}
	if result.Parse != nil {
		return &orm.KnowledgeMarketInstall{DatasetID: result.DatasetID, InstallState: result.Parse.State}, *result.Parse
	}
	if (job.Status == "pending" || job.Status == "running") && len(result.TaskIDs) == 0 {
		// Download/import progress is useful, but terminal state belongs to the
		// previous attempt until this worker finishes its submission.
		if current != nil && (current.InstallState == "downloading" || current.InstallState == "importing") {
			return current, parseProgressInfo{State: "pending"}
		}
		return nil, parseProgressInfo{State: "pending"}
	}
	if len(result.TaskIDs) > 0 || len(result.Failures) > 0 {
		cfg, _ := json.Marshal(installConfig{TaskIDs: result.TaskIDs, Failures: result.Failures})
		own := &orm.KnowledgeMarketInstall{DatasetID: result.DatasetID, InstallState: "done", Config: cfg}
		parse := parseProgress(r, db, own)
		return installWithEffectiveState(own, parse), parse
	}
	if result.Reason == "no_change" || result.Reason == "not_installed" || job.JobType == updateAllJobType {
		return &orm.KnowledgeMarketInstall{DatasetID: result.DatasetID, InstallState: "done"}, parseProgressInfo{State: "done"}
	}
	var newer int64
	err := db.WithContext(r.Context()).Model(&orm.AsyncJob{}).
		Where("create_user_id = ? AND resource_id = ? AND job_type IN ? AND (created_at > ? OR (created_at = ? AND id > ?))", job.CreateUserID, job.ResourceID, marketJobTypes, job.CreatedAt, job.CreatedAt, job.ID).Count(&newer).Error
	if err == nil && newer == 0 && current != nil {
		parse := parseProgress(r, db, current)
		return installWithEffectiveState(current, parse), parse
	}
	return &orm.KnowledgeMarketInstall{DatasetID: result.DatasetID, InstallState: "done"}, parseProgressInfo{State: "done"}
}

// loadMarketItemByID loads one item or returns nil when the id is missing
// (a removed catalog item must not fail task history queries).
func loadMarketItemByID(r *http.Request, db *gorm.DB, id string) (*orm.KnowledgeMarketItem, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	var row orm.KnowledgeMarketItem
	err := db.WithContext(r.Context()).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func loadMarketItemsByIDs(r *http.Request, db *gorm.DB, ids []string) (map[string]*orm.KnowledgeMarketItem, error) {
	out := make(map[string]*orm.KnowledgeMarketItem, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []orm.KnowledgeMarketItem
	if err := db.WithContext(r.Context()).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

// loadInstall loads the install row of one (user, item) pair, if present.
func loadInstall(r *http.Request, db *gorm.DB, userID, itemID string) (*orm.KnowledgeMarketInstall, error) {
	if strings.TrimSpace(itemID) == "" {
		return nil, nil
	}
	var row orm.KnowledgeMarketInstall
	err := db.WithContext(r.Context()).
		Where("user_id = ? AND market_item_id = ?", userID, itemID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func loadInstallsByItemIDs(r *http.Request, db *gorm.DB, userID string, itemIDs []string) (map[string]*orm.KnowledgeMarketInstall, error) {
	out := make(map[string]*orm.KnowledgeMarketInstall, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	var rows []orm.KnowledgeMarketInstall
	if err := db.WithContext(r.Context()).
		Where("user_id = ? AND market_item_id IN ?", userID, itemIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		out[rows[i].MarketItemID] = &rows[i]
	}
	return out, nil
}

// marketPayloadDTO decodes the payload of any knowledge market job type into a
// small client-facing object.
func marketPayloadDTO(raw json.RawMessage) map[string]any {
	var install installJobPayload
	if err := json.Unmarshal(raw, &install); err == nil && install.MarketItemID != "" {
		return map[string]any{"market_item_id": install.MarketItemID, "revision": install.Revision}
	}
	var upd updateJobPayload
	if err := json.Unmarshal(raw, &upd); err == nil && upd.MarketItemID != "" {
		return map[string]any{"market_item_id": upd.MarketItemID, "revision": upd.Revision, "force": upd.Force}
	}
	return nil
}

// marketResultDTO decodes the structured success result of any knowledge
// market job (install/update/update-all).
func marketResultDTO(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return nil
	}
	delete(m, "dispatched_task_ids")
	return m
}

// parseProgressInfo is the aggregated parsing state of the install-submitted
// tasks.
type parseProgressInfo = doc.MarketParseProgressInfo

func parseProgress(r *http.Request, db *gorm.DB, install *orm.KnowledgeMarketInstall) parseProgressInfo {
	if install == nil {
		return parseProgressInfo{State: "pending"}
	}
	cfg := decodeInstallConfig(install)
	return doc.MarketParseProgress(r.Context(), db, install.DatasetID, cfg.TaskIDs, cfg.Failures)
}

// installWithEffectiveState returns a copy whose state reflects the parse
// tasks submitted by the install/update job. Persisted "done" means the files
// were submitted successfully; the API only exposes "done" after every parse
// task has actually succeeded.
func installWithEffectiveState(install *orm.KnowledgeMarketInstall, parse parseProgressInfo) *orm.KnowledgeMarketInstall {
	if install == nil {
		return nil
	}
	copy := *install
	if copy.InstallState != string(orm.InstallStateDone) && copy.InstallState != string(orm.InstallStateVectorizing) {
		return &copy
	}
	if parse.Total == 0 {
		return &copy
	}
	switch parse.State {
	case "parsing", "pending", "unknown":
		copy.InstallState = string(orm.InstallStateVectorizing)
	case "partial_failed":
		copy.InstallState = string(orm.InstallStatePartialFailed)
	case "failed":
		copy.InstallState = string(orm.InstallStateFailed)
	case "canceled", "partial_canceled":
		copy.InstallState = string(orm.InstallStatePartialFailed)
	case "done":
		copy.InstallState = string(orm.InstallStateDone)
	}
	return &copy
}

// installStageAndPercent derives the stage of the install chain and the
// overall 0-100 percent for a single progress bar:
// downloading 0->40, importing 40->60, parsing 60->100, done 100.
func installStageAndPercent(job orm.AsyncJob, install *orm.KnowledgeMarketInstall, parse parseProgressInfo) (string, int64) {
	if job.Status == "pending" {
		return "pending", 0
	}
	phase := phaseStage(install, job, parse)
	if job.Status == "succeeded" && parse.Total > 0 {
		return parse.State, overallPercent(parse.State, job, parse)
	}
	if job.Status == "running" {
		if phase != "downloading" && phase != "importing" {
			return "running", 0
		}
		return phase, overallPercent(phase, job, parse)
	}
	if job.Status == "failed" || job.Status == "canceled" {
		return "failed", overallPercent(phase, job, parse)
	}
	return phase, overallPercent(phase, job, parse)
}

// phaseStage derives the underlying install phase from the install row and the
// parse aggregation, ignoring the async job status.
func phaseStage(install *orm.KnowledgeMarketInstall, job orm.AsyncJob, parse parseProgressInfo) string {
	if install == nil {
		return "pending"
	}
	switch install.InstallState {
	case string(orm.InstallStateDownloading):
		return "downloading"
	case string(orm.InstallStateImporting):
		return "importing"
	case string(orm.InstallStateDone), string(orm.InstallStateVectorizing):
		switch parse.State {
		case "done":
			return "done"
		case "partial_failed":
			return "partial_failed"
		case "failed":
			return "failed"
		default:
			if parse.Total == 0 {
				return "done"
			}
			return "parsing"
		}
	case string(orm.InstallStatePartialFailed):
		return "partial_failed"
	case string(orm.InstallStateFailed):
		// Freeze the bar at the phase where the install failed. High-resolution
		// download progress uses total=100; a failed download in that state must
		// not be mistaken for the parsing/importing stage markers (total 2/3).
		if job.ProgressTotal > 2 {
			return "downloading"
		}
		switch {
		case job.ProgressCurrent >= 2:
			return "parsing"
		case job.ProgressCurrent >= 1:
			return "importing"
		default:
			return "downloading"
		}
	}
	return "pending"
}

// overallPercent maps a phase onto 0-100 with the fixed stage weights.
func overallPercent(phase string, job orm.AsyncJob, parse parseProgressInfo) int64 {
	var p int64
	switch phase {
	case "downloading":
		if job.ProgressTotal > 2 {
			// High-resolution download progress: current/total maps 0-100
			// onto the 0-40 download band.
			p = 40 * job.ProgressCurrent / job.ProgressTotal
		} else {
			p = 40 * job.ProgressCurrent
		}
	case "importing":
		p = 40 + 20*(job.ProgressCurrent-1)
	case "parsing", "partial_failed", "failed", "canceled", "partial_canceled", "unknown":
		if parse.Total > 0 {
			p = 60 + int64(float64(parse.Done+parse.Failed+parse.Canceled)/float64(parse.Total)*40)
		} else {
			p = 60
		}
	case "done":
		p = 100
	default:
		p = 0
	}
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}
