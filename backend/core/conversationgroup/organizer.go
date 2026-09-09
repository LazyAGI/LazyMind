package conversationgroup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

const (
	organizerJobType  = "conversation_organize"
	organizerTaskType = "conversation.organize_step"
)

var errLeaseLost = errors.New("conversation organizer lease lost")

const organizerAlreadyActiveMessage = "another conversation organizer run is active"

type snapshotConversation struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Summary          string `json:"summary"`
	TitleRevision    int64  `json:"title_revision"`
	MetadataRevision int64  `json:"metadata_revision"`
}
type snapshotGroup struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Scope    string                 `json:"scope"`
	Version  int64                  `json:"version"`
	Examples []snapshotConversation `json:"examples"`
}
type organizerSnapshot struct {
	ID            string                 `json:"id"`
	Conversations []snapshotConversation `json:"conversations"`
	Groups        []snapshotGroup        `json:"groups"`
}

func (s organizerSnapshot) conversationIDs() []string {
	ids := make([]string, 0, len(s.Conversations))
	for _, item := range s.Conversations {
		ids = append(ids, item.ID)
	}
	return ids
}

type organizerProgress struct {
	Processed int64  `json:"processed"`
	Total     int64  `json:"total"`
	Stage     string `json:"stage"`
}
type proposedNewGroup struct {
	CandidateID     string   `json:"candidate_id"`
	Name            string   `json:"name"`
	Scope           string   `json:"scope"`
	ConversationIDs []string `json:"conversation_ids"`
}
type proposedAssignment struct {
	GroupID         string   `json:"group_id"`
	GroupVersion    int64    `json:"group_version"`
	ConversationIDs []string `json:"conversation_ids"`
}
type organizerProposal struct {
	NewGroups                []proposedNewGroup   `json:"new_groups"`
	ExistingGroupAssignments []proposedAssignment `json:"existing_group_assignments"`
	FreeConversationIDs      []string             `json:"free_conversation_ids"`
	UnassignedReasons        map[string]string    `json:"unassigned_reasons,omitempty"`
}
type organizerStepOutput struct {
	Identity    string                  `json:"identity"`
	Operations  []candidateOperation    `json:"operations"`
	Assignments []incrementalAssignment `json:"assignments"`
	Processed   int                     `json:"processed"`
	Accepted    bool                    `json:"accepted"`
	Checkpoint  json.RawMessage         `json:"checkpoint"`
	Progress    organizerProgress       `json:"progress"`
	Done        bool                    `json:"done"`
	Proposal    *organizerProposal      `json:"proposal,omitempty"`
}
type organizerTaskResult struct {
	Status    string              `json:"status"`
	Output    organizerStepOutput `json:"output"`
	ErrorCode string              `json:"error_code"`
	Retryable bool                `json:"retryable"`
	Usage     json.RawMessage     `json:"usage"`
}

type organizerResult struct {
	OrganizedCount          int               `json:"organized_count"`
	FreeCount               int               `json:"free_count"`
	SkippedCount            int               `json:"skipped_count"`
	SkipReasons             map[string]string `json:"skip_reasons"`
	UnassignedReasons       map[string]string `json:"unassigned_reasons,omitempty"`
	SummaryErrors           map[string]string `json:"summary_errors,omitempty"`
	ControlledGroupVersions map[string]int64  `json:"controlled_group_versions"`
}

func RegisterAsyncJobs() { asyncjob.Register(organizerJobType, handleOrganizerJob) }

