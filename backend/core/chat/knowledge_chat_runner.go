package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type KnowledgeChatRequest struct {
	UserID         string
	Query          string
	KnowledgeIDs   []string
	ConversationID string
	UseMemory      bool
	EnablePlugin   bool
}

type KnowledgeChatResult struct {
	Answer         string
	Sources        []KnowledgeChatSource
	ConversationID string
	MessageID      string
	ToolCallTurns  int
}

type KnowledgeChatSource struct {
	KnowledgeID string
	DocumentID  string
	ChunkID     string
	Title       string
	Text        string
	Number      int
}

type KnowledgeChatRunner interface {
	RunKnowledgeChat(ctx context.Context, input KnowledgeChatRequest) (KnowledgeChatResult, error)
}

type KnowledgeChatErrorCode string

const (
	KnowledgeChatInvalidArgument    KnowledgeChatErrorCode = "INVALID_ARGUMENT"
	KnowledgeChatNotFound           KnowledgeChatErrorCode = "NOT_FOUND"
	KnowledgeChatForbidden          KnowledgeChatErrorCode = "FORBIDDEN"
	KnowledgeChatBackendUnavailable KnowledgeChatErrorCode = "BACKEND_UNAVAILABLE"
	KnowledgeChatInternal           KnowledgeChatErrorCode = "INTERNAL"
)

type KnowledgeChatError struct {
	Code    KnowledgeChatErrorCode
	Message string
	Cause   error
}

func (e *KnowledgeChatError) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = string(e.Code)
	}
	if e.Cause != nil {
		return msg + ": " + e.Cause.Error()
	}
	return msg
}

func (e *KnowledgeChatError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *KnowledgeChatError) Is(target error) bool {
	if e == nil {
		return false
	}
	var targetErr *KnowledgeChatError
	if !errors.As(target, &targetErr) || targetErr == nil || targetErr.Code == "" {
		return false
	}
	return e.Code == targetErr.Code
}

type KnowledgeAccessChecker interface {
	EnsureKnowledgeReadable(ctx context.Context, userID string, knowledgeID string) error
}

type KnowledgeChatRunnerDeps struct {
	DB            *gorm.DB
	BaseURL       string
	AccessChecker KnowledgeAccessChecker
	StreamChat    func(ctx context.Context, baseURL string, body map[string]any) (<-chan UpstreamStreamChunk, error)
}

type knowledgeChatRunner struct {
	db            *gorm.DB
	baseURL       string
	accessChecker KnowledgeAccessChecker
	streamChat    func(ctx context.Context, baseURL string, body map[string]any) (<-chan UpstreamStreamChunk, error)
}

var toolPreviewTagPattern = regexp.MustCompile(`(?s)<(?:tp|trp)\b[^>]*>.*?</(?:tp|trp)>`)

func NewKnowledgeChatRunner(deps KnowledgeChatRunnerDeps) KnowledgeChatRunner {
	streamChat := deps.StreamChat
	if streamChat == nil {
		streamChat = StreamChatUpstream
	}
	return &knowledgeChatRunner{
		db:            deps.DB,
		baseURL:       strings.TrimRight(strings.TrimSpace(deps.BaseURL), "/"),
		accessChecker: deps.AccessChecker,
		streamChat:    streamChat,
	}
}

