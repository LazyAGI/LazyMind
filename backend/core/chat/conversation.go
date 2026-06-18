package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/modelconfig"
	"lazymind/core/plugin"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/taskcenter"
)

func writeConversationJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// writeSSEChunk text SSE text： data: {"result":{...}}\n\n
func writeSSEChunk(w http.ResponseWriter, flusher http.Flusher, v any) {
	wrapped := map[string]any{"result": v}
	b, _ := json.Marshal(wrapped)
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
	if flusher != nil {
		flusher.Flush()
	}
}

func Chat(w http.ResponseWriter, r *http.Request) {
	ChatConversations(w, r)
}

// ChatConversations text POST /api/v1/conversations:chat
func ChatConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		common.ReplyErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fmt.Println("[Core] [CHAT_REQUEST] path=", r.URL.Path,
		" authorization=", modelconfig.APIKeyState(r.Header.Get("Authorization")),
		" x_user_id=", r.Header.Get("X-User-Id"),
		" x_user_name=", r.Header.Get("X-User-Name"))

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "read body failed", err), http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	_, items := extractMessageForACL(r, bodyBytes)
	if len(items) > 0 {
		uid := strings.TrimSpace(r.Header.Get("X-User-Id"))
		for _, it := range items {
			if it.NeedPerm == "" || !acl.Can(uid, it.ResourceType, it.ResourceID, it.NeedPerm) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(common.ForbiddenBody))
				return
			}
		}
	}

	var raw map[string]any
	if json.Unmarshal(bodyBytes, &raw) != nil {
		common.ReplyErr(w, "invalid json", http.StatusBadRequest)
		return
	}
	setConversationDefaultValue(raw)
	if !checkInput(raw) {
		common.ReplyErr(w, "input required", http.StatusBadRequest)
		return
	}
	if !checkSearchConfig(raw) {
		common.ReplyErr(w, "invalid search_config (top_k 1-10, confidence 0-1)", http.StatusBadRequest)
		return
	}

	convID, _ := raw["conversation_id"].(string)
	if convID == "" {
		convID = newConversationID()
	}
	if len(convID) > maxConversationIDLength {
		common.ReplyErr(w, "conversation_id too long", http.StatusBadRequest)
		return
	}
	conv, _ := raw["conversation"].(map[string]any)
	displayName := ""
	if conv != nil {
		displayName, _ = conv["display_name"].(string)
	}
	if displayName == "" {
		var fusionInput []map[string]any
		if in, ok := raw["input"].([]any); ok {
			for _, it := range in {
				if m, ok2 := it.(map[string]any); ok2 {
					fusionInput = append(fusionInput, m)
				}
			}
		}
		displayName = GetDefaultDisplayName(convID, fusionInput)
	}
	if len([]rune(displayName)) > maxConversationDisplayNameLength {
		common.ReplyErr(w, "display_name too long", http.StatusBadRequest)
		return
	}

	stream, _ := raw["stream"].(bool)
	models, _ := raw["models"].([]any)
	modelStrs := make([]string, 0, len(models))
	for _, m := range models {
		if s, ok := m.(string); ok && s != "" {
			modelStrs = append(modelStrs, s)
		}
	}
	dualReply := stream && len(modelStrs) >= 2

	query := ""
	if v, ok := raw["query"].(string); ok && strings.TrimSpace(v) != "" {
		query = strings.TrimSpace(v)
	}
	if query == "" {
		if v, ok := raw["content"].(string); ok && strings.TrimSpace(v) != "" {
			query = strings.TrimSpace(v)
		}
	}
	if query == "" {
		if in, ok := raw["input"].([]any); ok && len(in) > 0 {
			if m, ok2 := in[0].(map[string]any); ok2 {
				if s, ok3 := m["text"].(string); ok3 {
					query = strings.TrimSpace(s)
				}
				if query == "" {
					query, _ = m["content"].(string)
					query = strings.TrimSpace(query)
				}
			}
		}
	}
	if query == "" {
		common.ReplyErr(w, "query required", http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		userID = "0"
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	var searchConfigJSON json.RawMessage
	if conv != nil {
		if sc, ok := conv["search_config"]; ok {
			if b, err := json.Marshal(sc); err == nil {
				searchConfigJSON = b
			}
		}
	}
	var modelsJSON json.RawMessage
	if len(modelStrs) > 0 {
		if b, err := json.Marshal(modelStrs); err == nil {
			modelsJSON = b
		}
	}

	// Extract initial_plugin_settings from request body (only used on first message of a new conversation).
	var initialPluginSettings map[string]any
	if rawPS, ok := raw["initial_plugin_settings"].(map[string]any); ok {
		initialPluginSettings = rawPS
	}

	_, seq, err := ensureConversation(r.Context(), db, convID, displayName, searchConfigJSON, modelsJSON, userID, userName, initialPluginSettings)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "failed to ensure conversation", err), http.StatusInternalServerError)
		return
	}

	var histories []orm.ChatHistory
	db.Where("conversation_id = ?", convID).Order("seq ASC").Find(&histories)
	target := resolvePersistTarget(histories, raw, seq)
	upstreamHistories := historiesForUpstream(histories, target)
	sessionID := upstreamSessionID(convID)
	resourceContext, err := evolution.BuildChatResourceContext(r.Context(), db, userID, userName, sessionID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "build chat resource context failed", err), http.StatusInternalServerError)
		return
	}
	dbDisabledTools, err := listDisabledToolNames(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query disabled tools failed", http.StatusInternalServerError)
		return
	}
	if len(dbDisabledTools) > 0 {
		resourceContext.DisabledTools = mergeDisabledToolNames(resourceContext.DisabledTools, dbDisabledTools)
	}
	reqBody := buildChatRequestBody(r.Context(), db, convID, sessionID, query, upstreamHistories, raw, resourceContext, userID, target.Seq)
	if err := applyLocalFSPathsForChat(r.Context(), r, db, userID, reqBody); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "load local fs chat paths failed", err), http.StatusInternalServerError)
		return
	}
	if cnt, err := subagent.CountByConversation(r.Context(), db, convID); err == nil && cnt > 0 {
		reqBody["has_subagents"] = true
	}
	// Reconcile plugin_context with the DB-authoritative active session.
	// Rules:
	//   1. No plugin_context from frontend → inject from DB if an active session exists.
	//   2. Frontend sent plugin_context → cross-check with DB; overwrite any stale fields
	//      so Python always receives the ground-truth session_id / current_step.
	//
	// Resolve plugin_mode with correct priority:
	//   request body > conversation DB (loaded via applyChatRuntimeConfigs) > global default
	// applyChatRuntimeConfigs is called later, so we first apply it to get DB-resolved values,
	// then override with any explicit body value.
	if err := applyChatRuntimeConfigs(r.Context(), db, userID, reqBody); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "load chat runtime config failed", err), http.StatusInternalServerError)
		return
	}
	applyMCPRuntimeConfig(r.Context(), db, userID, reqBody)
	// resolvePluginModeWithFallback determines the effective plugin_mode for this request.
	// It is injected into plugin_context (below) so Python can use it; it is not sent
	// as a top-level reqBody field because Python reads it exclusively from plugin_context.
	pluginMode := resolvePluginModeWithFallback(raw, reqBody)

	// Promote enable_plugin and enable_subagent from agentic_config to top-level
	// so Python chat_routes can receive them as explicit parameters.
	if ac, ok := reqBody["agentic_config"].(map[string]any); ok {
		if v, ok := ac["enable_plugin"]; ok {
			reqBody["enable_plugin"] = v
		}
		if v, ok := ac["enable_subagent"]; ok {
			reqBody["enable_subagent"] = v
		}
	}

	if activeSess, err := plugin.GetLatestSession(r.Context(), db, convID); err == nil && activeSess != nil {
		existing, hasPC := reqBody["plugin_context"].(map[string]any)
		if !hasPC || existing == nil {
			// Case 1: inject from DB.
			reqBody["plugin_context"] = map[string]any{
				"session_id":   activeSess.ID,
				"plugin_id":    activeSess.PluginID,
				"current_step": activeSess.CurrentStepID,
				"plugin_mode":  pluginMode,
			}
			fmt.Printf("[PLUGIN_CONTEXT_INJECTED] conversation_id=%s session_id=%s plugin_id=%s current_step=%s plugin_mode=%s\n",
				convID, activeSess.ID, activeSess.PluginID, activeSess.CurrentStepID, pluginMode)
		} else {
			// Case 2: validate/correct stale fields from frontend.
			stale := false
			if sid, _ := existing["session_id"].(string); sid != activeSess.ID {
				existing["session_id"] = activeSess.ID
				stale = true
			}
			if pid, _ := existing["plugin_id"].(string); pid != activeSess.PluginID {
				existing["plugin_id"] = activeSess.PluginID
				stale = true
			}
			if cs, _ := existing["current_step"].(string); cs != activeSess.CurrentStepID {
				existing["current_step"] = activeSess.CurrentStepID
				stale = true
			}
			existing["plugin_mode"] = pluginMode
			if stale {
				fmt.Printf("[PLUGIN_CONTEXT_CORRECTED] conversation_id=%s session_id=%s plugin_id=%s current_step=%s\n",
					convID, activeSess.ID, activeSess.PluginID, activeSess.CurrentStepID)
			}
		}
	} else if _, hasPC := reqBody["plugin_context"]; hasPC {
		// No active session in DB but frontend sent a plugin_context — clear it to avoid
		// Python entering advance-step mode with a stale/non-existent session.
		delete(reqBody, "plugin_context")
		fmt.Printf("[PLUGIN_CONTEXT_CLEARED] conversation_id=%s no active session in DB\n", convID)
	}
	historyExt := buildChatHistoryExt(raw, query)
	baseURL := chatServiceURL()
	reqCtx := r.Context()
	stateStore := store.State()

	if !stream {
		handleNonStreamChat(w, reqCtx, db, stateStore, baseURL, reqBody, convID, query, target, historyExt)
		return
	}

	// run_in_background: create a background_chat task record so it appears in the
	// task center. Status is derived on read via resolveTaskStatus (chat_histories
	// presence), so no status callback is needed after the SSE drains.
	if runInBackground, _ := raw["run_in_background"].(bool); runInBackground {
		taskTitle := query
		if len([]rune(taskTitle)) > 40 {
			taskTitle = string([]rune(taskTitle)[:40]) + "..."
		}
		bgTask := &orm.TaskCenterTask{
			UserID:         userID,
			ConversationID: convID,
			TaskType:       "background_chat",
			Title:          &taskTitle,
			Status:         "running",
		}
		_ = taskcenter.CreateTask(reqCtx, db, bgTask)
	}

	handleStreamChat(w, r, db, stateStore, baseURL, reqBody, convID, query, target, dualReply, historyExt)
}

