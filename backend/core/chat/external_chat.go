package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const (
	ChatExecutorLazyMind  = "lazymind"
	ChatExecutorCodex     = "codex"
	ChatExecutorCursor    = "cursor"
	ChatExecutorWorkBuddy = "workbuddy"
	externalHostTTL       = 15 * time.Second
	externalRunLeaseTTL   = 2 * externalHostTTL
)

type chatExecutorDefinition struct {
	ID          string
	DisplayName string
	Kind        string
}

var chatExecutorDefinitions = []chatExecutorDefinition{
	{ID: ChatExecutorLazyMind, DisplayName: "LazyMind", Kind: "internal"},
	{ID: ChatExecutorCodex, DisplayName: "Codex CLI", Kind: "external"},
	{ID: ChatExecutorCursor, DisplayName: "Cursor Agent CLI", Kind: "external"},
	{ID: ChatExecutorWorkBuddy, DisplayName: "WorkBuddy", Kind: "external"},
}

type externalChatJob struct {
	RunID            string `json:"run_id"`
	ConversationID   string `json:"conversation_id"`
	HistoryID        string `json:"history_id"`
	Provider         string `json:"provider"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	Action           string `json:"action"`
	Prompt           string `json:"prompt"`
	Query            string `json:"query"`
	LeaseToken       string `json:"lease_token"`
	HostID           string `json:"host_id"`
}

type externalChatEvent struct {
	EventID          string `json:"event_id"`
	Type             string `json:"type"`
	Text             string `json:"text,omitempty"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	Error            string `json:"error,omitempty"`
}

