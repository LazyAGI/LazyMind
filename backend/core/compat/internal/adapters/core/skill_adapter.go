package core

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/compat/contract"
	compatskill "lazymind/core/compat/skill"
	skillrevision "lazymind/core/skillv2/revision"
	skillservice "lazymind/core/skillv2/service"
)

const skillMDPath = "SKILL.md"

type SkillService interface {
	ListSkills(ctx context.Context, req skillservice.ListSkillsRequest) (skillservice.ListSkillsResponse, error)
	GetSkill(ctx context.Context, req skillservice.GetSkillRequest) (skillservice.SkillDetail, error)
}

type RevisionReader interface {
	ReadRevisionFile(ctx context.Context, req skillrevision.ReadRevisionFileRequest) (skillrevision.FileContent, error)
}

type SkillAdapter struct {
	service        SkillService
	revisionReader RevisionReader
}

func NewSkillAdapter(service SkillService, revisionReader RevisionReader) (*SkillAdapter, error) {
	if service == nil {
		return nil, contract.NewError(contract.Internal, "skill.adapter.new", "skill service is required", false, nil)
	}
	if revisionReader == nil {
		return nil, contract.NewError(contract.Internal, "skill.adapter.new", "revision reader is required", false, nil)
	}
	return &SkillAdapter{service: service, revisionReader: revisionReader}, nil
}

func NewSkillAdapterForDB(service *skillservice.SkillService, db *gorm.DB) (*SkillAdapter, error) {
	if service == nil {
		return nil, contract.NewError(contract.Internal, "skill.adapter.new", "skill service is required", false, nil)
	}
	if db == nil {
		return nil, contract.NewError(contract.Internal, "skill.adapter.new", "gorm db is required", false, nil)
	}
	revisionReader := skillrevision.NewService(skillrevision.ServiceDeps{
		DB:        db,
		BlobStore: skillrevision.NewBlobStore(db, skillrevision.NewLocalObjectStore("")),
	})
	return NewSkillAdapter(service, revisionReader)
}

func (a *SkillAdapter) List(ctx context.Context, callCtx contract.CallContext, input compatskill.ListInput) (compatskill.ListResult, error) {
	userID := strings.TrimSpace(callCtx.UserID)
	if userID == "" {
		return compatskill.ListResult{}, contract.InvalidArgumentError("skill.list", "user_id is required")
	}
	page := input.Page.Normalize()
	offset, err := contract.DecodeOffsetPageToken(page.PageToken)
	if err != nil {
		return compatskill.ListResult{}, contract.NewError(contract.InvalidArgument, "skill.list", "invalid page token", false, err)
	}
	resp, err := a.service.ListSkills(ctx, skillservice.ListSkillsRequest{
		UserID:   userID,
		Keyword:  strings.TrimSpace(input.Keyword),
		Category: strings.TrimSpace(input.Category),
		Tags:     append([]string(nil), input.Tags...),
		Offset:   offset,
		Limit:    page.PageSize,
	})
	if err != nil {
		return compatskill.ListResult{}, mapServiceError("skill.list", err)
	}
	items := make([]compatskill.Summary, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, mapSummary(item))
	}
	total := resp.Total
	result := compatskill.ListResult{
		Items: items,
		Page:  contract.PageResult{Total: &total},
	}
	if offset+len(resp.Items) < int(resp.Total) {
		result.Page.NextPageToken = contract.EncodeOffsetPageToken(offset + len(resp.Items))
	}
	return result, nil
}

func (a *SkillAdapter) GetMetadata(ctx context.Context, callCtx contract.CallContext, skillID string) (compatskill.Summary, error) {
	userID := strings.TrimSpace(callCtx.UserID)
	if userID == "" {
		return compatskill.Summary{}, contract.InvalidArgumentError("skill.get", "user_id is required")
	}
	detail, err := a.service.GetSkill(ctx, skillservice.GetSkillRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		return compatskill.Summary{}, mapServiceError("skill.get", err)
	}
	return mapSummary(detail.SkillSummary), nil
}

func (a *SkillAdapter) ReadContent(ctx context.Context, callCtx contract.CallContext, skillID, revisionID string) (compatskill.Content, error) {
	userID := strings.TrimSpace(callCtx.UserID)
	if userID == "" {
		return compatskill.Content{}, contract.InvalidArgumentError("skill.read_content", "user_id is required")
	}
	revisionID = strings.TrimSpace(revisionID)
	if revisionID == "" {
		return compatskill.Content{}, contract.InvalidArgumentError("skill.read_content", "revision_id is required")
	}
	// Revision reads do not perform owner checks, so the adapter binds access to
	// the metadata path before reading immutable revision content.
	if _, err := a.service.GetSkill(ctx, skillservice.GetSkillRequest{SkillID: skillID, UserID: userID}); err != nil {
		return compatskill.Content{}, mapServiceError("skill.read_content", err)
	}
	file, err := a.revisionReader.ReadRevisionFile(ctx, skillrevision.ReadRevisionFileRequest{SkillID: skillID, RevisionID: revisionID, Path: skillMDPath})
	if err != nil {
		return compatskill.Content{}, mapServiceError("skill.read_content", err)
	}
	if file.Binary {
		return compatskill.Content{}, contract.NewError(contract.Unsupported, "skill.read_content", "SKILL.md is binary", false, nil)
	}
	return compatskill.Content{Path: file.Path, RevisionID: revisionID, Text: file.Content}, nil
}

func mapSummary(item skillservice.SkillSummary) compatskill.Summary {
	return compatskill.Summary{
		ID:             item.ID,
		Name:           item.Name,
		Description:    item.Description,
		Category:       item.Category,
		Tags:           append([]string(nil), item.Tags...),
		HeadRevisionID: item.HeadRevisionID,
		AutoEvo:        item.AutoEvo,
		Enabled:        item.IsEnabled,
		Draft: compatskill.DraftSummary{
			HasUncommittedDraft: item.Draft.HasUncommittedDraft,
			TaskID:              item.Draft.TaskID,
			Version:             item.Draft.Version,
		},
	}
}

func mapServiceError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var compatErr *contract.Error
	if errors.As(err, &compatErr) {
		return err
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), strings.Contains(msg, "not found"):
		return contract.NewError(contract.NotFound, operation, "skill not found", false, err)
	case strings.Contains(msg, "stale"), strings.Contains(msg, "conflict"), strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"):
		return contract.NewError(contract.Conflict, operation, "skill conflict", false, err)
	case strings.Contains(msg, "unsupported"):
		return contract.NewError(contract.Unsupported, operation, "unsupported skill operation", false, err)
	case strings.Contains(msg, "db is not configured"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "timeout"):
		return contract.NewError(contract.BackendUnavailable, operation, "backend unavailable", true, err)
	default:
		return contract.NewError(contract.Internal, operation, "internal error", false, err)
	}
}
