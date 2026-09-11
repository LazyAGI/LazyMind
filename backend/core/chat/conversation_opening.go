package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/modelconfig"
)

const openingGeneratorVersion = "v1"
const openingJobType = "conversation.opening"
const openingBackfillJobType = "conversation.opening.backfill"

var ConversationOpeningJobTypes = []string{openingJobType, openingBackfillJobType}
var conversationOpeningChanged func(context.Context, *gorm.DB, string) error

type openingJobPayload struct {
	SeedRevision int64 `json:"seed_revision"`
	UseDefault   bool  `json:"use_default"`
}

type openingService struct {
	db         *gorm.DB
	call       func(context.Context, json.RawMessage, map[string]any, int) (algo.OpeningTaskResult, error)
	loadConfig func(context.Context, *gorm.DB, string) (map[string]any, error)
}

func openingIgnoredHistoryIDs(meta orm.ConversationOpening) []string {
	var ids []string
	_ = json.Unmarshal(meta.SourceHistoryIDs, &ids)
	if meta.IntentStatus == "empty" {
		return ids
	}
	ignored := len(ids) - meta.OpeningTurns
	if ignored <= 0 {
		return nil
	}
	return ids[:ignored]
}

func newOpeningService(db *gorm.DB) *openingService {
	return &openingService{db: db, call: algo.DescribeConversationOpening, loadConfig: modelconfig.LoadLLMConfig}
}

func RegisterConversationOpeningJobs(db *gorm.DB) {
	service := newOpeningService(db)
	asyncjob.Register(openingJobType, service.generate)
	asyncjob.Register(openingBackfillJobType, service.backfill)
	conversationOpeningChanged = func(ctx context.Context, db *gorm.DB, id string) error {
		_, err := newOpeningService(db).enqueue(ctx, id, "")
		return err
	}
}

func StartConversationOpening(ctx context.Context, db *gorm.DB) []<-chan struct{} {
	live := asyncjob.Start(ctx, db, asyncjob.Options{Concurrency: 1, SerializeResources: true, LockTTL: time.Duration(openingOption("LAZYMIND_OPENING_TIMEOUT_SECONDS", 60)+30) * time.Second, JobTypes: []string{openingJobType}})
	history := asyncjob.Start(ctx, db, asyncjob.Options{Concurrency: 1, SerializeResources: true, LockTTL: time.Duration(openingOption("LAZYMIND_OPENING_TIMEOUT_SECONDS", 60)+30) * time.Second, JobTypes: []string{openingBackfillJobType}, YieldToJobTypes: []string{openingJobType}})
	return []<-chan struct{}{live.Done(), history.Done()}
}

func notifyConversationOpening(db *gorm.DB, id string) {
	if conversationOpeningChanged == nil {
		return
	}
	if err := conversationOpeningChanged(context.Background(), db, id); err != nil {
		log.Logger.Warn().Err(err).Str("conversation_id", id).Msg("enqueue conversation opening")
	}
}

func openingConversations(db *gorm.DB) *gorm.DB {
	return db.Model(&orm.Conversation{}).Where("deleted_at IS NULL AND archived_at IS NULL AND archive_folder_id IS NULL AND is_ephemeral = ?", false).
		Where("chat_executor IN ?", []string{"", "lazymind"}).
		Where("NOT EXISTS (SELECT 1 FROM external_agent_bindings b WHERE b.conversation_id = conversations.id)")
}

