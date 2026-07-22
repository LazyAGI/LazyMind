package scheduler

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type dependencyInput struct {
	SourceScheduleID string   `json:"source_schedule_id"`
	SourceClientKey  string   `json:"source_client_key"`
	WindowType       string   `json:"window_type"`
	ContentTypes     []string `json:"content_types"`
	IncompletePolicy string   `json:"incomplete_policy"`
	MaxWaitSeconds   int      `json:"max_wait_seconds"`
}

type dependencyResponse struct {
	ID               string   `json:"id"`
	SourceScheduleID string   `json:"source_schedule_id"`
	SourceName       string   `json:"source_name"`
	WindowType       string   `json:"window_type"`
	ContentTypes     []string `json:"content_types"`
	IncompletePolicy string   `json:"incomplete_policy"`
	MaxWaitSeconds   int      `json:"max_wait_seconds"`
}

func normalizeDependency(d dependencyInput) dependencyInput {
	if d.WindowType == "" {
		d.WindowType = "between_target_fires"
	}
	if len(d.ContentTypes) == 0 {
		d.ContentTypes = []string{"final_answer", "artifacts"}
	}
	if d.IncompletePolicy == "" {
		d.IncompletePolicy = "wait_then_run_with_warning"
	}
	if d.MaxWaitSeconds <= 0 {
		d.MaxWaitSeconds = 7200
	}
	return d
}