// ResumeChat text POST /api/v1/conversations:resumeChat
func ResumeChat(w http.ResponseWriter, r *http.Request) {
	resumeChatStream(w, r)
}

func resumeChatStream(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationID string `json:"conversation_id"`
		HistoryID      string `json:"history_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	convID := strings.TrimSpace(body.ConversationID)
	historyID := strings.TrimSpace(body.HistoryID)
	if convID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	var conv orm.Conversation
	if err := db.Where("id = ? AND create_user_id = ?", convID, userID).First(&conv).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "conversation not found", err), http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		common.ReplyErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()
	stateStore := store.State()
	if stateStore == nil {
		resumeFromDBOnly(db, convID, flusher, w)
		return
	}

	generatingIDs, _ := getGeneratingHistoryIDs(ctx, stateStore, convID)
	if len(generatingIDs) == 0 {
		resumeCompletedFromDB(db, convID, flusher, w)
		return
	}

	var multiInfo *MultiAnswerInfo
	for _, id := range generatingIDs {
		info, err := getMultiAnswerInfo(ctx, stateStore, convID, id)
		if err == nil && info != nil {
			multiInfo = info
			break
		}
	}

	if multiInfo != nil {
		resumeMultiAnswerChat(ctx, stateStore, convID, multiInfo, w, flusher)
		return
	}

	targetHistoryID := historyID
	if targetHistoryID == "" {
		targetHistoryID = generatingIDs[0]
	}
	resumeSingleAnswerChat(ctx, stateStore, convID, targetHistoryID, w, flusher)
}

func resumeFromDBOnly(db *gorm.DB, convID string, flusher http.Flusher, w http.ResponseWriter) {
	var last orm.ChatHistory
	if err := db.Where("conversation_id = ?", convID).Order("seq DESC").First(&last).Error; err != nil || last.ID == "" {
		writeSSEChunk(w, flusher, map[string]any{"finish_reason": "FINISH_REASON_UNKNOWN"})
		return
	}
	writeSSEChunk(w, flusher, map[string]any{
		"conversation_id": convID,
		"seq":             last.Seq,
		"message":         stripThinkTags(stripToolTags(last.Result)),
		"delta":           stripThinkTags(stripToolTags(last.Result)),
		"finish_reason":   "FINISH_REASON_STOP",
		"history_id":      last.ID,
	})
}

func resumeCompletedFromDB(db *gorm.DB, convID string, flusher http.Flusher, w http.ResponseWriter) {
	var last orm.ChatHistory
	if err := db.Where("conversation_id = ?", convID).Order("seq DESC").First(&last).Error; err == nil && last.ID != "" {
		writeSSEChunk(w, flusher, map[string]any{
			"conversation_id": convID,
			"seq":             last.Seq,
			"message":         stripThinkTags(stripToolTags(last.Result)),
			"delta":           stripThinkTags(stripToolTags(last.Result)),
			"finish_reason":   "FINISH_REASON_STOP",
			"history_id":      last.ID,
		})
		return
	}

	var mh []orm.MultiAnswersChatHistory
	if err := db.Where("conversation_id = ?", convID).Order("seq DESC, create_time DESC").Limit(2).Find(&mh).Error; err != nil || len(mh) == 0 {
		writeSSEChunk(w, flusher, map[string]any{"finish_reason": "FINISH_REASON_UNKNOWN"})
		return
	}
	for i, h := range mh {
		finish := ""
		if i == len(mh)-1 {
			finish = "FINISH_REASON_STOP"
		}
		writeSSEChunk(w, flusher, map[string]any{
			"conversation_id": convID,
			"seq":             h.Seq,
			"message":         stripThinkTags(stripToolTags(h.Result)),
			"delta":           stripThinkTags(stripToolTags(h.Result)),
			"finish_reason":   finish,
			"history_id":      h.ID,
		})
	}
}

func mergeChunksToFirstChunk(chunks []*ChatChunkResponse) *ChatChunkResponse {
	if len(chunks) == 0 {
		return nil
	}
	var fullDelta, fullReasoning string
	last := chunks[len(chunks)-1]
	for _, ch := range chunks {
		if ch == nil {
			continue
		}
		fullDelta += ch.Delta
		fullReasoning += ch.ReasoningContent
	}
	if last == nil {
		return nil
	}
	return &ChatChunkResponse{
		ConversationID:   last.ConversationID,
		Seq:              last.Seq,
		HistoryID:        last.HistoryID,
		Delta:            fullDelta,
		ReasoningContent: fullReasoning,
		Sources:          last.Sources,
		FinishReason:     last.FinishReason,
	}
}

func sendChunk(w http.ResponseWriter, flusher http.Flusher, ch *ChatChunkResponse) {
	if ch == nil {
		return
	}
	// Defaulttext finish_reason，text
	if ch.FinishReason == "" {
		ch.FinishReason = "FINISH_REASON_UNSPECIFIED"
	}
	writeSSEChunk(w, flusher, ch)
}

func resumeSingleAnswerChat(ctx context.Context, stateStore state.Store, convID, historyID string, w http.ResponseWriter, flusher http.Flusher) {
	status, _ := getChatStatus(ctx, stateStore, convID, historyID)
	chunks, _ := getChatChunks(ctx, stateStore, convID, historyID)

	first := mergeChunksToFirstChunk(chunks)
	if first != nil {
		sendChunk(w, flusher, first)
	}

	if status != nil && (status.Status == "completed" || status.Status == "stopped" || status.Status == "failed") {
		full := strings.TrimSpace(status.CurrentResult)
		seq := int32(0)
		var sources []any
		if first != nil {
			seq = first.Seq
			sources = first.Sources
		}
		if full != "" {
			current := ""
			if first != nil {
				current = first.Delta
			}
			if len(full) > len(current) && strings.HasPrefix(full, current) {
				sendChunk(w, flusher, &ChatChunkResponse{
					ConversationID: convID,
					Seq:            seq,
					HistoryID:      historyID,
					Delta:          full[len(current):],
					Sources:        sources,
				})
			}
		}
		sendChunk(w, flusher, &ChatChunkResponse{
			ConversationID: convID,
			Seq:            seq,
			HistoryID:      historyID,
			FinishReason:   "FINISH_REASON_STOP",
		})
		_ = clearChatData(context.Background(), stateStore, convID, historyID)
		return
	}

	lastIdx := int64(len(chunks) - 1)
	if lastIdx < 0 {
		lastIdx = -1
	}
	err := watchChatChunks(ctx, stateStore, convID, historyID, lastIdx, func(ch *ChatChunkResponse) error {
		sendChunk(w, flusher, ch)
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}

	finalStatus, _ := getChatStatus(context.Background(), stateStore, convID, historyID)
	if finalStatus != nil && (finalStatus.Status == "completed" || finalStatus.Status == "stopped") {
		sendChunk(w, flusher, &ChatChunkResponse{
			ConversationID: convID,
			HistoryID:      historyID,
			FinishReason:   "FINISH_REASON_STOP",
		})
		_ = clearChatData(context.Background(), stateStore, convID, historyID)
	}
}

func resumeMultiAnswerChat(ctx context.Context, stateStore state.Store, convID string, info *MultiAnswerInfo, w http.ResponseWriter, flusher http.Flusher) {
	primaryChunks, _ := getChatChunks(ctx, stateStore, convID, info.PrimaryHistoryID)
	secondaryChunks, _ := getChatChunks(ctx, stateStore, convID, info.SecondaryHistoryID)

	for _, ch := range primaryChunks {
		if ch != nil {
			ch.FinishReason = ""
			sendChunk(w, flusher, ch)
		}
	}
	for _, ch := range secondaryChunks {
		if ch != nil {
			ch.FinishReason = ""
			sendChunk(w, flusher, ch)
		}
	}

	primaryStatus, _ := getChatStatus(ctx, stateStore, convID, info.PrimaryHistoryID)
	secondaryStatus, _ := getChatStatus(ctx, stateStore, convID, info.SecondaryHistoryID)

	var wg sync.WaitGroup
	var writeMu sync.Mutex
	watchOne := func(historyID string, startIdx int64) {
		defer wg.Done()
		_ = watchChatChunks(ctx, stateStore, convID, historyID, startIdx, func(ch *ChatChunkResponse) error {
			if ch == nil {
				return nil
			}
			ch.FinishReason = ""
			writeMu.Lock()
			sendChunk(w, flusher, ch)
			writeMu.Unlock()
			return nil
		})
	}

	if primaryStatus != nil && primaryStatus.Status == "generating" {
		wg.Add(1)
		go watchOne(info.PrimaryHistoryID, int64(len(primaryChunks)-1))
	}
	if secondaryStatus != nil && secondaryStatus.Status == "generating" {
		wg.Add(1)
		go watchOne(info.SecondaryHistoryID, int64(len(secondaryChunks)-1))
	}
	wg.Wait()

	patchTail := func(historyID string) {
		st, _ := getChatStatus(context.Background(), stateStore, convID, historyID)
		if st == nil || st.CurrentResult == "" {
			return
		}
		list, _ := getChatChunks(context.Background(), stateStore, convID, historyID)
		merged := mergeChunksToFirstChunk(list)
		current := ""
		seq := int32(info.Seq)
		var sources []any
		if merged != nil {
			current = merged.Delta
			seq = merged.Seq
			sources = merged.Sources
		}
		full := st.CurrentResult
		if len(full) > len(current) && strings.HasPrefix(full, current) {
			sendChunk(w, flusher, &ChatChunkResponse{
				ConversationID: convID,
				Seq:            seq,
				HistoryID:      historyID,
				Delta:          full[len(current):],
				Sources:        sources,
			})
		}
	}
	patchTail(info.PrimaryHistoryID)
	patchTail(info.SecondaryHistoryID)

	sendChunk(w, flusher, &ChatChunkResponse{
		ConversationID: convID,
		Seq:            int32(info.Seq),
		HistoryID:      info.PrimaryHistoryID,
		FinishReason:   "FINISH_REASON_STOP",
	})
	sendChunk(w, flusher, &ChatChunkResponse{
		ConversationID: convID,
		Seq:            int32(info.Seq),
		HistoryID:      info.SecondaryHistoryID,
		FinishReason:   "FINISH_REASON_STOP",
	})

	if ctx.Err() == nil {
		_ = clearChatData(context.Background(), stateStore, convID, info.PrimaryHistoryID)
		_ = clearChatData(context.Background(), stateStore, convID, info.SecondaryHistoryID)
	}
}

// StopChatGeneration text POST /api/v1/conversations:stopChatGeneration
func StopChatGeneration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationID string `json:"conversation_id"`
		HistoryID      string `json:"history_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	convID := strings.TrimSpace(body.ConversationID)
	historyID := strings.TrimSpace(body.HistoryID)
	if convID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var conv orm.Conversation
	if err := store.DB().Where("id = ? AND create_user_id = ?", convID, userID).First(&conv).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "conversation not found", err), http.StatusNotFound)
		return
	}

	stateStore := store.State()
	if stateStore != nil {
		ids, _ := getGeneratingHistoryIDs(r.Context(), stateStore, convID)
		if len(ids) == 0 && historyID != "" {
			ids = append(ids, historyID)
		}
		for _, hid := range ids {
			_ = setChatCancelSignal(r.Context(), stateStore, convID, hid)
		}
	}

	// Interrupt any active plugin session steps.
	if db := store.DB(); db != nil {
		plugin.StopActivePluginSession(r.Context(), db, stateStore, convID)
	}

	// Notify Python ChatAgent to cancel any active chat session for this conversation.
	go plugin.NotifyChatCancel(convID)

	common.ReplyOK(w, nil)
}

