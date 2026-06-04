package evalset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

var (
	errInvalidImportToken = errors.New("invalid import_token")
	errImportTaskNotFound = errors.New("import task not found")
)

func (s *Service) CreateByImport(ctx context.Context, req CreateEvalSetByImportRequest, userID, userName string) (*CreateEvalSetByImportResponse, error) {
	createReq := CreateEvalSetRequest{
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		DatasetIDs:  normalizeDatasetIDs(req.DatasetIDs),
		GroupID:     strings.TrimSpace(req.GroupID),
	}
	if err := validateCreateRequest(createReq); err != nil {
		return nil, err
	}
	importToken := strings.TrimSpace(req.ImportToken)
	if importToken == "" {
		return nil, errInvalidImportToken
	}

	evalSetID := newEvalSetID()
	var jobID string
	err := s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		preview, err := consumeReadyImportPreview(ctx, tx, importToken, userID)
		if err != nil {
			return err
		}
		job, err := asyncjob.Enqueue(ctx, tx, asyncjob.EnqueueRequest{
			JobType:        importJobType,
			ResourceType:   acl.ResourceTypeEvalSet,
			ResourceID:     evalSetID,
			IdempotencyKey: "eval_set_import:" + importToken,
			Payload: EvalSetImportJobPayload{
				Mode:        importModeCreate,
				EvalSetID:   evalSetID,
				Name:        createReq.Name,
				Description: createReq.Description,
				DatasetIDs:  createReq.DatasetIDs,
				GroupID:     createReq.GroupID,
				ImportToken: importToken,
				TempPath:    preview.TempPath,
				FileName:    preview.FileName,
				FileType:    preview.FileType,
				TotalRows:   preview.TotalRows,
				ValidRows:   preview.ValidRows,
			},
			MaxAttempts:    1,
			CreateUserID:   userID,
			CreateUserName: userName,
		})
		if err != nil {
			return err
		}
		jobID = job.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &CreateEvalSetByImportResponse{EvalSetID: evalSetID, TaskID: jobID}, nil
}

func (s *Service) AppendImport(ctx context.Context, evalSetID string, req AppendEvalSetImportRequest, userID, userName string, groupIDs []string) (*AppendEvalSetImportResponse, error) {
	evalSetID = strings.TrimSpace(evalSetID)
	if evalSetID == "" {
		return nil, errEvalSetNotFound
	}
	evalSet, err := s.requireEvalSetPermission(ctx, evalSetID, userID, groupIDs, acl.PermissionEvalSetWrite)
	if err != nil {
		return nil, err
	}
	importToken := strings.TrimSpace(req.ImportToken)
	if importToken == "" {
		return nil, errInvalidImportToken
	}

	var jobID string
	err = s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		preview, err := consumeReadyImportPreview(ctx, tx, importToken, userID)
		if err != nil {
			return err
		}
		job, err := asyncjob.Enqueue(ctx, tx, asyncjob.EnqueueRequest{
			JobType:        importJobType,
			ResourceType:   acl.ResourceTypeEvalSet,
			ResourceID:     evalSet.ID,
			IdempotencyKey: "eval_set_import:" + importToken,
			Payload: EvalSetImportJobPayload{
				Mode:        importModeAppend,
				EvalSetID:   evalSet.ID,
				ImportToken: importToken,
				TempPath:    preview.TempPath,
				FileName:    preview.FileName,
				FileType:    preview.FileType,
				TotalRows:   preview.TotalRows,
				ValidRows:   preview.ValidRows,
			},
			MaxAttempts:    1,
			CreateUserID:   userID,
			CreateUserName: userName,
		})
		if err != nil {
			return err
		}
		jobID = job.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &AppendEvalSetImportResponse{TaskID: jobID}, nil
}

