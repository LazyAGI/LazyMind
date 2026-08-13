package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

var (
	ErrNotFound            error = repositoryError("WORKFLOW_NOT_FOUND")
	ErrPermissionDenied    error = repositoryError("PERMISSION_DENIED")
	ErrIdempotencyConflict error = repositoryError("IDEMPOTENCY_CONFLICT")
	ErrSessionConflict     error = repositoryError("WORKFLOW_SESSION_CONFLICT")
)

// MaxAutomaticWorkflowStepAttempts limits executions controlled autonomously by AI,
// including the initial execution and subsequent AI retries.
// A retry explicitly requested by the user is a separate control path and is
// deliberately not constrained by this budget.
const MaxAutomaticWorkflowStepAttempts = 3

type repositoryError string

func (e repositoryError) Error() string { return string(e) }

type Repository struct {
	db        *gorm.DB
	mu        sync.RWMutex
	commandMu sync.Mutex
	subs      map[string]map[chan Event]struct{}
}

// ListWorkflowPackages returns active published Workflows visible to owner.
// Enabled settings are respected when present; absence of a setting keeps an
// owned/public Workflow discoverable so newly published revisions are usable.
func (r *Repository) ListWorkflowPackages(ctx context.Context, owner string) ([]WorkflowPackage, error) {
	type row struct {
		orm.WorkflowResource
		TreeHash           string          `gorm:"column:tree_hash"`
		GraphHash          string          `gorm:"column:graph_hash"`
		GraphSchemaVersion string          `gorm:"column:graph_schema_version"`
		CompiledGraph      json.RawMessage `gorm:"column:compiled_graph"`
		Enabled            *bool           `gorm:"column:enabled"`
	}
	var rows []row
	err := r.db.WithContext(ctx).Table("plugins p").
		Select("p.*, pr.tree_hash, pr.graph_hash, pr.graph_schema_version, pr.compiled_graph, ups.enabled").
		Joins("JOIN plugin_revisions pr ON pr.id = p.head_revision_id").
		Joins("LEFT JOIN user_plugin_settings ups ON ups.plugin_ref = p.plugin_ref AND ups.user_id = ?", owner).
		Where("p.status = 'active' AND (p.owner_user_id = ? OR p.owner_user_id = '') AND (ups.enabled IS NULL OR ups.enabled = ?)", owner, true).
		Order("p.plugin_ref ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowPackage, 0, len(rows))
	for _, value := range rows {
		out = append(out, WorkflowPackage{WorkflowRef: value.WorkflowRef, WorkflowID: value.WorkflowID,
			Name: value.Name, Description: value.Description, WhenToUse: value.WhenToUse,
			SourceType: value.SourceType, RevisionID: value.HeadRevisionID, RevisionNo: value.Version,
			TreeHash: value.TreeHash, GraphHash: value.GraphHash, GraphVersion: value.GraphSchemaVersion,
			CompiledGraph: append([]byte(nil), value.CompiledGraph...), ContainsScripts: value.ContainsScripts})
	}
	return out, nil
}

func (r *Repository) GetWorkflowPackage(ctx context.Context, owner, refOrID, revisionID string) (WorkflowPackage, error) {
	var resource orm.WorkflowResource
	query := r.db.WithContext(ctx).Where("status = 'active' AND (owner_user_id = ? OR owner_user_id = '')", owner)
	if err := query.Where("plugin_ref = ? OR plugin_id = ?", refOrID, refOrID).First(&resource).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowPackage{}, ErrNotFound
		}
		return WorkflowPackage{}, err
	}
	if revisionID == "" {
		revisionID = resource.HeadRevisionID
	}
	var revision orm.WorkflowRevision
	if err := r.db.WithContext(ctx).Where("id = ? AND plugin_resource_id = ?", revisionID, resource.ID).First(&revision).Error; err != nil {
		return WorkflowPackage{}, ErrNotFound
	}
	var entries []orm.WorkflowRevisionEntry
	if err := r.db.WithContext(ctx).Where("revision_id = ?", revision.ID).Order("path ASC").Find(&entries).Error; err != nil {
		return WorkflowPackage{}, err
	}
	files := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.BlobHash == nil || entry.EntryType != "file" {
			continue
		}
		var blob orm.WorkflowBlob
		if err := r.db.WithContext(ctx).Where("hash = ?", *entry.BlobHash).First(&blob).Error; err != nil {
			return WorkflowPackage{}, err
		}
		path := entry.Path
		if path == "workflow.yaml" {
			path = "workflow.yaml"
		}
		files[path] = append([]byte(nil), blob.Content...)
	}
	// Force deterministic JSON/map consumers even when the database returned a
	// non-deterministic entry order.
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make(map[string][]byte, len(keys))
	for _, key := range keys {
		ordered[key] = files[key]
	}
	return WorkflowPackage{WorkflowRef: resource.WorkflowRef, WorkflowID: resource.WorkflowID,
		Name: resource.Name, Description: resource.Description, WhenToUse: resource.WhenToUse,
		SourceType: resource.SourceType, RevisionID: revision.ID, RevisionNo: revision.RevisionNo,
		TreeHash: revision.TreeHash, GraphHash: revision.GraphHash, GraphVersion: revision.GraphSchemaVersion,
		CompiledGraph: append([]byte(nil), revision.CompiledGraph...), ContainsScripts: resource.ContainsScripts,
		Files: ordered}, nil
}

