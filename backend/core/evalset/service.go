package evalset

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

var (
	errForbidden       = errors.New("forbidden")
	errEvalSetNotFound = errors.New("eval set not found")
)

type Service struct {
	repo *Repository
}

func NewService(db *gorm.DB) *Service {
	return &Service{repo: NewRepository(db)}
}

func (s *Service) List(ctx context.Context, userID string, groupIDs []string, filter ListFilter) (*ListEvalSetsResponse, error) {
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.DatasetIDs = normalizeDatasetIDs(filter.DatasetIDs)
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 10
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}

	rows, total, err := s.repo.List(ctx, userID, groupIDs, filter)
	if err != nil {
		return nil, err
	}
	responses, err := s.responsesForRows(ctx, rows, userID, groupIDs)
	if err != nil {
		return nil, err
	}
	return &ListEvalSetsResponse{
		Items:    responses,
		Total:    total,
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}, nil
}

func (s *Service) Create(ctx context.Context, req CreateEvalSetRequest, userID, userName string) (*EvalSetResponse, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.DatasetIDs = normalizeDatasetIDs(req.DatasetIDs)
	req.GroupID = strings.TrimSpace(req.GroupID)
	if err := validateCreateRequest(req); err != nil {
		return nil, err
	}

	row, err := s.repo.Create(ctx, req, userID, userName)
	if err != nil {
		return nil, err
	}
	datasetIDs := parseDatasetIDsJSON(row.DatasetIDs)
	names, _ := s.repo.DatasetNames(ctx, datasetIDs)
	return evalSetResponse(row, names, []string{acl.PermissionEvalSetRead, acl.PermissionEvalSetWrite}), nil
}

func (s *Service) Get(ctx context.Context, id, userID string, groupIDs []string) (*EvalSetResponse, error) {
	row, err := s.repo.GetActive(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errEvalSetNotFound
		}
		return nil, err
	}
	perms := evalSetPermissionsForUser(row, userID, groupIDs)
	if !hasPermission(perms, acl.PermissionEvalSetRead) && !hasPermission(perms, acl.PermissionEvalSetWrite) {
		return nil, errForbidden
	}
	datasetIDs := parseDatasetIDsJSON(row.DatasetIDs)
	names, _ := s.repo.DatasetNames(ctx, datasetIDs)
	return evalSetResponse(row, names, normalizedEvalSetPermissions(perms)), nil
}

func (s *Service) Update(ctx context.Context, id string, req UpdateEvalSetRequest, userID string, groupIDs []string) (*EvalSetResponse, error) {
	update, err := normalizeUpdateRequest(req)
	if err != nil {
		return nil, err
	}

	row, err := s.repo.GetActive(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errEvalSetNotFound
		}
		return nil, err
	}
	if !hasPermission(evalSetPermissionsForUser(row, userID, groupIDs), acl.PermissionEvalSetWrite) {
		return nil, errForbidden
	}

	updated, err := s.repo.Update(ctx, id, update, userID)
	if err != nil {
		return nil, err
	}
	datasetIDs := parseDatasetIDsJSON(updated.DatasetIDs)
	names, _ := s.repo.DatasetNames(ctx, datasetIDs)
	return evalSetResponse(updated, names, evalSetPermissionsForUser(updated, userID, groupIDs)), nil
}

func (s *Service) Delete(ctx context.Context, id, userID string, groupIDs []string) error {
	row, err := s.repo.GetActive(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errEvalSetNotFound
		}
		return err
	}
	if !hasPermission(evalSetPermissionsForUser(row, userID, groupIDs), acl.PermissionEvalSetWrite) {
		return errForbidden
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errEvalSetNotFound
		}
		return err
	}
	return nil
}

func (s *Service) ListDatasetOptions(ctx context.Context, userID string) (*DatasetOptionsResponse, error) {
	rows, err := s.repo.ListKBOptions(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]DatasetOption, 0, len(rows))
	for _, row := range rows {
		if !acl.Can(userID, acl.ResourceTypeKB, row.ID, acl.PermRead) {
			continue
		}
		items = append(items, DatasetOption{ID: row.ID, Name: row.Name})
	}
	return &DatasetOptionsResponse{Items: items}, nil
}

func (s *Service) ListQuestionTypeOptions() QuestionTypeOptionsResponse {
	return QuestionTypeOptionsResponse{
		Items: []QuestionTypeOption{
			{Value: "1", Label: "1"},
			{Value: "2", Label: "2"},
			{Value: "操作问答", Label: "操作问答"},
		},
	}
}

