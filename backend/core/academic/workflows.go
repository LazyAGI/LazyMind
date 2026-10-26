package academic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/chat"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
	workflowdocument "lazymind/core/workflow/document"
	workflowstore "lazymind/core/workflow/store"
)

type WorkflowPaper struct {
	Paper             PaperRef `json:"paper"`
	IsSeed            bool     `json:"is_seed"`
	IsImportant       bool     `json:"is_important"`
	SeedRelation      string   `json:"seed_relation,omitempty"`
	DirectionRelation string   `json:"direction_relation,omitempty"`
	KeyContribution   string   `json:"key_contribution,omitempty"`
}
type ReviewWorkflowInput struct {
	UserInstruction     string          `json:"user_instruction"`
	CandidateDirections []string        `json:"candidate_directions"`
	SelectedDirections  []string        `json:"selected_directions"`
	Papers              []WorkflowPaper `json:"papers"`
}

const ChatLaunchJobType = "academic.chat_launch"

type paperLaunch struct {
	Kind             string   `json:"kind"`
	DatasetID        string   `json:"dataset_id"`
	DocumentIDs      []string `json:"document_ids"`
	PaperIDs         []string `json:"paper_ids,omitempty"`
	Direction        string   `json:"direction"`
	UserInstruction  string   `json:"user_instruction"`
	RequestKey       string   `json:"request_key"`
	ExpansionID      string   `json:"expansion_id,omitempty"`
	ExpansionVersion *int64   `json:"expansion_version,omitempty"`
	ReflectionFirst  bool     `json:"reflection_first"`
}

