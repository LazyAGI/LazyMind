package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
)

// OrganizerOpeningPreparer reuses opening extraction and the provider protocol.
// It freezes evidence instead of re-reading a changing conversation during generation.
type OrganizerOpeningPreparer struct{}
type frozenOrganizerOpening struct {
	Snapshot         openingSnapshot `json:"snapshot"`
	ConversationID   string          `json:"conversation_id"`
	SeedRevision     int64           `json:"seed_revision"`
	MetadataRevision int64           `json:"metadata_revision"`
	TitleRevision    int64           `json:"title_revision"`
	JobID            string          `json:"job_id,omitempty"`
}

func (OrganizerOpeningPreparer) Freeze(ctx context.Context, tx *gorm.DB, conv orm.Conversation) (conversationgroup.OpeningPreparation, error) {
	out := conversationgroup.OpeningPreparation{Title: conv.DisplayName}
	var external int64
	if err := tx.WithContext(ctx).Table("external_agent_bindings").Where("conversation_id=?", conv.ID).Count(&external).Error; err != nil {
		return out, err
	}
	if external > 0 || (conv.ChatExecutor != "" && conv.ChatExecutor != "lazymind") {
		out.Reason = "unsupported_conversation"
		return out, nil
	}
	var meta orm.ConversationOpening
	err := tx.Where("conversation_id=? AND user_id=?", conv.ID, conv.CreateUserID).Take(&meta).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	if (meta.IntentStatus == "ready" || meta.IntentStatus == "provisional") && strings.TrimSpace(meta.Summary) != "" {
		out.Summary = meta.Summary
		return out, nil
	}
	var count int64
	if err := tx.Model(&orm.ChatHistory{}).Where("conversation_id=?", conv.ID).Count(&count).Error; err != nil {
		return out, err
	}
	if count == 0 {
		out.Reason = "no_messages"
		return out, nil
	}
	snap, err := loadOrganizerOpeningSnapshot(tx, conv)
	if err != nil {
		return out, err
	}
	if snap.Turns == 0 {
		out.Reason = "no_task_intent"
		return out, nil
	}
	frozen := frozenOrganizerOpening{Snapshot: snap, ConversationID: conv.ID, SeedRevision: meta.SeedRevision, MetadataRevision: meta.MetadataRevision, TitleRevision: conv.TitleRevision}
	if meta.SourceHash == snap.Hash && (meta.Status == "pending" || meta.Status == "running") {
		frozen.JobID = meta.JobID
	}
	out.Frozen, err = json.Marshal(frozen)
	return out, err
}

func (OrganizerOpeningPreparer) Resolve(ctx context.Context, db *gorm.DB, uid string, raw json.RawMessage, config map[string]any) (algo.OpeningTaskResult, error) {
	var frozen frozenOrganizerOpening
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return algo.OpeningTaskResult{}, err
	}
	if frozen.JobID == "" {
		var current orm.ConversationOpening
		err := db.WithContext(ctx).Where("conversation_id=? AND user_id=? AND source_hash=?", frozen.ConversationID, uid, frozen.Snapshot.Hash).Take(&current).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return algo.OpeningTaskResult{}, err
		}
		if err == nil && current.Status == "done" && (current.IntentStatus == "empty" || strings.TrimSpace(current.Summary) != "") {
			var missing []string
			_ = json.Unmarshal(current.MissingContext, &missing)
			return algo.OpeningTaskResult{Status: "succeeded", Output: algo.OpeningDescription{Summary: current.Summary, IntentStatus: current.IntentStatus, MissingContext: missing}, Usage: current.UsageJSON}, nil
		}
		if err == nil && (current.Status == "pending" || current.Status == "running") {
			frozen.JobID = current.JobID
			frozen.SeedRevision = current.SeedRevision
		}
	}
	if frozen.JobID != "" {
		// Reuse an exact frozen seed. A replaced seed must not supply a newer summary.
		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(3*(openingOption("LAZYMIND_OPENING_TIMEOUT_SECONDS", 60)+30))*time.Second)
		defer cancel()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			var meta orm.ConversationOpening
			err := db.WithContext(waitCtx).Where("conversation_id=? AND user_id=? AND source_hash=? AND seed_revision=? AND job_id=?", frozen.ConversationID, uid, frozen.Snapshot.Hash, frozen.SeedRevision, frozen.JobID).Take(&meta).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				break
			}
			if err != nil {
				return algo.OpeningTaskResult{}, err
			}
			if meta.Status == "done" {
				var missing []string
				_ = json.Unmarshal(meta.MissingContext, &missing)
				return algo.OpeningTaskResult{Status: "succeeded", Output: algo.OpeningDescription{Summary: meta.Summary, IntentStatus: meta.IntentStatus, MissingContext: missing}, Usage: meta.UsageJSON}, nil
			}
			var job orm.AsyncJob
			if err := db.WithContext(waitCtx).Where("id=?", frozen.JobID).Take(&job).Error; err != nil {
				return algo.OpeningTaskResult{}, err
			}
			if meta.Status == "failed" || job.Status == "failed" || job.Status == "canceled" || job.Status == "succeeded" {
				code := meta.ErrorCode
				if code == "" {
					code = job.ErrorCode
				}
				if code == "" {
					code = "model_failed"
				}
				return algo.OpeningTaskResult{Status: "failed", ErrorCode: code}, nil
			}
			select {
			case <-waitCtx.Done():
				return algo.OpeningTaskResult{Status: "failed", ErrorCode: "request_timeout"}, nil
			case <-ticker.C:
			}
		}
	}
	selected, aux := config["conversation_metadata"]
	if !aux {
		selected = config["llm"]
	}
	requestConfig := map[string]any{}
	if selected != nil {
		requestConfig["llm"] = selected
	}
	result, err := algo.DescribeConversationOpening(ctx, frozen.Snapshot.Input, requestConfig, openingOption("LAZYMIND_OPENING_TIMEOUT_SECONDS", 60))
	if err == nil && result.Status != "succeeded" && result.ErrorCode == "token_limit" && aux {
		result, err = algo.DescribeConversationOpening(ctx, frozen.Snapshot.Input, map[string]any{"llm": config["llm"]}, openingOption("LAZYMIND_OPENING_TIMEOUT_SECONDS", 60))
	}
	return result, err
}

