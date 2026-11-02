package modelconfig

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestConversationResolverDispatchAndError(t *testing.T) {
	conversationResolver.RLock()
	previous := conversationResolver.load
	conversationResolver.RUnlock()
	t.Cleanup(func() { SetConversationResolver(previous) })
	wantErr := errors.New("selected model unavailable")
	SetConversationResolver(func(ctx context.Context, db *gorm.DB, userID, conversationID string) (map[string]any, error) {
		if userID != "user" || conversationID != "conversation" {
			t.Fatal("lost conversation identity")
		}
		return nil, wantErr
	})
	if _, err := LoadConversationLLMConfig(context.Background(), nil, "user", "conversation"); !errors.Is(err, wantErr) {
		t.Fatalf("must propagate selection error without fallback: %v", err)
	}
}