func replaceDependencies(tx *gorm.DB, userID, targetID string, deps []dependencyInput) error {
	if err := tx.Where("user_id = ? AND target_schedule_id = ?", userID, targetID).Delete(&orm.ScheduleDependency{}).Error; err != nil {
		return err
	}
	for _, raw := range deps {
		d := normalizeDependency(raw)
		if d.SourceScheduleID == "" || d.SourceScheduleID == targetID {
			return gorm.ErrInvalidData
		}
		var count int64
		if err := tx.Model(&orm.UserSchedule{}).Where("id = ? AND user_id = ?", d.SourceScheduleID, userID).Count(&count).Error; err != nil || count == 0 {
			return gorm.ErrRecordNotFound
		}
		var source, target orm.UserSchedule
		if err := tx.Select("id", "cron_expr").Where("id = ? AND user_id = ?", d.SourceScheduleID, userID).First(&source).Error; err != nil {
			return err
		}
		if err := tx.Select("id", "cron_expr").Where("id = ? AND user_id = ?", targetID, userID).First(&target).Error; err != nil {
			return err
		}
		if scheduleAnnualFrequency(target.CronExpr) > scheduleAnnualFrequency(source.CronExpr) {
			return errDependencyTooSparse
		}
		if wouldCreateCycle(tx, userID, d.SourceScheduleID, targetID) {
			return errDependencyCycle
		}
		content, _ := json.Marshal(d.ContentTypes)
		now := time.Now().UTC()
		row := orm.ScheduleDependency{ID: common.GeneratePrefixedID("dep_", 36), UserID: userID, SourceScheduleID: d.SourceScheduleID, TargetScheduleID: targetID, WindowType: d.WindowType, ContentTypesJSON: content, IncompletePolicy: d.IncompletePolicy, MaxWaitSeconds: d.MaxWaitSeconds, Enabled: true, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

var errDependencyCycle = &dependencyError{"dependency would create a cycle"}
var errDependencyTooSparse = &dependencyError{"target schedule must run no more frequently than its source schedule"}

type dependencyError struct{ message string }

func (e *dependencyError) Error() string { return e.message }

func scheduleAnnualFrequency(expr string) int {
	interval, _, cronExpr, err := parseCadenceExpr(expr)
	if err != nil {
		return 1 << 30
	}
	fields := strings.Fields(cronExpr)
	if len(fields) != 5 {
		return 1 << 30
	}
	if fields[2] != "*" {
		return len(strings.Split(fields[2], ",")) * 12 / interval
	}
	if fields[4] != "*" {
		return len(strings.Split(fields[4], ",")) * 52 / interval
	}
	return 365
}

func wouldCreateCycle(db *gorm.DB, userID, sourceID, targetID string) bool {
	// Adding source -> target is invalid when target already reaches source.
	type edge struct{ Source, Target string }
	var rows []edge
	_ = db.Model(&orm.ScheduleDependency{}).Select("source_schedule_id AS source, target_schedule_id AS target").Where("user_id = ? AND enabled = true", userID).Find(&rows).Error
	graph := map[string][]string{}
	for _, e := range rows {
		graph[e.Source] = append(graph[e.Source], e.Target)
	}
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if id == sourceID {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, next := range graph[id] {
			if visit(next) {
				return true
			}
		}
		return false
	}
	return visit(targetID)
}

func loadDependencies(db *gorm.DB, userID string, targetIDs []string) map[string][]dependencyResponse {
	result := map[string][]dependencyResponse{}
	if len(targetIDs) == 0 {
		return result
	}
	type row struct {
		orm.ScheduleDependency
		SourceName string `gorm:"column:source_name"`
	}
	var rows []row
	_ = db.Table("schedule_dependencies sd").Select("sd.*, us.name AS source_name").Joins("JOIN user_schedules us ON us.id = sd.source_schedule_id").Where("sd.user_id = ? AND sd.target_schedule_id IN ? AND sd.enabled = true", userID, targetIDs).Order("sd.created_at ASC").Find(&rows).Error
	for _, r := range rows {
		var content []string
		_ = json.Unmarshal(r.ContentTypesJSON, &content)
		result[r.TargetScheduleID] = append(result[r.TargetScheduleID], dependencyResponse{ID: r.ID, SourceScheduleID: r.SourceScheduleID, SourceName: r.SourceName, WindowType: r.WindowType, ContentTypes: content, IncompletePolicy: r.IncompletePolicy, MaxWaitSeconds: r.MaxWaitSeconds})
	}
	return result
}

type groupResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Remark    string    `json:"remark"`
	Timezone  string    `json:"timezone"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	TaskCount int       `json:"task_count"`
}

func ListGroupsHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	db := store.DB()
	var groups []orm.AutomationGroup
	if err := db.Where("user_id = ?", userID).Order("created_at DESC").Find(&groups).Error; err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	var counts []struct {
		GroupID string `gorm:"column:group_id"`
		Count   int    `gorm:"column:count"`
	}
	_ = db.Model(&orm.UserSchedule{}).Select("group_id, count(*) AS count").Where("user_id = ? AND group_id IS NOT NULL", userID).Group("group_id").Find(&counts).Error
	countMap := map[string]int{}
	for _, c := range counts {
		countMap[c.GroupID] = c.Count
	}
	items := make([]groupResponse, 0, len(groups))
	for _, g := range groups {
		items = append(items, groupResponse{g.ID, g.Name, g.Remark, g.Timezone, g.Enabled, g.CreatedAt, countMap[g.ID]})
	}
	common.ReplyJSON(w, map[string]any{"items": items, "total": len(items)})
}

func CreateGroupHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	var body struct{ Name, Remark, Timezone string }
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Name) == "" {
		common.ReplyErr(w, "name required", 400)
		return
	}
	if body.Timezone == "" {
		body.Timezone = "Asia/Shanghai"
	}
	now := time.Now().UTC()
	g := orm.AutomationGroup{ID: common.GeneratePrefixedID("grp_", 36), UserID: userID, Name: strings.TrimSpace(body.Name), Remark: body.Remark, Timezone: body.Timezone, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := store.DB().Create(&g).Error; err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyJSON(w, groupResponse{g.ID, g.Name, g.Remark, g.Timezone, g.Enabled, g.CreatedAt, 0})
}

func DeleteGroupHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	id := strings.TrimPrefix(r.URL.Path, "/automation-groups/")
	db := store.DB()
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.UserSchedule{}).Where("user_id = ? AND group_id = ?", userID, id).Updates(map[string]any{"group_id": nil, "group_position": 0}).Error; err != nil {
			return err
		}
		res := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&orm.AutomationGroup{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, nil)
}

func MoveScheduleHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	path := strings.TrimPrefix(r.URL.Path, "/schedules/")
	id := strings.TrimSuffix(path, ":move")
	var body struct {
		GroupID  *string `json:"group_id"`
		Position int     `json:"position"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	db := store.DB()
	if body.GroupID != nil {
		var n int64
		db.Model(&orm.AutomationGroup{}).Where("id = ? AND user_id = ?", *body.GroupID, userID).Count(&n)
		if n == 0 {
			common.ReplyErr(w, "group not found", 404)
			return
		}
	}
	res := db.Model(&orm.UserSchedule{}).Where("id = ? AND user_id = ?", id, userID).Updates(map[string]any{"group_id": body.GroupID, "group_position": body.Position})
	if res.Error != nil {
		common.ReplyErr(w, res.Error.Error(), 500)
		return
	}
	common.ReplyOK(w, nil)
}

func BatchCreateHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	var body struct {
		Group struct {
			Name     string `json:"name"`
			Remark   string `json:"remark"`
			Timezone string `json:"timezone"`
		} `json:"group"`
		Tasks []struct {
			ClientKey      string            `json:"client_key"`
			Name           string            `json:"name"`
			Remark         string            `json:"remark"`
			CronExpr       string            `json:"cron_expr"`
			Timezone       string            `json:"timezone"`
			PromptTemplate string            `json:"prompt_template"`
			KbIDs          []string          `json:"kb_ids"`
			FileIDs        []string          `json:"file_ids"`
			Dependencies   []dependencyInput `json:"dependencies"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Group.Name) == "" {
		common.ReplyErr(w, "group name required", 400)
		return
	}
	created := map[string]string{}
	var group orm.AutomationGroup
	db := store.DB()
	for _, item := range body.Tasks {
		if err := validateScheduleDescription(r.Context(), item.PromptTemplate); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		tz := body.Group.Timezone
		if tz == "" {
			tz = "Asia/Shanghai"
		}
		group = orm.AutomationGroup{ID: common.GeneratePrefixedID("grp_", 36), UserID: userID, Name: body.Group.Name, Remark: body.Group.Remark, Timezone: tz, Enabled: true, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		for i, item := range body.Tasks {
			if item.ClientKey == "" || created[item.ClientKey] != "" || item.CronExpr == "" || item.PromptTemplate == "" {
				return gorm.ErrInvalidData
			}
			s := orm.UserSchedule{UserID: userID, Name: item.Name, Remark: item.Remark, CronExpr: item.CronExpr, Timezone: tz, PromptTemplate: item.PromptTemplate, Enabled: true, GroupID: &group.ID, GroupPosition: i}
			kb, _ := json.Marshal(item.KbIDs)
			files, _ := json.Marshal(item.FileIDs)
			s.KbIDs = string(kb)
			s.FileIDs = string(files)
			if err := CreateSchedule(r.Context(), tx, &s); err != nil {
				return err
			}
			created[item.ClientKey] = s.ID
			deps := make([]dependencyInput, len(item.Dependencies))
			copy(deps, item.Dependencies)
			for j := range deps {
				if deps[j].SourceClientKey != "" {
					id := created[deps[j].SourceClientKey]
					if id == "" {
						return gorm.ErrInvalidData
					}
					deps[j].SourceScheduleID = id
				}
			}
			if err := replaceDependencies(tx, userID, s.ID, deps); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	keys := make([]string, 0, len(created))
	for k := range created {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	common.ReplyJSON(w, map[string]any{"group_id": group.ID, "schedule_ids": created})
}
