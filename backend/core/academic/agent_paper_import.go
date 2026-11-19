package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
	"strings"
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
	// Authorize the destination before sending title queries to external services.
	var targetErr error
	ctx, targetErr = prepareSourceTarget(ctx, op.Caller, in.KnowledgeBaseID)
	if targetErr != nil {
		return result, targetErr
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
	cachedSelections, err := acceptedTitleSelections(ctx, db, op.Caller.UserID, op.OperationKey, in)
	if err != nil {
		return result, err
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
			if cached, ok := cachedSelections[id]; ok {
				inspected = append(inspected, cached)
				continue
			}
			if paper.SourceID != "" && len(records) > 0 {
				var err error
				paper, err = paperSelectionFromSource(ctx, db, paper, records)
				if err != nil {
					inspected = append(inspected, SourceInspection{SourceID: id, Title: paper.Title, Requested: &paper, Reason: err.Error()})
					continue
				}
			}
			validated, validationErr := inspectPaperSelection(id, paper)
			if validationErr != nil {
				validated.Reason = validationErr.Error()
				inspected = append(inspected, validated)
				continue
			}
			paper = *validated.Requested
			if paper.WorkID == "" && paper.DOI == "" && paper.ArxivID == "" && strings.TrimSpace(paper.Title) != "" {
				work, err := resolveSelectedTitle(ctx, op.Caller.UserID, paper)
				if err != nil {
					inspected = append(inspected, SourceInspection{SourceID: id, Title: paper.Title, Requested: &paper, Reason: err.Error()})
					continue
				}
				paper.Title, paper.Authors, paper.Year, paper.Venue = work.Title, work.Authors, work.Year, work.Venue
				paper.DOI, paper.ArxivID = work.DOI, work.ArxivID
			}
			item, err := inspectPaperSelection(id, paper)
			if err != nil {
				item.Reason = err.Error()
			}
			inspected = append(inspected, item)
		}
	}
	// A caller-supplied pair is not evidence that two identifiers name one work.
	// Keep the selected edition's authority, including in the durable worker input.
	// Existing WorkIDs are checked against the saved identity in agentImportDraft.
	for i := range inspected {
		item := &inspected[i]
		if item.Requested == nil || item.Requested.WorkID != "" || !item.Eligible {
			continue
		}
		p := *item.Requested
		if p.DOI == "" || p.ArxivID == "" {
			continue
		}
		if publicationRequested(p) {
			p.ArxivID = ""
		} else {
			p.DOI = ""
		}
		accepted, err := inspectPaperSelection(item.SourceID, p)
		if err != nil {
			return result, err
		}
		item.Requested, item.PaperKey = accepted.Requested, accepted.PaperKey
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
	ctx, err = prepareSourceTarget(ctx, op.Caller, in.KnowledgeBaseID)
	if err != nil {
		return result, err
	}
	input := createImportRequest{EntryType: "conversation_sources", TargetDatasetID: in.KnowledgeBaseID,
		IdempotencyKey: "chat:" + sourceHash([]string{op.Caller.UserID, op.OperationKey})}
	positions := []int{}
	selectedSources := []SourceInspection{}
	for i, source := range inspected {
		if !source.Eligible || unique[sourceSelectionKey(source)].SourceID != source.SourceID {
			continue
		}
		draft, err := agentImportDraft(ctx, db, source)
		if err != nil {
			return result, capability.NewError(capability.InvalidArgument, capability.PaperImportTool, err.Error(), false, nil)
		}
		workID := source.Work.WorkID
		if source.Requested != nil {
			workID = source.Requested.WorkID
		}
		input.Items = append(input.Items, selectedImportItem{Paper: draft, WorkID: workID})
		selectedSources = append(selectedSources, source)
		positions = append(positions, i)
	}
	decisions := make([]paperImportDecision, 0, len(inspected))
	for _, inspection := range inspected {
		decisions = append(decisions, paperImportDecision{Inspection: inspection, Selection: inspection.Requested})
	}
	batch, ids, err := submitLibraryImport(ctx, op.Caller.UserID, op.Caller.UserName, input, libraryImportOptions{
		Source: &source, TenantID: op.Caller.TenantID, RequestHash: sourceHash(in), Sources: selectedSources, Decisions: decisions,
		Fence: func(ctx context.Context, tx *gorm.DB) error {
			if err := op.ValidateExecution(ctx, tx); err != nil {
				return err
			}
			dataset, err := lockSourceTarget(ctx, tx, op.Caller, in.KnowledgeBaseID)
			if err != nil {
				return err
			}
			if dataset.Scene != "academic_papers" {
				return errSourceTargetScene
			}
			return nil
		},
	})
	if err != nil {
		if errors.Is(err, errSourceTargetScene) {
			return result, capability.NewError(capability.InvalidArgument, capability.PaperImportTool, err.Error(), false, nil)
		}
		return result, err
	}
	var items []orm.PaperImportItem
	if err := db.WithContext(ctx).Where("batch_id=?", batch.ID).Order("created_at, id").Find(&items).Error; err != nil {
		return result, err
	}
	byPaper := map[string]orm.PaperImportItem{}
	for _, item := range items {
		byPaper[item.LibraryPaperID] = item
		if item.Status == "pending" || item.Status == "running" {
			result.QueuedCount++
		}
	}
	for j, i := range positions {
		result.Items[i].LibraryPaperID = ids[j]
		result.Items[i].Status = "metadata_saved"
		result.Items[i].Reason = "题录已加入资料库，全文与元信息在后台补全；不表示全文已就绪"
		if byPaper[ids[j]].ResultKind == "already_present" {
			result.Items[i].Status = "already_present"
			result.Items[i].Reason = "资料库已有该论文，未重复创建条目"
		}
	}
	result.BatchID = batch.ID
	return result, nil
}

