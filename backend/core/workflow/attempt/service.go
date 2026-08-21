package attempt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"lazymind/core/common/orm"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	CodeLeaseLost         = "ATTEMPT_LEASE_LOST"
	CodeAlreadyTerminal   = "ATTEMPT_ALREADY_TERMINAL"
	CodeNotFound          = "ATTEMPT_NOT_FOUND"
	CodeNotClaimable      = "ATTEMPT_NOT_CLAIMABLE"
	CodeSchemaUnavailable = "WORKFLOW_ATTEMPT_SCHEMA_UNAVAILABLE"
)

var (
	ErrLeaseLost         = protocolError(CodeLeaseLost)
	ErrAlreadyTerminal   = protocolError(CodeAlreadyTerminal)
	ErrNotFound          = protocolError(CodeNotFound)
	ErrNotClaimable      = protocolError(CodeNotClaimable)
	ErrSchemaUnavailable = protocolError(CodeSchemaUnavailable)
)

type protocolError string

func (e protocolError) Error() string { return string(e) }

type Config struct {
	LeaseDuration time.Duration
}

func (c Config) leaseDuration() time.Duration {
	if c.LeaseDuration <= 0 {
		return 30 * time.Second
	}
	return c.LeaseDuration
}

type Service struct {
	db     *gorm.DB
	config Config
	now    func() time.Time
}

// ServiceCapable identifies the protocol implementation compiled into this
// binary. Deployment capability additionally requires SchemaCapable.
func ServiceCapable() bool { return ContractVersion == "workflow.v1" }

func New(db *gorm.DB, config Config) *Service {
	return &Service{db: db, config: config, now: func() time.Time { return time.Now().UTC() }}
}

func SchemaCapable(db *gorm.DB) bool {
	if db == nil || !db.Migrator().HasTable(&orm.WorkflowOutbox{}) {
		return false
	}
	for _, column := range []string{"lease_owner", "lease_token", "fencing_generation", "lease_expires_at", "heartbeat_at", "progress_json", "terminal_code", "result_json"} {
		if !db.Migrator().HasColumn(&orm.WorkflowSessionStep{}, column) {
			return false
		}
	}
	return true
}

type QueueRequest struct {
	AttemptID   string
	SessionID   string
	StepID      string
	AttemptNo   int
	Payload     json.RawMessage
	OwnerUserID string
}

func appendEvent(tx *gorm.DB, row orm.WorkflowSessionStep, owner, eventType string, payload json.RawMessage, now time.Time) error {
	if owner == "" {
		var session orm.WorkflowSession
		if err := tx.Select("create_user_id").Where("id = ?", row.SessionID).First(&session).Error; err == nil {
			owner = session.CreateUserID
		}
	}
	return tx.Create(&orm.WorkflowEvent{SessionID: row.SessionID, OwnerUserID: owner,
		ContractVersion: ContractVersion, EventType: eventType, EntityID: row.ID,
		PayloadJSON: payload, CreatedAt: now}).Error
}

// Queue persists the authoritative queued Attempt and generic Outbox in one
// transaction. The legacy LazyMind worker outbox is intentionally untouched.
func (s *Service) Queue(ctx context.Context, request QueueRequest) (orm.WorkflowSessionStep, error) {
	if !SchemaCapable(s.db) {
		return orm.WorkflowSessionStep{}, ErrSchemaUnavailable
	}
	if request.AttemptID == "" {
		request.AttemptID = uuid.NewString()
	}
	if request.AttemptNo < 1 {
		request.AttemptNo = 1
	}
	now := s.now()
	row := orm.WorkflowSessionStep{ID: request.AttemptID, SessionID: request.SessionID,
		StepID: request.StepID, Attempt: request.AttemptNo, TaskID: request.AttemptID,
		Status: "queued", Validity: "effective", ProgressJSON: `{}`,
		ResultJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		outbox := orm.WorkflowOutbox{ID: uuid.NewString(), AttemptID: row.ID,
			SessionID: row.SessionID, PayloadJSON: request.Payload, Status: "pending",
			CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&outbox).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"attempt_id": row.ID, "step_id": row.StepID, "status": "queued"})
		return appendEvent(tx, row, request.OwnerUserID, "attempt.patch", payload, now)
	})
	return row, err
}

