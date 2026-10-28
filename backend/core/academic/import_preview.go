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
	"lazymind/core/doc"
	"lazymind/core/store"
)

const importPreviewTTL = 30 * time.Minute

type importPreviewReceipt struct {
	UserID           string              `json:"user_id"`
	DatasetID        string              `json:"dataset_id"`
	ExpiresAt        time.Time           `json:"expires_at"`
	Items            []PreviewItem       `json:"items"`
	Verified         map[string]PaperRef `json:"verified"`
	Hashes           map[string]string   `json:"hashes,omitempty"`
	ExpansionID      string              `json:"expansion_id,omitempty"`
	ExpansionVersion int64               `json:"expansion_version,omitempty"`
}

// This field exists only in persisted job input. The public candidate type
// deliberately cannot accept a client-supplied verification snapshot.
type savedImportCandidate struct {
	FulltextCandidate
	VerifiedPaper *PaperRef `json:"verified_paper,omitempty"`
	ExpectedHash  string    `json:"expected_hash,omitempty"`
}

func saveImportPreview(ctx context.Context, user, dataset string, items []PreviewItem, verified map[string]PaperRef, expansion *orm.AcademicExpansion, hashes ...map[string]string) (string, error) {
	if store.State() == nil {
		return "", errors.New("import preview state is unavailable")
	}
	token := uuid.NewString()
	receipt := importPreviewReceipt{UserID: user, DatasetID: dataset, ExpiresAt: time.Now().UTC().Add(importPreviewTTL), Items: items, Verified: verified}
	if len(hashes) > 0 {
		receipt.Hashes = hashes[0]
	}
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
			ref.SourceProvider, ref.SourceVersion, ref.PDFURL = input.Candidate.SourceProvider, input.Candidate.Version, input.Candidate.URL
			return &ref, nil
		}
		return nil, nil
	}
	return nil, errors.New("论文或版本与导入预览不一致，请重新确认")
}

// Verification belongs to the selected source, not to a particular index.
func verifiedImportMetadata(selected savedImportCandidate, workID string) (WorkInput, error) {
	ref := selected.VerifiedPaper
	if ref == nil || ref.PaperID != workID || strings.TrimSpace(ref.Title) == "" || ref.SourceProvider != selected.SourceProvider || ref.SourceVersion != selected.Version || ref.PDFURL != selected.URL {
		return WorkInput{}, errors.New("invalid verified paper snapshot")
	}
	if selected.SourceProvider == "arxiv" {
		metadata, ok := arxivMetadataFromRef(*ref)
		if !ok {
			return WorkInput{}, errors.New("invalid verified arXiv snapshot")
		}
		urls, err := canonicalPaperURLs(selected.FulltextCandidate)
		if err != nil || urls[0] != metadata.PDFURL {
			return WorkInput{}, errors.New("verified paper identity conflict")
		}
		return arxivWorkInput(metadata), nil
	}
	in := WorkInput{Title: ref.Title, Authors: ref.Authors, Year: ref.Year, Venue: ref.Venue, DOI: ref.DOI, ArxivID: ref.ArxivID, CitationCount: ref.CitationCount, Provider: ref.SourceProvider, Provenance: map[string]any{"pdf_url": selected.URL, "pdf_version": selected.Version}}
	if ref.Abstract != nil {
		in.Abstract = *ref.Abstract
	}
	return in, nil
}

func expansionImportCandidates(ctx context.Context, user string, source paperSource) ([]FulltextCandidate, string, error) {
	candidates := append([]FulltextCandidate{}, source.Candidates...)
	if len(candidates) == 0 && source.Paper.PDFURL != "" {
		candidates = append(candidates, FulltextCandidate{URL: source.Paper.PDFURL, SourceProvider: source.Paper.SourceProvider, Version: source.Paper.SourceVersion, SourceType: "open_access_pdf", ExpectedMIME: "application/pdf"})
	}
	content, err := findSourceContent(store.DB().WithContext(ctx), user, source)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return candidates, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if !usablePaperContent(content) {
		return candidates, "", nil
	}
	var quality contentQuality
	if err = json.Unmarshal(content.QualityJSON, &quality); err != nil {
		return nil, "", err
	}
	if quality.Acquisition.Candidate != nil {
		preferred := *quality.Acquisition.Candidate
		result := []FulltextCandidate{preferred}
		for _, candidate := range candidates {
			if candidate != preferred {
				result = append(result, candidate)
			}
		}
		return result, content.SourceHash, nil
	}
	return candidates, "", nil
}

func fulltextLookupDiagnostic(err error) *doc.ProcessingDiagnostic {
	failure := importFailure(err, "resolve", false)
	return &doc.ProcessingDiagnostic{Stage: "lookup", Code: failure.Code, Message: "题录已查到，但全文查询暂不可用", Recovery: "可以重新查询全文，或仅保存题录后补充 PDF。"}
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