// GetChatStatus text GET /api/v1/conversations/{conversation_id}:status
func GetChatStatus(w http.ResponseWriter, r *http.Request) {
	convID := conversationIDFromPath(r)
	if convID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var conv orm.Conversation
	if err := store.DB().Where("id = ? AND create_user_id = ?", convID, userID).First(&conv).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "conversation not found", err), http.StatusNotFound)
		return
	}
	isGenerating := false
	stateStore := store.State()
	if stateStore != nil {
		ids, _ := getGeneratingHistoryIDs(r.Context(), stateStore, convID)
		isGenerating = len(ids) > 0
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{"is_generating": isGenerating})
}

// GetConversation text GET /api/v1/conversations/{name}
func GetConversation(w http.ResponseWriter, r *http.Request) {
	name := conversationNameFromPath(r)
	convID := conversationIDFromName(name)
	if convID == "" {
		common.ReplyErr(w, "invalid conversation name", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var c orm.Conversation
	if err := store.DB().Where("id = ? AND create_user_id = ?", convID, userID).First(&c).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "conversation not found", err), http.StatusNotFound)
		return
	}

	// text search_config
	var searchCfg any
	if len(c.SearchConfig) > 0 {
		_ = json.Unmarshal(c.SearchConfig, &searchCfg)
	}
	// text models
	var models []string
	if len(c.Models) > 0 {
		_ = json.Unmarshal(c.Models, &models)
	} else if c.Model != "" {
		models = []string{c.Model}
	}

	// text
	var likeCnt, unlikeCnt int64
	db := store.DB()
	db.Model(&orm.ChatHistory{}).Where("conversation_id = ? AND feed_back = ?", c.ID, 1).Count(&likeCnt)
	db.Model(&orm.ChatHistory{}).Where("conversation_id = ? AND feed_back = ?", c.ID, 2).Count(&unlikeCnt)

	writeConversationJSON(w, http.StatusOK, map[string]any{
		"name":                  "conversations/" + c.ID,
		"conversation_id":       c.ID,
		"display_name":          c.DisplayName,
		"search_config":         searchCfg,
		"user":                  c.CreateUserName,
		"chat_times":            c.ChatTimes,
		"total_feedback_like":   likeCnt,
		"total_feedback_unlike": unlikeCnt,
		"create_time":           c.CreatedAt.UTC().Format(time.RFC3339),
		"update_time":           c.UpdatedAt.UTC().Format(time.RFC3339),
		"models":                models,
	})
}

