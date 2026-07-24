package source

import "context"

func (e *DefaultEngine) UpdateBindingChatEnabled(ctx context.Context, bindingID string, chatEnabled bool) error {
	return mapStoreError(e.repo.UpdateBindingChatEnabled(ctx, bindingID, chatEnabled))
}
// IsBindingPathAccessible checks whether the binding'''s root directory still
// exists by calling the agent'''s stat endpoint via the local_fs connector.
// Fail-open: returns true when the check cannot be performed, so transient
// agent errors do not incorrectly hide bindings from the user.
func (e *DefaultEngine) IsBindingPathAccessible(ctx context.Context, agentID, targetRef string) bool {
	if e.registry == nil {
		return true // no connector registry, assume accessible
	}
	conn, err := e.registry.Get("local_fs")
	if err != nil {
		return true // can'''t reach connector, assume accessible
	}
	// Type-assert to access CheckPathExists without modifying SourceConnector.
	if checker, ok := conn.(interface {
		CheckPathExists(context.Context, string, string) bool
	}); ok {
		return checker.CheckPathExists(ctx, agentID, targetRef)
	}
	return true // connector doesn'''t support path checking, assume accessible
}

