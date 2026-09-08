package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/log"
	"lazymind/core/store"
)

const maxForkTurns = 2000
const maxForkBytes = 16 << 20

func mergeForkMetadata(ctx context.Context, db *gorm.DB, c orm.Conversation, item map[string]any) error {
	origin, err := loadForkOrigin(ctx, db, c)
	if err != nil {
		return err
	}
	item["fork_origin"] = origin
	reason := forkCapability(c)
	item["fork_capability"] = map[string]any{"supported": reason == "", "reason_code": reason}
	var count int64
	if err := db.WithContext(ctx).Table("conversation_fork_origins o").Joins("JOIN conversations c ON c.id = o.conversation_id").Where("o.source_conversation_id = ? AND c.create_user_id = ? AND c.deleted_at IS NULL", c.ID, c.CreateUserID).Count(&count).Error; err != nil {
		return err
	}
	item["has_fork_descendants"] = count > 0
	return nil
}

type forkPreviewRequest struct {
	SourceHistoryID string `json:"source_history_id"`
}
type forkCreateRequest struct {
	SourceHistoryID        string                     `json:"source_history_id"`
	ExpectedPrefixRevision string                     `json:"expected_prefix_revision"`
	ReplacementModel       *initialChatModelSelection `json:"replacement_model,omitempty"`
	ConfirmedFields        []string                   `json:"confirmed_fields,omitempty"`
	ConfirmedValues        map[string]any             `json:"confirmed_values,omitempty"`
}
type forkConfigIssue struct {
	Field                string `json:"field"`
	Reason               string `json:"reason"`
	SuggestedValue       any    `json:"suggested_value"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}
type forkPreview struct {
	SourceHistoryID       string                     `json:"source_history_id"`
	SourceSeq             int                        `json:"source_seq"`
	SourceHistoryRevision string                     `json:"source_history_revision"`
	PrefixRevision        string                     `json:"prefix_revision"`
	SourceTitle           string                     `json:"source_title"`
	Excerpt               string                     `json:"excerpt"`
	InheritedTurnCount    int                        `json:"inherited_turn_count"`
	InheritedMessageCount int                        `json:"inherited_message_count"`
	ConfigSnapshotSummary conversationConfigSnapshot `json:"config_snapshot_summary"`
	ConfigStatus          string                     `json:"config_status"`
	ConfigIssues          []forkConfigIssue          `json:"config_issues"`
	AttachmentsSummary    []forkAttachment           `json:"attachments_summary"`
	Warnings              []string                   `json:"warnings"`
	CanFork               bool                       `json:"can_fork"`
	ReasonCode            string                     `json:"reason_code,omitempty"`
}
type forkOriginView struct {
	orm.ConversationForkOrigin
	SourceStatus string `json:"source_status"`
	CanLocate    bool   `json:"can_locate"`
}
type forkResult struct {
	Conversation       map[string]any  `json:"conversation"`
	ForkOrigin         *forkOriginView `json:"fork_origin"`
	InheritedTurnCount int             `json:"inherited_turn_count"`
	Warnings           []string        `json:"warnings"`
	Replayed           bool            `json:"replayed"`
}

func forkCapability(c orm.Conversation) string {
	if c.IsEphemeral || c.IsTaskConv || c.ParentConversationID != nil || c.RelationType == conversationRelationSidechat || c.SourceType != "" || (c.ChatExecutor != "" && c.ChatExecutor != ChatExecutorLazyMind) {
		return "FORK_UNSUPPORTED"
	}
	return ""
}

func loadForkPrefix(ctx context.Context, db *gorm.DB, userID, conversationID, historyID string) (orm.Conversation, []orm.ChatHistory, error) {
	var c orm.Conversation
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).Take(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c, nil, forkFail("SOURCE_UNAVAILABLE")
		}
		return c, nil, err
	}
	if reason := forkCapability(c); reason != "" {
		return c, nil, forkFail(reason)
	}
	source, err := conversationSourceFor(ctx, db, userID, conversationID)
	if err != nil {
		return c, nil, err
	}
	if source.Assistant != ChatExecutorLazyMind {
		return c, nil, forkFail("FORK_UNSUPPORTED")
	}
	var target orm.ChatHistory
	if err := db.Where("id = ? AND conversation_id = ?", historyID, conversationID).Take(&target).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return c, nil, err
		}
		var count int64
		if err := db.Model(&orm.MultiAnswersChatHistory{}).Where("id = ? AND conversation_id = ?", historyID, conversationID).Count(&count).Error; err != nil {
			return c, nil, err
		}
		if count > 0 {
			return c, nil, forkFail("ANSWER_SELECTION_REQUIRED")
		}
		return c, nil, forkFail("SOURCE_UNAVAILABLE")
	}
	var histories []orm.ChatHistory
	if err := db.Where("conversation_id = ? AND seq <= ?", conversationID, target.Seq).Order("seq ASC, create_time ASC, id ASC").Limit(maxForkTurns + 1).Find(&histories).Error; err != nil {
		return c, nil, err
	}
	if len(histories) > maxForkTurns {
		return c, nil, forkFail("FORK_TOO_LARGE")
	}
	var candidates int64
	if err := db.Model(&orm.MultiAnswersChatHistory{}).Where("conversation_id = ? AND seq <= ?", conversationID, target.Seq).Count(&candidates).Error; err != nil {
		return c, nil, err
	}
	if candidates > 0 {
		return c, nil, forkFail("ANSWER_SELECTION_REQUIRED")
	}
	size := 0
	for _, h := range histories {
		size += len(h.RawContent) + len(h.Content) + len(h.Result) + len(h.Ext) + len(h.RetrievalResult)
	}
	if size > maxForkBytes {
		return c, nil, forkFail("FORK_TOO_LARGE")
	}
	if err := validateForkPrefix(histories); err != nil {
		return c, nil, err
	}
	if len(histories) == 0 || histories[len(histories)-1].ID != historyID {
		return c, nil, forkFail("ANSWER_SELECTION_REQUIRED")
	}
	// Stable failures can remain in the inherited transcript, but a new branch
	// must start from a successful reply. Legacy completion is checked above.
	if target.RunStatus != "completed" && target.RunStatus != "" {
		return c, nil, forkFail("SOURCE_NOT_COMPLETED")
	}
	return c, histories, nil
}

func prepareForkConfig(ctx context.Context, db *gorm.DB, userID string, h orm.ChatHistory) (conversationConfigSnapshot, []forkConfigIssue, error) {
	s := forkConfigFromHistory(h)
	issues := []forkConfigIssue{}
	add := func(field, reason string, value any) {
		issues = append(issues, forkConfigIssue{field, reason, value, true})
	}
	models, err := loadAvailableChatModels(ctx, db, userID)
	if err != nil {
		return s, nil, err
	}
	if s.Model == nil || findAvailableChatModel(models, s.Model.ModelID) == nil || len(models) == 0 {
		add("model", "MODEL_UNAVAILABLE", nil)
	}
	defaults, err := entryDefaultsForRequest(ctx, db, userID, false)
	if err != nil {
		return s, nil, err
	}
	if _, valid := normalizeThinkingDepth(s.ThinkingDepth); !valid {
		s.ThinkingDepth = defaults.ThinkingDepth
		add("thinking_depth", "missing", s.ThinkingDepth)
	}
	if s.EnableWorkflow == nil {
		value := defaults.ConversationSettings.EnableWorkflow
		s.EnableWorkflow = &value
		add("enable_workflow", "missing", value)
	}
	if s.EnableSubagent == nil {
		value := defaults.ConversationSettings.EnableSubagent
		s.EnableSubagent = &value
		add("enable_subagent", "missing", value)
	}
	if s.WorkflowMode == "" {
		s.WorkflowMode = defaults.ConversationSettings.WorkflowMode
		add("workflow_mode", "missing", s.WorkflowMode)
	}
	if s.ChatExecutor == "" {
		s.ChatExecutor = ChatExecutorLazyMind
		add("chat_executor", "missing", s.ChatExecutor)
	}
	if s.ChatExecutor != ChatExecutorLazyMind {
		return s, nil, forkFail("CONFIG_UNSUPPORTED")
	}
	if s.Version != 1 {
		s.Filters = map[string]any{}
		add("resource_bindings", "missing", s.Filters)
		add("reasoning", "missing", true)
		s.Reasoning = true
		add("use_memory", "missing", false)
		s.UseMemory = false
		add("mode", "missing", "auto")
		s.Mode = "auto"
	}
	availableIDs := []string{}
	resourcesChanged := false
	for _, id := range stringSliceFromAny(s.Filters["kb_id"]) {
		if err := authorizeKnowledgeBaseID(ctx, db, userID, id); err != nil {
			if !errors.Is(err, errKnowledgeBaseNotReadable) {
				return s, nil, err
			}
			resourcesChanged = true
		} else {
			availableIDs = append(availableIDs, id)
		}
	}
	if resourcesChanged {
		s.Filters["kb_id"] = availableIDs
		add("resource_bindings", "unavailable", s.Filters)
	}
	return s, issues, nil
}

func buildForkPreview(ctx context.Context, db *gorm.DB, caller doc.DatasetCatalogCaller, c orm.Conversation, histories []orm.ChatHistory) (*forkPreview, error) {
	target := histories[len(histories)-1]
	artifacts, err := loadForkArtifacts(ctx, db, c, histories)
	if err != nil {
		return nil, err
	}
	prefix, err := forkSnapshotRevision(histories, artifacts)
	if err != nil {
		return nil, err
	}
	revision, err := forkPrefixRevision([]orm.ChatHistory{target})
	if err != nil {
		return nil, err
	}
	config, issues, err := prepareForkConfig(ctx, db, caller.UserID, target)
	if err != nil {
		return nil, err
	}
	attachments, err := inspectForkAttachments(ctx, db, caller, histories)
	if err != nil {
		return nil, err
	}
	if len(attachments)+len(artifacts) > 200 {
		return nil, forkFail("FORK_TOO_LARGE")
	}
	for _, a := range artifacts {
		status := "available"
		if a.Unavailable {
			status = "unavailable"
		}
		attachments = append(attachments, forkAttachment{Name: a.Filename, Status: status})
	}
	excerpt := []rune(stripThinkTags(stripToolTags(target.Result)))
	if len(excerpt) > 300 {
		excerpt = append(excerpt[:300], '…')
	}
	status := "complete"
	if len(issues) > 0 {
		status = "confirmation_required"
	}
	return &forkPreview{SourceHistoryID: target.ID, SourceSeq: target.Seq, SourceTitle: c.DisplayName,
		SourceHistoryRevision: revision, PrefixRevision: prefix, Excerpt: string(excerpt),
		InheritedTurnCount: len(histories), InheritedMessageCount: len(histories) * 2,
		ConfigSnapshotSummary: config, ConfigStatus: status, ConfigIssues: issues,
		AttachmentsSummary: attachments, Warnings: forkAttachmentWarnings(attachments), CanFork: true}, nil
}

func loadForkOrigin(ctx context.Context, db *gorm.DB, c orm.Conversation) (*forkOriginView, error) {
	var origin orm.ConversationForkOrigin
	if err := db.WithContext(ctx).Where("conversation_id = ?", c.ID).Take(&origin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	view := &forkOriginView{ConversationForkOrigin: origin, SourceStatus: "unavailable"}
	var source orm.Conversation
	err := db.WithContext(ctx).Where("id = ? AND create_user_id = ?", origin.SourceConversationID, c.CreateUserID).Take(&source).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		view.SourceTitleSnapshot = ""
		return view, nil
	}
	if err != nil {
		return nil, err
	}
	if source.DeletedAt != nil {
		view.SourceStatus = "deleted"
		return view, nil
	}
	view.SourceTitleSnapshot = source.DisplayName
	var target orm.ChatHistory
	err = db.Where("id = ? AND conversation_id = ?", origin.SourceHistoryID, source.ID).Take(&target).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		view.SourceStatus = "node_deleted"
		return view, nil
	}
	if err != nil {
		return nil, err
	}
	view.SourceStatus = "available"
	view.CanLocate = true
	revision, err := forkPrefixRevision([]orm.ChatHistory{target})
	if err != nil {
		return nil, err
	}
	if revision != origin.SourceHistoryRevision {
		view.SourceStatus = "changed"
	}
	return view, nil
}

func forkResultFor(ctx context.Context, db *gorm.DB, userID, id string, replayed bool) (*forkResult, error) {
	var c orm.Conversation
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", id, userID).Take(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, forkFail("FORK_RESULT_UNAVAILABLE")
		}
		return nil, err
	}
	origin, err := loadForkOrigin(ctx, db, c)
	if err != nil {
		return nil, err
	}
	return &forkResult{Conversation: map[string]any{"conversation_id": c.ID, "display_name": c.DisplayName, "name": "conversations/" + c.ID}, ForkOrigin: origin, InheritedTurnCount: int(c.ChatTimes), Warnings: []string{}, Replayed: replayed}, nil
}

func replayForkRequest(ctx context.Context, db *gorm.DB, userID, key, hash string) (*forkResult, error) {
	var receipt orm.ConversationForkRequest
	if err := db.WithContext(ctx).Where("actor_user_id = ? AND idempotency_key = ?", userID, key).Take(&receipt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if receipt.RequestHash != hash {
		return nil, forkFail("IDEMPOTENCY_CONFLICT")
	}
	return forkResultFor(ctx, db, userID, receipt.ConversationID, true)
}

func createConversationFork(ctx context.Context, db *gorm.DB, caller doc.DatasetCatalogCaller, sourceID, key string, request forkCreateRequest) (*forkResult, error) {
	result, err := createConversationForkAttempt(ctx, db, caller, sourceID, key, request)
	if err == nil {
		return result, nil
	}
	// A successful competitor may commit between our initial receipt lookup and
	// source validation (including a subsequent source deletion).
	hash, hashErr := forkDigest(struct {
		Source  string
		Request forkCreateRequest
	}{sourceID, request})
	if hashErr == nil {
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if replay, replayErr := replayForkRequest(checkCtx, db, caller.UserID, key, hash); replay != nil {
			return replay, replayErr
		}
	}
	return nil, err
}

func createConversationForkAttempt(ctx context.Context, db *gorm.DB, caller doc.DatasetCatalogCaller, sourceID, key string, request forkCreateRequest) (*forkResult, error) {
	hash, err := forkDigest(struct {
		Source  string
		Request forkCreateRequest
	}{sourceID, request})
	if err != nil {
		return nil, err
	}
	if result, err := replayForkRequest(ctx, db, caller.UserID, key, hash); result != nil || err != nil {
		return result, err
	}
	c, histories, err := loadForkPrefix(ctx, db, caller.UserID, sourceID, request.SourceHistoryID)
	if err != nil {
		return nil, err
	}
	artifacts, err := loadForkArtifacts(ctx, db, c, histories)
	if err != nil {
		return nil, err
	}
	revision, err := forkSnapshotRevision(histories, artifacts)
	if err != nil {
		return nil, err
	}
	if revision != request.ExpectedPrefixRevision {
		return nil, forkFail("SOURCE_CHANGED")
	}
	id := newConversationID()
	copied, err := copyForkHistories(histories, id)
	if err != nil {
		return nil, err
	}
	// Prepare file objects before taking the database write checkpoint. They are
	// published only by the transaction below and never share a writable handle.
	cleanup := func() {
		if err := removeConversationArtifactFiles(caller.UserID, id); err != nil {
			log.Logger.Warn().Str("conversation_id", id).Msg("unpublished fork artifact cleanup failed")
		}
	}
	copiedArtifacts, err := prepareForkArtifactCopies(caller.UserID, id, histories, copied, artifacts)
	if err != nil {
		cleanup()
		return nil, err
	}
	var result *forkResult
	err = conversationCheckpoint(ctx, db, sourceID, func(tx *gorm.DB) error {
		if replay, err := replayForkRequest(ctx, tx, caller.UserID, key, hash); replay != nil || err != nil {
			result = replay
			return err
		}
		c, histories, err := loadForkPrefix(ctx, tx, caller.UserID, sourceID, request.SourceHistoryID)
		if err != nil {
			return err
		}
		preview, err := buildForkPreview(ctx, tx, caller, c, histories)
		if err != nil {
			return err
		}
		if preview.PrefixRevision != request.ExpectedPrefixRevision {
			return forkFail("SOURCE_CHANGED")
		}
		confirmed := map[string]bool{}
		for _, field := range request.ConfirmedFields {
			confirmed[field] = true
		}
		config := preview.ConfigSnapshotSummary
		modelMissing := false
		for _, issue := range preview.ConfigIssues {
			if issue.Field == "model" {
				modelMissing = true
				if request.ReplacementModel == nil {
					return forkFail("MODEL_UNAVAILABLE")
				}
			} else if !confirmed[issue.Field] {
				return forkFail("CONFIG_CONFIRMATION_REQUIRED")
			} else {
				actual, ok := request.ConfirmedValues[issue.Field]
				want, _ := forkDigest(issue.SuggestedValue)
				got, _ := forkDigest(actual)
				if !ok || want != got {
					return forkFail("CONFIG_CONFIRMATION_REQUIRED")
				}
			}
		}
		for field := range confirmed {
			known := false
			for _, issue := range preview.ConfigIssues {
				if issue.Field == field && field != "model" {
					known = true
				}
			}
			if !known {
				return forkFail("CONFIG_UNSUPPORTED")
			}
		}
		for field := range request.ConfirmedValues {
			if !confirmed[field] {
				return forkFail("CONFIG_UNSUPPORTED")
			}
		}
		if request.ReplacementModel != nil {
			if !modelMissing || !validChatModelSelection(request.ReplacementModel.Mode, request.ReplacementModel.ModelID) {
				return forkFail("CONFIG_UNSUPPORTED")
			}
			models, err := loadAvailableChatModels(ctx, tx, caller.UserID)
			if err != nil {
				return err
			}
			model := findAvailableChatModel(models, request.ReplacementModel.ModelID)
			if len(models) == 0 || (request.ReplacementModel.Mode == "fixed" && model == nil) {
				return forkFail("MODEL_UNAVAILABLE")
			}
			if request.ReplacementModel.Mode == "auto" {
				config.Model = &chatModelRoute{Mode: "auto"}
			} else {
				config.Model = fixedChatModelRoute(model)
			}
		}
		now := time.Now().UTC()
		title := []rune(c.DisplayName)
		if len(title) > maxConversationDisplayNameLength-7 {
			title = title[:maxConversationDisplayNameLength-7]
		}
		branch := orm.Conversation{ID: id, DisplayName: string(title) + " · Fork", ChannelID: c.ChannelID,
			ChatExecutor: ChatExecutorLazyMind, ThinkingDepth: config.ThinkingDepth, EnableWorkflow: config.EnableWorkflow,
			EnableSubagent: config.EnableSubagent, WorkflowMode: &config.WorkflowMode, ChatTimes: int32(len(histories)),
			BaseModel: orm.BaseModel{CreateUserID: caller.UserID, CreateUserName: c.CreateUserName, CreatedAt: now, UpdatedAt: now}}
		mode := config.Model.Mode
		branch.ChatModelMode = &mode
		branch.ChatModelVersion = 1
		if config.Model.ModelID != "" {
			modelID := config.Model.ModelID
			branch.ChatModelID = &modelID
		}
		branch.ChatModelSnapshot, _ = json.Marshal(chatModelSnapshot{ModelID: config.Model.ModelID, ProviderID: config.Model.ProviderID, ProviderName: config.Model.ProviderName, ModelName: config.Model.ModelName, Source: config.Model.Source})
		selectors := []map[string]string{}
		for _, datasetID := range stringSliceFromAny(config.Filters["kb_id"]) {
			selectors = append(selectors, map[string]string{"id": datasetID})
		}
		branch.SearchConfig, _ = json.Marshal(map[string]any{"dataset_list": selectors, "creators": config.Filters["creator"], "tags": config.Filters["tags"]})
		branch.Ext = marshalChatHistoryExt(map[string]any{"fork_config": config, "fork_config_confirmations": preview.ConfigIssues})
		if err := applyForkAttachments(copied, preview.AttachmentsSummary); err != nil {
			return err
		}
		if err := tx.Create(&branch).Error; err != nil {
			return err
		}
		if err := tx.CreateInBatches(copied, 50).Error; err != nil {
			return err
		}
		if len(copiedArtifacts) > 0 {
			if err := tx.CreateInBatches(copiedArtifacts, 50).Error; err != nil {
				return err
			}
		}
		origin := orm.ConversationForkOrigin{ConversationID: id, SourceConversationID: sourceID, SourceHistoryID: request.SourceHistoryID, SourceSeq: preview.SourceSeq,
			SourceHistoryRevision: preview.SourceHistoryRevision, SourcePrefixRevision: preview.PrefixRevision, SourceTitleSnapshot: c.DisplayName, ForkedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		if err := tx.Create(&orm.ConversationForkRequest{ActorUserID: caller.UserID, IdempotencyKey: key, RequestHash: hash, ConversationID: id, CreatedAt: now}).Error; err != nil {
			return err
		}
		result, err = forkResultFor(ctx, tx, caller.UserID, id, false)
		if result != nil {
			result.Warnings = preview.Warnings
		}
		return err
	})
	if err != nil {
		// A competing key can have committed on a different source checkpoint.
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if replay, replayErr := replayForkRequest(checkCtx, db, caller.UserID, key, hash); replay != nil || replayErr != nil {
			if replay != nil && replay.Conversation["conversation_id"] != id {
				cleanup()
			}
			var problem *forkError
			if errors.As(replayErr, &problem) {
				cleanup()
			}
			return replay, replayErr
		}
		cleanup()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, forkFail("SOURCE_UNAVAILABLE")
		}
		return nil, err
	}
	if result != nil && result.Conversation["conversation_id"] != id {
		cleanup()
	}
	return result, nil
}

func forkReplyError(w http.ResponseWriter, err error) {
	code := "FORK_FAILED"
	status := http.StatusInternalServerError
	var problem *forkError
	if errors.As(err, &problem) {
		code = problem.Code
		status = http.StatusConflict
		if code == "SOURCE_UNAVAILABLE" {
			status = http.StatusNotFound
		}
		if code == "INVALID_REQUEST" {
			status = http.StatusBadRequest
		}
		if code == "FORK_TOO_LARGE" {
			status = http.StatusRequestEntityTooLarge
		}
	}
	requestID := newID("req_")
	log.Logger.Warn().Str("request_id", requestID).Str("code", code).Msg("conversation fork request failed")
	message := "Unable to complete this fork. Review the source and retry."
	if code == "SOURCE_NOT_COMPLETED" {
		message = "Choose a successfully completed reply to fork."
	}
	writeConversationJSON(w, status, map[string]any{"code": code, "message": message, "request_id": requestID})
}

func decodeForkRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		forkReplyError(w, forkFail("INVALID_REQUEST"))
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		forkReplyError(w, forkFail("INVALID_REQUEST"))
		return false
	}
	return true
}

func PreviewConversationFork(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	var request forkPreviewRequest
	if !decodeForkRequest(w, r, &request) {
		return
	}
	if request.SourceHistoryID == "" || len(request.SourceHistoryID) > 64 {
		forkReplyError(w, forkFail("INVALID_REQUEST"))
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	sourceID := mux.Vars(r)["conversation_id"]
	var preview *forkPreview
	err := conversationCheckpoint(r.Context(), store.DB(), sourceID, func(tx *gorm.DB) error {
		c, histories, err := loadForkPrefix(r.Context(), tx, userID, sourceID, request.SourceHistoryID)
		if err != nil {
			return err
		}
		preview, err = buildForkPreview(r.Context(), tx, sidechatDatasetCaller(r, userID), c, histories)
		return err
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = forkFail("SOURCE_UNAVAILABLE")
	}
	if err != nil {
		forkReplyError(w, err)
		return
	}
	writeConversationJSON(w, http.StatusOK, preview)
}

func CreateConversationFork(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	var request forkCreateRequest
	if !decodeForkRequest(w, r, &request) {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 || request.SourceHistoryID == "" || len(request.SourceHistoryID) > 64 || len(request.ExpectedPrefixRevision) > 80 {
		forkReplyError(w, forkFail("INVALID_REQUEST"))
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	result, err := createConversationFork(r.Context(), store.DB(), sidechatDatasetCaller(r, userID), mux.Vars(r)["conversation_id"], key, request)
	if err != nil {
		forkReplyError(w, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	writeConversationJSON(w, status, result)
	log.Logger.Info().Int("inherited_turns", result.InheritedTurnCount).Bool("replayed", result.Replayed).Dur("elapsed", time.Since(started)).Msg("conversation fork created")
}