func parseConversationHistoryPage(r *http.Request) (pageSize, offset int) {
	q := r.URL.Query()
	pageToken := strings.TrimSpace(q.Get("page_token"))
	pageSizeStr := strings.TrimSpace(q.Get("page_size"))

	pageSize = 20
	if pageSizeStr != "" {
		if v, err := strconv.Atoi(pageSizeStr); err == nil && v > 0 {
			pageSize = v
		}
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset = 0
	if pageToken != "" {
		if v, err := parseListPageToken(pageToken); err == nil && v >= 0 {
			offset = v
		}
	}
	return pageSize, offset
}

func loadConversationHistories(ctx context.Context, convID string) []orm.ChatHistory {
	var histories []orm.ChatHistory
	store.DB().Where("conversation_id = ?", convID).Order("seq DESC").Find(&histories)

	stateStore := store.State()
	if stateStore == nil {
		return histories
	}
	ids, _ := getGeneratingHistoryIDs(ctx, stateStore, convID)
	exists := make(map[string]struct{}, len(histories))
	for _, h := range histories {
		exists[h.ID] = struct{}{}
	}
	for _, hid := range ids {
		if _, ok := exists[hid]; ok {
			continue
		}
		in, err := getChatInput(ctx, stateStore, convID, hid)
		if err != nil || in == nil || strings.TrimSpace(in.RawContent) == "" {
			continue
		}
		ct := time.UnixMilli(in.CreatedAt)
		histories = append(histories, orm.ChatHistory{
			ID:             hid,
			Seq:            in.Seq,
			ConversationID: convID,
			RawContent:     in.RawContent,
			Content:        in.RawContent,
			Result:         "",
			Ext:            in.Ext,
			TimeMixin:      orm.TimeMixin{CreateTime: ct, UpdateTime: ct},
		})
	}
	sort.Slice(histories, func(i, j int) bool { return histories[i].Seq > histories[j].Seq })
	return histories
}

func chatHistoryToResponseItem(h orm.ChatHistory) map[string]any {
	var sources any
	if len(h.RetrievalResult) > 0 {
		var rr struct {
			Sources any `json:"sources"`
		}
		if err := json.Unmarshal(h.RetrievalResult, &rr); err == nil {
			sources = rr.Sources
		}
	}
	var input any
	var askPending any
	if len(h.Ext) > 0 {
		var ext struct {
			Input      any `json:"input"`
			AskPending any `json:"ask_pending"`
		}
		if err := json.Unmarshal(h.Ext, &ext); err == nil {
			input = ext.Input
			askPending = ext.AskPending
		}
	}
	item := map[string]any{
		"seq":             h.Seq,
		"query":           h.RawContent,
		"result":          stripThinkTags(stripToolTags(h.Result)),
		"id":              h.ID,
		"feed_back":       h.FeedBack,
		"sources":         sources,
		"input":           input,
		"reason":          h.Reason,
		"expected_answer": h.ExpectedAnswer,
		"create_time":     h.CreateTime.UTC().Format(time.RFC3339),
	}
	if askPending != nil {
		item["ask_pending"] = askPending
	}
	return item
}

func conversationHistoryResponseItems(histories []orm.ChatHistory) []map[string]any {
	list := make([]map[string]any, 0, len(histories))
	for _, h := range histories {
		list = append(list, chatHistoryToResponseItem(h))
	}
	return list
}

// GetConversationDetail text GET /api/v1/conversations/{name}:detail
func GetConversationDetail(w http.ResponseWriter, r *http.Request) {
	name := conversationNameFromPath(r)
	convID := conversationIDFromName(name)
	if convID == "" {
		common.ReplyErr(w, "invalid conversation name", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var c orm.Conversation
	if err := store.DB().Where("id = ? AND create_user_id = ?", convID, userID).First(&c).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "conversation not found", err), http.StatusNotFound)
		return
	}
	// textConversationtext
	var searchCfg any
	if len(c.SearchConfig) > 0 {
		_ = json.Unmarshal(c.SearchConfig, &searchCfg)
	}
	var models []string
	if len(c.Models) > 0 {
		_ = json.Unmarshal(c.Models, &models)
	} else if c.Model != "" {
		models = []string{c.Model}
	}

	var likeCnt, unlikeCnt int64
	db := store.DB()
	db.Model(&orm.ChatHistory{}).Where("conversation_id = ? AND feed_back = ?", c.ID, 1).Count(&likeCnt)
	db.Model(&orm.ChatHistory{}).Where("conversation_id = ? AND feed_back = ?", c.ID, 2).Count(&unlikeCnt)

	writeConversationJSON(w, http.StatusOK, map[string]any{
		"conversation": map[string]any{
			"name":                  "conversations/" + c.ID,
			"conversation_id":       c.ID,
			"display_name":          c.DisplayName,
			"search_config":         searchCfg,
			"user":                  c.CreateUserName,
			"chat_times":            c.ChatTimes,
			"total_feedback_like":   likeCnt,
			"total_feedback_unlike": unlikeCnt,
			"create_time":           c.CreatedAt.UTC().Format(time.RFC3339),
			"update_time":           c.UpdatedAt.UTC().Format(time.RFC3339),
			"models":                models,
			"enable_plugin":         c.EnablePlugin,
			"plugin_mode":           c.PluginMode,
			"enable_subagent":       c.EnableSubagent,
		},
	})
}

