package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"path/filepath"
	"strings"
)

type LocalUploadItem struct {
	Path        string   `json:"path"`
	DisplayName string   `json:"display_name,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}
type LocalUploadRequest struct {
	KnowledgeBaseID string            `json:"knowledge_base_id"`
	ParentID        string            `json:"parent_id,omitempty"`
	Files           []LocalUploadItem `json:"files"`
}

func uploadItemError(err error) *ResourceError {
	var resource *ResourceError
	if errors.As(err, &resource) {
		return resource
	}
	var service *DatasetServiceError
	if errors.As(err, &service) {
		return resourceErr(string(service.Code), SafeProcessingMessage(service.Message))
	}
	return resourceErr("INTERNAL", "file import failed; inspect the operation before retrying")
}

// UploadLocalBatch preserves per-file receipts so an interrupted batch never
// repeats a committed import. Each file and its folder statistics commit together;
// deferring statistics until the end would leave stale counts after cancellation.
func (s *ResourceService) UploadLocalBatch(ctx context.Context, op ResourceOperationContext, req LocalUploadRequest) (ResourceOperationResult, error) {
	const action = "knowledge.document.upload"
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceOperationResult{}, err
	}
	if len(req.Files) == 0 || len(req.Files) > 20 || len(req.Files) > s.budget.MaxResources || strings.TrimSpace(req.KnowledgeBaseID) == "" {
		return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "select a target library and one to twenty files")
	}
	if s.originals == nil {
		return ResourceOperationResult{}, resourceErr("UNAVAILABLE", "original service unavailable")
	}
	if saved, found, err := s.replay(ctx, op, action, req); found || err != nil {
		return saved, err
	}
	if _, err := s.dataset(ctx, s.db, op, req.KnowledgeBaseID, acl.PermissionDatasetUpload); err != nil {
		return ResourceOperationResult{}, err
	}
	if err := resourceParent(s.db, req.KnowledgeBaseID, req.ParentID); err != nil {
		return ResourceOperationResult{}, err
	}
	parent := op
	if parent.OperationKey == "" {
		parent.OperationKey = newDocID()
	}
	digest := resourceOperationHash(op, req)
	headerOp := parent
	headerOp.OperationKey = "upload:" + resourceHash([]string{parent.OperationKey})[:40] + ":header"
	header, found, err := s.replay(ctx, headerOp, action+".header", req)
	if err != nil {
		return header, err
	}
	if !found {
		items := make([]ResourceOperationItem, len(req.Files))
		var total int64
		for i, file := range req.Files {
			size, e := LocalOriginalSize(file.Path)
			total += size
			item := ResourceOperationItem{InputIndex: &i, Name: firstNonEmpty(file.DisplayName, filepath.Base(file.Path)), ResourceRef: ResourceRef{Kind: "document", KnowledgeBaseID: req.KnowledgeBaseID}, ParentID: req.ParentID, ResourceStatus: "not_attempted", ProcessingStatus: "not_submitted", Before: map[string]any{"source_size": size}}
			if e != nil {
				item.ResourceStatus = "failed"
				item.Error = uploadItemError(e)
			}
			items[i] = item
		}
		if total > OriginalBatchMaxBytes || total > s.budget.MaxBytes {
			return ResourceOperationResult{}, resourceErr("OPERATION_TOO_LARGE", "batch upload size limit exceeded")
		}
		header, err = s.mutate(ctx, headerOp, action+".header", req, func(tx *gorm.DB) (ResourceOperationResult, error) {
			if _, e := s.dataset(ctx, tx, op, req.KnowledgeBaseID, acl.PermissionDatasetUpload); e != nil {
				return ResourceOperationResult{}, e
			}
			if e := resourceParent(tx, req.KnowledgeBaseID, req.ParentID); e != nil {
				return ResourceOperationResult{}, e
			}
			return ResourceOperationResult{Items: items}, nil
		})
		if err != nil {
			return header, err
		}
	}
	result := ResourceOperationResult{Action: action, Items: []ResourceOperationItem{}}
	for i, file := range req.Files {
		item := header.Items[i]
		child := op
		child.OperationKey = fmt.Sprintf("%s:file:%d", headerOp.OperationKey, i)
		child.InputDigest = resourceHash([]any{digest, i, file})
		child.ExpectedSnapshot = ""
		if saved, exists, e := s.replay(ctx, child, action, file); e != nil {
			item.ResourceStatus = "unconfirmed"
			item.Error = resourceErr("UNCONFIRMED", "could not read file receipt; reconcile before retrying")
			result.Items = append(result.Items, item)
			continue
		} else if exists {
			result.Items = append(result.Items, saved.Items...)
			continue
		}
		if ctx.Err() != nil {
			item.ResourceStatus = "not_attempted"
			item.Error = resourceErr("CANCELED", "request ended before this file was attempted")
			result.Items = append(result.Items, item)
			continue
		}
		if item.Error != nil {
			result.Items = append(result.Items, item)
			continue
		}
		size, e := LocalOriginalSize(file.Path)
		if e == nil {
			expected, _ := json.Marshal(item.Before["source_size"])
			actual, _ := json.Marshal(size)
			if string(expected) != string(actual) {
				e = resourceErr("SOURCE_CHANGED", "file size changed after batch preflight")
			}
		}
		if e == nil {
			var ref OriginalRef
			ref, e = s.originals.AcquireLocalOriginal(ctx, op.Caller, file.Path, size)
			if e == nil {
				var outcome ResourceOperationResult
				outcome, e = s.Upload(ctx, child, []ImportOriginalRequest{{FileRef: ref.FileRef, DatasetID: req.KnowledgeBaseID, ParentID: req.ParentID, DisplayName: file.DisplayName, Tags: file.Tags}})
				if e == nil && len(outcome.Items) == 1 {
					item = outcome.Items[0]
					item.InputIndex = &i
					outcome.Items[0] = item
					s.updateReceiptResult(ctx, child, outcome)
				}
				if !resourceOutcomeUnconfirmed(e) {
					_ = s.originals.discardLocalAcquisition(ctx, op.Caller, ref.FileRef)
				}
			}
		}
		if e != nil {
			item.ResourceStatus = "failed"
			item.Error = uploadItemError(e)
			if resourceOutcomeUnconfirmed(e) {
				item.ResourceStatus = "unconfirmed"
			}
			failed := ResourceOperationResult{Action: action, Items: []ResourceOperationItem{item}}
			summarizeResourceResult(&failed)
			saved, saveErr := s.storeResourceOutcome(child, file, failed)
			if saveErr == nil && len(saved.Items) > 0 {
				item = saved.Items[0]
			} else {
				item.ResourceStatus = "unconfirmed"
				item.Error = resourceErr("UNCONFIRMED", "file outcome could not be saved; reconcile before retrying")
			}
		}
		result.Items = append(result.Items, item)
	}
	summarizeResourceResult(&result)
	// Canceled/unconfirmed batches remain resumable from the durable per-file receipts.
	if ctx.Err() == nil && result.Summary.Unconfirmed == 0 {
		saved, e := s.storeResourceOutcome(parent, req, result)
		if e == nil {
			return saved, nil
		}
	}
	return result, nil
}
