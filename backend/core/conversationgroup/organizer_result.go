package conversationgroup

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// latestOrganizerResult excludes older, superseded results from the review gate.
func latestOrganizerResult(tx *gorm.DB, uid string) (orm.ConversationOrganizerRun, error) {
	var run orm.ConversationOrganizerRun
	err := tx.Where("user_id=? AND status IN ?", uid, []string{"succeeded", "undone", "confirmed"}).Order("created_at DESC").Take(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return run, nil
	}
	return run, err
}

func runDTO(ctx context.Context, db *gorm.DB, row orm.ConversationOrganizerRun, withItems bool) map[string]any {
	var counts organizerResult
	_ = json.Unmarshal(row.ResultJSON, &counts)
	var latestID string
	_ = db.WithContext(ctx).Model(&orm.ConversationOrganizerRun{}).Select("id").Where("user_id=? AND status IN ?", row.UserID, []string{"succeeded", "undone", "confirmed"}).Order("created_at DESC").Limit(1).Scan(&latestID).Error
	// The checkpoint version counts completed batches until final reconciliation starts.
	var checkpoint struct {
		Version int64 `json:"version"`
	}
	_ = json.Unmarshal(row.CheckpointJSON, &checkpoint)
	remainingBatches := (row.ProgressTotal - row.ProgressCurrent + 49) / 50
	batchTotal := checkpoint.Version + remainingBatches
	batchCurrent := checkpoint.Version
	if remainingBatches > 0 {
		batchCurrent++
	}
	var preparation organizerPreparation
	if len(row.PreparationJSON) > 0 {
		_ = json.Unmarshal(row.PreparationJSON, &preparation)
	}
	preparationBatchTotal := preparation.BatchTotal
	if preparationBatchTotal == 0 && preparation.Total > 0 {
		preparationBatchTotal = (preparation.Total + titlePreparationBatchSize - 1) / titlePreparationBatchSize
	}
	preparationBatchCurrent := min(preparation.BatchCurrent+1, preparationBatchTotal)
	if len(row.PreparationJSON) > 0 && (row.Status == "succeeded" || row.Status == "undone") {
		var grouped int64
		db.WithContext(ctx).Table("conversation_organizer_snapshot_items s").Joins("JOIN conversation_group_members m ON m.conversation_id=s.conversation_id").Where("s.run_id=?", row.ID).Count(&grouped)
		counts.OrganizedCount = int(grouped)
		var total int64
		db.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=?", row.ID).Count(&total)
		counts.FreeCount = int(total - grouped)
	}
	recovery := organizerRecovery(ctx, db, row)
	dto := map[string]any{"id": row.ID, "status": row.Status, "stage": row.Stage, "progress": map[string]any{"current": row.ProgressCurrent, "total": row.ProgressTotal, "batch_current": batchCurrent, "batch_total": batchTotal, "preparation_current": preparation.Current, "preparation_total": preparation.Total, "preparation_batch_current": preparationBatchCurrent, "preparation_batch_completed": preparation.BatchCurrent, "preparation_batch_total": preparationBatchTotal}, "organized_count": counts.OrganizedCount, "free_count": counts.FreeCount, "skipped_count": counts.SkippedCount, "can_cancel": (row.Status == "pending" || row.Status == "running") && row.Stage != "canceling", "can_retry": recovery == recoveryRetry, "can_restart": organizerCanRestart(ctx, db, row, recovery), "can_undo": row.Status == "succeeded" && row.ID == latestID, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt}
	var streaming organizerStream
	dto["steps"] = organizerSteps(row)
	if json.Unmarshal(row.StreamJSON, &streaming) == nil {
		dto["model_progress"] = map[string]any{"state": streaming.State, "received_chars": streaming.ReceivedChars, "elapsed_seconds": streaming.ElapsedSeconds, "idle_seconds": streaming.IdleSeconds, "first_response_at": streaming.FirstResponseAt, "last_activity_at": streaming.LastActivityAt}
	}
	if row.ErrorMessage != "" {
		dto["error"] = map[string]any{"code": row.ErrorCode, "message": row.ErrorMessage}
	}
	if withItems {
		type organizerRunItem struct {
			ConversationID   string  `json:"conversation_id"`
			Title            string  `json:"title"`
			Summary          string  `json:"summary"`
			GroupID          *string `json:"group_id"`
			State            string  `json:"state"`
			Corrected        bool    `json:"corrected"`
			SkipReason       string  `json:"skip_reason"`
			UnassignedReason string  `json:"unassigned_reason,omitempty"`
			SummaryErrorCode string  `json:"summary_error_code,omitempty"`
		}
		items := make([]organizerRunItem, 0)
		query := db.WithContext(ctx).Table("conversation_organizer_snapshot_items s").Select("s.conversation_id,s.title,s.summary,m.group_id,CASE WHEN c.id IS NULL THEN 'missing' WHEN c.deleted_at IS NOT NULL THEN 'deleted' WHEN c.archived_at IS NOT NULL THEN 'archived' WHEN m.group_id IS NULL THEN 'free' ELSE 'grouped' END AS state, CASE WHEN EXISTS (SELECT 1 FROM conversation_organizer_changes ch WHERE ch.run_id=s.run_id AND ch.conversation_id=s.conversation_id AND ch.kind='correction') THEN true ELSE false END AS corrected, CASE WHEN c.id IS NULL THEN 'conversation_missing' WHEN c.deleted_at IS NOT NULL THEN 'conversation_deleted' WHEN c.archived_at IS NOT NULL THEN 'conversation_archived' ELSE '' END AS skip_reason").Joins("LEFT JOIN conversations c ON c.id=s.conversation_id").Joins("LEFT JOIN conversation_group_members m ON m.conversation_id=s.conversation_id").Where("s.run_id=?", row.ID).Order("s.created_at,s.conversation_id")
		if len(row.PreparationJSON) == 0 && row.Status != "pending" && row.Status != "running" && row.Status != "applying" {
			var snapshot organizerSnapshot
			_ = json.Unmarshal(row.SnapshotJSON, &snapshot)
			query = query.Where("s.conversation_id IN ?", snapshot.conversationIDs())
		}
		query.Scan(&items)
		var rows []orm.ConversationOrganizerSnapshotItem
		db.Select("conversation_id,preparation_reason,preparation_error").Where("run_id=?", row.ID).Find(&rows)
		for _, row := range rows {
			preparation.Items = append(preparation.Items, preparationItem{Conversation: snapshotConversation{ID: row.ConversationID}, Reason: row.PreparationReason, ErrorCode: row.PreparationError})
		}
		for _, item := range preparation.Items {
			if counts.UnassignedReasons == nil {
				counts.UnassignedReasons = map[string]string{}
			}
			if counts.SummaryErrors == nil {
				counts.SummaryErrors = map[string]string{}
			}
			if item.Reason != "" {
				counts.UnassignedReasons[item.Conversation.ID] = item.Reason
			}
			if item.ErrorCode != "" {
				counts.SummaryErrors[item.Conversation.ID] = item.ErrorCode
			}
		}
		for i := range items {
			if items[i].GroupID == nil {
				items[i].UnassignedReason = counts.UnassignedReasons[items[i].ConversationID]
				items[i].SummaryErrorCode = counts.SummaryErrors[items[i].ConversationID]
			}
			if reason := counts.SkipReasons[items[i].ConversationID]; reason != "" {
				items[i].SkipReason = reason
			}
		}
		dto["items"] = items
	}
	return dto
}
