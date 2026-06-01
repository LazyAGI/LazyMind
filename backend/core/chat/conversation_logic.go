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

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
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

func marshalRetrievalResult(sources []any) json.RawMessage {
	payload, err := json.Marshal(map[string]any{"sources": sources})
	if err != nil {
		return nil
	}
	return payload
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
func ensureConversation(db *gorm.DB, convID, displayName string, searchConfig json.RawMessage, models json.RawMessage, userID, userName string) (*orm.Conversation, int, error) {
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
	if err := db.Create(&c).Error; err != nil {
		return nil, 0, err
	}
	return &c, 1, nil
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

func buildChatRequestBody(convID, sessionID, query string, histories []orm.ChatHistory, raw map[string]any, resourceContext *evolution.ChatResourceContext, userID string) map[string]any {
	if strings.TrimSpace(sessionID) == "" {
		sessionID = upstreamSessionID(convID)
	}
	useMemory := resolveUseMemory(raw, resourceContext)
	body := map[string]any{
		"query":           query,
		"session_id":      sessionID,
		"history":         buildHistoryMessages(histories),
		"filters":         raw["filters"],
		"files":           filePathsForUpstreamChat(raw),
		"databases":       raw["databases"],
		"debug":           raw["debug"],
		"reasoning":       resolveReasoning(raw),
		"priority":        raw["priority"],
		"enable_thinking": raw["enable_thinking"],
		"use_memory":      useMemory,
		"user_id":         strings.TrimSpace(userID),
	}
	if environmentContext, ok := raw["environment_context"].(map[string]any); ok {
		body["environment_context"] = environmentContext
	}
	if resourceContext != nil {
		body["available_tools"] = resourceContext.AvailableTools
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
				if len(filters) > 0 {
					body["filters"] = filters
				}
			}
		}
	}
	return body
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
	rdb *redis.Client,
	baseURL string,
	reqBody map[string]any,
	convID, query string,
	target chatPersistTarget,
	historyExt json.RawMessage,
) {
	pyBody, _ := json.Marshal(reqBody)
	upstreamURL := common.JoinURL(baseURL, "/api/chat")
	fmt.Printf("DEBUG upstream request url=%s params=%+v\n", upstreamURL, reqBody)
	respBytes, statusCode, err := common.HTTPPost(reqCtx, upstreamURL, "application/json", pyBody)
	if err != nil {
		fmt.Println("DEBUG upstream request failed url=", upstreamURL, " err=", err)
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "chat service unavailable", err), http.StatusBadGateway)
		return
	}
	fmt.Println("DEBUG upstream response url=", upstreamURL, " status=", statusCode)
	var pyResp struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(respBytes, &pyResp)
	answer := ""
	rawAnswer := ""
	var sources []any
	if pyResp.Code == 200 && len(pyResp.Data) > 0 {
		var data struct {
			Text    string `json:"text"`
			Think   string `json:"think"`
			Sources []any  `json:"sources"`
		}
		if json.Unmarshal(pyResp.Data, &data) == nil {
			if data.Think != "" {
				rawAnswer = "<think>" + strings.TrimSpace(data.Think) + "</think>" + strings.TrimSpace(data.Text)
			} else {
				rawAnswer = strings.TrimSpace(data.Text)
			}
			answer = strings.TrimSpace(stripToolTags(data.Text))
			sources = data.Sources
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
	if rdb != nil {
		_ = setChatStatus(reqCtx, rdb, convID, historyID, "completed", answer)
	}
	db.Model(&orm.Conversation{}).Where("id = ?", convID).Update("updated_at", now)
	if !target.IsRegeneration {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1))
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
	rdb *redis.Client,
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
	if rdb != nil {
		if target.IsRegeneration {
			_ = clearChatData(chatCtx, rdb, convID, historyID)
		}
		_ = setChatInput(chatCtx, rdb, convID, historyID, query, target.Seq, historyExt)
		_ = setChatStatus(chatCtx, rdb, convID, historyID, "generating", "")
		if dualReply {
			_ = setChatInput(chatCtx, rdb, convID, secondaryHistoryID, query, target.Seq, historyExt)
			_ = setChatStatus(chatCtx, rdb, convID, secondaryHistoryID, "generating", "")
			_ = setMultiAnswerInfo(chatCtx, rdb, convID, historyID, secondaryHistoryID, target.Seq)
		}
		go func() {
			_ = watchChatCancelSignal(chatCtx, rdb, convID, historyID)
			chatCancel()
		}()
	}

	if !dualReply {
		streamSingleAnswer(chatCtx, reqCtx, w, flusher, db, rdb, baseURL, reqBody, convID, query, historyID, target, historyExt)
		return
	}
	streamDualAnswer(chatCtx, reqCtx, w, flusher, db, rdb, baseURL, reqBody, convID, query, historyID, secondaryHistoryID, target, historyExt)
}

