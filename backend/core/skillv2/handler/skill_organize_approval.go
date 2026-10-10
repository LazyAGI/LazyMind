package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	skillfs "lazymind/core/skillv2/fs"
	skillrevision "lazymind/core/skillv2/revision"
	skillservice "lazymind/core/skillv2/service"
)

const (
	organizeApprovalPending  = "pending"
	organizeApprovalAccepted = "accepted"
	organizeApprovalRejected = "rejected"
)

type organizeApprovalItem struct {
	ID              string                `json:"id"`
	Type            string                `json:"type"`
	SourceKeys      []string              `json:"source_keys"`
	TargetSourceKey string                `json:"target_source_key"`
	TargetName      string                `json:"target_name"`
	Content         string                `json:"content"`
	SearchMetadata  *organizeSearchFields `json:"search_metadata"`
	DeleteKeys      []string              `json:"delete_keys"`
	DependsOn       []string              `json:"depends_on"`
}

type organizeSearchFields struct {
	Field    *string   `json:"field"`
	Tags     *[]string `json:"tags"`
	Aliases  *[]string `json:"aliases"`
	Keywords *[]string `json:"keywords"`
}

type organizeItemBinding struct {
	SourceSkillIDs []string          `json:"source_skill_ids"`
	TargetSkillID  string            `json:"target_skill_id"`
	DeleteSkillIDs []string          `json:"delete_skill_ids"`
	Heads          map[string]string `json:"heads"`
}

type organizeItemDecision struct {
	Status                 string          `json:"status"`
	AppliedRevisionID      string          `json:"applied_revision_id,omitempty"`
	PreviousHeadRevisionID string          `json:"previous_head_revision_id,omitempty"`
	PreviousField          string          `json:"previous_field,omitempty"`
	PreviousTags           json.RawMessage `json:"previous_tags,omitempty"`
	PreviousAliases        json.RawMessage `json:"previous_aliases,omitempty"`
	PreviousKeywords       json.RawMessage `json:"previous_keywords,omitempty"`
	TrashedSkillIDs        []string        `json:"trashed_skill_ids,omitempty"`
}

type organizeApprovalConflict struct {
	message string
}

func (e *organizeApprovalConflict) Error() string { return e.message }

func ListSkillOrganizeApprovals(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	tasks, err := listOrganizeApprovals(r.Context(), db, userID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"tasks": tasks})
}

