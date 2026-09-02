package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/log"
	"lazymind/core/resourceupdate"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
)

const (
	maxConversationIDLength          = 36
	maxConversationDisplayNameLength = 255
	maxTopK                          = 10
	defaultTopK                      = 3
	routerTrafficAttemptsExtKey      = "router_traffic_attempts"
)

type routerTrafficAttempt struct {
	AlgorithmID string    `json:"algorithm_id"`
	FeedBack    int       `json:"feed_back"`
	Reason      string    `json:"reason,omitempty"`
	CreateTime  time.Time `json:"create_time"`
}

func shouldEmitStreamFrame(delta string, sources []any) bool {
	return delta != "" || len(sources) > 0
}

func userIDFromChatRequestBody(reqBody map[string]any) string {
	userID, _ := reqBody["user_id"].(string)
	return strings.TrimSpace(userID)
}

func llmConfigFromBody(reqBody map[string]any) map[string]any {
	if cfg, ok := reqBody["llm_config"].(map[string]any); ok && len(cfg) > 0 {
		return cfg
	}
	return nil
}

func toolConfigFromBody(reqBody map[string]any) map[string]any {
	if cfg, ok := reqBody["tool_config"].(map[string]any); ok && len(cfg) > 0 {
		return cfg
	}
	return nil
}

func marshalRetrievalResult(sources []any) json.RawMessage {
	payload, err := json.Marshal(map[string]any{"sources": sources})
	if err != nil {
		return nil
	}
	return payload
}

