package service

import (
	"context"
	"errors"
	"os"
	"strings"

	"lazymind/core/cloudpackage"
	skillmetadata "lazymind/core/skillv2/metadata"
)

type CloudSkillExportRequest struct {
	OwnerUserID    string
	SkillID        string
	DesktopVersion string
}

func (s *SkillService) PrepareCloudSkillPackage(ctx context.Context, request CloudSkillExportRequest) (cloudpackage.Prepared, error) {
	if s == nil || s.db == nil || s.blobStore == nil || strings.TrimSpace(request.OwnerUserID) == "" || strings.TrimSpace(request.SkillID) == "" {
		return cloudpackage.Prepared{}, errors.New("cloud Skill export request is incomplete")
	}
	var skill skillRow
	if err := s.db.WithContext(ctx).
		Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", strings.TrimSpace(request.SkillID), strings.TrimSpace(request.OwnerUserID)).
		Take(&skill).Error; err != nil {
		return cloudpackage.Prepared{}, err
	}
	if skill.HeadRevisionID == nil || strings.TrimSpace(*skill.HeadRevisionID) == "" {
		return cloudpackage.Prepared{}, errors.New("Skill has no head revision")
	}
	var entries []skillRevisionEntryRow
	if err := s.db.WithContext(ctx).Where("revision_id = ? AND entry_type = ?", *skill.HeadRevisionID, "file").Order("path ASC").Find(&entries).Error; err != nil {
		return cloudpackage.Prepared{}, err
	}
	files := make(map[string]cloudpackage.File, len(entries))
	for _, entry := range entries {
		if entry.BlobHash == nil || strings.TrimSpace(*entry.BlobHash) == "" {
			return cloudpackage.Prepared{}, errors.New("Skill head contains a file without content")
		}
		body, err := s.blobStore.Get(ctx, *entry.BlobHash)
		if err != nil {
			return cloudpackage.Prepared{}, err
		}
		files[entry.Path] = cloudpackage.File{Data: body, Executable: entry.Mode&0o111 != 0}
	}
	return cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "skill", ResourceName: skill.SkillName,
		ClientResourceKey: "skill:" + skill.ID,
		DesktopVersion:    strings.TrimSpace(request.DesktopVersion), Files: files,
	})
}

type CloudSkillImportRequest struct {
	OwnerUserID   string
	OwnerUserName string
	Files         map[string][]byte
}

func (s *SkillService) ImportCloudSkillPackage(ctx context.Context, request CloudSkillImportRequest) (CreateSkillResponse, error) {
	owner := strings.TrimSpace(request.OwnerUserID)
	if s == nil || s.db == nil || owner == "" {
		return CreateSkillResponse{}, errors.New("cloud Skill import request is incomplete")
	}
	files := make(map[string]cloudpackage.File, len(request.Files))
	for path, body := range request.Files {
		files[path] = cloudpackage.File{Data: body}
	}
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "skill", ResourceName: "cloud-import", ClientResourceKey: "cloud-import",
		DesktopVersion: "0.0.0", Files: files,
	})
	if err != nil {
		return CreateSkillResponse{}, err
	}
	prepared, err = cloudpackage.WriteZIP(prepared)
	if err != nil {
		return CreateSkillResponse{}, err
	}
	defer os.Remove(prepared.ZIPPath)
	metadata, err := skillmetadata.FromFiles(request.Files)
	if err != nil {
		return CreateSkillResponse{}, err
	}
	return s.CreateSkill(ctx, CreateSkillRequest{
		OwnerUserID: owner, OwnerUserName: strings.TrimSpace(request.OwnerUserName),
		CreateUserID: owner, CreateUserName: strings.TrimSpace(request.OwnerUserName),
		Name: metadata.Name, Category: skillmetadata.ExternalCategory, Description: metadata.Description,
		Source: SourceInput{Type: "local_zip", StoredPath: prepared.ZIPPath, Filename: "lazymind-cloud-skill.zip"},
	})
}
