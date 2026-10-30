package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

// Background enrichment must never restore a paper the user removed or merged.
// Call again inside the attachment publication transaction to close that race.
func checkEnrichmentMembership(tx *gorm.DB, b orm.PaperImportBatch, item orm.PaperImportItem) error {
	if b.EntryType != "bibliography" {
		return nil
	}
	var entry orm.PaperLibraryEntry
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND id=? AND academic_work_id=? AND removed_at IS NULL", b.TargetDatasetID, item.LibraryPaperID, item.AcademicWorkID).Take(&entry).Error; err != nil {
		return fmt.Errorf("题录已移除或合并，停止后台补全: %w", err)
	}
	return nil
}

func fillBibliographyMetadata(m paperlibrary.Metadata, in WorkInput) paperlibrary.Metadata {
	if m.Title == "" {
		m.Title = in.Title
	}
	if len(m.Authors) == 0 {
		m.Authors = in.Authors
	}
	if m.Venue == "" || m.Venue == "未标注" {
		m.Venue = in.Venue
	}
	if m.Year == 0 {
		m.Year = in.Year
	}
	if m.Abstract == "" {
		m.Abstract = in.Abstract
	}
	if m.CitationCount == nil {
		m.CitationCount = in.CitationCount
	}
	return m
}

// Metadata and PDF lookups are separate: a Crossref outage cannot prevent a
// DOI with already imported metadata from finding an available PDF.
func enrichBibliographyItem(ctx context.Context, job asyncjob.Job, b orm.PaperImportBatch, item *orm.PaperImportItem, caller doc.DatasetCatalogCaller) error {
	var saved FulltextCandidate
	if err := json.Unmarshal(item.SelectedCandidateJSON, &saved); err != nil {
		return err
	}
	if saved.URL != "" {
		return nil
	} // Durable candidate receipt survives retries.
	var entry orm.PaperLibraryEntry
	var work orm.AcademicWork
	db := store.DB().WithContext(ctx)
	if err := db.Where("dataset_id=? AND id=? AND removed_at IS NULL", b.TargetDatasetID, item.LibraryPaperID).Take(&entry).Error; err != nil {
		return err
	}
	if err := db.Where("id=?", item.AcademicWorkID).Take(&work).Error; err != nil {
		return err
	}
	m, err := paperlibrary.EffectiveMetadata(entry, work)
	if err != nil {
		return err
	}
	identifier := m.Identifiers.DOI
	if m.Identifiers.Arxiv != "" {
		identifier = "arXiv:" + m.Identifiers.Arxiv + entry.ArxivVersion
	}
	if identifier == "" {
		doi, err := findBibliographyDOI(ctx, m)
		if err != nil {
			return err
		}
		// Resolve a discovered identity only while the imported entry is unchanged.
		err = db.Transaction(func(tx *gorm.DB) error {
			if _, err := lockSourceTarget(ctx, tx, caller, b.TargetDatasetID); err != nil {
				return err
			}
			if err := sourceJobFence(tx, job, b.ID); err != nil {
				return err
			}
			if err := checkEnrichmentMembership(tx, b, *item); err != nil {
				return err
			}
			var current orm.PaperLibraryEntry
			if err := tx.Where("dataset_id=? AND id=? AND revision=?", b.TargetDatasetID, entry.ID, entry.Revision).Take(&current).Error; err != nil {
				return errors.New("题录已变更，请重试补全")
			}
			matched, _, err := findOrCreateWork(ctx, tx, WorkInput{Title: m.Title, Authors: m.Authors, Year: m.Year, Venue: m.Venue, DOI: doi, Provider: "crossref"})
			if err != nil {
				return err
			}
			var duplicates int64
			if err := tx.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND academic_work_id=? AND id<>?", b.TargetDatasetID, matched.ID, entry.ID).Count(&duplicates).Error; err != nil {
				return err
			}
			if duplicates > 0 {
				return errors.New("题录已保存；匹配到库中已有文献，请核对重复项后合并")
			}
			m.Identifiers.DOI = doi
			if err := tx.Model(&current).Updates(map[string]any{"academic_work_id": matched.ID, "metadata_json": confirmedMetadata(m), "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.PaperImportItem{}).Where("id=?", item.ID).Update("academic_work_id", matched.ID).Error; err != nil {
				return err
			}
			item.AcademicWorkID = matched.ID
			return nil
		})
		if err != nil {
			return err
		}
		entry.Revision++
		identifier = doi
	}
	if err := sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"stage": "metadata"}); err != nil {
		return err
	}
	metadataCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	resolved, metadataErr := lookupPaperLinkMetadata(metadataCtx, identifier)
	cancel()
	if metadataErr == nil {
		if paperlibrary.IdentityConflict(m.Identifiers, paperlibrary.Identifiers{DOI: resolved.DOI, Arxiv: resolved.ArxivID}) {
			return errors.New("在线元信息与题录标识不一致，未修改题录")
		}
		updated := fillBibliographyMetadata(m, resolved)
		if err := validateLibraryMetadata(&updated); err != nil {
			return err
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if _, err := lockSourceTarget(ctx, tx, caller, b.TargetDatasetID); err != nil {
				return err
			}
			if err := sourceJobFence(tx, job, b.ID); err != nil {
				return err
			}
			if err := checkEnrichmentMembership(tx, b, *item); err != nil {
				return err
			}
			// Do not overwrite edits made while the external request was running.
			if err := tx.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND id=? AND revision=? AND removed_at IS NULL", b.TargetDatasetID, entry.ID, entry.Revision).Updates(map[string]any{"metadata_json": confirmedMetadata(updated), "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			return tx.Model(&orm.PaperImportItem{}).Where("id=?", item.ID).Update("presence_snapshot_json", marshal(map[string]any{"metadata_provider": resolved.Provider, "retrieved_metadata": resolved, "original_metadata": m})).Error
		}); err != nil {
			return err
		}
	}
	if err := sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"stage": "pdf_lookup"}); err != nil {
		return err
	}
	var candidate FulltextCandidate
	var warnings []string
	if m.Identifiers.Arxiv != "" {
		candidate = FulltextCandidate{URL: "https://arxiv.org/pdf/" + m.Identifiers.Arxiv + entry.ArxivVersion, SourceProvider: "arxiv", SourceType: "official_pdf", ExpectedMIME: "application/pdf"}
	} else {
		found := lookupDOIPDF(ctx, m.Identifiers.DOI)
		warnings = found.Warnings
		if found.Candidate != nil {
			candidate = *found.Candidate
		}
	}
	if metadataErr != nil {
		warnings = append(warnings, "元信息补全: "+metadataErr.Error())
	}
	if candidate.URL == "" {
		return fmt.Errorf("题录已保存，但未找到可用 PDF，可手动上传或重试。%s", strings.Join(warnings, "; "))
	}
	item.SelectedCandidateJSON = marshal(candidate)
	item.PresenceSnapshotJSON = marshal(map[string]any{"metadata_provider": resolved.Provider, "retrieved_metadata": resolved, "original_metadata": m, "lookup_warnings": warnings})
	return sourceItemUpdate(ctx, job, b, item.ID, map[string]any{"selected_candidate_json": item.SelectedCandidateJSON, "stage": "acquire", "presence_snapshot_json": item.PresenceSnapshotJSON})
}

func bibliographyDiagnostics(b orm.PaperImportBatch, item orm.PaperImportItem) json.RawMessage {
	if b.EntryType != "bibliography" {
		return json.RawMessage("{}")
	}
	var snapshot struct {
		Warnings []string `json:"lookup_warnings"`
	}
	_ = json.Unmarshal(item.PresenceSnapshotJSON, &snapshot)
	return marshal(map[string]string{"message": strings.Join(snapshot.Warnings, "; ")})
}
