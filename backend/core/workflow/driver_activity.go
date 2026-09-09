package workflow

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"lazymind/core/common"
	"lazymind/core/state"
)

// DriverActivity identifies computation between terminal steps and the next chat
// turn. It must not share the chat resume cache, which contains real history IDs.
type DriverActivity struct {
	SessionID string `json:"session_id"`
	ExpiresAt int64  `json:"expires_at"`
}

func DriverActivityKey(conversationID string) string { return "rag/workflow/drivers:" + conversationID }

func beginDriverActivity(cache state.Store, conversationID, sessionID string) func() {
	if cache == nil {
		return func() {}
	}
	// Two driver calls (2 minutes each) plus admission of the next turn (10 minutes).
	const ttl = 15 * time.Minute
	key, token := DriverActivityKey(conversationID), common.GenerateID()
	payload, _ := json.Marshal(DriverActivity{SessionID: sessionID, ExpiresAt: time.Now().Add(ttl).Unix()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cache.HSet(ctx, key, map[string]any{token: string(payload)}, ttl)
	cancel()
	var once sync.Once
	return func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = cache.HDel(ctx, key, token)
		})
	}
}