func (s *openingService) enqueue(ctx context.Context, id, backfillID string) (bool, error) {
	queued := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv orm.Conversation
		if err := openingConversations(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&conv).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var meta orm.ConversationOpening
		err := tx.Where("conversation_id = ?", id).Take(&meta).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		exists := err == nil
		changed := false
		if exists {
			var ids []string
			_ = json.Unmarshal(meta.SourceHistoryIDs, &ids)
			hash, err := openingEvidence(tx, conv, ids)
			if err != nil {
				return err
			}
			changed = hash != meta.EvidenceHash
			if !changed && meta.WindowClosed {
				return nil
			}
		}
		var ignored []string
		if exists && !changed {
			ignored = openingIgnoredHistoryIDs(meta)
		}
		snap, err := loadOpeningSnapshot(tx, conv, ignored...)
		if err != nil {
			return err
		}
		if snap.Active {
			return nil
		}
		if exists && !changed && snap.Hash == meta.SourceHash {
			return nil
		}
		if !exists {
			meta = orm.ConversationOpening{ConversationID: id, UserID: conv.CreateUserID, SeedRevision: 0}
			if conv.TitleSource == "unknown" && conv.DisplayName == snap.DefaultTitle {
				if err := tx.Model(&conv).UpdateColumn("title_source", "default").Error; err != nil {
					return err
				}
				conv.TitleSource = "default"
			}
		}
		if changed {
			meta.GenerationCount = 0
			meta.Summary = ""
			meta.IntentStatus = ""
			meta.WindowClosed = false
		}
		meta.SeedRevision++
		meta.SourceHash, meta.EvidenceHash = snap.Hash, snap.Evidence
		meta.InputJSON = snap.Input
		meta.SourceHistoryIDs, _ = json.Marshal(snap.IDs)
		meta.OpeningTurns = snap.Turns
		meta.TitleRevision = conv.TitleRevision
		meta.CallCount = 0
		meta.Status = "pending"
		meta.ErrorCode = ""
		meta.GeneratorVersion = openingGeneratorVersion
		meta.UpdatedAt = time.Now().UTC()
		if backfillID != "" {
			meta.BackfillID = backfillID
		}
		if snap.Turns == 0 {
			meta.IntentStatus = "empty"
			meta.Status = "done"
			return tx.Save(&meta).Error
		}
		// A new seed gets its own job; the previous execution can no longer write back.
		jobType := openingJobType
		if backfillID != "" {
			jobType = openingBackfillJobType
		}
		queued = true
		return enqueueOpeningVersion(ctx, tx, &meta, jobType)

	})
	return queued, err
}

func enqueueOpeningVersion(ctx context.Context, tx *gorm.DB, meta *orm.ConversationOpening, jobType string) error {
	job, err := asyncjob.Enqueue(ctx, tx, asyncjob.EnqueueRequest{
		JobType: jobType, ResourceType: "conversation", ResourceID: meta.ConversationID,
		IdempotencyKey: fmt.Sprintf("%s:%s:%d:%s", meta.UserID, meta.ConversationID, meta.SeedRevision, openingGeneratorVersion),
		Payload:        openingJobPayload{SeedRevision: meta.SeedRevision}, MaxAttempts: 3, CreateUserID: meta.UserID,
	})
	if err != nil {
		return err
	}
	meta.JobID = job.ID
	return tx.Save(meta).Error
}

func openingOption(name string, fallback int) int {
	n, err := strconv.Atoi(os.Getenv(name))
	if err == nil && n > 0 {
		return n
	}
	return fallback
}

func openingConfigHash(config map[string]any) string {
	return openingHash([]any{config, openingOption("LAZYMIND_OPENING_TIMEOUT_SECONDS", 60)})
}