func streamSingleAnswer(
	chatCtx, reqCtx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	db *gorm.DB,
	rdb *redis.Client,
	baseURL string,
	reqBody map[string]any,
	convID, query, historyID string,
	target chatPersistTarget,
	historyExt json.RawMessage,
) {
	seq := target.Seq
	ch, err := StreamChatUpstream(chatCtx, baseURL, reqBody)
	if err != nil {
		if rdb != nil {
			_ = setChatStatus(chatCtx, rdb, convID, historyID, "failed", "")
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
	var sources []any
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
		if rdb != nil {
			_ = appendChatChunk(chatCtx, rdb, convID, historyID, chunk)
		}
	}
	now := time.Now()
	retrievalResult := marshalRetrievalResult(sources)
	if pendingThink != "" {
		fullResult += "<think>" + pendingThink + "</think>"
	}
	if target.IsRegeneration && target.Existing != nil {
		_ = db.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(map[string]any{
			"seq":              seq,
			"raw_content":      query,
			"content":          query,
			"result":           fullResult,
			"retrieval_result": retrievalResult,
			"feed_back":        0,
			"reason":           "",
			"expected_answer":  "",
			"ext":              historyExt,
			"update_time":      now,
		}).Error
	} else {
		_ = db.Create(&orm.ChatHistory{
			ID:              historyID,
			Seq:             seq,
			ConversationID:  convID,
			RawContent:      query,
			RetrievalResult: retrievalResult,
			Content:         query,
			Result:          fullResult,
			Ext:             historyExt,
			TimeMixin:       orm.TimeMixin{CreateTime: now, UpdateTime: now},
		}).Error
	}
	if rdb != nil {
		_ = setChatStatus(context.Background(), rdb, convID, historyID, "completed", stripToolTags(fullText))
	}
	db.Model(&orm.Conversation{}).Where("id = ?", convID).Update("updated_at", now)
	if !target.IsRegeneration {
		db.Model(&orm.Conversation{}).Where("id = ?", convID).UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1))
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
	rdb *redis.Client,
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
		if rdb != nil {
			_ = setChatStatus(chatCtx, rdb, convID, historyID, "failed", "")
			_ = setChatStatus(chatCtx, rdb, convID, secondaryHistoryID, "failed", "")
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
		if rdb != nil {
			_ = appendChatChunk(chatCtx, rdb, convID, historyID, &ChatChunkResponse{
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
		if rdb != nil {
			_ = appendChatChunk(chatCtx, rdb, convID, secondaryHistoryID, &ChatChunkResponse{
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
			appendPrimary(d.Text, d.ReasoningText, d.Sources)
		case d, ok := <-secondaryCh:
			if !ok {
				secondaryDone = true
				continue
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
						if rdb != nil {
							_ = appendChatChunk(bg, rdb, convID, historyID, &ChatChunkResponse{
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
						if rdb != nil {
							_ = appendChatChunk(bg, rdb, convID, secondaryHistoryID, &ChatChunkResponse{
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
		Ext:       historyExt,
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error
	_ = db.Create(&orm.MultiAnswersChatHistory{
		ID: secondaryHistoryID, Seq: seq, ConversationID: convID, RawContent: query, Content: query, Result: secondaryResult,
		Ext:       historyExt,
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error
	if rdb != nil {
		_ = setChatStatus(context.Background(), rdb, convID, historyID, "completed", stripToolTags(primaryText))
		_ = setChatStatus(context.Background(), rdb, convID, secondaryHistoryID, "completed", stripToolTags(secondaryText))
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