func New(db *gorm.DB) *Repository {
	return &Repository{db: db, subs: map[string]map[chan Event]struct{}{}}
}

func Models() []any {
	return []any{&Preparation{}, &Event{}, &Command{}, &InputResource{}, &InputBinding{}}
}

func (r *Repository) ImportInputResource(ctx context.Context, owner, name, mime, hash string, content []byte) (InputResource, bool, error) {
	var existing InputResource
	err := r.db.WithContext(ctx).Where("owner_user_id = ? AND content_hash = ?", owner, hash).First(&existing).Error
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return InputResource{}, false, err
	}
	created := InputResource{ID: uuid.NewString(), OwnerUserID: owner, Name: name, MimeType: mime,
		Size: int64(len(content)), ContentHash: hash, Revision: 1, Content: content, CreatedAt: time.Now().UTC()}
	if err := r.db.WithContext(ctx).Create(&created).Error; err != nil {
		return InputResource{}, false, err
	}
	return created, true, nil
}

func (r *Repository) BindInput(ctx context.Context, owner string, binding InputBinding) error {
	if err := r.AuthorizeSession(ctx, binding.WorkflowSessionID, owner); err != nil {
		return err
	}
	var resource InputResource
	if err := r.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", binding.ResourceID, owner).First(&resource).Error; err != nil {
		return ErrPermissionDenied
	}
	if resource.Revision != binding.ResourceRevision || resource.ContentHash != binding.ContentHash {
		return ErrIdempotencyConflict
	}
	binding.ID = uuid.NewString()
	binding.CreatedAt = time.Now().UTC()
	binding.Validity = "effective"
	return r.db.WithContext(ctx).Create(&binding).Error
}

func (r *Repository) GetInputResource(ctx context.Context, owner, id string) (InputResource, error) {
	var resource InputResource
	if err := r.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, owner).First(&resource).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return InputResource{}, ErrNotFound
		}
		return InputResource{}, err
	}
	return resource, nil
}

func (r *Repository) ListInputBindings(ctx context.Context, owner, sessionID string) ([]InputBinding, error) {
	if err := r.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return nil, err
	}
	var values []InputBinding
	err := r.db.WithContext(ctx).Where("workflow_session_id = ? AND validity = 'effective'", sessionID).
		Order("material_id ASC, created_at ASC").Find(&values).Error
	return values, err
}

