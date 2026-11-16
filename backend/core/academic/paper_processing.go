package academic

import (
	"net/http"

	"gorm.io/gorm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

// Library and import views observe the same latest task and processing revision
// as document pages. Default pending stage rows are not current task outcomes.
func processingMetadata(r *http.Request, db *gorm.DB, dataset string, ids []string) (map[string]doc.DocumentMetadata, error) {
	service, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: db, LazyDB: store.LazyLLMDB()})
	if err != nil {
		return nil, err
	}
	return service.GetProcessingMetadata(r.Context(), sourceCaller(r), dataset, ids)
}

func loadLibraryView(r *http.Request, db *gorm.DB, dataset string, ids []string, folders bool) (paperlibrary.View, error) {
	view, err := paperlibrary.Load(r.Context(), db, dataset, store.UserID(r), ids, folders)
	if err != nil {
		return view, err
	}
	documents := []string{}
	for _, attachment := range view.Data.Attachments {
		if attachment.Kind == "pdf" && attachment.Available && attachment.ContentRef != "" {
			documents = append(documents, attachment.ContentRef)
		}
	}
	metadata, err := processingMetadata(r, db, dataset, documents)
	if err != nil {
		return view, err
	}
	for i := range view.Data.Papers {
		view.Data.Papers[i].Parse = "unknown"
	}
	for i := range view.Data.Attachments {
		attachment := &view.Data.Attachments[i]
		if attachment.Kind != "pdf" {
			continue
		}
		current, exists := metadata[attachment.ContentRef]
		attachment.Parse = "unknown"
		if exists {
			status := doc.DocumentProcessingStatus(doc.ProcessingLevelParsed, current.ProcessingState)
			taskStatus := doc.NormalizeTaskStateForUI(current.ParseStatus)
			switch {
			case status == "ready":
				attachment.Parse = "ready"
			case status == "failed":
				attachment.Parse = "failed"
			case (status == "pending" || status == "running") && taskStatus == "WAITING":
				attachment.Parse = "queued"
			case (status == "pending" || status == "running") && taskStatus == "WORKING":
				attachment.Parse = "parsing"
			case current.ObservedProcessingLevel == doc.ProcessingLevelStored && current.ParseStatus == "" && current.ProcessingState.ParseStatus == "pending":
				attachment.Parse = "not_requested"
			}
			if current.ProcessingError != nil && attachment.Parse == "failed" {
				attachment.ProcessingError = current.ProcessingError.Message
			}
		}
		// Preserve original import/submission failures stored in attachment extras.
		// A proven newer success or active task supersedes the old diagnostic.
		if attachment.Parse == "ready" || attachment.Parse == "queued" || attachment.Parse == "parsing" {
			attachment.ProcessingError = ""
		} else if attachment.ProcessingError != "" {
			attachment.Parse = "failed"
		}
		if attachment.Parse == "parsing" || attachment.Parse == "queued" {
			view.Active = true
		}
		if attachment.IsPrimary {
			for j := range view.Data.Papers {
				paper := &view.Data.Papers[j]
				if paper.ID == attachment.PaperID {
					paper.Parse = attachment.Parse
					paper.ProcessingError = attachment.ProcessingError
				}
			}
		}
	}
	return view, nil
}
