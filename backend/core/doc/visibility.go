package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

const documentVisibilityBatchSize = 500

type DocumentVisibilityRef struct {
	KnowledgeID string `json:"knowledge_id"`
	DocumentID  string `json:"document_id"`
}
type DocumentVisibilityService struct {
	db, lazyDB *gorm.DB
	ready      atomic.Bool
}

func NewDocumentVisibilityService(db, lazyDB *gorm.DB) *DocumentVisibilityService {
	return &DocumentVisibilityService{db: db, lazyDB: lazyDB}
}

var documentVisibility atomic.Pointer[DocumentVisibilityService]

// InitializeDocumentVisibility must finish before native ownership filtering is enabled.
// A failed historical backfill leaves the endpoint unavailable, never permissive.
func InitializeDocumentVisibility(ctx context.Context, db, lazyDB *gorm.DB) error {
	svc := NewDocumentVisibilityService(db, lazyDB)
	if err := svc.Backfill(ctx); err != nil {
		return err
	}
	documentVisibility.Store(svc)
	return nil
}

// Backfill maps only existing readonly KB associations with a unique Core dataset.
// Tombstones and existing IDs always win; missing associations are never inferred.
func (s *DocumentVisibilityService) Backfill(ctx context.Context) error {
	s.ready.Store(false)
	if s.db == nil || s.lazyDB == nil {
		return errors.New("ownership backfill databases are unavailable")
	}
	var datasets []orm.Dataset
	if err := s.db.WithContext(ctx).Order("id").Find(&datasets).Error; err != nil {
		return err
	}
	byKB := map[string]orm.Dataset{}
	for _, dataset := range datasets {
		kb := strings.TrimSpace(dataset.KbID)
		if kb == "" {
			continue
		}
		if previous, ok := byKB[kb]; ok && previous.ID != dataset.ID {
			return fmt.Errorf("ambiguous Core ownership for KB %s", kb)
		}
		byKB[kb] = dataset
	}
	for kb, dataset := range byKB {
		if dataset.DeletedAt != nil {
			continue
		}
		var checkpoint orm.DocumentOwnershipBackfill
		err := s.db.WithContext(ctx).Where("dataset_id = ?", dataset.ID).Take(&checkpoint).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var maxID int64
			if err := s.lazyDB.WithContext(ctx).Model(&readonlyorm.LazyLLMKBDocRow{}).Where("kb_id = ?", kb).Select("COALESCE(MAX(id),0)").Scan(&maxID).Error; err != nil {
				return err
			}
			checkpoint = orm.DocumentOwnershipBackfill{DatasetID: dataset.ID, KbID: kb, TenantID: resourceTenant(dataset.TenantID), MaxAssociationID: maxID, CreatedAt: time.Now().UTC()}
			if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&checkpoint).Error; err != nil {
				return err
			}
			if err := s.db.WithContext(ctx).Where("dataset_id = ?", dataset.ID).Take(&checkpoint).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if checkpoint.KbID != kb || checkpoint.TenantID != resourceTenant(dataset.TenantID) {
			return errors.New("dataset changed during ownership backfill")
		}
		if checkpoint.CompletedAt != nil {
			continue
		}
		offset := 0
		for {
			var associations []readonlyorm.LazyLLMKBDocRow
			if err := s.lazyDB.WithContext(ctx).Where("kb_id = ? AND id <= ?", kb, checkpoint.MaxAssociationID).Order("id").Limit(documentVisibilityBatchSize).Offset(offset).Find(&associations).Error; err != nil {
				return err
			}
			if len(associations) == 0 {
				break
			}
			offset += len(associations)
			ids := make([]string, 0, len(associations))
			for _, association := range associations {
				ids = append(ids, association.DocID)
			}
			var originals []readonlyorm.LazyLLMDocRow
			if err := s.lazyDB.WithContext(ctx).Where("doc_id IN ?", ids).Find(&originals).Error; err != nil {
				return err
			}
			originalsByID := map[string]readonlyorm.LazyLLMDocRow{}
			for _, original := range originals {
				originalsByID[original.DocID] = original
			}
			// Verify every association before enabling the fence. An index-only identity
			// without an owning original row is corruption, not a recoverable mapping.
			for _, id := range ids {
				if _, ok := originalsByID[id]; !ok {
					return fmt.Errorf("historical document %s has no original metadata", id)
				}
			}
			err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var current orm.Dataset
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleted_at IS NULL", dataset.ID).Take(&current).Error; err != nil {
					return err
				}
				if current.KbID != kb || resourceTenant(current.TenantID) != resourceTenant(dataset.TenantID) {
					return errors.New("dataset changed during ownership backfill")
				}
				var coreDocuments []orm.Document
				if err := tx.Where("dataset_id = ?", dataset.ID).Find(&coreDocuments).Error; err != nil {
					return err
				}
				for _, id := range ids {
					var rejected int64
					if err := tx.Model(&orm.RejectedDocumentPublication{}).Where("dataset_id = ? AND external_doc_id = ?", dataset.ID, id).Count(&rejected).Error; err != nil {
						return err
					}
					if rejected != 0 {
						continue
					}
					var existing []orm.Document
					if err := tx.Where("id = ? OR lazyllm_doc_id = ?", id, id).Find(&existing).Error; err != nil {
						return err
					}
					alreadyMapped := false
					for _, row := range existing {
						if row.DatasetID == dataset.ID {
							alreadyMapped = true
							continue
						}
						var owner orm.Dataset
						if err := tx.Where("id = ?", row.DatasetID).Take(&owner).Error; err != nil {
							return errors.New("historical document has unknown owner")
						}
						if resourceTenant(owner.TenantID) != resourceTenant(dataset.TenantID) {
							return errors.New("historical document association crosses tenant boundary")
						}
					}
					if alreadyMapped {
						continue
					}
					original := originalsByID[id]
					// A row tied to a Core-managed original must use the guarded
					// publisher. This also covers pre-upgrade in-flight results
					// whose external identity was never written onto a tombstone.
					coreManaged := false
					for _, row := range coreDocuments {
						var metadata documentExt
						_ = json.Unmarshal(row.Ext, &metadata)
						if (original.Path != "" && (metadata.StoredPath == original.Path || parsePathForAdd(metadata) == original.Path)) ||
							(row.DeletedAt != nil && row.LazyllmDocID == "" && metadata.StoredPath == "") {
							coreManaged = true
							break
						}
					}
					if coreManaged {
						continue
					}
					size := int64(0)
					if original.SizeBytes != nil {
						size = int64(*original.SizeBytes)
					}
					meta := newDocumentExt(original.Path, "", original.Filename, size, "", "", nil)
					// A generated Core ID avoids stealing an existing globally unique resource ID.
					row := orm.Document{ID: newDocID(), LazyllmDocID: id, DatasetID: dataset.ID, DisplayName: original.Filename, DocumentType: fileDocumentTypeFromName(original.Filename), ResourceVersion: 1, Ext: mustJSON(meta), BaseModel: orm.BaseModel{CreateUserID: dataset.CreateUserID, CreateUserName: dataset.CreateUserName, CreatedAt: original.CreatedAt, UpdatedAt: time.Now().UTC()}}
					if err := tx.Create(&row).Error; err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		if err := s.db.WithContext(ctx).Model(&orm.DocumentOwnershipBackfill{}).Where("dataset_id = ? AND kb_id = ? AND tenant_id = ?", dataset.ID, kb, resourceTenant(dataset.TenantID)).Update("completed_at", now).Error; err != nil {
			return err
		}
	}
	s.ready.Store(true)
	return nil
}
func (s *DocumentVisibilityService) Filter(ctx context.Context, caller DatasetCatalogCaller, refs []DocumentVisibilityRef) ([]DocumentVisibilityRef, error) {
	if !s.ready.Load() {
		return nil, originalError(DatasetServiceUnavailable, "document ownership backfill is not ready")
	}
	if len(refs) > documentVisibilityBatchSize {
		return nil, originalError(DatasetServiceInvalidArgument, "too many document references")
	}
	allowed := make([]DocumentVisibilityRef, 0, len(refs))
	datasets := map[string]*orm.Dataset{}
	for _, ref := range refs {
		if ref.KnowledgeID == "" || ref.DocumentID == "" {
			continue
		}
		dataset, known := datasets[ref.KnowledgeID]
		if !known {
			var rows []orm.Dataset
			if err := s.db.WithContext(ctx).Where("(id = ? OR kb_id = ?) AND deleted_at IS NULL", ref.KnowledgeID, ref.KnowledgeID).Limit(2).Find(&rows).Error; err != nil {
				return nil, err
			}
			if len(rows) != 1 {
				datasets[ref.KnowledgeID] = nil
				continue
			}
			ds, err := ResolveResourceDataset(ctx, s.db, caller, rows[0].ID, acl.PermissionDatasetRead)
			if err != nil {
				var resource *ResourceError
				if errors.As(err, &resource) && (resource.Code == "FORBIDDEN" || resource.Code == "NOT_FOUND") {
					datasets[ref.KnowledgeID] = nil
					continue
				}
				return nil, err
			}
			dataset = &ds
			datasets[ref.KnowledgeID] = dataset
		}
		if dataset == nil {
			continue
		}
		var count int64
		if err := s.db.WithContext(ctx).Model(&orm.Document{}).Where("dataset_id = ? AND (id = ? OR lazyllm_doc_id = ?) AND lazyllm_doc_id <> '' AND deleted_at IS NULL", dataset.ID, ref.DocumentID, ref.DocumentID).Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 1 {
			allowed = append(allowed, ref)
		}
	}
	return allowed, nil
}

// FilterDocumentVisibilityHTTP is a read-only batch authorization boundary for
// native semantic/keyword retrieval, node expansion, image and citation sources.
func FilterDocumentVisibilityHTTP(w http.ResponseWriter, r *http.Request) {
	svc := documentVisibility.Load()
	if svc == nil {
		common.ReplyErr(w, "document ownership backfill is not ready", http.StatusServiceUnavailable)
		return
	}
	var input struct {
		Items []DocumentVisibilityRef `json:"items"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&input); err != nil {
		common.ReplyErr(w, "invalid document references", http.StatusBadRequest)
		return
	}
	caller, err := resolveDatasetCatalogCallerFromRequest(r)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusForbidden)
		return
	}
	refs, err := svc.Filter(r.Context(), caller, input.Items)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	common.ReplyJSON(w, map[string]any{"items": refs})
}
