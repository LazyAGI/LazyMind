package doc

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

type ResourceAuthorizationRequest struct{ DatasetID, Action string }
type resourceAuthorizationKey struct{}
type resourcePreparedGroupsKey struct{}
type resourceAuthorization struct {
	user, tenant string
	groups       []string
	allowed      map[ResourceAuthorizationRequest]bool
}

// PrepareResourceAuthorization resolves live external policy before opening a transaction.
// The transaction still rechecks tenant, ACL rows, membership snapshots and resource versions.
func PrepareResourceAuthorization(ctx context.Context, c DatasetCatalogCaller, requests []ResourceAuthorizationRequest) (context.Context, error) {
	return prepareResourceAuthorization(ctx, c, requests, strictResourceSourceAccess)
}
func prepareResourceAuthorization(ctx context.Context, c DatasetCatalogCaller, requests []ResourceAuthorizationRequest, check func(context.Context, DatasetCatalogCaller, string, string) error) (context.Context, error) {
	auth := &resourceAuthorization{user: c.UserID, tenant: resourceTenant(c.TenantID), groups: []string{}, allowed: map[ResourceAuthorizationRequest]bool{}}
	if groups, ok := ctx.Value(resourcePreparedGroupsKey{}).([]string); ok {
		auth.groups = groups
	} else if len(requests) > 0 {
		auth.groups = append(auth.groups, acl.ResolveUserGroupIDs(c.UserID)...)
	}
	for _, r := range requests {
		if auth.allowed[r] {
			continue
		}
		if err := check(ctx, c, r.DatasetID, r.Action); err != nil {
			return ctx, err
		}
		auth.allowed[r] = true
	}
	return context.WithValue(ctx, resourceAuthorizationKey{}, auth), nil
}
func resourceTransaction(tx *gorm.DB) bool {
	_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
	return ok
}
func resourcePreparedPolicy(tx *gorm.DB, c DatasetCatalogCaller, id, action string) error {
	auth, _ := tx.Statement.Context.Value(resourceAuthorizationKey{}).(*resourceAuthorization)
	if auth == nil || auth.user != c.UserID || auth.tenant != resourceTenant(c.TenantID) || !auth.allowed[ResourceAuthorizationRequest{id, action}] {
		return resourceErr("UNAVAILABLE", "resource authorization must be prepared before transaction")
	}
	return nil
}
func (s *ResourceService) checkSource(ctx context.Context, tx *gorm.DB, c DatasetCatalogCaller, id, action string) error {
	if resourceTransaction(tx) {
		return resourcePreparedPolicy(tx, c, id, action)
	}
	return s.sourceAccess(ctx, c, id, action)
}
func resourceTransactionGroups(tx *gorm.DB) []string {
	auth, _ := tx.Statement.Context.Value(resourceAuthorizationKey{}).(*resourceAuthorization)
	if auth != nil {
		return auth.groups
	}
	if resourceTransaction(tx) {
		return []string{}
	}
	return nil
}
func (s *ResourceService) authorizationRequests(ctx context.Context, c DatasetCatalogCaller, input any) ([]ResourceAuthorizationRequest, error) {
	var out []ResourceAuthorizationRequest
	add := func(id, action string) { out = append(out, ResourceAuthorizationRequest{id, action}) }
	names := []string{}
	switch req := input.(type) {
	case []ImportOriginalRequest:
		for _, item := range req {
			add(item.DatasetID, acl.PermissionDatasetUpload)
		}
	case CreateKnowledgeRequest:
		names = append(names, req.Name)
	case CreateFoldersRequest:
		add(req.KnowledgeBaseID, acl.PermissionDatasetUpload)
	case UpdateMetadataRequest:
		for _, item := range req.Items {
			add(item.Target.KnowledgeBaseID, acl.PermissionDatasetWrite)
			if item.Target.Kind == "knowledge_base" && item.Changes.Name != nil {
				names = append(names, *item.Changes.Name)
			}
		}
	case MoveResourcesRequest:
		for _, ref := range req.Sources {
			add(ref.KnowledgeBaseID, acl.PermissionDatasetWrite)
		}
		add(req.Destination.KnowledgeBaseID, acl.PermissionDatasetUpload)
	case DeleteResourcesRequest:
		for _, ref := range req.Targets {
			add(ref.KnowledgeBaseID, acl.PermissionDatasetWrite)
			add(ref.KnowledgeBaseID, "DELETE")
		}
	}
	if len(names) > 0 {
		var rows []orm.Dataset
		if err := s.db.WithContext(ctx).Where("display_name IN ? AND deleted_at IS NULL", names).Limit(s.budget.MaxResources + 1).Find(&rows).Error; err != nil {
			return nil, err
		}
		if len(rows) > s.budget.MaxResources {
			return nil, s.tooLarge("name_candidates", int64(len(rows)), int64(s.budget.MaxResources))
		}
		for _, row := range rows {
			if resourceTenant(row.TenantID) != resourceTenant(c.TenantID) || row.CreateUserID == c.UserID {
				continue
			}
			perms, _ := acl.PermissionsForWithDB(s.db, acl.ResourceTypeDB, row.ID, c.UserID, nil)
			if len(perms) == 0 {
				continue
			}
			add(row.ID, acl.PermissionDatasetRead)
		}
	}
	return out, nil
}

type resourceAdditionalAuthorizationKey struct{}

func (s *ResourceService) prepareGroups(ctx context.Context, c DatasetCatalogCaller, requests []ResourceAuthorizationRequest) context.Context {
	ids := []string{}
	for _, r := range requests {
		ids = append(ids, r.DatasetID)
	}
	groups := []string{}
	var n int64
	s.db.WithContext(ctx).Model(&orm.Dataset{}).Where("id IN ? AND create_user_id <> ? AND deleted_at IS NULL", ids, c.UserID).Count(&n)
	if n > 0 {
		groups = append(groups, acl.ResolveUserGroupIDs(c.UserID)...)
	}
	return context.WithValue(ctx, resourcePreparedGroupsKey{}, groups)
}
