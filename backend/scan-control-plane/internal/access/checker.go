package access

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/localfs"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const localSourceAccessDeniedMessage = "local data sources can only be accessed by administrators"

type DefaultChecker struct {
	store              SourceStore
	authVerifier       AuthConnectionVerifier
	adminVerifier      AdminVerifier
	permissionVerifier SourcePermissionVerifier
}

type Option func(*DefaultChecker)

func NewDefaultChecker(store SourceStore, options ...Option) *DefaultChecker {
	checker := &DefaultChecker{store: store}
	for _, option := range options {
		option(checker)
	}
	return checker
}

func WithAuthConnectionVerifier(verifier AuthConnectionVerifier) Option {
	return func(c *DefaultChecker) {
		c.authVerifier = verifier
	}
}

func WithAdminVerifier(verifier AdminVerifier) Option {
	return func(c *DefaultChecker) {
		c.adminVerifier = verifier
	}
}

func WithSourcePermissionVerifier(verifier SourcePermissionVerifier) Option {
	return func(c *DefaultChecker) {
		c.permissionVerifier = verifier
	}
}

func (c *DefaultChecker) ListReadableSourceIDs(ctx context.Context, actor Actor) ([]string, error) {
	if err := validateActor(actor); err != nil {
		return nil, err
	}
	if c.store == nil {
		return nil, forbidden("access store is not configured")
	}
	sources, err := c.store.ListSourceAccess(ctx, actor.TenantID)
	if err != nil {
		return nil, internal(err)
	}
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		if err := c.checkSourceAction(ctx, actor, source, SourceActionRead); err == nil {
			ids = append(ids, source.SourceID)
		} else if ErrorCodeOf(err) != ErrCodeForbidden {
			return nil, err
		}
	}
	return ids, nil
}

func (c *DefaultChecker) CanCreateSource(ctx context.Context, actor Actor) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	if c.permissionVerifier != nil {
		return c.permissionVerifier.CanCreateSource(ctx, actor)
	}
	return nil
}

func (c *DefaultChecker) CanReadSource(ctx context.Context, actor Actor, sourceID string) error {
	return c.canAccessSource(ctx, actor, sourceID, SourceActionRead)
}

func (c *DefaultChecker) CanWriteSource(ctx context.Context, actor Actor, sourceID string) error {
	return c.canAccessSource(ctx, actor, sourceID, SourceActionWrite)
}

func (c *DefaultChecker) CanDeleteSource(ctx context.Context, actor Actor, sourceID string) error {
	return c.canAccessSource(ctx, actor, sourceID, SourceActionDelete)
}

func (c *DefaultChecker) CanReadBinding(ctx context.Context, actor Actor, sourceID, bindingID string) error {
	return c.canAccessBinding(ctx, actor, sourceID, bindingID, SourceActionRead)
}

func (c *DefaultChecker) CanWriteBinding(ctx context.Context, actor Actor, sourceID, bindingID string) error {
	return c.canAccessBinding(ctx, actor, sourceID, bindingID, SourceActionWrite)
}

func (c *DefaultChecker) CanDeleteBinding(ctx context.Context, actor Actor, sourceID, bindingID string) error {
	return c.canAccessBinding(ctx, actor, sourceID, bindingID, SourceActionDelete)
}

func (c *DefaultChecker) CanReadTask(ctx context.Context, actor Actor, taskID string) error {
	return c.canAccessTask(ctx, actor, taskID, SourceActionRead)
}

func (c *DefaultChecker) CanWriteTask(ctx context.Context, actor Actor, taskID string) error {
	return c.canAccessTask(ctx, actor, taskID, SourceActionWrite)
}

func (c *DefaultChecker) CanAccessBindingTarget(ctx context.Context, actor Actor, req BindingTargetRequest) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	if c.ShouldBlockLocalSourceAccess(ctx, actor, LocalSourceAccessRequest{
		BindingTargets: []BindingTargetRequest{req},
	}) {
		return forbidden(localSourceAccessDeniedMessage)
	}
	if req.BindingID != "" {
		if strings.TrimSpace(req.SourceID) == "" {
			return forbidden("access denied")
		}
		if err := c.CanReadBinding(ctx, actor, req.SourceID, req.BindingID); err != nil {
			return err
		}
	} else if req.SourceID != "" {
		if err := c.CanReadSource(ctx, actor, req.SourceID); err != nil {
			return err
		}
	}
	if strings.TrimSpace(req.AgentID) != "" {
		if err := c.CanUseAgent(ctx, actor, req.AgentID); err != nil {
			return err
		}
	}
	if strings.TrimSpace(req.AuthConnectionID) != "" {
		if err := c.CanUseAuthConnection(ctx, actor, req.AuthConnectionID); err != nil {
			return err
		}
	}
	return nil
}

func (c *DefaultChecker) CanUseAgent(ctx context.Context, actor Actor, agentID string) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	if c.store == nil {
		return forbidden("access store is not configured")
	}
	agent, err := c.store.GetAgent(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return forbidden("access denied")
	}
	if strings.TrimSpace(agent.TenantID) != actor.TenantID {
		return forbidden("access denied")
	}
	if !agentUsable(agent) {
		return forbidden("agent is not available")
	}
	return nil
}

func (c *DefaultChecker) CanUseAuthConnection(ctx context.Context, actor Actor, authConnectionID string) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	if strings.TrimSpace(authConnectionID) == "" {
		return forbidden("access denied")
	}
	if c.authVerifier == nil {
		return forbidden("auth connection verifier is not configured")
	}
	if err := c.authVerifier.VerifyAuthConnection(ctx, actor, strings.TrimSpace(authConnectionID)); err != nil {
		return err
	}
	return nil
}

