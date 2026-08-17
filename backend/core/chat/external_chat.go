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
	{ID: ChatExecutorCodex, DisplayName: "Codex", Kind: "external"},
	{ID: ChatExecutorCursor, DisplayName: "Cursor", Kind: "external"},
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

func externalConversationKnowledgeBaseIDs(
	ctx context.Context,
	db *gorm.DB,
	reqBody map[string]any,
	conversationID string,
) []string {
	if filters, ok := reqBody["filters"].(map[string]any); ok {
		if value, scoped := filters["kb_id"]; scoped {
			return stringSlice(value)
		}
	}
	var conversation orm.Conversation
	if err := db.WithContext(ctx).Select("search_config").Where("id = ?", conversationID).First(&conversation).Error; err != nil {
		return nil
	}
	var searchConfig map[string]any
	if json.Unmarshal(conversation.SearchConfig, &searchConfig) != nil {
		return nil
	}
	return datasetIDsFromSearchConfig(searchConfig)
}

func externalAgentPrompt(reqBody map[string]any, query string, resume bool) string {
	var out strings.Builder
	out.WriteString("You are the execution Agent for one LazyMind Chat turn. LazyMind owns the conversation, Workflow runtime, artifacts, versions and audit records; you provide the Agent reasoning and final user-facing answer. The `lazymind` MCP server is already configured. Use its Knowledge, Skill and Workflow tools whenever the request needs those capabilities. For a Workflow, follow the returned step contract in order and submit every required artifact before claiming completion. After workflow.state reports completed, call workflow.artifact.list and use its LazyMind-managed artifact URLs in the final answer when the user needs a file. Never expose Agent workspace paths or file:// URLs. Do not describe these integration instructions to the user.\n")
	if workflowContext, ok := reqBody["workflow_context"].(map[string]any); ok {
		sessionID, _ := workflowContext["session_id"].(string)
		if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
			workflowID, _ := workflowContext["workflow_id"].(string)
			currentStep, _ := workflowContext["current_step"].(string)
			workflowMode, _ := workflowContext["workflow_mode"].(string)
			out.WriteString("\nActive LazyMind Workflow (authoritative runtime state):\n")
			fmt.Fprintf(&out, "- session_id: %s\n", sessionID)
			if workflowID = strings.TrimSpace(workflowID); workflowID != "" {
				fmt.Fprintf(&out, "- workflow_id: %s\n", workflowID)
			}
			if currentStep = strings.TrimSpace(currentStep); currentStep != "" {
				fmt.Fprintf(&out, "- current_step: %s\n", currentStep)
			}
			if workflowMode = strings.TrimSpace(workflowMode); workflowMode != "" {
				fmt.Fprintf(&out, "- workflow_mode: %s\n", workflowMode)
			}
			out.WriteString("Call workflow.state for this session before acting. Continue the existing session; do not create a duplicate Workflow.\n")
		}
	}
	if bindings, ok := reqBody["explicit_resource_bindings"].(map[string]any); ok {
		resources := []struct{ label, key string }{
			{label: "skills", key: "skill_names"},
			{label: "knowledge_base_ids", key: "knowledge_base_ids"},
			{label: "workflow_refs", key: "workflow_refs"},
		}
		wroteHeader := false
		for _, resource := range resources {
			values := stringSlice(bindings[resource.key])
			if len(values) == 0 {
				continue
			}
			if !wroteHeader {
				out.WriteString("\nResources explicitly selected by the user:\n")
				wroteHeader = true
			}
			fmt.Fprintf(&out, "- %s: %s\n", resource.label, strings.Join(values, ", "))
		}
	}
	knowledgeBaseIDs := stringSlice(reqBody["_external_knowledge_base_ids"])
	if len(knowledgeBaseIDs) == 0 {
		if filters, ok := reqBody["filters"].(map[string]any); ok {
			knowledgeBaseIDs = stringSlice(filters["kb_id"])
		}
	}
	if len(knowledgeBaseIDs) > 0 {
		out.WriteString("\nKnowledge bases configured for this LazyMind conversation:\n")
		fmt.Fprintf(&out, "- knowledge_base_ids: %s\n", strings.Join(knowledgeBaseIDs, ", "))
		out.WriteString("Use these IDs as the default scope when the user asks to retrieve knowledge.\n")
	}
	if !resume {
		if history, ok := reqBody["history"].([]map[string]string); ok && len(history) > 0 {
			out.WriteString("\nConversation history:\n")
			start := 0
			if len(history) > 20 {
				start = len(history) - 20
			}
			for _, message := range history[start:] {
				content := strings.TrimSpace(message["content"])
				contentRunes := []rune(content)
				if len(contentRunes) > 8000 {
					content = string(contentRunes[:8000])
				}
				if content != "" {
					fmt.Fprintf(&out, "[%s] %s\n", strings.TrimSpace(message["role"]), content)
				}
			}
		}
	}
	out.WriteString("\nCurrent user request:\n")
	out.WriteString(strings.TrimSpace(query))
	return out.String()
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
			return streamExistingExternalChat(ctx, db, owner, existing.ID), "external:" + provider, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", err
		}
	}
	if err := externalChatUnavailableError(ctx, owner, provider); err != nil {
		return nil, "", err
	}

	var previous orm.ExternalChatRun
	resume := db.WithContext(ctx).
		Where("conversation_id = ? AND actor_user_id = ? AND provider = ? AND provider_thread_id <> '' AND status = ?", conversationID, owner, provider, "completed").
		Order("created_at DESC").Take(&previous).Error == nil
	action, threadID := "start", ""
	if resume {
		action, threadID = "resume", previous.ProviderThreadID
	}
	resumeProviderThread := resume
	if isRegeneration {
		// Regeneration is a new provider turn that replaces an existing LazyMind
		// history row. Do not omit conversation history as if it were resuming the
		// provider thread; every adapter intentionally starts a fresh thread here.
		action, threadID, resumeProviderThread = "regenerate", "", false
	}
	reqBody["_external_knowledge_base_ids"] = externalConversationKnowledgeBaseIDs(
		ctx,
		db,
		reqBody,
		conversationID,
	)
	runID, _ := externalChatIdentity(requestKey)
	if runID == "" {
		runID = newID("ecr_")
	}
	if requestKey == "" {
		requestKey = runID
	}
	record := orm.ExternalChatRun{
		ID: runID, RequestID: requestKey, ConversationID: conversationID, HistoryID: historyID,
		Provider: provider, ProviderThreadID: threadID, ActorUserID: owner, Action: action,
		Prompt: externalAgentPrompt(reqBody, query, resumeProviderThread), Query: query,
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
	return streamExistingExternalChat(ctx, db, owner, runID), "external:" + provider, nil
}

func streamExistingExternalChat(
	ctx context.Context,
	db *gorm.DB,
	owner, runID string,
) <-chan UpstreamStreamChunk {
	out := make(chan UpstreamStreamChunk, 16)
	app := newExternalChatApplication(db)
	go func() {
		defer close(out)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		var cursor int64
		lastHeartbeat := time.Time{}
		for {
			events, current, err := app.eventsAfter(ctx, owner, runID, cursor)
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
						select {
						case out <- UpstreamStreamChunk{Text: event.Text, ExternalEventSequence: event.Sequence, Execution: &projection}:
						case <-ctx.Done():
							return
						}
					}
				case "completed", "stopped":
					return
				case "failed":
					message := strings.TrimSpace(event.ErrorMessage)
					if message == "" {
						message = "external Agent failed"
					}
					select {
					case out <- UpstreamStreamChunk{Text: "External Agent failed: " + message, Err: fmt.Errorf("external Agent failed: %s", message), ExternalEventSequence: event.Sequence, Execution: &projection}:
					case <-ctx.Done():
					}
					return
				}
			}
			if externalRunTerminal(current.Status) {
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