func ResolveSkillOrganizeApprovals(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		RequestID string   `json:"request_id"`
		ItemIDs   []string `json:"item_ids"`
		Action    string   `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.Action = strings.TrimSpace(req.Action)
	if req.RequestID == "" || len(req.ItemIDs) == 0 {
		replyError(w, "request_id and item_ids are required", http.StatusBadRequest)
		return
	}
	switch req.Action {
	case "accept", "reject", "revoke":
	default:
		replyError(w, "action must be accept, reject, or revoke", http.StatusBadRequest)
		return
	}
	results, err := resolveOrganizeApprovals(r.Context(), db, userID, req.RequestID, req.ItemIDs, req.Action)
	if err != nil {
		var conflict *organizeApprovalConflict
		if errors.As(err, &conflict) {
			common.ReplyErrWithData(w, conflict.message, map[string]any{"code": "skill_organize_approval_deferred"}, http.StatusConflict)
			return
		}
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"results": results})
}

func listOrganizeApprovals(ctx context.Context, db *gorm.DB, userID string) ([]map[string]any, error) {
	var tasks []orm.ResourceUpdateTask
	if err := db.WithContext(ctx).
		Where("user_id = ? AND task_type = ? AND status = ?", userID, orm.ResourceUpdateTaskTypeOrganizeSkill, orm.ResourceUpdateTaskStatusDone).
		Order("created_at DESC").
		Limit(20).
		Find(&tasks).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, task := range tasks {
		view, err := loadOrganizeApprovalView(ctx, db, userID, task)
		if err != nil || view == nil {
			if err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, view)
	}
	return out, nil
}

func loadOrganizeApprovalView(ctx context.Context, db *gorm.DB, userID string, task orm.ResourceUpdateTask) (map[string]any, error) {
	requestID := organizeRequestID(task, userID)
	if requestID == "" || organizeRunStillWriting(ctx, db, userID, requestID) {
		return nil, nil
	}
	summary, ok, err := completedOrganizeSummary(ctx, db, userID, requestID)
	if err != nil || !ok {
		return nil, err
	}
	items, err := parseOrganizeApprovalItems(summary)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	bindings, decisions, err := ensureOrganizeBindings(ctx, db, userID, task, items)
	if err != nil {
		return map[string]any{"request_id": requestID, "items": []any{}, "error": err.Error()}, nil
	}
	listed := make([]map[string]any, 0, len(items))
	for _, item := range items {
		decision := decisions[item.ID]
		status := organizeApprovalPending
		if decision.Status != "" {
			status = decision.Status
		}
		if status == organizeApprovalRejected {
			continue
		}
		binding := bindings[item.ID]
		row := map[string]any{
			"id":                item.ID,
			"type":              item.Type,
			"status":            status,
			"source_keys":       item.SourceKeys,
			"target_source_key": item.TargetSourceKey,
			"target_name":       item.TargetName,
			"source_skill_ids":  binding.SourceSkillIDs,
			"target_skill_id":   binding.TargetSkillID,
			"delete_keys":       item.DeleteKeys,
			"depends_on":        item.DependsOn,
		}
		if status == organizeApprovalPending {
			row["content"] = item.Content
		}
		listed = append(listed, row)
	}
	if len(listed) == 0 {
		return nil, nil
	}
	return map[string]any{"request_id": requestID, "items": listed}, nil
}

func resolveOrganizeApprovals(ctx context.Context, db *gorm.DB, userID, requestID string, itemIDs []string, action string) ([]map[string]any, error) {
	task, err := organizeTaskByRequest(ctx, db, userID, requestID)
	if err != nil {
		return nil, err
	}
	if task.Status != orm.ResourceUpdateTaskStatusDone || organizeRunStillWriting(ctx, db, userID, requestID) {
		return nil, &organizeApprovalConflict{message: "整理任务完成后才能审批"}
	}
	summary, ok, err := completedOrganizeSummary(ctx, db, userID, requestID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &organizeApprovalConflict{message: "整理结果尚未交付"}
	}
	items, err := parseOrganizeApprovalItems(summary)
	if err != nil {
		return nil, err
	}
	byID := map[string]organizeApprovalItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	results := make([]map[string]any, 0, len(itemIDs))
	for _, rawID := range itemIDs {
		itemID := strings.TrimSpace(rawID)
		item, found := byID[itemID]
		if !found {
			results = append(results, map[string]any{"id": itemID, "status": "failed", "error": "审批项不存在"})
			continue
		}
		status, itemErr := resolveOneOrganizeItem(ctx, db, userID, task, item, action)
		row := map[string]any{"id": itemID, "status": status}
		if itemErr != nil {
			row["status"] = "failed"
			row["error"] = itemErr.Error()
		}
		results = append(results, row)
	}
	return results, nil
}

func resolveOneOrganizeItem(ctx context.Context, db *gorm.DB, userID string, task orm.ResourceUpdateTask, item organizeApprovalItem, action string) (string, error) {
	var status string
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.ResourceUpdateTask{}).Where("id = ? AND status = ?", task.ID, orm.ResourceUpdateTaskStatusDone).UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		var current orm.ResourceUpdateTask
		if err := tx.Where("id = ?", task.ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Status != orm.ResourceUpdateTaskStatusDone {
			return &organizeApprovalConflict{message: "整理任务完成后才能审批"}
		}
		items := []organizeApprovalItem{item}
		bindings, decisions, err := ensureOrganizeBindings(ctx, tx, userID, current, items)
		if err != nil {
			return err
		}
		// Re-read after ensure, which may have written bindings on another statement.
		if err := tx.Where("id = ?", task.ID).Take(&current).Error; err != nil {
			return err
		}
		_, decisions, err = readOrganizeApprovalState(current.ResultJSON)
		if err != nil {
			return err
		}
		bindings, _, err = readOrganizeApprovalState(current.ResultJSON)
		if err != nil {
			return err
		}
		binding, ok := bindings[item.ID]
		if !ok {
			return fmt.Errorf("缺少审批项 %s 的 Skill 绑定", item.ID)
		}
		decision := decisions[item.ID]
		switch action {
		case "accept":
			if decision.Status != "" && decision.Status != organizeApprovalPending {
				return &organizeApprovalConflict{message: "该审批项已处理"}
			}
			if err := requireAcceptedDependencies(item, decisions); err != nil {
				return err
			}
			if err := requireUnchangedHeads(tx, userID, binding); err != nil {
				return err
			}
			next, err := acceptOrganizeItem(ctx, tx, userID, item, binding)
			if err != nil {
				return err
			}
			status = organizeApprovalAccepted
			return writeOrganizeDecision(tx, current, item.ID, next)
		case "reject":
			if decision.Status == organizeApprovalAccepted {
				return &organizeApprovalConflict{message: "已接受的结果要用撤销，不能拒绝"}
			}
			if decision.Status == organizeApprovalRejected {
				status = organizeApprovalRejected
				return nil
			}
			status = organizeApprovalRejected
			return writeOrganizeDecision(tx, current, item.ID, organizeItemDecision{Status: organizeApprovalRejected})
		case "revoke":
			if decision.Status != organizeApprovalAccepted {
				return &organizeApprovalConflict{message: "只能撤销已接受的整理结果"}
			}
			if err := revokeOrganizeItem(ctx, tx, userID, binding, decision); err != nil {
				return err
			}
			status = organizeApprovalPending
			return clearOrganizeDecision(tx, current, item.ID)
		default:
			return fmt.Errorf("unsupported organize approval action %q", action)
		}
	})
	return status, err
}

func acceptOrganizeItem(ctx context.Context, tx *gorm.DB, userID string, item organizeApprovalItem, binding organizeItemBinding) (organizeItemDecision, error) {
	decision := organizeItemDecision{Status: organizeApprovalAccepted, TrashedSkillIDs: append([]string(nil), binding.DeleteSkillIDs...)}
	if item.Type == "delete_duplicate" {
		for _, skillID := range binding.DeleteSkillIDs {
			if err := newSkillService(tx).TrashSkill(ctx, skillservice.DeleteSkillRequest{SkillID: skillID, UserID: userID}); err != nil {
				return organizeItemDecision{}, err
			}
		}
		return decision, nil
	}
	targetID := binding.TargetSkillID
	if targetID == "" {
		return organizeItemDecision{}, fmt.Errorf("整理项缺少目标 Skill")
	}
	var before orm.SkillV2Skill
	if err := tx.Where("id = ? AND owner_user_id = ?", targetID, userID).Take(&before).Error; err != nil {
		return organizeItemDecision{}, err
	}
	decision.PreviousHeadRevisionID = stringPointer(before.HeadRevisionID)
	decision.PreviousField = before.Field
	decision.PreviousTags = append(json.RawMessage(nil), before.Tags...)
	decision.PreviousAliases = append(json.RawMessage(nil), before.Aliases...)
	decision.PreviousKeywords = append(json.RawMessage(nil), before.Keywords...)
	if strings.TrimSpace(item.Content) == "" {
		return organizeItemDecision{}, fmt.Errorf("整理项没有待审批正文")
	}
	var draft orm.SkillV2Draft
	if err := tx.Where("skill_id = ?", targetID).Take(&draft).Error; err != nil {
		return organizeItemDecision{}, err
	}
	written, err := newDraftFS(tx).WriteText(ctx, skillfs.WriteTextRequest{
		SkillID:              targetID,
		Path:                 "SKILL.md",
		Content:              item.Content,
		ExpectedDraftVersion: draft.Version,
		UserID:               userID,
		DraftStatus:          "pending_confirm",
	})
	if err != nil {
		return organizeItemDecision{}, err
	}
	committed, err := newRevisionService(tx).CommitDraft(ctx, skillrevision.CommitDraftRequest{
		SkillID:      targetID,
		UserID:       userID,
		DraftVersion: written.DraftVersion,
	})
	if err != nil {
		return organizeItemDecision{}, err
	}
	decision.AppliedRevisionID = committed.RevisionID
	if err := patchOrganizeMetadata(ctx, tx, userID, targetID, item.SearchMetadata); err != nil {
		return organizeItemDecision{}, err
	}
	for _, skillID := range binding.DeleteSkillIDs {
		if skillID == targetID {
			continue
		}
		if err := newSkillService(tx).TrashSkill(ctx, skillservice.DeleteSkillRequest{SkillID: skillID, UserID: userID}); err != nil {
			return organizeItemDecision{}, err
		}
	}
	return decision, nil
}

func revokeOrganizeItem(ctx context.Context, tx *gorm.DB, userID string, binding organizeItemBinding, decision organizeItemDecision) error {
	if decision.AppliedRevisionID != "" {
		var skill orm.SkillV2Skill
		if err := tx.Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", binding.TargetSkillID, userID).Take(&skill).Error; err != nil {
			return &organizeApprovalConflict{message: "后续已有修改，不能撤销这次整理"}
		}
		if stringPointer(skill.HeadRevisionID) != decision.AppliedRevisionID {
			return &organizeApprovalConflict{message: "后续已有修改，不能撤销这次整理"}
		}
		if _, err := newRevisionService(tx).Rollback(ctx, skillrevision.RollbackRequest{
			SkillID:          binding.TargetSkillID,
			UserID:           userID,
			TargetRevisionID: decision.PreviousHeadRevisionID,
		}); err != nil {
			return err
		}
		updates := map[string]any{
			"field":    decision.PreviousField,
			"tags":     jsonOrEmpty(decision.PreviousTags),
			"aliases":  jsonOrEmpty(decision.PreviousAliases),
			"keywords": jsonOrEmpty(decision.PreviousKeywords),
		}
		if err := tx.Model(&orm.SkillV2Skill{}).Where("id = ?", binding.TargetSkillID).Updates(updates).Error; err != nil {
			return err
		}
	}
	for _, skillID := range decision.TrashedSkillIDs {
		var skill orm.SkillV2Skill
		if err := tx.Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&skill).Error; err != nil {
			return err
		}
		if skill.DeletedAt == nil {
			return &organizeApprovalConflict{message: "后续已有修改，不能撤销这次整理"}
		}
		if expected := binding.Heads[skillID]; expected != "" && stringPointer(skill.HeadRevisionID) != expected {
			return &organizeApprovalConflict{message: "后续已有修改，不能撤销这次整理"}
		}
		if err := newSkillService(tx).RestoreSkill(ctx, skillservice.RestoreSkillRequest{SkillID: skillID, UserID: userID}); err != nil {
			return err
		}
	}
	return nil
}

func patchOrganizeMetadata(ctx context.Context, tx *gorm.DB, userID, skillID string, metadata *organizeSearchFields) error {
	if metadata == nil || (metadata.Field == nil && metadata.Tags == nil && metadata.Aliases == nil && metadata.Keywords == nil) {
		return nil
	}
	_, err := newSkillService(tx).PatchSkill(ctx, skillservice.PatchSkillRequest{
		SkillID:  skillID,
		UserID:   userID,
		Field:    metadata.Field,
		Tags:     metadata.Tags,
		Aliases:  metadata.Aliases,
		Keywords: metadata.Keywords,
	})
	return err
}

func requireAcceptedDependencies(item organizeApprovalItem, decisions map[string]organizeItemDecision) error {
	for _, dependency := range item.DependsOn {
		if decisions[dependency].Status != organizeApprovalAccepted {
			return &organizeApprovalConflict{message: "关联审批尚未接受"}
		}
	}
	return nil
}

func requireUnchangedHeads(tx *gorm.DB, userID string, binding organizeItemBinding) error {
	for skillID, head := range binding.Heads {
		var skill orm.SkillV2Skill
		if err := tx.Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", skillID, userID).Take(&skill).Error; err != nil {
			return &organizeApprovalConflict{message: "Skill 在整理完成后已变化"}
		}
		if stringPointer(skill.HeadRevisionID) != head {
			return &organizeApprovalConflict{message: "Skill 在整理完成后已有修改，请重新整理"}
		}
	}
	return nil
}

func ensureOrganizeBindings(ctx context.Context, db *gorm.DB, userID string, task orm.ResourceUpdateTask, items []organizeApprovalItem) (map[string]organizeItemBinding, map[string]organizeItemDecision, error) {
	bindings, decisions, err := readOrganizeApprovalState(task.ResultJSON)
	if err != nil {
		return nil, nil, err
	}
	missing := false
	for _, item := range items {
		if _, ok := bindings[item.ID]; !ok {
			missing = true
			break
		}
	}
	if !missing {
		return bindings, decisions, nil
	}
	allItems := items
	if summary, ok, err := completedOrganizeSummary(ctx, db, userID, organizeRequestID(task, userID)); err != nil {
		return nil, nil, err
	} else if ok {
		if parsed, err := parseOrganizeApprovalItems(summary); err != nil {
			return nil, nil, err
		} else if len(parsed) > 0 {
			allItems = parsed
		}
	}
	fresh, err := bindOrganizeItems(ctx, db, userID, task, allItems)
	if err != nil {
		return nil, nil, err
	}
	for id, binding := range fresh {
		if _, ok := bindings[id]; !ok {
			bindings[id] = binding
		}
	}
	if err := writeOrganizeBindings(db, task, bindings); err != nil {
		return nil, nil, err
	}
	return bindings, decisions, nil
}

func bindOrganizeItems(ctx context.Context, db *gorm.DB, userID string, task orm.ResourceUpdateTask, items []organizeApprovalItem) (map[string]organizeItemBinding, error) {
	roots := make([]string, 0)
	for _, item := range items {
		roots = append(roots, item.SourceKeys...)
		roots = append(roots, item.DeleteKeys...)
		if item.TargetSourceKey != "" {
			roots = append(roots, item.TargetSourceKey)
		}
	}
	byRoot, err := skillsByRelativeRoot(ctx, db, userID, roots)
	if err != nil {
		return nil, err
	}
	allowed := organizeSavedSkillIDs(task)
	bindings := map[string]organizeItemBinding{}
	for _, item := range items {
		sourceIDs := make([]string, 0, len(item.SourceKeys))
		heads := map[string]string{}
		for _, key := range item.SourceKeys {
			skill, ok := byRoot[key]
			if !ok {
				return nil, fmt.Errorf("找不到整理范围内的 Skill %s", key)
			}
			if len(allowed) > 0 && !allowed[skill.ID] {
				return nil, fmt.Errorf("Skill %s 不在本次整理范围", key)
			}
			sourceIDs = append(sourceIDs, skill.ID)
			heads[skill.ID] = stringPointer(skill.HeadRevisionID)
		}
		targetKey := item.TargetSourceKey
		if item.Type != "merge" && len(item.SourceKeys) > 0 {
			targetKey = item.SourceKeys[0]
		}
		targetID := ""
		if targetKey != "" {
			skill, ok := byRoot[targetKey]
			if !ok {
				return nil, fmt.Errorf("找不到整理目标 Skill %s", targetKey)
			}
			targetID = skill.ID
			heads[skill.ID] = stringPointer(skill.HeadRevisionID)
		}
		deleteIDs := make([]string, 0, len(item.DeleteKeys))
		if item.Type == "delete_duplicate" {
			deleteIDs = append(deleteIDs, sourceIDs...)
		} else {
			for _, key := range item.DeleteKeys {
				skill, ok := byRoot[key]
				if !ok {
					return nil, fmt.Errorf("找不到要移除的 Skill %s", key)
				}
				deleteIDs = append(deleteIDs, skill.ID)
				heads[skill.ID] = stringPointer(skill.HeadRevisionID)
			}
		}
		bindings[item.ID] = organizeItemBinding{
			SourceSkillIDs: sourceIDs,
			TargetSkillID:  targetID,
			DeleteSkillIDs: deleteIDs,
			Heads:          heads,
		}
	}
	return bindings, nil
}

func skillsByRelativeRoot(ctx context.Context, db *gorm.DB, userID string, roots []string) (map[string]orm.SkillV2Skill, error) {
	unique := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		unique = append(unique, root)
	}
	out := map[string]orm.SkillV2Skill{}
	if len(unique) == 0 {
		return out, nil
	}
	var rows []orm.SkillV2Skill
	if err := db.WithContext(ctx).Where("owner_user_id = ? AND deleted_at IS NULL AND relative_root IN ?", userID, unique).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.RelativeRoot] = row
	}
	return out, nil
}

func organizeSavedSkillIDs(task orm.ResourceUpdateTask) map[string]bool {
	var payload struct {
		SkillIDs []string `json:"skill_ids"`
	}
	_ = json.Unmarshal(task.RequestJSON, &payload)
	if len(payload.SkillIDs) == 0 {
		return nil
	}
	out := make(map[string]bool, len(payload.SkillIDs))
	for _, id := range payload.SkillIDs {
		if id = strings.TrimSpace(id); id != "" {
			out[id] = true
		}
	}
	return out
}

func organizeTaskByRequest(ctx context.Context, db *gorm.DB, userID, requestID string) (orm.ResourceUpdateTask, error) {
	var task orm.ResourceUpdateTask
	err := db.WithContext(ctx).
		Where("user_id = ? AND task_type = ? AND (trigger_id = ? OR result_id = ?)", userID, orm.ResourceUpdateTaskTypeOrganizeSkill, "skill_organize:"+userID+":"+requestID, requestID).
		Order("created_at DESC").
		Take(&task).Error
	return task, err
}

func organizeRequestID(task orm.ResourceUpdateTask, userID string) string {
	var payload struct {
		RequestID string `json:"requestid"`
	}
	_ = json.Unmarshal(task.RequestJSON, &payload)
	if requestID := strings.TrimSpace(payload.RequestID); requestID != "" {
		return requestID
	}
	prefix := "skill_organize:" + userID + ":"
	return strings.TrimPrefix(task.TriggerID, prefix)
}

func organizeRunStillWriting(ctx context.Context, db *gorm.DB, userID, requestID string) bool {
	var row orm.SkillReviewStats
	err := db.WithContext(ctx).Model(&orm.SkillReviewStats{}).
		Scopes(orm.SkillReviewStatsActiveScope).
		Where("userid = ? AND requestid = ?", userID, requestID).
		Take(&row).Error
	return err == nil
}

func completedOrganizeSummary(ctx context.Context, db *gorm.DB, userID, requestID string) (string, bool, error) {
	var row orm.SkillReviewStats
	err := db.WithContext(ctx).
		Where("userid = ? AND requestid = ? AND status = ?", userID, requestID, orm.SkillReviewStatsStatusCompleted).
		Order("started_at DESC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Summary, true, nil
}

func parseOrganizeApprovalItems(summary string) ([]organizeApprovalItem, error) {
	var payload struct {
		ApprovalItems []organizeApprovalItem `json:"approval_items"`
	}
	if strings.TrimSpace(summary) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(summary), &payload); err != nil {
		return nil, err
	}
	items := make([]organizeApprovalItem, 0, len(payload.ApprovalItems))
	for _, item := range payload.ApprovalItems {
		item.ID = strings.TrimSpace(item.ID)
		item.Type = strings.TrimSpace(item.Type)
		if item.ID == "" || item.Type == "" || item.Type == "keep" {
			continue
		}
		if item.DependsOn == nil {
			item.DependsOn = []string{}
		}
		if item.SourceKeys == nil {
			item.SourceKeys = []string{}
		}
		if item.DeleteKeys == nil {
			item.DeleteKeys = []string{}
		}
		items = append(items, item)
	}
	return items, nil
}

func readOrganizeApprovalState(raw json.RawMessage) (map[string]organizeItemBinding, map[string]organizeItemDecision, error) {
	payload := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, nil, err
		}
	}
	bindings := map[string]organizeItemBinding{}
	decisions := map[string]organizeItemDecision{}
	if encoded := payload["approval_bindings"]; len(encoded) > 0 {
		if err := json.Unmarshal(encoded, &bindings); err != nil {
			return nil, nil, err
		}
	}
	if encoded := payload["approval_decisions"]; len(encoded) > 0 {
		if err := json.Unmarshal(encoded, &decisions); err != nil {
			return nil, nil, err
		}
	}
	if bindings == nil {
		bindings = map[string]organizeItemBinding{}
	}
	if decisions == nil {
		decisions = map[string]organizeItemDecision{}
	}
	return bindings, decisions, nil
}

func writeOrganizeBindings(db *gorm.DB, task orm.ResourceUpdateTask, bindings map[string]organizeItemBinding) error {
	return updateOrganizeResult(db, task.ID, func(payload map[string]json.RawMessage) error {
		encoded, err := json.Marshal(bindings)
		if err != nil {
			return err
		}
		payload["approval_bindings"] = encoded
		return nil
	})
}

func writeOrganizeDecision(db *gorm.DB, task orm.ResourceUpdateTask, itemID string, decision organizeItemDecision) error {
	return updateOrganizeResult(db, task.ID, func(payload map[string]json.RawMessage) error {
		decisions := map[string]organizeItemDecision{}
		if encoded := payload["approval_decisions"]; len(encoded) > 0 {
			if err := json.Unmarshal(encoded, &decisions); err != nil {
				return err
			}
		}
		if decisions == nil {
			decisions = map[string]organizeItemDecision{}
		}
		decisions[itemID] = decision
		encoded, err := json.Marshal(decisions)
		if err != nil {
			return err
		}
		payload["approval_decisions"] = encoded
		return nil
	})
}

func clearOrganizeDecision(db *gorm.DB, task orm.ResourceUpdateTask, itemID string) error {
	return updateOrganizeResult(db, task.ID, func(payload map[string]json.RawMessage) error {
		decisions := map[string]organizeItemDecision{}
		if encoded := payload["approval_decisions"]; len(encoded) > 0 {
			if err := json.Unmarshal(encoded, &decisions); err != nil {
				return err
			}
		}
		delete(decisions, itemID)
		encoded, err := json.Marshal(decisions)
		if err != nil {
			return err
		}
		payload["approval_decisions"] = encoded
		return nil
	})
}

func updateOrganizeResult(db *gorm.DB, taskID string, mutate func(map[string]json.RawMessage) error) error {
	var task orm.ResourceUpdateTask
	if err := db.Where("id = ?", taskID).Take(&task).Error; err != nil {
		return err
	}
	payload := map[string]json.RawMessage{}
	if len(task.ResultJSON) > 0 {
		if err := json.Unmarshal(task.ResultJSON, &payload); err != nil {
			return err
		}
	}
	if payload == nil {
		payload = map[string]json.RawMessage{}
	}
	if err := mutate(payload); err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", taskID).UpdateColumn("result_json", json.RawMessage(encoded)).Error
}

func stringPointer(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func jsonOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("[]")
	}
	return raw
}