// GetConversationHistory text GET /api/v1/conversations/{name}:history
func GetConversationHistory(w http.ResponseWriter, r *http.Request) {
	name := conversationNameFromPath(r)
	convID := conversationIDFromName(name)
	if convID == "" {
		common.ReplyErr(w, "invalid conversation name", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	if err := store.DB().Where("id = ? AND create_user_id = ?", convID, userID).First(&orm.Conversation{}).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "conversation not found", err), http.StatusNotFound)
		return
	}

	pageSize, offset := parseConversationHistoryPage(r)
	histories := loadConversationHistories(r.Context(), convID)
	total := len(histories)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	page := histories[offset:end]

	nextToken := ""
	if end < total {
		nextToken = encodeListPageToken(end, pageSize, total)
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{
		"conversation_id": convID,
		"name":            "conversations/" + convID,
		"history":         conversationHistoryResponseItems(page),
		"total_size":      total,
		"next_page_token": nextToken,
	})
}

// DeleteConversation text DELETE /api/v1/conversations/{name}
func DeleteConversation(w http.ResponseWriter, r *http.Request) {
	name := conversationNameFromPath(r)
	convID := conversationIDFromName(name)
	if convID == "" {
		common.ReplyErr(w, "invalid conversation name", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	db := store.DB()
	res := db.Where("id = ? AND create_user_id = ?", convID, userID).Delete(&orm.Conversation{})
	if res.RowsAffected == 0 {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}
	db.Where("conversation_id = ?", convID).Delete(&orm.ChatHistory{})
	db.Where("conversation_id = ?", convID).Delete(&orm.MultiAnswersChatHistory{})
	// Cascade-delete task center entries for this conversation.
	db.Where("conversation_id = ?", convID).Delete(&orm.TaskCenterTask{})
	writeConversationJSON(w, http.StatusOK, map[string]any{})
}

// BatchDeleteConversations text POST /api/v1/conversations:batchDelete
func BatchDeleteConversations(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationIDs []string `json:"conversation_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if len(body.ConversationIDs) == 0 {
		common.ReplyErr(w, "conversation_ids required", http.StatusBadRequest)
		return
	}

	uniqueIDs := make([]string, 0, len(body.ConversationIDs))
	seen := make(map[string]struct{}, len(body.ConversationIDs))
	for _, id := range body.ConversationIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		common.ReplyErr(w, "conversation_ids required", http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	db := store.DB()

	var ownedIDs []string
	if err := db.Model(&orm.Conversation{}).
		Where("id IN ? AND create_user_id = ?", uniqueIDs, userID).
		Pluck("id", &ownedIDs).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query conversations failed", err), http.StatusInternalServerError)
		return
	}
	if len(ownedIDs) == 0 {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id IN ?", ownedIDs).Delete(&orm.Conversation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("conversation_id IN ?", ownedIDs).Delete(&orm.ChatHistory{}).Error; err != nil {
			return err
		}
		if err := tx.Where("conversation_id IN ?", ownedIDs).Delete(&orm.MultiAnswersChatHistory{}).Error; err != nil {
			return err
		}
		return tx.Where("conversation_id IN ?", ownedIDs).Delete(&orm.TaskCenterTask{}).Error
	}); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "batch delete conversations failed", err), http.StatusInternalServerError)
		return
	}

	writeConversationJSON(w, http.StatusOK, map[string]any{
		"deleted_count": len(ownedIDs),
		"deleted_ids":   ownedIDs,
	})
}

// ListConversations text GET /api/v1/conversations
func ListConversations(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	pageSize := 20
	if s := r.URL.Query().Get("page_size"); s != "" {
		if n, _ := strconv.Atoi(s); n > 0 && n <= 100 {
			pageSize = n
		}
	}
	offset := 0
	if s := r.URL.Query().Get("page_token"); s != "" {
		if n, _ := strconv.Atoi(s); n > 0 {
			offset = n
		}
	}

	db := store.DB()
	q := db.Model(&orm.Conversation{}).Where("create_user_id = ?", userID)
	if keyword != "" {
		q = q.Where("display_name LIKE ?", "%"+keyword+"%")
	}
	// Filter by is_task_conv when the caller passes the query param.
	// Accepted values: "true" → only task conversations, "false" → only regular conversations.
	// When absent, default to "false" (hide task conversations from the normal history list).
	isTaskConvParam := strings.TrimSpace(r.URL.Query().Get("is_task_conv"))
	switch isTaskConvParam {
	case "true":
		q = q.Where("is_task_conv = ?", true)
	case "false":
		// Explicit false: show only regular (non-task) conversations.
		q = q.Where("is_task_conv = ? OR is_task_conv IS NULL", false)
	default:
		// No filter param: show all conversations (both regular and task).
		// This path is hit when the frontend selects both "普通对话" and "Task 对话".
	}
	var total int64
	q.Count(&total)
	var list []orm.Conversation
	q.Order("updated_at DESC").Offset(offset).Limit(pageSize).Find(&list)

	items := make([]map[string]any, 0, len(list))
	for _, c := range list {
		// text search_config
		var searchCfg any
		if len(c.SearchConfig) > 0 {
			_ = json.Unmarshal(c.SearchConfig, &searchCfg)
		}
		// text models：text models text，text model
		var models []string
		if len(c.Models) > 0 {
			_ = json.Unmarshal(c.Models, &models)
		} else if c.Model != "" {
			models = []string{c.Model}
		}
		// text/text
		var likeCnt, unlikeCnt int64
		db.Model(&orm.ChatHistory{}).Where("conversation_id = ? AND feed_back = ?", c.ID, 1).Count(&likeCnt)
		db.Model(&orm.ChatHistory{}).Where("conversation_id = ? AND feed_back = ?", c.ID, 2).Count(&unlikeCnt)

		items = append(items, map[string]any{
			"name":                  "conversations/" + c.ID,
			"conversation_id":       c.ID,
			"display_name":          c.DisplayName,
			"search_config":         searchCfg,
			"user":                  c.CreateUserName,
			"chat_times":            c.ChatTimes,
			"total_feedback_like":   likeCnt,
			"total_feedback_unlike": unlikeCnt,
			"create_time":           c.CreatedAt.UTC().Format(time.RFC3339),
			"update_time":           c.UpdatedAt.UTC().Format(time.RFC3339),
			"models":                models,
			"is_task_conv":          c.IsTaskConv,
		})
	}
	nextToken := ""
	if offset+len(list) < int(total) {
		nextToken = strconv.Itoa(offset + len(list))
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{
		"conversations":   items,
		"total_size":      total,
		"next_page_token": nextToken,
	})
}

// SetChatHistory text POST /api/v1/conversations:setChatHistory
func SetChatHistory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SetHistoryID     string `json:"set_history_id"`
		DeletedHistoryID string `json:"deleted_history_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if body.SetHistoryID == "" {
		common.ReplyErr(w, "set_history_id required", http.StatusBadRequest)
		return
	}
	if body.DeletedHistoryID == "" {
		common.ReplyErr(w, "deleted_history_id required", http.StatusBadRequest)
		return
	}

	db := store.DB()
	now := time.Now()

	var selected orm.MultiAnswersChatHistory
	if err := db.Where("id = ?", body.SetHistoryID).First(&selected).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "set_history_id not found", err), http.StatusNotFound)
		return
	}
	var deleted orm.MultiAnswersChatHistory
	if err := db.Where("id = ?", body.DeletedHistoryID).First(&deleted).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "deleted_history_id not found", err), http.StatusNotFound)
		return
	}
	if selected.ConversationID == "" || selected.ConversationID != deleted.ConversationID {
		common.ReplyErr(w, "history ids are not in same conversation", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var conv orm.Conversation
	if err := db.Where("id = ? AND create_user_id = ?", selected.ConversationID, userID).First(&conv).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "conversation not found", err), http.StatusNotFound)
		return
	}

	var exists orm.ChatHistory
	if err := db.Where("id = ?", body.SetHistoryID).First(&exists).Error; err != nil {
		target := orm.ChatHistory{
			ID:              selected.ID,
			Seq:             selected.Seq,
			ConversationID:  selected.ConversationID,
			RawContent:      selected.RawContent,
			RetrievalResult: selected.RetrievalResult,
			Content:         selected.Content,
			Result:          selected.Result,
			ToolCallTurns:   nonNegativeToolCallTurns(int64(selected.ToolCallTurns)),
			FeedBack:        selected.FeedBack,
			Reason:          selected.Reason,
			Ext:             selected.Ext,
			Version:         "2.3",
			TimeMixin:       orm.TimeMixin{CreateTime: now, UpdateTime: now},
		}
		if err := db.Create(&target).Error; err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "set history failed", err), http.StatusInternalServerError)
			return
		}
		recordConversationIdleAfterPersist(context.Background(), db, store.State(), selected.ConversationID, userID, selected.ID, now, selected.RawContent, stripToolTags(selected.Result))
	}

	_ = db.Where("id IN ?", []string{body.SetHistoryID, body.DeletedHistoryID}).Delete(&orm.MultiAnswersChatHistory{}).Error
	writeConversationJSON(w, http.StatusOK, map[string]any{"history_id": body.SetHistoryID})
}