func (c *DefaultChecker) ShouldBlockLocalSourceAccess(ctx context.Context, actor Actor, req LocalSourceAccessRequest) bool {
	if c == nil || !c.localSourceAccessRequestIsLocal(ctx, req) {
		return false
	}
	return !c.actorRealtimeAdmin(ctx, actor)
}

func (c *DefaultChecker) canAccessBinding(ctx context.Context, actor Actor, sourceID, bindingID string, action SourceAction) error {
	if err := c.canAccessSource(ctx, actor, sourceID, action); err != nil {
		return err
	}
	binding, err := c.store.GetBinding(ctx, sourceID, bindingID)
	if err != nil {
		return forbidden("access denied")
	}
	if binding.SourceID != sourceID {
		return forbidden("access denied")
	}
	return nil
}

func (c *DefaultChecker) canAccessTask(ctx context.Context, actor Actor, taskID string, action SourceAction) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	item, err := c.store.GetParseTask(ctx, taskID)
	if err != nil {
		return forbidden("access denied")
	}
	return c.canAccessSource(ctx, actor, item.Task.SourceID, action)
}

func (c *DefaultChecker) canAccessSource(ctx context.Context, actor Actor, sourceID string, action SourceAction) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	if c.store == nil {
		return forbidden("access store is not configured")
	}
	source, err := c.store.GetSource(ctx, sourceID)
	if err != nil {
		return forbidden("access denied")
	}
	return c.checkSourceAction(ctx, actor, source, action)
}

func (c *DefaultChecker) checkSourceAction(ctx context.Context, actor Actor, source store.Source, action SourceAction) error {
	if strings.TrimSpace(source.TenantID) != actor.TenantID {
		return forbidden("access denied")
	}
	local, err := c.sourceIsLocal(ctx, source)
	if err != nil {
		return internal(err)
	}
	if local && !c.actorRealtimeAdmin(ctx, actor) {
		return forbidden(localSourceAccessDeniedMessage)
	}
	if c.permissionVerifier != nil {
		return c.permissionVerifier.CanAccessSource(ctx, actor, source, action)
	}
	return OwnerSourcePermissionVerifier{}.CanAccessSource(ctx, actor, source, action)
}

func (c *DefaultChecker) localSourceAccessRequestIsLocal(ctx context.Context, req LocalSourceAccessRequest) bool {
	if sourceOptionsAreLocal(req.SourceOptions) {
		return true
	}
	for _, target := range req.BindingTargets {
		if bindingTargetIsLocal(string(target.ConnectorType), string(target.TargetType)) {
			return true
		}
	}
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" || c.store == nil {
		return false
	}
	source, err := c.store.GetSource(ctx, sourceID)
	if err != nil {
		return false
	}
	local, err := c.sourceIsLocal(ctx, source)
	return err == nil && local
}

func (c *DefaultChecker) sourceIsLocal(ctx context.Context, source store.Source) (bool, error) {
	if sourceOptionsAreLocal(source.SourceOptions) {
		return true, nil
	}
	if c.store == nil {
		return false, nil
	}
	bindings, err := c.store.ListBindings(ctx, source.SourceID)
	if err != nil {
		return false, err
	}
	for _, binding := range bindings {
		if strings.EqualFold(strings.TrimSpace(binding.Status), "DELETING") {
			continue
		}
		if bindingTargetIsLocal(binding.ConnectorType, binding.TargetType) {
			return true, nil
		}
	}
	return false, nil
}

func (c *DefaultChecker) actorRealtimeAdmin(ctx context.Context, actor Actor) bool {
	if c == nil || c.adminVerifier == nil {
		return false
	}
	ok, err := c.adminVerifier.IsAdmin(ctx, actor)
	return err == nil && ok
}

func sourceOptionsAreLocal(options map[string]any) bool {
	sourceType, ok := stringMapValue(options, "source_type")
	if !ok {
		return false
	}
	switch strings.ToLower(sourceType) {
	case "local", "local_fs", "localfs":
		return true
	default:
		return false
	}
}

func bindingTargetIsLocal(connectorType, targetType string) bool {
	return strings.EqualFold(strings.TrimSpace(connectorType), string(localfs.ConnectorType)) ||
		strings.EqualFold(strings.TrimSpace(targetType), string(localfs.TargetTypeLocalPath))
}

func stringMapValue(values map[string]any, key string) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	value, ok := values[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

type OwnerSourcePermissionVerifier struct{}

func (OwnerSourcePermissionVerifier) CanCreateSource(context.Context, Actor) error {
	return nil
}

func (OwnerSourcePermissionVerifier) CanAccessSource(_ context.Context, actor Actor, source store.Source, action SourceAction) error {
	switch action {
	case SourceActionRead, SourceActionWrite, SourceActionDelete:
	default:
		return forbidden("access denied")
	}
	if strings.TrimSpace(source.CreatedBy) == actor.UserID {
		return nil
	}
	return forbidden("access denied")
}

func agentUsable(agent store.Agent) bool {
	switch strings.ToUpper(strings.TrimSpace(agent.Status)) {
	case "ONLINE", "DEGRADED":
		return true
	default:
		return false
	}
}

func validateActor(actor Actor) error {
	if strings.TrimSpace(actor.UserID) == "" {
		return unauthorized("missing caller")
	}
	return nil
}

var _ Checker = (*DefaultChecker)(nil)