// StartTerminalJobReconciler keeps organizer runs aligned with asyncjob even
// when a worker reaches a terminal job state before invoking this module's
// handler (for example, an older Core process that does not know the job type).
func StartTerminalJobReconciler(ctx context.Context, db *gorm.DB, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := ReconcileTerminalJobs(ctx, db); err != nil && ctx.Err() == nil {
				log.Logger.Warn().Err(err).Msg("reconcile conversation organizer terminal jobs failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func ReconcileTerminalJobs(ctx context.Context, db *gorm.DB) error {
	type terminal struct {
		RunID, JobID, JobStatus, ErrorCode, ErrorMessage string
		StreamJSON                                       json.RawMessage
	}
	var rows []terminal
	if err := db.WithContext(ctx).Table("conversation_organizer_runs r").
		Select("r.id AS run_id,r.job_id,j.status AS job_status,j.error_code,j.error_message,r.stream_json").
		Joins("JOIN async_jobs j ON j.id=r.job_id").
		Where("r.status IN ? AND j.status IN ?", []string{"pending", "running", "applying"}, []string{string(asyncjob.StatusFailed), string(asyncjob.StatusCanceled)}).
		Scan(&rows).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if !settleOrganizerStream(ctx, row.StreamJSON) {
			continue
		}
		status, stage := "failed", "failed"
		if row.JobStatus == string(asyncjob.StatusCanceled) {
			status, stage = "canceled", "canceled"
		}
		res := db.WithContext(ctx).Model(&orm.ConversationOrganizerRun{}).
			Where("id=? AND job_id=? AND status IN ?", row.RunID, row.JobID, []string{"pending", "running", "applying"}).
			Updates(map[string]any{"status": status, "stage": stage, "error_code": row.ErrorCode, "error_message": row.ErrorMessage, "finished_at": now, "updated_at": now, "version": gorm.Expr("version + 1")})
		if res.Error != nil {
			return res.Error
		}
	}
	return nil
}

// latestOrganizerResult excludes older, superseded results from the review gate.
func latestOrganizerResult(tx *gorm.DB, uid string) (orm.ConversationOrganizerRun, error) {
	var run orm.ConversationOrganizerRun
	err := tx.Where("user_id=? AND status IN ?", uid, []string{"succeeded", "undone", "confirmed"}).Order("created_at DESC").Take(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return run, nil
	}
	return run, err
}

func ConfirmOrganizer(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id := common.PathVar(r, "run_id")
	err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		run, err := latestOrganizerResult(tx, uid)
		if err != nil {
			return err
		}
		if run.ID != id || (run.Status != "succeeded" && run.Status != "confirmed") {
			return errors.New("conversation organizer result cannot be confirmed")
		}
		if run.Status == "confirmed" {
			return nil
		}
		return tx.Model(&run).Updates(map[string]any{"status": "confirmed", "stage": "confirmed", "updated_at": time.Now().UTC(), "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	getOrganizer(w, r, id, true)
}

func StartOrganizer(w http.ResponseWriter, r *http.Request) {
	uid, uname := user(r)
	db := store.DB().WithContext(r.Context())
	var run orm.ConversationOrganizerRun
	llmConfig, configErr := modelconfig.LoadLLMConfig(r.Context(), store.DB(), uid)
	if configErr != nil {
		common.ReplyErr(w, "load organizer model config failed", 500)
		return
	}
	modelRaw, _ := json.Marshal(sanitizeModelConfig(llmConfig))
	err := UserTransaction(r.Context(), db, uid, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND status IN ?", uid, []string{"pending", "running", "applying"}).Order("created_at DESC").Take(&run).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		previous, err := latestOrganizerResult(tx, uid)
		if err != nil {
			return err
		}
		if previous.Status == "succeeded" {
			run = previous
			return nil
		}
		now := time.Now().UTC()
		run = orm.ConversationOrganizerRun{ProtocolVersion: 2, ID: uuid.NewString(), UserID: uid, Status: "pending", Stage: "snapshot", Version: 1, ModelConfigJSON: modelRaw, CreatedAt: now, UpdatedAt: now}
		snapshot, items, err := buildSnapshot(r.Context(), tx, run.ID, uid)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("no free conversations to organize")
		}
		preparation, err := freezePreparation(r.Context(), tx, &snapshot, items)
		if err != nil {
			return err
		}
		for i := range items {
			item := preparation.Items[i]
			items[i].Ordinal = i
			items[i].Title, items[i].Summary = item.Conversation.Title, item.Conversation.Summary
			items[i].FrozenInput = item.Frozen
			items[i].PreparationStatus = "done"
			if !item.Done {
				items[i].PreparationStatus = "pending"
			}
			items[i].PreparationReason = item.Reason
		}
		preparation.Version = 2
		preparation.Items = nil
		run.PreparationJSON, _ = json.Marshal(preparation)
		if !preparation.Sealed {
			run.Stage = "preparing"
		}
		raw, _ := json.Marshal(snapshot)
		sum := sha256.Sum256(raw)
		run.SnapshotJSON = raw
		run.SnapshotHash = hex.EncodeToString(sum[:])
		run.ProgressTotal = int64(len(snapshot.Conversations))
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].RunID = run.ID
			if err := tx.Create(&items[i]).Error; err != nil {
				return err
			}
		}
		job, err := asyncjob.EnqueueInTransaction(r.Context(), tx, asyncjob.EnqueueRequest{JobType: organizerJobType, ResourceType: "conversation_organizer_run", ResourceID: run.ID, IdempotencyKey: run.ID, Payload: map[string]any{"run_id": run.ID}, MaxAttempts: 3, RunAt: now, CreateUserID: uid, CreateUserName: uname})
		if err != nil {
			return err
		}
		run.JobID = job.ID
		return tx.Model(&orm.ConversationOrganizerRun{}).Where("id=?", run.ID).Update("job_id", job.ID).Error
	})
	if err != nil {
		if isUnique(err) && store.DB().WithContext(r.Context()).Where("user_id=? AND status IN ?", uid, []string{"pending", "running", "applying"}).Order("created_at DESC").Take(&run).Error == nil {
			writeJSON(w, http.StatusAccepted, map[string]any{"run": runDTO(r.Context(), store.DB(), run, false)})
			return
		}
		status := 500
		if err.Error() == "no free conversations to organize" {
			status = 409
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": runDTO(r.Context(), store.DB(), run, false)})
}

func GetOrganizer(w http.ResponseWriter, r *http.Request) {
	getOrganizer(w, r, common.PathVar(r, "run_id"), true)
}
func GetLatestOrganizer(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	db := store.DB().WithContext(r.Context())
	_ = ReconcileTerminalJobs(r.Context(), store.DB())
	var freeCount int64
	if err := freeConversationQuery(db, uid).Count(&freeCount).Error; err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	var row orm.ConversationOrganizerRun
	err := db.Where("user_id=?", uid).Order("created_at DESC").Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeJSON(w, 200, map[string]any{"run": nil, "latest_successful_run_id": nil, "free_conversation_count": freeCount})
			return
		}
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	var completed struct{ ID, Status string }
	_ = db.Model(&orm.ConversationOrganizerRun{}).Select("id,status").Where("user_id=? AND status IN ?", uid, []string{"succeeded", "undone", "confirmed"}).Order("created_at DESC").Limit(1).Scan(&completed).Error
	var latestSuccess any = nil
	if completed.Status == "succeeded" {
		latestSuccess = completed.ID
	}
	writeJSON(w, 200, map[string]any{"run": runDTO(r.Context(), store.DB(), row, true), "latest_successful_run_id": latestSuccess, "free_conversation_count": freeCount})
}
func getOrganizer(w http.ResponseWriter, r *http.Request, id string, items bool) {
	uid, _ := user(r)
	_ = ReconcileTerminalJobs(r.Context(), store.DB())
	var row orm.ConversationOrganizerRun
	err := store.DB().WithContext(r.Context()).Where("id=? AND user_id=?", id, uid).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "conversation organizer run not found", 404)
		} else {
			common.ReplyErr(w, err.Error(), 500)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"run": runDTO(r.Context(), store.DB(), row, items)})
}

