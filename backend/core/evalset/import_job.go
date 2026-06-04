package evalset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const (
	importJobType = "eval_set_import"

	importModeCreate = "create"
	importModeAppend = "append"

	importBatchSize = 500

	importErrorInvalidPayload  = "invalid_payload"
	importErrorTempFileMissing = "temp_file_missing"
	importErrorEvalSetNotFound = "eval_set_not_found"
	importErrorInsertFailed    = "insert_failed"
)

type EvalSetImportJobPayload struct {
	Mode        string   `json:"mode"`
	EvalSetID   string   `json:"eval_set_id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	DatasetIDs  []string `json:"dataset_ids,omitempty"`
	GroupID     string   `json:"group_id,omitempty"`
	ImportToken string   `json:"import_token"`
	TempPath    string   `json:"temp_path"`
	FileName    string   `json:"file_name"`
	FileType    string   `json:"file_type"`
	TotalRows   int64    `json:"total_rows"`
	ValidRows   int64    `json:"valid_rows"`
}

type importJobResult struct {
	EvalSetID    string `json:"eval_set_id"`
	InsertedRows int64  `json:"inserted_rows"`
}

func RegisterAsyncJobs() {
	asyncjob.Register(importJobType, HandleImportJob)
}

func HandleImportJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	payload, err := decodeImportJobPayload(job.PayloadJSON)
	if err != nil {
		return asyncjob.Result{ErrorCode: importErrorInvalidPayload}, err
	}

	rows, err := readImportTempRows(payload)
	if err != nil {
		return asyncjob.Result{ErrorCode: importErrorTempFileMissing}, err
	}
	if int64(len(rows)) != payload.ValidRows {
		return asyncjob.Result{ErrorCode: importErrorInvalidPayload}, fmt.Errorf("valid_rows mismatch: payload=%d file=%d", payload.ValidRows, len(rows))
	}

	if reporter != nil {
		if err := reporter.SetProgress(ctx, 0, payload.ValidRows); err != nil {
			return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, err
		}
	}

	db := store.DB()
	if db == nil {
		return asyncjob.Result{ErrorCode: asyncjob.ErrorCodeHandlerFailed}, errors.New("store not initialized")
	}

	var result asyncjob.Result
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, ok, err := lockedSucceededImportResult(ctx, tx, job.ID)
		if err != nil {
			return err
		}
		if ok {
			result = asyncjob.Result{ResultJSON: existing}
			return nil
		}

		evalSet, shard, err := prepareImportTarget(ctx, tx, payload, job)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%s: %w", importErrorEvalSetNotFound, err)
			}
			return err
		}

		items, estimatedBytes, err := buildImportItems(rows, evalSet, job)
		if err != nil {
			return err
		}
		insertedRows := int64(len(items))
		for start := 0; start < len(items); start += importBatchSize {
			end := start + importBatchSize
			if end > len(items) {
				end = len(items)
			}
			if err := tx.WithContext(ctx).Create(items[start:end]).Error; err != nil {
				return fmt.Errorf("%s: %w", importErrorInsertFailed, err)
			}
		}

		now := time.Now().UTC()
		if err := tx.Model(&orm.EvalSet{}).
			Where("id = ? AND status = ?", evalSet.ID, StatusActive).
			Update("item_count", evalSet.ItemCount+insertedRows).Error; err != nil {
			return err
		}
		if err := updateShardCounters(tx, shard, insertedRows, estimatedBytes, now); err != nil {
			return err
		}

		resultJSON, err := json.Marshal(importJobResult{EvalSetID: evalSet.ID, InsertedRows: insertedRows})
		if err != nil {
			return err
		}
		if err := markImportJobSucceededInTx(tx, job.ID, resultJSON, payload.ValidRows, now); err != nil {
			return err
		}
		result = asyncjob.Result{ResultJSON: resultJSON}
		return nil
	})
	if err != nil {
		_ = os.Remove(payload.TempPath)
		return asyncjob.Result{ErrorCode: importErrorCodeForError(err)}, err
	}

	_ = os.Remove(payload.TempPath)
	return result, nil
}

func decodeImportJobPayload(raw json.RawMessage) (EvalSetImportJobPayload, error) {
	var payload EvalSetImportJobPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("decode import job payload: %w", err)
	}
	payload.Mode = strings.TrimSpace(payload.Mode)
	payload.EvalSetID = strings.TrimSpace(payload.EvalSetID)
	payload.DatasetIDs = normalizeDatasetIDs(payload.DatasetIDs)
	payload.ImportToken = strings.TrimSpace(payload.ImportToken)
	payload.TempPath = strings.TrimSpace(payload.TempPath)
	if payload.Mode != importModeCreate && payload.Mode != importModeAppend {
		return payload, errors.New("invalid import mode")
	}
	if payload.EvalSetID == "" || payload.ImportToken == "" || payload.TempPath == "" || payload.ValidRows < 0 {
		return payload, errors.New("invalid import payload")
	}
	if payload.Mode == importModeCreate && len(payload.DatasetIDs) == 0 {
		return payload, errors.New("invalid import payload")
	}
	return payload, nil
}

func readImportTempRows(payload EvalSetImportJobPayload) ([]ImportNormalizedRow, error) {
	raw, err := os.ReadFile(payload.TempPath)
	if err != nil {
		return nil, fmt.Errorf("read import temp file: %w", err)
	}
	var rows []ImportNormalizedRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode import temp rows: %w", err)
	}
	return rows, nil
}

func lockedSucceededImportResult(ctx context.Context, tx *gorm.DB, jobID string) (json.RawMessage, bool, error) {
	var row orm.AsyncJob
	if err := withUpdateLock(tx.WithContext(ctx)).
		Where("id = ?", jobID).
		First(&row).Error; err != nil {
		return nil, false, err
	}
	if row.Status == string(asyncjob.StatusSucceeded) && len(row.ResultJSON) > 0 {
		return row.ResultJSON, true, nil
	}
	return nil, false, nil
}

func prepareImportTarget(ctx context.Context, tx *gorm.DB, payload EvalSetImportJobPayload, job asyncjob.Job) (*orm.EvalSet, *orm.EvalSetShard, error) {
	switch payload.Mode {
	case importModeCreate:
		shardRepo := NewRepository(tx)
		shard, err := shardRepo.allocateShard(ctx, tx)
		if err != nil {
			return nil, nil, err
		}
		now := time.Now().UTC()
		evalSet := &orm.EvalSet{
			ID:             payload.EvalSetID,
			Name:           payload.Name,
			Description:    payload.Description,
			DatasetIDs:     datasetIDsJSON(payload.DatasetIDs),
			OwnerID:        job.CreateUserID,
			GroupID:        payload.GroupID,
			ShardID:        shard.ID,
			Status:         StatusActive,
			ItemCount:      0,
			CreateUserID:   job.CreateUserID,
			CreateUserName: job.CreateUserName,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.WithContext(ctx).Create(evalSet).Error; err != nil {
			return nil, nil, err
		}
		if err := insertACLRows(tx, evalSet.ID, acl.GranteeUser, job.CreateUserID, job.CreateUserID, now); err != nil {
			return nil, nil, err
		}
		if payload.GroupID != "" {
			if err := insertACLRows(tx, evalSet.ID, acl.GranteeGroup, payload.GroupID, job.CreateUserID, now); err != nil {
				return nil, nil, err
			}
		}
		return evalSet, shard, nil
	case importModeAppend:
		evalSet, shard, err := lockEvalSetAndShard(ctx, tx, payload.EvalSetID)
		if err != nil {
			return nil, nil, err
		}
		return evalSet, shard, nil
	default:
		return nil, nil, errors.New("invalid import mode")
	}
}

func buildImportItems(rows []ImportNormalizedRow, evalSet *orm.EvalSet, job asyncjob.Job) ([]*orm.EvalSetItem, int64, error) {
	now := time.Now().UTC()
	items := make([]*orm.EvalSetItem, 0, len(rows))
	var estimatedBytes int64
	for _, row := range rows {
		item := &orm.EvalSetItem{
			ShardID:           evalSet.ShardID,
			ID:                newEvalSetItemID(),
			EvalSetID:         evalSet.ID,
			CaseID:            strings.TrimSpace(row.CaseID),
			Question:          strings.TrimSpace(row.Question),
			GroundTruth:       strings.TrimSpace(row.GroundTruth),
			QuestionType:      strings.TrimSpace(row.QuestionType),
			GenerateReason:    strings.TrimSpace(row.GenerateReason),
			KeyPoints:         strings.TrimSpace(row.KeyPoints),
			ReferenceChunkIDs: strings.TrimSpace(row.ReferenceChunkIDs),
			ReferenceContext:  strings.TrimSpace(row.ReferenceContext),
			ReferenceDoc:      strings.TrimSpace(row.ReferenceDoc),
			ReferenceDocIDs:   strings.TrimSpace(row.ReferenceDocIDs),
			IsDeleted:         row.IsDeleted,
			Source:            SourceUpload,
			CreateUserID:      job.CreateUserID,
			CreateUserName:    job.CreateUserName,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if item.CaseID == "" {
			item.CaseID = "case_" + common.GenerateID()
		}
		if err := validateItemFields(item); err != nil {
			return nil, 0, err
		}
		item.EstimatedBytes = estimateEvalSetItemBytes(item)
		estimatedBytes += item.EstimatedBytes
		items = append(items, item)
	}
	return items, estimatedBytes, nil
}

func markImportJobSucceededInTx(tx *gorm.DB, jobID string, resultJSON json.RawMessage, progressTotal int64, now time.Time) error {
	return tx.Model(&orm.AsyncJob{}).
		Where("id = ?", jobID).
		Updates(map[string]any{
			"status":             string(asyncjob.StatusSucceeded),
			"result_json":        resultJSON,
			"error_code":         "",
			"error_message":      "",
			"error_details_json": nil,
			"progress_current":   progressTotal,
			"progress_total":     progressTotal,
			"locked_by":          "",
			"lock_until":         nil,
			"finished_at":        now,
			"updated_at":         now,
		}).Error
}

func importErrorCodeForError(err error) string {
	if err == nil {
		return asyncjob.ErrorCodeHandlerFailed
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, importErrorEvalSetNotFound):
		return importErrorEvalSetNotFound
	case strings.Contains(msg, importErrorInsertFailed):
		return importErrorInsertFailed
	case strings.Contains(msg, "temp file"), strings.Contains(msg, "no such file"):
		return importErrorTempFileMissing
	case strings.Contains(msg, "payload"), strings.Contains(msg, "mode"), strings.Contains(msg, "valid_rows"):
		return importErrorInvalidPayload
	default:
		return asyncjob.ErrorCodeHandlerFailed
	}
}