func marshal(value any) json.RawMessage { b, _ := json.Marshal(value); return b }
func paperConversationInputs(histories []orm.ChatHistory) ([]string, []map[string]any) {
	updates := []string{}
	interactions := []map[string]any{}
	for _, history := range histories {
		if text := strings.TrimSpace(history.RawContent); text != "" {
			updates = append(updates, text)
		}
		var ext map[string]any
		_ = json.Unmarshal(history.Ext, &ext)
		if ext["ask_pending"] != nil {
			interactions = append(interactions, map[string]any{"questions": ext["ask_pending"], "answered": ext["ask_answered"] == true, "answers": ext["ask_saved_answers"]})
		}
	}
	return updates, interactions
}
func newAcademicConversation(tx *gorm.DB, user, dataset, title, kind string) (string, error) {
	id := uuid.NewString()
	enabled := true
	mode := "auto"
	ext := marshal(map[string]any{"academic_task": kind})
	row := orm.Conversation{ID: id, DisplayName: title, ChannelID: "default", Ext: ext, Models: json.RawMessage(`[]`), SearchConfig: json.RawMessage(`{}`), SourceType: "academic", SourceDatasetID: dataset, SourceDisplayName: title, EnableWorkflow: &enabled, WorkflowMode: &mode, BaseModel: orm.BaseModel{CreateUserID: user, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
	return id, tx.Create(&row).Error
}
func resolvePaperSelection(r *http.Request, user, dataset string, ids []string) ([]WorkflowPaper, []map[string]string, error) {
	if !academicDataset(r, user, dataset) {
		return nil, nil, errors.New("academic knowledge base is not accessible")
	}
	var result []WorkflowPaper
	invalid := []map[string]string{}
	seenNodes := map[string]bool{}
	seenPapers := map[string]bool{}
	queue := append([]string(nil), ids...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seenNodes[id] {
			continue
		}
		seenNodes[id] = true
		var row orm.Document
		if err := store.DB().Where("id=? AND dataset_id=? AND deleted_at IS NULL", id, dataset).Take(&row).Error; err != nil {
			invalid = append(invalid, map[string]string{"document_id": id, "reason": "文件不存在或无权限"})
			continue
		}
		if strings.EqualFold(row.DocumentType, "FOLDER") {
			offset := 0
			for {
				var children []orm.Document
				if err := store.DB().Where("dataset_id=? AND p_id=? AND deleted_at IS NULL", dataset, id).Order("id").Limit(200).Offset(offset).Find(&children).Error; err != nil {
					return nil, nil, err
				}
				for _, child := range children {
					queue = append(queue, child.ID)
				}
				if len(children) < 200 {
					break
				}
				offset += len(children)
			}
			continue
		}
		var state orm.AcademicDocumentState
		if store.DB().Where("document_id=? AND kind='paper' AND paper_id<>''", id).Take(&state).Error != nil {
			invalid = append(invalid, map[string]string{"document_id": id, "reason": "尚未确认论文身份，请先识别或手动修正"})
			continue
		}
		if seenPapers[state.PaperID] {
			continue
		}
		var work orm.AcademicWork
		if err := store.DB().Where("id=?", state.PaperID).Take(&work).Error; err != nil {
			return nil, nil, err
		}
		seenPapers[work.ID] = true
		var mark orm.AcademicUserPaperState
		if err := store.DB().Where("user_id=? AND dataset_id=? AND paper_id=?", user, dataset, work.ID).Take(&mark).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
		ref, refErr := datasetPaperRef(store.DB().WithContext(r.Context()), dataset, work)
		if refErr != nil {
			return nil, nil, refErr
		}
		ref.SourceDocumentID = id
		var link orm.AcademicWorkDocument
		if err := store.DB().Where("dataset_id=? AND document_id=? AND academic_work_id=?", dataset, id, work.ID).Take(&link).Error; err == nil {
			ref.SourceVersion, ref.SourceProvider = link.SourceVersion, link.SourceProvider
			pinPaperURLs(&ref)
		}
		result = append(result, WorkflowPaper{Paper: ref, IsSeed: true, IsImportant: mark.IsImportant})
	}
	return result, invalid, nil
}
func ResolvePaperSelection(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req paperLaunch
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "invalid selection")
		return
	}
	papers, invalid, err := resolvePaperSelection(r, user, req.DatasetID, req.DocumentIDs)
	if err != nil {
		replyError(w, 403, "FORBIDDEN", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"papers": papers, "invalid": invalid})
}
func LaunchPaperWorkflow(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req paperLaunch
	if !parseJSON(r, &req) || req.RequestKey == "" || len(req.RequestKey) > 128 {
		replyError(w, 400, "INVALID_REQUEST", "request_key is required")
		return
	}
	if req.Kind != "expansion" && req.Kind != "review" && req.Kind != "reflection" && req.Kind != "ppt" {
		replyError(w, 400, "INVALID_KIND", "invalid paper task")
		return
	}
	if req.PaperIDs != nil && (req.Kind != "review" || req.ExpansionID == "") {
		replyError(w, 400, "INVALID_REQUEST", "paper_ids requires a review from expansion results")
		return
	}
	if req.ExpansionID != "" {
		if req.Kind != "review" {
			replyError(w, 400, "INVALID_REQUEST", "history only supports review")
			return
		}
		launchHistoryReview(w, r, user, req)
		return
	}
	papers, invalid, err := resolvePaperSelection(r, user, req.DatasetID, req.DocumentIDs)
	if err != nil {
		replyError(w, 403, "FORBIDDEN", err.Error())
		return
	}
	if len(invalid) > 0 {
		replyError(w, 422, "INVALID_SELECTION", string(marshal(invalid)))
		return
	}
	if len(papers) == 0 && (strings.TrimSpace(req.Direction) == "" || (req.Kind != "expansion" && req.Kind != "review")) {
		replyError(w, 422, "PAPER_REQUIRED", "select at least one confirmed paper or supply a direction")
		return
	}
	instruction := strings.TrimSpace(req.UserInstruction)
	if req.Direction != "" {
		instruction = firstNonEmpty(instruction, req.Direction)
	}
	if instruction == "" {
		instruction = "围绕所选论文进行研究"
	}
	input := ReviewWorkflowInput{UserInstruction: instruction, CandidateDirections: []string{}, SelectedDirections: []string{}, Papers: papers}
	if req.Direction != "" {
		input.CandidateDirections = []string{req.Direction}
		input.SelectedDirections = []string{req.Direction}
	}
	var convID, recordID string
	err = conversationgroup.UserTransaction(r.Context(), store.DB(), user, func(tx *gorm.DB) error {
		if req.Kind == "expansion" || req.Kind == "review" {
			var existing orm.AcademicExpansion
			if tx.Where("request_owner=? AND request_key=?", user, req.RequestKey).Take(&existing).Error == nil {
				if existing.DeletedAt != nil {
					return errors.New("request was deleted; start a new request")
				}
				convID = existing.ConversationID
				recordID = existing.ID
				return nil
			}
			var e error
			convID, e = newAcademicConversation(tx, user, req.DatasetID, "论文扩展："+truncateTitle(instruction), "expansion")
			if e != nil {
				return e
			}
			seeds, e := seedSources(tx, req.DatasetID, papers)
			if e != nil {
				return e
			}
			record := orm.AcademicExpansion{DiscoveryMode: "external", SeedSnapshotJSON: marshal(seeds), DiscoveryJSON: json.RawMessage(`[]`), ID: uuid.NewString(), UserID: user, RequestOwner: user, RequestKey: req.RequestKey, DatasetID: req.DatasetID, ConversationID: convID, Title: instruction, Goal: req.Kind, Status: "pending", ResultJSON: marshal(input), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if e = tx.Create(&record).Error; e != nil {
				return e
			}
			recordID = record.ID
			query := "请围绕所选论文或方向开展论文拓展。\n" + instruction
			if req.Kind == "review" {
				query = "请先拓展论文，达到全文要求后在新的 Chat 生成综述。\n" + instruction
			}
			return enqueueAcademicChat(r.Context(), tx, user, convID, "paper_expansion", query, req.RequestKey)
		}
		handoff, e := createPaperHandoff(r.Context(), tx, user, req.DatasetID, "", 0, req.Kind, req.RequestKey, input, nil, req.ReflectionFirst)
		if e != nil {
			return e
		}
		convID = handoff.ConversationID
		return nil
	})
	if err != nil {
		replyError(w, 409, "LAUNCH_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]string{"conversation_id": convID, "expansion_id": recordID})
}
func truncateTitle(s string) string {
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:60])
	}
	return s
}
func enqueueAcademicChat(ctx context.Context, tx *gorm.DB, user, conv, workflowID, query, key string) error {
	_, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: ChatLaunchJobType, ResourceType: "conversation", ResourceID: conv, CreateUserID: user, IdempotencyKey: conv + ":" + key, Payload: map[string]string{"query": query, "workflow_id": workflowID}, MaxAttempts: 3})
	return err
}

type discardChatResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (d *discardChatResponse) Header() http.Header    { return d.header }
func (d *discardChatResponse) WriteHeader(status int) { d.status = status }
func (d *discardChatResponse) Write(b []byte) (int, error) {
	if d.body.Len() < 65536 {
		d.body.Write(b)
	}
	return len(b), nil
}
func (d *discardChatResponse) Flush() {}
func init() {
	chat.AcademicConversationContext = academicChatContext
	chat.AcademicConversationUpdated = func(ctx context.Context, db *gorm.DB, conversation string) {
		var session orm.WorkflowSession
		if db.WithContext(ctx).Where("conversation_id=? AND plugin_id='paper_expansion'", conversation).Order("created_at DESC").Take(&session).Error == nil && workflow.OnAcademicRuntimeChanged != nil {
			workflow.OnAcademicRuntimeChanged(ctx, db, session.ID)
			return
		}
		var history orm.ChatHistory
		if db.WithContext(ctx).Where("conversation_id=?", conversation).Order("seq DESC").Take(&history).Error == nil && history.RunStatus == "failed" {
			db.WithContext(ctx).Model(&orm.AcademicExpansion{}).Where("conversation_id=? AND deleted_at IS NULL", conversation).Updates(map[string]any{"status": "failed", "updated_at": time.Now().UTC()})
		}
	}
	asyncjob.Register(ChatLaunchJobType, runAcademicChat)
	asyncjob.Register("academic.project", projectAcademicResult)
	workflow.OnAcademicRuntimeChanged = func(ctx context.Context, db *gorm.DB, session string) {
		if db == nil || !db.Migrator().HasTable(&orm.AcademicExpansion{}) {
			return
		}
		var s orm.WorkflowSession
		if db.WithContext(ctx).Where("id=? AND plugin_id='paper_expansion'", session).Take(&s).Error != nil {
			return
		}
		var record orm.AcademicExpansion
		if db.WithContext(ctx).Where("conversation_id=? AND deleted_at IS NULL", s.ConversationID).Take(&record).Error != nil {
			return
		}
		payload := expansionProjectionPayload{SessionID: session}
		if s.Status == "completed" {
			if artifacts, err := workflowstore.New(db).ListArtifacts(ctx, record.UserID, session); err == nil {
				for i := range artifacts {
					if value, err := academicArtifactJSON(artifacts[i].Value); err == nil {
						artifacts[i].Value = value
					}
				}
				payload.CompletedArtifacts = artifacts
			}
		}
		asyncjob.Enqueue(ctx, db, asyncjob.EnqueueRequest{JobType: "academic.project", ResourceType: "academic_expansion", ResourceID: record.ID, CreateUserID: record.UserID, IdempotencyKey: uuid.NewString(), Payload: payload, MaxAttempts: 5})
	}
}
func runAcademicChat(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var row orm.Conversation
	if err := store.DB().Where("id=? AND create_user_id=? AND deleted_at IS NULL", job.ResourceID, job.CreateUserID).Take(&row).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	var payload map[string]string
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	// A persisted history is the acceptance receipt; never replay a launched Chat.
	var count int64
	if err := store.DB().Model(&orm.ChatHistory{}).Where("conversation_id=?", row.ID).Count(&count).Error; err != nil {
		return asyncjob.Result{}, err
	}
	if count > 0 {
		return asyncjob.Result{}, nil
	}
	if err := store.DB().WithContext(ctx).Model(&orm.AcademicExpansion{}).Where("conversation_id=? AND user_id=? AND deleted_at IS NULL", row.ID, job.CreateUserID).Updates(map[string]any{"status": "running", "updated_at": time.Now().UTC()}).Error; err != nil {
		return asyncjob.Result{}, err
	}
	var task struct {
		Kind string `json:"academic_task"`
	}
	_ = json.Unmarshal(row.Ext, &task)
	name := firstNonEmpty(paperTaskNames[task.Kind], payload["workflow_id"])
	body := marshal(map[string]any{"conversation_id": row.ID, "query": payload["query"], "stream": true, "enable_workflow": true, "enable_plugin": true, "workflow_mode": "auto", "mentions": []map[string]string{{"type": "workflow", "resource_id": "builtin:" + payload["workflow_id"], "display_name": name}}})
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://internal/api/core/conversations:chat", bytes.NewReader(body))
	req.Header.Set("X-User-Id", job.CreateUserID)
	req.Header.Set("Content-Type", "application/json")
	response := &discardChatResponse{header: make(http.Header), status: 200}
	chat.ChatConversations(response, req)
	if response.status >= 400 {
		return asyncjob.Result{}, fmt.Errorf("chat launch returned HTTP %d: %s", response.status, response.body.String())
	}
	return asyncjob.Result{}, nil
}
func createPaperHandoff(ctx context.Context, tx *gorm.DB, user, dataset, record string, version int64, kind, key string, input ReviewWorkflowInput, contents []orm.AcademicPaperContent, reflectionFirst bool) (orm.AcademicHandoff, error) {
	var old orm.AcademicHandoff
	if tx.Where("user_id=? AND request_key=?", user, key).Take(&old).Error == nil {
		return old, nil
	}
	snapshot := handoffContentSnapshot{Sources: []paperSource{}, Contents: contents}
	refs := make([]PaperRef, 0, len(input.Papers))
	for _, paper := range input.Papers {
		refs = append(refs, paper.Paper)
		if paper.IsSeed {
			sources, err := seedSources(tx, dataset, []WorkflowPaper{paper})
			if err != nil {
				return old, err
			}
			snapshot.Sources = append(snapshot.Sources, sources[0])
		} else {
			source, err := matchingLibrarySource(tx, paperSource{Paper: paper.Paper, DatasetID: dataset})
			if err != nil {
				return old, err
			}
			snapshot.Sources = append(snapshot.Sources, source)
		}
	}
	inputJSON := marshal(input)
	if kind != "review" {
		task := PaperTaskInput{UserInstruction: input.UserInstruction, Papers: refs}
		if kind == "reflection" || reflectionFirst {
			notes, err := paperTaskNotes(ctx, tx, dataset, refs)
			if err != nil {
				return old, err
			}
			task.Notes = notes
		}
		inputJSON = marshal(task)
	}
	conv, err := newAcademicConversation(tx, user, dataset, paperTaskNames[kind]+"："+truncateTitle(input.UserInstruction), kind)
	if err != nil {
		return old, err
	}
	handoff := orm.AcademicHandoff{DatasetID: dataset, ReflectionFirst: reflectionFirst, ID: uuid.NewString(), UserID: user, RequestKey: key, ExpansionID: record, Version: version, Kind: kind, ConversationID: conv, InputJSON: inputJSON, ContentsJSON: marshal(snapshot), CreatedAt: time.Now().UTC()}
	if err = tx.Create(&handoff).Error; err != nil {
		return handoff, err
	}
	query := "请生成" + paperTaskNames[kind] + "。\n" + input.UserInstruction
	if reflectionFirst {
		query += "\n先生成阅读心得，再携带心得和原论文清单生成汇报 PPT。"
	}
	workflowID := "paper_workflow_mock"
	if kind == "ppt" && reflectionFirst {
		workflowID = "paper_ppt_with_reflection_mock"
	}
	return handoff, enqueueAcademicChat(ctx, tx, user, conv, workflowID, query, key)
}
func snapshotReview(ctx context.Context, tx *gorm.DB, user, dataset string, input ReviewWorkflowInput, requireAllContents bool) (ReviewWorkflowInput, []orm.AcademicPaperContent, error) {
	seen := map[string]bool{}
	papers := []WorkflowPaper{}
	contents := []orm.AcademicPaperContent{}
	ready := 0
	for _, p := range input.Papers {
		if seen[p.Paper.PaperID] {
			continue
		}
		seen[p.Paper.PaperID] = true
		var work orm.AcademicWork
		if err := tx.Where("id=?", p.Paper.PaperID).Take(&work).Error; err != nil {
			return input, nil, err
		}
		ref, err := datasetPaperRef(tx, dataset, work)
		if err != nil {
			return input, nil, err
		}
		ref.SourceDocumentID = p.Paper.SourceDocumentID
		if p.Paper.SourceVersion != "" {
			ref.SourceVersion, ref.SourceProvider = p.Paper.SourceVersion, p.Paper.SourceProvider
			pinPaperURLs(&ref)
		}
		p.Paper = ref
		var mark orm.AcademicUserPaperState
		if err := tx.Where("user_id=? AND dataset_id=? AND paper_id=?", user, dataset, work.ID).Take(&mark).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return input, nil, err
		}
		p.IsImportant = mark.IsImportant
		source := paperSource{Paper: p.Paper, DatasetID: dataset}
		if p.IsSeed {
			seeds, e := seedSources(tx, dataset, []WorkflowPaper{p})
			if e != nil {
				return input, nil, e
			}
			source = seeds[0]
		} else {
			var sourceErr error
			source, sourceErr = matchingLibrarySource(tx, source)
			if sourceErr != nil {
				return input, nil, sourceErr
			}
		}
		content, contentErr := findSourceContent(tx, user, source)
		available := false
		if contentErr == nil {
			contents = append(contents, content)
			available = content.Status == "ready" && content.ContentHash != "" && len(content.PagesJSON) > 2
			if available {
				ready++
			}
		} else if !errors.Is(contentErr, gorm.ErrRecordNotFound) {
			return input, nil, contentErr
		}
		if requireAllContents && !available {
			return input, nil, errors.New("所选论文全文暂不可用，请刷新后重试")
		}
		papers = append(papers, p)
	}
	input.Papers = papers
	if ready < 2 {
		return input, contents, errors.New("至少两篇不同论文全文解析成功后才能生成综述")
	}
	return input, contents, nil
}
func selectReviewPapers(input ReviewWorkflowInput, ids []string) (ReviewWorkflowInput, error) {
	if len(ids) == 0 {
		return input, errors.New("请至少选择两篇全文可用论文")
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	papers := make([]WorkflowPaper, 0, len(wanted))
	seen := make(map[string]bool, len(wanted))
	for _, paper := range input.Papers {
		id := paper.Paper.PaperID
		if wanted[id] && !seen[id] {
			papers = append(papers, paper)
			seen[id] = true
		}
	}
	if len(seen) != len(wanted) {
		return input, errors.New("所选论文不属于当前扩展成果，请重新选择")
	}
	input.Papers = papers
	return input, nil
}

func launchHistoryReview(w http.ResponseWriter, r *http.Request, user string, req paperLaunch) {
	if req.ExpansionVersion != nil && *req.ExpansionVersion <= 0 {
		replyError(w, 400, "INVALID_VERSION", "select a completed expansion version")
		return
	}
	var record orm.AcademicExpansion
	if store.DB().Where("id=? AND user_id=? AND deleted_at IS NULL", req.ExpansionID, user).Take(&record).Error != nil {
		replyError(w, 404, "NOT_FOUND", "expansion not found")
		return
	}
	if !academicDataset(r, user, record.DatasetID) {
		replyError(w, 403, "FORBIDDEN", "knowledge base is unavailable")
		return
	}
	var handoff orm.AcademicHandoff
	requestConflict := errors.New("request_key belongs to a different expansion version or paper selection")
	err := conversationgroup.UserTransaction(r.Context(), store.DB(), user, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND deleted_at IS NULL", record.ID).Take(&record).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id=? AND request_key=?", user, req.RequestKey).Take(&handoff).Error; err == nil {
			if handoff.ExpansionID != record.ID || handoff.Kind != "review" || (req.ExpansionVersion != nil && handoff.Version != *req.ExpansionVersion) {
				return requestConflict
			}
			if req.PaperIDs != nil {
				var previous ReviewWorkflowInput
				if err := json.Unmarshal(handoff.InputJSON, &previous); err != nil {
					return err
				}
				selected, err := selectReviewPapers(previous, req.PaperIDs)
				if err != nil || len(selected.Papers) != len(previous.Papers) {
					return requestConflict
				}
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if record.Version == 0 {
			return errors.New("拓展尚未完成")
		}
		version := record.Version
		if req.ExpansionVersion != nil {
			version = *req.ExpansionVersion
		}
		if version > record.Version {
			return errors.New("所选拓展版本尚未完成")
		}
		snapshot := record.ResultJSON
		if version != record.Version {
			var round orm.AcademicExpansionRound
			if err := tx.Where("expansion_id=? AND version=? AND status='completed'", record.ID, version).Take(&round).Error; err != nil {
				return fmt.Errorf("所选拓展版本不可用: %w", err)
			}
			snapshot = round.ResultJSON
		}
		var input ReviewWorkflowInput
		if err := json.Unmarshal(snapshot, &input); err != nil {
			return err
		}
		if req.UserInstruction != "" {
			input.UserInstruction += "\n\n" + req.UserInstruction
		}
		if req.PaperIDs != nil {
			selected, err := selectReviewPapers(input, req.PaperIDs)
			if err != nil {
				return err
			}
			input = selected
		}
		input, contents, e := snapshotReview(r.Context(), tx, user, record.DatasetID, input, req.PaperIDs != nil)
		if e != nil {
			return e
		}
		handoff, e = createPaperHandoff(r.Context(), tx, user, record.DatasetID, record.ID, version, "review", req.RequestKey, input, contents, false)
		return e
	})
	if err != nil {
		if errors.Is(err, requestConflict) {
			replyError(w, 409, "REQUEST_CONFLICT", err.Error())
			return
		}
		replyError(w, 422, "REVIEW_NOT_READY", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]string{"conversation_id": handoff.ConversationID, "handoff_id": handoff.ID})
}

type expansionProjectionPayload struct {
	SessionID          string                   `json:"session_id"`
	CompletedArtifacts []workflowstore.Artifact `json:"completed_artifacts,omitempty"`
}

func academicArtifactJSON(raw json.RawMessage) (json.RawMessage, error) {
	value, err := workflowdocument.ReadArtifactValue(raw)
	if err != nil {
		return nil, err
	}
	return workflowdocument.ArtifactData(value), nil
}

func rejectExpansionResult(db *gorm.DB, record orm.AcademicExpansion, session orm.WorkflowSession, artifact workflowstore.Artifact, projectionErr error) (asyncjob.Result, error) {
	stale := false
	err := db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND deleted_at IS NULL", record.ID).Take(&record).Error; e != nil {
			return e
		}
		var published orm.AcademicExpansionRound
		e := tx.Where("expansion_id=? AND status='completed'", record.ID).Order("version DESC").Take(&published).Error
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e == nil {
			stale, e = publishedRoundAfter(tx, published, session, artifact)
			if e != nil {
				return e
			}
			if stale {
				return nil
			}
		}
		return tx.Model(&orm.AcademicExpansion{}).Where("id=? AND deleted_at IS NULL", record.ID).Where("EXISTS (SELECT 1 FROM plugin_sessions WHERE id=? AND state_version=? AND status='completed')", session.ID, session.StateVersion).Updates(map[string]any{"status": "failed", "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		return asyncjob.Result{}, err
	}
	if stale {
		return asyncjob.Result{}, nil
	}
	return asyncjob.Result{Permanent: true}, projectionErr
}

// Compare the runtime's session, attempt and artifact order, including timestamp ties.
func publishedRoundAfter(tx *gorm.DB, round orm.AcademicExpansionRound, session orm.WorkflowSession, artifact workflowstore.Artifact) (bool, error) {
	if round.SessionID != session.ID {
		var published orm.WorkflowSession
		if e := tx.Where("id=?", round.SessionID).Take(&published).Error; e != nil {
			return false, e
		}
		if !published.CreatedAt.Equal(session.CreatedAt) {
			return published.CreatedAt.After(session.CreatedAt), nil
		}
		return published.ID > session.ID, nil
	}
	var attempt orm.WorkflowSessionStep
	if e := tx.Where("id=?", round.AttemptID).Take(&attempt).Error; e != nil {
		return false, e
	}
	if attempt.Attempt != artifact.Attempt {
		return attempt.Attempt > artifact.Attempt, nil
	}
	var previous orm.WorkflowSlotRevision
	if e := tx.Where("id=?", round.RequestKey).Take(&previous).Error; e != nil {
		return false, e
	}
	return previous.Revision >= artifact.Revision, nil
}

func projectAcademicResult(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var record orm.AcademicExpansion
	db := store.DB().WithContext(ctx)
	if err := db.Where("id=? AND user_id=? AND deleted_at IS NULL", job.ResourceID, job.CreateUserID).Take(&record).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	var conv orm.Conversation
	if db.Where("id=? AND deleted_at IS NULL", record.ConversationID).Take(&conv).Error != nil {
		return asyncjob.Result{}, nil
	}
	var payload expansionProjectionPayload
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	var session orm.WorkflowSession
	if err := db.Where("id=? AND conversation_id=? AND create_user_id=?", payload.SessionID, conv.ID, record.UserID).Take(&session).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	artifacts, err := workflowstore.New(db).ListArtifacts(ctx, record.UserID, session.ID)
	if err != nil {
		return asyncjob.Result{}, err
	}
	progress := map[string]json.RawMessage{}
	for _, artifact := range artifacts {
		if artifact.Validity == "effective" && artifact.Selected && !artifact.Deleted {
			value, err := academicArtifactJSON(artifact.Value)
			if err != nil {
				return asyncjob.Result{}, err
			}
			progress[artifact.Slot] = value
		}
	}
	status := session.Status
	if status == "active" || status == "waiting" {
		status = "running"
	}
	var histories []orm.ChatHistory
	if e := db.Where("conversation_id=?", record.ConversationID).Order("seq ASC").Find(&histories).Error; e != nil {
		return asyncjob.Result{}, e
	}
	if len(histories) > 0 {
		var ext map[string]any
		_ = json.Unmarshal(histories[len(histories)-1].Ext, &ext)
		answered, _ := ext["ask_answered"].(bool)
		if pending := ext["ask_pending"]; pending != nil && !answered {
			status = "awaiting_input"
			progress["interaction"] = marshal(pending)
		}
	}
	if session.LastStoppedAt != nil {
		status = "stopped"
	}
	userUpdates, interactions := paperConversationInputs(histories)

	var latestSession orm.WorkflowSession
	if e := db.Where("conversation_id=?", record.ConversationID).Order("created_at DESC").Take(&latestSession).Error; e != nil {
		return asyncjob.Result{}, e
	}
	currentSession := latestSession.ID == session.ID
	if currentSession {
		if err = db.Model(&orm.AcademicExpansion{}).Where("id=? AND deleted_at IS NULL", record.ID).Where("EXISTS (SELECT 1 FROM plugin_sessions WHERE id=? AND state_version=?)", session.ID, session.StateVersion).Updates(map[string]any{"status": status, "progress_json": marshal(progress), "updated_at": time.Now().UTC()}).Error; err != nil {
			return asyncjob.Result{}, err
		}
	}
	if status == "failed" || status == "stopped" {
		var failed orm.WorkflowSessionStep
		if db.Where("session_id=? AND validity='effective'", session.ID).Order("created_at DESC").Take(&failed).Error == nil {
			now := time.Now().UTC()
			round := orm.AcademicExpansionRound{DiscoveryMode: record.DiscoveryMode, ID: uuid.NewString(), ExpansionID: record.ID, RequestKey: "failure:" + failed.ID + ":" + status, Status: status, Version: record.Version, InputJSON: record.ResultJSON, ResultJSON: marshal(progress), SessionID: session.ID, AttemptID: failed.ID, CreatedAt: failed.CreatedAt, UpdatedAt: now}
			if e := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&round).Error; e != nil {
				return asyncjob.Result{}, e
			}
		}
	}
	finalArtifacts := artifacts
	completedSnapshot := len(payload.CompletedArtifacts) > 0
	if completedSnapshot {
		finalArtifacts = payload.CompletedArtifacts
	}
	for _, artifact := range finalArtifacts {
		if !completedSnapshot && session.Status != "completed" {
			break
		}
		if artifact.Slot != "expansion_result" || artifact.Validity != "effective" || artifact.Deleted || !artifact.Selected {
			continue
		}
		var step orm.WorkflowSessionStep
		q := db.Where("session_id=? AND step_id='compose_result' AND status='succeeded'", session.ID)
		if !completedSnapshot {
			q = q.Where("validity='effective'")
		}
		if artifact.ProducerAttemptID != "" {
			q = q.Where("id=?", artifact.ProducerAttemptID)
		} else {
			q = q.Where("attempt=?", artifact.Attempt)
		}
		if q.Take(&step).Error != nil {
			continue
		}
		var input ReviewWorkflowInput
		raw, err := academicArtifactJSON(artifact.Value)
		if err != nil {
			return asyncjob.Result{}, err
		}
		var wrapped string
		if json.Unmarshal(raw, &wrapped) == nil {
			raw = []byte(wrapped)
		}
		if err = json.Unmarshal(raw, &input); err != nil {
			return rejectExpansionResult(db, record, session, artifact, err)
		}
		if len(input.SelectedDirections) == 0 || len(input.CandidateDirections) == 0 {
			return rejectExpansionResult(db, record, session, artifact, errors.New("expansion requires selected directions"))
		}
		candidates := map[string]bool{}
		for _, d := range input.CandidateDirections {
			candidates[d] = true
		}
		for _, d := range input.SelectedDirections {
			if !candidates[d] {
				return rejectExpansionResult(db, record, session, artifact, errors.New("selected direction is not a candidate"))
			}
		}
		err = conversationgroup.UserTransaction(ctx, db, record.UserID, func(tx *gorm.DB) error {
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND deleted_at IS NULL", record.ID).Take(&record).Error; e != nil {
				return e
			}
			var exists int64
			if e := tx.Model(&orm.AcademicExpansionRound{}).Where("expansion_id=? AND request_key=?", record.ID, artifact.ID).Count(&exists).Error; e != nil {
				return e
			}
			if exists > 0 {
				return nil
			}
			roundInput := input
			var newest orm.AcademicExpansionRound
			newestErr := tx.Where("expansion_id=? AND status='completed'", record.ID).Order("version DESC").Take(&newest).Error
			if newestErr != nil && !errors.Is(newestErr, gorm.ErrRecordNotFound) {
				return newestErr
			}
			if newestErr == nil {
				stale, e := publishedRoundAfter(tx, newest, session, artifact)
				if e != nil {
					return e
				}
				if stale {
					return nil
				}
			}
			merged := map[string]WorkflowPaper{}
			order := []string{}
			for _, p := range input.Papers {
				id := p.Paper.PaperID
				if id == "" {
					return errors.New("paper_id is required")
				}
				var work orm.AcademicWork
				if e := tx.Where("id=?", id).Take(&work).Error; e != nil {
					return e
				}
				source, sourceErr := expansionSource(tx, record, id)
				if sourceErr != nil {
					return fmt.Errorf("%w: %v", errUnregisteredExpansionPaper, sourceErr)
				}
				p.Paper, p.IsSeed = source.Paper, source.IsSeed

				var mark orm.AcademicUserPaperState
				if e := tx.Where("user_id=? AND dataset_id=? AND paper_id=?", record.UserID, record.DatasetID, id).Take(&mark).Error; e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
					return e
				}
				p.IsImportant = mark.IsImportant
				if old, ok := merged[id]; ok {
					p.IsSeed = p.IsSeed || old.IsSeed
				} else {
					order = append(order, id)
				}
				merged[id] = p
			}
			input.Papers = []WorkflowPaper{}
			for _, id := range order {
				input.Papers = append(input.Papers, merged[id])
			}
			if strings.TrimSpace(input.UserInstruction) == "" {
				return errors.New("user instruction missing")
			}
			input.UserInstruction = paperUserInstruction(record.Title, userUpdates)
			version := record.Version + 1
			now := time.Now().UTC()
			round := orm.AcademicExpansionRound{DiscoveryMode: record.DiscoveryMode, ID: uuid.NewString(), ExpansionID: record.ID, RequestKey: artifact.ID, Status: "completed", Version: version, InputJSON: marshal(map[string]any{"workflow_input": roundInput, "user_updates": userUpdates, "interactions": interactions}), ResultJSON: marshal(input), SessionID: session.ID, AttemptID: step.ID, CreatedAt: artifact.CreatedAt, UpdatedAt: now}
			if e := tx.Create(&round).Error; e != nil {
				return e
			}
			return tx.Model(&record).Updates(map[string]any{"version": version, "result_json": marshal(input), "updated_at": now}).Error
		})
		if err != nil {
			if errors.Is(err, errUnregisteredExpansionPaper) {
				return rejectExpansionResult(db, record, session, artifact, err)
			}
			return asyncjob.Result{}, err
		}
	}
	// Full text uses only the expansion sources verified above.
	var latest orm.AcademicExpansion
	if err = db.Where("id=? AND deleted_at IS NULL", record.ID).Take(&latest).Error; err != nil {
		return asyncjob.Result{}, err
	}
	var input ReviewWorkflowInput
	if err = json.Unmarshal(latest.ResultJSON, &input); err != nil {
		return asyncjob.Result{}, err
	}
	processing := false
	for _, paper := range input.Papers {
		source, e := expansionSource(db, latest, paper.Paper.PaperID)
		if e != nil {
			continue
		} // Legacy histories remain visible; a continued round must discover candidates again.
		content, e := ensurePaperContent(ctx, db, record.UserID, paper.Paper.PaperID, source)
		if e != nil {
			return asyncjob.Result{}, e
		}
		processing = processing || content.Status == "processing"
	}
	if latest.Version > 0 && latest.Goal == "review" && latest.AutoReviewID == "" {
		err = conversationgroup.UserTransaction(ctx, db, record.UserID, func(tx *gorm.DB) error {
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND deleted_at IS NULL", record.ID).Take(&latest).Error; e != nil {
				return e
			}
			if latest.AutoReviewID != "" {
				return nil
			}
			if e := json.Unmarshal(latest.ResultJSON, &input); e != nil {
				return e
			}
			snapshot, contents, e := snapshotReview(ctx, tx, record.UserID, record.DatasetID, input, false)
			if e != nil {
				return nil
			}
			handoff, e := createPaperHandoff(ctx, tx, record.UserID, record.DatasetID, record.ID, latest.Version, "review", "auto:"+record.ID, snapshot, contents, false)
			if e != nil {
				return e
			}
			return tx.Model(&latest).Update("auto_review_id", handoff.ID).Error
		})
		if err != nil {
			return asyncjob.Result{}, err
		}
	}
	if processing && session.Status == "completed" {
		// A short rescheduled job releases the worker while downloads/parsing run.
		_, err = asyncjob.Enqueue(ctx, db, asyncjob.EnqueueRequest{JobType: "academic.project", ResourceType: "academic_expansion", ResourceID: record.ID, CreateUserID: record.UserID, IdempotencyKey: uuid.NewString(), Payload: payload, RunAt: time.Now().Add(5 * time.Second), MaxAttempts: 3})
	}
	return asyncjob.Result{}, err
}
func ListExpansions(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	dataset := r.URL.Query().Get("dataset_id")
	if !academicDataset(r, user, dataset) {
		replyError(w, 403, "FORBIDDEN", "knowledge base unavailable")
		return
	}
	var rows []orm.AcademicExpansion
	if err := store.DB().Where("user_id=? AND dataset_id=? AND deleted_at IS NULL", user, dataset).Order("updated_at DESC").Find(&rows).Error; err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"items": rows})
}
func GetExpansion(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var record orm.AcademicExpansion
	if store.DB().Where("id=? AND user_id=? AND deleted_at IS NULL", mux.Vars(r)["expansion_id"], user).Take(&record).Error != nil {
		replyError(w, 404, "NOT_FOUND", "expansion not found")
		return
	}
	if !academicDataset(r, user, record.DatasetID) {
		replyError(w, 403, "FORBIDDEN", "knowledge base unavailable")
		return
	}
	var rounds []orm.AcademicExpansionRound
	if err := store.DB().Where("expansion_id=?", record.ID).Order("created_at").Find(&rounds).Error; err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	chatStatus := "deleted"
	var conv orm.Conversation
	if store.DB().Where("id=? AND create_user_id=?", record.ConversationID, user).Take(&conv).Error == nil {
		chatStatus = "available"
		if conv.DeletedAt != nil {
			chatStatus = "trashed"
		}
	}
	var contents []orm.AcademicPaperContent
	var input ReviewWorkflowInput
	_ = json.Unmarshal(record.ResultJSON, &input)
	ids := []string{}
	for _, p := range input.Papers {
		ids = append(ids, p.Paper.PaperID)
	}
	for _, id := range ids {
		source, e := expansionSource(store.DB(), record, id)
		if e != nil {
			continue
		}
		if content, e := findSourceContent(store.DB(), user, source); e == nil {
			contents = append(contents, content)
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			replyError(w, 500, "READ_FAILED", e.Error())
			return
		}
	}

	presence, err := presenceForWorks(r.Context(), r, user, ids, record.DatasetID, true)
	if err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	for i := range presence {
		source, e := expansionSource(store.DB(), record, presence[i].WorkID)
		if e != nil {
			continue
		}
		presence[i].Importing, err = activePaperImport(r.Context(), user, record.DatasetID, presence[i].WorkID, source.Paper.SourceVersion)
		if err != nil {
			replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
			return
		}
	}

	var handoffs []orm.AcademicHandoff
	if err := store.DB().Where("user_id=? AND expansion_id=?", user, record.ID).Order("created_at").Find(&handoffs).Error; err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	var histories []orm.ChatHistory
	if err := store.DB().Select("id", "raw_content", "ext", "seq").Where("conversation_id=?", record.ConversationID).Order("seq").Find(&histories).Error; err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	updates, interactions := paperConversationInputs(histories)
	importance := make(map[string]bool, len(ids))
	for _, id := range ids {
		importance[id] = false
	}
	if len(ids) > 0 {
		var marks []orm.AcademicUserPaperState
		if err := store.DB().Where("user_id=? AND dataset_id=? AND paper_id IN ?", user, record.DatasetID, ids).Find(&marks).Error; err != nil {
			replyError(w, 500, "READ_FAILED", err.Error())
			return
		}
		for _, mark := range marks {
			importance[mark.PaperID] = mark.IsImportant
		}
	}
	common.ReplyJSON(w, map[string]any{"record": record, "rounds": rounds, "chat_status": chatStatus, "contents": contents, "handoffs": handoffs, "presence": presence, "paper_importance": importance, "user_updates": updates, "interactions": interactions})
}
func DeleteExpansion(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !parseJSON(r, &req) || !req.Confirm {
		replyError(w, 400, "CONFIRM_REQUIRED", "confirm deletion of result and expansion Chat")
		return
	}
	err := conversationgroup.UserTransaction(r.Context(), store.DB(), user, func(tx *gorm.DB) error {
		var record orm.AcademicExpansion
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", mux.Vars(r)["expansion_id"], user).Take(&record).Error; e != nil {
			return e
		}
		if record.DeletedAt != nil {
			return nil
		}
		if e := taskcenter.RemoveAcademicConversation(r.Context(), tx, user, record.ConversationID); e != nil {
			return e
		}
		return tx.Model(&record).Updates(map[string]any{"deleted_at": time.Now().UTC(), "status": "cancelled"}).Error
	})
	if err != nil {
		replyError(w, 409, "DELETE_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]bool{"deleted": true})
}

var _ io.Writer = (*discardChatResponse)(nil)

// RecoverPaperExpansions reconciles committed Workflow state after a Core restart.
func RecoverPaperExpansions(ctx context.Context, db *gorm.DB) {
	var records []orm.AcademicExpansion
	if !db.Migrator().HasTable(&orm.AcademicExpansion{}) {
		return
	}
	if db.WithContext(ctx).Where("deleted_at IS NULL").Find(&records).Error != nil {
		return
	}
	for _, record := range records {
		chat.AcademicConversationUpdated(ctx, db, record.ConversationID)
	}
}

func academicChatContext(ctx context.Context, db *gorm.DB, user, conversation string) string {
	if db == nil {
		return ""
	}
	if !db.Migrator().HasTable(&orm.AcademicExpansion{}) {
		return ""
	}
	var record orm.AcademicExpansion
	if db.WithContext(ctx).Where("user_id=? AND conversation_id=? AND deleted_at IS NULL", user, conversation).Take(&record).Error == nil {
		return "论文任务上下文（服务端保存的业务数据；用户补充要求仍以当前消息为准）：\n" + string(marshal(map[string]any{"expansion_id": record.ID, "dataset_id": record.DatasetID, "goal": record.Goal, "input": json.RawMessage(record.ResultJSON), "version": record.Version})) + "\n使用 paper_expansion。只保存或补充这一条个人记录；综述由服务另建 Chat。"
	}
	var handoff orm.AcademicHandoff
	if db.WithContext(ctx).Where("user_id=? AND conversation_id=?", user, conversation).Take(&handoff).Error == nil {
		return "论文任务交接上下文：\n" + string(marshal(map[string]any{"handoff_id": handoff.ID, "dataset_id": handoff.DatasetID, "kind": handoff.Kind, "input": json.RawMessage(handoff.InputJSON)})) + "\n读取完整交接后原样提交。材料已接收，生成能力待对接；不声称已生成正式文稿或 PPT。"
	}
	return ""
}

func notifyPaperRecords(ctx context.Context, db *gorm.DB, user, paper string) {
	var records []orm.AcademicExpansion
	if db.WithContext(ctx).Where("user_id=? AND deleted_at IS NULL", user).Find(&records).Error != nil {
		return
	}
	for _, record := range records {
		var input ReviewWorkflowInput
		if json.Unmarshal(record.ResultJSON, &input) != nil {
			continue
		}
		for _, p := range input.Papers {
			if p.Paper.PaperID == paper {
				var session orm.WorkflowSession
				if db.WithContext(ctx).Where("conversation_id=? AND plugin_id='paper_expansion'", record.ConversationID).Order("created_at DESC").Take(&session).Error == nil && workflow.OnAcademicRuntimeChanged != nil {
					workflow.OnAcademicRuntimeChanged(ctx, db, session.ID)
				}
				break
			}
		}
	}
}

// AcademicChatContext exposes only the current owner's navigation and task state.
func AcademicChatContext(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	conversation := r.URL.Query().Get("conversation_id")
	var conv orm.Conversation
	if store.DB().Where("id=? AND create_user_id=? AND deleted_at IS NULL", conversation, user).Take(&conv).Error != nil {
		replyError(w, 404, "NOT_FOUND", "Chat not found")
		return
	}
	var record orm.AcademicExpansion
	if store.DB().Where("conversation_id=? AND user_id=? AND deleted_at IS NULL", conversation, user).Take(&record).Error == nil {
		if !academicDataset(r, user, record.DatasetID) {
			replyError(w, 403, "FORBIDDEN", "knowledge base unavailable")
			return
		}
		var input ReviewWorkflowInput
		_ = json.Unmarshal(record.ResultJSON, &input)
		ready := 0
		ids := []string{}
		for _, paper := range input.Papers {
			ids = append(ids, paper.Paper.PaperID)
		}
		for _, id := range ids {
			source, e := expansionSource(store.DB(), record, id)
			if e != nil {
				continue
			}
			if content, e := findSourceContent(store.DB(), user, source); e == nil && content.Status == "ready" {
				ready++
			}
		}

		next := ""
		if record.AutoReviewID != "" {
			var handoff orm.AcademicHandoff
			if store.DB().Where("id=? AND user_id=?", record.AutoReviewID, user).Take(&handoff).Error == nil {
				next = handoff.ConversationID
			}
		}
		common.ReplyJSON(w, map[string]any{"kind": "expansion", "expansion_id": record.ID, "dataset_id": record.DatasetID, "status": record.Status, "version": record.Version, "next_conversation_id": next, "ready_count": ready, "paper_count": len(input.Papers)})
		return
	}
	var handoff orm.AcademicHandoff
	if store.DB().Where("conversation_id=? AND user_id=?", conversation, user).Take(&handoff).Error == nil {
		if !academicDataset(r, user, handoff.DatasetID) {
			replyError(w, 403, "FORBIDDEN", "knowledge base unavailable")
			return
		}
		source := ""
		dataset := handoff.DatasetID
		available := false
		if handoff.ExpansionID != "" && store.DB().Where("id=? AND user_id=?", handoff.ExpansionID, user).Take(&record).Error == nil {
			dataset = record.DatasetID
			if record.DeletedAt == nil {
				available = true
				var sourceChat orm.Conversation
				if store.DB().Where("id=? AND create_user_id=? AND deleted_at IS NULL", record.ConversationID, user).Take(&sourceChat).Error == nil {
					source = record.ConversationID
				}
			}
		}
		common.ReplyJSON(w, map[string]any{"handoff_id": handoff.ID, "input": json.RawMessage(handoff.InputJSON), "contents": json.RawMessage(handoff.ContentsJSON), "kind": handoff.Kind, "expansion_id": handoff.ExpansionID, "dataset_id": dataset, "version": handoff.Version, "source_conversation_id": source, "source_available": available, "status": "awaiting_downstream"})
		return
	}
	replyError(w, 404, "NOT_FOUND", "paper task not found")
}