func retrievalSources(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return nil
	}
	var result struct {
		Sources json.RawMessage `json:"sources"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	if len(result.Sources) == 0 || string(result.Sources) == "null" {
		return nil
	}

	var sources []any
	if err := json.Unmarshal(result.Sources, &sources); err == nil {
		return sources
	}

	var sourceMap map[string]any
	if err := json.Unmarshal(result.Sources, &sourceMap); err != nil {
		return nil
	}
	indices := make([]string, 0, len(sourceMap))
	for index := range sourceMap {
		indices = append(indices, index)
	}
	sort.Strings(indices)
	for _, index := range indices {
		source := sourceMap[index]
		if fields, ok := source.(map[string]any); ok {
			if _, hasIndex := fields["index"]; !hasIndex {
				fields["index"] = index
			}
		}
		sources = append(sources, source)
	}
	return sources
}

func nonNegativeToolCallTurns(v int64) int {
	if v < 0 {
		return 0
	}
	maxInt := int(^uint(0) >> 1)
	if v > int64(maxInt) {
		return maxInt
	}
	return int(v)
}

// newID text history text ID。
func newID(prefix string) string {
	return prefix + strconvBase36(time.Now().UnixNano())
}

func strconvBase36(v int64) string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [32]byte
	i := len(b)
	for v > 0 && i > 0 {
		i--
		b[i] = chars[v%36]
		v /= 36
	}
	if neg && i > 0 {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// GetDefaultDisplayName:
// 1. Use the first non-empty "text" from input.
// 2. Otherwise use the first non-empty "uri".
// 3. Otherwise fall back to conversationID.
// 4. Truncate to at most 255 runes.
func GetDefaultDisplayName(conversationID string, input []map[string]any) string {
	tempContent := ""
	for _, q := range input {
		if t, ok := q["text"].(string); ok && strings.TrimSpace(t) != "" {
			tempContent = strings.TrimSpace(t)
			break
		}
		if tempContent == "" {
			if u, ok := q["uri"].(string); ok && strings.TrimSpace(u) != "" {
				tempContent = strings.TrimSpace(u)
			}
		}
	}
	if tempContent == "" {
		tempContent = conversationID
	}
	runes := []rune(tempContent)
	if len(runes) > maxConversationDisplayNameLength {
		return string(runes[:maxConversationDisplayNameLength])
	}
	return string(runes)
}

func newConversationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	out := make([]byte, 36)
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out)
}

func conversationIDFromName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "conversations/")
	name = strings.TrimPrefix(name, "/")
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// ensureConversation textCreatetextUsertextConversation，textConversation、text history text seq、error
func ensureConversation(ctx context.Context, db *gorm.DB, convID, displayName string, searchConfig json.RawMessage, models json.RawMessage, userID, userName string, runInBackground bool, requestedThinkingDepth string, conversationSettings map[string]any) (*orm.Conversation, int, error) {
	now := time.Now()
	var c orm.Conversation
	err := db.Where("id = ? AND create_user_id = ?", convID, userID).First(&c).Error
	if err == nil {
		if c.DeletedAt != nil {
			return nil, 0, errConversationInTrash
		}
		var count int64
		db.Model(&orm.ChatHistory{}).Where("conversation_id = ?", convID).Count(&count)

		updates := map[string]any{}
		if c.ArchivedAt != nil {
			updates["archived_at"] = nil
			updates["archive_folder_id"] = nil
			c.ArchivedAt = nil
			c.ArchiveFolderID = nil
			if err := taskcenter.RestoreTasksForConversations(ctx, db, userID, []string{convID}, taskcenter.ArchivedReasonConversationArchive, now.UTC()); err != nil {
				return nil, 0, err
			}
		}
		if len(searchConfig) > 0 && (len(c.SearchConfig) == 0 || string(c.SearchConfig) == "{}") {
			updates["search_config"] = searchConfig
		}
		if len(models) > 0 && len(c.Models) == 0 {
			updates["models"] = models
		}
		if displayName != "" && c.DisplayName == "" {
			updates["display_name"] = displayName
		}
		if len(updates) > 0 {
			db.Model(&orm.Conversation{}).Where("id = ?", c.ID).Updates(updates)
		}

		return &c, int(count) + 1, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, 0, err
	}
	c = orm.Conversation{
		ID:           convID,
		DisplayName:  displayName,
		ChannelID:    "default",
		SearchConfig: searchConfig,
		Models:       models,
		ChatTimes:    0,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if ephemeral, _ := conversationSettings["ephemeral"].(bool); ephemeral {
		c.IsEphemeral = true
		if persistent, _ := conversationSettings["persistent_ephemeral"].(bool); !persistent {
			expiresAt := now.Add(24 * time.Hour)
			c.EphemeralExpiresAt = &expiresAt
		}
		c.SourceType, _ = conversationSettings["source_type"].(string)
		c.SourceDatasetID, _ = conversationSettings["source_dataset_id"].(string)
		c.SourceDocumentID, _ = conversationSettings["source_document_id"].(string)
		c.SourceDisplayName, _ = conversationSettings["source_display_name"].(string)
	}
	// Resolve plugin settings for the new conversation.
	// Priority: caller-supplied conversation settings > user_chat_settings defaults.
	// All fields are written once so the conversation owns a stable execution
	// policy without per-request fallback queries.
	settings := resolveInitialConversationSettings(ctx, db, userID, runInBackground, requestedThinkingDepth, conversationSettings)
	c.EnableWorkflow = &settings.enableWorkflow
	c.WorkflowMode = &settings.workflowMode
	c.EnableSubagent = &settings.enableSubagent
	c.ChatExecutor = settings.chatExecutor
	c.ThinkingDepth = settings.thinkingDepth
	if err := db.Create(&c).Error; err != nil {
		return nil, 0, err
	}
	return &c, 1, nil
}

type resolvedConversationSettings struct {
	enableWorkflow bool
	workflowMode   string
	enableSubagent bool
	chatExecutor   string
	thinkingDepth  string
}

// resolveInitialConversationSettings merges caller-supplied overrides with the user's
// global defaults from user_chat_settings. Fields present in conversationSettings take
// priority; missing fields fall back to the DB defaults (or hardcoded values if
// the user has no row yet).
func resolveInitialConversationSettings(ctx context.Context, db *gorm.DB, userID string, runInBackground bool, requestedThinkingDepth string, conversationSettings map[string]any) resolvedConversationSettings {
	legacy := defaultUserChatSettings(userID)
	entry := quickQuestionDefaultsFromLegacy(legacy)
	if runInBackground {
		entry = newTaskDefaultsFromLegacy(legacy)
	}
	if db != nil {
		if stored, err := entryDefaultsForRequest(ctx, db, userID, runInBackground); err == nil {
			entry = stored
		}
	}
	out := resolvedConversationSettings{
		enableWorkflow: entry.ConversationSettings.EnableWorkflow,
		workflowMode:   entry.ConversationSettings.WorkflowMode,
		enableSubagent: entry.ConversationSettings.EnableSubagent,
		chatExecutor:   entry.ConversationSettings.ChatExecutor,
		thinkingDepth:  entry.ThinkingDepth,
	}
	if depth, valid := normalizeThinkingDepth(requestedThinkingDepth); valid {
		out.thinkingDepth = depth
	}
	// Apply caller-supplied overrides.
	if v, ok := conversationSettings["enable_workflow"].(bool); ok {
		out.enableWorkflow = v
	}
	if v, ok := conversationSettings["workflow_mode"].(string); ok && (v == "dynamic" || v == "auto") {
		out.workflowMode = v
	}
	if v, ok := conversationSettings["enable_subagent"].(bool); ok {
		out.enableSubagent = v
	}
	if v, ok := conversationSettings["chat_executor"].(string); ok {
		if normalized, valid := normalizeChatExecutor(v); valid {
			out.chatExecutor = normalized
		}
	}
	return out
}

// askAnswersStructuredFromRaw extracts the ask_answers_structured map from the raw request body.
// Returns nil if not present or not a valid map.
func askAnswersStructuredFromRaw(raw map[string]any) map[string]any {
	v, ok := raw["ask_answers_structured"]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

// askAnswersStructuredPayload mirrors the JSON sent by the frontend when the user
// submits an AskCard.
type askAnswersStructuredPayload struct {
	AskID     string                    `json:"ask_id"`
	Questions []askAnsweredQuestionItem `json:"questions"`
}

type askAnsweredQuestionItem struct {
	Text          string          `json:"text"`
	Type          string          `json:"type"`
	Choices       []string        `json:"choices"`
	CustomChoices []string        `json:"custom_choices"`
	Answer        json.RawMessage `json:"answer"` // null or object
}

// buildAskUserToolResultContent formats the three cases described in the plan.
// askStructured non-nil → full submission; askSavedAnswers non-nil → partial; both nil → unanswered.
func buildAskUserToolResultContent(
	askPendingData map[string]any,
	askStructured *askAnswersStructuredPayload,
	askSavedAnswers map[string]any,
) string {
	questionsRaw, _ := askPendingData["questions"].([]any)

	if askStructured != nil {
		lines := []string{"Questions were shown via an interactive card. The user submitted the form; some answers may be omitted.", ""}
		for i, sq := range askStructured.Questions {
			prefix := fmt.Sprintf("Q%d: %s", i+1, sq.Text)
			if len(sq.Choices) > 0 {
				opts := make([]string, len(sq.Choices))
				for ci, ch := range sq.Choices {
					label := ch
					if ci < len(sq.CustomChoices) && sq.CustomChoices[ci] != "" {
						label = sq.CustomChoices[ci]
					}
					opts[ci] = fmt.Sprintf("[%c] %s", rune('A'+ci), label)
				}
				lines = append(lines, prefix)
				lines = append(lines, "  Options: "+strings.Join(opts, "  "))
			} else {
				lines = append(lines, prefix)
			}
			answerStr := "(no answer)"
			if len(sq.Answer) > 0 && string(sq.Answer) != "null" {
				var ans map[string]any
				if json.Unmarshal(sq.Answer, &ans) == nil {
					if v, ok := ans["value"]; ok {
						answerStr = fmt.Sprintf("%v", v)
					}
				} else {
					answerStr = string(sq.Answer)
				}
			}
			lines = append(lines, "  Answer: "+answerStr)
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}

	if askSavedAnswers != nil && len(questionsRaw) > 0 {
		lines := []string{
			"Questions were shown. The user partially filled the form but did NOT submit.",
			"Treat the user's new message as additional guidance — use available answers and do NOT re-ask.",
			"",
		}
		for i, qRaw := range questionsRaw {
			qMap, _ := qRaw.(map[string]any)
			qText, _ := qMap["text"].(string)
			prefix := fmt.Sprintf("Q%d: %s", i+1, qText)
			lines = append(lines, prefix)
			idxKey := fmt.Sprintf("%d", i)
			if _, hasAns := askSavedAnswers[idxKey]; hasAns {
				lines = append(lines, "  Answer: [partial answer saved]")
			} else {
				lines = append(lines, "  Answer: [未填写]")
			}
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}

	return "Questions were shown but the user ignored them and sent a new message instead.\n" +
		"Treat the new message as a clarification or modification of the original task.\n" +
		"Do NOT re-ask these questions unless the user explicitly requests it."
}

// buildHistoryMessages converts stored chat histories to the format expected by Python.
// When askAnswersStructured is provided, the last unanswered ask_user tool_result is
// rewritten to contain the full structured context.
// Each message may include history_seq (chat_histories.seq) for dual-track compression.
func buildHistoryMessages(histories []orm.ChatHistory, askAnswersStructured map[string]any) []map[string]any {
	if len(histories) == 0 {
		return nil
	}

	// Find the last history that has an unanswered ask_pending.
	var askRewriteIdx int = -1
	for i := len(histories) - 1; i >= 0; i-- {
		h := &histories[i]
		if len(h.Ext) == 0 {
			continue
		}
		var ext map[string]any
		if err := json.Unmarshal(h.Ext, &ext); err != nil {
			continue
		}
		if ext["ask_pending"] == nil {
			continue
		}
		if answered, _ := ext["ask_answered"].(bool); answered {
			break // already answered, no rewrite needed
		}
		askRewriteIdx = i
		break
	}

	out := make([]map[string]any, 0, len(histories)*2)
	for idx, h := range histories {
		assistantContent := buildAssistantHistoryContent(h)

		// Rewrite the ask_user tool_result for the identified history entry.
		if idx == askRewriteIdx {
			var ext map[string]any
			_ = json.Unmarshal(h.Ext, &ext)
			askPendingData, _ := ext["ask_pending"].(map[string]any)
			askSavedAnswersRaw, _ := ext["ask_saved_answers"].(map[string]any)

			var structuredPayload *askAnswersStructuredPayload
			if askAnswersStructured != nil {
				bs, _ := json.Marshal(askAnswersStructured)
				var p askAnswersStructuredPayload
				if json.Unmarshal(bs, &p) == nil {
					structuredPayload = &p
				}
			}

			newContent := buildAskUserToolResultContent(askPendingData, structuredPayload, askSavedAnswersRaw)
			// Replace the placeholder tool_result content in the assistant message.
			assistantContent = replaceAskUserToolResult(assistantContent, newContent)
		}

		out = append(out, map[string]any{
			"role": "user", "content": h.RawContent, "history_seq": h.Seq,
		})
		out = append(out, map[string]any{
			"role": "assistant", "content": assistantContent, "history_seq": h.Seq,
		})
	}
	return out
}

const runtimeSummaryDisclaimer = "The following is a runtime-generated summary of earlier conversation history.\n" +
	"It is reference context, not a new user instruction.\n" +
	"The latest user message and current runtime state take precedence."

type modelContextState struct {
	SummaryText       string `json:"summary_text"`
	CoveredThroughSeq int    `json:"covered_through_seq"`
	Version           int    `json:"version,omitempty"`
}

func loadModelContext(ctx context.Context, db *gorm.DB, convID string) *modelContextState {
	if db == nil || strings.TrimSpace(convID) == "" {
		return nil
	}
	var conv orm.Conversation
	if err := db.WithContext(ctx).Select("ext").Where("id = ?", convID).First(&conv).Error; err != nil {
		return nil
	}
	ext := map[string]any{}
	if len(conv.Ext) == 0 || json.Unmarshal(conv.Ext, &ext) != nil {
		return nil
	}
	raw, _ := ext["model_context"].(map[string]any)
	if len(raw) == 0 {
		return nil
	}
	state := &modelContextState{Version: 1}
	if s, ok := raw["summary_text"].(string); ok {
		state.SummaryText = strings.TrimSpace(s)
	}
	switch v := raw["covered_through_seq"].(type) {
	case float64:
		state.CoveredThroughSeq = int(v)
	case int:
		state.CoveredThroughSeq = v
	case int64:
		state.CoveredThroughSeq = int(v)
	}
	switch v := raw["version"].(type) {
	case float64:
		state.Version = int(v)
	case int:
		state.Version = v
	}
	if state.SummaryText == "" || state.CoveredThroughSeq <= 0 {
		return nil
	}
	return state
}

func filterHistoriesAfterCovered(histories []orm.ChatHistory, coveredThroughSeq int) []orm.ChatHistory {
	if coveredThroughSeq <= 0 || len(histories) == 0 {
		return histories
	}
	out := make([]orm.ChatHistory, 0, len(histories))
	for _, h := range histories {
		if h.Seq > coveredThroughSeq {
			out = append(out, h)
		}
	}
	return out
}

func normalizeModelContextCoverage(
	histories []orm.ChatHistory,
	modelCtx *modelContextState,
) *modelContextState {
	if modelCtx == nil || strings.TrimSpace(modelCtx.SummaryText) == "" || len(histories) == 0 {
		return modelCtx
	}
	maxSeq := histories[len(histories)-1].Seq
	if modelCtx.CoveredThroughSeq <= maxSeq {
		return modelCtx
	}
	return nil
}

func buildModelHistoryMessages(
	histories []orm.ChatHistory,
	askAnswersStructured map[string]any,
	modelCtx *modelContextState,
) []map[string]any {
	modelCtx = normalizeModelContextCoverage(histories, modelCtx)
	tail := histories
	if modelCtx != nil {
		tail = filterHistoriesAfterCovered(histories, modelCtx.CoveredThroughSeq)
	}
	msgs := buildHistoryMessages(tail, askAnswersStructured)
	if modelCtx == nil || strings.TrimSpace(modelCtx.SummaryText) == "" {
		return msgs
	}
	summaryMsg := map[string]any{
		"role":    "user",
		"content": runtimeSummaryDisclaimer + "\n\n" + strings.TrimSpace(modelCtx.SummaryText),
	}
	out := make([]map[string]any, 0, len(msgs)+1)
	out = append(out, summaryMsg)
	out = append(out, msgs...)
	return out
}

func clearModelContext(ctx context.Context, db *gorm.DB, convID string) error {
	if db == nil || strings.TrimSpace(convID) == "" {
		return nil
	}
	var conv orm.Conversation
	if err := db.WithContext(ctx).Select("id", "ext").Where("id = ?", convID).First(&conv).Error; err != nil {
		return err
	}
	ext := map[string]any{}
	if len(conv.Ext) == 0 {
		return nil
	}
	if err := json.Unmarshal(conv.Ext, &ext); err != nil {
		return err
	}
	if _, ok := ext["model_context"]; !ok {
		return nil
	}
	delete(ext, "model_context")
	raw, err := json.Marshal(ext)
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", convID).Update("ext", raw).Error
}

// handleModelContextUpdated atomically writes summary_text + covered_through_seq.
// Rejects empty summary, non-positive covered, stale/equal covered watermarks,
// or regressions below the current covered seq.
func handleModelContextUpdated(
	ctx context.Context,
	db *gorm.DB,
	convID string,
	ev *ModelContextUpdatedEvent,
) {
	if db == nil || ev == nil || strings.TrimSpace(convID) == "" {
		return
	}
	summary := strings.TrimSpace(ev.SummaryText)
	if summary == "" || ev.CoveredThroughSeq <= 0 {
		return
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv orm.Conversation
		if err := tx.Select("id", "ext").Where("id = ?", convID).First(&conv).Error; err != nil {
			return err
		}
		ext := map[string]any{}
		if len(conv.Ext) > 0 {
			_ = json.Unmarshal(conv.Ext, &ext)
		}
		prevCovered := 0
		if prev, ok := ext["model_context"].(map[string]any); ok {
			switch v := prev["covered_through_seq"].(type) {
			case float64:
				prevCovered = int(v)
			case int:
				prevCovered = v
			case int64:
				prevCovered = int(v)
			}
		}
		if ev.CoveredThroughSeq <= prevCovered {
			return nil
		}
		version := ev.Version
		if version <= 0 {
			version = 1
		}
		ext["model_context"] = map[string]any{
			"summary_text":        summary,
			"covered_through_seq": ev.CoveredThroughSeq,
			"version":             version,
		}
		raw, err := json.Marshal(ext)
		if err != nil {
			return err
		}
		return tx.Model(&orm.Conversation{}).Where("id = ?", convID).Update("ext", raw).Error
	})
	if err != nil {
		fmt.Printf("[Core] [MODEL_CONTEXT_UPDATE_FAILED] conv=%s err=%v\n", convID, err)
	}
}

var askUserToolResultPattern = regexp.MustCompile(`(?s)(<tool_result\b[^>]*>)Question sent to user \(ask_id=[^)]+\)\.(</tool_result>)`)
var toolResultBlockPattern = regexp.MustCompile(`(?s)<tool_result\b[^>]*>.*?</tool_result>`)

// replaceAskUserToolResult replaces the placeholder ask_user tool_result content
// in an assistant message with enriched context so the LLM understands the state.
func replaceAskUserToolResult(assistantContent, newContent string) string {
	replaced := askUserToolResultPattern.ReplaceAllString(
		assistantContent, "${1}"+newContent+"${2}",
	)
	return toolResultBlockPattern.ReplaceAllStringFunc(replaced, func(block string) string {
		openEnd := strings.Index(block, ">")
		closeStart := strings.LastIndex(block, "</tool_result>")
		if openEnd < 0 || closeStart <= openEnd {
			return block
		}
		var payload map[string]any
		if json.Unmarshal([]byte(block[openEnd+1:closeStart]), &payload) != nil {
			return block
		}
		if name, _ := payload["name"].(string); name != "ask_user" {
			return block
		}
		payload["result"] = newContent
		encoded, err := json.Marshal(payload)
		if err != nil {
			return block
		}
		return block[:openEnd+1] + string(encoded) + block[closeStart:]
	})
}

const chatActionRegeneration = "CHAT_ACTION_REGENERATION"

type chatPersistTarget struct {
	HistoryID      string
	Seq            int
	Existing       *orm.ChatHistory
	IsRegeneration bool
}

func parseChatAction(raw map[string]any) string {
	if action, ok := raw["action"].(string); ok {
		return strings.TrimSpace(action)
	}
	return ""
}

func resolvePersistTarget(histories []orm.ChatHistory, raw map[string]any, nextSeq int) chatPersistTarget {
	target := chatPersistTarget{Seq: nextSeq}
	if parseChatAction(raw) != chatActionRegeneration || len(histories) == 0 {
		return target
	}
	last := histories[len(histories)-1]
	target.HistoryID = last.ID
	target.Seq = last.Seq
	target.IsRegeneration = true
	target.Existing = &last
	return target
}

func historiesForUpstream(histories []orm.ChatHistory, target chatPersistTarget) []orm.ChatHistory {
	if !target.IsRegeneration || len(histories) == 0 {
		return histories
	}
	return histories[:len(histories)-1]
}

func setConversationDefaultValue(raw map[string]any) {
	if raw["conversation"] == nil {
		raw["conversation"] = map[string]any{}
	}
	conv, _ := raw["conversation"].(map[string]any)
	if conv["search_config"] == nil {
		conv["search_config"] = map[string]any{}
	}
	sc, _ := conv["search_config"].(map[string]any)
	if topK, ok := sc["top_k"].(float64); !ok || topK < 1 || topK > maxTopK {
		sc["top_k"] = defaultTopK
	}
	if conf, ok := sc["confidence"].(float64); !ok || conf < 0 || conf > 1 {
		sc["confidence"] = 0.5
	}
}

func checkInput(raw map[string]any) bool {
	in, ok := raw["input"].([]any)
	if !ok || len(in) == 0 {
		return raw["query"] != nil || raw["content"] != nil
	}
	for _, it := range in {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		if s, _ := m["text"].(string); strings.TrimSpace(s) != "" {
			return true
		}
		if s, _ := m["content"].(string); strings.TrimSpace(s) != "" {
			return true
		}
		if _, hasURI := m["uri"]; hasURI {
			return true
		}
	}
	return false
}

func buildChatHistoryExt(raw map[string]any, query string) json.RawMessage {
	input := chatHistoryInput(raw, query)
	if input == nil {
		return nil
	}
	ext := map[string]any{"input": input}
	if mentions, ok := raw["mentions"].([]any); ok && len(mentions) > 0 {
		ext["mentions"] = mentions
	}
	if documentContext, ok := raw["document_context"].(map[string]any); ok && len(documentContext) > 0 {
		ext["document_context"] = documentContext
	}
	b, err := json.Marshal(ext)
	if err != nil {
		return nil
	}
	return b
}

// buildChatHistoryExtWithTrail extends the existing history metadata only when
// the request explicitly used the citation/reference action. Relationship
// inference from question text is intentionally excluded from this path.
func buildChatHistoryExtWithTrail(
	raw map[string]any,
	query string,
	histories []orm.ChatHistory,
	target chatPersistTarget,
) json.RawMessage {
	base := buildChatHistoryExt(raw, query)
	ext := map[string]any{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &ext)
	}

	if target.IsRegeneration && target.Existing != nil {
		var previous struct {
			Trail json.RawMessage `json:"trail"`
		}
		if json.Unmarshal(target.Existing.Ext, &previous) == nil && len(previous.Trail) > 0 {
			ext["trail"] = json.RawMessage(previous.Trail)
		}
	}
	// Regeneration reuses the existing user turn. A citation tag embedded in
	// its stored input is not a new reference action; only an explicit source
	// ID can change the relationship during regeneration/editing.
	if target.IsRegeneration && len(referencedHistoryIDs(raw)) == 0 {
		return marshalChatHistoryExt(ext)
	}

	if !hasExplicitConversationReference(raw) {
		return marshalChatHistoryExt(ext)
	}

	referenceHistoryIDs := referencedHistoryIDs(raw)
	trail := conversationTrailMetadata{
		Source:              "reference",
		ReferenceHistoryIDs: referenceHistoryIDs,
	}
	parentID := firstExistingHistoryID(referenceHistoryIDs, histories, target)
	if parentID == "" && len(referenceHistoryIDs) == 0 {
		// Keep legacy clients that send only <cite_message> usable. New clients
		// always send the source ID and must not silently attach to another turn
		// when that ID is stale or belongs to a different conversation.
		parentID = latestReferenceableHistoryID(histories, target)
	}
	trail.ParentHistoryID = parentID
	if parentID != "" {
		for _, history := range histories {
			if history.ID != parentID {
				continue
			}
			parent := conversationTrailMetadataFromExt(history.Ext)
			trail.Depth = parent.Depth + 1
			break
		}
	}
	if trail.Depth > maxConversationTrailDepth {
		trail.Depth = maxConversationTrailDepth
	}
	ext["trail"] = trail
	return marshalChatHistoryExt(ext)
}

func marshalChatHistoryExt(ext map[string]any) json.RawMessage {
	if len(ext) == 0 {
		return nil
	}
	b, err := json.Marshal(ext)
	if err != nil {
		return nil
	}
	return b
}

// archiveRegeneratedTrafficAttempt keeps the minimal traffic fields for the
// answer that a regeneration replaces. The visible history row remains the
// latest answer, while statistics can still count every generated answer.
func archiveRegeneratedTrafficAttempt(historyExt json.RawMessage, target chatPersistTarget) json.RawMessage {
	if !target.IsRegeneration || target.Existing == nil || strings.TrimSpace(target.Existing.AlgorithmID) == "" {
		return historyExt
	}

	ext := map[string]any{}
	if len(historyExt) > 0 {
		_ = json.Unmarshal(historyExt, &ext)
	}
	var previous struct {
		Attempts []routerTrafficAttempt `json:"router_traffic_attempts"`
	}
	if json.Unmarshal(target.Existing.Ext, &previous) == nil && len(previous.Attempts) > 0 {
		ext[routerTrafficAttemptsExtKey] = previous.Attempts
	}
	attempts, _ := ext[routerTrafficAttemptsExtKey].([]routerTrafficAttempt)
	attempts = append(attempts, routerTrafficAttempt{
		AlgorithmID: strings.TrimSpace(target.Existing.AlgorithmID),
		FeedBack:    target.Existing.FeedBack,
		Reason:      strings.TrimSpace(target.Existing.Reason),
		CreateTime:  target.Existing.CreateTime.UTC(),
	})
	ext[routerTrafficAttemptsExtKey] = attempts
	return marshalChatHistoryExt(ext)
}

func hasExplicitConversationReference(raw map[string]any) bool {
	if len(referencedHistoryIDs(raw)) > 0 {
		return true
	}
	if value, ok := raw["query"].(string); ok && strings.Contains(strings.ToLower(value), "<cite_message>") {
		return true
	}
	if value, ok := raw["content"].(string); ok && strings.Contains(strings.ToLower(value), "<cite_message>") {
		return true
	}
	input, _ := raw["input"].([]any)
	for _, item := range input {
		entry, _ := item.(map[string]any)
		text, _ := entry["text"].(string)
		if strings.Contains(strings.ToLower(text), "<cite_message>") {
			return true
		}
	}
	return false
}

func referencedHistoryIDs(raw map[string]any) []string {
	value, ok := raw["cite_history_ids"]
	if !ok {
		return nil
	}
	var values []string
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if id, ok := item.(string); ok && strings.TrimSpace(id) != "" {
				values = append(values, strings.TrimSpace(id))
			}
		}
	case []string:
		for _, id := range typed {
			if strings.TrimSpace(id) != "" {
				values = append(values, strings.TrimSpace(id))
			}
		}
	}
	return uniqueStrings(values)
}

func firstExistingHistoryID(ids []string, histories []orm.ChatHistory, target chatPersistTarget) string {
	allowed := make(map[string]struct{}, len(histories))
	for _, history := range histories {
		if target.IsRegeneration && target.Existing != nil && history.ID == target.Existing.ID {
			continue
		}
		allowed[history.ID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := allowed[id]; ok {
			return id
		}
	}
	return ""
}

func latestReferenceableHistoryID(histories []orm.ChatHistory, target chatPersistTarget) string {
	for index := len(histories) - 1; index >= 0; index-- {
		if target.IsRegeneration && target.Existing != nil && histories[index].ID == target.Existing.ID {
			continue
		}
		return histories[index].ID
	}
	return ""
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func chatHistoryInput(raw map[string]any, query string) any {
	in, hasInput := raw["input"].([]any)
	if displayQuery, ok := raw["display_query"].(string); ok && strings.TrimSpace(displayQuery) != "" {
		// Keep multimodal attachments while replacing the text payload with the
		// user-visible display_query. Dropping images here breaks later plugin
		// steps that recover uploads from chat_histories.ext.
		out := []any{map[string]any{"input_type": "text", "text": strings.TrimSpace(displayQuery)}}
		if hasInput {
			for _, item := range in {
				entry, ok := item.(map[string]any)
				if !ok {
					continue
				}
				typ, _ := entry["input_type"].(string)
				typ = strings.ToLower(strings.TrimSpace(typ))
				if typ == "image" || typ == "file" {
					out = append(out, entry)
				}
			}
		}
		return out
	}
	if hasInput && len(in) > 0 {
		return in
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	return []any{map[string]any{"input_type": "text", "text": query}}
}

func checkSearchConfig(raw map[string]any) bool {
	conv, _ := raw["conversation"].(map[string]any)
	if conv == nil {
		return true
	}
	sc, _ := conv["search_config"].(map[string]any)
	if sc == nil {
		return true
	}
	if topK, ok := sc["top_k"].(float64); ok && (topK < 1 || topK > maxTopK) {
		return false
	}
	if conf, ok := sc["confidence"].(float64); ok && (conf < 0 || conf > 1) {
		return false
	}
	return true
}

func upstreamSessionID(convID string) string {
	return fmt.Sprintf("%s_%d", convID, time.Now().UnixMilli())
}

// filePathsForUpstreamChat merges top-level `files` with local filesystem paths taken from
// `input` items whose input_type is `image` or `file` and `uri` is set. HTTP(S) URIs are
// skipped because the algorithm chat service only accepts on-disk paths under MOUNT_BASE_DIR.
func filePathsForUpstreamChat(raw map[string]any) any {
	seen := make(map[string]struct{})
	out := make([]any, 0, 4)

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		lower := strings.ToLower(s)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			return
		}
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}

	if v, ok := raw["files"]; ok && v != nil {
		switch xs := v.(type) {
		case []any:
			for _, it := range xs {
				if s, ok := it.(string); ok {
					add(s)
				}
			}
		case []string:
			for _, s := range xs {
				add(s)
			}
		}
	}

	in, ok := raw["input"].([]any)
	if ok {
		for _, it := range in {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["input_type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			if typ != "image" && typ != "file" {
				continue
			}
			uri, _ := m["uri"].(string)
			add(uri)
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// filesPerTurnMap builds a map of turn -> []filePath from historical chat_histories
// plus the current turn's uploads. Format: {"current": [...], "<seq>": [...]}.
// Python uses this both for per-turn file context and to reconstruct the merged file list.

// filesPerTurnMap builds a map of seq -> []filePath from historical chat_histories,
// plus an entry for the current turn (seq=0) from the raw input.
// This is passed to Python as history_files_per_turn so it can rebuild per-turn file context.
func filesPerTurnMap(histories []orm.ChatHistory, currentFiles any, currentSeq int) map[string][]string {
	out := make(map[string][]string)
	// Current turn files keyed by actual seq number.
	var currentPaths []string
	switch xs := currentFiles.(type) {
	case []any:
		for _, it := range xs {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				currentPaths = append(currentPaths, strings.TrimSpace(s))
			}
		}
	case []string:
		for _, s := range xs {
			if strings.TrimSpace(s) != "" {
				currentPaths = append(currentPaths, strings.TrimSpace(s))
			}
		}
	}
	if len(currentPaths) > 0 {
		out[fmt.Sprintf("%d", currentSeq)] = currentPaths
	}
	// Historical turns keyed by seq.
	for _, h := range histories {
		if len(h.Ext) == 0 {
			continue
		}
		var ext struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(h.Ext, &ext); err != nil {
			continue
		}
		seqKey := fmt.Sprintf("%d", h.Seq)
		for _, item := range ext.Input {
			typ, _ := item["input_type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			if typ != "image" && typ != "file" {
				continue
			}
			uri, _ := item["uri"].(string)
			uri = strings.TrimSpace(uri)
			if uri == "" {
				continue
			}
			out[seqKey] = append(out[seqKey], uri)
		}
	}
	return out
}

func resolveMailDraftConfirmID(raw map[string]any) string {
	if draftID, ok := raw["mail_draft_confirm_id"].(string); ok {
		return strings.TrimSpace(draftID)
	}
	return ""
}

func buildChatRequestBody(ctx context.Context, db *gorm.DB, convID, sessionID, query string, histories []orm.ChatHistory, raw map[string]any, resourceContext *evolution.ChatResourceContext, userID string, currentSeq int) map[string]any {
	if strings.TrimSpace(sessionID) == "" {
		sessionID = upstreamSessionID(convID)
	}
	useMemory := resolveUseMemory(raw, resourceContext)
	mode := "auto"
	if m, ok := raw["mode"].(string); ok && strings.TrimSpace(m) != "" {
		if m = strings.TrimSpace(m); m == "auto" || m == "manual" {
			mode = m
		}
	}
	currentFilePaths := filePathsForUpstreamChat(raw)
	filesMap := filesPerTurnMap(histories, currentFilePaths, currentSeq)
	modelCtx := loadModelContext(ctx, db, convID)
	body := map[string]any{
		"query":            query,
		"user_query":       query,
		"session_id":       sessionID,
		"conversation_id":  convID,
		"history":          buildModelHistoryMessages(histories, askAnswersStructuredFromRaw(raw), modelCtx),
		"filters":          raw["filters"],
		"files":            filesMap,
		"current_turn_seq": currentSeq,
		"databases":        raw["databases"],
		"debug":            raw["debug"],
		"reasoning":        resolveReasoning(raw),
		"thinking_depth":   resolveThinkingDepth(ctx, db, convID, userID, raw),
		"priority":         raw["priority"],
		"use_memory":       useMemory,
		"user_id":          strings.TrimSpace(userID),
		"mode":             mode,
		"intent_context":   loadConversationIntentContext(ctx, db, convID),
	}
	if modelCtx != nil {
		body["model_context"] = map[string]any{
			"summary_text":        modelCtx.SummaryText,
			"covered_through_seq": modelCtx.CoveredThroughSeq,
			"version":             modelCtx.Version,
		}
	}
	requestDisabledTools := stringSliceFromAny(raw["disabled_tools"])
	if len(requestDisabledTools) > 0 {
		body["disabled_tools"] = requestDisabledTools
	}
	if skip, ok := raw["skip_sensitive_filter"].(bool); ok && skip {
		body["skip_sensitive_filter"] = true
	}
	if confirmID := resolveMailDraftConfirmID(raw); confirmID != "" {
		body["mail_draft_confirm_id"] = confirmID
	}
	if mentionContext := buildMentionResourceContext(ctx, db, userID, histories, raw); mentionContext != "" {
		body["query"] = mentionContext + "\n\nUser query:\n" + query
	}
	if environmentContext, ok := raw["environment_context"].(map[string]any); ok {
		body["environment_context"] = environmentContext
	}
	// Propagate workflow_context so Python ChatAgent receives the active session info.
	// Merge workflow_ui_state (focused_tab, focused_sort_order) from the request body.
	// Python reads artifact state directly from the DB via _build_session_artifact_section.
	if pc, ok := raw["workflow_context"].(map[string]any); ok && len(pc) > 0 {
		mergedPC := make(map[string]any, len(pc)+4)
		for k, v := range pc {
			mergedPC[k] = v
		}
		if uis, ok := raw["workflow_ui_state"].(map[string]any); ok {
			if ft, ok := uis["focused_tab"]; ok {
				mergedPC["focused_tab"] = ft
			}
			if fso, ok := uis["focused_sort_order"]; ok {
				mergedPC["focused_sort_order"] = fso
			}
		}
		body["workflow_context"] = mergedPC
	}
	if resourceContext != nil {
		body["disabled_tools"] = mergeDisabledToolNames(
			requestDisabledTools, resourceContext.DisabledTools,
		)
		body["available_skills"] = resourceContext.AvailableSkills
	}
	if body["filters"] == nil {
		conv, _ := raw["conversation"].(map[string]any)
		if conv != nil {
			if sc, _ := conv["search_config"].(map[string]any); sc != nil {
				body["filters"] = filtersFromSearchConfig(sc)
			}
		}
	}
	// Internal/auto-advance requests omit conversation.search_config; fall back to the
	// persisted conversation row so kb_id scope matches the user's original selection.
	if body["filters"] == nil && db != nil && convID != "" {
		var c orm.Conversation
		if err := db.WithContext(ctx).Select("search_config").Where("id = ?", convID).First(&c).Error; err == nil && len(c.SearchConfig) > 0 {
			var sc map[string]any
			if json.Unmarshal(c.SearchConfig, &sc) == nil {
				body["filters"] = filtersFromSearchConfig(sc)
			}
		}
	}
	applyDocumentContextFilter(body, raw)
	return body
}

// applyDocumentContextFilter scopes preview chat retrieval to the document the
// user is viewing. The context remains hidden from the visible user message,
// while doc_id is forwarded as a normal retrieval filter.
func applyDocumentContextFilter(body, raw map[string]any) {
	context, _ := raw["document_context"].(map[string]any)
	documentID, _ := context["document_id"].(string)
	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		return
	}

	filters, _ := body["filters"].(map[string]any)
	if filters == nil {
		filters = map[string]any{}
		body["filters"] = filters
	}
	if len(stringSlice(filters["doc_id"])) == 0 {
		filters["doc_id"] = []string{documentID}
	}
}

func promoteAgentRuntimeFlags(raw, body map[string]any) {
	agentConfig, _ := body["agentic_config"].(map[string]any)
	for _, key := range []string{"enable_workflow", "enable_subagent"} {
		if value, ok := raw[key].(bool); ok {
			body[key] = value
			continue
		}
		if value, ok := agentConfig[key].(bool); ok {
			body[key] = value
		}
	}
}

// filtersFromSearchConfig builds upstream dataset filters from a search_config dict.
func filtersFromSearchConfig(sc map[string]any) map[string]any {
	if sc == nil {
		return nil
	}
	filters := map[string]any{}
	if kbIDs := datasetIDsFromSearchConfig(sc); len(kbIDs) > 0 {
		filters["kb_id"] = kbIDs
	}
	if creators := stringSliceFromAny(sc["creators"]); len(creators) > 0 {
		filters["creator"] = creators
	}
	if tags := stringSliceFromAny(sc["tags"]); len(tags) > 0 {
		filters["tags"] = tags
	}
	if len(filters) == 0 {
		return nil
	}
	return filters
}

// resolveWorkflowMode determines the effective workflow_mode for this request.
// Priority: request body > "dynamic" default.
// Valid values: "auto", "dynamic". Anything else is normalised to "dynamic".
func resolveWorkflowMode(raw map[string]any) string {
	if v, ok := raw["workflow_mode"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "auto" || v == "dynamic" {
			return v
		}
	}
	return "dynamic"
}

// resolveWorkflowModeWithFallback determines the effective workflow_mode with full priority chain:
//
//	request body > DB-resolved agentic_config (loaded via applyChatRuntimeConfigs) > "dynamic"
//
// reqBody must have already been populated by applyChatRuntimeConfigs so that
// reqBody["agentic_config"]["workflow_mode"] reflects the DB value.
func resolveWorkflowModeWithFallback(raw map[string]any, reqBody map[string]any) string {
	// Highest priority: explicit value in the original request body.
	if v, ok := raw["workflow_mode"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "auto" || v == "dynamic" {
			return v
		}
	}
	return workflowModeFromReqBody(reqBody)
}

// workflowModeFromReqBody reads the resolved workflow_mode from a fully-built chat request body.
// Priority: workflow_context > agentic_config > "dynamic".
// Used when persisting workflow_step task params so OnSubAgentDone can branch on auto vs dynamic.
func workflowModeFromReqBody(reqBody map[string]any) string {
	if pc, ok := reqBody["workflow_context"].(map[string]any); ok {
		if v, ok := pc["workflow_mode"].(string); ok {
			v = strings.TrimSpace(v)
			if v == "auto" || v == "dynamic" {
				return v
			}
		}
	}
	if ac, ok := reqBody["agentic_config"].(map[string]any); ok {
		if v, ok := ac["workflow_mode"].(string); ok {
			v = strings.TrimSpace(v)
			if v == "auto" || v == "dynamic" {
				return v
			}
		}
	}
	return "dynamic"
}

func resolveUseMemory(raw map[string]any, resourceContext *evolution.ChatResourceContext) bool {
	enabled := true
	if resourceContext != nil {
		enabled = resourceContext.UsePersonalization
	}
	if value, ok := raw["use_memory"].(bool); ok {
		return value && enabled
	}
	return enabled
}

func resolveReasoning(raw map[string]any) bool {
	if value, ok := raw["reasoning"].(bool); ok {
		return value
	}
	return true
}

func resolveThinkingDepth(ctx context.Context, db *gorm.DB, convID, userID string, raw map[string]any) string {
	if value, ok := raw["thinking_depth"].(string); ok {
		if depth, valid := normalizeThinkingDepth(value); valid {
			return depth
		}
	}
	if db != nil && strings.TrimSpace(convID) != "" {
		var snapshot struct {
			ThinkingDepth string `gorm:"column:thinking_depth"`
		}
		query := db.WithContext(ctx).Model(&orm.Conversation{}).
			Select("thinking_depth").Where("id = ?", strings.TrimSpace(convID))
		if strings.TrimSpace(userID) != "" {
			query = query.Where("create_user_id = ?", strings.TrimSpace(userID))
		}
		if err := query.Take(&snapshot).Error; err == nil {
			if depth, valid := normalizeThinkingDepth(snapshot.ThinkingDepth); valid {
				return depth
			}
		}
	}
	runInBackground, _ := raw["run_in_background"].(bool)
	if db != nil {
		if entry, err := entryDefaultsForRequest(ctx, db, userID, runInBackground); err == nil {
			return entry.ThinkingDepth
		}
	}
	if runInBackground {
		return "high"
	}
	return "medium"
}

func datasetIDsFromSearchConfig(sc map[string]any) []string {
	if ids := stringSliceFromAny(sc["dataset_ids"]); len(ids) > 0 {
		return ids
	}

	rawList, _ := sc["dataset_list"].([]any)
	if len(rawList) == 0 {
		return nil
	}

	ids := make([]string, 0, len(rawList))
	for _, item := range rawList {
		selector, _ := item.(map[string]any)
		if selector == nil {
			continue
		}
		id, _ := selector["id"].(string)
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func stringSliceFromAny(v any) []string {
	raw, _ := v.([]any)
	if len(raw) == 0 {
		return nil
	}

	result := make([]string, 0, len(raw))
	for _, item := range raw {
		s, _ := item.(string)
		if strings.TrimSpace(s) != "" {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func handleNonStreamChat(
	w http.ResponseWriter,
	reqCtx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	baseURL string,
	reqBody map[string]any,
	convID, query string,
	target chatPersistTarget,
	historyExt json.RawMessage,
) {
	historyExt = archiveRegeneratedTrafficAttempt(historyExt, target)
	runID := newID("run_")
	reqBody["run_id"] = runID
	chunks, _, err := StreamChatUpstream(reqCtx, baseURL, reqBody)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "chat service unavailable", err), http.StatusBadGateway)
		return
	}
	var fullText, pendingThink, rawAnswer string
	var toolCallTurns int
	var sources []any
	var runEvent *ChatRuntimeEvent
	var runTerminal *RunTerminal
	for chunk := range chunks {
		if chunk.Err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "chat service unavailable", chunk.Err), http.StatusBadGateway)
			return
		}
		if chunk.RuntimeEvent != nil {
			if chunk.RuntimeEvent.Type == RuntimeEventRunFinished {
				runEvent = chunk.RuntimeEvent
				runTerminal, _ = runEvent.Terminal()
			}
			continue
		}
		if next := nonNegativeToolCallTurns(chunk.ToolCallTurns); next > toolCallTurns {
			toolCallTurns = next
		}
		if chunk.ReasoningText != "" {
			pendingThink += chunk.ReasoningText
			continue
		}
		if pendingThink != "" {
			rawAnswer += "<think>" + pendingThink + "</think>"
			pendingThink = ""
		}
		fullText += chunk.Text
		rawAnswer += chunk.Text
		if len(chunk.Sources) > 0 {
			sources = chunk.Sources
		}
	}
	if pendingThink != "" {
		rawAnswer += "<think>" + pendingThink + "</think>"
	}
	if runTerminal == nil {
		common.ReplyErr(w, "chat service returned no run terminal", http.StatusBadGateway)
		return
	}
	rawAnswer = strings.TrimSpace(rawAnswer)
	answer := strings.TrimSpace(stripToolTags(fullText))
	if answer == "" {
		common.ReplyErr(w, "chat service returned no answer", http.StatusBadGateway)
		return
	}
	historyID := target.HistoryID
	if historyID == "" {
		historyID = newID("h_")
	}
	now := time.Now()
	retrievalResult := marshalRetrievalResult(sources)
	hist := orm.ChatHistory{
		ID:              historyID,
		Seq:             target.Seq,
		ConversationID:  convID,
		RawContent:      query,
		RetrievalResult: retrievalResult,
		Content:         query,
		Result:          rawAnswer,
		RunID:           runID,
		RunStatus:       runTerminal.Status,
		RunTerminal:     terminalJSON(runTerminal),
		ToolCallTurns:   toolCallTurns,
		FeedBack:        0,
		Reason:          "",
		ExpectedAnswer:  "",
		Ext:             historyExt,
		TimeMixin:       orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if target.IsRegeneration && target.Existing != nil {
		if err := db.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(map[string]any{
			"seq":              target.Seq,
			"raw_content":      query,
			"content":          query,
			"result":           rawAnswer,
			"run_id":           runID,
			"run_status":       runTerminal.Status,
			"run_terminal":     terminalJSON(runTerminal),
			"tool_call_turns":  toolCallTurns,
			"retrieval_result": retrievalResult,
			"feed_back":        0,
			"reason":           "",
			"expected_answer":  "",
			"ext":              historyExt,
			"create_time":      now,
			"update_time":      now,
		}).Error; err != nil {
			common.ReplyErr(w, "failed to update history", http.StatusInternalServerError)
			return
		}
	} else {
		if err := db.Create(&hist).Error; err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "failed to save history", err), http.StatusInternalServerError)
			return
		}
	}
	if stateStore != nil {
		_ = setChatRuntimeStatus(reqCtx, stateStore, convID, historyID, runTerminal.Status, answer, runID, runTerminal)
	}
	db.Model(&orm.Conversation{}).Where("id = ?", convID).Update("updated_at", now)
	if !target.IsRegeneration {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1))
	}
	recordConversationIdleActivity(context.Background(), db, stateStore, convID, userIDFromChatRequestBody(reqBody), historyID, query, answer, now)
	common.ReplyOK(w, map[string]any{
		"conversation_id": convID,
		"seq":             target.Seq,
		"message":         answer,
		"delta":           "",
		"history_id":      historyID,
		"sources":         sources,
		"runtime_event":   runEvent,
	})
}

func handleStreamChat(
	w http.ResponseWriter,
	r *http.Request,
	db *gorm.DB,
	stateStore state.Store,
	baseURL string,
	reqBody map[string]any,
	convID, query string,
	target chatPersistTarget,
	dualReply bool,
	historyExt json.RawMessage,
) {
	reqCtx := r.Context()
	flusher, ok := w.(http.Flusher)
	if !ok {
		common.ReplyErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	historyID := target.HistoryID
	if historyID == "" {
		historyID = newID("h_")
	}
	secondaryHistoryID := ""
	primaryRunID, _ := reqBody["_external_run_id"].(string)
	if primaryRunID == "" {
		if requestKey, _ := reqBody["_external_request_key"].(string); requestKey != "" {
			primaryRunID, _ = externalChatIdentity(requestKey)
		}
	}
	if primaryRunID == "" {
		primaryRunID = newID("run_")
	}
	reqBody["run_id"] = primaryRunID
	secondaryRunID := ""
	if dualReply {
		secondaryHistoryID = newID("h_")
		secondaryRunID = newID("run_")
		reqBody["secondary_run_id"] = secondaryRunID
	}
	chatCtx, chatCancel := context.WithCancel(context.Background())
	defer chatCancel()
	if stateStore != nil {
		if target.IsRegeneration {
			_ = clearChatData(chatCtx, stateStore, convID, historyID)
		}
		_ = setChatInput(chatCtx, stateStore, convID, historyID, query, target.Seq, historyExt)
		_ = setChatRuntimeStatus(chatCtx, stateStore, convID, historyID, "generating", "", primaryRunID, nil)
		if dualReply {
			_ = setChatInput(chatCtx, stateStore, convID, secondaryHistoryID, query, target.Seq, historyExt)
			_ = setChatRuntimeStatus(chatCtx, stateStore, convID, secondaryHistoryID, "generating", "", secondaryRunID, nil)
			_ = setMultiAnswerInfo(chatCtx, stateStore, convID, historyID, secondaryHistoryID, target.Seq)
		}
		go cancelChatOnStop(chatCtx, stateStore, convID, historyID, chatCancel)
	}

	if !dualReply {
		streamSingleAnswer(chatCtx, reqCtx, w, flusher, db, stateStore, baseURL, reqBody, convID, query, historyID, target, historyExt)
		return
	}
	streamDualAnswer(chatCtx, reqCtx, w, flusher, db, stateStore, baseURL, reqBody, convID, query, historyID, secondaryHistoryID, target, historyExt)
}

func elapsedThinkingSeconds(elapsed time.Duration) int64 {
	if elapsed <= 0 {
		return 1
	}
	return int64((elapsed + time.Second - 1) / time.Second)
}

type runtimeChunkDecision struct {
	Event    *ChatRuntimeEvent
	Terminal *RunTerminal
	Stop     bool
}

// consumeRuntimeChunk centralizes the ordering contract shared by single,
// dual, and disconnected drain consumers: an error always wins over any
// runtime event carried by the same chunk.
func consumeRuntimeChunk(chunk UpstreamStreamChunk, runID string, partialOutput bool) (runtimeChunkDecision, bool) {
	if chunk.Err != nil {
		event := failedRunEvent(runID, "upstream_stream_failed", partialOutput)
		terminal, _ := event.Terminal()
		return runtimeChunkDecision{Event: event, Terminal: terminal, Stop: true}, true
	}
	if chunk.RuntimeEvent == nil {
		return runtimeChunkDecision{}, false
	}
	decision := runtimeChunkDecision{Event: chunk.RuntimeEvent}
	if chunk.RuntimeEvent.Type == RuntimeEventRunFinished {
		terminal, err := chunk.RuntimeEvent.Terminal()
		if err != nil {
			event := failedRunEvent(runID, "invalid_run_terminal", partialOutput)
			terminal, _ = event.Terminal()
			return runtimeChunkDecision{Event: event, Terminal: terminal, Stop: true}, true
		}
		decision.Terminal = terminal
	}
	return decision, true
}

func publishRuntimeChunk(
	reqCtx, storeCtx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	stateStore state.Store,
	convID, historyID string,
	seq int,
	event *ChatRuntimeEvent,
	writeClient bool,
) {
	chunk := &ChatChunkResponse{
		ConversationID: convID,
		Seq:            int32(seq),
		HistoryID:      historyID,
		RuntimeEvent:   event,
	}
	if writeClient && reqCtx.Err() == nil {
		writeSSEChunk(w, flusher, chunk)
	}
	if stateStore != nil {
		_ = appendChatChunk(storeCtx, stateStore, convID, historyID, chunk)
	}
}

func streamSingleAnswer(
	chatCtx, reqCtx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	db *gorm.DB,
	stateStore state.Store,
	baseURL string,
	reqBody map[string]any,
	convID, query, historyID string,
	target chatPersistTarget,
	historyExt json.RawMessage,
) {
	historyExt = archiveRegeneratedTrafficAttempt(historyExt, target)
	seq := target.Seq
	runID, _ := reqBody["run_id"].(string)
	ch, algorithmID, err := streamChatOutput(
		chatCtx, db, baseURL, reqBody, convID, historyID, query,
		target.Seq, historyExt, target.IsRegeneration,
	)
	if err != nil {
		runEvent := failedRunEvent(runID, "upstream_request_failed", false)
		terminal, _ := runEvent.Terminal()
		persistImmediateRunTerminal(db, convID, historyID, query, runID, target, historyExt, terminal)
		if stateStore != nil {
			_ = setChatRuntimeStatus(chatCtx, stateStore, convID, historyID, terminal.Status, "", runID, terminal)
		}
		writeSSEChunk(w, flusher, &ChatChunkResponse{
			ConversationID: convID,
			Seq:            int32(seq),
			HistoryID:      historyID,
			RuntimeEvent:   runEvent,
		})
		return
	}
	var fullText string
	var pendingThink string
	var fullResult string
	var toolCallTurns int
	var sources []any
	var pendingAskPending any
	var pendingConversationIntent *IntentUpdatedEvent
	thinkStart := time.Now()
	var thinkingDurationS int64
	var thinkingActive bool
	var sawToolResultPreview bool
	var runTerminal *RunTerminal
	progressRowCreated := target.IsRegeneration && target.Existing != nil
	persistThinkingProgress := func() {
		partialResult := fullResult
		if pendingThink != "" {
			partialResult += "<think>" + pendingThink + "</think>"
		}
		values := map[string]any{
			"algorithm_id":        algorithmID,
			"seq":                 seq,
			"raw_content":         query,
			"content":             query,
			"result":              partialResult,
			"thinking_duration_s": thinkingDurationS,
			"ext":                 historyExt,
			"run_id":              runID,
			"update_time":         time.Now(),
		}
		if progressRowCreated {
			if err := db.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(values).Error; err != nil {
				log.Logger.Warn().Err(err).Str("history_id", historyID).Msg("failed to persist thinking progress")
			}
			return
		}
		now := time.Now()
		if err := db.Create(&orm.ChatHistory{
			ID: historyID, Seq: seq, ConversationID: convID, RawContent: query, Content: query, AlgorithmID: algorithmID,
			Result: partialResult, ThinkingDurationS: thinkingDurationS, RunID: runID, Ext: historyExt,
			TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
		}).Error; err != nil {
			log.Logger.Warn().Err(err).Str("history_id", historyID).Msg("failed to create thinking progress")
			return
		}
		progressRowCreated = true
	}
	writeSSEChunk(w, flusher, &ChatChunkResponse{
		ConversationID:    convID,
		Seq:               int32(seq),
		Message:           "",
		Delta:             "",
		HistoryID:         historyID,
		Sources:           nil,
		PromptQuestions:   []string{},
		ReasoningContent:  "",
		ThinkingDurationS: 0,
	})
	for d := range ch {
		decision, handled := consumeRuntimeChunk(d, runID, fullResult != "" || pendingThink != "")
		if handled {
			if decision.Terminal != nil {
				runTerminal = decision.Terminal
			}
			publishRuntimeChunk(reqCtx, chatCtx, w, flusher, stateStore, convID, historyID, seq, decision.Event, true)
			if decision.Stop {
				break
			}
			continue
		}
		if d.ArtifactCreated != nil {
			persistAndPublishConversationArtifact(
				chatCtx, reqCtx, w, flusher, db, stateStore, reqBody,
				convID, historyID, seq, d.ArtifactCreated,
			)
			continue
		}
		if d.TaskCreated != nil {
			userIDForTask, _ := reqBody["user_id"].(string)
			workflowModeForTask := workflowModeFromReqBody(reqBody)
			notice, taskErr := handleTaskCreated(chatCtx, db, stateStore, convID, historyID, userIDForTask, d.TaskCreated, llmConfigFromBody(reqBody), toolConfigFromBody(reqBody), workflowModeForTask)
			if taskErr != nil {
				failurePrefix := "TASK_START_FAILED: "
				if d.TaskCreated.AgentType == "workflow_step" {
					failurePrefix = "WORKFLOW_START_FAILED: "
				}
				failure := failurePrefix + taskErr.Error()
				fullText += failure
				fullResult += failure
				if reqCtx.Err() == nil {
					writeSSEChunk(w, flusher, &ChatChunkResponse{
						ConversationID: convID,
						Seq:            int32(seq),
						HistoryID:      historyID,
						Delta:          failure,
					})
				}
				continue
			}
			if notice != nil {
				taskChunk := &ChatChunkResponse{
					ConversationID: convID,
					Seq:            int32(seq),
					HistoryID:      historyID,
					TaskCreated:    notice,
				}
				if reqCtx.Err() == nil {
					writeSSEChunk(w, flusher, taskChunk)
				}
				if stateStore != nil {
					_ = appendChatChunk(chatCtx, stateStore, convID, historyID, taskChunk)
					// Also write to the conversation-level events channel so the frontend
					// receives task_created notifications regardless of which history stream
					// is currently open (covers auto-advance internal requests).
					_ = AppendConvEvent(chatCtx, stateStore, convID, &ConvEvent{
						Type:    "task_created",
						Payload: notice,
					})
				}
			}
			continue
		}
		if d.AskPending != nil {
			pendingAskPending = d.AskPending
			askChunk := &ChatChunkResponse{
				ConversationID: convID,
				Seq:            int32(seq),
				HistoryID:      historyID,
				AskPending:     d.AskPending,
			}
			if reqCtx.Err() == nil {
				writeSSEChunk(w, flusher, askChunk)
			}
			if stateStore != nil {
				_ = appendChatChunk(chatCtx, stateStore, convID, historyID, askChunk)
				_ = AppendConvEvent(chatCtx, stateStore, convID, &ConvEvent{
					Type:    "ask_pending",
					Payload: d.AskPending,
				})
			}
			continue
		}
		if d.ToolLimitPending != nil {
			limitChunk := &ChatChunkResponse{
				ConversationID:   convID,
				Seq:              int32(seq),
				HistoryID:        historyID,
				ToolLimitPending: d.ToolLimitPending,
			}
			if reqCtx.Err() == nil {
				writeSSEChunk(w, flusher, limitChunk)
			}
			if stateStore != nil {
				_ = appendChatChunk(chatCtx, stateStore, convID, historyID, limitChunk)
			}
			continue
		}
		if d.IntentUpdated != nil {
			updated := handleIntentUpdated(chatCtx, db, stateStore, convID, d.IntentUpdated)
			if updated != nil {
				pendingConversationIntent = updated
				intentChunk := &ChatChunkResponse{ConversationID: convID, Seq: int32(seq), HistoryID: historyID, IntentUpdated: updated}
				if reqCtx.Err() == nil {
					writeSSEChunk(w, flusher, intentChunk)
				}
				if stateStore != nil {
					_ = appendChatChunk(chatCtx, stateStore, convID, historyID, intentChunk)
				}
			}
			continue
		}
		if d.WorkflowPreflightUpdated != nil {
			handleWorkflowPreflightUpdated(chatCtx, db, convID, d.WorkflowPreflightUpdated)
			continue
		}
		if d.ModelContextUpdated != nil {
			handleModelContextUpdated(chatCtx, db, convID, d.ModelContextUpdated)
			continue
		}
		if d.Heartbeat {
			if thinkingActive || d.Execution != nil {
				nextDuration := elapsedThinkingSeconds(time.Since(thinkStart))
				if nextDuration != thinkingDurationS || d.Execution != nil {
					thinkingDurationS = nextDuration
					if thinkingActive {
						persistThinkingProgress()
					}
					thinkingChunk := &ChatChunkResponse{
						ConversationID: convID, Seq: int32(seq), HistoryID: historyID,
						ThinkingDurationS: thinkingDurationS,
						Execution:         d.Execution,
					}
					if reqCtx.Err() == nil {
						writeSSEChunk(w, flusher, thinkingChunk)
					}
					if stateStore != nil {
						_ = appendChatChunk(chatCtx, stateStore, convID, historyID, thinkingChunk)
					}
				}
			}
			continue
		}
		if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > toolCallTurns {
			toolCallTurns = next
		}
		if d.ReasoningText != "" {
			thinkingActive = true
			pendingThink += d.ReasoningText
			thinkingDurationS = elapsedThinkingSeconds(time.Since(thinkStart))
			persistThinkingProgress()
			thinkingChunk := &ChatChunkResponse{
				ConversationID: convID, Seq: int32(seq), HistoryID: historyID,
				ThinkingDurationS: thinkingDurationS,
			}
			if reqCtx.Err() == nil {
				writeSSEChunk(w, flusher, thinkingChunk)
			}
			if stateStore != nil {
				_ = appendChatChunk(chatCtx, stateStore, convID, historyID, thinkingChunk)
			}
			continue
		}
		hasToolPreview := strings.Contains(d.Text, "<tp") || strings.Contains(d.Text, "<trp")
		if hasToolPreview {
			thinkingActive = true
			thinkingDurationS = elapsedThinkingSeconds(time.Since(thinkStart))
			if strings.Contains(d.Text, "<trp") {
				sawToolResultPreview = true
			}
		} else if sawToolResultPreview && d.Text != "" {
			thinkingActive = false
		}
		if pendingThink != "" {
			thinkingDurationS = elapsedThinkingSeconds(time.Since(thinkStart))
			fullResult += "<think>" + pendingThink + "</think>"
			pendingThink = ""
		}
		fullText += d.Text
		fullResult += d.Text
		if len(d.Sources) > 0 {
			sources = d.Sources
		}
		deltaToSend := stripToolTags(d.Text)
		if !shouldEmitStreamFrame(deltaToSend, d.Sources) {
			continue
		}
		chunk := &ChatChunkResponse{
			ConversationID:        convID,
			Seq:                   int32(seq),
			Message:               "",
			Delta:                 deltaToSend,
			HistoryID:             historyID,
			Sources:               sources,
			PromptQuestions:       []string{},
			ReasoningContent:      "",
			ThinkingDurationS:     thinkingDurationS,
			ExternalEventSequence: d.ExternalEventSequence,
			Execution:             d.Execution,
		}
		if reqCtx.Err() == nil {
			writeSSEChunk(w, flusher, chunk)
		}
		if stateStore != nil {
			_ = appendChatChunk(chatCtx, stateStore, convID, historyID, chunk)
		}
	}
	if runTerminal == nil {
		var runEvent *ChatRuntimeEvent
		if chatCtx.Err() != nil {
			runEvent = cancelledRunEvent(runID, fullResult != "" || pendingThink != "")
		} else {
			runEvent = failedRunEvent(runID, "missing_run_terminal", fullResult != "" || pendingThink != "")
		}
		runTerminal, _ = runEvent.Terminal()
		runtimeChunk := &ChatChunkResponse{ConversationID: convID, Seq: int32(seq), HistoryID: historyID, RuntimeEvent: runEvent}
		if reqCtx.Err() == nil {
			writeSSEChunk(w, flusher, runtimeChunk)
		}
		if stateStore != nil {
			_ = appendChatChunk(context.Background(), stateStore, convID, historyID, runtimeChunk)
		}
	}
	now := time.Now()
	retrievalResult := marshalRetrievalResult(sources)
	if pendingThink != "" {
		thinkingDurationS = elapsedThinkingSeconds(time.Since(thinkStart))
		fullResult += "<think>" + pendingThink + "</think>"
	}
	// Persist ask_pending into ext so the ask card survives page reload.
	if pendingAskPending != nil {
		historyExt = mergeAskPendingIntoExt(historyExt, pendingAskPending)
	}
	if pendingConversationIntent != nil {
		historyExt = mergeIntentUpdatedIntoExt(historyExt, pendingConversationIntent)
	}
	persisted := false
	externalFinalized := strings.HasPrefix(algorithmID, "external:")
	if externalFinalized {
		var persistedHistory orm.ChatHistory
		persisted = db.Where("id = ? AND conversation_id = ?", historyID, convID).Take(&persistedHistory).Error == nil
	}
	if !externalFinalized && target.IsRegeneration && target.Existing != nil {
		if err := db.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(map[string]any{
			"algorithm_id":        algorithmID,
			"seq":                 seq,
			"raw_content":         query,
			"content":             query,
			"result":              fullResult,
			"tool_call_turns":     toolCallTurns,
			"thinking_duration_s": thinkingDurationS,
			"retrieval_result":    retrievalResult,
			"feed_back":           0,
			"reason":              "",
			"expected_answer":     "",
			"ext":                 historyExt,
			"run_id":              runID,
			"run_status":          runTerminal.Status,
			"run_terminal":        terminalJSON(runTerminal),
			"create_time":         now,
			"update_time":         now,
		}).Error; err != nil {
			log.Logger.Warn().Err(err).Str("conversation_id", convID).Str("history_id", historyID).Msg("failed to update stream chat history")
		} else {
			persisted = true
		}
	} else if !externalFinalized && progressRowCreated {
		if err := db.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(map[string]any{
			"algorithm_id": algorithmID, "seq": seq, "raw_content": query, "content": query, "result": fullResult,
			"tool_call_turns": toolCallTurns, "thinking_duration_s": thinkingDurationS,
			"retrieval_result": retrievalResult, "ext": historyExt, "update_time": now,
			"run_id": runID, "run_status": runTerminal.Status, "run_terminal": terminalJSON(runTerminal),
		}).Error; err != nil {
			log.Logger.Warn().Err(err).Str("conversation_id", convID).Str("history_id", historyID).Msg("failed to finalize stream chat history")
		} else {
			persisted = true
		}
	} else if !externalFinalized {
		if err := db.Create(&orm.ChatHistory{
			ID:                historyID,
			Seq:               seq,
			ConversationID:    convID,
			AlgorithmID:       algorithmID,
			RawContent:        query,
			RetrievalResult:   retrievalResult,
			Content:           query,
			Result:            fullResult,
			ToolCallTurns:     toolCallTurns,
			ThinkingDurationS: thinkingDurationS,
			RunID:             runID,
			RunStatus:         runTerminal.Status,
			RunTerminal:       terminalJSON(runTerminal),
			Ext:               historyExt,
			TimeMixin:         orm.TimeMixin{CreateTime: now, UpdateTime: now},
		}).Error; err != nil {
			log.Logger.Warn().Err(err).Str("conversation_id", convID).Str("history_id", historyID).Msg("failed to save stream chat history")
		} else {
			persisted = true
		}
	}
	finalStatus := runTerminal.Status
	if stateStore != nil {
		_ = setChatRuntimeStatus(context.Background(), stateStore, convID, historyID, finalStatus, stripToolTags(fullText), runID, runTerminal)
	}
	if persisted && !externalFinalized {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).Update("updated_at", now)
		// Reaching this point means the upstream SSE channel has closed and the
		// final history payload was persisted. Intermediate thinking persistence
		// never reaches this update.
		taskStatus := "succeeded"
		if finalStatus == "failed" || finalStatus == "interrupted" {
			taskStatus = "failed"
		} else if finalStatus == "cancelled" {
			taskStatus = "canceled"
		}
		if err := db.Model(&orm.TaskCenterTask{}).
			Where("conversation_id = ? AND task_type = ? AND archived_at IS NULL AND status NOT IN ('succeeded','failed','canceled')", convID, "background_chat").
			Updates(map[string]any{"status": taskStatus, "finished_at": now, "updated_at": now}).Error; err != nil {
			log.Logger.Warn().Err(err).Str("conversation_id", convID).Msg("failed to finish background task after SSE close")
		}
	}
	if persisted && !externalFinalized && !target.IsRegeneration {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1))
	}
	if persisted && !externalFinalized {
		recordConversationIdleActivity(context.Background(), db, stateStore, convID, userIDFromChatRequestBody(reqBody), historyID, query, stripToolTags(fullText), now)
	}
}

func persistImmediateRunTerminal(
	db *gorm.DB,
	convID, historyID, query, runID string,
	target chatPersistTarget,
	historyExt json.RawMessage,
	terminal *RunTerminal,
) {
	if db == nil || terminal == nil {
		return
	}
	now := time.Now()
	values := map[string]any{
		"seq": target.Seq, "raw_content": query, "content": query, "result": "",
		"run_id": runID, "run_status": terminal.Status, "run_terminal": terminalJSON(terminal),
		"ext": historyExt, "update_time": now,
	}
	if target.IsRegeneration && target.Existing != nil {
		values["create_time"] = now
		if err := db.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(values).Error; err != nil {
			log.Logger.Warn().Err(err).Str("history_id", historyID).Msg("failed to persist immediate run terminal")
		}
		return
	}
	history := orm.ChatHistory{
		ID: historyID, Seq: target.Seq, ConversationID: convID, RawContent: query, Content: query,
		RunID: runID, RunStatus: terminal.Status, RunTerminal: terminalJSON(terminal), Ext: historyExt,
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if err := db.Create(&history).Error; err != nil {
		log.Logger.Warn().Err(err).Str("history_id", historyID).Msg("failed to persist immediate run terminal")
	}
}

// persistAndPublishConversationArtifact is the shared main-Agent artifact path
// for single-answer and multi-answer streams. Persist first so every client
// notification refers to an artifact that is already queryable after refresh.
func persistAndPublishConversationArtifact(
	chatCtx, reqCtx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	db *gorm.DB,
	stateStore state.Store,
	reqBody map[string]any,
	convID, historyID string,
	seq int,
	event *ArtifactCreatedEvent,
) {
	userID := userIDFromChatRequestBody(reqBody)
	if userID == "" {
		userID = "0"
	}
	notice, err := persistConversationArtifact(
		chatCtx, db, convID, historyID, userID, event,
	)
	if err != nil {
		log.Logger.Error().Err(err).Str("conversation_id", convID).
			Str("history_id", historyID).Msg("persist main chat artifact failed")
		return
	}
	chunk := &ChatChunkResponse{
		ConversationID:  convID,
		Seq:             int32(seq),
		HistoryID:       historyID,
		ArtifactCreated: notice,
	}
	if reqCtx.Err() == nil {
		writeSSEChunk(w, flusher, chunk)
	}
	if stateStore != nil {
		_ = appendChatChunk(chatCtx, stateStore, convID, historyID, chunk)
		_ = AppendConvEvent(chatCtx, stateStore, convID, &ConvEvent{
			Type: "artifact_created", Payload: notice,
		})
	}
}

func streamDualAnswer(
	chatCtx, reqCtx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	db *gorm.DB,
	stateStore state.Store,
	baseURL string,
	reqBody map[string]any,
	convID, query, historyID, secondaryHistoryID string,
	target chatPersistTarget,
	historyExt json.RawMessage,
) {
	seq := target.Seq
	primaryRunID, _ := reqBody["run_id"].(string)
	secondaryRunID, _ := reqBody["secondary_run_id"].(string)
	primaryCh, _, err1 := StreamChatUpstream(chatCtx, baseURL, reqBody)
	secondaryReq := make(map[string]any)
	for k, v := range reqBody {
		secondaryReq[k] = v
	}
	secondaryReq["run_id"] = secondaryRunID
	delete(secondaryReq, "secondary_run_id")
	if sc, ok := secondaryReq["filters"].(map[string]any); ok {
		sc["kb_id"] = nil
	}
	secondaryCh, _, err2 := StreamChatUpstream(chatCtx, baseURL, secondaryReq)
	if err1 != nil {
		primaryCh = nil
	}
	if err2 != nil {
		secondaryCh = nil
	}
	writeSSEChunk(w, flusher, map[string]any{"conversation_id": convID, "seq": seq, "delta": "", "history_id": historyID})
	writeSSEChunk(w, flusher, map[string]any{"conversation_id": convID, "seq": seq, "delta": "", "history_id": secondaryHistoryID})

	var primaryText, secondaryText string
	var primaryResult, secondaryResult string
	var primarySources, secondarySources []any
	var primaryPendingThink, secondaryPendingThink string
	var primaryToolCallTurns, secondaryToolCallTurns int
	var primaryTerminal, secondaryTerminal *RunTerminal
	thinkStart := time.Now()
	var primaryThinkingDurationS, secondaryThinkingDurationS int64
	primaryProgressCreated, secondaryProgressCreated := false, false
	persistProgress := func(id, result, pending string, duration int64, created *bool) {
		partialResult := result
		if pending != "" {
			partialResult += "<think>" + pending + "</think>"
		}
		if *created {
			_ = db.Model(&orm.MultiAnswersChatHistory{}).Where("id = ?", id).Updates(map[string]any{
				"result": partialResult, "thinking_duration_s": duration, "update_time": time.Now(),
			}).Error
			return
		}
		now := time.Now()
		if err := db.Create(&orm.MultiAnswersChatHistory{
			ID: id, Seq: seq, ConversationID: convID, RawContent: query, Content: query,
			Result: partialResult, ThinkingDurationS: duration, Ext: historyExt,
			TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
		}).Error; err == nil {
			*created = true
		}
	}
	primaryDone := primaryCh == nil
	secondaryDone := secondaryCh == nil
	appendPrimary := func(delta, reasoning string, sources []any) {
		if len(sources) > 0 {
			primarySources = sources
		}
		if reasoning != "" {
			primaryPendingThink += reasoning
			primaryThinkingDurationS = int64(time.Since(thinkStart).Seconds())
			persistProgress(historyID, primaryResult, primaryPendingThink, primaryThinkingDurationS, &primaryProgressCreated)
			if reqCtx.Err() == nil {
				writeSSEChunk(w, flusher, map[string]any{"conversation_id": convID, "seq": seq, "history_id": historyID, "thinking_duration_s": primaryThinkingDurationS})
			}
			return
		}
		if primaryPendingThink != "" {
			primaryResult += "<think>" + primaryPendingThink + "</think>"
			primaryPendingThink = ""
		}
		primaryText += delta
		primaryResult += delta
		delta = stripToolTags(delta)
		if !shouldEmitStreamFrame(delta, sources) {
			return
		}
		if reqCtx.Err() == nil {
			writeSSEChunk(w, flusher, map[string]any{
				"conversation_id": convID, "seq": seq, "delta": delta, "history_id": historyID,
				"sources": sources,
			})
		}
		if stateStore != nil {
			_ = appendChatChunk(chatCtx, stateStore, convID, historyID, &ChatChunkResponse{
				ConversationID: convID, Seq: int32(seq), Delta: delta, HistoryID: historyID,
				ReasoningContent: "", Sources: sources,
			})
		}
	}
	appendSecondary := func(delta, reasoning string, sources []any) {
		if len(sources) > 0 {
			secondarySources = sources
		}
		if reasoning != "" {
			secondaryPendingThink += reasoning
			secondaryThinkingDurationS = int64(time.Since(thinkStart).Seconds())
			persistProgress(secondaryHistoryID, secondaryResult, secondaryPendingThink, secondaryThinkingDurationS, &secondaryProgressCreated)
			if reqCtx.Err() == nil {
				writeSSEChunk(w, flusher, map[string]any{"conversation_id": convID, "seq": seq, "history_id": secondaryHistoryID, "thinking_duration_s": secondaryThinkingDurationS})
			}
			return
		}
		if secondaryPendingThink != "" {
			secondaryResult += "<think>" + secondaryPendingThink + "</think>"
			secondaryPendingThink = ""
		}
		secondaryText += delta
		secondaryResult += delta
		delta = stripToolTags(delta)
		if !shouldEmitStreamFrame(delta, sources) {
			return
		}
		if reqCtx.Err() == nil {
			writeSSEChunk(w, flusher, map[string]any{
				"conversation_id": convID, "seq": seq, "delta": delta, "history_id": secondaryHistoryID,
				"sources": sources,
			})
		}
		if stateStore != nil {
			_ = appendChatChunk(chatCtx, stateStore, convID, secondaryHistoryID, &ChatChunkResponse{
				ConversationID: convID, Seq: int32(seq), Delta: delta, HistoryID: secondaryHistoryID,
				ReasoningContent: "", Sources: sources,
			})
		}
	}
	for !primaryDone || !secondaryDone {
		select {
		case d, ok := <-primaryCh:
			if !ok {
				primaryDone = true
				primaryCh = nil
				continue
			}
			if decision, handled := consumeRuntimeChunk(d, primaryRunID, primaryResult != "" || primaryPendingThink != ""); handled {
				if decision.Terminal != nil {
					primaryTerminal = decision.Terminal
				}
				publishRuntimeChunk(reqCtx, chatCtx, w, flusher, stateStore, convID, historyID, seq, decision.Event, true)
				if decision.Stop {
					primaryDone = true
					primaryCh = nil
				}
				continue
			}
			if d.ArtifactCreated != nil {
				persistAndPublishConversationArtifact(
					chatCtx, reqCtx, w, flusher, db, stateStore, reqBody,
					convID, historyID, seq, d.ArtifactCreated,
				)
				continue
			}
			if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > primaryToolCallTurns {
				primaryToolCallTurns = next
			}
			appendPrimary(d.Text, d.ReasoningText, d.Sources)
		case d, ok := <-secondaryCh:
			if !ok {
				secondaryDone = true
				secondaryCh = nil
				continue
			}
			if decision, handled := consumeRuntimeChunk(d, secondaryRunID, secondaryResult != "" || secondaryPendingThink != ""); handled {
				if decision.Terminal != nil {
					secondaryTerminal = decision.Terminal
				}
				publishRuntimeChunk(reqCtx, chatCtx, w, flusher, stateStore, convID, secondaryHistoryID, seq, decision.Event, true)
				if decision.Stop {
					secondaryDone = true
					secondaryCh = nil
				}
				continue
			}
			if d.ArtifactCreated != nil {
				persistAndPublishConversationArtifact(
					chatCtx, reqCtx, w, flusher, db, stateStore, reqBody,
					convID, secondaryHistoryID, seq, d.ArtifactCreated,
				)
				continue
			}
			if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > secondaryToolCallTurns {
				secondaryToolCallTurns = next
			}
			appendSecondary(d.Text, d.ReasoningText, d.Sources)
		case <-reqCtx.Done():
			bg := context.Background()
			for !primaryDone || !secondaryDone {
				select {
				case d, ok := <-primaryCh:
					if !ok {
						primaryDone = true
						primaryCh = nil
					} else {
						if decision, handled := consumeRuntimeChunk(d, primaryRunID, primaryResult != "" || primaryPendingThink != ""); handled {
							if decision.Terminal != nil {
								primaryTerminal = decision.Terminal
							}
							publishRuntimeChunk(reqCtx, bg, w, flusher, stateStore, convID, historyID, seq, decision.Event, false)
							if decision.Stop {
								primaryDone = true
								primaryCh = nil
							}
							continue
						}
						if len(d.Sources) > 0 {
							primarySources = d.Sources
						}
						if d.ArtifactCreated != nil {
							persistAndPublishConversationArtifact(
								bg, reqCtx, w, flusher, db, stateStore, reqBody,
								convID, historyID, seq, d.ArtifactCreated,
							)
							continue
						}
						if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > primaryToolCallTurns {
							primaryToolCallTurns = next
						}
						if d.ReasoningText != "" {
							primaryPendingThink += d.ReasoningText
							primaryThinkingDurationS = int64(time.Since(thinkStart).Seconds())
							persistProgress(historyID, primaryResult, primaryPendingThink, primaryThinkingDurationS, &primaryProgressCreated)
							continue
						}
						if primaryPendingThink != "" {
							primaryResult += "<think>" + primaryPendingThink + "</think>"
							primaryPendingThink = ""
						}
						primaryText += d.Text
						primaryResult += d.Text
						delta := stripToolTags(d.Text)
						if !shouldEmitStreamFrame(delta, d.Sources) {
							continue
						}
						if stateStore != nil {
							_ = appendChatChunk(bg, stateStore, convID, historyID, &ChatChunkResponse{
								ConversationID: convID, Seq: int32(seq), Delta: delta, HistoryID: historyID,
								ReasoningContent: "", Sources: d.Sources,
							})
						}
					}
				case d, ok := <-secondaryCh:
					if !ok {
						secondaryDone = true
						secondaryCh = nil
					} else {
						if decision, handled := consumeRuntimeChunk(d, secondaryRunID, secondaryResult != "" || secondaryPendingThink != ""); handled {
							if decision.Terminal != nil {
								secondaryTerminal = decision.Terminal
							}
							publishRuntimeChunk(reqCtx, bg, w, flusher, stateStore, convID, secondaryHistoryID, seq, decision.Event, false)
							if decision.Stop {
								secondaryDone = true
								secondaryCh = nil
							}
							continue
						}
						if len(d.Sources) > 0 {
							secondarySources = d.Sources
						}
						if d.ArtifactCreated != nil {
							persistAndPublishConversationArtifact(
								bg, reqCtx, w, flusher, db, stateStore, reqBody,
								convID, secondaryHistoryID, seq, d.ArtifactCreated,
							)
							continue
						}
						if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > secondaryToolCallTurns {
							secondaryToolCallTurns = next
						}
						if d.ReasoningText != "" {
							secondaryPendingThink += d.ReasoningText
							secondaryThinkingDurationS = int64(time.Since(thinkStart).Seconds())
							persistProgress(secondaryHistoryID, secondaryResult, secondaryPendingThink, secondaryThinkingDurationS, &secondaryProgressCreated)
							continue
						}
						if secondaryPendingThink != "" {
							secondaryResult += "<think>" + secondaryPendingThink + "</think>"
							secondaryPendingThink = ""
						}
						secondaryText += d.Text
						secondaryResult += d.Text
						delta := stripToolTags(d.Text)
						if !shouldEmitStreamFrame(delta, d.Sources) {
							continue
						}
						if stateStore != nil {
							_ = appendChatChunk(bg, stateStore, convID, secondaryHistoryID, &ChatChunkResponse{
								ConversationID: convID, Seq: int32(seq), Delta: delta, HistoryID: secondaryHistoryID,
								ReasoningContent: "", Sources: d.Sources,
							})
						}
					}
				}
			}
			goto dualPersist
		}
	}
dualPersist:
	now := time.Now()
	if primaryPendingThink != "" {
		primaryResult += "<think>" + primaryPendingThink + "</think>"
	}
	if secondaryPendingThink != "" {
		secondaryResult += "<think>" + secondaryPendingThink + "</think>"
	}
	if primaryTerminal == nil {
		event := failedRunEvent(primaryRunID, "missing_run_terminal", primaryResult != "")
		primaryTerminal, _ = event.Terminal()
		chunk := &ChatChunkResponse{ConversationID: convID, Seq: int32(seq), HistoryID: historyID, RuntimeEvent: event}
		if reqCtx.Err() == nil {
			writeSSEChunk(w, flusher, chunk)
		}
		if stateStore != nil {
			_ = appendChatChunk(context.Background(), stateStore, convID, historyID, chunk)
		}
	}
	if secondaryTerminal == nil {
		event := failedRunEvent(secondaryRunID, "missing_run_terminal", secondaryResult != "")
		secondaryTerminal, _ = event.Terminal()
		chunk := &ChatChunkResponse{ConversationID: convID, Seq: int32(seq), HistoryID: secondaryHistoryID, RuntimeEvent: event}
		if reqCtx.Err() == nil {
			writeSSEChunk(w, flusher, chunk)
		}
		if stateStore != nil {
			_ = appendChatChunk(context.Background(), stateStore, convID, secondaryHistoryID, chunk)
		}
	}
	primaryHistory := &orm.MultiAnswersChatHistory{
		ID: historyID, Seq: seq, ConversationID: convID, RawContent: query, Content: query, Result: primaryResult,
		ToolCallTurns: primaryToolCallTurns, ThinkingDurationS: primaryThinkingDurationS,
		RetrievalResult: marshalRetrievalResult(primarySources), Ext: historyExt,
		RunID: primaryRunID, RunStatus: primaryTerminal.Status, RunTerminal: terminalJSON(primaryTerminal),
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if primaryProgressCreated {
		_ = db.Model(primaryHistory).Where("id = ?", historyID).Updates(primaryHistory).Error
	} else {
		_ = db.Create(primaryHistory).Error
	}
	secondaryHistory := &orm.MultiAnswersChatHistory{
		ID: secondaryHistoryID, Seq: seq, ConversationID: convID, RawContent: query, Content: query, Result: secondaryResult,
		ToolCallTurns: secondaryToolCallTurns, ThinkingDurationS: secondaryThinkingDurationS,
		RetrievalResult: marshalRetrievalResult(secondarySources), Ext: historyExt,
		RunID: secondaryRunID, RunStatus: secondaryTerminal.Status, RunTerminal: terminalJSON(secondaryTerminal),
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if secondaryProgressCreated {
		_ = db.Model(secondaryHistory).Where("id = ?", secondaryHistoryID).Updates(secondaryHistory).Error
	} else {
		_ = db.Create(secondaryHistory).Error
	}
	if stateStore != nil {
		_ = setChatRuntimeStatus(context.Background(), stateStore, convID, historyID, primaryTerminal.Status, stripToolTags(primaryText), primaryRunID, primaryTerminal)
		_ = setChatRuntimeStatus(context.Background(), stateStore, convID, secondaryHistoryID, secondaryTerminal.Status, stripToolTags(secondaryText), secondaryRunID, secondaryTerminal)
	}
	db.Model(&orm.Conversation{}).Where("id = ?", convID).Update("updated_at", now)
	if !target.IsRegeneration {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1))
	}
	recordConversationIdleActivity(context.Background(), db, stateStore, convID, userIDFromChatRequestBody(reqBody), historyID, query, stripToolTags(primaryText), now)
}

func recordConversationIdleActivity(ctx context.Context, db *gorm.DB, stateStore state.Store, conversationID, userID, historyID, userContent, assistantText string, now time.Time) {
	if db == nil || stateStore == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(userID) == "" || strings.TrimSpace(historyID) == "" {
		return
	}
	_ = resourceupdate.RecordConversationIdleMessage(ctx, db, stateStore, resourceupdate.ConversationIdleRecord{
		ConversationID: conversationID,
		UserID:         userID,
		LastHistoryID:  historyID,
		LastActivityAt: now,
		UserContent:    userContent,
		AssistantText:  assistantText,
	})
}

// handleTaskCreated persists a SubAgent task record (allocating seq in a transaction),
// seeds the Redis status snapshot, launches the SubAgent runner goroutine, and returns
// a notice for the main SSE so the frontend can subscribe to the Task SSE stream.
func handleTaskCreated(
	chatCtx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	convID, historyID, userID string,
	ev *TaskCreatedEvent,
	llmConfig map[string]any,
	toolConfig map[string]any,
	workflowMode string,
) (*TaskCreatedNotice, error) {
	if ev == nil || strings.TrimSpace(ev.TaskID) == "" {
		return nil, fmt.Errorf("task_created event is missing task_id")
	}

	// Workflow Step path — handled separately.
	if ev.AgentType == "workflow_step" {
		return handleWorkflowStepCreated(chatCtx, db, stateStore, convID, historyID, userID, ev, llmConfig, toolConfig, workflowMode)
	}
	mode := ev.Mode
	if mode != "auto" && mode != "manual" {
		mode = "auto"
	}
	paramsJSON, _ := json.Marshal(ev.Params)
	inputKeysJSON, _ := json.Marshal(ev.InputSlots)
	outputKeysJSON, _ := json.Marshal(ev.OutputSlots)
	workspacePath := subagent.WorkspacePath(userID, ev.TaskID)

	// Resume path: reuse an existing task record (e.g. interrupted) instead of creating a new one.
	if ev.Resume {
		existing, getErr := subagent.GetTask(chatCtx, db, ev.TaskID)
		if getErr == nil && existing != nil {
			_ = subagent.UpdateStatus(chatCtx, db, existing.ID, subagent.StatusRunning)
			_ = subagent.WriteStatus(chatCtx, stateStore, existing.ID, map[string]any{
				"status": subagent.StatusRunning, "progress": existing.ProgressPct,
			})
			go subagent.Run(context.Background(), db, stateStore, subagent.RunRequest{
				TaskID:        existing.ID,
				AgentType:     existing.AgentType,
				Params:        ev.Params,
				WorkspacePath: existing.WorkspacePath,
				Tools:         ev.Tools,
				DBDSN:         subagent.DBDSN(),
				Resume:        true,
				LLMConfig:     llmConfig,
				ToolConfig:    toolConfig,
			})
			return &TaskCreatedNotice{
				TaskID:            existing.ID,
				TriggerHistoryID:  existing.TriggerHistoryID,
				Title:             existing.Title,
				AgentType:         existing.AgentType,
				Mode:              existing.Mode,
				Status:            subagent.StatusRunning,
				SeqInConversation: existing.SeqInConversation,
			}, nil
		}
	}

	task, err := subagent.CreateTask(chatCtx, db, subagent.CreateTaskInput{
		TaskID:           ev.TaskID,
		ConversationID:   convID,
		TriggerHistoryID: historyID,
		AgentType:        ev.AgentType,
		Title:            ev.Title,
		Objective:        ev.Objective,
		Mode:             mode,
		Params:           paramsJSON,
		InputSlots:       inputKeysJSON,
		OutputSlots:      outputKeysJSON,
		WorkspacePath:    workspacePath,
		CreateUserID:     strings.TrimSpace(userID),
	})
	if err != nil {
		fmt.Println("[Core] [SUBAGENT_CREATE_TASK_FAILED] err=", err)
		return nil, fmt.Errorf("create subagent task: %w", err)
	}
	_ = subagent.WriteStatus(chatCtx, stateStore, task.ID, map[string]any{
		"status": subagent.StatusPending, "progress": 0,
	})

	go subagent.Run(context.Background(), db, stateStore, subagent.RunRequest{
		TaskID:        task.ID,
		AgentType:     ev.AgentType,
		Params:        ev.Params,
		WorkspacePath: workspacePath,
		Tools:         ev.Tools,
		DBDSN:         subagent.DBDSN(),
		Resume:        false,
		LLMConfig:     llmConfig,
		ToolConfig:    toolConfig,
	})

	return &TaskCreatedNotice{
		TaskID:            task.ID,
		TriggerHistoryID:  task.TriggerHistoryID,
		Title:             task.Title,
		AgentType:         task.AgentType,
		Mode:              task.Mode,
		Status:            task.Status,
		SeqInConversation: task.SeqInConversation,
	}, nil
}

// handleWorkflowStepCreated processes a task_created event for agent_type='workflow_step'.
// It delegates to the plugin package EventLoop to manage session/step lifecycle.
func handleWorkflowStepCreated(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	convID, historyID, userID string,
	ev *TaskCreatedEvent,
	llmConfig map[string]any,
	toolConfig map[string]any,
	workflowMode string,
) (*TaskCreatedNotice, error) {
	params := workflowStepParamsFromEventParams(ev.Params)
	// Carry the resolved workflow_mode into params so it is persisted with the task
	// and available when OnSubAgentDone reconstructs WorkflowChatContext from DB.
	if workflowMode == "auto" || workflowMode == "dynamic" {
		params.WorkflowMode = workflowMode
	} else {
		params.WorkflowMode = "dynamic"
	}
	if params.WorkflowID == "" || params.StepID == "" {
		fmt.Println("[Core] [WORKFLOW_STEP_INVALID_PARAMS] workflow_id or step_id missing")
		return nil, fmt.Errorf("workflow_id or step_id missing")
	}

	sessionID, taskID, workflowCompleted, err := workflow.HandleWorkflowStepCreated(
		ctx, db, stateStore, convID, historyID, userID,
		ev.TaskID, ev.Title, ev.Objective,
		params,
		ev.InputSlots, ev.OutputSlots,
		llmConfig, toolConfig,
	)
	if err != nil {
		fmt.Printf("[Core] [WORKFLOW_STEP_FAILED] err=%v\n", err)
		return nil, err
	}

	// When ChatAgent signals plugin completion via __end__, emit workflow_completed
	// to the conversation event stream so the frontend can close the plugin panel.
	if workflowCompleted {
		_ = AppendConvEvent(ctx, stateStore, convID, &ConvEvent{
			Type: "workflow_completed",
			Payload: map[string]any{
				"session_id":  sessionID,
				"workflow_id": params.WorkflowID,
			},
		})
		return nil, nil
	}

	// Fetch the created task for the notice.
	task, getErr := subagent.GetTask(ctx, db, taskID)
	if getErr != nil {
		fmt.Printf("[Core] [WORKFLOW_STEP_GET_TASK_FAILED] err=%v\n", getErr)
		return nil, fmt.Errorf("plugin step was accepted but task lookup failed: %w", getErr)
	}
	return &TaskCreatedNotice{
		TaskID:            task.ID,
		TriggerHistoryID:  task.TriggerHistoryID,
		Title:             task.Title,
		AgentType:         "workflow_step",
		Mode:              "manual",
		Status:            task.Status,
		SeqInConversation: task.SeqInConversation,
		WorkflowSessionID: sessionID,
	}, nil
}

func workflowStepParamsFromEventParams(raw map[string]any) workflow.WorkflowStepParams {
	var params workflow.WorkflowStepParams
	if raw == nil {
		return params
	}
	if pid, ok := raw["workflow_id"].(string); ok {
		params.WorkflowID = pid
	}
	if v, ok := raw["workflow_ref"].(string); ok {
		params.WorkflowRef = v
	}
	if v, ok := raw["revision_id"].(string); ok {
		params.RevisionID = v
	}
	if v, ok := raw["tree_hash"].(string); ok {
		params.TreeHash = v
	}
	if v, ok := raw["remote_root"].(string); ok {
		params.RemoteRoot = v
	}
	switch v := raw["revision_no"].(type) {
	case float64:
		params.RevisionNo = int64(v)
	case int64:
		params.RevisionNo = v
	case int:
		params.RevisionNo = int64(v)
	}
	if sid, ok := raw["step_id"].(string); ok {
		params.StepID = sid
	}
	if sessID, ok := raw["session_id"].(string); ok {
		params.SessionID = sessID
	}
	if chatSID, ok := raw["chat_session_id"].(string); ok {
		params.ChatSessionID = chatSID
	}
	if ui, ok := raw["user_input"].(string); ok {
		params.UserInput = ui
	}
	if cold, ok := raw["is_cold_start"].(bool); ok {
		params.IsColdStart = cold
	}
	if handOff, ok := raw["hand_off"].(bool); ok {
		params.HandOff = &handOff
	}
	if preflightID, ok := raw["preflight_id"].(string); ok {
		params.PreflightID = preflightID
	}
	if rh, ok := raw["retry_hint"].(string); ok {
		params.RetryHint = rh
	}
	if pi, ok := raw["partial_indices"].(map[string]any); ok {
		parsed := make(map[string][]int, len(pi))
		for k, v := range pi {
			if arr, ok2 := v.([]any); ok2 {
				ints := make([]int, 0, len(arr))
				for _, elem := range arr {
					if f, ok3 := elem.(float64); ok3 {
						ints = append(ints, int(f))
					}
				}
				parsed[k] = ints
			}
		}
		params.PartialIndices = parsed
	}
	if hfpt, ok := raw["history_files_per_turn"].(map[string]any); ok {
		parsed := make(map[string][]string, len(hfpt))
		for k, v := range hfpt {
			if arr, ok2 := v.([]any); ok2 {
				strs := make([]string, 0, len(arr))
				for _, elem := range arr {
					if s, ok3 := elem.(string); ok3 {
						strs = append(strs, s)
					}
				}
				parsed[k] = strs
			}
		}
		params.HistoryFilesPerTurn = parsed
	}
	if flt, ok := raw["filters"].(map[string]any); ok && len(flt) > 0 {
		params.Filters = flt
	}
	if uid, ok := raw["user_id"].(string); ok && uid != "" {
		params.UserID = uid
	}
	return params
}

// handleWorkflowPreflightUpdated stores the latest trigger context outside chat history,
// so long clarification sequences are not lost to agent history compaction.
func handleWorkflowPreflightUpdated(
	ctx context.Context,
	db *gorm.DB,
	convID string,
	ev *WorkflowPreflightUpdatedEvent,
) {
	if db == nil || ev == nil || strings.TrimSpace(convID) == "" {
		return
	}
	var conv orm.Conversation
	if err := db.WithContext(ctx).Select("id", "ext").Where("id = ?", convID).First(&conv).Error; err != nil {
		return
	}
	ext := map[string]any{}
	if len(conv.Ext) > 0 {
		_ = json.Unmarshal(conv.Ext, &ext)
	}
	if ev.Clear {
		delete(ext, "workflow_preflight")
	} else if len(ev.Snapshot) > 0 {
		ext["workflow_preflight"] = ev.Snapshot
	}
	raw, _ := json.Marshal(ext)
	_ = db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", convID).Update("ext", raw).Error
}

func loadWorkflowPreflightContext(ctx context.Context, db *gorm.DB, convID string) map[string]any {
	if db == nil || strings.TrimSpace(convID) == "" {
		return nil
	}
	var conv orm.Conversation
	if err := db.WithContext(ctx).Select("ext").Where("id = ?", convID).First(&conv).Error; err != nil {
		return nil
	}
	ext := map[string]any{}
	if json.Unmarshal(conv.Ext, &ext) != nil {
		return nil
	}
	preflight, _ := ext["workflow_preflight"].(map[string]any)
	return preflight
}

func loadConversationIntentContext(ctx context.Context, db *gorm.DB, convID string) map[string]any {
	if db == nil || strings.TrimSpace(convID) == "" {
		return nil
	}
	var conv orm.Conversation
	if err := db.WithContext(ctx).Select("ext").Where("id = ?", convID).First(&conv).Error; err != nil {
		return nil
	}
	ext := map[string]any{}
	if json.Unmarshal(conv.Ext, &ext) != nil {
		return nil
	}
	intent, _ := ext["intent_context"].(map[string]any)
	return intent
}

// mergeAskPendingIntoExt merges ask_pending data into the ext JSON field so that
// the ask card is persisted and can be restored on page reload.
var intentScalarFields = map[string]bool{"goal": true, "deliverable": true, "execution_mode": true}
var intentListFields = map[string]bool{
	"constraints": true, "corrections": true, "emphasized_points": true, "superseded": true,
}

func normalizeIntentDocument(raw any) map[string]any {
	doc, _ := raw.(map[string]any)
	if doc == nil {
		doc = map[string]any{}
	}
	if text, _ := doc["text"].(string); strings.TrimSpace(text) != "" {
		doc = map[string]any{"constraints": []any{strings.TrimSpace(text)}}
	}
	doc["version"] = 2
	if _, ok := doc["revision"]; !ok {
		doc["revision"] = 0
	}
	return doc
}

func intentRevision(doc map[string]any) int {
	switch value := doc["revision"].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	}
	return 0
}

func applyIntentOperations(doc map[string]any, operations []IntentOperation) (map[string]any, error) {
	doc = normalizeIntentDocument(doc)
	for _, operation := range operations {
		op := strings.TrimSpace(operation.Op)
		field := strings.TrimSpace(operation.Field)
		value := strings.TrimSpace(operation.Value)
		if value == "" {
			return nil, fmt.Errorf("intent value is required")
		}
		if op == "set" {
			if !intentScalarFields[field] {
				return nil, fmt.Errorf("cannot set intent field %q", field)
			}
			doc[field] = value
			continue
		}
		if !intentListFields[field] || (op != "add" && op != "remove" && op != "supersede") {
			return nil, fmt.Errorf("invalid intent operation %q for field %q", op, field)
		}
		items, _ := doc[field].([]any)
		if typed, ok := doc[field].([]string); ok {
			items = make([]any, 0, len(typed))
			for _, item := range typed {
				items = append(items, item)
			}
		}
		if op == "supersede" {
			remaining := make([]any, 0, len(items))
			for _, item := range items {
				text := strings.TrimSpace(fmt.Sprint(item))
				if text != "" && text != value {
					remaining = append(remaining, text)
				}
			}
			doc[field] = remaining
			superseded, _ := doc["superseded"].([]any)
			found := false
			for _, item := range superseded {
				if strings.TrimSpace(fmt.Sprint(item)) == value {
					found = true
				}
			}
			if !found {
				superseded = append(superseded, value)
			}
			doc["superseded"] = superseded
			continue
		}
		filtered := make([]any, 0, len(items)+1)
		seen := false
		for _, item := range items {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text == value {
				seen = true
				if op == "remove" {
					continue
				}
			}
			if text != "" {
				filtered = append(filtered, text)
			}
		}
		if op != "remove" && !seen {
			filtered = append(filtered, value)
		}
		doc[field] = filtered
	}
	doc["revision"] = intentRevision(doc) + 1
	return doc, nil
}

// handleIntentUpdated writes the patch emitted by intentwrite to DB,
// then pushes an intent_updated convEvent so the frontend can refresh immediately.
func handleIntentUpdated(ctx context.Context, db *gorm.DB, stateStore state.Store, convID string, ev *IntentUpdatedEvent) *IntentUpdatedEvent {
	if ev == nil || len(ev.Operations) == 0 {
		return nil
	}
	var conversationUpdate *IntentUpdatedEvent
	if db != nil {
		now := time.Now().UTC()
		if ev.Scope == "conversation" && strings.TrimSpace(convID) != "" {
			var conv orm.Conversation
			if db.WithContext(ctx).Select("id", "ext").Where("id = ?", convID).First(&conv).Error == nil {
				ext := map[string]any{}
				_ = json.Unmarshal(conv.Ext, &ext)
				doc := normalizeIntentDocument(ext["intent_context"])
				if updated, err := applyIntentOperations(doc, ev.Operations); err == nil {
					ext["intent_context"] = updated
					raw, _ := json.Marshal(ext)
					if db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", convID).Update("ext", raw).Error == nil {
						conversationUpdate = &IntentUpdatedEvent{Scope: "conversation", IntentContext: updated}
					}
				}
			}
		} else if ev.Scope == "workflow_session" && ev.SessionID != "" {
			var session orm.WorkflowSession
			if db.WithContext(ctx).Select("intent_context").Where("id = ?", ev.SessionID).First(&session).Error == nil {
				doc := map[string]any{}
				_ = json.Unmarshal([]byte(session.IntentContext), &doc)
				if updated, err := applyIntentOperations(doc, ev.Operations); err == nil {
					payload, _ := json.Marshal(updated)
					_ = db.WithContext(ctx).Model(&orm.WorkflowSession{}).Where("id = ?", ev.SessionID).
						Updates(map[string]any{"intent_context": string(payload), "updated_at": now}).Error
				}
			}
		} else if ev.Scope == "workflow_step" && ev.SessionID != "" && ev.StepID != "" {
			var existing orm.WorkflowStepIntent
			doc := map[string]any{}
			if db.WithContext(ctx).Where("session_id = ? AND step_id = ?", ev.SessionID, ev.StepID).
				First(&existing).Error == nil {
				_ = json.Unmarshal([]byte(existing.IntentContext), &doc)
			}
			updated, err := applyIntentOperations(doc, ev.Operations)
			if err == nil {
				payload, _ := json.Marshal(updated)
				rowID := fmt.Sprintf("psi_%s", common.GenerateID())
				_ = db.WithContext(ctx).Exec(
					`INSERT INTO plugin_step_intents (id, session_id, step_id, intent_context, updated_at)
				 VALUES (?, ?, ?, ?, ?)
				 ON CONFLICT (session_id, step_id) DO UPDATE
				 SET intent_context = EXCLUDED.intent_context, updated_at = EXCLUDED.updated_at`,
					rowID, ev.SessionID, ev.StepID, string(payload), now,
				).Error
			}
		}
	}
	if stateStore != nil {
		_ = AppendConvEvent(ctx, stateStore, convID, &ConvEvent{
			Type: "intent_updated",
			Payload: map[string]any{
				"session_id": ev.SessionID,
				"scope":      ev.Scope,
				"step_id":    ev.StepID,
			},
		})
	}
	return conversationUpdate
}

func mergeIntentUpdatedIntoExt(ext json.RawMessage, intent *IntentUpdatedEvent) json.RawMessage {
	m := make(map[string]any)
	if len(ext) > 0 {
		_ = json.Unmarshal(ext, &m)
	}
	m["intent_updated"] = intent
	b, err := json.Marshal(m)
	if err != nil {
		return ext
	}
	return b
}

func mergeAskPendingIntoExt(ext json.RawMessage, askPending any) json.RawMessage {
	m := make(map[string]any)
	if len(ext) > 0 {
		_ = json.Unmarshal(ext, &m)
	}
	m["ask_pending"] = askPending
	b, err := json.Marshal(m)
	if err != nil {
		return ext
	}
	return b
}

// markLastAskPendingAnswered finds the most recent history entry that has
// ask_pending in ext, sets ask_answered=true in its ext, and clears
// ask_saved_answers so the AskCard shows as submitted on next page load.
func markLastAskPendingAnswered(ctx context.Context, db *gorm.DB, histories []orm.ChatHistory) {
	if db == nil {
		return
	}
	for i := len(histories) - 1; i >= 0; i-- {
		h := &histories[i]
		if len(h.Ext) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(h.Ext, &m); err != nil {
			continue
		}
		if m["ask_pending"] == nil {
			continue
		}
		if answered, _ := m["ask_answered"].(bool); answered {
			break
		}
		m["ask_answered"] = true
		delete(m, "ask_saved_answers")
		updated, err := json.Marshal(m)
		if err != nil {
			break
		}
		db.WithContext(ctx).Model(&orm.ChatHistory{}).
			Where("id = ?", h.ID).
			Update("ext", updated)
		break
	}
}

// SaveAskAnswers persists partial ask answers into the history ext so the
// user can return to the AskCard and continue where they left off.
func SaveAskAnswers(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var body struct {
		HistoryID string         `json:"history_id"`
		Answers   map[string]any `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.HistoryID == "" {
		common.ReplyErr(w, "history_id required", http.StatusBadRequest)
		return
	}

	var h orm.ChatHistory
	if err := db.WithContext(r.Context()).Where("id = ?", body.HistoryID).First(&h).Error; err != nil {
		common.ReplyErr(w, "history not found", http.StatusNotFound)
		return
	}
	// Verify the conversation owning this history belongs to the requesting user.
	if err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", h.ConversationID, userID).
		First(&orm.Conversation{}).Error; err != nil {
		common.ReplyErr(w, "history not found", http.StatusNotFound)
		return
	}
	m := make(map[string]any)
	if len(h.Ext) > 0 {
		_ = json.Unmarshal(h.Ext, &m)
	}
	if answered, _ := m["ask_answered"].(bool); answered {
		// Already submitted — do not allow overwriting answers.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m["ask_saved_answers"] = body.Answers
	updated, err := json.Marshal(m)
	if err != nil {
		common.ReplyErr(w, "failed to marshal ext", http.StatusInternalServerError)
		return
	}
	if err := db.WithContext(r.Context()).Model(&orm.ChatHistory{}).
		Where("id = ?", body.HistoryID).
		Update("ext", updated).Error; err != nil {
		common.ReplyErr(w, "failed to update history", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