func (r *Repository) ListArtifacts(ctx context.Context, owner, sessionID string) ([]Artifact, error) {
	if err := r.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return nil, err
	}
	var revisions []orm.WorkflowSlotRevision
	if err := r.db.WithContext(ctx).Where("session_id = ? AND selected = ?", sessionID, true).
		Order("slot_id ASC, list_index ASC, revision ASC").Find(&revisions).Error; err != nil {
		return nil, err
	}
	out := make([]Artifact, 0, len(revisions))
	for _, revision := range revisions {
		value, contentType, caption, err := r.resolveArtifact(ctx, revision)
		if err != nil {
			return nil, err
		}
		out = append(out, Artifact{ID: revision.ID, SessionID: revision.SessionID, SlotID: revision.SlotID,
			Slot: revision.Slot, StepID: revision.StepID, Attempt: revision.Attempt,
			ProducerAttemptID: revision.ProducerAttemptID, Revision: revision.Revision,
			ListIndex: revision.ListIndex, Selected: revision.Selected, Validity: revision.Validity,
			ChangeSource: revision.ChangeSource, ContentType: contentType, Value: value,
			Caption: caption, Deleted: revision.Validity == "deleted", CreatedAt: revision.CreatedAt})
	}
	return out, nil
}

func (r *Repository) resolveArtifact(ctx context.Context, revision orm.WorkflowSlotRevision) (json.RawMessage, string, *string, error) {
	if revision.HumanArtifactID != nil {
		var value orm.WorkflowHumanArtifact
		if err := r.db.WithContext(ctx).Where("id = ?", *revision.HumanArtifactID).First(&value).Error; err != nil {
			return nil, "", nil, err
		}
		return append(json.RawMessage(nil), value.Value...), value.ContentType, value.Caption, nil
	}
	if revision.ArtifactSeq != nil {
		var step orm.WorkflowSessionStep
		if err := r.db.WithContext(ctx).Where("session_id = ? AND step_id = ? AND attempt = ?",
			revision.SessionID, revision.StepID, revision.Attempt).First(&step).Error; err != nil {
			return nil, "", nil, err
		}
		var value orm.SubAgentArtifact
		if err := r.db.WithContext(ctx).Where("task_id = ? AND slot = ? AND seq = ?",
			step.TaskID, revision.Slot, *revision.ArtifactSeq).First(&value).Error; err != nil {
			return nil, "", nil, err
		}
		return append(json.RawMessage(nil), value.Value...), value.ContentType, value.Caption, nil
	}
	return append(json.RawMessage(nil), revision.ContentSnapshot...), "json", nil, nil
}

func (r *Repository) ReadArtifact(ctx context.Context, owner, artifactID string) (Artifact, error) {
	var revision orm.WorkflowSlotRevision
	if err := r.db.WithContext(ctx).Where("id = ?", artifactID).First(&revision).Error; err != nil {
		return Artifact{}, ErrNotFound
	}
	if err := r.AuthorizeSession(ctx, revision.SessionID, owner); err != nil {
		return Artifact{}, err
	}
	value, contentType, caption, err := r.resolveArtifact(ctx, revision)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{ID: revision.ID, SessionID: revision.SessionID, SlotID: revision.SlotID,
		Slot: revision.Slot, StepID: revision.StepID, Attempt: revision.Attempt,
		ProducerAttemptID: revision.ProducerAttemptID, Revision: revision.Revision,
		ListIndex: revision.ListIndex, Selected: revision.Selected, Validity: revision.Validity,
		ChangeSource: revision.ChangeSource, ContentType: contentType, Value: value,
		Caption: caption, Deleted: revision.Validity == "deleted", CreatedAt: revision.CreatedAt}, nil
}