func (s *Service) GetImportTask(ctx context.Context, taskID, userID string, groupIDs []string) (*EvalSetImportTaskResponse, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errImportTaskNotFound
	}

	job, err := asyncjob.Get(ctx, s.repo.db, taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errImportTaskNotFound
		}
		return nil, err
	}
	if job.JobType != importJobType {
		return nil, errImportTaskNotFound
	}

	payload := EvalSetImportJobPayload{}
	if len(job.PayloadJSON) > 0 {
		_ = json.Unmarshal(job.PayloadJSON, &payload)
	}
	evalSetID := strings.TrimSpace(payload.EvalSetID)
	if evalSetID == "" {
		evalSetID = job.ResourceID
	}

	var datasetIDs []string
	evalSet, err := s.repo.GetActive(ctx, evalSetID)
	if err == nil {
		datasetIDs = parseDatasetIDsJSON(evalSet.DatasetIDs)
		perms := evalSetPermissionsForUser(evalSet, userID, groupIDs)
		if !hasPermission(perms, acl.PermissionEvalSetRead) && !hasPermission(perms, acl.PermissionEvalSetWrite) {
			return nil, errForbidden
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		if job.CreateUserID != userID {
			return nil, errForbidden
		}
		datasetIDs = normalizeDatasetIDs(payload.DatasetIDs)
	} else {
		return nil, err
	}

	names, _ := s.repo.DatasetNames(ctx, datasetIDs)
	return importTaskResponse(job, payload, datasetIDs, names), nil
}

func consumeReadyImportPreview(ctx context.Context, tx *gorm.DB, importToken, userID string) (*orm.EvalSetImportPreview, error) {
	now := time.Now().UTC()
	var preview orm.EvalSetImportPreview
	err := withUpdateLock(tx.WithContext(ctx)).
		Where("token = ? AND create_user_id = ? AND status = ? AND expires_at > ?", importToken, userID, importPreviewStatusReady, now).
		First(&preview).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errInvalidImportToken
		}
		return nil, err
	}
	if err := tx.WithContext(ctx).Model(&orm.EvalSetImportPreview{}).
		Where("token = ? AND status = ?", preview.Token, importPreviewStatusReady).
		Updates(map[string]any{
			"status":      importPreviewStatusConsumed,
			"consumed_at": now,
		}).Error; err != nil {
		return nil, err
	}
	return &preview, nil
}

func importTaskResponse(job *orm.AsyncJob, payload EvalSetImportJobPayload, datasetIDs []string, datasetNames map[string]string) *EvalSetImportTaskResponse {
	result := importJobResult{}
	if len(job.ResultJSON) > 0 {
		_ = json.Unmarshal(job.ResultJSON, &result)
	}
	errorDetails := []ImportValidationErrorDetail{}
	if len(job.ErrorDetailsJSON) > 0 {
		_ = json.Unmarshal(job.ErrorDetailsJSON, &errorDetails)
	}
	evalSetID := strings.TrimSpace(payload.EvalSetID)
	if evalSetID == "" {
		evalSetID = job.ResourceID
	}
	if result.EvalSetID != "" {
		evalSetID = result.EvalSetID
	}
	return &EvalSetImportTaskResponse{
		ID:              job.ID,
		EvalSetID:       evalSetID,
		DatasetIDs:      datasetIDs,
		DatasetNames:    datasetNamesForIDs(datasetIDs, datasetNames),
		Status:          job.Status,
		FileName:        payload.FileName,
		FileType:        payload.FileType,
		TotalRows:       payload.TotalRows,
		ValidRows:       payload.ValidRows,
		InsertedRows:    result.InsertedRows,
		ProgressCurrent: job.ProgressCurrent,
		ProgressTotal:   job.ProgressTotal,
		ErrorCode:       job.ErrorCode,
		ErrorMessage:    job.ErrorMessage,
		ErrorDetails:    errorDetails,
		CreatedAt:       job.CreatedAt,
		StartedAt:       job.StartedAt,
		FinishedAt:      job.FinishedAt,
	}
}
