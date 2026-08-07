package knowledge_market

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/doc"
	"lazymind/core/store"
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
		common.ReplyErr(w, "X-User-Id is required", http.StatusBadRequest)
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
		common.ReplyErr(w, "X-User-Id is required", http.StatusBadRequest)
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
		Where("id = ? AND create_user_id = ?", jobID, userID).
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
	parse := parseProgress(r, db, install)
	stage, overall := installStageAndPercent(job, install, parse)
	data := taskItemDTO(job, item, install)
	data["attempt_count"] = job.AttemptCount
	data["max_attempts"] = job.MaxAttempts
	data["started_at"] = job.StartedAt
	data["updated_at"] = job.UpdatedAt
	data["payload"] = marketPayloadDTO(job.PayloadJSON)
	data["result"] = marketResultDTO(job.ResultJSON)
	data["stage"] = stage
	data["overall_percent"] = overall
	data["parse"] = parse
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
		common.ReplyErr(w, "X-User-Id is required", http.StatusBadRequest)
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
	activeByItem, batchRunning, err := activeMarketJobsForUser(r, db, userID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		name, icon, domain := "", "", ""
		if item := itemByID[row.MarketItemID]; item != nil {
			name = item.Name
			icon = item.Icon
			domain = item.Domain
		}
		items = append(items, map[string]any{
			"market_item_id": row.MarketItemID,
			"name":           name,
			"icon":           icon,
			"domain":         domain,
			"install_state":  row.InstallState,
			"dataset_id":     row.DatasetID,
			"installed_at":   row.InstalledAt,
			"updated_at":     row.UpdatedAt,
			"active":         activeByItem[row.MarketItemID] || batchRunning,
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
	for _, job := range jobs {
		items = append(items, taskItemDTO(job, itemByID[job.ResourceID], installByItem[job.ResourceID]))
	}
	return items, nil
}

// activeMarketJobsForUser returns the set of market items with an in-flight
// install/update job for the user, plus whether a one-click batch is running.
func activeMarketJobsForUser(r *http.Request, db *gorm.DB, userID string) (map[string]bool, bool, error) {
	active := make(map[string]bool)
	batchRunning := false
	if strings.TrimSpace(userID) == "" {
		return active, false, nil
	}
	var rows []orm.AsyncJob
	if err := db.WithContext(r.Context()).
		Where("create_user_id = ? AND status IN ? AND job_type IN ?",
			userID, []string{"pending", "running"}, []string{installJobType, updateJobType, updateAllJobType}).
		Find(&rows).Error; err != nil {
		return nil, false, err
	}
	for _, row := range rows {
		if row.JobType == updateAllJobType {
			batchRunning = true
			continue
		}
		if id := strings.TrimSpace(row.ResourceID); id != "" {
			active[id] = true
		}
	}
	return active, batchRunning, nil
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
	return m
}

// parseProgressInfo is the aggregated parsing state of the install-submitted
// tasks.
type parseProgressInfo struct {
	State   string `json:"state"` // pending | parsing | done | failed
	Total   int    `json:"total"`
	Pending int    `json:"pending"`
	Parsing int    `json:"parsing"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
}

// marketTaskExt is the slice of tasks.ext needed to group parse progress; it
// mirrors doc.taskExt without importing the unexported type. It is only a
// fallback for tasks that were never submitted to the doc service.
type marketTaskExt struct {
	TaskState string `json:"task_state"`
}

// parseProgress aggregates the parse tasks recorded in the install config
// (task_ids) so the frontend can show the parsing stage of the install chain.
// The authoritative state lives in the doc-service task table
// (lazyllm_doc_service_tasks), linked via tasks.lazyllm_task_id; ext.task_state
// is only consulted as a fallback for legacy or not-yet-submitted tasks.
func parseProgress(r *http.Request, db *gorm.DB, install *orm.KnowledgeMarketInstall) parseProgressInfo {
	zero := parseProgressInfo{State: "pending"}
	if install == nil {
		return zero
	}
	var cfg installConfig
	if err := json.Unmarshal(install.Config, &cfg); err != nil || len(cfg.TaskIDs) == 0 {
		return zero
	}
	var rows []orm.Task
	if err := db.WithContext(r.Context()).
		Where("id IN ? AND dataset_id = ? AND deleted_at IS NULL", cfg.TaskIDs, install.DatasetID).
		Find(&rows).Error; err != nil {
		return zero
	}
	statusByTaskID, statusByDocID := loadDocServiceTaskStatuses(r, install.DatasetID, rows)
	p := parseProgressInfo{}
	for _, row := range rows {
		state := ""
		if s, ok := statusByTaskID[row.LazyllmTaskID]; ok {
			state = s
		} else if s, ok := statusByDocID[row.DocID]; ok {
			state = s
		}
		if state == "" {
			var ext marketTaskExt
			_ = json.Unmarshal(row.Ext, &ext)
			state = ext.TaskState
		}
		switch parseGroup(state) {
		case "pending":
			p.Pending++
		case "parsing":
			p.Parsing++
		case "done":
			p.Done++
		case "failed":
			p.Failed++
		}
	}
	p.Total = len(cfg.TaskIDs)
	switch {
	case p.Failed > 0:
		p.State = "failed"
	case p.Total > 0 && p.Done == p.Total:
		p.State = "done"
	case p.Total > 0:
		p.State = "parsing"
	default:
		p.State = "pending"
	}
	return p
}

// loadDocServiceTaskStatuses reads the authoritative parse statuses from the
// doc-service task table (lazyllm_doc_service_tasks), keyed by lazyllm task id
// with a doc-id fallback for tasks whose lazyllm_task_id is empty or stale. A
// lookup failure degrades to empty maps so callers fall back to ext.task_state.
func loadDocServiceTaskStatuses(r *http.Request, datasetID string, rows []orm.Task) (byTaskID, byDocID map[string]string) {
	byTaskID = make(map[string]string)
	byDocID = make(map[string]string)
	taskIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if id := strings.TrimSpace(row.LazyllmTaskID); id != "" {
			taskIDs = append(taskIDs, id)
		}
	}
	if len(taskIDs) > 0 {
		var extTasks []readonlyorm.LazyLLMDocServiceTaskRow
		if err := store.LazyLLMDB().WithContext(r.Context()).
			Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).
			Where("task_id IN ?", taskIDs).
			Find(&extTasks).Error; err == nil {
			for _, task := range extTasks {
				if s := strings.TrimSpace(task.Status); s != "" {
					byTaskID[task.TaskID] = s
				}
			}
		}
	}

	missedDocIDs := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, ok := byTaskID[row.LazyllmTaskID]; ok {
			continue
		}
		docID := strings.TrimSpace(row.DocID)
		if docID == "" || seen[docID] {
			continue
		}
		seen[docID] = true
		missedDocIDs = append(missedDocIDs, docID)
	}
	if len(missedDocIDs) > 0 {
		var extDocs []readonlyorm.LazyLLMDocServiceTaskRow
		if err := store.LazyLLMDB().WithContext(r.Context()).
			Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).
			Where("doc_id IN ? AND kb_id = ?", missedDocIDs, datasetID).
			Order("updated_at DESC").
			Find(&extDocs).Error; err == nil {
			for _, task := range extDocs {
				if _, ok := byDocID[task.DocID]; ok {
					continue
				}
				if s := strings.TrimSpace(task.Status); s != "" {
					byDocID[task.DocID] = s
				}
			}
		}
	}
	return byTaskID, byDocID
}

// parseGroup maps one task state (doc-service status or the ext.task_state
// fallback) onto a progress bucket. Doc-service statuses are normalized with
// the same helper the local-upload task panel uses so the two surfaces agree.
func parseGroup(state string) string {
	switch doc.NormalizeTaskStateForUI(state) {
	case "WORKING":
		return "parsing"
	case "SUCCESS":
		return "done"
	case "FAILED", "CANCELED":
		return "failed"
	default:
		return "pending"
	}
}

// installStageAndPercent derives the stage of the install chain and the
// overall 0-100 percent for a single progress bar:
// downloading 0->40, importing 40->60, parsing 60->100, done 100.
func installStageAndPercent(job orm.AsyncJob, install *orm.KnowledgeMarketInstall, parse parseProgressInfo) (string, int64) {
	phase := phaseStage(install, job, parse)
	stage := phase
	if job.Status == "failed" || job.Status == "canceled" {
		stage = "failed"
	}
	return stage, overallPercent(phase, job, parse)
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
	case string(orm.InstallStateDone):
		switch parse.State {
		case "done":
			return "done"
		case "failed":
			return "failed"
		default:
			return "parsing"
		}
	case string(orm.InstallStateFailed):
		// Freeze the bar at the phase where the install failed.
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
		p = 40 * job.ProgressCurrent
	case "importing":
		p = 40 + 20*(job.ProgressCurrent-1)
	case "parsing", "failed":
		if parse.Total > 0 {
			p = 60 + int64(float64(parse.Done)/float64(parse.Total)*40)
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
