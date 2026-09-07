package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

const (
	ChatSourceAttachmentScopeTemp    = "temp"
	ChatSourceAttachmentScopeDataset = "dataset"
)

var (
	ErrChatSourceAttachmentUnavailable = errors.New("sidechat source attachment is no longer available")
	ErrChatSourceAttachmentForbidden   = errors.New("sidechat source attachment access denied")
)

// ChatSourceAttachmentReference is the stable identity stored beside a
// sidechat's private source path. Path is runtime-only and is never serialized.
type ChatSourceAttachmentReference struct {
	Path         string `json:"-"`
	UploadFileID string `json:"upload_file_id,omitempty"`
	UploadID     string `json:"upload_id,omitempty"`
	DatasetID    string `json:"dataset_id,omitempty"`
	Scope        string `json:"scope"`
}

type resolvedChatSourceAttachment struct {
	reference ChatSourceAttachmentReference
	datasetID string
	tenantID  string
}

// ValidateChatSourceAttachments resolves legacy path-only references and
// revalidates stable references against the current upload and dataset state.
// Returned errors never contain a storage path.
func ValidateChatSourceAttachments(
	ctx context.Context,
	db *gorm.DB,
	caller DatasetCatalogCaller,
	references []ChatSourceAttachmentReference,
) ([]ChatSourceAttachmentReference, error) {
	if len(references) == 0 {
		return nil, nil
	}
	if db == nil {
		return nil, errors.New("chat source attachment store is not configured")
	}
	caller.UserID = strings.TrimSpace(caller.UserID)
	if caller.UserID == "" {
		return nil, ErrChatSourceAttachmentForbidden
	}

	resolved := make([]resolvedChatSourceAttachment, 0, len(references))
	datasetIDs := make([]string, 0)
	datasetSeen := make(map[string]struct{})
	for _, reference := range references {
		item, err := resolveChatSourceAttachment(ctx, db, caller.UserID, reference)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, item)
		if item.datasetID != "" {
			if _, exists := datasetSeen[item.datasetID]; !exists {
				datasetSeen[item.datasetID] = struct{}{}
				datasetIDs = append(datasetIDs, item.datasetID)
			}
		}
	}

	if err := validateChatSourceDatasets(ctx, db, caller, resolved, datasetIDs); err != nil {
		return nil, err
	}
	out := make([]ChatSourceAttachmentReference, 0, len(resolved))
	for _, item := range resolved {
		out = append(out, item.reference)
	}
	return out, nil
}

func resolveChatSourceAttachment(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	reference ChatSourceAttachmentReference,
) (resolvedChatSourceAttachment, error) {
	reference.Path = strings.TrimSpace(reference.Path)
	reference.UploadFileID = strings.TrimSpace(reference.UploadFileID)
	reference.UploadID = strings.TrimSpace(reference.UploadID)
	reference.DatasetID = strings.TrimSpace(reference.DatasetID)
	reference.Scope = strings.ToLower(strings.TrimSpace(reference.Scope))
	relPath, fullPath, ok := chatSourceAttachmentPath(reference.Path)
	if !ok {
		return resolvedChatSourceAttachment{}, ErrChatSourceAttachmentUnavailable
	}
	info, err := os.Lstat(fullPath)
	if err != nil || !info.Mode().IsRegular() {
		return resolvedChatSourceAttachment{}, ErrChatSourceAttachmentUnavailable
	}

	candidateID := filepath.Base(filepath.Dir(fullPath))
	if reference.UploadFileID != "" {
		return resolveUploadedFileReference(ctx, db, userID, relPath, reference, reference.UploadFileID)
	}
	if reference.UploadID != "" {
		return resolveUploadSessionReference(ctx, db, userID, relPath, reference, reference.UploadID)
	}
	if candidateID == "" || candidateID == "." || candidateID == string(filepath.Separator) {
		return resolvedChatSourceAttachment{}, ErrChatSourceAttachmentUnavailable
	}
	if item, found, err := findUploadedFileReference(ctx, db, userID, relPath, reference, candidateID); err != nil {
		return resolvedChatSourceAttachment{}, err
	} else if found {
		return item, nil
	}
	if item, found, err := findUploadSessionReference(ctx, db, userID, relPath, reference, candidateID); err != nil {
		return resolvedChatSourceAttachment{}, err
	} else if found {
		return item, nil
	}
	if item, found := resolveDatasetPathReference(relPath, reference); found {
		return item, nil
	}
	return resolvedChatSourceAttachment{}, ErrChatSourceAttachmentUnavailable
}

