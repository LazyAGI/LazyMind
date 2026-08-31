package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/state"
)

const (
	chatStreamKeyPrefix = "rag/chat/stream:%s:%s"
	chatStatusKeyPrefix = "rag/chat/status:%s"
	chatStopKeyPrefix   = "rag/chat/stop:%s:%s"
	chatMultiKeyPrefix  = "rag/chat/multi:%s:%s"
	chatInputKeyPrefix  = "rag/chat/input:%s:%s"

	// convEventsKeyPrefix is a conversation-level event LIST, keyed only by conversation_id.
	// It multiplexes independent-task changes and Workflow lifecycle events across
	// chat turns so the frontend needs one background stream per conversation.
	convEventsKeyPrefix = "rag/conv/events:%s"

	chatCacheExpireTime  = time.Hour * 2
	chatStopExpireTime   = 15 * time.Minute
	chatCancelPollTime   = 500 * time.Millisecond
	convEventsExpireTime = time.Hour * 24
)

type ChatStatus struct {
	Status        string       `json:"status"`
	RunID         string       `json:"run_id"`
	RunTerminal   *RunTerminal `json:"run_terminal,omitempty"`
	CurrentResult string       `json:"current_result"`
	LastUpdate    int64        `json:"last_update"`
	TotalChunks   int32        `json:"total_chunks"`
}

type ChatInput struct {
	RawContent string          `json:"raw_content"`
	Seq        int             `json:"seq"`
	CreatedAt  int64           `json:"created_at"`
	Ext        json.RawMessage `json:"ext,omitempty"`
}

type MultiAnswerInfo struct {
	PrimaryHistoryID   string `json:"primary_history_id"`
	SecondaryHistoryID string `json:"secondary_history_id"`
	Seq                int    `json:"seq"`
	CreatedAt          int64  `json:"created_at"`
}

type ChatDeltaMode string

const (
	ChatDeltaModeAppend  ChatDeltaMode = "append"
	ChatDeltaModeReplace ChatDeltaMode = "replace"
)

type ChatChunkResponse struct {
	ConversationID        string                       `json:"conversation_id"`
	Seq                   int32                        `json:"seq"`
	Message               string                       `json:"message"`
	Delta                 string                       `json:"delta"`
	DeltaMode             ChatDeltaMode                `json:"delta_mode,omitempty"`
	HistoryID             string                       `json:"history_id"`
	Sources               []any                        `json:"sources,omitempty"`
	PromptQuestions       []string                     `json:"prompt_questions,omitempty"`
	ReasoningContent      string                       `json:"reasoning_content,omitempty"`
	ThinkingDurationS     int64                        `json:"thinking_duration_s,omitempty"`
	ToolCallTurns         int                          `json:"tool_call_turns,omitempty"`
	ExternalEventSequence int64                        `json:"external_event_sequence,omitempty"`
	Execution             *externalExecutionProjection `json:"execution,omitempty"`
	TaskCreated           *TaskCreatedNotice           `json:"task_created,omitempty"`
	ArtifactCreated       *ConversationArtifactDTO     `json:"artifact_created,omitempty"`
	AskPending            *AskPendingEvent             `json:"ask_pending,omitempty"`
	ToolLimitPending      *ToolLimitPendingEvent       `json:"tool_limit_pending,omitempty"`
	IntentUpdated         *IntentUpdatedEvent          `json:"intent_updated,omitempty"`
	RuntimeEvent          *ChatRuntimeEvent            `json:"runtime_event,omitempty"`
}

// TaskCreatedNotice notifies the frontend that an independent SubAgent task exists.
// Subsequent changes arrive through the conversation event stream.
type TaskCreatedNotice struct {
	TaskID            string `json:"task_id"`
	TriggerHistoryID  string `json:"trigger_history_id"`
	Title             string `json:"title"`
	AgentType         string `json:"agent_type"`
	Mode              string `json:"mode"`
	Status            string `json:"status"`
	SeqInConversation int    `json:"seq_in_conversation"`
	// WorkflowSessionID is set when the task is a Workflow Step (agent_type='workflow_step').
	WorkflowSessionID string `json:"workflow_session_id,omitempty"`
}

