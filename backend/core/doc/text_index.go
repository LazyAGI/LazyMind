package doc

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
)

// GetTextIndex exposes index evidence independently of the library's default
// processing level: a parse-only paper library may contain explicitly indexed PDFs.
func GetTextIndex(w http.ResponseWriter, r *http.Request) {
	id := datasetIDFromPath(r)
	if _, _, ok := requireDatasetPermission(r, id, acl.PermissionDatasetRead); !ok {
		replyDatasetForbidden(w)
		return
	}
	syncProcessingTaskStates(r, id)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	type row struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		ParseStatus      string `json:"parse_status"`
		IndexStatus      string `json:"index_status"`
		EmbeddingVersion string `json:"embedding_version"`
		Error            string `json:"error"`
	}
	query := store.DB().WithContext(r.Context()).Table("documents d").
		Joins("LEFT JOIN document_processing_states s ON s.dataset_id=d.dataset_id AND s.document_id=d.id").
		Where("d.dataset_id=? AND d.deleted_at IS NULL AND (d.document_type IS NULL OR d.document_type <> ?)", id, "FOLDER")
	// Legacy folders predate document_type. Use the same classifier as parsing,
	// before counting/pagination so they never appear as indexable documents.
	var legacy []orm.Document
	if err := store.DB().WithContext(r.Context()).Select("id", "display_name", "document_type", "ext").
		Where("dataset_id=? AND deleted_at IS NULL AND (document_type IS NULL OR document_type = '')", id).Find(&legacy).Error; err != nil {
		common.ReplyErr(w, "query legacy document types failed", 500)
		return
	}
	var folderIDs []string
	for _, document := range legacy {
		if isFolderLikeDocument(document) {
			folderIDs = append(folderIDs, document.ID)
		}
	}
	if len(folderIDs) > 0 {
		query = query.Where("d.id NOT IN ?", folderIDs)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ReplyErr(w, "query documents failed", 500)
		return
	}
	rows := []row{}
	if err := query.Select("d.id, d.display_name AS name, COALESCE(s.parse_status, '') AS parse_status, COALESCE(s.index_status, '') AS index_status, COALESCE(s.embedding_version, '') AS embedding_version, COALESCE(s.index_error_message, '') AS error").
		Order("d.created_at DESC, d.id").Limit(100).Offset((page - 1) * 100).Scan(&rows).Error; err != nil {
		common.ReplyErr(w, "query documents failed", 500)
		return
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if len(ids) > 0 {
		snapshot, err := loadProcessingSnapshot(r.Context(), store.DB(), store.LazyLLMDB(), id, "", ids)
		if err != nil {
			common.ReplyErr(w, "query processing details failed", 503)
			return
		}
		byID := make(map[string]orm.Document, len(snapshot.documents))
		for _, document := range snapshot.documents {
			byID[document.ID] = document
		}
		for i := range rows {
			document := byID[rows[i].ID]
			state, raw := snapshot.observe(document)
			task := snapshot.tasks[document.ID]
			detail := snapshot.diagnostics[[2]string{task.LazyllmTaskID, document.LazyllmDocID}]
			if task.LazyllmTaskID == "" {
				var ext taskExt
				_ = json.Unmarshal(task.Ext, &ext)
				detail[1] = ext.ErrorMessage
			}
			rows[i].ParseStatus, rows[i].IndexStatus = state.ParseStatus, state.IndexStatus
			if diagnostic := processingDiagnostic(state, task, raw, detail[0], detail[1]); diagnostic != nil {
				rows[i].Error = diagnostic.Message
			} else {
				rows[i].Error = ""
			}
		}
	}
	common.ReplyJSON(w, map[string]any{"documents": rows, "total": total})
}

// ReindexText is explicit and does not promote a parse-only library to indexed.
func ReindexText(w http.ResponseWriter, r *http.Request) {
	id := datasetIDFromPath(r)
	_, user, ok := requireDatasetPermission(r, id, acl.PermissionDatasetWrite)
	if !ok {
		replyDatasetForbidden(w)
		return
	}
	var body struct {
		DocumentIDs []string `json:"document_ids"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || len(body.DocumentIDs) == 0 || len(body.DocumentIDs) > 20 {
		common.ReplyErr(w, "select one to twenty documents", 400)
		return
	}
	if !systemdeps.PythonComponentActive("rag") {
		common.ReplyErr(w, "RAG component is required for indexing", 409)
		return
	}
	if replyEmbedNotReady(w, r, user) {
		return
	}
	// Validate every selected document before creating any task.
	documents := []orm.Document{}
	if err := store.DB().WithContext(r.Context()).Where("dataset_id=? AND id IN ? AND deleted_at IS NULL AND (document_type IS NULL OR document_type <> ?)", id, body.DocumentIDs, "FOLDER").Find(&documents).Error; err != nil {
		common.ReplyErr(w, "query documents failed", 500)
		return
	}
	if len(documents) != len(body.DocumentIDs) {
		common.ReplyErr(w, "document not found or duplicate selection", 400)
		return
	}
	for _, document := range documents {
		if isFolderLikeDocument(document) {
			common.ReplyErr(w, "folder cannot be indexed", 400)
			return
		}
	}
	results := []StartTaskResult{}
	for _, document := range documents {
		kind := string(TaskTypeReparse)
		if strings.TrimSpace(document.LazyllmDocID) == "" {
			kind = string(TaskTypeParseUploaded)
		}
		var state orm.DocumentProcessingState
		if err := store.DB().WithContext(r.Context()).Where("dataset_id=? AND document_id=?", id, document.ID).Find(&state).Error; err != nil {
			results = append(results, StartTaskResult{DocumentID: document.ID, Status: "FAILED", Message: "query processing state failed"})
			continue
		}
		task, err := createTaskFromExistingDocument(r, id, user, store.UserName(r), CreateTaskItem{Task: TaskPayload{
			DocumentID: document.ID, ReparseMode: textIndexReparseMode(state), ReparseGroups: textIndexReparseGroups(state),
		}}, kind)
		if err != nil {
			results = append(results, StartTaskResult{DocumentID: document.ID, Status: "FAILED", Message: err.Error()})
			continue
		}
		var submitted []StartTaskResult
		if kind == string(TaskTypeReparse) {
			submitted, err = startReparseTasksInternalAtLevel(r, id, []string{task.ID}, ProcessingLevelIndexed)
		} else {
			submitted, err = startParseTasksAtLevel(r.Context(), user, id, []string{task.ID}, ProcessingLevelIndexed)
		}
		if len(submitted) == 0 && err != nil {
			submitted = []StartTaskResult{{TaskID: task.ID, DocumentID: document.ID, Status: "FAILED", Message: err.Error()}}
		}
		results = append(results, submitted...)
	}
	common.ReplyJSON(w, map[string]any{"tasks": results})
}