func resolveDatasetPathReference(
	relPath string,
	reference ChatSourceAttachmentReference,
) (resolvedChatSourceAttachment, bool) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) < 5 || parts[0] != "tenants" || parts[2] != "datasets" {
		return resolvedChatSourceAttachment{}, false
	}
	tenantID := strings.TrimSpace(parts[1])
	datasetID := strings.TrimSpace(parts[3])
	if tenantID == "" || datasetID == "" || tenantID == "." || datasetID == "." {
		return resolvedChatSourceAttachment{}, false
	}
	if reference.Scope != "" && reference.Scope != ChatSourceAttachmentScopeDataset {
		return resolvedChatSourceAttachment{}, false
	}
	if reference.DatasetID != "" && reference.DatasetID != datasetID {
		return resolvedChatSourceAttachment{}, false
	}
	reference.DatasetID = datasetID
	reference.Scope = ChatSourceAttachmentScopeDataset
	return resolvedChatSourceAttachment{
		reference: reference,
		datasetID: datasetID,
		tenantID:  tenantID,
	}, true
}

func resolveUploadedFileReference(
	ctx context.Context,
	db *gorm.DB,
	userID, relPath string,
	reference ChatSourceAttachmentReference,
	uploadFileID string,
) (resolvedChatSourceAttachment, error) {
	item, found, err := findUploadedFileReference(ctx, db, userID, relPath, reference, uploadFileID)
	if err != nil {
		return resolvedChatSourceAttachment{}, err
	}
	if !found {
		return resolvedChatSourceAttachment{}, ErrChatSourceAttachmentUnavailable
	}
	return item, nil
}

func findUploadedFileReference(
	ctx context.Context,
	db *gorm.DB,
	userID, relPath string,
	reference ChatSourceAttachmentReference,
	uploadFileID string,
) (resolvedChatSourceAttachment, bool, error) {
	var rows []orm.UploadedFile
	if err := db.WithContext(ctx).Where(
		"upload_file_id = ? AND deleted_at IS NULL AND status IN ?",
		uploadFileID, []string{UploadedFileStateUploaded, UploadedFileStateBound},
	).Limit(1).Find(&rows).Error; err != nil {
		return resolvedChatSourceAttachment{}, false, fmt.Errorf("query chat source upload: %w", err)
	}
	if len(rows) == 0 {
		return resolvedChatSourceAttachment{}, false, nil
	}
	row := rows[0]
	var ext uploadedFileExt
	if json.Unmarshal(row.Ext, &ext) != nil {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
	}
	storedRel, _, ok := chatSourceAttachmentPath(ext.StoredPath)
	if !ok || storedRel != relPath {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
	}

	scope := ChatSourceAttachmentScopeTemp
	if strings.TrimSpace(row.DatasetID) != "" {
		scope = ChatSourceAttachmentScopeDataset
	}
	if reference.Scope != "" && reference.Scope != scope {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
	}
	if reference.DatasetID != "" && reference.DatasetID != strings.TrimSpace(row.DatasetID) {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
	}
	if scope == ChatSourceAttachmentScopeTemp && strings.TrimSpace(row.CreateUserID) != userID {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentForbidden
	}
	tenantID := ""
	if scope == ChatSourceAttachmentScopeDataset {
		pathIdentity, valid := resolveDatasetPathReference(relPath, reference)
		rowTenantID := strings.TrimSpace(row.TenantID)
		if !valid || pathIdentity.datasetID != strings.TrimSpace(row.DatasetID) ||
			(rowTenantID != "" && pathIdentity.tenantID != rowTenantID) {
			return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
		}
		tenantID = pathIdentity.tenantID
	}

	reference.UploadFileID = row.UploadFileID
	reference.UploadID = ""
	reference.DatasetID = strings.TrimSpace(row.DatasetID)
	reference.Scope = scope
	return resolvedChatSourceAttachment{
		reference: reference,
		datasetID: reference.DatasetID,
		tenantID:  tenantID,
	}, true, nil
}