func (r *Repository) PatchArtifact(ctx context.Context, owner, artifactID string, baseRevision int,
	contentType string, value json.RawMessage, caption *string, commandID string) (Artifact, error) {
	current, err := r.ReadArtifact(ctx, owner, artifactID)
	if err != nil {
		return Artifact{}, err
	}
	if !current.Selected || current.Deleted || current.Revision != baseRevision {
		return Artifact{}, ErrIdempotencyConflict
	}
	now := time.Now().UTC()
	humanID, revisionID := uuid.NewString(), uuid.NewString()
	var created orm.WorkflowSlotRevision
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&orm.WorkflowSlotRevision{}).Where(
			"session_id = ? AND slot_id = ? AND selected = ?", current.SessionID, current.SlotID, true)
		if current.ListIndex == nil {
			query = query.Where("list_index IS NULL")
		} else {
			query = query.Where("list_index = ?", *current.ListIndex)
		}
		result := query.Where("revision = ?", baseRevision).Update("selected", false)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrIdempotencyConflict
		}
		if err := tx.Create(&orm.WorkflowHumanArtifact{ID: humanID, SessionID: current.SessionID,
			Slot: current.Slot, ContentType: contentType, Value: value, Caption: caption, CreatedAt: now}).Error; err != nil {
			return err
		}
		created = orm.WorkflowSlotRevision{ID: revisionID, SessionID: current.SessionID,
			SlotID: current.SlotID, Revision: baseRevision + 1, ListIndex: current.ListIndex, Selected: true,
			HumanArtifactID: &humanID, ChangeSource: "agent", ProducerAttemptID: current.ProducerAttemptID,
			Slot: current.Slot, StepID: current.StepID,
			Attempt: current.Attempt, Validity: "effective", CreatedAt: now}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		var session orm.WorkflowSession
		if err := tx.Where("id = ?", current.SessionID).First(&session).Error; err != nil {
			return err
		}
		stateVersion := session.StateVersion + 1
		if err := tx.Model(&session).Updates(map[string]any{"state_version": stateVersion, "updated_at": now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"artifact_id": created.ID, "slot_id": created.SlotID,
			"revision": created.Revision, "state_version": stateVersion})
		return tx.Create(&orm.WorkflowEvent{SessionID: current.SessionID, OwnerUserID: owner,
			ContractVersion: "workflow.v1", EventType: "artifact.upsert", EntityID: created.ID,
			StateVersion: stateVersion, CommandID: commandID, PayloadJSON: payload, CreatedAt: now}).Error
	})
	if err != nil {
		return Artifact{}, err
	}
	return r.ReadArtifact(ctx, owner, created.ID)
}

// DeleteArtifact creates an immutable selected tombstone revision. Historical
// revisions and their bytes remain readable by id for lineage and audit.
func (r *Repository) DeleteArtifact(ctx context.Context, owner, artifactID string,
	baseRevision int, commandID string) (Artifact, error) {
	current, err := r.ReadArtifact(ctx, owner, artifactID)
	if err != nil {
		return Artifact{}, err
	}
	if !current.Selected || current.Deleted || current.Revision != baseRevision {
		return Artifact{}, ErrIdempotencyConflict
	}
	now := time.Now().UTC()
	humanID, revisionID := uuid.NewString(), uuid.NewString()
	var created orm.WorkflowSlotRevision
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&orm.WorkflowSlotRevision{}).Where(
			"session_id = ? AND slot_id = ? AND selected = ?", current.SessionID, current.SlotID, true)
		if current.ListIndex == nil {
			query = query.Where("list_index IS NULL")
		} else {
			query = query.Where("list_index = ?", *current.ListIndex)
		}
		result := query.Where("revision = ?", baseRevision).Update("selected", false)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrIdempotencyConflict
		}
		caption := "deleted"
		if err := tx.Create(&orm.WorkflowHumanArtifact{ID: humanID, SessionID: current.SessionID,
			Slot: current.Slot, ContentType: "application/x-lazymind-deleted",
			Value: json.RawMessage(`null`), Caption: &caption, CreatedAt: now}).Error; err != nil {
			return err
		}
		created = orm.WorkflowSlotRevision{ID: revisionID, SessionID: current.SessionID,
			SlotID: current.SlotID, Revision: baseRevision + 1, ListIndex: current.ListIndex, Selected: true,
			HumanArtifactID: &humanID, ChangeSource: "agent", ProducerAttemptID: current.ProducerAttemptID,
			Slot: current.Slot, StepID: current.StepID,
			Attempt: current.Attempt, Validity: "deleted", CreatedAt: now}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		var session orm.WorkflowSession
		if err := tx.Where("id = ?", current.SessionID).First(&session).Error; err != nil {
			return err
		}
		stateVersion := session.StateVersion + 1
		if err := tx.Model(&session).Updates(map[string]any{
			"state_version": stateVersion, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"artifact_id": created.ID,
			"previous_artifact_id": current.ID, "slot_id": created.SlotID,
			"revision": created.Revision, "deleted": true, "state_version": stateVersion})
		return tx.Create(&orm.WorkflowEvent{SessionID: current.SessionID, OwnerUserID: owner,
			ContractVersion: "workflow.v1", EventType: "artifact.delete", EntityID: created.ID,
			StateVersion: stateVersion, CommandID: commandID, PayloadJSON: payload, CreatedAt: now}).Error
	})
	if err != nil {
		return Artifact{}, err
	}
	return r.ReadArtifact(ctx, owner, created.ID)
}

