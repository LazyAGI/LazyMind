package academic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// A one-click citation import must resolve to a publisher or scholarly-index
// record before it becomes a library entry. The general reference resolver is
// intentionally more permissive because it also powers bibliography browsing.
func verifiedCitationInput(ctx context.Context, ref *orm.AcademicReference) (WorkInput, error) {
	if conflictingReferenceIDs(ref.RawText) {
		return WorkInput{}, errors.New("这条参考文献包含冲突的论文编号，请先核对原文")
	}
	rawDOI, rawArxiv, rawVersion := extractStrongIDs(ref.RawText)
	doi := NormalizeDOI(ref.DOINormalized)
	if doi != "" && rawDOI != "" && doi != rawDOI {
		return WorkInput{}, errors.New("参考文献的 DOI 与原文不一致，请先核对原文")
	}
	if doi == "" {
		doi = rawDOI
	}
	// An explicit arXiv ID in the citation outranks a DOI carried over from
	// an earlier permissive resolution of the same reference row.
	if rawDOI == "" && rawArxiv != "" {
		doi = ""
	}
	if doi != "" {
		input, err := lookupPaperLinkMetadata(ctx, doi)
		if err != nil {
			return WorkInput{}, err
		}
		if rawDOI == "" && !citationIdentityMatches(ref, input) {
			return WorkInput{}, errors.New("已保存的 DOI 与原文题名或年份不符，请核对参考文献")
		}
		return input, nil
	}
	arxivID, _ := NormalizeArxivID(ref.ArxivIDBase)
	if arxivID != "" && rawArxiv != "" && arxivID != rawArxiv {
		return WorkInput{}, errors.New("参考文献的 arXiv 编号与原文不一致，请先核对原文")
	}
	if arxivID == "" {
		arxivID = rawArxiv
	}
	if arxivID != "" {
		if rawArxiv == arxivID {
			arxivID += rawVersion
		}
		input, err := lookupPaperLinkMetadata(ctx, arxivID)
		if err != nil {
			return WorkInput{}, err
		}
		if rawArxiv == "" && !citationIdentityMatches(ref, input) {
			return WorkInput{}, errors.New("已保存的 arXiv 编号与原文题名或年份不符，请核对参考文献")
		}
		return input, nil
	}

	openAlex, openAlexOK, openAlexErr := resolveWithOpenAlex(ctx, ref.RawText)
	if openAlexOK && citationIdentityMatches(ref, openAlex) {
		return openAlex, nil
	}
	arxiv, arxivOK, arxivErr := resolveWithArxiv(ctx, ref.RawText)
	if arxivOK && citationIdentityMatches(ref, arxiv) {
		return arxiv, nil
	}
	if err := errors.Join(openAlexErr, arxivErr); err != nil {
		return WorkInput{}, fmt.Errorf("查询参考文献来源失败: %w", err)
	}
	return WorkInput{}, errors.New("未能从 OpenAlex 或 arXiv 可靠匹配这条参考文献，请先核对 DOI 或 arXiv 编号")
}

func citationIdentityMatches(ref *orm.AcademicReference, found WorkInput) bool {
	title := citationTitle(ref.RawText)
	if title == "" {
		title = strings.TrimSpace(ref.Title)
	}
	if title == "" || NormalizeTitle(title) != NormalizeTitle(found.Title) {
		return false
	}
	year := extractYear(ref.RawText)
	if year == 0 {
		year = ref.PublicationYear
	}
	return year == 0 || found.Year == 0 || found.Year >= year-1 && found.Year <= year+1
}

func saveVerifiedCitationResolution(ctx context.Context, tx *gorm.DB, ref *orm.AcademicReference, input WorkInput) (orm.AcademicWork, error) {
	work, method, err := findOrCreateWork(ctx, tx, input)
	if err != nil {
		return orm.AcademicWork{}, err
	}
	// An older loose citation resolution could create an unattached work whose
	// entire title was the raw bibliography line. Only that recognizable
	// placeholder may be replaced; never overwrite an imported or edited work.
	if work.CanonicalTitle == strings.TrimSpace(ref.RawText) && work.CanonicalTitle != input.Title {
		var documents, entries int64
		if err := tx.WithContext(ctx).Model(&orm.AcademicWorkDocument{}).Where("academic_work_id=?", work.ID).Count(&documents).Error; err != nil {
			return orm.AcademicWork{}, err
		}
		if err := tx.WithContext(ctx).Model(&orm.PaperLibraryEntry{}).Where("academic_work_id=? AND removed_at IS NULL", work.ID).Count(&entries).Error; err != nil {
			return orm.AcademicWork{}, err
		}
		if documents == 0 && entries == 0 {
			work.CanonicalTitle, work.NormalizedTitle = input.Title, NormalizeTitle(input.Title)
			if err := tx.WithContext(ctx).Model(&work).Updates(map[string]any{"canonical_title": work.CanonicalTitle, "normalized_title": work.NormalizedTitle, "updated_at": time.Now().UTC()}).Error; err != nil {
				return orm.AcademicWork{}, err
			}
		}
	}
	status, confidence := "high_confidence", 0.9
	if method == "doi" || method == "arxiv" {
		status, confidence = "exact", 1
	}
	if err := tx.WithContext(ctx).Model(ref).Updates(map[string]any{
		"resolved_work_id":      work.ID,
		"doi_normalized":        work.DOINormalized,
		"arxiv_id_base":         work.ArxivIDBase,
		"title":                 input.Title,
		"authors_json":          marshal(input.Authors),
		"publication_year":      input.Year,
		"resolution_status":     status,
		"resolution_method":     method,
		"resolution_confidence": confidence,
		"updated_at":            time.Now().UTC(),
	}).Error; err != nil {
		return orm.AcademicWork{}, err
	}
	ref.ResolvedWorkID, ref.ResolutionStatus, ref.DOINormalized, ref.ArxivIDBase = work.ID, status, work.DOINormalized, work.ArxivIDBase
	ref.Title, ref.AuthorsJSON, ref.PublicationYear = input.Title, marshal(input.Authors), input.Year
	return work, nil
}
