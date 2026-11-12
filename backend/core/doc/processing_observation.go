package doc

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/store"
)

type processingSnapshot struct {
	dataset     orm.Dataset
	documents   []orm.Document
	states      map[string]orm.DocumentProcessingState
	tasks       map[string]orm.Task
	diagnostics map[[2]string][2]string
	external    map[[2]string]string // exact external task ID and document ID
}

// Core ownership, latest tasks and processing revision are loaded together.
// External observations are bounded batches after the read-only Core snapshot;
// none of these results is published back into mutable stage state.
func loadProcessingSnapshot(ctx context.Context, db, lazyDB *gorm.DB, datasetID, documentID string, documentIDs ...[]string) (processingSnapshot, error) {
	snapshot := processingSnapshot{diagnostics: map[[2]string][2]string{}, states: map[string]orm.DocumentProcessingState{}, tasks: map[string]orm.Task{}, external: map[[2]string]string{}}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND deleted_at IS NULL", datasetID).Take(&snapshot.dataset).Error; err != nil {
			return err
		}
		docs := tx.Where("dataset_id = ? AND deleted_at IS NULL", datasetID)
		if documentID != "" {
			docs = docs.Where("id = ?", documentID)
		} else if len(documentIDs) > 0 {
			docs = docs.Where("id IN ?", documentIDs[0])
		} else {
			docs = docs.Where("document_type <> ?", "FOLDER")
		}
		if err := docs.Find(&snapshot.documents).Error; err != nil {
			return err
		}
		if len(snapshot.documents) == 0 || (documentID == "" && len(documentIDs) == 0 && effectiveProcessingLevel(snapshot.dataset.ProcessingLevel) == ProcessingLevelStored) {
			return nil
		}
		var states []orm.DocumentProcessingState
		q := tx.Where("dataset_id = ?", datasetID)
		if documentID != "" {
			q = q.Where("document_id = ?", documentID)
		} else if len(documentIDs) > 0 {
			q = q.Where("document_id IN ?", documentIDs[0])
		}
		if err := q.Find(&states).Error; err != nil {
			return err
		}
		for _, state := range states {
			snapshot.states[state.DocumentID] = state
		}
		var tasks []orm.Task
		q = tx.Where("tasks.dataset_id = ? AND tasks.deleted_at IS NULL", datasetID).
			Where("EXISTS (SELECT 1 FROM documents owned WHERE owned.id = tasks.doc_id AND owned.dataset_id = tasks.dataset_id AND owned.deleted_at IS NULL)")
		if documentID != "" {
			q = q.Where("tasks.doc_id = ?", documentID)
		} else if len(documentIDs) > 0 {
			q = q.Where("tasks.doc_id IN ?", documentIDs[0])
		}
		// Creation order, not update order: an older task finishing late cannot win.
		q = q.Where(`NOT EXISTS (SELECT 1 FROM tasks newer WHERE newer.dataset_id = tasks.dataset_id AND newer.doc_id = tasks.doc_id AND newer.deleted_at IS NULL AND (newer.created_at > tasks.created_at OR (newer.created_at = tasks.created_at AND newer.id > tasks.id)))`)
		if err := q.Find(&tasks).Error; err != nil {
			return err
		}
		for _, task := range tasks {
			snapshot.tasks[task.DocID] = task
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return snapshot, err
	}
	if lazyDB == nil || snapshot.dataset.KbID == "" {
		return snapshot, nil
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, document := range snapshot.documents {
		task := snapshot.tasks[document.ID]
		if document.LazyllmDocID != "" && task.LazyllmTaskID != "" && !seen[task.LazyllmTaskID] {
			ids = append(ids, task.LazyllmTaskID)
			seen[task.LazyllmTaskID] = true
		}
	}
	const batchSize = 500
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		var rows []readonlyorm.LazyLLMDocServiceTaskRow
		if err := lazyDB.WithContext(ctx).Where("task_id IN ? AND kb_id = ?", ids[start:end], snapshot.dataset.KbID).Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
			return snapshot, err
		}
		for _, row := range rows {
			key := [2]string{row.TaskID, row.DocID}
			if _, exists := snapshot.external[key]; !exists {
				snapshot.external[key] = strings.TrimSpace(row.Status)
				code, message := "", ""
				if row.ErrorCode != nil {
					code = *row.ErrorCode
				}
				if row.ErrorMsg != nil {
					message = *row.ErrorMsg
				}
				if message == "" && row.Message != nil {
					message = *row.Message
				}
				// Parser callbacks use code=200/message=success for ordinary acknowledgements.
				// An actual error on this same task overrides a success status.
				code = strings.TrimSpace(code)
				if code == "200" {
					code = ""
				}
				if strings.EqualFold(strings.TrimSpace(message), "success") {
					message = ""
				}
				explicitError := row.ErrorMsg != nil && strings.TrimSpace(*row.ErrorMsg) != "" && !strings.EqualFold(strings.TrimSpace(*row.ErrorMsg), "success")
				if code != "" || explicitError {
					snapshot.external[key] = "FAILED"
				}
				snapshot.diagnostics[key] = [2]string{code, message}
			}
		}
	}
	return snapshot, nil
}