func (r *Repository) CommandByID(ctx context.Context, owner, commandID string) (Command, error) {
	var value Command
	if err := r.db.WithContext(ctx).Where("command_id = ? AND owner_user_id = ?", commandID, owner).First(&value).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Command{}, ErrNotFound
		}
		return Command{}, err
	}
	return value, nil
}

func (r *Repository) UpdateCommandResponse(ctx context.Context, owner, commandID string, status int, response json.RawMessage) error {
	result := r.db.WithContext(ctx).Model(&Command{}).Where("command_id = ? AND owner_user_id = ?", commandID, owner).
		Updates(map[string]any{"http_status": status, "response_json": response})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) SetSessionStopped(ctx context.Context, owner, sessionID, commandID string, stop bool) (int64, error) {
	if err := r.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return 0, err
	}
	var version int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session orm.WorkflowSession
		if err := tx.Where("id = ? AND create_user_id = ?", sessionID, owner).First(&session).Error; err != nil {
			return err
		}
		status := "active"
		if stop {
			status = "stopped"
			if err := tx.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND status IN ?", sessionID,
				[]string{"queued", "claimed", "running", "pending"}).Updates(map[string]any{
				"status": "interrupted", "terminal_code": "WORKFLOW_STOPPED", "lease_expires_at": nil,
				"updated_at": time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.WorkflowOutbox{}).Where("session_id = ? AND status IN ?", sessionID,
				[]string{"pending", "claimed"}).Updates(map[string]any{"status": "cancelled", "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		} else if session.Status != "stopped" {
			return repositoryError("WORKFLOW_NOT_STOPPED")
		}
		version = session.StateVersion + 1
		if err := tx.Model(&orm.WorkflowSession{}).Where("id = ?", sessionID).Updates(map[string]any{
			"status": status, "state_version": version, "updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"session_id": sessionID, "status": status, "state_version": version})
		return tx.Create(&orm.WorkflowEvent{SessionID: sessionID, OwnerUserID: owner, ContractVersion: "workflow.v1",
			EventType: "workflow.patch", EntityID: sessionID, StateVersion: version, CommandID: commandID,
			PayloadJSON: payload, CreatedAt: time.Now().UTC()}).Error
	})
	return version, err
}

func (r *Repository) CreateHostSession(ctx context.Context, owner, sessionID, conversationID, originHost,
	originRef, controllerHost string, workflow WorkflowPackage) (orm.WorkflowSession, bool, error) {
	var existing orm.WorkflowSession
	if err := r.db.WithContext(ctx).Where("id = ?", sessionID).First(&existing).Error; err == nil {
		if existing.CreateUserID != owner || existing.WorkflowRevisionID != workflow.RevisionID {
			return orm.WorkflowSession{}, false, ErrIdempotencyConflict
		}
		return existing, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.WorkflowSession{}, false, err
	}
	if conversationID != "" {
		var count int64
		if err := r.db.WithContext(ctx).Model(&orm.WorkflowSession{}).
			Where("conversation_id = ? AND dismissed = false", conversationID).
			Count(&count).Error; err != nil {
			return orm.WorkflowSession{}, false, err
		}
		if count > 0 {
			return orm.WorkflowSession{}, false, ErrSessionConflict
		}
	}
	if originHost == "" {
		originHost = "lazymind"
	}
	if controllerHost == "" {
		controllerHost = originHost
	}
	now := time.Now().UTC()
	created := orm.WorkflowSession{ID: sessionID, ConversationID: conversationID, OriginHost: originHost,
		OriginRef: originRef, ControllerHost: controllerHost, WorkflowID: workflow.WorkflowID,
		WorkflowRef: workflow.WorkflowRef, WorkflowRevisionID: workflow.RevisionID,
		WorkflowRevisionNo: workflow.RevisionNo, WorkflowTreeHash: workflow.TreeHash,
		StateVersion: 1, GraphHash: workflow.GraphHash, GraphSchemaVersion: workflow.GraphVersion,
		Status: "active", CreateUserID: owner, CreatedAt: now, UpdatedAt: now}
	if err := r.db.WithContext(ctx).Create(&created).Error; err != nil {
		return orm.WorkflowSession{}, false, err
	}
	payload, _ := json.Marshal(map[string]any{"session_id": sessionID, "status": "active", "state_version": 1})
	_ = r.AppendEvent(ctx, &Event{SessionID: sessionID, OwnerUserID: owner, EventType: "workflow.snapshot",
		EntityID: sessionID, StateVersion: 1, PayloadJSON: payload})
	return created, true, nil
}

func (r *Repository) UpdateSessionIntent(ctx context.Context, sessionID, intentContext string) error {
	return r.db.WithContext(ctx).Model(&orm.WorkflowSession{}).
		Where("id = ?", sessionID).
		Updates(map[string]any{
			"intent_context": intentContext,
			"updated_at":     time.Now().UTC(),
		}).Error
}

type TaskAttemptStatus struct {
	TaskID       string `json:"task_id"`
	StepID       string `json:"step_id"`
	Status       string `json:"status"`
	Attempt      int    `json:"attempt"`
	TerminalCode string `json:"terminal_code,omitempty"`
}

func (r *Repository) AutomaticAttemptCount(ctx context.Context, sessionID, stepID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&orm.WorkflowTransitionCommand{}).
		Where("session_id = ? AND target_step_id = ? AND operation IN ? AND retry_origin = ? AND status = ?",
			sessionID, stepID, []string{"execute", "retry"}, "automatic", "accepted").Count(&count).Error
	return count, err
}

