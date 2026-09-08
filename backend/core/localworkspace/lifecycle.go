package localworkspace

import (
	"context"
	"sync"
)

type StopConversationFunc func(context.Context, string, string) error

var stopConversation struct {
	sync.RWMutex
	fn StopConversationFunc
}

func SetStopConversationFunc(fn StopConversationFunc) {
	stopConversation.Lock()
	stopConversation.fn = fn
	stopConversation.Unlock()
}

func requestConversationStop(ctx context.Context, userID, conversationID string) error {
	stopConversation.RLock()
	fn := stopConversation.fn
	stopConversation.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, userID, conversationID)
}
