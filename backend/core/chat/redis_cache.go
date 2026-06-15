package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	chatStreamKeyPrefix = "rag/chat/stream:%s:%s"
	chatStatusKeyPrefix = "rag/chat/status:%s"
	chatStopKeyPrefix   = "rag/chat/stop:%s:%s"
	chatMultiKeyPrefix  = "rag/chat/multi:%s:%s"
	chatInputKeyPrefix  = "rag/chat/input:%s:%s"

	chatCacheExpireTime = time.Hour * 2
	chatStopExpireTime  = 15 * time.Minute
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

func setChatStatus(ctx context.Context, rdb *redis.Client, conversationID, historyID, status, currentResult string) error {
	key := chatStatusKey(conversationID)
	totalChunks := int32(0)
	chunks, _ := getChatChunks(ctx, rdb, conversationID, historyID)
	if len(chunks) > 0 {
		totalChunks = int32(len(chunks))
	}
	data := ChatStatus{Status: status, CurrentResult: currentResult, LastUpdate: time.Now().Unix(), TotalChunks: totalChunks}
	bs, _ := json.Marshal(data)
	if err := rdb.HSet(ctx, key, historyID, bs).Err(); err != nil {
		return err
	}
	return rdb.Expire(ctx, key, chatCacheExpireTime).Err()
}

func getGeneratingHistoryIDs(ctx context.Context, rdb *redis.Client, conversationID string) ([]string, error) {
	m, err := rdb.HGetAll(ctx, chatStatusKey(conversationID)).Result()
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

func getChatStatus(ctx context.Context, rdb *redis.Client, conversationID, historyID string) (*ChatStatus, error) {
	bs, err := rdb.HGet(ctx, chatStatusKey(conversationID), historyID).Bytes()
	if err != nil {
		return nil, err
	}
	var st ChatStatus
	if err := json.Unmarshal(bs, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func clearChatData(ctx context.Context, rdb *redis.Client, conversationID, historyID string) error {
	key := chatStatusKey(conversationID)
	_ = rdb.HDel(ctx, key, historyID).Err()
	_ = rdb.Del(ctx, chatStreamKey(conversationID, historyID)).Err()
	_ = rdb.Del(ctx, chatInputKey(conversationID, historyID)).Err()
	return nil
}

func setChatInput(ctx context.Context, rdb *redis.Client, conversationID, historyID, rawContent string, seq int, ext json.RawMessage) error {
	data := ChatInput{RawContent: rawContent, Seq: seq, CreatedAt: time.Now().UnixMilli(), Ext: ext}
	bs, _ := json.Marshal(data)
	return rdb.Set(ctx, chatInputKey(conversationID, historyID), bs, chatCacheExpireTime).Err()
}

func getChatInput(ctx context.Context, rdb *redis.Client, conversationID, historyID string) (*ChatInput, error) {
	bs, err := rdb.Get(ctx, chatInputKey(conversationID, historyID)).Bytes()
	if err != nil {
		return nil, err
	}
	var in ChatInput
	if err := json.Unmarshal(bs, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

func appendChatChunk(ctx context.Context, rdb *redis.Client, conversationID, historyID string, chunk *ChatChunkResponse) error {
	bs, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	key := chatStreamKey(conversationID, historyID)
	if err := rdb.RPush(ctx, key, bs).Err(); err != nil {
		return err
	}
	return rdb.Expire(ctx, key, chatCacheExpireTime).Err()
}

func getChatChunks(ctx context.Context, rdb *redis.Client, conversationID, historyID string) ([]*ChatChunkResponse, error) {
	return getChatChunksFrom(ctx, rdb, conversationID, historyID, 0)
}

func getChatChunksFrom(ctx context.Context, rdb *redis.Client, conversationID, historyID string, from int64) ([]*ChatChunkResponse, error) {
	key := chatStreamKey(conversationID, historyID)
	list, err := rdb.LRange(ctx, key, from, -1).Result()
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

func setChatCancelSignal(ctx context.Context, rdb *redis.Client, conversationID, historyID string) error {
	key := chatStopKey(conversationID, historyID)
	if err := rdb.LPush(ctx, key, "1").Err(); err != nil {
		return err
	}
	return rdb.Expire(ctx, key, chatStopExpireTime).Err()
}

func watchChatCancelSignal(ctx context.Context, rdb *redis.Client, conversationID, historyID string) error {
	key := chatStopKey(conversationID, historyID)
	_, err := rdb.BLPop(ctx, 0, key).Result()
	return err
}

func watchChatChunks(ctx context.Context, rdb *redis.Client, conversationID, historyID string, lastIndex int64, callback func(*ChatChunkResponse) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			chunks, err := getChatChunksFrom(ctx, rdb, conversationID, historyID, lastIndex+1)
			if err != nil {
				return err
			}
			for _, c := range chunks {
				if err := callback(c); err != nil {
					return err
				}
				lastIndex++
			}
			st, _ := getChatStatus(ctx, rdb, conversationID, historyID)
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

func setMultiAnswerInfo(ctx context.Context, rdb *redis.Client, conversationID, primaryHistoryID, secondaryHistoryID string, seq int) error {
	key := chatMultiKey(conversationID, primaryHistoryID)
	data := MultiAnswerInfo{
		PrimaryHistoryID:   primaryHistoryID,
		SecondaryHistoryID: secondaryHistoryID,
		Seq:                seq,
		CreatedAt:          time.Now().Unix(),
	}
	bs, _ := json.Marshal(data)
	return rdb.Set(ctx, key, bs, chatCacheExpireTime).Err()
}

func getMultiAnswerInfo(ctx context.Context, rdb *redis.Client, conversationID, primaryHistoryID string) (*MultiAnswerInfo, error) {
	bs, err := rdb.Get(ctx, chatMultiKey(conversationID, primaryHistoryID)).Bytes()
	if err != nil {
		return nil, err
	}
	var info MultiAnswerInfo
	if err := json.Unmarshal(bs, &info); err != nil {
		return nil, err
	}
	return &info, nil
}