func (r *Repository) WaitTaskStatuses(ctx context.Context, sessionID string, taskIDs []string) (map[string]TaskAttemptStatus, error) {
	statuses := map[string]TaskAttemptStatus{}
	if len(taskIDs) == 0 {
		return statuses, nil
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		var rows []orm.WorkflowSessionStep
		if err := r.db.WithContext(ctx).Where("session_id = ? AND task_id IN ?", sessionID, taskIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		terminal := len(rows) == len(taskIDs)
		for _, row := range rows {
			statuses[row.TaskID] = TaskAttemptStatus{TaskID: row.TaskID, StepID: row.StepID,
				Status: row.Status, Attempt: row.Attempt, TerminalCode: row.TerminalCode}
			if row.Status != "succeeded" && row.Status != "failed" && row.Status != "cancelled" && row.Status != "interrupted" {
				terminal = false
			}
		}
		if terminal {
			return statuses, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Repository) AutoMigrate() error { return r.db.AutoMigrate(Models()...) }

func (r *Repository) PreparationByKey(ctx context.Context, owner, key string) (Preparation, error) {
	var prepared Preparation
	err := r.db.WithContext(ctx).Where("owner_user_id = ? AND idempotency_key = ?", owner, key).First(&prepared).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Preparation{}, ErrNotFound
	}
	return prepared, err
}

func (r *Repository) Prepare(ctx context.Context, owner, key, workflowID, version string, request, response json.RawMessage) (Preparation, bool, error) {
	var existing Preparation
	err := r.db.WithContext(ctx).Where("owner_user_id = ? AND idempotency_key = ?", owner, key).First(&existing).Error
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Preparation{}, false, err
	}
	now := time.Now().UTC()
	created := Preparation{ID: uuid.NewString(), IdempotencyKey: key, OwnerUserID: owner, WorkflowID: workflowID, ContractVersion: version, RequestJSON: request, ResponseJSON: response, CreatedAt: now, UpdatedAt: now}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&created)
	if result.Error != nil {
		return Preparation{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		if err := r.db.WithContext(ctx).Where("owner_user_id = ? AND idempotency_key = ?", owner, key).First(&existing).Error; err != nil {
			return Preparation{}, false, err
		}
		return existing, false, nil
	}
	return created, true, nil
}

func (r *Repository) Consume(ctx context.Context, id, owner, sessionID string) (Preparation, bool, error) {
	var result Preparation
	consumed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&result).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if result.OwnerUserID != owner {
			return ErrPermissionDenied
		}
		if result.ConsumedAt != nil {
			return nil
		}
		now := time.Now().UTC()
		if err := tx.Model(&Preparation{}).Where("id = ? AND consumed_at IS NULL", id).Updates(map[string]any{"consumed_at": now, "session_id": sessionID, "updated_at": now}).Error; err != nil {
			return err
		}
		result.ConsumedAt, result.SessionID, result.UpdatedAt = &now, sessionID, now
		consumed = true
		return nil
	})
	return result, consumed, err
}

