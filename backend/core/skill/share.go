package skill

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/store"
)

const (
	shareStatusPendingAccept = "pending_accept"
	shareStatusCompleted     = "completed"
	shareStatusRejected      = "rejected"
	shareStatusFailed        = "failed"
)

type shareTargetStatusSummary struct {
	PendingAccept int `json:"pending_accept"`
	Completed     int `json:"completed"`
	Rejected      int `json:"rejected"`
	Failed        int `json:"failed"`
}

type latestShareTargetRecord struct {
	item orm.SkillShareItem
	task orm.SkillShareTask
}

func Share(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}
	var req shareSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}

	var parent orm.SkillResource
	if err := db.WithContext(r.Context()).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&parent).Error; err != nil {
		common.ReplyErr(w, "skill not found", http.StatusNotFound)
		return
	}
	if parent.NodeType != evolution.SkillNodeTypeParent {
		common.ReplyErr(w, "only parent skill supports share", http.StatusBadRequest)
		return
	}

	targetUsers, err := expandTargetUsers(r, compactStrings(req.TargetUserIDs), compactStrings(req.TargetGroupIDs))
	if err != nil {
		common.ReplyErr(w, "expand target users failed", http.StatusInternalServerError)
		return
	}
	filtered := make([]string, 0, len(targetUsers))
	for _, target := range targetUsers {
		if target != userID {
			filtered = append(filtered, target)
		}
	}
	if len(filtered) == 0 {
		common.ReplyErr(w, "no target users to share", http.StatusBadRequest)
		return
	}

	now := time.Now()
	task := orm.SkillShareTask{
		ID:                    evolution.NewID(),
		SourceUserID:          userID,
		SourceUserName:        userName,
		SourceSkillID:         parent.ID,
		SourceCategory:        parent.Category,
		SourceParentSkillName: parent.SkillName,
		SourceRelativeRoot:    filepath.ToSlash(filepath.Join(parent.Category, parent.SkillName)),
		Message:               strings.TrimSpace(req.Message),
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	items := make([]orm.SkillShareItem, 0, len(filtered))
	targetUserNames := resolveShareTargetUserNames(r, db, filtered)
	for _, target := range filtered {
		targetUserName := firstNonEmpty(targetUserNames[target], target)
		items = append(items, orm.SkillShareItem{
			ID:             evolution.NewID(),
			ShareTaskID:    task.ID,
			TargetUserID:   target,
			TargetUserName: targetUserName,
			Status:         shareStatusPendingAccept,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		return tx.Create(&items).Error
	}); err != nil {
		common.ReplyErr(w, "create share task failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"share_task_id": task.ID, "items": items})
}

func IncomingShares(w http.ResponseWriter, r *http.Request) {
	listShares(w, r, true)
}

func OutgoingShares(w http.ResponseWriter, r *http.Request) {
	listShares(w, r, false)
}

func ListSkillShareTargets(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}
	var parent orm.SkillResource
	if err := db.WithContext(r.Context()).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&parent).Error; err != nil {
		common.ReplyErr(w, "skill not found", http.StatusNotFound)
		return
	}
	if parent.NodeType != evolution.SkillNodeTypeParent {
		common.ReplyErr(w, "only parent skill supports share status query", http.StatusBadRequest)
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	pageSize := parsePositiveInt(r.URL.Query().Get("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	var tasks []orm.SkillShareTask
	if err := db.WithContext(r.Context()).
		Where("source_user_id = ? AND source_skill_id = ?", userID, skillID).
		Find(&tasks).Error; err != nil {
		common.ReplyErr(w, "query share tasks failed", http.StatusInternalServerError)
		return
	}
	summary := shareTargetStatusSummary{}
	if len(tasks) == 0 {
		common.ReplyOK(w, map[string]any{
			"skill_id":       skillID,
			"status_summary": summary,
			"items":          []any{},
			"page":           page,
			"page_size":      pageSize,
			"total":          0,
		})
		return
	}

	taskIDs := make([]string, 0, len(tasks))
	taskMap := make(map[string]orm.SkillShareTask, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ID)
		taskMap[task.ID] = task
	}

	var items []orm.SkillShareItem
	if err := db.WithContext(r.Context()).
		Where("share_task_id IN ?", taskIDs).
		Order("created_at DESC").
		Order("updated_at DESC").
		Find(&items).Error; err != nil {
		common.ReplyErr(w, "query share items failed", http.StatusInternalServerError)
		return
	}

	latestByUser := make(map[string]latestShareTargetRecord, len(items))
	for _, item := range items {
		existing, ok := latestByUser[item.TargetUserID]
		if ok && !shareItemIsNewer(item, existing.item) {
			continue
		}
		latestByUser[item.TargetUserID] = latestShareTargetRecord{
			item: item,
			task: taskMap[item.ShareTaskID],
		}
	}

	records := make([]latestShareTargetRecord, 0, len(latestByUser))
	for _, record := range latestByUser {
		switch record.item.Status {
		case shareStatusPendingAccept:
			summary.PendingAccept++
		case shareStatusCompleted:
			summary.Completed++
		case shareStatusRejected:
			summary.Rejected++
		case shareStatusFailed:
			summary.Failed++
		}
		if status != "" && record.item.Status != status {
			continue
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		return shareItemIsNewer(records[i].item, records[j].item)
	})

	total := len(records)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	respItems := make([]map[string]any, 0, end-start)
	resolvedTargetUserNames := resolveShareTargetUserNames(r, db, collectLatestShareTargetUserIDs(records[start:end]))
	for _, record := range records[start:end] {
		respItems = append(respItems, map[string]any{
			"target_user_id":       record.item.TargetUserID,
			"target_user_name":     shareTargetDisplayName(record.item.TargetUserID, record.item.TargetUserName, resolvedTargetUserNames),
			"status":               record.item.Status,
			"share_item_id":        record.item.ID,
			"share_task_id":        record.item.ShareTaskID,
			"message":              record.task.Message,
			"accepted_at":          record.item.AcceptedAt,
			"rejected_at":          record.item.RejectedAt,
			"target_root_skill_id": record.item.TargetRootSkillID,
			"error_message":        record.item.ErrorMessage,
			"shared_at":            record.item.CreatedAt,
			"updated_at":           record.item.UpdatedAt,
		})
	}

	common.ReplyOK(w, map[string]any{
		"skill_id":       skillID,
		"status_summary": summary,
		"items":          respItems,
		"page":           page,
		"page_size":      pageSize,
		"total":          total,
	})
}

func GetShareItem(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	shareItemID := common.PathVar(r, "share_item_id")
	if shareItemID == "" {
		common.ReplyErr(w, "missing share_item_id", http.StatusBadRequest)
		return
	}
	item, task, err := loadShareItem(r.Context(), db, shareItemID)
	if err != nil {
		common.ReplyErr(w, "share item not found", http.StatusNotFound)
		return
	}
	if item.TargetUserID != userID && task.SourceUserID != userID {
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
		return
	}
	detail, err := getSkillDetail(r.Context(), db, task.SourceUserID, task.SourceSkillID)
	if err != nil {
		common.ReplyErr(w, "load source skill failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"share_item_id": shareItemID,
		"status":        item.Status,
		"message":       task.Message,
		"source":        detail,
	})
}

func AcceptShare(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	shareItemID := common.PathVar(r, "share_item_id")
	if shareItemID == "" {
		common.ReplyErr(w, "missing share_item_id", http.StatusBadRequest)
		return
	}
	item, task, err := loadShareItem(r.Context(), db, shareItemID)
	if err != nil {
		common.ReplyErr(w, "share item not found", http.StatusNotFound)
		return
	}
	if item.TargetUserID != userID {
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
		return
	}
	if item.Status != shareStatusPendingAccept {
		common.ReplyErr(w, "share item is not pending_accept", http.StatusConflict)
		return
	}

	var sourceParent orm.SkillResource
	if err := db.WithContext(r.Context()).Where("id = ? AND owner_user_id = ?", task.SourceSkillID, task.SourceUserID).Take(&sourceParent).Error; err != nil {
		common.ReplyErr(w, "source skill not found", http.StatusNotFound)
		return
	}
	var sourceChildren []orm.SkillResource
	if err := db.WithContext(r.Context()).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", task.SourceUserID, evolution.SkillNodeTypeChild, sourceParent.Category, sourceParent.SkillName).
		Find(&sourceChildren).Error; err != nil {
		common.ReplyErr(w, "query source children failed", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	targetParentID := evolution.NewID()
	targetParent := sourceParent
	targetParent.ID = targetParentID
	targetParent.OwnerUserID = userID
	targetParent.OwnerUserName = userName
	targetParent.CreateUserID = userID
	targetParent.CreateUserName = userName
	targetParent.DraftSourceVersion = 0
	targetParent.DraftContent = ""
	targetParent.DraftStatus = ""
	targetParent.DraftUpdatedAt = nil
	targetParent.UpdateStatus = evolution.UpdateStatusUpToDate
	targetParent.Ext = evolution.WithDraftSuggestionIDs(nil, nil)
	targetParent.CreatedAt = now
	targetParent.UpdatedAt = now

	targetChildren := make([]orm.SkillResource, 0, len(sourceChildren))
	for _, sourceChild := range sourceChildren {
		child := sourceChild
		child.ID = evolution.NewID()
		child.OwnerUserID = userID
		child.OwnerUserName = userName
		child.CreateUserID = userID
		child.CreateUserName = userName
		child.UpdateStatus = evolution.UpdateStatusUpToDate
		child.CreatedAt = now
		child.UpdatedAt = now
		targetChildren = append(targetChildren, child)
	}

	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&targetParent).Error; err != nil {
			return err
		}
		if len(targetChildren) > 0 {
			if err := tx.Create(&targetChildren).Error; err != nil {
				return err
			}
		}
		return tx.Model(&orm.SkillShareItem{}).Where("id = ?", item.ID).Updates(map[string]any{
			"status":               shareStatusCompleted,
			"accepted_at":          now,
			"updated_at":           now,
			"target_relative_root": task.SourceRelativeRoot,
			"target_root_skill_id": targetParentID,
			"error_message":        "",
		}).Error
	}); err != nil {
		common.ReplyErr(w, "accept share failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"accepted": true, "target_root_skill_id": targetParentID})
}

func RejectShare(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	shareItemID := common.PathVar(r, "share_item_id")
	if shareItemID == "" {
		common.ReplyErr(w, "missing share_item_id", http.StatusBadRequest)
		return
	}
	item, _, err := loadShareItem(r.Context(), db, shareItemID)
	if err != nil {
		common.ReplyErr(w, "share item not found", http.StatusNotFound)
		return
	}
	if item.TargetUserID != userID {
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
		return
	}
	if item.Status != shareStatusPendingAccept {
		common.ReplyErr(w, "share item is not pending_accept", http.StatusConflict)
		return
	}
	now := time.Now()
	if err := db.WithContext(r.Context()).Model(&orm.SkillShareItem{}).Where("id = ?", item.ID).Updates(map[string]any{
		"status":      shareStatusRejected,
		"rejected_at": now,
		"updated_at":  now,
	}).Error; err != nil {
		common.ReplyErr(w, "reject share failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"rejected": true})
}

func listShares(w http.ResponseWriter, r *http.Request, incoming bool) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	pageSize := parsePositiveInt(r.URL.Query().Get("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	var items []orm.SkillShareItem
	query := db.WithContext(r.Context()).Model(&orm.SkillShareItem{})
	if incoming {
		query = query.Where("target_user_id = ?", userID)
	} else {
		var taskIDs []string
		if err := db.WithContext(r.Context()).Model(&orm.SkillShareTask{}).Where("source_user_id = ?", userID).Pluck("id", &taskIDs).Error; err != nil {
			common.ReplyErr(w, "query share tasks failed", http.StatusInternalServerError)
			return
		}
		if len(taskIDs) == 0 {
			common.ReplyOK(w, map[string]any{"items": []any{}, "page": page, "page_size": pageSize, "total": 0})
			return
		}
		query = query.Where("share_task_id IN ?", taskIDs)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ReplyErr(w, "query share items failed", http.StatusInternalServerError)
		return
	}
	if err := query.Order("created_at DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error; err != nil {
		common.ReplyErr(w, "query share items failed", http.StatusInternalServerError)
		return
	}
	taskIDs := make([]string, 0, len(items))
	for _, item := range items {
		taskIDs = append(taskIDs, item.ShareTaskID)
	}
	var tasks []orm.SkillShareTask
	if len(taskIDs) > 0 {
		_ = db.WithContext(r.Context()).Where("id IN ?", taskIDs).Find(&tasks).Error
	}
	taskMap := make(map[string]orm.SkillShareTask, len(tasks))
	for _, task := range tasks {
		taskMap[task.ID] = task
	}
	resolvedTargetUserNames := resolveShareTargetUserNames(r, db, collectShareTargetUserIDs(items))
	resp := make([]map[string]any, 0, len(items))
	for _, item := range items {
		task := taskMap[item.ShareTaskID]
		resp = append(resp, map[string]any{
			"share_item_id":            item.ID,
			"share_task_id":            item.ShareTaskID,
			"status":                   item.Status,
			"source_user_id":           task.SourceUserID,
			"source_user_name":         task.SourceUserName,
			"target_user_id":           item.TargetUserID,
			"target_user_name":         shareTargetDisplayName(item.TargetUserID, item.TargetUserName, resolvedTargetUserNames),
			"source_skill_id":          task.SourceSkillID,
			"source_category":          task.SourceCategory,
			"source_parent_skill_name": task.SourceParentSkillName,
			"message":                  task.Message,
			"accepted_at":              item.AcceptedAt,
			"rejected_at":              item.RejectedAt,
			"target_root_skill_id":     item.TargetRootSkillID,
			"error_message":            item.ErrorMessage,
			"created_at":               item.CreatedAt,
			"updated_at":               item.UpdatedAt,
		})
	}
	common.ReplyOK(w, map[string]any{
		"items":     resp,
		"page":      page,
		"page_size": pageSize,
		"total":     total,
	})
}

func loadShareItem(ctx context.Context, db *gorm.DB, shareItemID string) (*orm.SkillShareItem, *orm.SkillShareTask, error) {
	var item orm.SkillShareItem
	if err := db.WithContext(ctx).Where("id = ?", shareItemID).Take(&item).Error; err != nil {
		return nil, nil, err
	}
	var task orm.SkillShareTask
	if err := db.WithContext(ctx).Where("id = ?", item.ShareTaskID).Take(&task).Error; err != nil {
		return nil, nil, err
	}
	return &item, &task, nil
}

func shareItemIsNewer(candidate, current orm.SkillShareItem) bool {
	if candidate.CreatedAt.After(current.CreatedAt) {
		return true
	}
	if candidate.CreatedAt.Before(current.CreatedAt) {
		return false
	}
	if candidate.UpdatedAt.After(current.UpdatedAt) {
		return true
	}
	if candidate.UpdatedAt.Before(current.UpdatedAt) {
		return false
	}
	return strings.Compare(strings.TrimSpace(candidate.ID), strings.TrimSpace(current.ID)) > 0
}

func expandTargetUsers(r *http.Request, userIDs, groupIDs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(userIDs))
	out := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	if len(groupIDs) == 0 {
		return out, nil
	}
	// Group shares must respect auth-service active_only filtering so disabled users
	// are not reintroduced from stale local membership cache rows.
	for _, userID := range common.FetchGroupUserIDsFromAuthService(r, groupIDs) {
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	sort.Strings(out)
	return out, nil
}

func collectLatestShareTargetUserIDs(records []latestShareTargetRecord) []string {
	userIDs := make([]string, 0, len(records))
	for _, record := range records {
		userIDs = append(userIDs, record.item.TargetUserID)
	}
	return userIDs
}

func collectShareTargetUserIDs(items []orm.SkillShareItem) []string {
	userIDs := make([]string, 0, len(items))
	for _, item := range items {
		userIDs = append(userIDs, item.TargetUserID)
	}
	return userIDs
}

func shareTargetDisplayName(userID, storedName string, resolved map[string]string) string {
	if resolvedName := strings.TrimSpace(resolved[userID]); resolvedName != "" {
		return resolvedName
	}
	return firstNonEmpty(storedName, userID)
}

func resolveShareTargetUserNames(r *http.Request, db *gorm.DB, userIDs []string) map[string]string {
	userIDs = compactStrings(userIDs)
	if len(userIDs) == 0 {
		return map[string]string{}
	}

	out := common.FetchUserNamesFromAuthService(r, userIDs)
	unresolved := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		if strings.TrimSpace(out[userID]) == "" {
			unresolved = append(unresolved, userID)
		}
	}
	if len(unresolved) == 0 {
		return out
	}

	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	for userID, userName := range loadShareTargetUserNamesFromSkillResources(ctx, db, unresolved) {
		if strings.TrimSpace(userName) != "" {
			out[userID] = userName
		}
	}
	return out
}

func loadShareTargetUserNamesFromSkillResources(ctx context.Context, db *gorm.DB, userIDs []string) map[string]string {
	out := make(map[string]string, len(userIDs))
	if db == nil || len(userIDs) == 0 {
		return out
	}

	type skillOwnerRow struct {
		UserID        string    `gorm:"column:owner_user_id"`
		OwnerUserName string    `gorm:"column:owner_user_name"`
		UpdatedAt     time.Time `gorm:"column:updated_at"`
	}
	var rows []skillOwnerRow
	if err := db.WithContext(ctx).
		Model(&orm.SkillResource{}).
		Select("owner_user_id", "owner_user_name", "updated_at").
		Where("owner_user_id IN ? AND owner_user_name <> ''", userIDs).
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		userID := strings.TrimSpace(row.UserID)
		if userID == "" {
			continue
		}
		if _, exists := out[userID]; exists {
			continue
		}
		if userName := strings.TrimSpace(row.OwnerUserName); userName != "" {
			out[userID] = userName
		}
	}
	return out
}
