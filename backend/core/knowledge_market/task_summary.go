package knowledge_market

import (
	"net/http"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

// Count the same nonterminal states as task rows, independently of pagination
// and filters. A successful import can still have unfinished parse work.
func unfinishedMarketTaskCount(r *http.Request, db *gorm.DB, userID string) (int, error) {
	var jobs []orm.AsyncJob
	if err := db.WithContext(r.Context()).Where("create_user_id = ? AND job_type IN ?", userID, marketJobTypes).
		Order("created_at DESC, id DESC").Find(&jobs).Error; err != nil {
		return 0, err
	}
	itemIDs := make([]string, 0, len(jobs))
	latest := make(map[string]string)
	for _, job := range jobs {
		if _, ok := latest[job.ResourceID]; !ok {
			latest[job.ResourceID] = job.ID
			itemIDs = append(itemIDs, job.ResourceID)
		}
	}
	installs, err := loadInstallsByItemIDs(r, db, userID, itemIDs)
	if err != nil {
		return 0, err
	}
	type liveTask struct {
		job orm.AsyncJob
		cfg installConfig
	}
	live := make(map[string][]liveTask)
	count := 0
	for _, job := range jobs {
		install, snapshot := taskProgressSource(job, installs[job.ResourceID], func() bool { return latest[job.ResourceID] == job.ID })
		if snapshot != nil {
			if !marketTaskTerminal(marketDisplay(job, *snapshot)) {
				count++
			}
			continue
		}
		live[install.DatasetID] = append(live[install.DatasetID], liveTask{job, decodeInstallConfig(install)})
	}
	for datasetID, tasks := range live {
		ids := make([]string, 0)
		seen := make(map[string]bool)
		for _, task := range tasks {
			for _, id := range task.cfg.TaskIDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		// Read files once per dataset, without probing workers or loading each
		// task's detail/files endpoint. Historical snapshots need no live reads.
		files, err := doc.MarketTaskStates(r.Context(), db, datasetID, ids)
		if err != nil {
			return 0, err
		}
		for _, task := range tasks {
			parse := doc.MarketParseProgressFromStates(task.cfg.TaskIDs, task.cfg.Failures, files)
			if !marketTaskTerminal(marketDisplay(task.job, parse)) {
				count++
			}
		}
	}
	return count, nil
}
