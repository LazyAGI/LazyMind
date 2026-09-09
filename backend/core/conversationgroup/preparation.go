package conversationgroup

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

// OpeningPreparation keeps the chat-specific evidence format out of the organizer.
type OpeningPreparation struct {
	Frozen  json.RawMessage `json:"frozen,omitempty"`
	Title   string          `json:"title"`
	Summary string          `json:"summary"`
	Reason  string          `json:"reason,omitempty"`
}

type OpeningPreparer interface {
	Freeze(context.Context, *gorm.DB, orm.Conversation) (OpeningPreparation, error)
	Resolve(context.Context, *gorm.DB, string, json.RawMessage, map[string]any) (algo.OpeningTaskResult, error)
	Persist(context.Context, *gorm.DB, orm.Conversation, json.RawMessage, algo.OpeningTaskResult) error
}

var openingPreparer OpeningPreparer

func RegisterOpeningPreparer(p OpeningPreparer) { openingPreparer = p }

type preparationItem struct {
	Conversation     snapshotConversation `json:"conversation"`
	Frozen           json.RawMessage      `json:"frozen,omitempty"`
	NeedsPreparation bool                 `json:"needs_preparation"`
	Done             bool                 `json:"done"`
	Reason           string               `json:"reason,omitempty"`
	ErrorCode        string               `json:"error_code,omitempty"`
}
type organizerPreparation struct {
	Version int               `json:"version"`
	Items   []preparationItem `json:"items"`
	Current int               `json:"current"`
	Total   int               `json:"total"`
	Sealed  bool              `json:"sealed"`
}

func freezePreparation(ctx context.Context, tx *gorm.DB, snapshot *organizerSnapshot, locks []orm.ConversationOrganizerSnapshotItem) (organizerPreparation, error) {
	p := organizerPreparation{Version: 1, Items: make([]preparationItem, 0, len(locks))}
	if openingPreparer == nil {
		return p, errors.New("opening preparer is not registered")
	}
	snapshot.Conversations = []snapshotConversation{}
	for _, lock := range locks {
		var conv orm.Conversation
		if err := tx.WithContext(ctx).Where("id=? AND create_user_id=?", lock.ConversationID, lock.UserID).Take(&conv).Error; err != nil {
			return p, err
		}
		input, err := openingPreparer.Freeze(ctx, tx, conv)
		if err != nil {
			return p, err
		}
		item := preparationItem{Conversation: snapshotConversation{ID: conv.ID, Title: input.Title, Summary: input.Summary, TitleRevision: conv.TitleRevision, MetadataRevision: lock.MetadataRevision}, Frozen: input.Frozen, Reason: input.Reason}
		item.NeedsPreparation = input.Reason == "" && strings.TrimSpace(input.Summary) == ""
		item.Done = !item.NeedsPreparation
		if item.NeedsPreparation {
			p.Total++
		}
		p.Items = append(p.Items, item)
	}
	if p.Total == 0 {
		p.seal(snapshot)
	}
	return p, nil
}
func (p *organizerPreparation) seal(snapshot *organizerSnapshot) {
	snapshot.Conversations = []snapshotConversation{}
	for _, item := range p.Items {
		if item.Reason == "" && strings.TrimSpace(item.Conversation.Summary) != "" {
			snapshot.Conversations = append(snapshot.Conversations, item.Conversation)
		}
	}
	p.Sealed = true
}

// Cancellation must reach an in-flight preparation call before the next lease heartbeat.
func organizerContext(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun, job asyncjob.Job) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var count int64
				err := db.WithContext(ctx).Model(&orm.ConversationOrganizerRun{}).Where("id=? AND job_id=? AND status IN ?", run.ID, job.ID, []string{"pending", "running", "applying"}).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id=? AND status=? AND attempt_count=? AND lock_until>?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Count(&count).Error
				if err != nil || count == 0 {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

func prepareOrganizer(ctx context.Context, db *gorm.DB, run *orm.ConversationOrganizerRun, job asyncjob.Job, config map[string]any) error {
	if len(run.PreparationJSON) == 0 {
		return nil
	} // Old runs retain their original snapshot.
	var p organizerPreparation
	if err := json.Unmarshal(run.PreparationJSON, &p); err != nil {
		return err
	}
	if p.Version != 1 {
		return errors.New("unsupported organizer preparation version")
	}
	if p.Sealed {
		return nil
	}
	if openingPreparer == nil {
		return errors.New("opening preparer is not registered")
	}
	for i := range p.Items {
		item := &p.Items[i]
		if item.Done {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := openingPreparer.Resolve(ctx, db, run.UserID, item.Frozen, config)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		item.Done = true
		switch {
		case err != nil:
			item.Reason = "summary_failed"
			item.ErrorCode = "transport_error"
		case result.Status != "succeeded":
			item.Reason = "summary_failed"
			item.ErrorCode = result.ErrorCode
			if item.ErrorCode == "" {
				item.ErrorCode = "model_failed"
			}
		case result.Output.IntentStatus == "empty":
			item.Reason = "no_task_intent"
		case strings.TrimSpace(result.Output.Summary) == "":
			item.Reason = "summary_failed"
			item.ErrorCode = "invalid_output"
		default:
			item.Conversation.Summary = result.Output.Summary
			if strings.TrimSpace(result.Output.Title) != "" {
				item.Conversation.Title = result.Output.Title
			}
		}
		p.Current++
		raw, _ := json.Marshal(p)
		if err := UserTransaction(ctx, db, run.UserID, func(tx *gorm.DB) error {
			if err := ownedRunUpdate(ctx, tx, run.ID, job, "running", map[string]any{"preparation_json": raw}); err != nil {
				return err
			}
			if result.Status == "succeeded" && item.Reason != "summary_failed" {
				var conv orm.Conversation
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND create_user_id=?", item.Conversation.ID, run.UserID).Take(&conv).Error; err != nil {
					return err
				}
				if err := openingPreparer.Persist(ctx, tx, conv, item.Frozen, result); err != nil {
					return err
				}
			}
			return tx.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=? AND conversation_id=?", run.ID, item.Conversation.ID).Updates(map[string]any{"title": item.Conversation.Title, "summary": item.Conversation.Summary}).Error
		}); err != nil {
			return err
		}
		run.PreparationJSON = raw
	}
	var snapshot organizerSnapshot
	if err := json.Unmarshal(run.SnapshotJSON, &snapshot); err != nil {
		return err
	}
	p.seal(&snapshot)
	raw, _ := json.Marshal(p)
	snapRaw, _ := json.Marshal(snapshot)
	if err := ownedRunUpdate(ctx, db, run.ID, job, "running", map[string]any{"preparation_json": raw, "snapshot_json": snapRaw, "snapshot_hash": snapshotDigest(snapRaw), "progress_total": len(snapshot.Conversations), "stage": "organizing"}); err != nil {
		return err
	}
	run.PreparationJSON = raw
	run.SnapshotJSON = snapRaw
	run.ProgressTotal = int64(len(snapshot.Conversations))
	run.Stage = "organizing"
	return nil
}