func (s *openingService) generate(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	var payload openingJobPayload
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	var meta orm.ConversationOpening
	err := s.db.WithContext(ctx).Where("conversation_id = ? AND user_id = ? AND seed_revision = ? AND job_id = ?", job.ResourceID, job.CreateUserID, payload.SeedRevision, job.ID).Take(&meta).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return asyncjob.Result{}, nil
	}
	if err != nil {
		return asyncjob.Result{}, err
	}
	var eligible int64
	if err := openingConversations(s.db.WithContext(ctx)).Where("id = ? AND create_user_id = ?", meta.ConversationID, meta.UserID).Count(&eligible).Error; err != nil {
		return asyncjob.Result{}, err
	}
	if eligible == 0 {
		return asyncjob.Result{}, s.db.WithContext(ctx).Model(&meta).Updates(map[string]any{"status": "skipped", "error_code": "conversation_unavailable"}).Error
	}
	config, err := s.loadConfig(ctx, s.db, job.CreateUserID)
	if err != nil {
		return s.failOpening(ctx, job, meta, "model_configuration", false, err)
	}
	selected, hasAux := config["conversation_metadata"]
	if !hasAux || payload.UseDefault {
		selected = config["llm"]
	}
	requestConfig := map[string]any{}
	if selected != nil {
		requestConfig["llm"] = selected
	}
	// Static runtime configurations remain supported when no dynamic selection exists.
	reserved := s.db.WithContext(ctx).Model(&orm.ConversationOpening{}).
		Where("conversation_id = ? AND seed_revision = ? AND job_id = ? AND call_count < 3", meta.ConversationID, meta.SeedRevision, job.ID).
		Updates(map[string]any{"call_count": gorm.Expr("call_count + 1"), "status": "running", "updated_at": time.Now().UTC()})
	if reserved.Error != nil {
		return asyncjob.Result{}, reserved.Error
	}
	if reserved.RowsAffected == 0 {
		return asyncjob.Result{Permanent: true}, errors.New("opening call budget exhausted or seed replaced")
	}

	return s.runOpeningCall(ctx, job, reporter, meta, requestConfig, hasAux && !payload.UseDefault, openingConfigHash(config))
}