type Claim struct {
	AttemptID         string    `json:"attempt_id"`
	LeaseToken        string    `json:"lease_token"`
	LeaseExpiresAt    time.Time `json:"lease_expires_at"`
	FencingGeneration int64     `json:"fencing_generation"`
}

func newToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// Claim uses a compare-and-swap update. Expired claimed/running Attempts may
// be reclaimed and always receive a new token and higher fencing generation.
func (s *Service) Claim(ctx context.Context, executorID string) (Claim, error) {
	return s.ClaimForHost(ctx, executorID, "")
}

// ClaimForHost restricts ownership to Sessions controlled by the requested
// Host. An empty Host is retained only for compatibility and tests.
func (s *Service) ClaimForHost(ctx context.Context, executorID, host string) (Claim, error) {
	if !SchemaCapable(s.db) {
		return Claim{}, ErrSchemaUnavailable
	}
	now := s.now()
	var candidate orm.WorkflowSessionStep
	query := s.db.WithContext(ctx).Model(&orm.WorkflowSessionStep{}).Where(
		"plugin_session_steps.validity = 'effective' AND (plugin_session_steps.status = 'queued' OR "+ // workflow-naming: persistence
			"(plugin_session_steps.status IN ('claimed','running') AND plugin_session_steps.lease_expires_at < ?))", now, // workflow-naming: persistence
	)
	if host != "" {
		query = query.Joins("JOIN plugin_sessions ps ON ps.id = plugin_session_steps.session_id").
			Where("COALESCE(ps.controller_host, 'lazymind') = ?", host)
	}
	err := query.Order("plugin_session_steps.created_at ASC").First(&candidate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Claim{}, ErrNotClaimable
	}
	if err != nil {
		return Claim{}, err
	}
	return s.claimCandidate(ctx, candidate, executorID, false)
}

// ClaimAttemptForHost claims one exact Attempt for an interactive Host. A
// repeat from the same executor rotates the lease, which lets a restarted MCP
// connection resume without exposing the internal lease token to the model.
func (s *Service) ClaimAttemptForHost(ctx context.Context, attemptID, executorID, host string) (Claim, error) {
	if !SchemaCapable(s.db) {
		return Claim{}, ErrSchemaUnavailable
	}
	var candidate orm.WorkflowSessionStep
	query := s.db.WithContext(ctx).Model(&orm.WorkflowSessionStep{}).
		Select("plugin_session_steps.*").                                                               // workflow-naming: persistence
		Where("plugin_session_steps.id = ? AND plugin_session_steps.validity = 'effective'", attemptID) // workflow-naming: persistence
	if host != "" {
		query = query.Joins("JOIN plugin_sessions ps ON ps.id = plugin_session_steps.session_id").
			Where("COALESCE(ps.controller_host, 'lazymind') = ?", host)
	}
	if err := query.First(&candidate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Claim{}, ErrNotClaimable
		}
		return Claim{}, err
	}
	return s.claimCandidate(ctx, candidate, executorID, true)
}

func (s *Service) claimCandidate(ctx context.Context, candidate orm.WorkflowSessionStep, executorID string, allowCurrentOwner bool) (Claim, error) {
	now := s.now()
	token, err := newToken()
	if err != nil {
		return Claim{}, err
	}
	expires := now.Add(s.config.leaseDuration())
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		condition := "id = ? AND fencing_generation = ? AND (status = 'queued' OR lease_expires_at < ?"
		args := []any{candidate.ID, candidate.FencingGeneration, now}
		if allowCurrentOwner {
			condition += " OR (lease_owner = ? AND status IN ('claimed','running'))"
			args = append(args, executorID)
		}
		condition += ")"
		result := tx.Model(&orm.WorkflowSessionStep{}).Where(condition, args...).
			Updates(map[string]any{"status": "claimed", "lease_owner": executorID,
				"lease_token": token, "fencing_generation": gorm.Expr("fencing_generation + 1"),
				"lease_expires_at": expires, "heartbeat_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNotClaimable
		}
		if err := tx.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ? AND status IN ('pending','claimed')", candidate.ID).
			Updates(map[string]any{"status": "claimed", "updated_at": now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"attempt_id": candidate.ID, "status": "claimed", "fencing_generation": candidate.FencingGeneration + 1})
		return appendEvent(tx, candidate, "", "attempt.patch", payload, now)
	})
	if err != nil {
		return Claim{}, err
	}
	return Claim{AttemptID: candidate.ID, LeaseToken: token, LeaseExpiresAt: expires,
		FencingGeneration: candidate.FencingGeneration + 1}, nil
}

