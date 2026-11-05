package academic

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
)

func earlierPaperAnswer(ctx context.Context, db *gorm.DB, call capability.InvocationContext, turn int, id string) (orm.ChatHistory, []sourceRecord, error) {
	var active orm.ChatHistory
	if err := db.WithContext(ctx).Select("seq").Where("conversation_id = ? AND id = ?", call.ConversationID, call.HistoryID).Take(&active).Error; err != nil {
		return orm.ChatHistory{}, nil, err
	}
	query := db.WithContext(ctx).Model(&orm.ChatHistory{}).Select("id", "seq", "retrieval_result").
		Where("conversation_id = ? AND seq < ? AND retrieval_result IS NOT NULL", call.ConversationID, active.Seq)
	if turn > 0 {
		query = query.Where("seq = ?", turn)
	}
	if id != "" {
		query = query.Where("id = ?", id)
	}
	rows, err := query.Order("seq DESC").Rows()
	if err != nil {
		return orm.ChatHistory{}, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var answer orm.ChatHistory
		if err := db.ScanRows(rows, &answer); err != nil {
			return orm.ChatHistory{}, nil, err
		}
		records, err := decodeSources(answer.RetrievalResult)
		if err != nil || id != "" || turn > 0 || len(records) > 0 {
			return answer, records, err
		}
	}
	return orm.ChatHistory{}, nil, rows.Err()
}

func AgentPaperSources(ctx context.Context, call capability.InvocationContext, in capability.PaperSourcesInput) (capability.PaperSourcesResult, error) {
	if !call.Principal.Permissions.Has("document.read") && !call.Principal.Permissions.Has("*") {
		return capability.PaperSourcesResult{}, capability.NewError(capability.PermissionDenied, capability.PaperSourcesTool, "document read permission required", false, nil)
	}
	if in.AnswerTurn < 0 {
		return capability.PaperSourcesResult{}, capability.NewError(capability.InvalidArgument, capability.PaperSourcesTool, "answer_turn must be positive when specified", false, nil)
	}
	db := store.DB()
	answer, records, err := earlierPaperAnswer(ctx, db, call, in.AnswerTurn, "")
	if err != nil {
		return capability.PaperSourcesResult{}, err
	}
	inspected, err := inspectSourceRecords(ctx, db, records, nil)
	if err != nil {
		return capability.PaperSourcesResult{}, err
	}
	items := make([]capability.PaperSource, 0, len(inspected))
	for _, source := range inspected {
		items = append(items, agentPaperSource(source))
	}
	return capability.PaperSourcesResult{AnswerID: answer.ID, AnswerTurn: answer.Seq, Items: items}, nil
}

func agentPaperSource(source SourceInspection) capability.PaperSource {
	status := "unidentified"
	if source.Requested != nil && !source.Eligible {
		status = "needs_confirmation"
	}
	if source.Eligible {
		status = "identified"
		if source.NeedsPDFLookup {
			status = "needs_pdf_lookup"
		}
	}
	return capability.PaperSource{SourceID: source.SourceID, Title: source.Title, URL: source.URL, PaperKey: source.PaperKey, Cited: source.Cited, Status: status, Reason: source.Reason}
}

func AgentImportPapers(ctx context.Context, call capability.InvocationContext, in capability.PaperImportInput, op doc.ResourceOperationContext) (capability.PaperImportResult, error) {
	result := capability.PaperImportResult{KnowledgeBaseID: in.KnowledgeBaseID, Items: []capability.PaperSource{}}
	if !call.Principal.Permissions.Has("document.write") && !call.Principal.Permissions.Has("*") {
		return result, capability.NewError(capability.PermissionDenied, capability.PaperImportTool, "document write permission required", false, nil)
	}
	if !systemdeps.PythonComponentActive("rag") {
		return result, capability.NewError(capability.Unsupported, capability.PaperImportTool, "paper import is unavailable", false, nil)
	}
	if in.KnowledgeBaseID == "" || len(in.SourceIDs) > 500 || len(in.Papers) > 500 ||
		(len(in.SourceIDs) == 0) == (len(in.Papers) == 0) ||
		len(in.SourceIDs) > 0 && (in.AnswerID != "") == (len(in.CurrentSources) > 0) ||
		len(in.Papers) > 0 && len(in.CurrentSources) > 0 {
		return result, capability.NewError(capability.InvalidArgument, capability.PaperImportTool, "a target paper collection and exactly one of source_ids or papers are required", false, nil)
	}
	if call.AllowedDatasetIDs != nil && !slices.Contains(call.AllowedDatasetIDs, in.KnowledgeBaseID) {
		return result, capability.NewError(capability.PermissionDenied, capability.PaperImportTool, "target outside the allowed library scope", false, nil)
	}
	db := store.DB()
	source := SourceContext{ConversationID: call.ConversationID, HistoryID: call.HistoryID}
	var records []sourceRecord
	var answer orm.ChatHistory
	if in.AnswerID != "" {
		var err error
		answer, records, err = earlierPaperAnswer(ctx, db, call, 0, in.AnswerID)
		if err != nil {
			return result, err
		}
		if answer.ID == "" {
			return result, capability.NewError(capability.InvalidArgument, capability.PaperImportTool, "answer unavailable in this conversation", false, nil)
		}
		source.HistoryID = answer.ID
	} else {
		for _, item := range in.CurrentSources {
			records = append(records, sourceRecord{ID: item.SourceID, Title: item.Title, URL: item.URL, DOI: item.DOI})
		}
	}
	var inspected []SourceInspection
	switch {
	case len(in.SourceIDs) > 0:
		var err error
		inspected, err = inspectSourceRecords(ctx, db, records, in.SourceIDs)
		if err != nil {
			return result, capability.NewError(capability.InvalidArgument, capability.PaperImportTool, err.Error(), false, nil)
		}
	default:
		for i, paper := range in.Papers {
			id := fmt.Sprintf("paper:%d", i+1)
			if paper.SourceID != "" && len(records) > 0 {
				var err error
				paper, err = paperSelectionFromSource(ctx, db, paper, records)
				if err != nil {
					inspected = append(inspected, SourceInspection{SourceID: id, Title: paper.Title, Requested: &paper, Reason: err.Error()})
					continue
				}
			}
			item, err := inspectPaperSelection(id, paper)
			if err != nil {
				item.Reason = err.Error()
			}
			inspected = append(inspected, item)
		}
	}
	unique := importableSources(inspected)
	for _, item := range inspected {
		view := agentPaperSource(item)
		if item.Eligible {
			view.Status = "queued"
			if unique[sourceSelectionKey(item)].SourceID != item.SourceID {
				view.Status = "duplicate_selection"
			}
		}
		result.Items = append(result.Items, view)
	}
	if len(unique) == 0 {
		return result, nil
	}
	if err := op.ValidateBeforeCommit(ctx); err != nil {
		return capability.PaperImportResult{}, err
	}
	ctx, err := prepareSourceTarget(ctx, op.Caller, in.KnowledgeBaseID)
	if err != nil {
		return capability.PaperImportResult{}, err
	}
	batchID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("source-import:"+op.Caller.UserID+":"+op.OperationKey)).String()
	batch := newSourceImportBatch(op.Caller, source, in.KnowledgeBaseID, batchID, op.OperationKey, sourceHash(in), len(unique), false)
	if err = enqueueSourceImport(ctx, op.Caller, &batch, unique, op.ValidateExecution); err != nil {
		return capability.PaperImportResult{}, err
	}
	result.BatchID, result.QueuedCount = batch.ID, batch.TotalItems
	return result, nil
}
