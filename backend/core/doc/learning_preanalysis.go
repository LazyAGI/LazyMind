package doc

import (
	"encoding/json"
	"lazymind/core/common"
	"lazymind/core/learning"
	"lazymind/core/store"
	"net/http"
	"time"
)

// CreatePreanalysisTask loads document content before invoking the learning service.
func CreatePreanalysisTask(w http.ResponseWriter, r *http.Request) {
	var in learning.PreanalysisRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if len(in.Items) == 0 && in.DatasetID != "" && in.DocumentID != "" {
		documentService, err := NewDocumentService(DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
		if err != nil {
			common.ReplyErr(w, err.Error(), 500)
			return
		}
		chunkRequest := DocumentChunksRequest{UserID: store.UserID(r), DatasetID: in.DatasetID, DocumentID: in.DocumentID, PageSize: 100, SegmentGroup: RootNodeGroup}
		loadChunks := func() error {
			in.Items = nil
			chunkRequest.PageToken = ""
			for {
				result, loadErr := documentService.ListDocumentChunks(r.Context(), chunkRequest)
				if loadErr != nil {
					return loadErr
				}
				for _, chunk := range result.Chunks {
					in.Items = append(in.Items, learning.PreanalysisItem{Text: chunk.Text, Context: chunk.Text, SegmentID: chunk.ID})
				}
				chunkRequest.PageToken = result.NextPageToken
				if chunkRequest.PageToken == "" {
					return nil
				}
			}
		}
		if loadErr := loadChunks(); loadErr != nil {
			common.ReplyErr(w, loadErr.Error(), 400)
			return
		}
		if len(in.Items) == 0 {
			parsed, ensureErr := documentService.EnsureDocumentParsed(r, EnsureDocumentParsedRequest{UserID: store.UserID(r), DatasetID: in.DatasetID, DocumentID: in.DocumentID})
			if ensureErr != nil {
				common.ReplyErr(w, ensureErr.Error(), 400)
				return
			}
			// Parsing is asynchronous. Wait briefly so one click can continue once
			// the canonical Reader root nodes have been persisted.
			deadline := time.NewTimer(30 * time.Second)
			ticker := time.NewTicker(time.Second)
			defer deadline.Stop()
			defer ticker.Stop()
			for len(in.Items) == 0 {
				select {
				case <-r.Context().Done():
					common.ReplyErr(w, "document chunk generation canceled", 408)
					return
				case <-deadline.C:
					common.ReplyErr(w, "document parsing is still running; please retry shortly", 409)
					return
				case <-ticker.C:
					if loadErr := loadChunks(); loadErr != nil {
						common.ReplyErr(w, loadErr.Error(), 400)
						return
					}
				}
			}
			_ = parsed
		}
	}
	task, err := learning.New(store.DB()).CreatePreanalysisTask(r.Context(), store.UserID(r), in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, task)
}
