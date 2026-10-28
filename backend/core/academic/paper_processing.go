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
	primary := map[string]string{}
	for _, attachment := range view.Data.Attachments {
		if attachment.IsPrimary && attachment.Kind == "pdf" && attachment.Available && attachment.ContentRef != "" {
			documents = append(documents, attachment.ContentRef)
			primary[attachment.PaperID] = attachment.ContentRef
		}
	}
	metadata, err := processingMetadata(r, db, dataset, documents)
	if err != nil {
		return view, err
	}
	for i := range view.Data.Papers {
		paper := &view.Data.Papers[i]
		id := primary[paper.ID]
		if id == "" {
			continue
		}
		current, exists := metadata[id]
		status := doc.DocumentProcessingStatus(current.ProcessingLevel, current.ProcessingState)
		switch {
		case exists && (status == "ready" || status == "not_requested"):
			paper.Parse = "ready"
		case exists && status == "failed":
			paper.Parse = "failed"
			if current.ProcessingError != nil {
				paper.ProcessingError = current.ProcessingError.Message
			}
		default:
			paper.Parse = "parsing"
			view.Active = true
		}
	}
	return view, nil
}
