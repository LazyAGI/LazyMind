package conversationgroup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
	"net/http"
	"strings"
	"time"
)

const (
	organizerJobType = "conversation_organize"
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
	Examples []snapshotConversation `json:"examples,omitempty"`
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
		// A new snapshot must also settle any previous terminal execution.
		var terminal orm.ConversationOrganizerRun
		err = tx.Where("user_id=? AND status IN ?", uid, []string{"failed", "canceled"}).Order("created_at DESC").Take(&terminal).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && !settleOrganizerStream(r.Context(), terminal.StreamJSON) {
			return errCancellationUnconfirmed
		}
		now := time.Now().UTC()
		run = orm.ConversationOrganizerRun{ID: uuid.NewString(), UserID: uid, Status: "pending", Stage: "snapshot", Version: 1, ModelConfigJSON: modelRaw, CreatedAt: now, UpdatedAt: now}
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
		if err.Error() == "no free conversations to organize" || errors.Is(err, errCancellationUnconfirmed) {
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
		if organizerRecovery(r.Context(), tx, row) != recoveryRetry {
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
		moved, err := moveMembershipTx(tx, uid, cid, groupIDOrNil(target), CreatedByUser, runID)
		if err != nil {
			return err
		}
		var change orm.ConversationOrganizerChange
		err = tx.Where("run_id=? AND conversation_id=? AND undone_at IS NULL", runID, cid).Order("created_at DESC").Take(&change).Error
		after := groupIDOrNil(target)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return recordChange(tx, runID, cid, moved.BeforeGroupID, after, moved.Revision, "correction")
		}
		if err != nil {
			return err
		}
		return tx.Model(&change).Updates(map[string]any{"after_group_id": after, "after_member_revision": moved.Revision, "kind": "correction"}).Error
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
			if _, err := moveMembershipTx(tx, uid, change.ConversationID, change.BeforeGroupID, CreatedByUser, ""); err != nil {
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
