package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudresource"
)

type CloudAdapter struct {
	Service        *SkillService
	DesktopVersion string
}

func (a CloudAdapter) PrepareUpload(ctx context.Context, ownerUserID, localResourceID string) (cloudresource.UploadPackage, error) {
	if a.Service == nil || a.Service.db == nil || a.Service.blobStore == nil {
		return cloudresource.UploadPackage{}, gorm.ErrInvalidDB
	}
	owner := strings.TrimSpace(ownerUserID)
	identifier := strings.TrimSpace(localResourceID)
	var before skillRow
	if err := a.Service.db.WithContext(ctx).
		Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", identifier, owner).
		Take(&before).Error; err != nil {
		return cloudresource.UploadPackage{}, err
	}
	if before.HeadRevisionID == nil || strings.TrimSpace(*before.HeadRevisionID) == "" {
		return cloudresource.UploadPackage{}, errors.New("Skill has no committed Head revision")
	}
	draft, err := a.Service.draftSummary(ctx, identifier)
	if err != nil {
		return cloudresource.UploadPackage{}, err
	}
	if draft.HasUncommittedDraft {
		return cloudresource.UploadPackage{}, errors.New("Skill has an uncommitted draft; only the committed Head can be uploaded")
	}
	prepared, err := a.Service.PrepareCloudSkillPackage(ctx, CloudSkillExportRequest{
		OwnerUserID: owner, SkillID: identifier, DesktopVersion: a.desktopVersion(),
	})
	if err != nil {
		return cloudresource.UploadPackage{}, err
	}
	var after skillRow
	if err := a.Service.db.WithContext(ctx).Select("head_revision_id").Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", identifier, owner).Take(&after).Error; err != nil {
		return cloudresource.UploadPackage{}, err
	}
	if after.HeadRevisionID == nil || *after.HeadRevisionID != *before.HeadRevisionID {
		return cloudresource.UploadPackage{}, errors.New("Skill Head changed while preparing the Cloud upload")
	}
	return cloudresource.UploadPackage{
		Local: cloudresource.LocalSnapshot{
			Exists: true, ResourceID: before.ID, RevisionID: *before.HeadRevisionID, ContentHash: prepared.Manifest.ContentHash,
		},
		Prepared: prepared,
	}, nil
}

func (a CloudAdapter) Probe(ctx context.Context, ownerUserID, localResourceID string) (cloudresource.LocalSnapshot, error) {
	if a.Service == nil || a.Service.db == nil {
		return cloudresource.LocalSnapshot{}, gorm.ErrInvalidDB
	}
	var skill skillRow
	err := a.Service.db.WithContext(ctx).
		Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", strings.TrimSpace(localResourceID), strings.TrimSpace(ownerUserID)).
		Take(&skill).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cloudresource.LocalSnapshot{}, nil
	}
	if err != nil {
		return cloudresource.LocalSnapshot{}, err
	}
	if skill.HeadRevisionID == nil || strings.TrimSpace(*skill.HeadRevisionID) == "" {
		return cloudresource.LocalSnapshot{Exists: true, ResourceID: skill.ID}, nil
	}
	prepared, err := a.Service.PrepareCloudSkillPackage(ctx, CloudSkillExportRequest{
		OwnerUserID: ownerUserID, SkillID: skill.ID, DesktopVersion: a.desktopVersion(),
	})
	if err != nil {
		return cloudresource.LocalSnapshot{}, err
	}
	return cloudresource.LocalSnapshot{
		Exists: true, ResourceID: skill.ID, RevisionID: *skill.HeadRevisionID, ContentHash: prepared.Manifest.ContentHash,
	}, nil
}

func (a CloudAdapter) FindExact(ctx context.Context, ownerUserID, resourceName, contentHash string) (*cloudresource.LocalSnapshot, error) {
	if a.Service == nil || a.Service.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var rows []skillRow
	if err := a.Service.db.WithContext(ctx).
		Where("owner_user_id = ? AND skill_name = ? AND deleted_at IS NULL", strings.TrimSpace(ownerUserID), strings.TrimSpace(resourceName)).
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
	if a.Service == nil || a.Service.db == nil || a.Service.blobStore == nil {
		return cloudresource.LocalSnapshot{}, gorm.ErrInvalidDB
	}
	files := make(map[string][]byte, len(request.Files))
	for path, file := range request.Files {
		files[path] = file.Data
	}
	var imported cloudresource.LocalSnapshot
	err := a.Service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		service := *a.Service
		service.db = tx
		if service.blobStore != nil {
			blobStore := *service.blobStore
			blobStore.db = tx
			service.blobStore = &blobStore
		}
		created, err := service.ImportCloudSkillPackage(ctx, CloudSkillImportRequest{
			OwnerUserID: request.OwnerUserID, OwnerUserName: request.OwnerUserID, Files: files,
		})
		if err != nil {
			return err
		}
		txAdapter := CloudAdapter{Service: &service, DesktopVersion: a.desktopVersion()}
		imported, err = txAdapter.Probe(ctx, request.OwnerUserID, created.SkillID)
		if err != nil {
			return err
		}
		if !imported.Exists || imported.ContentHash != request.Resource.ContentHash {
			return errors.New("imported Skill content does not match the Cloud resource")
		}
		_, err = cloudbinding.NewRepository(tx).Upsert(ctx, cloudbinding.Binding{
			CloudIssuer: request.CloudIssuer, CloudAccountID: request.CloudAccountID, ResourceType: "skill",
			CloudResourceID: request.Resource.ResourceID, ClientResourceKey: request.Resource.ClientResourceKey,
			CloudContentHash: request.Resource.ContentHash, LocalResourceID: imported.ResourceID,
			InstalledLocalRevisionID: imported.RevisionID, InstalledLocalContentHash: imported.ContentHash,
			CloudResourceName: request.Resource.ResourceName,
		})
		return err
	})
	if err != nil {
		return cloudresource.LocalSnapshot{}, fmt.Errorf("import Cloud Skill: %w", err)
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
var _ cloudresource.UploadAdapter = CloudAdapter{}