func unknownProcessingState(document orm.Document) orm.DocumentProcessingState {
	return orm.DocumentProcessingState{DatasetID: document.DatasetID, DocumentID: document.ID, ParseStatus: "unknown", ChunkStatus: "unknown", IndexStatus: "unknown"}
}

// Index cleanup invalidates previous parser results, but historical task
// records remain available. For reset documents, use the current task bound
// to the Core processing state instead of the latest remote history entry.
func applyManualReparseStages(ctx context.Context, datasetID string, rows []mergedDocRow) error {
	if len(rows) == 0 {
		return nil
	}
	rowByDocument := make(map[string]int, len(rows))
	ids := make([]string, 0, len(rows))
	for i, row := range rows {
		rowByDocument[row.DocID] = i
		ids = append(ids, row.DocID)
	}
	db := store.DB()
	var resetIDs []string
	if err := db.WithContext(ctx).Model(&orm.DocumentProcessingState{}).
		Where("dataset_id = ? AND document_id IN ? AND embedding_version IN ?", datasetID, ids, []string{embeddingVersionReembedManual, embeddingVersionRebuildRequired}).
		Pluck("document_id", &resetIDs).Error; err != nil || len(resetIDs) == 0 {
		return err
	}
	snapshot, err := loadProcessingSnapshot(ctx, db, store.LazyLLMDB(), datasetID, "", resetIDs)
	if err != nil {
		return err
	}
	for _, document := range snapshot.documents {
		if isFolderLikeDocument(document) || (snapshot.states[document.ID].EmbeddingVersion != embeddingVersionReembedManual && snapshot.states[document.ID].EmbeddingVersion != embeddingVersionRebuildRequired) {
			continue
		}
		state, _ := snapshot.observe(document)
		stage := "DOCUMENT_REPARSE_REQUIRED"
		switch state.ParseStatus {
		case "running":
			stage = "DOCUMENT_PARSING"
		case "succeeded":
			stage = "SUCCESS"
		case "failed":
			stage = "DOCUMENT_PARSING_FAILED"
		}
		rows[rowByDocument[document.ID]].DocumentStage = stage
	}
	return nil
}

func (snapshot processingSnapshot) observe(document orm.Document) (orm.DocumentProcessingState, string) {
	state, exists := snapshot.states[document.ID]
	if !exists {
		state = unknownProcessingState(document)
	}
	task, hasTask := snapshot.tasks[document.ID]
	if state.EmbeddingVersion == embeddingVersionReembedManual && (!hasTask || state.IndexArtifactRef != "core-task:"+task.ID) {
		state.IndexStatus = "pending"
		return state, ""
	}
	if state.EmbeddingVersion == embeddingVersionRebuildRequired {
		state.IndexStatus = "pending"
	}
	// Pending defaults are not evidence that an unrequested stage is queued.
	// Preserve persisted terminal results, then overlay proven current task stages.
	for _, stage := range []*string{&state.ChunkStatus, &state.IndexStatus} {
		if stage == &state.IndexStatus && state.EmbeddingVersion == embeddingVersionRebuildRequired {
			continue
		}
		if *stage != "succeeded" && *stage != "failed" {
			*stage = "unknown"
		}
	}
	if !hasTask {
		return state, ""
	}
	var ext taskExt
	_ = json.Unmarshal(task.Ext, &ext)
	if ext.ProcessingLevel == ProcessingLevelStored {
		return state, ""
	}
	// An upload receipt for an original-only paper is not a parser result.
	// Older receipts have no submitted level or remote task ID.
	if snapshot.dataset.Scene == "academic_papers" && task.TaskType == string(TaskTypeParseUploaded) &&
		task.LazyllmTaskID == "" && ext.ProcessingLevel == "" && ext.TaskState == string(TaskStateSucceeded) {
		return state, ""
	}
	nativeIndexing := (state.ParseArtifactRef != "" || (state.EmbeddingVersion == embeddingVersionRebuildRequired && state.ParseStatus == "succeeded") || state.EmbeddingVersion == embeddingVersionReembedPending || (state.EmbeddingVersion == embeddingVersionReembedManual && state.ParseStatus == "succeeded")) && ext.ProcessingLevel == ProcessingLevelIndexed
	if nativeIndexing && (state.IndexArtifactRef != "core-task:"+task.ID || ext.ProcessingRevision != snapshot.dataset.ProcessingRevision) {
		return state, ""
	}
	raw := strings.TrimSpace(ext.TaskState)
	if task.LazyllmTaskID != "" {
		// Submission alone is not evidence that the external task is running.
		raw = snapshot.external[[2]string{task.LazyllmTaskID, document.LazyllmDocID}]
		if raw == "" {
			raw = "UNKNOWN"
		}
	}
	status := ""
	switch NormalizeTaskStateForUI(raw) {
	case "SUCCESS":
		status = "succeeded"
	case "FAILED", "CANCELED":
		status = "failed"
	case "WORKING", "WAITING":
		status = "running"
	case "UNKNOWN":
		status = "unknown"
	}
	if status == "" {
		return state, raw
	}
	// Native parsing and chunks remain usable independently of vector indexing.
	// Only the task bound to this artifact and revision can overlay its index stage.
	if nativeIndexing {
		if !task.CreatedAt.Before(state.UpdatedAt) || status != "failed" {
			state.IndexErrorCode = ""
			state.IndexErrorMessage = ""
		}
		state.IndexStatus = status
		return state, raw
	}
	// A newer task must not inherit an older attempt's failure details.
	if !task.CreatedAt.Before(state.UpdatedAt) || status != "failed" {
		state.ParseErrorCode = ""
		state.ParseErrorMessage = ""
		state.ChunkErrorCode = ""
		state.ChunkErrorMessage = ""
		state.IndexErrorCode = ""
		state.IndexErrorMessage = ""
	}
	state.ParseStatus = status
	// Historical tasks prove parsing only. Other stages require the level and
	// revision actually submitted, not today's library default.
	if ext.ProcessingRevision != snapshot.dataset.ProcessingRevision {
		return state, raw
	}
	if ext.ProcessingLevel == ProcessingLevelChunked || ext.ProcessingLevel == ProcessingLevelIndexed {
		state.ChunkStatus = status
	}
	if ext.ProcessingLevel == ProcessingLevelIndexed {
		state.IndexStatus = status
	}
	return state, raw
}