func parseFeedbackType(raw json.RawMessage) (int32, error) {
	var tInt int32
	if err := json.Unmarshal(raw, &tInt); err == nil {
		return tInt, nil
	}

	var tStr string
	if err := json.Unmarshal(raw, &tStr); err != nil {
		return 0, err
	}
	s := strings.TrimSpace(strings.ToUpper(tStr))
	switch s {
	case "FEED_BACK_TYPE_UNSPECIFIED", "UNSPECIFIED":
		return 0, nil
	case "FEED_BACK_TYPE_LIKE", "LIKE":
		return 1, nil
	case "FEED_BACK_TYPE_UNLIKE", "UNLIKE":
		return 2, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return int32(n), nil
	}
	return 0, errors.New("invalid feedback type")
}

// FeedBackChatHistory text POST /api/v1/conversations:feedBackChatHistory
func FeedBackChatHistory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HistoryID      string          `json:"history_id"`
		Type           json.RawMessage `json:"type"`
		Reason         string          `json:"reason,omitempty"`
		ExpectedAnswer string          `json:"expected_answer,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if body.HistoryID == "" {
		common.ReplyErr(w, "history_id required", http.StatusBadRequest)
		return
	}
	feedbackType, err := parseFeedbackType(body.Type)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if feedbackType < 0 || feedbackType > 2 {
		common.ReplyErr(w, "feedback type must be 0/1/2", http.StatusBadRequest)
		return
	}

	db := store.DB()
	now := time.Now()
	var target orm.ChatHistory
	if err := db.Where("id = ?", body.HistoryID).First(&target).Error; err != nil {
		common.ReplyErr(w, "history not found", http.StatusNotFound)
		return
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.ChatHistory{}).
			Where("conversation_id = ? AND seq = ?", target.ConversationID, target.Seq).
			Updates(map[string]any{
				"feed_back":       0,
				"reason":          "",
				"expected_answer": "",
				"update_time":     now,
			}).Error; err != nil {
			return err
		}

		updates := map[string]any{
			"feed_back":       feedbackType,
			"reason":          "",
			"expected_answer": "",
			"update_time":     now,
		}
		if feedbackType == 2 {
			updates["reason"] = body.Reason
			updates["expected_answer"] = body.ExpectedAnswer
		}
		return tx.Model(&orm.ChatHistory{}).Where("id = ?", body.HistoryID).Updates(updates).Error
	}); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "update feedback failed", err), http.StatusInternalServerError)
		return
	}

	writeConversationJSON(w, http.StatusOK, map[string]any{})
}

// GetMultiAnswersSwitchStatus text GET /api/v1/conversation:switchStatus
func GetMultiAnswersSwitchStatus(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var row orm.MultiAnswersSwitch
	err := store.DB().Where("create_user_id = ?", userID).First(&row).Error
	st := int32(0)
	if err == nil {
		st = row.Status
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{"status": st})
}

// SetMultiAnswersSwitchStatus text POST /api/v1/conversation:switchStatus
func SetMultiAnswersSwitchStatus(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		userID = "0"
	}
	var body struct {
		Status int32 `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if body.Status != 0 && body.Status != 1 {
		common.ReplyErr(w, "status must be 0 or 1", http.StatusBadRequest)
		return
	}
	db := store.DB()
	now := time.Now()
	var row orm.MultiAnswersSwitch
	if db.Where("create_user_id = ?", userID).First(&row).Error != nil {
		row = orm.MultiAnswersSwitch{
			Status: body.Status,
			BaseModel: orm.BaseModel{
				CreateUserID:   userID,
				CreateUserName: userName,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		}
		db.Create(&row)
	} else {
		db.Model(&row).Updates(map[string]any{"status": body.Status, "updated_at": now})
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{"status": body.Status})
}

// StreamConvEvents is GET /conversations/{conversation_id}/events.
// It opens a long-lived SSE connection that replays all existing ConvEvents for the
// conversation and then tails new ones in real time. The frontend subscribes once per
// active conversation and uses the events to update TaskCenter and PluginPanel without
// depending on any specific chat-turn history_id stream.
func StreamConvEvents(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	convID := strings.TrimSpace(vars["conversation_id"])
	if convID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}

	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	var conv orm.Conversation
	if err := db.Where("id = ? AND create_user_id = ?", convID, userID).First(&conv).Error; err != nil {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		common.ReplyErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	stateStore := store.State()
	if stateStore == nil {
		// No state backend — nothing to stream; send a keepalive and return.
		fmt.Fprintf(w, "data: {}\n\n")
		flusher.Flush()
		return
	}

	ctx := r.Context()
	_ = WatchConvEvents(ctx, stateStore, convID, -1, func(ev *ConvEvent) error {
		bs, err := json.Marshal(ev)
		if err != nil {
			return nil
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", bs); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
}
