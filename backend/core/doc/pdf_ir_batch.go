package doc

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

// ObservePDFStructures batches database reads for an already authorized library
// snapshot. Callers pass only document IDs returned by their ACL-filtered view.
func ObservePDFStructures(ctx context.Context, db *gorm.DB, dataset string, ids []string) (map[string]pdfir.StructureStatus, error) {
	out := map[string]pdfir.StructureStatus{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []orm.Document
	if err := db.WithContext(ctx).Where("dataset_id=? AND id IN ?", dataset, ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	resources := make([]string, 0, len(rows))
	for _, row := range rows {
		resources = append(resources, pdfIRResource(dataset, row.ID))
	}
	var jobs []orm.AsyncJob
	if err := db.WithContext(ctx).Where("job_type=? AND resource_type=? AND resource_id IN ?", pdfIRJobType, "document_pdf_ir", resources).Order("created_at DESC, id DESC").Find(&jobs).Error; err != nil {
		return nil, err
	}
	byResource := map[string][]orm.AsyncJob{}
	for _, job := range jobs {
		byResource[job.ResourceID] = append(byResource[job.ResourceID], job)
	}
	for _, row := range rows {
		var ext documentExt
		if json.Unmarshal(row.Ext, &ext) != nil {
			continue
		}
		hash, err := pdfIRHash(ext)
		if err != nil {
			continue
		}
		state := pdfIRStatusFromJobs(row, hash, byResource[pdfIRResource(dataset, row.ID)])
		item := pdfir.StructureStatus{Status: state.Status, SourceSHA256: hash, ErrorMessage: state.ErrorMessage, ErrorCode: state.ErrorCode, Available: state.Document != nil}
		if state.Document != nil {
			item.Model = state.Document.Parser.Model
		}
		out[row.ID] = item
	}
	return out, nil
}