func chatStatusKey(conversationID string) string {
	return fmt.Sprintf(chatStatusKeyPrefix, conversationID)
}
func chatStreamKey(cid, hid string) string { return fmt.Sprintf(chatStreamKeyPrefix, cid, hid) }
func chatStopKey(cid, hid string) string   { return fmt.Sprintf(chatStopKeyPrefix, cid, hid) }
func chatMultiKey(cid, primaryHID string) string {
	return fmt.Sprintf(chatMultiKeyPrefix, cid, primaryHID)
}
func chatInputKey(cid, hid string) string { return fmt.Sprintf(chatInputKeyPrefix, cid, hid) }
func convEventsKey(cid string) string     { return fmt.Sprintf(convEventsKeyPrefix, cid) }

func setChatStatus(ctx context.Context, stateStore state.Store, conversationID, historyID, status, currentResult string) error {
	return setChatRuntimeStatus(ctx, stateStore, conversationID, historyID, status, currentResult, "", nil)
}

func setChatRuntimeStatus(ctx context.Context, stateStore state.Store, conversationID, historyID, status, currentResult, runID string, terminal *RunTerminal) error {
	if status != "generating" && runID != "" {
		if current, err := getChatStatus(ctx, stateStore, conversationID, historyID); err == nil &&
			current.RunID != "" && current.RunID != runID {
			log.Logger.Info().Str("conversation_id", conversationID).Str("history_id", historyID).
				Str("run_id", runID).Str("current_run_id", current.RunID).
				Msg("ignored stale chat status terminal")
			return nil
		}
	}
	key := chatStatusKey(conversationID)
	totalChunks := int32(0)
	chunks, _ := getChatChunks(ctx, stateStore, conversationID, historyID)
	if len(chunks) > 0 {
		totalChunks = int32(len(chunks))
	}
	data := ChatStatus{Status: status, RunID: runID, RunTerminal: terminal, CurrentResult: currentResult, LastUpdate: time.Now().Unix(), TotalChunks: totalChunks}
	bs, _ := json.Marshal(data)
	if err := stateStore.HSet(ctx, key, map[string]any{historyID: string(bs)}, chatCacheExpireTime); err != nil {
		return err
	}
	return nil
}

func getGeneratingHistoryIDs(ctx context.Context, stateStore state.Store, conversationID string) ([]string, error) {
	m, err := stateStore.HGetAll(ctx, chatStatusKey(conversationID))
	if err != nil {
		return nil, err
	}
	var ids []string
	for hid, bs := range m {
		var st ChatStatus
		if json.Unmarshal([]byte(bs), &st) != nil {
			continue
		}
		if st.Status == "generating" {
			ids = append(ids, hid)
		}
	}
	return ids, nil
}

// reconcileGeneratingExternalChatStatuses heals the derived chat cache from
// durable External Chat runs. Internal Chat histories are deliberately left
// untouched because their lifecycle is not owned by the External Chat store.
func reconcileGeneratingExternalChatStatuses(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	owner, conversationID string,
) ([]string, error) {
	ids, err := getGeneratingHistoryIDs(ctx, stateStore, conversationID)
	if err != nil || len(ids) == 0 || db == nil {
		return ids, err
	}
	var runs []orm.ExternalChatRun
	if err := db.WithContext(ctx).
		Where("actor_user_id = ? AND conversation_id = ? AND history_id IN ?", owner, conversationID, ids).
		Order("created_at DESC").Find(&runs).Error; err != nil {
		return ids, err
	}
	latest := make(map[string]orm.ExternalChatRun, len(runs))
	for _, run := range runs {
		if _, exists := latest[run.HistoryID]; !exists {
			latest[run.HistoryID] = run
		}
	}
	remaining := make([]string, 0, len(ids))
	for _, historyID := range ids {
		run, exists := latest[historyID]
		if !exists || !externalRunTerminal(run.Status) {
			remaining = append(remaining, historyID)
			continue
		}
		projected, err := projectExternalChatRunCacheRecord(ctx, db, stateStore, run)
		if err != nil {
			return ids, err
		}
		if !projected {
			remaining = append(remaining, historyID)
		}
	}
	return remaining, nil
}

func projectExternalChatRunCache(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	owner, runID string,
) error {
	if db == nil || stateStore == nil {
		return nil
	}
	var run orm.ExternalChatRun
	if err := db.WithContext(ctx).
		Where("id = ? AND actor_user_id = ?", runID, owner).Take(&run).Error; err != nil {
		return err
	}
	if !externalRunTerminal(run.Status) {
		return nil
	}
	_, err := projectExternalChatRunCacheRecord(ctx, db, stateStore, run)
	return err
}