func (s *openingService) runOpeningCall(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter, meta orm.ConversationOpening, config map[string]any, mayFallback bool, configHash string) (asyncjob.Result, error) {
	result, err := s.call(ctx, meta.InputJSON, config, openingOption("LAZYMIND_OPENING_TIMEOUT_SECONDS", 60))
	usage := map[string]any{}
	_ = json.Unmarshal(result.Usage, &usage)
	usage["configuration_hash"] = configHash
	result.Usage, _ = json.Marshal(usage)
	execution := map[string]any{"usage_json": result.Usage}
	if calls, ok := usage["model_calls"].(float64); ok && calls == 0 {
		execution["call_count"] = gorm.Expr("call_count - 1")
	}
	if identity := usage["model_id"]; identity != nil {
		execution["model_id"], _ = json.Marshal(identity)
	}

	if dbErr := s.db.WithContext(ctx).Model(&orm.ConversationOpening{}).Where("conversation_id = ? AND seed_revision = ? AND job_id = ?", meta.ConversationID, meta.SeedRevision, job.ID).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id = ? AND status = ? AND attempt_count = ? AND lock_until > ?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Updates(execution).Error; dbErr != nil {
		return asyncjob.Result{}, dbErr
	}
	if err != nil {
		var transport net.Error
		retryable := errors.As(err, &transport) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
		var upstream *common.HTTPError
		if errors.As(err, &upstream) {
			retryable = upstream.StatusCode == 408 || upstream.StatusCode == 429 || upstream.StatusCode >= 500
		}
		return s.failOpening(ctx, job, meta, "transport_error", retryable, err)
	}
	if result.Status != "succeeded" {
		if result.ErrorCode == "token_limit" && mayFallback {
			var payload openingJobPayload
			_ = json.Unmarshal(job.PayloadJSON, &payload)
			payload.UseDefault = true
			raw, _ := json.Marshal(payload)
			if err := s.db.WithContext(ctx).Model(&orm.AsyncJob{}).Where("id = ? AND status = ? AND attempt_count = ? AND lock_until > ?", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).UpdateColumn("payload_json", raw).Error; err != nil {
				return asyncjob.Result{}, err
			}
			return s.failOpening(ctx, job, meta, "token_limit", true, errors.New("retry with default model"))
		}
		return s.failOpening(ctx, job, meta, result.ErrorCode, result.Retryable, fmt.Errorf("conversation opening model failed: %s", result.ErrorCode))
	}
	rebuild := false
	advanceEmpty := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv orm.Conversation
		if err := openingConversations(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND create_user_id = ?", meta.ConversationID, meta.UserID).Take(&conv).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tx.Model(&orm.ConversationOpening{}).Where("conversation_id = ? AND seed_revision = ?", meta.ConversationID, meta.SeedRevision).Updates(map[string]any{"status": "skipped", "error_code": "conversation_unavailable"}).Error
			}
			return err
		}
		var ids []string
		_ = json.Unmarshal(meta.SourceHistoryIDs, &ids)
		hash, err := openingEvidence(tx, conv, ids)
		if err != nil {
			return err
		}
		if hash != meta.EvidenceHash {
			rebuild = true
			return nil
		}
		var owned int64
		if err := tx.Model(&orm.AsyncJob{}).Where("id = ? AND status = ? AND attempt_count = ? AND lock_until > ?", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Count(&owned).Error; err != nil {
			return err
		}
		if owned == 0 {
			return nil
		}
		missing, _ := json.Marshal(result.Output.MissingContext)
		var usage struct {
			ModelID json.RawMessage `json:"model_id"`
		}
		_ = json.Unmarshal(result.Usage, &usage)
		values := map[string]any{
			"summary": result.Output.Summary, "intent_status": result.Output.IntentStatus, "missing_context": missing,
			"metadata_revision": gorm.Expr("metadata_revision + 1"),
			"model_id":          usage.ModelID, "usage_json": result.Usage, "status": "done", "error_code": "", "updated_at": time.Now().UTC(),
		}
		if result.Output.IntentStatus != "empty" {
			values["generation_count"] = gorm.Expr("generation_count + 1")
			values["window_closed"] = result.Output.IntentStatus == "ready" || meta.GenerationCount+1 >= 3 || meta.BackfillID != ""
		} else if len(ids) >= maxOpeningScannedTurns {
			values["window_closed"] = true
		}
		update := tx.Model(&orm.ConversationOpening{}).Where("conversation_id = ? AND seed_revision = ? AND job_id = ?", meta.ConversationID, meta.SeedRevision, job.ID).Updates(values)
		if update.Error != nil {
			return update.Error
		}
		advanceEmpty = update.RowsAffected == 1 && result.Output.IntentStatus == "empty"
		if update.RowsAffected == 1 && result.Output.Title != "" {
			return tx.Model(&orm.Conversation{}).Where("id = ? AND title_revision = ? AND title_source IN ?", conv.ID, meta.TitleRevision, []string{"default", "auto"}).UpdateColumns(map[string]any{"display_name": result.Output.Title, "title_source": "auto", "title_revision": gorm.Expr("title_revision + 1")}).Error
		}
		return nil
	})
	if err == nil && (rebuild || advanceEmpty) {
		_, err = s.enqueue(ctx, meta.ConversationID, meta.BackfillID)
	}
	return asyncjob.Result{}, err
}

func (s *openingService) failOpening(ctx context.Context, job asyncjob.Job, meta orm.ConversationOpening, code string, retryable bool, err error) (asyncjob.Result, error) {
	state := "failed"
	var current orm.ConversationOpening
	if readErr := s.db.WithContext(ctx).Where("conversation_id = ?", meta.ConversationID).Take(&current).Error; readErr != nil {
		return asyncjob.Result{}, readErr
	}
	if retryable && current.CallCount < 3 {
		state = "pending"
	}
	if dbErr := s.db.WithContext(ctx).Model(&orm.ConversationOpening{}).Where("conversation_id = ? AND seed_revision = ? AND job_id = ?", meta.ConversationID, meta.SeedRevision, job.ID).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id = ? AND status = ? AND attempt_count = ? AND lock_until > ?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Updates(map[string]any{"status": state, "error_code": code, "updated_at": time.Now().UTC()}).Error; dbErr != nil {
		return asyncjob.Result{}, dbErr
	}
	return asyncjob.Result{ErrorCode: code, Permanent: state == "failed"}, err
}