func (s *Service) validLeaseQuery(attemptID, token string, now time.Time) *gorm.DB {
	return s.db.Model(&orm.WorkflowSessionStep{}).Where(
		"id = ? AND lease_token = ? AND lease_expires_at >= ? AND status IN ('claimed','running')",
		attemptID, token, now,
	)
}

// ValidateLease verifies that a remote Executor still owns a live Attempt lease.
// Remote context, input and Artifact endpoints use this same check so a stale
// worker cannot observe or mutate execution state after fencing advances.
func (s *Service) ValidateLease(ctx context.Context, attemptID, token string) error {
	var count int64
	err := s.validLeaseQuery(attemptID, token, s.now()).WithContext(ctx).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Service) Heartbeat(ctx context.Context, attemptID, token string) (time.Time, error) {
	now := s.now()
	expires := now.Add(s.config.leaseDuration())
	result := s.validLeaseQuery(attemptID, token, now).WithContext(ctx).
		Updates(map[string]any{"lease_expires_at": expires, "heartbeat_at": now, "updated_at": now})
	if result.Error != nil {
		return time.Time{}, result.Error
	}
	if result.RowsAffected != 1 {
		return time.Time{}, ErrLeaseLost
	}
	return expires, nil
}

func (s *Service) Progress(ctx context.Context, attemptID, token string, progress json.RawMessage) error {
	now := s.now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&orm.WorkflowSessionStep{}).Where(
			"id = ? AND lease_token = ? AND lease_expires_at >= ? AND status IN ('claimed','running')",
			attemptID, token, now).Updates(map[string]any{"status": "running", "progress_json": progress, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLeaseLost
		}
		var row orm.WorkflowSessionStep
		if err := tx.Where("id = ?", attemptID).First(&row).Error; err != nil {
			return err
		}
		return appendEvent(tx, row, "", "attempt.progress", progress, now)
	})
}

func terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "interrupted"
}

// Terminal implements first-valid-write-wins. Repeating the winning status is
// idempotent; a competing terminal status returns ATTEMPT_ALREADY_TERMINAL.
func (s *Service) Terminal(ctx context.Context, attemptID, token, status, code string, resultJSON json.RawMessage) error {
	if !terminal(status) {
		return ErrAlreadyTerminal
	}
	now := s.now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current orm.WorkflowSessionStep
		if err := tx.Where("id = ?", attemptID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if terminal(current.Status) {
			if current.Status == status {
				return nil
			}
			return ErrAlreadyTerminal
		}
		if current.LeaseToken != token || current.LeaseExpiresAt == nil || current.LeaseExpiresAt.Before(now) {
			return ErrLeaseLost
		}
		updated := tx.Model(&orm.WorkflowSessionStep{}).
			Where("id = ? AND lease_token = ? AND status IN ('claimed','running')", attemptID, token).
			Updates(map[string]any{"status": status, "terminal_code": code, "result_json": resultJSON,
				"lease_expires_at": nil, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAlreadyTerminal
		}
		outboxStatus := "completed"
		if status == "cancelled" {
			outboxStatus = "cancelled"
		} else if status == "failed" || status == "interrupted" {
			outboxStatus = "failed"
		}
		if err := tx.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", attemptID).
			Updates(map[string]any{"status": outboxStatus, "updated_at": now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"attempt_id": attemptID, "status": status, "error_code": code})
		return appendEvent(tx, current, "", "attempt.patch", payload, now)
	})
}

func (s *Service) Complete(ctx context.Context, attemptID, token string, result json.RawMessage) error {
	return s.Terminal(ctx, attemptID, token, "succeeded", "", result)
}

func (s *Service) Fail(ctx context.Context, attemptID, token, code string, result json.RawMessage) error {
	return s.Terminal(ctx, attemptID, token, "failed", code, result)
}

func (s *Service) Cancel(ctx context.Context, attemptID, token string) error {
	return s.Terminal(ctx, attemptID, token, "cancelled", "CANCELLED", json.RawMessage(`{}`))
}

func (s *Service) Attempt(ctx context.Context, id string) (orm.WorkflowSessionStep, error) {
	var row orm.WorkflowSessionStep
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	return row, err
}