func (s *Service) responsesForRows(ctx context.Context, rows []orm.EvalSet, userID string, groupIDs []string) ([]EvalSetResponse, error) {
	datasetIDs := collectEvalSetDatasetIDs(rows)
	names, err := s.repo.DatasetNames(ctx, datasetIDs)
	if err != nil {
		return nil, err
	}

	out := make([]EvalSetResponse, 0, len(rows))
	for i := range rows {
		row := rows[i]
		out = append(out, *evalSetResponse(&row, names, evalSetPermissionsForUser(&row, userID, groupIDs)))
	}
	return out, nil
}

func validateCreateRequest(req CreateEvalSetRequest) error {
	if req.Name == "" {
		return errors.New("name required")
	}
	if utf8.RuneCountInString(req.Name) > 255 {
		return errors.New("name too long")
	}
	if utf8.RuneCountInString(req.Description) > 4096 {
		return errors.New("description too long")
	}
	if err := validateDatasetIDs(req.DatasetIDs); err != nil {
		return err
	}
	if utf8.RuneCountInString(req.GroupID) > 255 {
		return errors.New("group_id too long")
	}
	return nil
}

func normalizeUpdateRequest(req UpdateEvalSetRequest) (EvalSetUpdate, error) {
	if req.Name == nil && req.Description == nil && req.DatasetIDs == nil && req.GroupID == nil {
		return EvalSetUpdate{}, errors.New("at least one field required")
	}

	update := EvalSetUpdate{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return EvalSetUpdate{}, errors.New("name required")
		}
		if utf8.RuneCountInString(name) > 255 {
			return EvalSetUpdate{}, errors.New("name too long")
		}
		update.Name = &name
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		if utf8.RuneCountInString(description) > 4096 {
			return EvalSetUpdate{}, errors.New("description too long")
		}
		update.Description = &description
	}
	if req.DatasetIDs != nil {
		datasetIDs := normalizeDatasetIDs(*req.DatasetIDs)
		if err := validateDatasetIDs(datasetIDs); err != nil {
			return EvalSetUpdate{}, err
		}
		update.DatasetIDs = &datasetIDs
	}
	if req.GroupID != nil {
		groupID := strings.TrimSpace(*req.GroupID)
		if utf8.RuneCountInString(groupID) > 255 {
			return EvalSetUpdate{}, errors.New("group_id too long")
		}
		update.GroupID = &groupID
	}
	return update, nil
}

func evalSetPermissionsForUser(row *orm.EvalSet, userID string, groupIDs []string) []string {
	if row == nil {
		return nil
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	if row.OwnerID == userID {
		return []string{acl.PermissionEvalSetRead, acl.PermissionEvalSetWrite}
	}
	perms, _ := acl.PermissionsForWithGroups(acl.ResourceTypeEvalSet, row.ID, userID, groupIDs)
	if row.GroupID != "" && containsString(groupIDs, row.GroupID) {
		perms = append(perms, acl.PermissionEvalSetRead, acl.PermissionEvalSetWrite)
	}
	return normalizedEvalSetPermissions(perms)
}

func normalizedEvalSetPermissions(perms []string) []string {
	write := hasPermission(perms, acl.PermissionEvalSetWrite)
	read := hasPermission(perms, acl.PermissionEvalSetRead) || write
	out := make([]string, 0, 2)
	if read {
		out = append(out, acl.PermissionEvalSetRead)
	}
	if write {
		out = append(out, acl.PermissionEvalSetWrite)
	}
	return out
}

func hasPermission(perms []string, want string) bool {
	for _, perm := range perms {
		if perm == want {
			return true
		}
	}
	return false
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func validateDatasetIDs(datasetIDs []string) error {
	if len(datasetIDs) == 0 {
		return errors.New("dataset_ids required")
	}
	for _, id := range datasetIDs {
		if utf8.RuneCountInString(id) > 255 {
			return errors.New("dataset_ids too long")
		}
	}
	return nil
}

func evalSetResponse(row *orm.EvalSet, datasetNames map[string]string, permissions []string) *EvalSetResponse {
	datasetIDs := parseDatasetIDsJSON(row.DatasetIDs)
	return &EvalSetResponse{
		ID:            row.ID,
		Name:          row.Name,
		Description:   row.Description,
		DatasetIDs:    datasetIDs,
		DatasetNames:  datasetNamesForIDs(datasetIDs, datasetNames),
		GroupID:       row.GroupID,
		ShardID:       row.ShardID,
		ItemCount:     row.ItemCount,
		CreatedBy:     row.CreateUserID,
		CreatedByName: row.CreateUserName,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		Permissions:   normalizedEvalSetPermissions(permissions),
	}
}