func CancelOrganizer(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id := common.PathVar(r, "run_id")
	now := time.Now().UTC()
	err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		var row orm.ConversationOrganizerRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, uid).Take(&row).Error; err != nil {
			return err
		}
		if row.Status != "pending" && row.Status != "running" {
			return errors.New("conversation organizer run cannot be canceled")
		}
		if err := tx.Model(&orm.AsyncJob{}).Where("id=? AND status IN ?", row.JobID, []string{"pending", "running"}).Updates(map[string]any{"status": asyncjob.StatusCanceled, "locked_by": "", "lock_until": nil, "finished_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&row).Updates(map[string]any{"stage": "canceling", "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		status := 500
		if strings.Contains(err.Error(), "cannot be canceled") {
			status = 409
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	getOrganizer(w, r, id, true)
}

func RetryOrganizer(w http.ResponseWriter, r *http.Request) {
	uid, uname := user(r)
	id := common.PathVar(r, "run_id")
	now := time.Now().UTC()
	err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		var row orm.ConversationOrganizerRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, uid).Take(&row).Error; err != nil {
			return err
		}
		if row.ProtocolVersion != 2 {
			return errOrganizerProtocol
		}
		if row.Status != "failed" && row.Status != "canceled" {
			return errors.New("conversation organizer run cannot be retried")
		}
		previous, err := latestOrganizerResult(tx, uid)
		if err != nil {
			return err
		}
		if previous.Status == "succeeded" {
			return errors.New("conversation organizer run cannot be retried before reviewing the current result")
		}
		var active int64
		if err := tx.Model(&orm.ConversationOrganizerRun{}).Where("user_id=? AND id<>? AND status IN ?", uid, id, []string{"pending", "running", "applying"}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return fmt.Errorf("%s", organizerAlreadyActiveMessage)
		}
		job, err := asyncjob.EnqueueInTransaction(r.Context(), tx, asyncjob.EnqueueRequest{JobType: organizerJobType, ResourceType: "conversation_organizer_run", ResourceID: id, IdempotencyKey: id + ":" + uuid.NewString(), Payload: map[string]any{"run_id": id}, MaxAttempts: 3, RunAt: now, CreateUserID: uid, CreateUserName: uname})
		if err != nil {
			return err
		}
		row.Stage = "organizing"
		var checkpointStage struct {
			Stage string `json:"stage"`
		}
		if json.Unmarshal(row.CheckpointJSON, &checkpointStage) == nil && checkpointStage.Stage == "final" {
			row.Stage = "final"
		}
		var preparation organizerPreparation
		if len(row.PreparationJSON) > 0 && json.Unmarshal(row.PreparationJSON, &preparation) == nil && !preparation.Sealed {
			row.Stage = "preparing"
		}
		return tx.Model(&row).Updates(map[string]any{"status": "pending", "stage": row.Stage, "job_id": job.ID, "error_code": "", "error_message": "", "finished_at": nil, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		status := 500
		if strings.Contains(err.Error(), "cannot be retried") || strings.Contains(err.Error(), organizerAlreadyActiveMessage) || isUnique(err) {
			status = 409
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	getOrganizer(w, r, id, true)
}

// CorrectOrganizerItem immediately persists a result-panel correction while
// retaining the run provenance needed for a safe undo.
func CorrectOrganizerItem(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	runID := common.PathVar(r, "run_id")
	cid := common.PathVar(r, "conversation_id")
	var raw map[string]json.RawMessage
	if json.NewDecoder(r.Body).Decode(&raw) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	var groupID *string
	if value, ok := raw["group_id"]; ok {
		if string(value) != "null" {
			var parsed string
			if json.Unmarshal(value, &parsed) != nil {
				common.ReplyErr(w, "invalid body", 400)
				return
			}
			parsed = strings.TrimSpace(parsed)
			if parsed != "" {
				groupID = &parsed
			}
		}
	}
	var newGroup *groupInput
	if value, ok := raw["new_group"]; ok && string(value) != "null" {
		var parsed groupInput
		if json.Unmarshal(value, &parsed) != nil {
			common.ReplyErr(w, "invalid body", 400)
			return
		}
		newGroup = &parsed
	}
	if _, has := raw["group_id"]; !has && newGroup == nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if groupID != nil && newGroup != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		var run orm.ConversationOrganizerRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND status=?", runID, uid, "succeeded").Take(&run).Error; err != nil {
			return err
		}
		var included int64
		if err := tx.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=? AND conversation_id=?", runID, cid).Count(&included).Error; err != nil {
			return err
		}
		if included != 1 {
			return gorm.ErrRecordNotFound
		}
		var conv orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND create_user_id=? AND deleted_at IS NULL AND archived_at IS NULL", cid, uid).Take(&conv).Error; err != nil {
			return err
		}
		if err := RequireOrganizerUnlocked(r.Context(), tx, uid, []string{cid}, runID); err != nil {
			return err
		}
		target := ""
		if newGroup != nil {
			if err := requireOrganizerNamesUnlocked(tx, uid); err != nil {
				return err
			}
			name, scope, err := validateGroupInput(*newGroup, true)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			g := orm.ConversationGroup{ID: uuid.NewString(), UserID: uid, Name: name, NormalizedName: normalizeName(name), Scope: scope, Version: 1, CreatedBy: CreatedByUser, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&g).Error; err != nil {
				return err
			}
			target = g.ID
		} else if groupID != nil {
			target = *groupID
		}
		if target != "" && newGroup == nil {
			var targetGroup orm.ConversationGroup
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", target, uid).Take(&targetGroup).Error; err != nil {
				return err
			}
		}
		var priorState orm.ConversationGroupState
		stateErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=? AND user_id=?", cid, uid).Take(&priorState).Error
		if stateErr != nil && !errors.Is(stateErr, gorm.ErrRecordNotFound) {
			return stateErr
		}
		var before *string
		if stateErr == nil {
			before = priorState.GroupID
		}
		var old orm.ConversationGroupMember
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=? AND user_id=?", cid, uid).Take(&old).Error
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		if stateErr != nil && findErr == nil {
			before = groupIDOrNil(old.GroupID)
		}
		if target == "" {
			if findErr == nil {
				if err := tx.Delete(&old).Error; err != nil {
					return err
				}
			}
		} else {
			now := time.Now().UTC()
			if findErr == nil {
				if err := tx.Model(&old).Updates(map[string]any{"group_id": target, "revision": gorm.Expr("revision + 1"), "source": CreatedByUser, "source_run_id": runID, "updated_at": now}).Error; err != nil {
					return err
				}
			} else {
				old = orm.ConversationGroupMember{ConversationID: cid, GroupID: target, UserID: uid, Revision: 1, Source: CreatedByUser, SourceRunID: runID, CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&old).Error; err != nil {
					return err
				}
			}
		}
		rev, err := advanceGroupState(tx, uid, cid, groupIDOrNil(target), runID)
		if err != nil {
			return err
		}
		var change orm.ConversationOrganizerChange
		err = tx.Where("run_id=? AND conversation_id=? AND undone_at IS NULL", runID, cid).Order("created_at DESC").Take(&change).Error
		after := groupIDOrNil(target)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return recordChange(tx, runID, cid, before, after, rev, "correction")
		}
		if err != nil {
			return err
		}
		return tx.Model(&change).Updates(map[string]any{"after_group_id": after, "after_member_revision": rev, "kind": "correction"}).Error
	})
	if err != nil {
		status := 500
		if errors.Is(err, ErrConversationOrganizing) {
			status = 409
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			status = 404
		} else if isUnique(err) {
			status = 409
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	getOrganizer(w, r, runID, true)
}

func UndoOrganizer(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	runID := common.PathVar(r, "run_id")
	now := time.Now().UTC()
	skipped := 0
	err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		var run orm.ConversationOrganizerRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", runID, uid).Take(&run).Error; err != nil {
			return err
		}
		if run.Status == "undone" {
			return nil
		}
		if run.Status != "succeeded" {
			return errors.New("conversation organizer run cannot be undone")
		}
		var latestID string
		if err := tx.Model(&orm.ConversationOrganizerRun{}).Select("id").Where("user_id=? AND status IN ?", uid, []string{"succeeded", "undone", "confirmed"}).Order("created_at DESC").Limit(1).Scan(&latestID).Error; err != nil {
			return err
		}
		if latestID != runID {
			return errors.New("conversation organizer run cannot be undone")
		}
		var controlledResult organizerResult
		if len(run.ResultJSON) > 0 {
			if err := json.Unmarshal(run.ResultJSON, &controlledResult); err != nil {
				return err
			}
		}
		var changes []orm.ConversationOrganizerChange
		if err := tx.Where("run_id=? AND undone_at IS NULL", runID).Order("created_at DESC").Find(&changes).Error; err != nil {
			return err
		}
		for _, change := range changes {
			var conv orm.Conversation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND create_user_id=? AND deleted_at IS NULL AND archived_at IS NULL", change.ConversationID, uid).Take(&conv).Error; err != nil {
				skipped++
				continue
			}
			if err := RequireOrganizerUnlocked(r.Context(), tx, uid, []string{change.ConversationID}, runID); err != nil {
				return err
			}
			if change.BeforeGroupID != nil {
				var group orm.ConversationGroup
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", *change.BeforeGroupID, uid).Take(&group).Error; err != nil {
					skipped++
					continue
				}
			}
			var state orm.ConversationGroupState
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=? AND user_id=?", change.ConversationID, uid).Take(&state).Error; err != nil || state.SourceRunID != runID || state.Revision != change.AfterMemberRevision || !sameGroup(state.GroupID, change.AfterGroupID) {
				skipped++
				continue
			}
			var current orm.ConversationGroupMember
			memberErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=? AND user_id=?", change.ConversationID, uid).Take(&current).Error
			if change.AfterGroupID == nil {
				if !errors.Is(memberErr, gorm.ErrRecordNotFound) {
					skipped++
					continue
				}
			} else if memberErr != nil || current.GroupID != *change.AfterGroupID {
				skipped++
				continue
			}
			if change.BeforeGroupID == nil {
				if memberErr == nil {
					if err := tx.Delete(&current).Error; err != nil {
						return err
					}
				}
			} else if errors.Is(memberErr, gorm.ErrRecordNotFound) {
				current = orm.ConversationGroupMember{ConversationID: change.ConversationID, GroupID: *change.BeforeGroupID, UserID: uid, Revision: 1, Source: CreatedByUser, CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&current).Error; err != nil {
					return err
				}
			} else if err := tx.Model(&current).Updates(map[string]any{"group_id": *change.BeforeGroupID, "revision": gorm.Expr("revision + 1"), "source": CreatedByUser, "source_run_id": "", "updated_at": now}).Error; err != nil {
				return err
			}
			if _, err := advanceGroupState(tx, uid, change.ConversationID, change.BeforeGroupID, ""); err != nil {
				return err
			}
			if err := tx.Model(&change).Update("undone_at", now).Error; err != nil {
				return err
			}
		}
		var created []orm.ConversationGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("created_run_id=? AND created_by=? AND deleted_at IS NULL", runID, CreatedByOrganizer).Order("id").Find(&created).Error; err != nil {
			return err
		}
		for _, group := range created {
			if controlledResult.ControlledGroupVersions[group.ID] != group.Version {
				continue
			}
			var count int64
			if err := tx.Model(&orm.ConversationGroupMember{}).Where("group_id=?", group.ID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Model(&group).Updates(map[string]any{"deleted_at": now, "normalized_name": group.NormalizedName + "#deleted#" + group.ID, "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
					return err
				}
			}
		}
		controlledResult.SkippedCount = skipped
		result, _ := json.Marshal(controlledResult)
		return tx.Model(&run).Updates(map[string]any{"status": "undone", "stage": "undone", "result_json": result, "undone_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		status := 500
		if strings.Contains(err.Error(), "cannot be undone") || errors.Is(err, ErrConversationOrganizing) {
			status = 409
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	getOrganizer(w, r, runID, true)
}

func groupIDOrNil(id string) *string {
	if id == "" {
		return nil
	}
	copy := id
	return &copy
}
func sameGroup(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func handleOrganizerJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	var payload struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(job.PayloadJSON, &payload) != nil || payload.RunID == "" {
		return asyncjob.Result{Permanent: true, ErrorCode: "invalid_payload"}, errors.New("invalid organizer payload")
	}
	db := store.DB()
	var run orm.ConversationOrganizerRun
	if err := db.WithContext(ctx).Where("id=? AND user_id=?", payload.RunID, job.CreateUserID).Take(&run).Error; err != nil {
		return asyncjob.Result{Permanent: true, ErrorCode: "run_not_found"}, err
	}
	if run.Status == "canceled" || run.Status == "succeeded" || run.Status == "undone" || run.Status == "confirmed" {
		raw, _ := json.Marshal(map[string]any{"run_id": run.ID, "status": run.Status})
		return asyncjob.Result{ResultJSON: raw}, nil
	}
	if run.ProtocolVersion != 2 {
		return asyncjob.Result{Permanent: true, ErrorCode: "organizer_protocol_upgraded"}, errOrganizerProtocol
	}
	ctx, cancel := organizerContext(ctx, db, run, job)
	defer cancel()
	stage := "organizing"
	var checkpointStage struct {
		Stage string `json:"stage"`
	}
	if json.Unmarshal(run.CheckpointJSON, &checkpointStage) == nil && checkpointStage.Stage == "final" {
		stage = "final"
	}
	var preparation organizerPreparation
	if len(run.PreparationJSON) > 0 {
		if err := json.Unmarshal(run.PreparationJSON, &preparation); err != nil {
			return failRun(ctx, db, run, job, "invalid_snapshot", err)
		}
		if !preparation.Sealed {
			stage = "preparing"
		}
	}
	if err := ownedRunUpdate(ctx, db, run.ID, job, "pending", map[string]any{"status": "running", "stage": stage}); err != nil {
		if err == errLeaseLost {
			return asyncjob.Result{ErrorCode: "lease_lost"}, err
		}
		return asyncjob.Result{ErrorCode: "update_failed"}, err
	}
	if !settleOrganizerStream(ctx, run.StreamJSON) {
		_ = ownedRunUpdate(ctx, db, run.ID, job, "running", map[string]any{"stage": "canceling"})
		return asyncjob.Result{Permanent: true, ErrorCode: "cancellation_unconfirmed"}, errCancellationUnconfirmed
	}
	llmConfig, err := modelconfig.LoadLLMConfig(ctx, db, run.UserID)
	if err != nil {
		return failRun(ctx, db, run, job, "model_config", err)
	}
	currentSanitized, _ := json.Marshal(sanitizeModelConfig(llmConfig))
	if string(currentSanitized) != string(run.ModelConfigJSON) {
		return failRun(ctx, db, run, job, "model_config_changed", errors.New("conversation organizer model config changed"))
	}
	if err := prepareOrganizer(ctx, db, &run, job, llmConfig); err != nil {
		if ctx.Err() != nil {
			return asyncjob.Result{ErrorCode: "lease_lost"}, ctx.Err()
		}
		return retryOrFailRun(ctx, db, run, job, "preparation_failed", err)
	}
	var snapshot organizerSnapshot
	if err := json.Unmarshal(run.SnapshotJSON, &snapshot); err != nil {
		return failRun(ctx, db, run, job, "invalid_snapshot", err)
	}
	if len(snapshot.Conversations) == 0 {
		proposal := organizerProposal{NewGroups: []proposedNewGroup{}, ExistingGroupAssignments: []proposedAssignment{}, FreeConversationIDs: []string{}}
		raw, _ := json.Marshal(proposal)
		if err := applyProposal(ctx, db, run, job, proposal, raw); err != nil {
			return failRun(ctx, db, run, job, "apply_failed", err)
		}
		result, _ := json.Marshal(map[string]any{"run_id": run.ID, "status": "succeeded"})
		return asyncjob.Result{ResultJSON: result}, nil
	}
	for step := 0; step < 100000; step++ {
		proposal, err := runIncrementalStep(ctx, db, &run, job, snapshot, llmConfig)
		if err != nil {
			if errors.Is(err, errCancellationUnconfirmed) {
				_ = ownedRunUpdate(ctx, db, run.ID, job, "running", map[string]any{"stage": "canceling"})
				return asyncjob.Result{Permanent: true, ErrorCode: "cancellation_unconfirmed"}, err
			}
			if ctx.Err() != nil {
				return asyncjob.Result{ErrorCode: "lease_lost"}, ctx.Err()
			}
			return retryOrFailRun(ctx, db, run, job, "incremental_step_failed", err)
		}
		if reporter != nil {
			if err := reporter.SetProgress(ctx, run.ProgressCurrent, int64(len(snapshot.Conversations))); err != nil {
				return asyncjob.Result{ErrorCode: "lease_lost"}, err
			}
		}
		if proposal == nil {
			continue
		}
		raw, _ := json.Marshal(proposal)
		if err := applyProposal(ctx, db, run, job, *proposal, raw); err != nil {
			return failRun(ctx, db, run, job, "apply_failed", err)
		}
		result, _ := json.Marshal(map[string]any{"run_id": run.ID, "status": "succeeded"})
		return asyncjob.Result{ResultJSON: result}, nil
	}
	return failRun(ctx, db, run, job, "step_limit", errors.New("conversation organizer exceeded step limit"))
}

func ownedRunUpdate(ctx context.Context, db *gorm.DB, runID string, job asyncjob.Job, from string, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC()
	statuses := []string{from}
	if from == "pending" {
		statuses = []string{"pending", "running"}
	}
	res := db.WithContext(ctx).Model(&orm.ConversationOrganizerRun{}).Where("id=? AND status IN ? AND job_id=?", runID, statuses, job.ID).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id=? AND status=? AND attempt_count=? AND lock_until>?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errLeaseLost
	}
	return nil
}

func freeConversationQuery(db *gorm.DB, uid string) *gorm.DB {
	return db.Table("conversations c").Joins("LEFT JOIN conversation_group_members m ON m.conversation_id=c.id").Where("c.create_user_id=? AND c.deleted_at IS NULL AND c.archived_at IS NULL AND c.is_ephemeral=? AND c.is_task_conv=? AND c.parent_conversation_id IS NULL AND m.conversation_id IS NULL", uid, false, false)
}

func buildSnapshot(ctx context.Context, tx *gorm.DB, runID, uid string) (organizerSnapshot, []orm.ConversationOrganizerSnapshotItem, error) {
	type itemRow struct {
		ID, DisplayName, Summary        string
		Eligible                        bool
		TitleRevision, MetadataRevision int64
	}
	var rows []itemRow
	err := freeConversationQuery(tx.WithContext(ctx), uid).Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "c"}}).
		Select("c.id,c.display_name,COALESCE(o.summary,'') AS summary,c.title_revision,COALESCE(o.metadata_revision,0) AS metadata_revision,COALESCE(o.status='done' AND o.intent_status='ready',false) AND c.chat_executor IN ('','lazymind') AND NOT EXISTS (SELECT 1 FROM external_agent_bindings e WHERE e.conversation_id=c.id) AS eligible").
		Joins("LEFT JOIN conversation_opening_metadata o ON o.conversation_id=c.id AND o.user_id=c.create_user_id").
		Order("c.created_at,c.id").Scan(&rows).Error
	if err != nil {
		return organizerSnapshot{}, nil, err
	}
	snap := organizerSnapshot{ID: runID, Conversations: make([]snapshotConversation, 0, len(rows)), Groups: make([]snapshotGroup, 0)}
	items := make([]orm.ConversationOrganizerSnapshotItem, 0, len(rows))
	now := time.Now().UTC()
	for _, row := range rows {
		it := snapshotConversation{row.ID, row.DisplayName, row.Summary, row.TitleRevision, row.MetadataRevision}
		if row.Eligible {
			snap.Conversations = append(snap.Conversations, it)
		}
		items = append(items, orm.ConversationOrganizerSnapshotItem{ConversationID: row.ID, UserID: uid, Title: row.DisplayName, Summary: row.Summary, TitleRevision: row.TitleRevision, MetadataRevision: row.MetadataRevision, CreatedAt: now})
	}
	var groups []orm.ConversationGroup
	if err := tx.Where("user_id=? AND deleted_at IS NULL", uid).Order("created_at,id").Find(&groups).Error; err != nil {
		return organizerSnapshot{}, nil, err
	}
	for _, group := range groups {
		sg := snapshotGroup{ID: group.ID, Name: group.Name, Scope: group.Scope, Version: group.Version, Examples: make([]snapshotConversation, 0)}
		var examples []snapshotConversation
		if err := tx.Table("conversation_group_members m").Select("c.id,c.display_name AS title,COALESCE(o.summary,'') AS summary").Joins("JOIN conversations c ON c.id=m.conversation_id").Joins("LEFT JOIN conversation_opening_metadata o ON o.conversation_id=c.id").Where("m.group_id=? AND c.deleted_at IS NULL AND c.archived_at IS NULL", group.ID).Order("c.updated_at DESC").Limit(3).Scan(&examples).Error; err != nil {
			return organizerSnapshot{}, nil, err
		}
		if examples != nil {
			sg.Examples = examples
		}
		snap.Groups = append(snap.Groups, sg)
	}
	return snap, items, nil
}

