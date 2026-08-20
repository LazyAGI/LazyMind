// Package externalcontext owns the correspondence between provider-native
// Agent threads and LazyMind conversations. It stores activity projections,
// never provider transcript content.
package externalcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
)

const ObservedAction = "observe"

var (
	ErrInvalidSource = errors.New("invalid external Agent source")
	ErrThreadOwned   = errors.New("external Agent thread belongs to another user or conversation")
)

// Source is the provider-neutral thread/turn identity extracted by an outer
// Agent adapter. Provider is never inferred from user-supplied tool arguments.
type Source struct {
	Provider     string `json:"provider"`
	ThreadID     string `json:"thread_id"`
	TurnID       string `json:"turn_id,omitempty"`
	ThreadSource string `json:"thread_source,omitempty"`
	Message      string `json:"message,omitempty"`
}

// Link is the resolved LazyMind authority for one provider-native turn.
type Link struct {
	ConversationID string `json:"conversation_id"`
	ExternalRef    string `json:"external_ref"`
	HistoryID      string `json:"history_id"`
	Provider       string `json:"provider"`
	ThreadID       string `json:"thread_id"`
	TurnID         string `json:"turn_id"`
}

type Service struct {
	db  *gorm.DB
	now func() time.Time
}

func New(db *gorm.DB) *Service {
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// ResolveInvocation binds an observed external thread, creates one lightweight
// activity history per provider turn, and returns the context inherited by MCP
// tools such as workflow.start. The caller owns the surrounding transaction.
func (s *Service) ResolveInvocation(
	ctx context.Context,
	owner, invocationID, toolName string,
	source Source,
) (Link, error) {
	owner, invocationID = strings.TrimSpace(owner), strings.TrimSpace(invocationID)
	source = normalizedSource(source, invocationID)
	if s == nil || s.db == nil || !validIdentity(owner, 255) || !validIdentity(invocationID, 80) || !validSource(source) {
		return Link{}, ErrInvalidSource
	}

	binding, err := s.resolveBinding(ctx, owner, source)
	if err != nil {
		return Link{}, err
	}
	if err := s.ensureConversation(ctx, owner, binding, source, toolName); err != nil {
		return Link{}, err
	}
	return s.resolveTurn(ctx, owner, binding, source, toolName)
}

// BindManagedThread records the same correspondence when LazyMind launched the
// provider turn and learned the native thread ID from a thread_started event.
func (s *Service) BindManagedThread(
	ctx context.Context,
	owner, provider, threadID, conversationID string,
) error {
	source := normalizedSource(Source{Provider: provider, ThreadID: threadID}, "")
	owner, conversationID = strings.TrimSpace(owner), strings.TrimSpace(conversationID)
	if s == nil || s.db == nil || !validIdentity(owner, 255) || !validIdentity(conversationID, 36) ||
		!validProvider(source.Provider) || !validIdentity(source.ThreadID, 128) {
		return ErrInvalidSource
	}
	var conversation orm.Conversation
	if err := s.db.WithContext(ctx).Where("id = ? AND create_user_id = ?", conversationID, owner).Take(&conversation).Error; err != nil {
		return err
	}

	var existing orm.ExternalAgentBinding
	err := s.db.WithContext(ctx).
		Where("provider = ? AND provider_thread_id = ?", source.Provider, source.ThreadID).
		Take(&existing).Error
	if err == nil {
		if existing.CreatedByUserID != owner || existing.ConversationID != conversationID {
			return ErrThreadOwned
		}
		return s.db.WithContext(ctx).Model(&orm.ExternalAgentBinding{}).Where("id = ?", existing.ID).
			Updates(map[string]any{"managed_by_lazymind": true, "updated_at": s.now()}).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	now := s.now()
	binding := orm.ExternalAgentBinding{
		ID:             deterministicID("binding", source.Provider+"\x00"+source.ThreadID),
		ConversationID: conversationID, Provider: source.Provider, ProviderThreadID: source.ThreadID,
		ManagedByLazyMind: true, CreatedByUserID: owner, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).
		Where("provider = ? AND provider_thread_id = ?", source.Provider, source.ThreadID).
		Take(&existing).Error; err != nil {
		return err
	}
	if existing.CreatedByUserID != owner || existing.ConversationID != conversationID {
		return ErrThreadOwned
	}
	return nil
}

// CompleteObservedTurn marks a direct MCP activity turn terminal after its last
// in-flight invocation finishes. Managed External Chat runs are untouched.
func (s *Service) CompleteObservedTurn(ctx context.Context, owner, externalRef string) error {
	owner, externalRef = strings.TrimSpace(owner), strings.TrimSpace(externalRef)
	if s == nil || s.db == nil || owner == "" || externalRef == "" {
		return nil
	}
	var run orm.ExternalChatRun
	if err := s.db.WithContext(ctx).
		Where("id = ? AND actor_user_id = ? AND action = ?", externalRef, owner, ObservedAction).
		Take(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var running int64
	if err := s.db.WithContext(ctx).Model(&orm.AgentInvocation{}).
		Where("owner_user_id = ? AND external_ref = ? AND status = ?", owner, externalRef, "running").
		Count(&running).Error; err != nil || running > 0 {
		return err
	}
	now := s.now()
	if err := s.db.WithContext(ctx).Model(&orm.ExternalChatRun{}).Where("id = ?", run.ID).
		Updates(map[string]any{"status": "completed", "completed_at": now, "updated_at": now}).Error; err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&orm.ChatHistory{}).Where("id = ?", run.HistoryID).
		Updates(map[string]any{"update_time": now}).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", run.ConversationID).
		Updates(map[string]any{"updated_at": now}).Error
}

// LinkWorkflowSession repairs legacy standalone Workflow sessions that were
// created before source context propagation existed. Existing non-empty
// conversation ownership is never overwritten.
func (s *Service) LinkWorkflowSession(ctx context.Context, owner, externalRef, sessionID string) error {
	owner, externalRef, sessionID = strings.TrimSpace(owner), strings.TrimSpace(externalRef), strings.TrimSpace(sessionID)
	if s == nil || s.db == nil || owner == "" || externalRef == "" || sessionID == "" {
		return nil
	}
	var run orm.ExternalChatRun
	if err := s.db.WithContext(ctx).
		Where("id = ? AND actor_user_id = ? AND action = ?", externalRef, owner, ObservedAction).
		Take(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return s.db.WithContext(ctx).Model(&orm.WorkflowSession{}).
		Where("id = ? AND create_user_id = ? AND (conversation_id = '' OR conversation_id IS NULL)", sessionID, owner).
		Updates(map[string]any{"conversation_id": run.ConversationID, "origin_ref": run.ID, "updated_at": s.now()}).Error
}

// SyncTurnAnswer completes the user-visible mirror after Codex finishes the
// provider-native turn. Tool calls remain in the invocation ledger only.
func (s *Service) SyncTurnAnswer(ctx context.Context, owner string, source Source, answer string) error {
	owner, answer = strings.TrimSpace(owner), strings.TrimSpace(answer)
	source = normalizedSource(source, "")
	if s == nil || s.db == nil || !validIdentity(owner, 255) || !validSource(source) || answer == "" || len([]rune(answer)) > 1<<20 {
		return ErrInvalidSource
	}
	identity := owner + "\x00" + source.Provider + "\x00" + source.ThreadID + "\x00" + source.TurnID
	runID := deterministicID("mcp-run", identity)
	historyID := deterministicID("mcp-history", identity)
	now := s.now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var binding orm.ExternalAgentBinding
		if err := tx.Where("provider = ? AND provider_thread_id = ? AND created_by_user_id = ?", source.Provider, source.ThreadID, owner).
			Take(&binding).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.ChatHistory{}).Where("id = ? AND conversation_id = ?", historyID, binding.ConversationID).
			Updates(map[string]any{"result": answer, "update_time": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.ExternalChatRun{}).Where("id = ? AND actor_user_id = ?", runID, owner).
			Updates(map[string]any{"status": "completed", "completed_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&orm.Conversation{}).Where("id = ?", binding.ConversationID).Update("updated_at", now).Error
	})
}

func (s *Service) resolveBinding(ctx context.Context, owner string, source Source) (orm.ExternalAgentBinding, error) {
	var binding orm.ExternalAgentBinding
	err := s.db.WithContext(ctx).
		Where("provider = ? AND provider_thread_id = ?", source.Provider, source.ThreadID).
		Take(&binding).Error
	if err == nil {
		if binding.CreatedByUserID != owner {
			return orm.ExternalAgentBinding{}, ErrThreadOwned
		}
		return binding, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.ExternalAgentBinding{}, err
	}
	now := s.now()
	binding = orm.ExternalAgentBinding{
		ID:             deterministicID("binding", source.Provider+"\x00"+source.ThreadID),
		ConversationID: deterministicID("conversation", owner+"\x00"+source.Provider+"\x00"+source.ThreadID),
		Provider:       source.Provider, ProviderThreadID: source.ThreadID, CreatedByUserID: owner,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
		return orm.ExternalAgentBinding{}, err
	}
	if err := s.db.WithContext(ctx).
		Where("provider = ? AND provider_thread_id = ?", source.Provider, source.ThreadID).
		Take(&binding).Error; err != nil {
		return orm.ExternalAgentBinding{}, err
	}
	if binding.CreatedByUserID != owner {
		return orm.ExternalAgentBinding{}, ErrThreadOwned
	}
	return binding, nil
}

func (s *Service) ensureConversation(
	ctx context.Context,
	owner string,
	binding orm.ExternalAgentBinding,
	source Source,
	toolName string,
) error {
	now := s.now()
	label := sourceLabel(source, toolName)
	if label == "" {
		label = activityLabel(source.Provider, source.ThreadID)
	}
	var conversation orm.Conversation
	err := s.db.WithContext(ctx).Where("id = ?", binding.ConversationID).Take(&conversation).Error
	if err == nil {
		if conversation.CreateUserID != owner {
			return ErrThreadOwned
		}
		updates := map[string]any{"deleted_at": nil, "chat_executor": chatExecutor(source.Provider), "updated_at": now}
		if strings.HasPrefix(conversation.DisplayName, activityLabel(source.Provider, source.ThreadID)) ||
			(source.Message != "" && conversation.ChatTimes <= 1) {
			updates["display_name"] = label
		}
		return s.db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Updates(updates).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	ext, _ := json.Marshal(map[string]any{"external_agent": map[string]any{
		"provider": source.Provider, "thread_id": source.ThreadID, "thread_source": source.ThreadSource,
		"sync_mode": "activity",
	}})
	conversation = orm.Conversation{
		ID: binding.ConversationID, DisplayName: label,
		ChannelID: "default", SearchConfig: json.RawMessage(`{}`), Ext: ext,
		Models: json.RawMessage(`[]`), ChatExecutor: chatExecutor(source.Provider),
		BaseModel: orm.BaseModel{CreateUserID: owner, CreateUserName: owner, CreatedAt: now, UpdatedAt: now},
	}
	return s.db.WithContext(ctx).Create(&conversation).Error
}

func (s *Service) resolveTurn(
	ctx context.Context,
	owner string,
	binding orm.ExternalAgentBinding,
	source Source,
	toolName string,
) (Link, error) {
	identity := owner + "\x00" + source.Provider + "\x00" + source.ThreadID + "\x00" + source.TurnID
	runID := deterministicID("mcp-run", identity)
	historyID := deterministicID("mcp-history", identity)
	requestHash := sha256.Sum256([]byte(identity))
	requestID := "mcp:" + hex.EncodeToString(requestHash[:])
	now := s.now()

	var conversation orm.Conversation
	if err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND create_user_id = ?", binding.ConversationID, owner).Take(&conversation).Error; err != nil {
		return Link{}, err
	}
	var run orm.ExternalChatRun
	err := s.db.WithContext(ctx).Where("id = ?", runID).Take(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var maxSequence int
		if err := s.db.WithContext(ctx).Model(&orm.ChatHistory{}).
			Where("conversation_id = ?", binding.ConversationID).
			Select("COALESCE(MAX(seq), 0)").Scan(&maxSequence).Error; err != nil {
			return Link{}, err
		}
		sequence := maxSequence + 1
		label := sourceLabel(source, toolName)
		run = orm.ExternalChatRun{
			ID: runID, RequestID: requestID, ConversationID: binding.ConversationID, HistoryID: historyID,
			Provider: source.Provider, ProviderThreadID: source.ThreadID, ProviderTurnID: source.TurnID,
			ActorUserID: owner, Action: ObservedAction, Status: "running",
			Query: label, Sequence: sequence,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
			return Link{}, err
		}
		if label != "" {
			ext, _ := json.Marshal(map[string]any{"external_agent_activity": map[string]any{
				"provider": source.Provider, "thread_id": source.ThreadID, "turn_id": source.TurnID,
			}})
			history := orm.ChatHistory{
				ID: historyID, Seq: sequence, ConversationID: binding.ConversationID,
				AlgorithmID: "external:" + source.Provider, RawContent: label, Content: label, Ext: ext,
				TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
			}
			if err := s.db.WithContext(ctx).Create(&history).Error; err != nil {
				return Link{}, err
			}
			if err := s.db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", binding.ConversationID).
				Updates(map[string]any{"chat_times": gorm.Expr("chat_times + 1"), "updated_at": now}).Error; err != nil {
				return Link{}, err
			}
		}
	} else if err != nil {
		return Link{}, err
	} else {
		if run.ActorUserID != owner || run.ConversationID != binding.ConversationID ||
			run.Provider != source.Provider || run.ProviderThreadID != source.ThreadID || run.ProviderTurnID != source.TurnID {
			return Link{}, ErrThreadOwned
		}
		updates := map[string]any{"status": "running", "completed_at": nil, "updated_at": now}
		if strings.TrimSpace(source.Message) != "" {
			updates["query"] = sourceLabel(source, toolName)
			if err := s.db.WithContext(ctx).Model(&orm.ChatHistory{}).Where("id = ?", run.HistoryID).
				Updates(map[string]any{"raw_content": updates["query"], "content": updates["query"], "update_time": now}).Error; err != nil {
				return Link{}, err
			}
		}
		if err := s.db.WithContext(ctx).Model(&orm.ExternalChatRun{}).Where("id = ?", run.ID).Updates(updates).Error; err != nil {
			return Link{}, err
		}
	}
	return Link{
		ConversationID: binding.ConversationID, ExternalRef: runID, HistoryID: historyID,
		Provider: source.Provider, ThreadID: source.ThreadID, TurnID: source.TurnID,
	}, nil
}

func normalizedSource(source Source, fallbackTurnID string) Source {
	source.Provider = strings.ToLower(strings.TrimSpace(source.Provider))
	source.ThreadID = strings.TrimSpace(source.ThreadID)
	source.TurnID = strings.TrimSpace(source.TurnID)
	source.ThreadSource = strings.ToLower(strings.TrimSpace(source.ThreadSource))
	source.Message = strings.TrimSpace(source.Message)
	if len([]rune(source.Message)) > 8192 {
		source.Message = string([]rune(source.Message)[:8192])
	}
	if source.TurnID == "" {
		source.TurnID = strings.TrimSpace(fallbackTurnID)
	}
	return source
}

func validSource(source Source) bool {
	return validProvider(source.Provider) && validIdentity(source.ThreadID, 128) && validIdentity(source.TurnID, 128) &&
		(source.ThreadSource == "" || validIdentity(source.ThreadSource, 32))
}

func validProvider(provider string) bool {
	switch provider {
	case "codex", "cursor", "workbuddy", "trae-work", "deepseek-harness":
		return true
	default:
		return false
	}
}

func validIdentity(value string, limit int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > limit {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func deterministicID(kind, identity string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("lazymind:"+kind+":"+identity)).String()
}

func activityLabel(provider, threadID string) string {
	label := map[string]string{
		"codex": "Codex", "cursor": "Cursor", "workbuddy": "WorkBuddy",
		"trae-work": "TRAE Work", "deepseek-harness": "DeepSeek Harness",
	}[provider]
	if label == "" {
		label = provider
	}
	runes := []rune(threadID)
	if len(runes) > 20 {
		threadID = string(runes[:12]) + "…" + string(runes[len(runes)-4:])
	}
	return label + " · " + threadID
}

func sourceLabel(source Source, toolName string) string {
	return strings.TrimSpace(source.Message)
}

func chatExecutor(provider string) string {
	switch provider {
	case "codex", "cursor", "workbuddy":
		return provider
	default:
		return "lazymind"
	}
}