func (OrganizerOpeningPreparer) Persist(ctx context.Context, tx *gorm.DB, conv orm.Conversation, raw json.RawMessage, result algo.OpeningTaskResult) error {
	var frozen frozenOrganizerOpening
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return err
	}
	current, err := loadOrganizerOpeningSnapshot(tx, conv)
	if err != nil {
		return err
	}
	if current.Hash != frozen.Snapshot.Hash || current.Evidence != frozen.Snapshot.Evidence || conv.TitleRevision != frozen.TitleRevision {
		return nil
	}
	var meta orm.ConversationOpening
	err = tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=?", conv.ID).Take(&meta).Error
	exists := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if exists && (meta.SeedRevision != frozen.SeedRevision || meta.MetadataRevision != frozen.MetadataRevision) {
		return nil
	}
	if !exists && frozen.SeedRevision != 0 {
		return nil
	}
	// A reused job owns its own metadata writeback.
	if frozen.JobID != "" && exists && meta.JobID == frozen.JobID {
		return nil
	}
	ids, _ := json.Marshal(frozen.Snapshot.IDs)
	missing, _ := json.Marshal(result.Output.MissingContext)
	meta.ConversationID = conv.ID
	meta.UserID = conv.CreateUserID
	meta.InputJSON = frozen.Snapshot.Input
	meta.SourceHistoryIDs = ids
	meta.SourceHash = frozen.Snapshot.Hash
	meta.EvidenceHash = frozen.Snapshot.Evidence
	meta.OpeningTurns = frozen.Snapshot.Turns
	meta.SeedRevision++
	meta.MetadataRevision++
	meta.TitleRevision = conv.TitleRevision
	meta.Summary = result.Output.Summary
	meta.IntentStatus = result.Output.IntentStatus
	meta.MissingContext = missing
	meta.Status = "done"
	meta.ErrorCode = ""
	meta.JobID = ""
	meta.BackfillID = ""
	meta.WindowClosed = result.Output.IntentStatus == "ready"
	meta.GeneratorVersion = openingGeneratorVersion
	meta.GenerationCount++
	meta.CallCount++
	meta.UsageJSON = result.Usage
	meta.UpdatedAt = time.Now().UTC()
	if err := tx.Save(&meta).Error; err != nil {
		return err
	}
	if result.Output.Title != "" {
		return tx.Model(&orm.Conversation{}).Where("id=? AND title_revision=? AND title_source IN ?", conv.ID, frozen.TitleRevision, []string{"auto", "default"}).UpdateColumns(map[string]any{"display_name": result.Output.Title, "title_revision": gorm.Expr("title_revision+1"), "title_source": "auto"}).Error
	}
	return nil
}