func (r *knowledgeChatRunner) RunKnowledgeChat(ctx context.Context, input KnowledgeChatRequest) (KnowledgeChatResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInvalidArgument, "user_id required", nil)
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInvalidArgument, "query required", nil)
	}
	kbIDs := normalizeKnowledgeIDs(input.KnowledgeIDs)
	if len(kbIDs) == 0 {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInvalidArgument, "knowledge_ids required", nil)
	}
	if r == nil || r.db == nil {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInternal, "chat store not initialized", nil)
	}
	if r.accessChecker == nil {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInternal, "knowledge access checker not initialized", nil)
	}
	if r.streamChat == nil {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInternal, "chat stream client not initialized", nil)
	}
	if err := r.ensureKnowledgeReadable(ctx, userID, kbIDs); err != nil {
		return KnowledgeChatResult{}, err
	}
	convID := strings.TrimSpace(input.ConversationID)
	if convID == "" {
		convID = newConversationID()
	}
	if len(convID) > maxConversationIDLength {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInvalidArgument, "conversation_id too long", nil)
	}
	if input.ConversationID != "" {
		if err := r.ensureConversationBelongsToUser(ctx, convID, userID); err != nil {
			return KnowledgeChatResult{}, err
		}
	}

	searchConfig := knowledgeSearchConfig(kbIDs)
	searchConfigJSON, err := json.Marshal(searchConfig)
	if err != nil {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInternal, "build search config failed", err)
	}
	displayName := GetDefaultDisplayName(convID, []map[string]any{{"text": query}})
	_, seq, err := ensureConversation(ctx, r.db, convID, displayName, searchConfigJSON, nil, userID, "", nil)
	if err != nil {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInternal, "ensure conversation failed", err)
	}

	var histories []orm.ChatHistory
	if err := r.db.WithContext(ctx).Where("conversation_id = ?", convID).Order("seq ASC").Find(&histories).Error; err != nil {
		return KnowledgeChatResult{}, knowledgeChatErr(KnowledgeChatInternal, "load conversation histories failed", err)
	}
	historyID := newID("h_")
	raw := map[string]any{
		"conversation": map[string]any{
			"search_config": searchConfig,
		},
		"filters": map[string]any{
			"kb_id": kbIDs,
		},
		"stream":        true,
		"mode":          "auto",
		"use_memory":    input.UseMemory,
		"enable_plugin": input.EnablePlugin,
		"files":         map[string]any{},
		"databases":     []any{},
	}
	reqBody := buildChatRequestBody(ctx, r.db, convID, upstreamSessionID(convID), query, histories, raw, nil, userID, seq)
	reqBody["conversation"] = raw["conversation"]
	reqBody["enable_plugin"] = input.EnablePlugin

	result, rawSources, err := r.collectKnowledgeChatStream(ctx, reqBody)
	if err != nil {
		return KnowledgeChatResult{}, err
	}
	result.ConversationID = convID
	result.MessageID = historyID
	if err := r.persistKnowledgeChatResult(ctx, convID, historyID, query, result, rawSources, seq); err != nil {
		return KnowledgeChatResult{}, err
	}
	return result, nil
}

func (r *knowledgeChatRunner) ensureKnowledgeReadable(ctx context.Context, userID string, kbIDs []string) error {
	for _, kbID := range kbIDs {
		if err := r.accessChecker.EnsureKnowledgeReadable(ctx, userID, kbID); err != nil {
			return mapKnowledgeAccessError(err)
		}
	}
	return nil
}

func (r *knowledgeChatRunner) ensureConversationBelongsToUser(ctx context.Context, convID, userID string) error {
	var c orm.Conversation
	err := r.db.WithContext(ctx).Select("id", "create_user_id").Where("id = ?", convID).First(&c).Error
	if err == nil {
		if strings.TrimSpace(c.CreateUserID) != userID {
			return knowledgeChatErr(KnowledgeChatNotFound, "conversation not found", nil)
		}
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return knowledgeChatErr(KnowledgeChatNotFound, "conversation not found", err)
	}
	return knowledgeChatErr(KnowledgeChatInternal, "load conversation failed", err)
}

func (r *knowledgeChatRunner) collectKnowledgeChatStream(ctx context.Context, reqBody map[string]any) (KnowledgeChatResult, []any, error) {
	ch, err := r.streamChat(ctx, r.baseURL, reqBody)
	if err != nil {
		return KnowledgeChatResult{}, nil, knowledgeChatErr(KnowledgeChatBackendUnavailable, "chat service unavailable", err)
	}
	var fullText string
	var sources []any
	var toolCallTurns int
	var failedStatus string
	for d := range ch {
		if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > toolCallTurns {
			toolCallTurns = next
		}
		if len(d.Sources) > 0 {
			sources = d.Sources
		}
		if strings.EqualFold(strings.TrimSpace(d.Status), "FAILED") {
			failedStatus = d.Status
		}
		fullText += d.Text
	}
	if err := ctx.Err(); err != nil {
		return KnowledgeChatResult{}, nil, knowledgeChatErr(KnowledgeChatBackendUnavailable, "chat stream interrupted", err)
	}
	if failedStatus != "" {
		return KnowledgeChatResult{}, nil, knowledgeChatErr(KnowledgeChatBackendUnavailable, "chat service failed", fmt.Errorf("upstream status %s", failedStatus))
	}
	answer := sanitizeKnowledgeChatAnswer(fullText)
	return KnowledgeChatResult{
		Answer:        answer,
		Sources:       sanitizeKnowledgeChatSources(sources),
		ToolCallTurns: toolCallTurns,
	}, sources, nil
}