type chatExecutorStatus struct {
	ID                string `json:"id"`
	DisplayName       string `json:"display_name"`
	Kind              string `json:"kind"`
	Installed         bool   `json:"installed"`
	HostOnline        bool   `json:"host_online"`
	Available         bool   `json:"available"`
	Connected         bool   `json:"connected"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// externalExecutionProjection is a read model assembled from existing Chat,
// External Run, MCP invocation and Workflow tables. It owns no state and never
// exposes prompts, conversation text or provider thread identifiers.
type externalExecutionProjection struct {
	RunID                 string                       `json:"run_id"`
	HistoryID             string                       `json:"history_id"`
	Provider              string                       `json:"provider"`
	Status                string                       `json:"status"`
	HostID                string                       `json:"host_id,omitempty"`
	HostOnline            bool                         `json:"host_online"`
	ClaimCount            int                          `json:"claim_count"`
	RecoveryCount         int                          `json:"recovery_count"`
	EventCount            int64                        `json:"event_count"`
	Invocation            externalInvocationProjection `json:"invocation"`
	Workflows             []externalWorkflowProjection `json:"workflows"`
	ArtifactCount         int64                        `json:"artifact_count"`
	ArtifactRevisionCount int64                        `json:"artifact_revision_count"`
	ErrorMessage          string                       `json:"error_message,omitempty"`
	ClaimedAt             *time.Time                   `json:"claimed_at,omitempty"`
	LastHeartbeatAt       *time.Time                   `json:"last_heartbeat_at,omitempty"`
	CompletedAt           *time.Time                   `json:"completed_at,omitempty"`
	CreatedAt             time.Time                    `json:"created_at"`
	UpdatedAt             time.Time                    `json:"updated_at"`
}

type externalInvocationProjection struct {
	Total       int      `json:"total"`
	Running     int      `json:"running"`
	Succeeded   int      `json:"succeeded"`
	Failed      int      `json:"failed"`
	Interrupted int      `json:"interrupted"`
	Tools       []string `json:"tools"`
}

type externalWorkflowProjection struct {
	SessionID             string `json:"session_id"`
	WorkflowID            string `json:"workflow_id"`
	Status                string `json:"status"`
	CurrentStepID         string `json:"current_step_id,omitempty"`
	StateVersion          int64  `json:"state_version"`
	ArtifactCount         int64  `json:"artifact_count"`
	ArtifactRevisionCount int64  `json:"artifact_revision_count"`
}

func basicExternalExecutionProjection(run orm.ExternalChatRun, now time.Time) externalExecutionProjection {
	recoveryCount := run.ClaimCount - 1
	if recoveryCount < 0 {
		recoveryCount = 0
	}
	hostOnline := run.Status == "running" && run.LastHeartbeatAt != nil &&
		now.Sub(run.LastHeartbeatAt.UTC()) <= externalHostTTL
	errorMessage := []rune(run.ErrorMessage)
	if len(errorMessage) > 500 {
		errorMessage = errorMessage[:500]
	}
	return externalExecutionProjection{
		RunID: run.ID, HistoryID: run.HistoryID, Provider: run.Provider, Status: run.Status,
		HostID: run.HostID, HostOnline: hostOnline, ClaimCount: run.ClaimCount,
		RecoveryCount: recoveryCount, EventCount: run.NextEventSequence,
		Invocation: externalInvocationProjection{Tools: []string{}},
		Workflows:  []externalWorkflowProjection{}, ErrorMessage: string(errorMessage),
		ClaimedAt: run.ClaimedAt, LastHeartbeatAt: run.LastHeartbeatAt,
		CompletedAt: run.CompletedAt, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func normalizeChatExecutor(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ChatExecutorLazyMind, true
	}
	for _, definition := range chatExecutorDefinitions {
		if definition.ID == value {
			return value, true
		}
	}
	return "", false
}

func isExternalChatProvider(provider string) bool {
	for _, definition := range chatExecutorDefinitions {
		if definition.ID == provider {
			return definition.Kind == "external"
		}
	}
	return false
}

func chatExecutorValidationMessage() string {
	values := make([]string, 0, len(chatExecutorDefinitions))
	for _, definition := range chatExecutorDefinitions {
		values = append(values, fmt.Sprintf("'%s'", definition.ID))
	}
	return "chat_executor must be " + strings.Join(values, ", ")
}

func externalChatRequestKey(owner, requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(owner + "\x00" + requestID))
	return fmt.Sprintf("req_%x", digest[:])
}

func externalChatIdentity(requestKey string) (runID, historyID string) {
	if !strings.HasPrefix(requestKey, "req_") || len(requestKey) < 36 {
		return "", ""
	}
	value := strings.TrimPrefix(requestKey, "req_")[:32]
	return "ecr_" + value, "h_" + value
}

func externalChatUnavailableError(ctx context.Context, owner, provider string) error {
	status, err := newExternalChatApplication(store.DB()).hostStatus(ctx, owner, provider)
	if err != nil {
		return fmt.Errorf("query %s Agent Host: %w", provider, err)
	}
	if status.Available {
		return nil
	}
	reason := status.UnavailableReason
	if reason == "" {
		reason = "External Agent is unavailable"
	}
	return fmt.Errorf("%s is unavailable: %s", provider, reason)
}

func externalAgentPrompt(query string) string {
	return strings.TrimSpace(query)
}

func streamChatOutput(
	ctx context.Context,
	db *gorm.DB,
	baseURL string,
	reqBody map[string]any,
	conversationID, historyID, query string,
	sequence int,
	historyExt json.RawMessage,
	isRegeneration bool,
) (<-chan UpstreamStreamChunk, string, error) {
	executor, _ := reqBody["_chat_executor"].(string)
	normalized, valid := normalizeChatExecutor(executor)
	if !valid {
		return nil, "", errors.New("unsupported chat executor")
	}
	if isExternalChatProvider(normalized) {
		return streamExternalChat(ctx, db, reqBody, conversationID, historyID, query, normalized, sequence, historyExt, isRegeneration)
	}
	return StreamChatUpstream(ctx, baseURL, reqBody)
}

func streamExternalChat(
	ctx context.Context,
	db *gorm.DB,
	reqBody map[string]any,
	conversationID, historyID, query, provider string,
	sequence int,
	historyExt json.RawMessage,
	isRegeneration bool,
) (<-chan UpstreamStreamChunk, string, error) {
	owner := userIDFromChatRequestBody(reqBody)
	if owner == "" {
		return nil, "", errors.New("external chat requires an authenticated user")
	}
	requestKey, _ := reqBody["_external_request_key"].(string)
	existingRunID, _ := reqBody["_external_run_id"].(string)
	if existingRunID == "" {
		if runID, _ := externalChatIdentity(requestKey); runID != "" {
			existingRunID = runID
		}
	}
	if existingRunID != "" {
		var existing orm.ExternalChatRun
		if err := db.WithContext(ctx).
			Where("id = ? AND actor_user_id = ? AND provider = ?", existingRunID, owner, provider).
			Take(&existing).Error; err == nil {
			return streamExistingExternalChat(ctx, db, owner, existing.ID, existing.ID), "external:" + provider, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", err
		}
	}
	if err := externalChatUnavailableError(ctx, owner, provider); err != nil {
		return nil, "", err
	}

	threadID, hostID, resume, err := externalConversationThread(
		ctx, db, owner, conversationID, provider,
	)
	if err != nil {
		return nil, "", err
	}
	action := "start"
	if resume {
		action = "resume"
	}
	if isRegeneration {
		// Regeneration replaces a LazyMind history row, but it must not fork the
		// provider-native conversation. Adapters resume the authoritative binding
		// when threadID is present and start only when no mapping exists yet.
		action = "regenerate"
	}
	runID, _ := externalChatIdentity(requestKey)
	if runID == "" {
		runID, _ = reqBody["run_id"].(string)
	}
	if runID == "" {
		runID = newID("ecr_")
	}
	if requestKey == "" {
		requestKey = runID
	}
	record := orm.ExternalChatRun{
		ID: runID, RequestID: requestKey, ConversationID: conversationID, HistoryID: historyID,
		Provider: provider, HostID: hostID, ProviderThreadID: threadID, ActorUserID: owner, Action: action,
		Prompt: externalAgentPrompt(query), Query: query,
		Sequence: sequence, HistoryExt: historyExt,
	}
	app := newExternalChatApplication(db)
	if err := app.createRun(ctx, &record); err != nil {
		var existing orm.ExternalChatRun
		if requestKey == runID || db.WithContext(ctx).
			Where("actor_user_id = ? AND provider = ? AND request_id = ?", owner, provider, requestKey).
			Take(&existing).Error != nil {
			return nil, "", fmt.Errorf("create external chat run: %w", err)
		}
		if existing.ConversationID != conversationID || existing.HistoryID != historyID || existing.Query != query {
			return nil, "", errors.New("external chat idempotency key conflicts with another request")
		}
		runID = existing.ID
	}
	return streamExistingExternalChat(ctx, db, owner, runID, runID), "external:" + provider, nil
}

func externalConversationThread(
	ctx context.Context,
	db *gorm.DB,
	owner, conversationID, provider string,
) (string, string, bool, error) {
	var binding orm.ExternalAgentBinding
	err := db.WithContext(ctx).
		Where("conversation_id = ? AND created_by_user_id = ? AND provider = ?", conversationID, owner, provider).
		Take(&binding).Error
	if err == nil {
		threadID := strings.TrimSpace(binding.ProviderThreadID)
		return threadID, strings.TrimSpace(binding.HostID), threadID != "", nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", false, err
	}

	// Legacy runs created before bindings became authoritative remain a safe
	// fallback. The next thread_started event will materialize the binding.
	var previous orm.ExternalChatRun
	err = db.WithContext(ctx).
		Where("conversation_id = ? AND actor_user_id = ? AND provider = ? AND provider_thread_id <> '' AND status = ?", conversationID, owner, provider, "completed").
		Order("created_at DESC").Take(&previous).Error
	if err == nil {
		threadID := strings.TrimSpace(previous.ProviderThreadID)
		return threadID, strings.TrimSpace(previous.HostID), threadID != "", nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", false, nil
	}
	return "", "", false, err
}

func streamExistingExternalChat(
	ctx context.Context,
	db *gorm.DB,
	owner, externalRunID, runtimeRunID string,
) <-chan UpstreamStreamChunk {
	out := make(chan UpstreamStreamChunk, 16)
	app := newExternalChatApplication(db)
	go func() {
		defer close(out)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		var cursor int64
		hasSemanticOutput := false
		lastHeartbeat := time.Time{}
		for {
			events, current, err := app.eventsAfter(ctx, owner, externalRunID, cursor)
			if err != nil {
				select {
				case out <- UpstreamStreamChunk{Err: fmt.Errorf("read external chat events: %w", err)}:
				case <-ctx.Done():
				}
				return
			}
			for _, event := range events {
				cursor = event.Sequence
				projection := basicExternalExecutionProjection(current, time.Now().UTC())
				switch event.Type {
				case "message":
					if event.Text != "" {
						hasSemanticOutput = true
						select {
						case out <- UpstreamStreamChunk{Text: event.Text, ExternalEventSequence: event.Sequence, Execution: &projection}:
						case <-ctx.Done():
							return
						}
					}
				case "completed":
					projection = app.executionProjection(ctx, owner, current.HistoryID, projection)
					select {
					case out <- UpstreamStreamChunk{RuntimeEvent: completedRunEvent(runtimeRunID, hasSemanticOutput), ExternalEventSequence: event.Sequence, Execution: &projection}:
					case <-ctx.Done():
					}
					return
				case "stopped":
					projection = app.executionProjection(ctx, owner, current.HistoryID, projection)
					select {
					case out <- UpstreamStreamChunk{RuntimeEvent: cancelledRunEvent(runtimeRunID, hasSemanticOutput), ExternalEventSequence: event.Sequence, Execution: &projection}:
					case <-ctx.Done():
					}
					return
				case "failed":
					projection = app.executionProjection(ctx, owner, current.HistoryID, projection)
					if !hasSemanticOutput {
						hasSemanticOutput = true
						select {
						case out <- UpstreamStreamChunk{Text: externalAgentFailureText(event.ErrorMessage), ExternalEventSequence: event.Sequence, Execution: &projection}:
						case <-ctx.Done():
							return
						}
					}
					select {
					case out <- UpstreamStreamChunk{RuntimeEvent: failedRunEvent(runtimeRunID, "external_agent_failed", hasSemanticOutput), ExternalEventSequence: event.Sequence, Execution: &projection}:
					case <-ctx.Done():
					}
					return
				}
			}
			if externalRunTerminal(current.Status) {
				projection := app.executionProjection(
					ctx, owner, current.HistoryID,
					basicExternalExecutionProjection(current, time.Now().UTC()),
				)
				if current.Status == "failed" && !hasSemanticOutput {
					hasSemanticOutput = true
					select {
					case out <- UpstreamStreamChunk{Text: externalAgentFailureText(current.ErrorMessage), Execution: &projection}:
					case <-ctx.Done():
						return
					}
				}
				var event *ChatRuntimeEvent
				switch current.Status {
				case "completed":
					event = completedRunEvent(runtimeRunID, hasSemanticOutput)
				case "stopped":
					event = cancelledRunEvent(runtimeRunID, hasSemanticOutput)
				default:
					event = failedRunEvent(runtimeRunID, "external_agent_failed", hasSemanticOutput)
				}
				select {
				case out <- UpstreamStreamChunk{RuntimeEvent: event, ExternalEventSequence: current.NextEventSequence, Execution: &projection}:
				case <-ctx.Done():
				}
				return
			}
			now := time.Now()
			if lastHeartbeat.IsZero() || now.Sub(lastHeartbeat) >= time.Second {
				lastHeartbeat = now
				projection := basicExternalExecutionProjection(current, now.UTC())
				select {
				case out <- UpstreamStreamChunk{Heartbeat: true, Execution: &projection}:
				case <-ctx.Done():
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return out
}

func (a *externalChatApplication) executionProjection(
	ctx context.Context,
	owner, historyID string,
	fallback externalExecutionProjection,
) externalExecutionProjection {
	projections, err := a.executionProjections(ctx, owner, []string{historyID})
	if err != nil {
		return fallback
	}
	if projection, ok := projections[historyID]; ok {
		return projection
	}
	return fallback
}

func externalAgentFailureText(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "外部助理执行失败，请检查助理连接状态后重试。"
	}
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500])
	}
	return "外部助理执行失败：" + message
}
