package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/log"
	"lazymind/core/plugin"
	"lazymind/core/resourceupdate"
	"lazymind/core/state"
	"lazymind/core/subagent"
)

const (
	maxConversationIDLength          = 36
	maxConversationDisplayNameLength = 255
	maxTopK                          = 10
	defaultTopK                      = 3
)

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

func recordConversationIdleAfterPersist(ctx context.Context, db *gorm.DB, stateStore state.Store, convID, userID, historyID string, at time.Time, query, answer string) {
	if db == nil || stateStore == nil {
		return
	}
	if err := resourceupdate.RecordConversationIdleMessage(ctx, db, stateStore, resourceupdate.ConversationIdleRecord{
		SessionID:      convID,
		UserID:         userID,
		LastMessageID:  historyID,
		LastActivityAt: at,
		UserContent:    query,
		AssistantText:  answer,
	}); err != nil {
		log.Logger.Warn().Err(err).Str("conversation_id", convID).Str("history_id", historyID).Msg("record conversation idle event failed")
	}
}

func marshalRetrievalResult(sources []any) json.RawMessage {
	payload, err := json.Marshal(map[string]any{"sources": sources})
	if err != nil {
		return nil
	}
	return payload
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
func ensureConversation(ctx context.Context, db *gorm.DB, convID, displayName string, searchConfig json.RawMessage, models json.RawMessage, userID, userName string, pluginSettings map[string]any) (*orm.Conversation, int, error) {
	now := time.Now()
	var c orm.Conversation
	err := db.Where("id = ? AND create_user_id = ?", convID, userID).First(&c).Error
	if err == nil {
		var count int64
		db.Model(&orm.ChatHistory{}).Where("conversation_id = ?", convID).Count(&count)

		updates := map[string]any{}
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
	// Resolve plugin settings for the new conversation.
	// Priority: caller-supplied pluginSettings > user_chat_settings defaults.
	// All three fields are always written so conversations.enable_plugin / plugin_mode /
	// enable_subagent are never NULL — no per-request fallback query needed.
	resolvedPS := resolveInitialPluginSettings(ctx, db, userID, pluginSettings)
	c.EnablePlugin = &resolvedPS.enablePlugin
	c.PluginMode = &resolvedPS.pluginMode
	c.EnableSubagent = &resolvedPS.enableSubagent
	if err := db.Create(&c).Error; err != nil {
		return nil, 0, err
	}
	return &c, 1, nil
}

type resolvedPluginSettings struct {
	enablePlugin   bool
	pluginMode     string
	enableSubagent bool
}

// resolveInitialPluginSettings merges caller-supplied overrides with the user's
// global defaults from user_chat_settings. Fields present in pluginSettings take
// priority; missing fields fall back to the DB defaults (or hardcoded values if
// the user has no row yet).
func resolveInitialPluginSettings(ctx context.Context, db *gorm.DB, userID string, pluginSettings map[string]any) resolvedPluginSettings {
	// Start from hardcoded fallbacks (matches user_chat_settings DB defaults).
	out := resolvedPluginSettings{
		enablePlugin:   true,
		pluginMode:     "dynamic",
		enableSubagent: true,
	}
	// Load user-level defaults.
	if db != nil {
		var s orm.UserChatSettings
		if err := db.WithContext(ctx).Where("user_id = ?", userID).First(&s).Error; err == nil {
			out.enablePlugin = s.EnablePlugin
			out.pluginMode = s.PluginMode
			out.enableSubagent = s.EnableSubagent
		}
	}
	// Apply caller-supplied overrides.
	if v, ok := pluginSettings["enable_plugin"].(bool); ok {
		out.enablePlugin = v
	}
	if v, ok := pluginSettings["plugin_mode"].(string); ok && (v == "dynamic" || v == "auto") {
		out.pluginMode = v
	}
	if v, ok := pluginSettings["enable_subagent"].(bool); ok {
		out.enableSubagent = v
	}
	return out
}

func buildHistoryMessages(histories []orm.ChatHistory) []map[string]string {
	if len(histories) == 0 {
		return nil
	}
	out := make([]map[string]string, 0, len(histories)*2)
	for _, h := range histories {
		out = append(out, map[string]string{"role": "user", "content": h.RawContent})
		out = append(out, map[string]string{"role": "assistant", "content": buildAssistantHistoryContent(h)})
	}
	return out
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
	b, err := json.Marshal(map[string]any{"input": input})
	if err != nil {
		return nil
	}
	return b
}

func chatHistoryInput(raw map[string]any, query string) any {
	if in, ok := raw["input"].([]any); ok && len(in) > 0 {
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
			lower := strings.ToLower(uri)
			if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
				continue
			}
			out[seqKey] = append(out[seqKey], uri)
		}
	}
	return out
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
	body := map[string]any{
		"query":            query,
		"session_id":       sessionID,
		"conversation_id":  convID,
		"history":          buildHistoryMessages(histories),
		"filters":          raw["filters"],
		"files":            filesMap,
		"current_turn_seq": currentSeq,
		"databases":        raw["databases"],
		"debug":            raw["debug"],
		"reasoning":        resolveReasoning(raw),
		"priority":         raw["priority"],
		"use_memory":       useMemory,
		"user_id":          strings.TrimSpace(userID),
		"mode":             mode,
	}
	if environmentContext, ok := raw["environment_context"].(map[string]any); ok {
		body["environment_context"] = environmentContext
	}
	// Propagate plugin_context so Python ChatAgent receives the active session info.
	// Merge plugin_ui_state (focused_tab, focused_sort_order) from the request body.
	// Python reads artifact state directly from the DB via _build_session_artifact_section.
	if pc, ok := raw["plugin_context"].(map[string]any); ok && len(pc) > 0 {
		mergedPC := make(map[string]any, len(pc)+4)
		for k, v := range pc {
			mergedPC[k] = v
		}
		if uis, ok := raw["plugin_ui_state"].(map[string]any); ok {
			if ft, ok := uis["focused_tab"]; ok {
				mergedPC["focused_tab"] = ft
			}
			if fso, ok := uis["focused_sort_order"]; ok {
				mergedPC["focused_sort_order"] = fso
			}
		}
		body["plugin_context"] = mergedPC
	}
	// Propagate ask_response so Python ChatAgent can resolve ask_pending state.
	// Format: {"ask_id": "...", "selected": [...]}
	if ar, ok := raw["ask_response"].(map[string]any); ok && len(ar) > 0 {
		body["ask_response"] = ar
	}
	if resourceContext != nil {
		body["disabled_tools"] = resourceContext.DisabledTools
		body["available_skills"] = resourceContext.AvailableSkills
		if useMemory {
			body["memory"] = resourceContext.Memory
			body["user_preference"] = resourceContext.UserPreference
		}
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
	return body
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

// resolvePluginMode determines the effective plugin_mode for this request.
// Priority: request body > "dynamic" default.
// Valid values: "auto", "dynamic". Anything else is normalised to "dynamic".
func resolvePluginMode(raw map[string]any) string {
	if v, ok := raw["plugin_mode"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "auto" || v == "dynamic" {
			return v
		}
	}
	return "dynamic"
}

// resolvePluginModeWithFallback determines the effective plugin_mode with full priority chain:
//
//	request body > DB-resolved agentic_config (loaded via applyChatRuntimeConfigs) > "dynamic"
//
// reqBody must have already been populated by applyChatRuntimeConfigs so that
// reqBody["agentic_config"]["plugin_mode"] reflects the DB value.
func resolvePluginModeWithFallback(raw map[string]any, reqBody map[string]any) string {
	// Highest priority: explicit value in the original request body.
	if v, ok := raw["plugin_mode"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "auto" || v == "dynamic" {
			return v
		}
	}
	return pluginModeFromReqBody(reqBody)
}

// pluginModeFromReqBody reads the resolved plugin_mode from a fully-built chat request body.
// Priority: plugin_context > agentic_config > "dynamic".
// Used when persisting plugin_step task params so OnSubAgentDone can branch on auto vs dynamic.
func pluginModeFromReqBody(reqBody map[string]any) string {
	if pc, ok := reqBody["plugin_context"].(map[string]any); ok {
		if v, ok := pc["plugin_mode"].(string); ok {
			v = strings.TrimSpace(v)
			if v == "auto" || v == "dynamic" {
				return v
			}
		}
	}
	if ac, ok := reqBody["agentic_config"].(map[string]any); ok {
		if v, ok := ac["plugin_mode"].(string); ok {
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
	pyBody, _ := json.Marshal(reqBody)
	upstreamURL := common.JoinURL(baseURL, "/api/chat")
	respBytes, _, err := common.HTTPPost(reqCtx, upstreamURL, "application/json", pyBody)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "chat service unavailable", err), http.StatusBadGateway)
		return
	}
	var pyResp struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(respBytes, &pyResp)
	answer := ""
	rawAnswer := ""
	var toolCallTurns int
	var sources []any
	if pyResp.Code == 200 && len(pyResp.Data) > 0 {
		var data struct {
			Text          string `json:"text"`
			Think         string `json:"think"`
			Sources       []any  `json:"sources"`
			ToolCallTurns int64  `json:"tool_call_turns"`
		}
		if json.Unmarshal(pyResp.Data, &data) == nil {
			if data.Think != "" {
				rawAnswer = "<think>" + strings.TrimSpace(data.Think) + "</think>" + strings.TrimSpace(data.Text)
			} else {
				rawAnswer = strings.TrimSpace(data.Text)
			}
			answer = strings.TrimSpace(stripToolTags(data.Text))
			sources = data.Sources
			toolCallTurns = nonNegativeToolCallTurns(data.ToolCallTurns)
		}
		if rawAnswer == "" {
			rawAnswer = strings.TrimSpace(string(pyResp.Data))
		}
		if answer == "" {
			answer = strings.TrimSpace(stripToolTags(rawAnswer))
		}
	}
	if pyResp.Code != 200 {
		answer = "error: " + pyResp.Msg
		rawAnswer = answer
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
		ToolCallTurns:   toolCallTurns,
		FeedBack:        0,
		Reason:          "",
		ExpectedAnswer:  "",
		Ext:             historyExt,
		TimeMixin:       orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if target.IsRegeneration && target.Existing != nil {
		hist.TimeMixin.CreateTime = target.Existing.CreateTime
		if err := db.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(map[string]any{
			"seq":              target.Seq,
			"raw_content":      query,
			"content":          query,
			"result":           rawAnswer,
			"tool_call_turns":  toolCallTurns,
			"retrieval_result": retrievalResult,
			"feed_back":        0,
			"reason":           "",
			"expected_answer":  "",
			"ext":              historyExt,
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
		_ = setChatStatus(reqCtx, stateStore, convID, historyID, "completed", answer)
	}
	db.Model(&orm.Conversation{}).Where("id = ?", convID).Update("updated_at", now)
	if !target.IsRegeneration {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1))
		recordConversationIdleAfterPersist(context.Background(), db, stateStore, convID, userIDFromChatRequestBody(reqBody), historyID, now, query, answer)
	}
	common.ReplyOK(w, map[string]any{
		"conversation_id": convID,
		"seq":             target.Seq,
		"message":         answer,
		"delta":           "",
		"finish_reason":   "FINISH_REASON_STOP",
		"history_id":      historyID,
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
	if dualReply {
		secondaryHistoryID = newID("h_")
	}
	chatCtx, chatCancel := context.WithCancel(context.Background())
	defer chatCancel()
	if stateStore != nil {
		if target.IsRegeneration {
			_ = clearChatData(chatCtx, stateStore, convID, historyID)
		}
		_ = setChatInput(chatCtx, stateStore, convID, historyID, query, target.Seq, historyExt)
		_ = setChatStatus(chatCtx, stateStore, convID, historyID, "generating", "")
		if dualReply {
			_ = setChatInput(chatCtx, stateStore, convID, secondaryHistoryID, query, target.Seq, historyExt)
			_ = setChatStatus(chatCtx, stateStore, convID, secondaryHistoryID, "generating", "")
			_ = setMultiAnswerInfo(chatCtx, stateStore, convID, historyID, secondaryHistoryID, target.Seq)
		}
		go func() {
			_ = watchChatCancelSignal(chatCtx, stateStore, convID, historyID)
			chatCancel()
		}()
	}

	if !dualReply {
		streamSingleAnswer(chatCtx, reqCtx, w, flusher, db, stateStore, baseURL, reqBody, convID, query, historyID, target, historyExt)
		return
	}
	streamDualAnswer(chatCtx, reqCtx, w, flusher, db, stateStore, baseURL, reqBody, convID, query, historyID, secondaryHistoryID, target, historyExt)
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
	seq := target.Seq
	ch, err := StreamChatUpstream(chatCtx, baseURL, reqBody)
	if err != nil {
		if stateStore != nil {
			_ = setChatStatus(chatCtx, stateStore, convID, historyID, "failed", "")
		}
		writeSSEChunk(w, flusher, &ChatChunkResponse{
			ConversationID:    convID,
			Seq:               int32(seq),
			Message:           "",
			Delta:             "",
			FinishReason:      "FINISH_REASON_UNKNOWN",
			HistoryID:         historyID,
			Sources:           nil,
			PromptQuestions:   []string{},
			ReasoningContent:  "",
			ThinkingDurationS: 0,
		})
		return
	}
	var fullText string
	var pendingThink string
	var fullResult string
	var toolCallTurns int
	var sources []any
	var pendingAskPending any
	thinkStart := time.Now()
	// text：textConversation/text，finish_reason text UNSPECIFIED
	writeSSEChunk(w, flusher, &ChatChunkResponse{
		ConversationID:    convID,
		Seq:               int32(seq),
		Message:           "",
		Delta:             "",
		FinishReason:      "FINISH_REASON_UNSPECIFIED",
		HistoryID:         historyID,
		Sources:           nil,
		PromptQuestions:   []string{},
		ReasoningContent:  "",
		ThinkingDurationS: 0,
	})
	for d := range ch {
		if d.TaskCreated != nil {
			userIDForTask, _ := reqBody["user_id"].(string)
			pluginModeForTask := pluginModeFromReqBody(reqBody)
			notice := handleTaskCreated(chatCtx, db, stateStore, convID, historyID, userIDForTask, d.TaskCreated, llmConfigFromBody(reqBody), toolConfigFromBody(reqBody), pluginModeForTask)
			if notice != nil {
				taskChunk := &ChatChunkResponse{
					ConversationID: convID,
					Seq:            int32(seq),
					HistoryID:      historyID,
					FinishReason:   "FINISH_REASON_UNSPECIFIED",
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
				FinishReason:   "FINISH_REASON_UNSPECIFIED",
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
		if d.Heartbeat {
			continue
		}
		if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > toolCallTurns {
			toolCallTurns = next
		}
		if d.ReasoningText != "" {
			pendingThink += d.ReasoningText
			continue
		}
		if pendingThink != "" {
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
			ConversationID:    convID,
			Seq:               int32(seq),
			Message:           "",
			Delta:             deltaToSend,
			FinishReason:      "FINISH_REASON_UNSPECIFIED",
			HistoryID:         historyID,
			Sources:           sources,
			PromptQuestions:   []string{},
			ReasoningContent:  "",
			ThinkingDurationS: int64(time.Since(thinkStart).Seconds()),
		}
		if reqCtx.Err() == nil {
			writeSSEChunk(w, flusher, chunk)
		}
		if stateStore != nil {
			_ = appendChatChunk(chatCtx, stateStore, convID, historyID, chunk)
		}
	}
	now := time.Now()
	retrievalResult := marshalRetrievalResult(sources)
	if pendingThink != "" {
		fullResult += "<think>" + pendingThink + "</think>"
	}
	// Persist ask_pending into ext so the ask card survives page reload.
	if pendingAskPending != nil {
		historyExt = mergeAskPendingIntoExt(historyExt, pendingAskPending)
	}
	persisted := false
	if target.IsRegeneration && target.Existing != nil {
		if err := db.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(map[string]any{
			"seq":              seq,
			"raw_content":      query,
			"content":          query,
			"result":           fullResult,
			"tool_call_turns":  toolCallTurns,
			"retrieval_result": retrievalResult,
			"feed_back":        0,
			"reason":           "",
			"expected_answer":  "",
			"ext":              historyExt,
			"update_time":      now,
		}).Error; err != nil {
			log.Logger.Warn().Err(err).Str("conversation_id", convID).Str("history_id", historyID).Msg("failed to update stream chat history")
		} else {
			persisted = true
		}
	} else {
		if err := db.Create(&orm.ChatHistory{
			ID:              historyID,
			Seq:             seq,
			ConversationID:  convID,
			RawContent:      query,
			RetrievalResult: retrievalResult,
			Content:         query,
			Result:          fullResult,
			ToolCallTurns:   toolCallTurns,
			Ext:             historyExt,
			TimeMixin:       orm.TimeMixin{CreateTime: now, UpdateTime: now},
		}).Error; err != nil {
			log.Logger.Warn().Err(err).Str("conversation_id", convID).Str("history_id", historyID).Msg("failed to save stream chat history")
		} else {
			persisted = true
		}
	}
	if stateStore != nil {
		_ = setChatStatus(context.Background(), stateStore, convID, historyID, "completed", stripToolTags(fullText))
	}
	if persisted {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).Update("updated_at", now)
	}
	if persisted && !target.IsRegeneration {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1))
		recordConversationIdleAfterPersist(context.Background(), db, stateStore, convID, userIDFromChatRequestBody(reqBody), historyID, now, query, stripToolTags(fullText))
	}
	if reqCtx.Err() == nil {
		// text：message text，finish_reason text STOP
		writeSSEChunk(w, flusher, &ChatChunkResponse{
			ConversationID:  convID,
			Seq:             int32(seq),
			Message:         stripToolTags(fullText),
			Delta:           "",
			FinishReason:    "FINISH_REASON_STOP",
			HistoryID:       historyID,
			Sources:         sources,
			PromptQuestions: []string{},
			// Do not replay reasoning on final message frame.
			ReasoningContent:  "",
			ThinkingDurationS: int64(time.Since(thinkStart).Seconds()),
		})
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
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
	primaryCh, err1 := StreamChatUpstream(chatCtx, baseURL, reqBody)
	secondaryReq := make(map[string]any)
	for k, v := range reqBody {
		secondaryReq[k] = v
	}
	if sc, ok := secondaryReq["filters"].(map[string]any); ok {
		sc["kb_id"] = nil
	}
	secondaryCh, err2 := StreamChatUpstream(chatCtx, baseURL, secondaryReq)
	if err1 != nil && err2 != nil {
		if stateStore != nil {
			_ = setChatStatus(chatCtx, stateStore, convID, historyID, "failed", "")
			_ = setChatStatus(chatCtx, stateStore, convID, secondaryHistoryID, "failed", "")
		}
		writeSSEChunk(w, flusher, map[string]any{"finish_reason": "FINISH_REASON_UNKNOWN"})
		return
	}
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
	var primaryPendingThink, secondaryPendingThink string
	var primaryToolCallTurns, secondaryToolCallTurns int
	primaryDone := primaryCh == nil
	secondaryDone := secondaryCh == nil
	var writeMu sync.Mutex
	appendPrimary := func(delta, reasoning string, sources []any) {
		if reasoning != "" {
			primaryPendingThink += reasoning
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
			writeMu.Lock()
			writeSSEChunk(w, flusher, map[string]any{
				"conversation_id": convID, "seq": seq, "delta": delta, "history_id": historyID,
				"sources": sources,
			})
			writeMu.Unlock()
		}
		if stateStore != nil {
			_ = appendChatChunk(chatCtx, stateStore, convID, historyID, &ChatChunkResponse{
				ConversationID: convID, Seq: int32(seq), Delta: delta, HistoryID: historyID,
				ReasoningContent: "", Sources: sources,
			})
		}
	}
	appendSecondary := func(delta, reasoning string, sources []any) {
		if reasoning != "" {
			secondaryPendingThink += reasoning
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
			writeMu.Lock()
			writeSSEChunk(w, flusher, map[string]any{
				"conversation_id": convID, "seq": seq, "delta": delta, "history_id": secondaryHistoryID,
				"sources": sources,
			})
			writeMu.Unlock()
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
				continue
			}
			if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > primaryToolCallTurns {
				primaryToolCallTurns = next
			}
			appendPrimary(d.Text, d.ReasoningText, d.Sources)
		case d, ok := <-secondaryCh:
			if !ok {
				secondaryDone = true
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
						if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > primaryToolCallTurns {
							primaryToolCallTurns = next
						}
						if d.ReasoningText != "" {
							primaryPendingThink += d.ReasoningText
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
						if next := nonNegativeToolCallTurns(d.ToolCallTurns); next > secondaryToolCallTurns {
							secondaryToolCallTurns = next
						}
						if d.ReasoningText != "" {
							secondaryPendingThink += d.ReasoningText
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
	_ = db.Create(&orm.MultiAnswersChatHistory{
		ID: historyID, Seq: seq, ConversationID: convID, RawContent: query, Content: query, Result: primaryResult,
		ToolCallTurns: primaryToolCallTurns,
		Ext:           historyExt,
		TimeMixin:     orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error
	_ = db.Create(&orm.MultiAnswersChatHistory{
		ID: secondaryHistoryID, Seq: seq, ConversationID: convID, RawContent: query, Content: query, Result: secondaryResult,
		ToolCallTurns: secondaryToolCallTurns,
		Ext:           historyExt,
		TimeMixin:     orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error
	if stateStore != nil {
		_ = setChatStatus(context.Background(), stateStore, convID, historyID, "completed", stripToolTags(primaryText))
		_ = setChatStatus(context.Background(), stateStore, convID, secondaryHistoryID, "completed", stripToolTags(secondaryText))
	}
	db.Model(&orm.Conversation{}).Where("id = ?", convID).Update("updated_at", now)
	if !target.IsRegeneration {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1))
	}
	if reqCtx.Err() == nil {
		writeSSEChunk(w, flusher, map[string]any{"finish_reason": "FINISH_REASON_STOP", "history_id": historyID})
		writeSSEChunk(w, flusher, map[string]any{"finish_reason": "FINISH_REASON_STOP", "history_id": secondaryHistoryID})
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}
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
	pluginMode string,
) *TaskCreatedNotice {
	if ev == nil || strings.TrimSpace(ev.TaskID) == "" {
		return nil
	}

	// Plugin Step path — handled separately.
	if ev.AgentType == "plugin_step" {
		return handlePluginStepCreated(chatCtx, db, stateStore, convID, historyID, userID, ev, llmConfig, toolConfig, pluginMode)
	}
	mode := ev.Mode
	if mode != "auto" && mode != "manual" {
		mode = "auto"
	}
	paramsJSON, _ := json.Marshal(ev.Params)
	inputKeysJSON, _ := json.Marshal(ev.InputArtifactKeys)
	outputKeysJSON, _ := json.Marshal(ev.OutputArtifactKeys)
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
				Title:             existing.Title,
				AgentType:         existing.AgentType,
				Mode:              existing.Mode,
				Status:            subagent.StatusRunning,
				SeqInConversation: existing.SeqInConversation,
			}
		}
	}

	task, err := subagent.CreateTask(chatCtx, db, subagent.CreateTaskInput{
		TaskID:             ev.TaskID,
		ConversationID:     convID,
		TriggerHistoryID:   historyID,
		AgentType:          ev.AgentType,
		Title:              ev.Title,
		Objective:          ev.Objective,
		Mode:               mode,
		Params:             paramsJSON,
		InputArtifactKeys:  inputKeysJSON,
		OutputArtifactKeys: outputKeysJSON,
		WorkspacePath:      workspacePath,
		CreateUserID:       strings.TrimSpace(userID),
	})
	if err != nil {
		fmt.Println("[Core] [SUBAGENT_CREATE_TASK_FAILED] err=", err)
		return nil
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
		Title:             task.Title,
		AgentType:         task.AgentType,
		Mode:              task.Mode,
		Status:            task.Status,
		SeqInConversation: task.SeqInConversation,
	}
}

// handlePluginStepCreated processes a task_created event for agent_type='plugin_step'.
// It delegates to the plugin package EventLoop to manage session/step lifecycle.
func handlePluginStepCreated(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	convID, historyID, userID string,
	ev *TaskCreatedEvent,
	llmConfig map[string]any,
	toolConfig map[string]any,
	pluginMode string,
) *TaskCreatedNotice {
	// Parse PluginStepParams from ev.Params.
	var params plugin.PluginStepParams
	if ev.Params != nil {
		if pid, ok := ev.Params["plugin_id"].(string); ok {
			params.PluginID = pid
		}
		if sid, ok := ev.Params["step_id"].(string); ok {
			params.StepID = sid
		}
		if sessID, ok := ev.Params["session_id"].(string); ok {
			params.SessionID = sessID
		}
		if ui, ok := ev.Params["user_input"].(string); ok {
			params.UserInput = ui
		}
		if cold, ok := ev.Params["is_cold_start"].(bool); ok {
			params.IsColdStart = cold
		}
		if rh, ok := ev.Params["retry_hint"].(string); ok {
			params.RetryHint = rh
		}
		if pi, ok := ev.Params["partial_indices"].(map[string]any); ok {
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
		if hfpt, ok := ev.Params["history_files_per_turn"].(map[string]any); ok {
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
	}
	// Carry the resolved plugin_mode into params so it is persisted with the task
	// and available when OnSubAgentDone reconstructs PluginChatContext from DB.
	if pluginMode == "auto" || pluginMode == "dynamic" {
		params.PluginMode = pluginMode
	} else {
		params.PluginMode = "dynamic"
	}
	if params.PluginID == "" || params.StepID == "" {
		fmt.Println("[Core] [PLUGIN_STEP_INVALID_PARAMS] plugin_id or step_id missing")
		return nil
	}

	sessionID, taskID, pluginCompleted, err := plugin.HandlePluginStepCreated(
		ctx, db, stateStore, convID, historyID, userID,
		ev.TaskID, ev.Title, ev.Objective,
		params,
		ev.InputArtifactKeys, ev.OutputArtifactKeys,
		llmConfig, toolConfig,
	)
	if err != nil {
		fmt.Printf("[Core] [PLUGIN_STEP_FAILED] err=%v\n", err)
		return nil
	}

	// When ChatAgent signals plugin completion via __end__, emit plugin_completed
	// to the conversation event stream so the frontend can close the plugin panel.
	if pluginCompleted {
		_ = AppendConvEvent(ctx, stateStore, convID, &ConvEvent{
			Type: "plugin_completed",
			Payload: map[string]any{
				"session_id": sessionID,
				"plugin_id":  params.PluginID,
			},
		})
		return nil
	}

	// Fetch the created task for the notice.
	task, getErr := subagent.GetTask(ctx, db, taskID)
	if getErr != nil {
		fmt.Printf("[Core] [PLUGIN_STEP_GET_TASK_FAILED] err=%v\n", getErr)
		return nil
	}
	return &TaskCreatedNotice{
		TaskID:            task.ID,
		Title:             task.Title,
		AgentType:         "plugin_step",
		Mode:              "manual",
		Status:            task.Status,
		SeqInConversation: task.SeqInConversation,
		PluginSessionID:   sessionID,
	}
}

// mergeAskPendingIntoExt merges ask_pending data into the ext JSON field so that
// the ask card is persisted and can be restored on page reload.
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
