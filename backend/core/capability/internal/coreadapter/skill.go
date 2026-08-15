package coreadapter

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/capability"
	skillrevision "lazymind/core/skillv2/revision"
	skillservice "lazymind/core/skillv2/service"
)

const skillMDPath = "SKILL.md"

type SkillService interface {
	ListSkills(context.Context, skillservice.ListSkillsRequest) (skillservice.ListSkillsResponse, error)
	GetSkill(context.Context, skillservice.GetSkillRequest) (skillservice.SkillDetail, error)
}

type RevisionReader interface {
	ReadRevisionFile(context.Context, skillrevision.ReadRevisionFileRequest) (skillrevision.FileContent, error)
}

type SkillReader struct {
	service   SkillService
	revisions RevisionReader
}

func NewSkillReader(service SkillService, revisions RevisionReader) (*SkillReader, error) {
	if service == nil {
		return nil, capability.NewError(capability.Internal, "skill.adapter.new", "skill service is required", false, nil)
	}
	if revisions == nil {
		return nil, capability.NewError(capability.Internal, "skill.adapter.new", "revision reader is required", false, nil)
	}
	return &SkillReader{service: service, revisions: revisions}, nil
}

func NewSkillReaderForDB(db *gorm.DB) (*SkillReader, error) {
	if db == nil {
		return nil, capability.NewError(capability.Internal, "skill.adapter.new", "gorm db is required", false, nil)
	}
	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db})
	revisions := skillrevision.NewService(skillrevision.ServiceDeps{
		DB:        db,
		BlobStore: skillrevision.NewBlobStore(db, skillrevision.NewLocalObjectStore("")),
	})
	return NewSkillReader(service, revisions)
}

func (r *SkillReader) ListSkills(ctx context.Context, call capability.InvocationContext, query capability.SkillListQuery) (capability.SkillListPage, error) {
	resp, err := r.service.ListSkills(ctx, skillservice.ListSkillsRequest{
		UserID:      strings.TrimSpace(call.Principal.UserID),
		Keyword:     query.Keyword,
		Category:    query.Category,
		Tags:        append([]string(nil), query.Tags...),
		Offset:      query.Offset,
		Limit:       query.Limit,
		EnabledOnly: true,
	})
	if err != nil {
		return capability.SkillListPage{}, mapSkillError("skill.list", err)
	}
	items := make([]capability.SkillSummary, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, skillSummary(item))
	}
	return capability.SkillListPage{Items: items, Total: resp.Total}, nil
}

func (r *SkillReader) GetSkillMetadata(ctx context.Context, call capability.InvocationContext, skillID string) (capability.SkillMetadata, error) {
	detail, err := r.service.GetSkill(ctx, skillservice.GetSkillRequest{
		SkillID: skillID,
		UserID:  strings.TrimSpace(call.Principal.UserID),
	})
	if err != nil {
		return capability.SkillMetadata{}, mapSkillError("skill.get", err)
	}
	return capability.SkillMetadata{Summary: skillSummary(detail.SkillSummary), Published: detail.IsEnabled}, nil
}

func (r *SkillReader) ReadSkillContent(ctx context.Context, call capability.InvocationContext, skillID, revisionID string) (capability.SkillContent, error) {
	userID := strings.TrimSpace(call.Principal.UserID)
	if _, err := r.service.GetSkill(ctx, skillservice.GetSkillRequest{SkillID: skillID, UserID: userID}); err != nil {
		return capability.SkillContent{}, mapSkillError("skill.get", err)
	}
	file, err := r.revisions.ReadRevisionFile(ctx, skillrevision.ReadRevisionFileRequest{
		SkillID: skillID, RevisionID: revisionID, Path: skillMDPath,
	})
	if err != nil {
		return capability.SkillContent{}, mapSkillError("skill.get", err)
	}
	if file.Binary {
		return capability.SkillContent{}, capability.NewError(capability.Unsupported, "skill.get", "SKILL.md is binary", false, nil)
	}
	return capability.SkillContent{RevisionID: revisionID, Text: file.Content}, nil
}

func skillSummary(item skillservice.SkillSummary) capability.SkillSummary {
	return capability.SkillSummary{
		ID:             item.ID,
		Name:           item.Name,
		Description:    item.Description,
		Category:       item.Category,
		Tags:           append([]string(nil), item.Tags...),
		HeadRevisionID: item.HeadRevisionID,
	}
}
