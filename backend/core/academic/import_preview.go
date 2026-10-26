package academic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const importPreviewTTL = 30 * time.Minute

type importPreviewReceipt struct {
	UserID           string              `json:"user_id"`
	DatasetID        string              `json:"dataset_id"`
	ExpiresAt        time.Time           `json:"expires_at"`
	Items            []PreviewItem       `json:"items"`
	Verified         map[string]PaperRef `json:"verified"`
	ExpansionID      string              `json:"expansion_id,omitempty"`
	ExpansionVersion int64               `json:"expansion_version,omitempty"`
}

// This field exists only in persisted job input. The public candidate type
// deliberately cannot accept a client-supplied verification snapshot.
type savedImportCandidate struct {
	FulltextCandidate
	VerifiedPaper *PaperRef `json:"verified_paper,omitempty"`
}

func saveImportPreview(ctx context.Context, user, dataset string, items []PreviewItem, verified map[string]PaperRef, expansion *orm.AcademicExpansion) (string, error) {
	if store.State() == nil {
		return "", errors.New("import preview state is unavailable")
	}
	token := uuid.NewString()
	receipt := importPreviewReceipt{UserID: user, DatasetID: dataset, ExpiresAt: time.Now().UTC().Add(importPreviewTTL), Items: items, Verified: verified}
	if expansion != nil {
		receipt.ExpansionID, receipt.ExpansionVersion = expansion.ID, expansion.Version
	}
	return token, store.State().Set(ctx, "academic:import-preview:"+token, marshal(receipt), importPreviewTTL)
}

func loadImportPreview(ctx context.Context, token, user, dataset string) (importPreviewReceipt, error) {
	var receipt importPreviewReceipt
	if store.State() == nil {
		return receipt, errors.New("import preview state is unavailable")
	}
	data, err := store.State().Get(ctx, "academic:import-preview:"+token)
	if err != nil {
		return receipt, errors.New("导入预览已过期，请刷新预览后重试")
	}
	if json.Unmarshal(data, &receipt) != nil || receipt.UserID != user || receipt.DatasetID != dataset || !time.Now().Before(receipt.ExpiresAt) {
		return receipt, errors.New("导入预览已过期或不属于当前用户和论文集，请刷新预览")
	}
	return receipt, nil
}

func (p importPreviewReceipt) selected(input selectedImportItem) (*PaperRef, error) {
	for _, item := range p.Items {
		if item.WorkID != input.WorkID {
			continue
		}
		matched := false
		for _, candidate := range item.Candidates {
			if candidate == input.Candidate {
				matched = true
				break
			}
		}
		if !matched {
			break
		}
		for _, id := range input.ReferenceIDs {
			found := false
			for _, allowed := range item.ReferenceIDs {
				if id == allowed {
					found = true
					break
				}
			}
			if !found {
				return nil, errors.New("参考文献与导入预览不一致，请重新确认")
			}
		}
		if ref, ok := p.Verified[item.WorkID]; ok {
			return &ref, nil
		}
		return nil, nil
	}
	return nil, errors.New("论文或版本与导入预览不一致，请重新确认")
}

func arxivMetadataFromRef(ref PaperRef) (arxivsource.Metadata, bool) {
	id, err := arxivsource.Parse(ref.ArxivID + ref.SourceVersion)
	if err != nil || ref.SourceProvider != "arxiv" || ref.SourceVersion == "" || id.Version != ref.SourceVersion || strings.TrimSpace(ref.Title) == "" {
		return arxivsource.Metadata{}, false
	}
	abs := ""
	if ref.Abstract != nil {
		abs = *ref.Abstract
	}
	return arxivsource.Metadata{Title: ref.Title, Authors: ref.Authors, Year: ref.Year, Abstract: abs, DOI: ref.DOI, Venue: ref.Venue, BaseID: id.BaseID, Version: id.Version, AbsURL: id.AbsURL(), PDFURL: id.PDFURL()}, true
}

// Recover pre-receipt imports only from this user's saved external discoveries
// in the target collection, never from mutable client metadata or other libraries.
func verifiedImportPaper(ctx context.Context, db *gorm.DB, user, dataset, work string, candidate FulltextCandidate) (*PaperRef, error) {
	if candidate.SourceProvider != "arxiv" || candidate.Version == "" {
		return nil, nil
	}
	var records []orm.AcademicExpansion
	if err := db.WithContext(ctx).Select("discovery_json").Where("user_id=? AND dataset_id=? AND discovery_mode='external' AND deleted_at IS NULL", user, dataset).Find(&records).Error; err != nil {
		return nil, err
	}
	for _, record := range records {
		var searches []paperDiscovery
		if err := json.Unmarshal(record.DiscoveryJSON, &searches); err != nil {
			return nil, err
		}
		for _, search := range searches {
			for _, ref := range search.Papers {
				if ref.PaperID == work && ref.SourceProvider == candidate.SourceProvider && ref.SourceVersion == candidate.Version && ref.PDFURL == candidate.URL {
					if _, ok := arxivMetadataFromRef(ref); ok {
						return &ref, nil
					}
				}
			}
		}
	}
	return nil, nil
}