func snapshotDigest(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func rawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

func sanitizeModelConfig(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "api_key" || normalized == "authorization" || strings.Contains(normalized, "secret") {
				continue
			}
			out[key] = sanitizeModelConfig(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeModelConfig(item)
		}
		return out
	default:
		return value
	}
}

func applyProposal(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun, job asyncjob.Job, p organizerProposal, raw json.RawMessage) error {
	return UserTransaction(ctx, db, run.UserID, func(tx *gorm.DB) error {
		if err := validateProposal(run.SnapshotJSON, p); err != nil {
			return err
		}
		var owned int64
		if err := tx.Model(&orm.AsyncJob{}).Where("id=? AND status=? AND attempt_count=? AND lock_until>?", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Count(&owned).Error; err != nil {
			return err
		}
		if owned != 1 {
			return errLeaseLost
		}
		if res := tx.Model(&orm.ConversationOrganizerRun{}).Where("id=? AND status=? AND job_id=?", run.ID, "running", job.ID).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id=? AND status=? AND attempt_count=? AND lock_until>?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Updates(map[string]any{"status": "applying", "stage": "applying", "proposal_json": raw, "updated_at": time.Now().UTC(), "version": gorm.Expr("version + 1")}); res.Error != nil || res.RowsAffected != 1 {
			if res.Error != nil {
				return res.Error
			}
			return errLeaseLost
		}
		allowed := map[string]bool{}
		var snapshot organizerSnapshot
		if err := json.Unmarshal(run.SnapshotJSON, &snapshot); err != nil {
			return err
		}
		snapshotIDs := snapshot.conversationIDs()
		for _, id := range snapshotIDs {
			allowed[id] = true
		}
		seen := map[string]bool{}
		skipReasons := map[string]string{}
		controlledGroupVersions := map[string]int64{}
		organized, skipped := 0, 0
		sort.Strings(snapshotIDs)
		var lockedConversations []orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND create_user_id=?", snapshotIDs, run.UserID).Order("id").Find(&lockedConversations).Error; err != nil {
			return err
		}
		formalIDs := make([]string, 0, len(p.ExistingGroupAssignments))
		for _, assignment := range p.ExistingGroupAssignments {
			formalIDs = append(formalIDs, assignment.GroupID)
		}
		sort.Strings(formalIDs)
		if len(formalIDs) > 0 {
			var lockedGroups []orm.ConversationGroup
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND user_id=?", formalIDs, run.UserID).Order("id").Find(&lockedGroups).Error; err != nil {
				return err
			}
		}
		var lockedStates []orm.ConversationGroupState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id IN ? AND user_id=?", snapshotIDs, run.UserID).Order("conversation_id").Find(&lockedStates).Error; err != nil {
			return err
		}
		var lockedMembers []orm.ConversationGroupMember
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id IN ? AND user_id=?", snapshotIDs, run.UserID).Order("conversation_id").Find(&lockedMembers).Error; err != nil {
			return err
		}
		conversationByID := make(map[string]orm.Conversation, len(lockedConversations))
		for _, conv := range lockedConversations {
			conversationByID[conv.ID] = conv
		}
		memberByID := make(map[string]bool, len(lockedMembers))
		for _, member := range lockedMembers {
			memberByID[member.ConversationID] = true
		}
		validConversation := func(id string) bool {
			if !allowed[id] || seen[id] {
				return false
			}
			seen[id] = true
			conv, exists := conversationByID[id]
			state := struct{ ConversationExists, Deleted, Archived, Grouped bool }{
				exists, conv.DeletedAt != nil, conv.ArchivedAt != nil, memberByID[id],
			}
			if state.ConversationExists && !state.Deleted && !state.Archived && !state.Grouped {
				return true
			}
			switch {
			case !state.ConversationExists:
				skipReasons[id] = "conversation_missing"
			case state.Deleted:
				skipReasons[id] = "conversation_deleted"
			case state.Archived:
				skipReasons[id] = "conversation_archived"
			case state.Grouped:
				skipReasons[id] = "membership_changed"
			}
			return false
		}
		for _, candidate := range p.NewGroups {
			ids := unique(candidate.ConversationIDs)
			valid := make([]string, 0, len(ids))
			for _, id := range ids {
				if validConversation(id) {
					valid = append(valid, id)
				} else {
					skipped++
				}
			}
			if len(valid) < 3 {
				for _, id := range valid {
					skipReasons[id] = "candidate_below_minimum"
				}
				continue
			}
			name, scope, err := validateGroupInput(groupInput{Name: &candidate.Name, Scope: &candidate.Scope}, true)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			group := orm.ConversationGroup{ID: uuid.NewString(), UserID: run.UserID, Name: name, NormalizedName: normalizeName(name), Scope: scope, Version: 1, CreatedBy: CreatedByOrganizer, CreatedRunID: run.ID, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&group).Error; err != nil {
				return err
			}
			controlledGroupVersions[group.ID] = group.Version
			for _, id := range valid {
				member := orm.ConversationGroupMember{ConversationID: id, GroupID: group.ID, UserID: run.UserID, Revision: 1, Source: CreatedByOrganizer, SourceRunID: run.ID, CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&member).Error; err != nil {
					return err
				}
				revision, err := advanceGroupState(tx, run.UserID, id, &group.ID, run.ID)
				if err != nil {
					return err
				}
				if err := recordChange(tx, run.ID, id, nil, &group.ID, revision, "organize"); err != nil {
					return err
				}
				organized++
			}
		}
		for _, assignment := range p.ExistingGroupAssignments {
			var group orm.ConversationGroup
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND version=? AND deleted_at IS NULL", assignment.GroupID, run.UserID, assignment.GroupVersion).Take(&group).Error; err != nil {
				skipped += len(assignment.ConversationIDs)
				for _, id := range assignment.ConversationIDs {
					seen[id] = true
					skipReasons[id] = "group_scope_changed"
				}
				continue
			}
			for _, id := range unique(assignment.ConversationIDs) {
				if !validConversation(id) {
					skipped++
					continue
				}
				now := time.Now().UTC()
				member := orm.ConversationGroupMember{ConversationID: id, GroupID: group.ID, UserID: run.UserID, Revision: 1, Source: CreatedByOrganizer, SourceRunID: run.ID, CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&member).Error; err != nil {
					return err
				}
				revision, err := advanceGroupState(tx, run.UserID, id, &group.ID, run.ID)
				if err != nil {
					return err
				}
				if err := recordChange(tx, run.ID, id, nil, &group.ID, revision, "organize"); err != nil {
					return err
				}
				organized++
			}
		}
		for _, id := range p.FreeConversationIDs {
			if allowed[id] && !seen[id] {
				seen[id] = true
			}
		}
		now := time.Now().UTC()
		reasons := p.UnassignedReasons
		if reasons == nil {
			reasons = map[string]string{}
		}
		summaryErrors := map[string]string{}
		total := len(snapshotIDs)
		if len(run.PreparationJSON) > 0 {
			var prep organizerPreparation
			if err := json.Unmarshal(run.PreparationJSON, &prep); err != nil {
				return err
			}
			if run.ProtocolVersion == 2 {
				var rows []orm.ConversationOrganizerSnapshotItem
				if err := tx.Select("conversation_id,preparation_reason,preparation_error").Where("run_id=?", run.ID).Find(&rows).Error; err != nil {
					return err
				}
				for _, row := range rows {
					prep.Items = append(prep.Items, preparationItem{Conversation: snapshotConversation{ID: row.ConversationID}, Reason: row.PreparationReason, ErrorCode: row.PreparationError})
				}
			}
			total = len(prep.Items)
			for _, item := range prep.Items {
				if item.Reason != "" {
					reasons[item.Conversation.ID] = item.Reason
				}
				if item.ErrorCode != "" {
					summaryErrors[item.Conversation.ID] = item.ErrorCode
				}
			}
		}
		for id, reason := range skipReasons {
			if reason == "candidate_below_minimum" {
				reasons[id] = "below_min_group_size"
				delete(skipReasons, id)
			}
		}
		// Only application conflicts count as skipped; all ungrouped items count as free.
		skipped = len(skipReasons)
		result, _ := json.Marshal(organizerResult{OrganizedCount: organized, FreeCount: total - organized, SkippedCount: skipped, SkipReasons: skipReasons, UnassignedReasons: reasons, SummaryErrors: summaryErrors, ControlledGroupVersions: controlledGroupVersions})
		return tx.Model(&orm.ConversationOrganizerRun{}).Where("id=? AND status=?", run.ID, "applying").Updates(map[string]any{"status": "succeeded", "stage": "completed", "result_json": result, "progress_current": len(snapshotIDs), "finished_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
}

func validateProposal(snapshotRaw json.RawMessage, p organizerProposal) error {
	var snapshot organizerSnapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, item := range snapshot.Conversations {
		want[item.ID] = true
	}
	groups := map[string]int64{}
	for _, group := range snapshot.Groups {
		groups[group.ID] = group.Version
	}
	seen := map[string]bool{}
	accept := func(ids []string) error {
		for _, id := range ids {
			if !want[id] {
				return errors.New("proposal contains unknown conversation")
			}
			if seen[id] {
				return errors.New("proposal contains duplicate conversation")
			}
			seen[id] = true
		}
		return nil
	}
	candidates := map[string]bool{}
	for _, group := range p.NewGroups {
		if strings.TrimSpace(group.CandidateID) == "" || candidates[group.CandidateID] {
			return errors.New("proposal contains invalid candidate id")
		}
		candidates[group.CandidateID] = true
		if strings.TrimSpace(group.Scope) == "" {
			return errors.New("automatic conversation group scope required")
		}
		if _, _, err := validateGroupInput(groupInput{Name: &group.Name, Scope: &group.Scope}, true); err != nil {
			return err
		}
		if err := accept(group.ConversationIDs); err != nil {
			return err
		}
	}
	assignedGroups := map[string]bool{}
	for _, assignment := range p.ExistingGroupAssignments {
		version, ok := groups[assignment.GroupID]
		if !ok || version != assignment.GroupVersion {
			return errors.New("proposal references stale conversation group")
		}
		if assignedGroups[assignment.GroupID] {
			return errors.New("proposal repeats conversation group")
		}
		assignedGroups[assignment.GroupID] = true
		if err := accept(assignment.ConversationIDs); err != nil {
			return err
		}
	}
	if err := accept(p.FreeConversationIDs); err != nil {
		return err
	}
	free := map[string]bool{}
	for _, id := range p.FreeConversationIDs {
		free[id] = true
	}
	for id, reason := range p.UnassignedReasons {
		if !free[id] || (reason != "no_matching_group" && reason != "below_min_group_size") {
			return errors.New("proposal contains invalid unassigned reason")
		}
	}
	if len(seen) != len(want) {
		return errors.New("proposal does not partition the snapshot")
	}
	return nil
}

func recordChange(tx *gorm.DB, runID, cid string, before, after *string, revision int64, kind string) error {
	return tx.Create(&orm.ConversationOrganizerChange{ID: uuid.NewString(), RunID: runID, ConversationID: cid, BeforeGroupID: before, AfterGroupID: after, AfterMemberRevision: revision, Kind: kind, CreatedAt: time.Now().UTC()}).Error
}
func unique(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func failRun(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun, job asyncjob.Job, code string, err error) (asyncjob.Result, error) {
	now := time.Now().UTC()
	_ = db.WithContext(ctx).Model(&orm.ConversationOrganizerRun{}).Where("id=? AND job_id=? AND status IN ?", run.ID, job.ID, []string{"pending", "running", "applying"}).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id=? AND status=? AND attempt_count=? AND lock_until>?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, now).Updates(map[string]any{"status": "failed", "stage": "failed", "error_code": code, "error_message": err.Error(), "finished_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	return asyncjob.Result{Permanent: true, ErrorCode: code}, err
}
func retryOrFailRun(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun, job asyncjob.Job, code string, err error) (asyncjob.Result, error) {
	var row orm.AsyncJob
	if e := db.WithContext(ctx).Where("id=?", job.ID).Take(&row).Error; e == nil && job.AttemptCount < row.MaxAttempts {
		return asyncjob.Result{ErrorCode: code}, err
	}
	return failRun(ctx, db, run, job, code, err)
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
	if len(row.PreparationJSON) > 0 && (row.Status == "succeeded" || row.Status == "undone") {
		var grouped int64
		db.WithContext(ctx).Table("conversation_organizer_snapshot_items s").Joins("JOIN conversation_group_members m ON m.conversation_id=s.conversation_id").Where("s.run_id=?", row.ID).Count(&grouped)
		counts.OrganizedCount = int(grouped)
		total := int64(len(preparation.Items))
		if row.ProtocolVersion == 2 {
			db.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=?", row.ID).Count(&total)
		}
		counts.FreeCount = int(total - grouped)
	}
	dto := map[string]any{"id": row.ID, "status": row.Status, "stage": row.Stage, "progress": map[string]any{"current": row.ProgressCurrent, "total": row.ProgressTotal, "batch_current": batchCurrent, "batch_total": batchTotal, "preparation_current": preparation.Current, "preparation_total": preparation.Total}, "organized_count": counts.OrganizedCount, "free_count": counts.FreeCount, "skipped_count": counts.SkippedCount, "can_cancel": (row.Status == "pending" || row.Status == "running") && row.Stage != "canceling", "can_retry": row.ProtocolVersion == 2 && (row.Status == "failed" || row.Status == "canceled"), "can_undo": row.Status == "succeeded" && row.ID == latestID, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt}
	var streaming organizerStream
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
		if row.ProtocolVersion == 2 {
			var rows []orm.ConversationOrganizerSnapshotItem
			db.Select("conversation_id,preparation_reason,preparation_error").Where("run_id=?", row.ID).Find(&rows)
			for _, row := range rows {
				preparation.Items = append(preparation.Items, preparationItem{Conversation: snapshotConversation{ID: row.ConversationID}, Reason: row.PreparationReason, ErrorCode: row.PreparationError})
			}
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