func requestHash(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }

func (r *Repository) Command(ctx context.Context, owner, sessionID, commandID, version string, request []byte, execute func(*gorm.DB) (int, json.RawMessage, error)) (Command, bool, error) {
	if r.db.Dialector.Name() == "sqlite" {
		// The delegated transition uses the shared Core DB directly. Keeping an
		// outer SQLite transaction open while it executes makes that inner write
		// contend with our own read snapshot and produces SQLITE_BUSY_SNAPSHOT.
		// Serialize local commands, execute without an outer transaction, then
		// persist the facade result in a short retryable transaction.
		r.commandMu.Lock()
		defer r.commandMu.Unlock()
		return r.commandSQLite(ctx, owner, sessionID, commandID, version, request, execute)
	}
	return r.commandTransactional(ctx, owner, sessionID, commandID, version, request, execute)
}

func (r *Repository) commandTransactional(ctx context.Context, owner, sessionID, commandID, version string, request []byte, execute func(*gorm.DB) (int, json.RawMessage, error)) (Command, bool, error) {
	var result Command
	var committedEvent *Event
	hash := requestHash(request)
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("command_id = ?", commandID).First(&result).Error; err == nil {
			if result.OwnerUserID != owner {
				return ErrPermissionDenied
			}
			if result.RequestHash != hash {
				return ErrIdempotencyConflict
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		status, response, err := execute(tx)
		if err != nil {
			return err
		}
		result = Command{CommandID: commandID, OwnerUserID: owner, SessionID: sessionID, ContractVersion: version, RequestHash: hash, HTTPStatus: status, ResponseJSON: response, CreatedAt: time.Now().UTC()}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if status < 400 {
			var responseObject map[string]any
			_ = json.Unmarshal(response, &responseObject)
			stateVersion, _ := responseObject["state_version"].(float64)
			event := &Event{SessionID: sessionID, OwnerUserID: owner, ContractVersion: version, EventType: "workflow.patch", EntityID: sessionID, StateVersion: int64(stateVersion), CommandID: commandID, PayloadJSON: response, CreatedAt: time.Now().UTC()}
			if err := tx.Create(event).Error; err != nil {
				return err
			}
			committedEvent = event
		}
		created = true
		return nil
	})
	if err != nil {
		return Command{}, false, err
	}
	if committedEvent != nil {
		r.publish(*committedEvent)
	}
	return result, created, nil
}