func resolveUploadSessionReference(
	ctx context.Context,
	db *gorm.DB,
	userID, relPath string,
	reference ChatSourceAttachmentReference,
	uploadID string,
) (resolvedChatSourceAttachment, error) {
	item, found, err := findUploadSessionReference(ctx, db, userID, relPath, reference, uploadID)
	if err != nil {
		return resolvedChatSourceAttachment{}, err
	}
	if !found {
		return resolvedChatSourceAttachment{}, ErrChatSourceAttachmentUnavailable
	}
	return item, nil
}

func findUploadSessionReference(
	ctx context.Context,
	db *gorm.DB,
	userID, relPath string,
	reference ChatSourceAttachmentReference,
	uploadID string,
) (resolvedChatSourceAttachment, bool, error) {
	var rows []orm.UploadSession
	if err := db.WithContext(ctx).Where(
		"upload_id = ? AND dataset_id = ? AND deleted_at IS NULL AND upload_state = ?",
		uploadID, "", string(TaskStateUploaded),
	).Limit(1).Find(&rows).Error; err != nil {
		return resolvedChatSourceAttachment{}, false, fmt.Errorf("query chat source upload session: %w", err)
	}
	if len(rows) == 0 {
		return resolvedChatSourceAttachment{}, false, nil
	}
	row := rows[0]
	if strings.TrimSpace(row.CreateUserID) != userID {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentForbidden
	}
	var meta uploadMeta
	if json.Unmarshal(row.Ext, &meta) != nil ||
		strings.ToUpper(strings.TrimSpace(meta.UploadScope)) != uploadScopeTemp ||
		strings.TrimSpace(meta.StoredName) == "" {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
	}
	expectedPath := filepath.Join(buildTempUploadFileDir(row.CreateUserID, row.UploadID), meta.StoredName)
	expectedRel, _, ok := chatSourceAttachmentPath(expectedPath)
	if !ok || expectedRel != relPath {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
	}
	if reference.Scope != "" && reference.Scope != ChatSourceAttachmentScopeTemp {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
	}
	if reference.DatasetID != "" || reference.UploadFileID != "" {
		return resolvedChatSourceAttachment{}, true, ErrChatSourceAttachmentUnavailable
	}

	reference.UploadID = row.UploadID
	reference.Scope = ChatSourceAttachmentScopeTemp
	return resolvedChatSourceAttachment{reference: reference}, true, nil
}

func validateChatSourceDatasets(
	ctx context.Context,
	db *gorm.DB,
	caller DatasetCatalogCaller,
	attachments []resolvedChatSourceAttachment,
	datasetIDs []string,
) error {
	if len(datasetIDs) == 0 {
		return nil
	}
	var datasets []orm.Dataset
	if err := db.WithContext(ctx).Where("id IN ? AND deleted_at IS NULL", datasetIDs).Find(&datasets).Error; err != nil {
		return fmt.Errorf("query chat source datasets: %w", err)
	}
	byID := make(map[string]orm.Dataset, len(datasets))
	for _, dataset := range datasets {
		byID[dataset.ID] = dataset
	}
	for _, datasetID := range datasetIDs {
		dataset, exists := byID[datasetID]
		if !exists {
			return ErrChatSourceAttachmentUnavailable
		}
		for _, attachment := range attachments {
			if attachment.datasetID == datasetID && attachment.tenantID != "" &&
				attachment.tenantID != strings.TrimSpace(dataset.TenantID) {
				return ErrChatSourceAttachmentUnavailable
			}
		}
		if !canAccessDataset(&dataset, caller.UserID, acl.PermissionDatasetRead) {
			return ErrChatSourceAttachmentForbidden
		}
	}
	items, checked := scanSourceAccessByDatasetForCaller(ctx, caller, datasetIDs, acl.PermissionDatasetRead)
	if checked {
		for _, datasetID := range datasetIDs {
			if item, exists := items[datasetID]; exists && item.Exists && !item.Allowed {
				return ErrChatSourceAttachmentForbidden
			}
		}
	}
	return nil
}

func chatSourceAttachmentPath(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	fullPath := raw
	if rel := relFromStaticFilesURL(raw); rel != "" {
		fullPath = resolveSignedStaticFullPath(rel)
	}
	relPath := fileRelativePath(fullPath)
	if relPath == "" || strings.HasPrefix(relPath, "subagent/") {
		return "", "", false
	}
	fullPath = resolveSignedStaticFullPath(relPath)
	if fullPath == "" {
		return "", "", false
	}
	return filepath.ToSlash(relPath), filepath.Clean(fullPath), true
}
