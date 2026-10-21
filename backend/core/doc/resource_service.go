package doc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"strings"
	"time"
)

type ResourceService struct {
	lazyDB        *gorm.DB
	submitReparse func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error)
	db            *gorm.DB
	budget        ResourceBudget
	sourceAccess  func(context.Context, DatasetCatalogCaller, string, string) error
	provision     func(context.Context, DatasetCatalogCaller, *orm.Dataset) error
	originals     *OriginalService
}

func NewResourceService(d ResourceServiceDeps) (*ResourceService, error) {
	if d.DB == nil {
		return nil, resourceErr("INTERNAL", "database required")
	}
	defaults := defaultResourceBudget()
	if d.Budget.MaxResources <= 0 {
		d.Budget.MaxResources = defaults.MaxResources
	}
	if d.Budget.MaxBytes <= 0 {
		d.Budget.MaxBytes = defaults.MaxBytes
	}
	if d.Budget.MaxPrepareDuration <= 0 {
		d.Budget.MaxPrepareDuration = defaults.MaxPrepareDuration
	}
	if d.Budget.CopyBytesPerSecond <= 0 {
		d.Budget.CopyBytesPerSecond = defaults.CopyBytesPerSecond
	}
	if d.SourceAccess == nil {
		d.SourceAccess = strictResourceSourceAccess
	}
	if d.ProvisionKnowledge == nil {
		d.ProvisionKnowledge = provisionResourceKnowledge
	}
	if d.SubmitReparse == nil {
		d.SubmitReparse = SubmitReparseTask
	}
	return &ResourceService{db: d.DB, budget: d.Budget, sourceAccess: d.SourceAccess, provision: d.ProvisionKnowledge, originals: d.Originals, lazyDB: d.LazyDB, submitReparse: d.SubmitReparse}, nil
}
func resourceErr(code, message string) *ResourceError {
	return &ResourceError{Code: code, Message: message}
}
func resourceTenant(t string) string {
	if strings.TrimSpace(t) == "" {
		return "default"
	}
	return strings.TrimSpace(t)
}
func resourceAccount(c DatasetCatalogCaller) error {
	if strings.TrimSpace(c.UserID) == "" {
		return resourceErr("FORBIDDEN", "authenticated user required")
	}
	for _, p := range c.Permissions {
		if p == "document.write" || p == "*" {
			return nil
		}
	}
	return resourceErr("FORBIDDEN", "document.write permission required")
}
func strictResourceSourceAccess(ctx context.Context, c DatasetCatalogCaller, id, action string) error {
	items, ok := scanSourceAccessByDatasetForCaller(ctx, c, []string{id}, action)
	if !ok {
		return resourceErr("UNAVAILABLE", "source permission service unavailable")
	}
	item, ok := items[id]
	if !ok {
		return resourceErr("UNAVAILABLE", "source permission result missing")
	}
	if item.Exists && !item.Allowed {
		return resourceErr("FORBIDDEN", "source does not allow requested action")
	}
	return nil
}