func projectExternalChatRunCacheRecord(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	run orm.ExternalChatRun,
) (bool, error) {
	var history orm.ChatHistory
	if err := db.WithContext(ctx).Select("result", "run_id").
		Where("id = ? AND run_id = ?", run.HistoryID, run.ID).Take(&history).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	terminalEvent := externalRunTerminalEvent(run.ID, run.Status, history.Result != "")
	terminal, _ := terminalEvent.Terminal()
	if err := setChatRuntimeStatus(ctx, stateStore, run.ConversationID, run.HistoryID, terminal.Status, history.Result, run.ID, terminal); err != nil {
		return false, err
	}
	return true, nil
}

func getChatStatus(ctx context.Context, stateStore state.Store, conversationID, historyID string) (*ChatStatus, error) {
	bs, err := stateStore.HGet(ctx, chatStatusKey(conversationID), historyID)
	if err != nil {
		return nil, err
	}
	var st ChatStatus
	if err := json.Unmarshal(bs, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func clearChatData(ctx context.Context, stateStore state.Store, conversationID, historyID string) error {
	key := chatStatusKey(conversationID)
	_ = stateStore.HDel(ctx, key, historyID)
	_ = stateStore.Del(ctx, chatStreamKey(conversationID, historyID))
	_ = stateStore.Del(ctx, chatInputKey(conversationID, historyID))
	_ = stateStore.Del(ctx, chatStopKey(conversationID, historyID))
	return nil
}

func setChatInput(ctx context.Context, stateStore state.Store, conversationID, historyID, rawContent string, seq int, ext json.RawMessage) error {
	data := ChatInput{RawContent: rawContent, Seq: seq, CreatedAt: time.Now().UnixMilli(), Ext: ext}
	bs, _ := json.Marshal(data)
	return stateStore.Set(ctx, chatInputKey(conversationID, historyID), bs, chatCacheExpireTime)
}

func getChatInput(ctx context.Context, stateStore state.Store, conversationID, historyID string) (*ChatInput, error) {
	bs, err := stateStore.Get(ctx, chatInputKey(conversationID, historyID))
	if err != nil {
		return nil, err
	}
	var in ChatInput
	if err := json.Unmarshal(bs, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

func appendChatChunk(ctx context.Context, stateStore state.Store, conversationID, historyID string, chunk *ChatChunkResponse) error {
	bs, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	key := chatStreamKey(conversationID, historyID)
	if err := stateStore.RPush(ctx, key, bs, chatCacheExpireTime); err != nil {
		return err
	}
	return nil
}

func getChatChunks(ctx context.Context, stateStore state.Store, conversationID, historyID string) ([]*ChatChunkResponse, error) {
	return getChatChunksFrom(ctx, stateStore, conversationID, historyID, 0)
}

func getChatChunksFrom(ctx context.Context, stateStore state.Store, conversationID, historyID string, from int64) ([]*ChatChunkResponse, error) {
	key := chatStreamKey(conversationID, historyID)
	list, err := stateStore.LRange(ctx, key, from, -1)
	if err != nil {
		return nil, err
	}
	out := make([]*ChatChunkResponse, 0, len(list))
	for _, s := range list {
		var c ChatChunkResponse
		if json.Unmarshal([]byte(s), &c) != nil {
			continue
		}
		out = append(out, &c)
	}
	return out, nil
}

func setChatCancelSignal(ctx context.Context, stateStore state.Store, conversationID, historyID string) error {
	key := chatStopKey(conversationID, historyID)
	if err := stateStore.LPush(ctx, key, []byte("1"), chatStopExpireTime); err != nil {
		return err
	}
	return nil
}

func watchChatCancelSignal(ctx context.Context, stateStore state.Store, conversationID, historyID string) (bool, error) {
	key := chatStopKey(conversationID, historyID)
	return retryChatCancelSignal(ctx, func(waitCtx context.Context) (bool, error) {
		return stateStore.LPop(waitCtx, key)
	}, func(err error, delay time.Duration) {
		log.Logger.Warn().Err(err).
			Str("conversation_id", conversationID).
			Str("history_id", historyID).
			Dur("retry_in", delay).
			Msg("chat cancel watcher state read failed; retrying")
	}, chatCancelPollTime, 100*time.Millisecond, 2*time.Second)
}

func cancelChatOnStop(ctx context.Context, stateStore state.Store, conversationID, historyID string, cancel context.CancelFunc) {
	receivedStop, _ := watchChatCancelSignal(ctx, stateStore, conversationID, historyID)
	if receivedStop {
		cancel()
	}
}

func retryChatCancelSignal(
	ctx context.Context,
	poll func(context.Context) (bool, error),
	onRetry func(error, time.Duration),
	pollInterval, initialRetryDelay, maxRetryDelay time.Duration,
) (bool, error) {
	retryDelay := initialRetryDelay
	for {
		received, err := poll(ctx)
		if err == nil {
			retryDelay = initialRetryDelay
			if received {
				return true, nil
			}
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if err := waitChatCancelPoll(ctx, pollInterval); err != nil {
				return false, err
			}
			continue
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if onRetry != nil {
			onRetry(err, retryDelay)
		}
		if err := waitChatCancelPoll(ctx, retryDelay); err != nil {
			return false, err
		}
		if retryDelay < maxRetryDelay {
			retryDelay *= 2
			if retryDelay > maxRetryDelay {
				retryDelay = maxRetryDelay
			}
		}
	}
}

func waitChatCancelPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func watchChatChunks(ctx context.Context, stateStore state.Store, conversationID, historyID string, lastIndex int64, callback func(*ChatChunkResponse) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			chunks, err := getChatChunksFrom(ctx, stateStore, conversationID, historyID, lastIndex+1)
			if err != nil {
				return err
			}
			for _, c := range chunks {
				if err := callback(c); err != nil {
					return err
				}
				lastIndex++
			}
			st, _ := getChatStatus(ctx, stateStore, conversationID, historyID)
			if st != nil {
				switch st.Status {
				case "completed", "interrupted", "failed", "cancelled":
					return nil
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func setMultiAnswerInfo(ctx context.Context, stateStore state.Store, conversationID, primaryHistoryID, secondaryHistoryID string, seq int) error {
	key := chatMultiKey(conversationID, primaryHistoryID)
	data := MultiAnswerInfo{
		PrimaryHistoryID:   primaryHistoryID,
		SecondaryHistoryID: secondaryHistoryID,
		Seq:                seq,
		CreatedAt:          time.Now().Unix(),
	}
	bs, _ := json.Marshal(data)
	return stateStore.Set(ctx, key, bs, chatCacheExpireTime)
}

func getMultiAnswerInfo(ctx context.Context, stateStore state.Store, conversationID, primaryHistoryID string) (*MultiAnswerInfo, error) {
	bs, err := stateStore.Get(ctx, chatMultiKey(conversationID, primaryHistoryID))
	if err != nil {
		return nil, err
	}
	var info MultiAnswerInfo
	if err := json.Unmarshal(bs, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ConvEvent is a conversation-level notification pushed to the frontend via the
// /conversations/{id}/events SSE endpoint. It is independent of any chat turn.
type ConvEvent struct {
	Type    string `json:"type"`    // task_created | workflow_artifact_updated | step_waiting | workflow_completed | workflow_error | driver_input | auto_chat_started | ask_pending
	Payload any    `json:"payload"` // *TaskCreatedNotice or plugin lifecycle payload map
	// Replayed is transport metadata added by StreamConvEvents. It is never
	// persisted. Consumers must not re-run command-like side effects for replayed
	// events (for example driver_input or auto_chat_started).
	Replayed bool `json:"replayed,omitempty"`
}

// AppendConvEvent appends a ConvEvent to the conversation-level event LIST.
// It is safe to call concurrently and expires after convEventsExpireTime.
// Do not trim this LIST while WatchConvEvents uses a positional cursor: removing
// entries from the head shifts every index and can permanently hide new events.
// High-volume task token events use the dedicated per-task stream instead.
func AppendConvEvent(ctx context.Context, stateStore state.Store, conversationID string, ev *ConvEvent) error {
	if stateStore == nil || conversationID == "" || ev == nil {
		return nil
	}
	bs, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	key := convEventsKey(conversationID)
	return stateStore.RPush(ctx, key, bs, convEventsExpireTime)
}

// WatchConvEvents long-polls the conversation-level event LIST starting from lastIndex+1
// and calls callback for each new ConvEvent. It returns when ctx is cancelled.
func WatchConvEvents(ctx context.Context, stateStore state.Store, conversationID string, lastIndex int64, callback func(int64, *ConvEvent) error) error {
	if stateStore == nil {
		return nil
	}
	key := convEventsKey(conversationID)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			list, err := stateStore.LRange(ctx, key, lastIndex+1, -1)
			if err != nil {
				return err
			}
			for _, s := range list {
				var ev ConvEvent
				if json.Unmarshal([]byte(s), &ev) != nil {
					lastIndex++
					continue
				}
				lastIndex++
				if err := callback(lastIndex, &ev); err != nil {
					return err
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}