func (snapshot processingSnapshot) observedProcessingLevel(task orm.Task, state orm.DocumentProcessingState) string {
	var ext taskExt
	_ = json.Unmarshal(task.Ext, &ext)
	if ext.ProcessingRevision == snapshot.dataset.ProcessingRevision {
		switch ext.ProcessingLevel {
		case ProcessingLevelParsed, ProcessingLevelChunked, ProcessingLevelIndexed:
			return ext.ProcessingLevel
		}
	}
	// Legacy tasks can still prove a completed stage through the observed
	// document state. Seeded pending values alone do not prove a submission.
	switch {
	case state.IndexStatus == "succeeded" || state.IndexStatus == "failed":
		return ProcessingLevelIndexed
	case state.ChunkStatus == "succeeded" || state.ChunkStatus == "failed":
		return ProcessingLevelChunked
	case state.ParseStatus == "succeeded" || state.ParseStatus == "failed" || state.ParseStatus == "running",
		task.LazyllmTaskID != "", ext.MarketSubmission == "submitting":
		return ProcessingLevelParsed
	default:
		return ProcessingLevelStored
	}
}

func ObserveDocumentProcessingDetails(ctx context.Context, db, lazyDB *gorm.DB, document orm.Document) (orm.DocumentProcessingState, string, *ProcessingDiagnostic, error) {
	snapshot, err := loadProcessingSnapshot(ctx, db, lazyDB, document.DatasetID, document.ID)
	if err != nil {
		return unknownProcessingState(document), "UNKNOWN", nil, err
	}
	if len(snapshot.documents) == 0 {
		return unknownProcessingState(document), "UNKNOWN", nil, gorm.ErrRecordNotFound
	}
	current := snapshot.documents[0]
	if current.ResourceVersion != document.ResourceVersion || current.LazyllmDocID != document.LazyllmDocID {
		return unknownProcessingState(document), "UNKNOWN", nil, nil
	}
	state, raw := snapshot.observe(current)
	task := snapshot.tasks[current.ID]
	detail := snapshot.diagnostics[[2]string{task.LazyllmTaskID, current.LazyllmDocID}]
	if task.LazyllmTaskID == "" {
		var ext taskExt
		_ = json.Unmarshal(task.Ext, &ext)
		detail[1] = ext.ErrorMessage
	}
	return state, raw, processingDiagnostic(state, task, raw, detail[0], detail[1]), nil
}

func datasetProcessingCoverage(ctx context.Context, db, lazyDB *gorm.DB, dataset orm.Dataset) (ProcessingCoverage, error) {
	snapshot, err := loadProcessingSnapshot(ctx, db, lazyDB, dataset.ID, "")
	if err != nil {
		return ProcessingCoverage{}, err
	}
	result := ProcessingCoverage{Total: int64(len(snapshot.documents))}
	level := effectiveProcessingLevel(snapshot.dataset.ProcessingLevel)
	for _, row := range snapshot.documents {
		if level == ProcessingLevelStored {
			result.Available++
			continue
		}
		state, _ := snapshot.observe(row)
		status := state.ParseStatus
		if level == ProcessingLevelChunked {
			status = state.ChunkStatus
		} else if level == ProcessingLevelIndexed {
			status = state.IndexStatus
		}
		if status == "succeeded" {
			result.Available++
		}
		if state.ParseStatus == "failed" || state.ChunkStatus == "failed" || state.IndexStatus == "failed" {
			result.Failed++
		} else if state.ParseStatus == "running" || state.ChunkStatus == "running" || state.IndexStatus == "running" {
			result.Processing++
		}
	}
	return result, nil
}