func (r *Repository) commandSQLite(ctx context.Context, owner, sessionID, commandID, version string, request []byte, execute func(*gorm.DB) (int, json.RawMessage, error)) (Command, bool, error) {
	hash := requestHash(request)
	var existing Command
	if err := r.db.WithContext(ctx).Where("command_id = ?", commandID).First(&existing).Error; err == nil {
		if existing.OwnerUserID != owner {
			return Command{}, false, ErrPermissionDenied
		}
		if existing.RequestHash != hash {
			return Command{}, false, ErrIdempotencyConflict
		}
		return existing, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Command{}, false, err
	}

	status, response, err := execute(r.db.WithContext(ctx))
	if err != nil {
		return Command{}, false, err
	}
	result := Command{CommandID: commandID, OwnerUserID: owner, SessionID: sessionID,
		ContractVersion: version, RequestHash: hash, HTTPStatus: status,
		ResponseJSON: response, CreatedAt: time.Now().UTC()}
	var committedEvent *Event
	err = common.TransactionWithSQLiteBusyRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if status >= 400 {
			return nil
		}
		var responseObject map[string]any
		_ = json.Unmarshal(response, &responseObject)
		stateVersion, _ := responseObject["state_version"].(float64)
		committedEvent = &Event{SessionID: sessionID, OwnerUserID: owner,
			ContractVersion: version, EventType: "workflow.patch", EntityID: sessionID,
			StateVersion: int64(stateVersion), CommandID: commandID,
			PayloadJSON: response, CreatedAt: time.Now().UTC()}
		return tx.Create(committedEvent).Error
	})
	if err != nil {
		return Command{}, false, err
	}
	if committedEvent != nil {
		r.publish(*committedEvent)
	}
	return result, true, nil
}

func (r *Repository) AppendEvent(ctx context.Context, event *Event) error {
	if event.ContractVersion == "" {
		event.ContractVersion = "workflow.v1"
	}
	event.CreatedAt = time.Now().UTC()
	if err := r.db.WithContext(ctx).Create(event).Error; err != nil {
		return err
	}
	r.publish(*event)
	return nil
}

func (r *Repository) publish(event Event) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for ch := range r.subs[event.SessionID] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (r *Repository) Replay(ctx context.Context, sessionID, owner string, after int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	var events []Event
	err := r.db.WithContext(ctx).Where("session_id = ? AND owner_user_id = ? AND id > ?", sessionID, owner, after).Order("id ASC").Limit(limit).Find(&events).Error
	return events, err
}

// LatestEventID returns the cursor represented by a freshly loaded session snapshot.
// It lets a new stream start from current state instead of replaying creation-time
// patches on top of that newer snapshot.
func (r *Repository) LatestEventID(ctx context.Context, sessionID, owner string) (int64, error) {
	var row struct {
		Cursor int64 `gorm:"column:cursor"`
	}
	err := r.db.WithContext(ctx).Model(&orm.WorkflowEvent{}).
		Select("COALESCE(MAX(id), 0) AS cursor").
		Where("session_id = ? AND owner_user_id = ?", sessionID, owner).
		Scan(&row).Error
	return row.Cursor, err
}

func (r *Repository) AuthorizeSession(ctx context.Context, sessionID, owner string) error {
	var session struct {
		CreateUserID string `gorm:"column:create_user_id"`
	}
	result := r.db.WithContext(ctx).Table("plugin_sessions").Select("create_user_id").Where("id = ?", sessionID).Take(&session)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if result.Error != nil {
		return result.Error
	}
	if session.CreateUserID != owner {
		return ErrPermissionDenied
	}
	return nil
}

func (r *Repository) Subscribe(sessionID string) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	r.mu.Lock()
	if r.subs[sessionID] == nil {
		r.subs[sessionID] = map[chan Event]struct{}{}
	}
	r.subs[sessionID][ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		if _, ok := r.subs[sessionID][ch]; ok {
			delete(r.subs[sessionID], ch)
			close(ch)
		}
		r.mu.Unlock()
	}
}