func sanitizeKnowledgeChatAnswer(text string) string {
	text = stripToolTags(text)
	text = stripThinkTags(text)
	text = toolPreviewTagPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func (r *knowledgeChatRunner) persistKnowledgeChatResult(ctx context.Context, convID, historyID, query string, result KnowledgeChatResult, rawSources []any, seq int) error {
	now := time.Now()
	retrievalResult := marshalRetrievalResult(knowledgeChatSourcesAsRaw(result.Sources))
	if retrievalResult == nil && len(rawSources) > 0 {
		retrievalResult = marshalRetrievalResult(nil)
	}
	history := orm.ChatHistory{
		ID:              historyID,
		Seq:             seq,
		ConversationID:  convID,
		RawContent:      query,
		RetrievalResult: retrievalResult,
		Content:         query,
		Result:          result.Answer,
		ToolCallTurns:   result.ToolCallTurns,
		TimeMixin:       orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if err := r.db.WithContext(ctx).Create(&history).Error; err != nil {
		return knowledgeChatErr(KnowledgeChatInternal, "save chat history failed", err)
	}
	if err := r.db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", convID).Updates(map[string]any{
		"updated_at": now,
		"chat_times": gorm.Expr("chat_times + ?", 1),
	}).Error; err != nil {
		return knowledgeChatErr(KnowledgeChatInternal, "update conversation failed", err)
	}
	return nil
}

func normalizeKnowledgeIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func knowledgeSearchConfig(kbIDs []string) map[string]any {
	datasets := make([]map[string]any, 0, len(kbIDs))
	for _, id := range kbIDs {
		datasets = append(datasets, map[string]any{"id": id})
	}
	return map[string]any{
		"dataset_list": datasets,
		"top_k":        defaultTopK,
	}
}

func sanitizeKnowledgeChatSources(raw []any) []KnowledgeChatSource {
	out := make([]KnowledgeChatSource, 0, len(raw))
	seen := map[string]struct{}{}
	for _, item := range raw {
		m, ok := sourceMap(item)
		if !ok {
			continue
		}
		source := KnowledgeChatSource{
			KnowledgeID: firstSourceString(m, "dataset_id", "knowledge_id", "kb_id"),
			DocumentID:  firstSourceString(m, "core_document_id"),
			ChunkID:     firstSourceString(m, "uid", "chunk_id", "segment_id", "segement_id"),
			Title:       firstSourceString(m, "file_name", "title", "name"),
			Text:        firstSourceString(m, "content", "text"),
			Number:      firstSourceInt(m, "segment_number", "number", "chunk_index"),
		}
		if source.KnowledgeID == "" && source.DocumentID == "" && source.ChunkID == "" && source.Title == "" && source.Text == "" {
			continue
		}
		key := source.KnowledgeID + "\x00" + source.DocumentID + "\x00" + source.ChunkID + "\x00" + source.Text
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, source)
	}
	return out
}

func knowledgeChatSourcesAsRaw(sources []KnowledgeChatSource) []any {
	out := make([]any, 0, len(sources))
	for _, source := range sources {
		m := map[string]any{}
		if source.KnowledgeID != "" {
			m["dataset_id"] = source.KnowledgeID
		}
		if source.DocumentID != "" {
			m["document_id"] = source.DocumentID
		}
		if source.ChunkID != "" {
			m["segement_id"] = source.ChunkID
		}
		if source.Title != "" {
			m["file_name"] = source.Title
		}
		if source.Text != "" {
			m["content"] = source.Text
		}
		if source.Number != 0 {
			m["segment_number"] = source.Number
		}
		out = append(out, m)
	}
	return out
}

func sourceMap(v any) (map[string]any, bool) {
	switch typed := v.(type) {
	case map[string]any:
		return typed, true
	case map[string]string:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			out[k] = val
		}
		return out, true
	default:
		return nil, false
	}
}

func firstSourceString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func firstSourceInt(m map[string]any, keys ...string) int {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch typed := v.(type) {
			case int:
				return typed
			case int32:
				return int(typed)
			case int64:
				return int(typed)
			case float64:
				return int(typed)
			case json.Number:
				if n, err := typed.Int64(); err == nil {
					return int(n)
				}
			case string:
				var n int
				if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &n); err == nil {
					return n
				}
			}
		}
	}
	return 0
}

func knowledgeChatErr(code KnowledgeChatErrorCode, message string, cause error) error {
	return &KnowledgeChatError{Code: code, Message: message, Cause: cause}
}

func mapKnowledgeAccessError(err error) error {
	if err == nil {
		return nil
	}
	var chatErr *KnowledgeChatError
	if errors.As(err, &chatErr) {
		switch chatErr.Code {
		case KnowledgeChatNotFound, KnowledgeChatForbidden:
			return knowledgeChatErr(KnowledgeChatNotFound, "knowledge not found", err)
		case KnowledgeChatInvalidArgument:
			return knowledgeChatErr(KnowledgeChatInvalidArgument, "invalid knowledge access request", err)
		case KnowledgeChatBackendUnavailable:
			return knowledgeChatErr(KnowledgeChatBackendUnavailable, "knowledge access unavailable", err)
		case KnowledgeChatInternal:
			return knowledgeChatErr(KnowledgeChatInternal, "knowledge access failed", err)
		}
	}
	return knowledgeChatErr(KnowledgeChatInternal, "knowledge access failed", err)
}