func agentImportDraft(ctx context.Context, db *gorm.DB, source SourceInspection) (*paperlibrary.Paper, error) {
	p := capability.PaperSelection{WorkID: source.Work.WorkID, DOI: source.Work.DOI, ArxivID: source.Work.ArxivID,
		Title: source.Title, Authors: source.Work.Authors, Year: source.Work.Year, Venue: source.Work.Venue}
	if source.Requested != nil {
		p = *source.Requested
	}
	if p.WorkID != "" {
		var work orm.AcademicWork
		if err := db.WithContext(ctx).Where("id=?", p.WorkID).Take(&work).Error; err != nil {
			return nil, err
		}
		ref := paperRef(work)
		arxiv, _ := NormalizeArxivID(p.ArxivID)
		if p.DOI != "" && p.DOI != ref.DOI || arxiv != "" && arxiv != ref.ArxivID {
			return nil, errPaperSelectionUnconfirmed
		}
		// Stable identity selects the work; descriptive fields are not an admission veto.
		p.Title, p.Authors, p.Year, p.Venue = ref.Title, ref.Authors, ref.Year, ref.Venue
		p.DOI = ref.DOI
		if p.ArxivID == "" {
			p.ArxivID = ref.ArxivID
		}
	}
	base, version := NormalizeArxivID(p.ArxivID)
	if version == "" && base != "" && source.Candidate.SourceProvider == "arxiv" {
		version = source.Candidate.Version
	}
	// An explicitly requested published edition uses its DOI, not a preprint fallback.
	if publicationRequested(p) {
		base, version = "", ""
	}
	title := firstNonEmpty(strings.TrimSpace(p.Title), base, p.DOI, p.WorkID)
	draft := &paperlibrary.Paper{ID: "chat-" + sourceHash(source.PaperKey)[:32], Tags: []string{},
		Metadata: paperlibrary.Metadata{Title: title, Authors: p.Authors, Year: p.Year, Venue: p.Venue,
			Identifiers: paperlibrary.Identifiers{DOI: p.DOI, Arxiv: base}}, Source: "unknown", ArxivVersion: version}
	if base != "" {
		draft.Source = "arxiv"
		draft.SourceURL = "https://arxiv.org/abs/" + base + version
	} else if p.DOI != "" {
		draft.Source = "doi"
		draft.SourceURL = "https://doi.org/" + p.DOI
	}
	return draft, nil
}

// Replayed operations reuse their accepted identity even if a provider is now unavailable.
func acceptedTitleSelections(ctx context.Context, db *gorm.DB, user, operation string, in capability.PaperImportInput) (map[string]SourceInspection, error) {
	result := map[string]SourceInspection{}
	hasTitle := false
	for _, p := range in.Papers {
		if strings.TrimSpace(p.WorkID) == "" && strings.TrimSpace(p.DOI) == "" && strings.TrimSpace(p.ArxivID) == "" && strings.TrimSpace(p.Title) != "" {
			hasTitle = true
		}
	}
	if !hasTitle {
		return result, nil
	}
	var batch orm.PaperImportBatch
	err := db.WithContext(ctx).Where("created_by=? AND idempotency_key=?", user, "chat:"+sourceHash([]string{user, operation})).Take(&batch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	var policy struct {
		RequestHash string                `json:"request_hash"`
		Decisions   []paperImportDecision `json:"paper_decisions"`
	}
	if err = json.Unmarshal(batch.PolicySnapshotJSON, &policy); err != nil {
		return nil, err
	}
	if policy.RequestHash != sourceHash(in) || batch.TargetDatasetID != in.KnowledgeBaseID {
		return nil, invalidLibraryMutation("INVALID_REQUEST", "导入请求与首次提交不同")
	}
	if len(policy.Decisions) > 0 {
		for _, decision := range policy.Decisions {
			inspection := decision.Inspection
			inspection.Requested = decision.Selection
			result[inspection.SourceID] = inspection
		}
		return result, nil
	}
	var items []orm.PaperImportItem
	if err = db.WithContext(ctx).Where("batch_id=?", batch.ID).Find(&items).Error; err != nil {
		return nil, err
	}
	for _, item := range items {
		var selected savedImportCandidate
		var ids []string
		if err = json.Unmarshal(item.SelectedCandidateJSON, &selected); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(item.ReferenceIDsJSON, &ids); err != nil {
			return nil, err
		}
		if selected.Requested != nil {
			p := selected.Requested
			for _, id := range ids {
				result[id], err = inspectPaperSelection(id, *p)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	originals := map[string]SourceInspection{}
	for i, p := range in.Papers {
		if accepted, ok := result[fmt.Sprintf("paper:%d", i+1)]; ok {
			originals[sourceHash(p)] = accepted
		}
	}
	for i, p := range in.Papers {
		id := fmt.Sprintf("paper:%d", i+1)
		if _, ok := result[id]; ok {
			continue
		}
		if same, ok := originals[sourceHash(p)]; ok {
			same.SourceID = id
			result[id] = same
		} else {
			copy := p
			result[id] = SourceInspection{SourceID: id, Title: p.Title, Requested: &copy, Reason: "该项未在首次请求中入库；如需重试，请重新发起导入"}
		}
	}
	return result, nil
}