// ResolveResourceDataset uses the caller's current ACL and a strict source-policy check.
func ResolveResourceDataset(ctx context.Context, tx *gorm.DB, c DatasetCatalogCaller, id, action string) (orm.Dataset, error) {
	ds, err := resolveResourceDatasetACL(ctx, tx, c, id, action)
	if err != nil {
		return ds, err
	}
	if resourceTransaction(tx) {
		return ds, resourcePreparedPolicy(tx, c, id, action)
	}
	return ds, strictResourceSourceAccess(ctx, c, id, action)
}
func resolveResourceDatasetACL(ctx context.Context, tx *gorm.DB, c DatasetCatalogCaller, id, action string) (orm.Dataset, error) {
	var ds orm.Dataset
	if c.UserID == "" {
		return ds, resourceErr("FORBIDDEN", "authenticated user required")
	}
	if err := tx.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&ds).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ds, resourceErr("NOT_FOUND", "knowledge base not found")
		}
		return ds, err
	}
	if resourceTenant(ds.TenantID) != resourceTenant(c.TenantID) {
		return ds, resourceErr("NOT_FOUND", "knowledge base not found")
	}
	if ds.CreateUserID == c.UserID {
		return ds, nil
	}
	permissions, _ := acl.PermissionsForWithDB(tx, acl.ResourceTypeDB, ds.ID, c.UserID, resourceTransactionGroups(tx))
	allowed := containsAll(permissions, []string{action}) || ((action == acl.PermissionDatasetRead || action == acl.PermissionDatasetUpload) && containsAll(permissions, []string{acl.PermissionDatasetWrite})) || (action == acl.PermissionDatasetRead && containsAll(permissions, []string{acl.PermissionDatasetUpload}))
	if !allowed {
		return ds, resourceErr("FORBIDDEN", fmt.Sprintf("knowledge base %s requires %s permission", ds.ID, action))
	}
	return ds, nil
}
func (s *ResourceService) dataset(ctx context.Context, tx *gorm.DB, op ResourceOperationContext, id, action string) (orm.Dataset, error) {
	if op.AllowedDatasetIDs != nil && !containsAll(op.AllowedDatasetIDs, []string{id}) {
		return orm.Dataset{}, resourceErr("FORBIDDEN", "knowledge base outside run scope")
	}
	ds, err := resolveResourceDatasetACL(ctx, tx, op.Caller, id, action)
	if err != nil {
		return ds, err
	}
	return ds, s.checkSource(ctx, tx, op.Caller, id, action)
}
func resourceHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s *ResourceService) mutate(ctx context.Context, op ResourceOperationContext, action string, input any, fn func(*gorm.DB) (ResourceOperationResult, error)) (ResourceOperationResult, error) {
	var out ResourceOperationResult
	if err := resourceAccount(op.Caller); err != nil {
		return out, err
	}
	if cached, found, err := s.replay(ctx, op, action, input); found || err != nil {
		return cached, err
	}
	requests, err := s.authorizationRequests(ctx, op.Caller, input)
	if err != nil {
		return out, err
	}
	if extra, ok := ctx.Value(resourceAdditionalAuthorizationKey{}).([]ResourceAuthorizationRequest); ok {
		requests = append(requests, extra...)
	}
	for _, request := range requests {
		action := request.Action
		if action == "DELETE" {
			action = acl.PermissionDatasetWrite
		}
		if _, err := resolveResourceDatasetACL(ctx, s.db, op.Caller, request.DatasetID, action); err != nil {
			return out, err
		}
	}
	ctx = s.prepareGroups(ctx, op.Caller, requests)
	ctx, err = prepareResourceAuthorization(ctx, op.Caller, requests, s.sourceAccess)
	if err != nil {
		return out, err
	}
	if op.ValidateBeforeCommit != nil {
		if err := op.ValidateBeforeCommit(ctx); err != nil {
			return out, err
		}
	}
	hash := resourceOperationHash(op, input)
	readyToCommit := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if op.ValidateExecution != nil {
			if err := op.ValidateExecution(ctx, tx); err != nil {
				return err
			}
		}
		if op.OperationKey != "" {
			var receipt orm.ResourceOperationReceipt
			err := tx.Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, op.OperationKey).First(&receipt).Error
			if err == nil {
				if receipt.Action != action || receipt.ParameterHash != hash {
					return resourceErr("CONFLICT", "operation key was already used for different parameters")
				}
				if err := json.Unmarshal(receipt.Result, &out); err != nil {
					return err
				}
				readyToCommit = true
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var err error
		out, err = fn(tx)
		if err != nil {
			return err
		}
		out.Action = action
		summarizeResourceResult(&out)
		if op.OperationKey != "" {
			b, err := json.Marshal(out)
			if err != nil {
				return err
			}
			if err := tx.Create(&orm.ResourceOperationReceipt{TenantID: resourceTenant(op.Caller.TenantID), UserID: op.Caller.UserID, OperationKey: op.OperationKey, Action: action, ParameterHash: hash, Result: b, CreatedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		readyToCommit = true
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil && readyToCommit {
		return s.reconcileResourceCommit(op, action, input, out, err)
	}
	return out, err
}
func resourceRef(row orm.Document) ResourceRef {
	if isFolderLikeDocument(row) {
		return ResourceRef{Kind: "folder", KnowledgeBaseID: row.DatasetID, FolderID: row.ID}
	}
	return ResourceRef{Kind: "document", KnowledgeBaseID: row.DatasetID, DocumentID: row.ID}
}
func resourceItem(row orm.Document, status string) ResourceOperationItem {
	return ResourceOperationItem{ResourceRef: resourceRef(row), Name: row.DisplayName, ParentID: row.PID, ResourceStatus: status, EntryURL: "/lib/knowledge/detail/" + row.DatasetID + "?document=" + row.ID}
}
func resourceDocument(tx *gorm.DB, ref ResourceRef) (orm.Document, error) {
	var row orm.Document
	if ref.Kind != "folder" && ref.Kind != "document" {
		return row, resourceErr("INVALID_ARGUMENT", "expected folder or document")
	}
	if ref.ID() == "" || ref.KnowledgeBaseID == "" {
		return row, resourceErr("INVALID_ARGUMENT", "resource ID required")
	}
	err := tx.Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", ref.ID(), ref.KnowledgeBaseID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, resourceErr("NOT_FOUND", "resource not found")
	}
	if err == nil && isFolderLikeDocument(row) != (ref.Kind == "folder") {
		return row, resourceErr("INVALID_ARGUMENT", "resource kind mismatch")
	}
	return row, err
}
func resourceParent(tx *gorm.DB, dataset, parent string) error {
	if parent == "" {
		return nil
	}
	_, err := resourceDocument(tx, ResourceRef{Kind: "folder", KnowledgeBaseID: dataset, FolderID: parent})
	return err
}
func resourceNameConflict(tx *gorm.DB, dataset, parent, name, exclude string) error {
	var n int64
	err := tx.Model(&orm.Document{}).Where("dataset_id = ? AND p_id = ? AND display_name = ? AND id <> ? AND deleted_at IS NULL", dataset, parent, name, exclude).Count(&n).Error
	if err != nil {
		return err
	}
	if n > 0 {
		return resourceErr("CONFLICT", "name already exists in destination")
	}
	return nil
}
func lockResourceDataset(tx *gorm.DB, ds orm.Dataset) error {
	r := tx.Model(&orm.Dataset{}).Where("id = ? AND resource_version = ? AND deleted_at IS NULL", ds.ID, ds.ResourceVersion).Updates(map[string]any{"resource_version": gorm.Expr("resource_version + 1"), "updated_at": time.Now().UTC()})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return resourceErr("CONFLICT", "knowledge base changed; refresh and retry")
	}
	return nil
}
func provisionResourceKnowledge(ctx context.Context, c DatasetCatalogCaller, ds *orm.Dataset) error {
	var result map[string]any
	req := kbCreateRequest{KbID: ds.ID, DisplayName: algoDatasetDisplayName(c.UserID, ds.DisplayName), OwnerID: c.UserID, AlgoID: parseDatasetAlgo(ds.Ext).AlgoID, Meta: map[string]any{"tags": parseDatasetTags(ds.Ext)}}
	if ds.Desc != "" {
		req.Description = &ds.Desc
	}
	if err := common.ApiPost(ctx, common.JoinURL(common.AlgoServiceEndpoint(), "/v1/kbs"), req, nil, &result, 10*time.Second); err != nil {
		return fmt.Errorf("provision knowledge base: %w", err)
	}
	ds.KbID = ds.ID
	if id, ok := result["kb_id"].(string); ok && id != "" {
		ds.KbID = id
	}
	return nil
}

func (s *ResourceService) replay(ctx context.Context, op ResourceOperationContext, action string, input any) (ResourceOperationResult, bool, error) {
	var out ResourceOperationResult
	if op.OperationKey == "" {
		return out, false, nil
	}
	var receipt orm.ResourceOperationReceipt
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, op.OperationKey).First(&receipt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if receipt.Action != action || receipt.ParameterHash != resourceOperationHash(op, input) {
		return out, true, resourceErr("CONFLICT", "operation key was already used for different parameters")
	}
	err = json.Unmarshal(receipt.Result, &out)
	return out, true, err
}

// ResourceInputDigest hashes canonical, server-validated tool parameters before acquiring files.
func ResourceInputDigest(input any) string { return resourceHash(input) }
func resourceOperationHash(op ResourceOperationContext, input any) string {
	if op.InputDigest != "" {
		return op.InputDigest
	}
	return resourceHash(input)
}
func (s *ResourceService) ReplayOperation(ctx context.Context, op ResourceOperationContext, action string, input any) (ResourceOperationResult, bool, error) {
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceOperationResult{}, false, err
	}
	return s.replay(ctx, op, action, input)
}

func (s *ResourceService) syncKnowledgeMetadata(ctx context.Context, c DatasetCatalogCaller, id string, algorithmChanged bool) error {
	var ds orm.Dataset
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).Take(&ds).Error; err != nil {
		return err
	}
	display := algoDatasetDisplayName(ds.CreateUserID, ds.DisplayName)
	algoID := parseDatasetAlgo(ds.Ext).AlgoID
	request := kbUpdateRequest{DisplayName: &display, Description: &ds.Desc, Meta: map[string]any{"tags": parseDatasetTags(ds.Ext)}}
	if algorithmChanged {
		request.AlgoID = &algoID
	}
	return common.ApiPost(ctx, common.JoinURL(common.AlgoServiceEndpoint(), "/v1/kbs/"+firstNonEmpty(ds.KbID, ds.ID)), request, nil, nil, 10*time.Second)
}

func resourceOutcomeUnconfirmed(err error) bool {
	var typed *ResourceError
	return errors.As(err, &typed) && typed.Code == "UNCONFIRMED"
}
func (s *ResourceService) reconcileResourceCommit(op ResourceOperationContext, action string, input any, out ResourceOperationResult, commitErr error) (ResourceOperationResult, error) {
	var pgErr *pgconn.PgError
	if errors.As(commitErr, &pgErr) && !strings.HasPrefix(pgErr.Code, "08") {
		if pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "23505" {
			return out, resourceErr("CONFLICT", "concurrent resource change; refresh before retry")
		}
		return out, commitErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if op.OperationKey != "" {
		if saved, found, err := s.replay(ctx, op, action, input); found && err == nil {
			return saved, nil
		}
	}
	out.Status = "unconfirmed"
	out.Summary.Succeeded = 0
	out.Summary.Unconfirmed = len(out.Items)
	for i := range out.Items {
		out.Items[i].ResourceStatus = "unconfirmed"
	}
	return out, &ResourceError{Code: "UNCONFIRMED", Message: "commit outcome could not be confirmed; verify these resources before retrying", Details: map[string]any{"action": action, "resources": out.Items, "next_action": "verify_before_retry"}}
}
