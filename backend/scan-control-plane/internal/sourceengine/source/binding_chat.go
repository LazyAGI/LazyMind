package source

import "context"

func (e *DefaultEngine) UpdateBindingChatEnabled(ctx context.Context, bindingID string, chatEnabled bool) error {
	return mapStoreError(e.repo.UpdateBindingChatEnabled(ctx, bindingID, chatEnabled))
}
