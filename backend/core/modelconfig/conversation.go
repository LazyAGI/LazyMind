package modelconfig

import (
	"context"
	"sync"

	"gorm.io/gorm"
)

var conversationResolver struct {
	sync.RWMutex
	load func(context.Context, *gorm.DB, string, string) (map[string]any, error)
}

// SetConversationResolver wires the Chat-owned selection policy at startup,
// without introducing a dependency cycle between Chat, Workflow and Subagent.
func SetConversationResolver(load func(context.Context, *gorm.DB, string, string) (map[string]any, error)) {
	conversationResolver.Lock()
	defer conversationResolver.Unlock()
	conversationResolver.load = load
}

func LoadConversationLLMConfig(ctx context.Context, db *gorm.DB, userID, conversationID string) (map[string]any, error) {
	conversationResolver.RLock()
	load := conversationResolver.load
	conversationResolver.RUnlock()
	if load != nil && conversationID != "" {
		return load(ctx, db, userID, conversationID)
	}
	return LoadLLMConfig(ctx, db, userID)
}
