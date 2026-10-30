package paperlibrary

import (
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// EffectiveMetadata uses the same primary-source and local-override rules as Load.
func EffectiveMetadata(db *gorm.DB, entry orm.PaperLibraryEntry, work orm.AcademicWork) (Metadata, error) {
	sources, err := automaticMetadata(db, entry.DatasetID, []orm.PaperLibraryEntry{entry})
	if err != nil {
		return Metadata{}, err
	}
	return mergeMetadata(entry, work, sources[entry.ID])
}

// Automatic metadata belongs to active members or server-owned import snapshots.
// An unverified primary never borrows another PDF's metadata.
func automaticMetadata(db *gorm.DB, dataset string, entries []orm.PaperLibraryEntry) (map[string]json.RawMessage, error) {
	result := map[string]json.RawMessage{}
	ids := make([]string, 0, len(entries))
	workIDs := map[string]string{}
	for _, entry := range entries {
		ids = append(ids, entry.ID)
		workIDs[entry.ID] = entry.AcademicWorkID
	}
	if len(ids) == 0 {
		return result, nil
	}
	type documentSource struct {
		PaperID, DocumentID, LinkedWorkID, StateWorkID, Kind, Status, SourceProvider, SourceVersion string
		MetadataJSON                                                                                orm.RawJSON `gorm:"type:json"`
	}
	var documents []documentSource
	err := db.Table("paper_library_attachments a").Select("a.paper_id,a.document_id,l.academic_work_id AS linked_work_id,s.paper_id AS state_work_id,s.kind,s.status,s.metadata_json,l.source_provider,l.source_version").
		Joins("JOIN documents d ON d.id=a.document_id AND d.dataset_id=a.dataset_id AND d.deleted_at IS NULL").
		Joins("LEFT JOIN academic_work_documents l ON l.document_id=a.document_id AND l.dataset_id=a.dataset_id").
		Joins("LEFT JOIN academic_document_states s ON s.document_id=a.document_id AND s.dataset_id=a.dataset_id").
		Where("a.dataset_id=? AND a.paper_id IN ? AND a.detached_at IS NULL AND a.kind='pdf'", dataset, ids).Order("a.is_primary DESC,a.created_at,a.id").Scan(&documents).Error
	if err != nil {
		return nil, err
	}
	primary := map[string]documentSource{}
	for _, source := range documents {
		if _, exists := primary[source.PaperID]; exists {
			continue
		}
		primary[source.PaperID] = source
		if source.LinkedWorkID == workIDs[source.PaperID] && source.StateWorkID == workIDs[source.PaperID] && source.Kind == "paper" && source.Status == "succeeded" && len(source.MetadataJSON) > 0 && string(source.MetadataJSON) != "{}" && string(source.MetadataJSON) != "null" {
			var metadata struct {
				Provenance struct {
					Version string `json:"pdf_version"`
				} `json:"provenance"`
			}
			if err := json.Unmarshal(source.MetadataJSON, &metadata); err != nil {
				return nil, err
			}
			if metadata.Provenance.Version != "" && metadata.Provenance.Version != source.SourceVersion {
				continue
			}
			result[source.PaperID] = json.RawMessage(source.MetadataJSON)
		}
	}
	missing := []string{}
	for _, id := range ids {
		if len(result[id]) == 0 {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return result, nil
	}
	var items []orm.PaperImportItem
	if err := db.Table("paper_import_items i").Select("i.*").Joins("JOIN paper_import_batches b ON b.id=i.batch_id").Where("b.target_dataset_id=? AND i.library_paper_id IN ? AND i.status IN ?", dataset, missing, []string{"succeeded", "skipped"}).Order("i.created_at DESC,i.id DESC").Scan(&items).Error; err != nil {
		return nil, err
	}
	for _, item := range items {
		if len(result[item.LibraryPaperID]) > 0 || item.AcademicWorkID != workIDs[item.LibraryPaperID] {
			continue
		}
		source, hasPDF := primary[item.LibraryPaperID]
		if hasPDF && (source.DocumentID != item.DocumentID || source.LinkedWorkID != item.AcademicWorkID || source.StateWorkID != "" && source.StateWorkID != item.AcademicWorkID) {
			continue
		}
		if !hasPDF && item.DocumentID != "" {
			continue
		}
		var selected struct {
			URL            string `json:"url"`
			SourceProvider string `json:"source_provider"`
			Version        string `json:"version"`
			VerifiedPaper  *struct {
				Metadata
				PaperID        string `json:"paper_id"`
				SourceProvider string `json:"source_provider"`
				SourceVersion  string `json:"source_version"`
				PDFURL         string `json:"pdf_url"`
			} `json:"verified_paper"`
		}
		if err := json.Unmarshal(item.SelectedCandidateJSON, &selected); err != nil {
			return nil, err
		}
		ref := selected.VerifiedPaper
		if ref == nil || ref.PaperID != item.AcademicWorkID || ref.Title == "" || ref.SourceProvider != selected.SourceProvider || ref.SourceVersion != selected.Version || ref.PDFURL != selected.URL {
			continue
		}
		if hasPDF && (source.SourceProvider != selected.SourceProvider || source.SourceVersion != selected.Version) {
			continue
		}
		var fields struct {
			VerifiedPaper json.RawMessage `json:"verified_paper"`
		}
		if err := json.Unmarshal(item.SelectedCandidateJSON, &fields); err != nil {
			return nil, err
		}
		result[item.LibraryPaperID] = fields.VerifiedPaper
	}
	return result, nil
}
