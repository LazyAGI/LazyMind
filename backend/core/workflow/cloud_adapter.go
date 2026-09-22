package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudresource"
	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

type CloudAdapter struct {
	DB             *gorm.DB
	DesktopVersion string
}

func (a CloudAdapter) Probe(ctx context.Context, ownerUserID, localResourceID string) (cloudresource.LocalSnapshot, error) {
	if a.DB == nil {
		return cloudresource.LocalSnapshot{}, gorm.ErrInvalidDB
	}
	value, err := workflowstore.New(a.DB).GetWorkflowPackage(ctx, strings.TrimSpace(ownerUserID), strings.TrimSpace(localResourceID), "")
	if errors.Is(err, workflowstore.ErrNotFound) {
		return cloudresource.LocalSnapshot{}, nil
	}
	if err != nil {
		return cloudresource.LocalSnapshot{}, err
	}
	prepared, err := PrepareCloudWorkflowPackage(ctx, CloudWorkflowExportRequest{
		OwnerUserID: ownerUserID, WorkflowRef: localResourceID, DesktopVersion: a.desktopVersion(), Reader: workflowstore.New(a.DB),
	})
	if err != nil {
		return cloudresource.LocalSnapshot{}, err
	}
	return cloudresource.LocalSnapshot{
		Exists: true, ResourceID: value.ResourceID, ResourceRef: value.WorkflowRef,
		RevisionID: value.RevisionID, ContentHash: prepared.Manifest.ContentHash,
	}, nil
}

func (a CloudAdapter) FindExact(ctx context.Context, ownerUserID, resourceName, contentHash string) (*cloudresource.LocalSnapshot, error) {
	if a.DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	var rows []orm.WorkflowResource
	if err := a.DB.WithContext(ctx).
		Where("owner_user_id = ? AND name = ? AND status = ?", strings.TrimSpace(ownerUserID), strings.TrimSpace(resourceName), "active").
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var match *cloudresource.LocalSnapshot
	for _, row := range rows {
		snapshot, err := a.Probe(ctx, ownerUserID, row.ID)
		if err != nil {
			return nil, err
		}
		if !snapshot.Exists || snapshot.ContentHash != contentHash {
			continue
		}
		if match != nil {
			return nil, nil
		}
		copy := snapshot
		match = &copy
	}
	return match, nil
}

func (a CloudAdapter) ImportAndBind(ctx context.Context, request cloudresource.ImportRequest) (cloudresource.LocalSnapshot, error) {
	if a.DB == nil {
		return cloudresource.LocalSnapshot{}, gorm.ErrInvalidDB
	}
	files := make(map[string][]byte, len(request.Files))
	for path, file := range request.Files {
		files[path] = file.Data
	}
	var imported cloudresource.LocalSnapshot
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result, err := ImportCloudWorkflowPackage(ctx, CloudWorkflowImportRequest{DB: tx, OwnerUserID: request.OwnerUserID, Files: files})
		if err != nil {
			return err
		}
		txAdapter := CloudAdapter{DB: tx, DesktopVersion: a.desktopVersion()}
		imported, err = txAdapter.Probe(ctx, request.OwnerUserID, result.WorkflowRef)
		if err != nil {
			return err
		}
		if !imported.Exists || imported.ContentHash != request.Resource.ContentHash {
			return errors.New("imported Workflow content does not match the Cloud resource")
		}
		_, err = cloudbinding.NewRepository(tx).Upsert(ctx, cloudbinding.Binding{
			CloudIssuer: request.CloudIssuer, CloudAccountID: request.CloudAccountID, ResourceType: "workflow",
			CloudResourceID: request.Resource.ResourceID, ClientResourceKey: request.Resource.ClientResourceKey,
			CloudContentHash: request.Resource.ContentHash, LocalResourceID: imported.ResourceID,
			LocalResourceRef: imported.ResourceRef, InstalledLocalRevisionID: imported.RevisionID,
			InstalledLocalContentHash: imported.ContentHash, CloudResourceName: request.Resource.ResourceName,
		})
		return err
	})
	if err != nil {
		return cloudresource.LocalSnapshot{}, fmt.Errorf("import Cloud Workflow: %w", err)
	}
	return imported, nil
}

func (a CloudAdapter) desktopVersion() string {
	if value := strings.TrimSpace(a.DesktopVersion); value != "" {
		return value
	}
	return "0.0.0"
}

var _ cloudresource.LocalAdapter = CloudAdapter{}
