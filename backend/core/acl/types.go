package acl

import "time"

// Knowledge basetext（textPermission）。
const (
	VisibilityPublic    = "public"    // text
	VisibilityProtected = "protected" // text ACL/text
	VisibilityPrivate   = "private"   // text ACL
)

// ACL text。
const (
	GranteeUser   = "user"
	GranteeGroup  = "group"
	GranteeTenant = "tenant" // text，text group
)

// textPermissiontext（textAuthorizationtext）。
const (
	PermNone   = "none"
	PermRead   = "read"
	PermWrite  = "write"
	PermUpload = "upload"
)

// textPermissiontext。
const (
	PermissionKBRead      = "KB_READ"
	PermissionKBWrite     = "KB_WRITE"
	PermissionKBCreateDoc = "KB_CREATE_DOC"
	PermissionKBDeleteDoc = "KB_DELETE_DOC"
	PermissionKBDelete    = "KB_DELETE"

	PermissionDatasetRead   = "DATASET_READ"
	PermissionDatasetWrite  = "DATASET_WRITE"
	PermissionDatasetUpload = "DATASET_UPLOAD"

	PermissionEvalSetRead  = "EVAL_SET_READ"
	PermissionEvalSetWrite = "EVAL_SET_WRITE"
)

// Permissiontext（text）。
const (
	SourceOwner     = "owner"
	SourcePublic    = "public"
	SourceProtected = "protected"
	SourceACL       = "acl"
)

// ACL text。
const (
	ResourceTypeKB      = "kb"       // Knowledge base
	ResourceTypeDB      = "db"       // text
	ResourceTypeEvalSet = "eval_set" // Evaluation set
)

// VisibilityRow text：id、resource_id(kb_id)、level（text private）。
type VisibilityRow struct {
	ID         int64  `json:"id"`
	ResourceID string `json:"resource_id"` // kb text kb_id
	Level      string `json:"level"`       // public / protected / private
}

// ACLRow text ACL text，text kb text db text。
type ACLRow struct {
	ID           int64      `json:"id"`
	ResourceType string     `json:"resource_type"` // kb / db
	ResourceID   string     `json:"resource_id"`   // kb_id text db_id
	GranteeType  string     `json:"grantee_type"`  // user / group
	TargetID     string     `json:"target_id"`     // user_id text group_id
	Permission   string     `json:"permission"`    // KB_READ / DATASET_WRITE / ...
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// ACLListItem text（API text grantee_id text target_id）。
type ACLListItem struct {
	ID          int64     `json:"id"`
	GranteeType string    `json:"grantee_type"`
	GranteeID   string    `json:"grantee_id"`
	Permission  string    `json:"permission"`
	CreatedAt   time.Time `json:"created_at"`
}

// KBInfo Knowledge basetext，text。
type KBInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OwnerID    string `json:"owner_id"`
	Visibility string `json:"visibility"`
}

// --- API Request/Response DTO ---

// AddACLRequest text POST /api/kb/{kb_id}/acl Requesttext
type AddACLRequest struct {
	GranteeType string     `json:"grantee_type"` // user / group（text tenant）
	GranteeID   string     `json:"grantee_id"`
	Permission  string     `json:"permission"` // text read/write，text KB_READ / DATASET_WRITE / ...
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// UpdateACLRequest text PUT /api/kb/{kb_id}/acl/{acl_id} Requesttext
type UpdateACLRequest struct {
	Permission string     `json:"permission"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// BatchAddACLRequest text POST /api/kb/{kb_id}/acl/batch Requesttext
type BatchAddACLRequest struct {
	Items []BatchAddACLItem `json:"items"`
}

type BatchAddACLItem struct {
	GranteeType string `json:"grantee_type"`
	GranteeID   string `json:"grantee_id"`
	Permission  string `json:"permission"`
}

// PermissionBatchRequest text POST /api/kb/permission/batch Requesttext
type PermissionBatchRequest struct {
	KbIDs []string `json:"kb_ids"`
}

// APIResponse textResponsetext：{ code, message, data }
type APIResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// PermissionResult text GET /api/kb/{kb_id}/permission
type PermissionResult struct {
	Permissions []string `json:"permissions"`
	Source      string   `json:"source"` // public / protected / owner / acl
}

// PermissionBatchItem text POST /api/kb/permission/batch text
type PermissionBatchItem struct {
	KbID        string   `json:"kb_id"`
	Permissions []string `json:"permissions"`
}

// CanResult text GET /api/kb/{kb_id}/can
type CanResult struct {
	Allowed bool `json:"allowed"`
}

// KBListResult text GET /api/kb/list
type KBListResult struct {
	Total int64       `json:"total"`
	List  []KBListRow `json:"list"`
}

type KBListRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Visibility  string   `json:"visibility"`
	Permissions []string `json:"permissions"`
}

type GroupInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UserCount int64  `json:"user_count,omitempty"`
}

type GroupMember struct {
	UserID string `json:"user_id"`
}

type CreateGroupRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type AddGroupUserRequest struct {
	UserID string `json:"user_id"`
}

type ListGroupsResponse struct {
	Groups []GroupInfo `json:"groups"`
}

type ListGroupUsersResponse struct {
	Users []GroupMember `json:"users"`
}

// --- Authorization page DTOs ---

// AuthorizationSubjectGrant describes one grantee (user/group) and all permissions granted on a KB.
type AuthorizationSubjectGrant struct {
	GranteeType string   `json:"grantee_type"` // user / group
	GranteeID   string   `json:"grantee_id"`
	Permissions []string `json:"permissions"`
}

// GetKBAuthorizationResponse is used by the authorization page to render current grants.
type GetKBAuthorizationResponse struct {
	KbID   string                      `json:"kb_id"`
	Grants []AuthorizationSubjectGrant `json:"grants"`
}

// SetKBAuthorizationRequest replaces ACL grants of a KB with the submitted grants.
type SetKBAuthorizationRequest struct {
	Grants []AuthorizationSubjectGrant `json:"grants"`
}

// GrantPrincipal represents a selectable user/group in authorization UI.
type GrantPrincipal struct {
	GranteeType string `json:"grantee_type"` // user / group
	GranteeID   string `json:"grantee_id"`
	Name        string `json:"name,omitempty"`
}

type ListGrantPrincipalsResponse struct {
	Users  []GrantPrincipal `json:"users"`
	Groups []GrantPrincipal `json:"groups"`
}
