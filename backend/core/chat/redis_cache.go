package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lazymind/core/state"
)

const (
	chatStreamKeyPrefix = "rag/chat/stream:%s:%s"
	chatStatusKeyPrefix = "rag/chat/status:%s"
	chatStopKeyPrefix   = "rag/chat/stop:%s:%s"
	chatMultiKeyPrefix  = "rag/chat/multi:%s:%s"
	chatInputKeyPrefix  = "rag/chat/input:%s:%s"

	// convEventsKeyPrefix is a conversation-level event LIST, keyed only by conversation_id.
	// It carries task_created and plugin lifecycle events across all chat turns so that
	// the frontend can subscribe at conversation granularity rather than per history_id.
	convEventsKeyPrefix = "rag/conv/events:%s"

	chatCacheExpireTime  = time.Hour * 2
	chatStopExpireTime   = 15 * time.Minute
	convEventsExpireTime = time.Hour * 24
	convEventsMaxLen     = int64(1000)
)

type ChatStatus struct {
	Status        string `json:"status"`
	CurrentResult string `json:"current_result"`
	LastUpdate    int64  `json:"last_update"`
	TotalChunks   int32  `json:"total_chunks"`
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

type ChatChunkResponse struct {
	ConversationID    string             `json:"conversation_id"`
	Seq               int32              `json:"seq"`
	Message           string             `json:"message"`
	Delta             string             `json:"delta"`
	FinishReason      string             `json:"finish_reason"`
	HistoryID         string             `json:"history_id"`
	Sources           []any              `json:"sources,omitempty"`
	PromptQuestions   []string           `json:"prompt_questions,omitempty"`
	ReasoningContent  string             `json:"reasoning_content,omitempty"`
	ThinkingDurationS int64              `json:"thinking_duration_s,omitempty"`
	TaskCreated       *TaskCreatedNotice `json:"task_created,omitempty"`
	AskPending        *AskPendingEvent   `json:"ask_pending,omitempty"`
}

// TaskCreatedNotice notifies the frontend (main SSE) that a SubAgent task was created,
// so it can subscribe to the corresponding Task SSE stream.
type TaskCreatedNotice struct {
	TaskID            string `json:"task_id"`
	Title             string `json:"title"`
	AgentType         string `json:"agent_type"`
	Mode              string `json:"mode"`
	Status            string `json:"status"`
	SeqInConversation int    `json:"seq_in_conversation"`
	// PluginSessionID is set when the task is a Plugin Step (agent_type='plugin_step').
	PluginSessionID string `json:"plugin_session_id,omitempty"`
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
	key := chatStatusKey(conversationID)
	totalChunks := int32(0)
	chunks, _ := getChatChunks(ctx, stateStore, conversationID, historyID)
	if len(chunks) > 0 {
		totalChunks = int32(len(chunks))
	}
	data := ChatStatus{Status: status, CurrentResult: currentResult, LastUpdate: time.Now().Unix(), TotalChunks: totalChunks}
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

func watchChatCancelSignal(ctx context.Context, stateStore state.Store, conversationID, historyID string) error {
	key := chatStopKey(conversationID, historyID)
	return stateStore.BLPop(ctx, key, 0)
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
				case "completed", "stopped", "failed":
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
	Type    string `json:"type"`    // task_created | step_waiting | plugin_completed | plugin_error | driver_input | auto_chat_started | ask_pending
	Payload any    `json:"payload"` // *TaskCreatedNotice or plugin lifecycle payload map
}

// AppendConvEvent appends a ConvEvent to the conversation-level event LIST.
// It is safe to call concurrently. The LIST is capped at convEventsMaxLen entries
// (oldest dropped) and expires after convEventsExpireTime.
func AppendConvEvent(ctx context.Context, stateStore state.Store, conversationID string, ev *ConvEvent) error {
	if stateStore == nil || conversationID == "" || ev == nil {
		return nil
	}
	bs, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	key := convEventsKey(conversationID)
	if err := stateStore.RPush(ctx, key, bs, convEventsExpireTime); err != nil {
		return err
	}
	if trimmer, ok := stateStore.(interface {
		LTrim(context.Context, string, int64, int64) error
	}); ok {
		return trimmer.LTrim(ctx, key, -convEventsMaxLen, -1)
	}
	return nil
}

// WatchConvEvents long-polls the conversation-level event LIST starting from lastIndex+1
// and calls callback for each new ConvEvent. It returns when ctx is cancelled.
func WatchConvEvents(ctx context.Context, stateStore state.Store, conversationID string, lastIndex int64, callback func(*ConvEvent) error) error {
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
				if err := callback(&ev); err != nil {
					return err
				}
				lastIndex++
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}
