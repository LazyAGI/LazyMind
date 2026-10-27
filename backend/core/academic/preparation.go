package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/chat"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
	"lazymind/core/workflow/graphengine"
)

const preparationJobType = "academic.prepare"

type expansionBudget struct {
	Preparation int `json:"preparation_seconds"`
	Formal      int `json:"formal_seconds"`
}

func budgetFor(mode string) (expansionBudget, bool) {
	switch mode {
	case "fast":
		return expansionBudget{120, 600}, true
	case "", "balanced":
		return expansionBudget{180, 1200}, true
	case "deep":
		return expansionBudget{300, 1800}, true
	}
	return expansionBudget{}, false
}

type preparationDirection struct {
	Name       string     `json:"name"`
	Question   string     `json:"question"`
	Reason     string     `json:"reason"`
	Difference string     `json:"difference"`
	PaperIDs   []string   `json:"paper_ids"`
	Papers     []PaperRef `json:"papers,omitempty"`
}
type preparationQuestion struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}
type preparationAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}
type preparationAttempt struct {
	Revision   int64           `json:"revision"`
	StartedAt  *time.Time      `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Model      string          `json:"model"`
	Metrics    json.RawMessage `json:"metrics,omitempty"`
	Error      string          `json:"error,omitempty"`
}
type expansionPreparation struct {
	OriginalInstruction string               `json:"original_instruction"`
	Updates             []string             `json:"updates,omitempty"`
	AnsweredQuestions   []preparationAnswer  `json:"answered_questions,omitempty"`
	Attempts            []preparationAttempt `json:"attempts,omitempty"`

	ExecutionStartedAt *time.Time           `json:"execution_started_at,omitempty"`
	ConfirmedAt        *time.Time           `json:"confirmed_at,omitempty"`
	StepStartedAt      map[string]time.Time `json:"step_started_at,omitempty"`

	UserInstruction string                 `json:"user_instruction"`
	Summary         string                 `json:"summary"`
	Questions       []preparationQuestion  `json:"questions"`
	Answers         map[string]string      `json:"answers"`
	Directions      []preparationDirection `json:"directions"`
	Selected        []string               `json:"selected_directions"`
	StartedAt       *time.Time             `json:"started_at,omitempty"`
	FinishedAt      *time.Time             `json:"finished_at,omitempty"`
	Error           string                 `json:"error,omitempty"`
	RequestKey      string                 `json:"request_key,omitempty"`
	Model           string                 `json:"model,omitempty"`
	Metrics         json.RawMessage        `json:"metrics,omitempty"`
}

func init() {
	asyncjob.Register(preparationJobType, runPreparation)
	workflow.ConfigureHostStep = configureAcademicStep
}
func enqueuePreparation(ctx context.Context, tx *gorm.DB, record orm.AcademicExpansion) error {
	_, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: preparationJobType, ResourceType: "academic_expansion", ResourceID: record.ID, CreateUserID: record.UserID, IdempotencyKey: fmt.Sprintf("%s:%d", record.ID, record.PreparationRevision), Payload: map[string]any{"revision": record.PreparationRevision}, MaxAttempts: 3})
	return err
}
func runPreparation(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	db := store.DB().WithContext(ctx)
	var record orm.AcademicExpansion
	var payload struct {
		Revision int64 `json:"revision"`
	}
	if json.Unmarshal(job.PayloadJSON, &payload) != nil {
		return asyncjob.Result{Permanent: true}, errors.New("invalid preparation revision")
	}
	if err := db.Where("id=? AND user_id=? AND deleted_at IS NULL", job.ResourceID, job.CreateUserID).Take(&record).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	if record.PreparationRevision != payload.Revision || record.Status != "preparing" {
		return asyncjob.Result{}, nil
	}
	var preparation expansionPreparation
	_ = json.Unmarshal(record.PreparationJSON, &preparation)
	now := time.Now().UTC()
	if preparation.StartedAt == nil {
		preparation.StartedAt = &now
	}
	budget, _ := budgetFor(record.BudgetMode)
	deadline := preparation.StartedAt.Add(time.Duration(budget.Preparation) * time.Second)
	if err := db.Model(&record).Where("preparation_revision=? AND status='preparing'", payload.Revision).Update("preparation_json", marshal(preparation)).Error; err != nil {
		return asyncjob.Result{}, err
	}
	result := preparation
	role, err := chat.LoadDefaultChatLLMConfig(ctx, db, record.UserID)
	if err == nil && time.Now().Before(deadline) {
		runCtx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		err = internalAcademic(runCtx, "preparation:run", map[string]any{"expansion_id": record.ID, "user_id": record.UserID, "revision": payload.Revision, "input": json.RawMessage(record.ResultJSON), "preparation": preparation, "llm_config": map[string]any{"llm": role}, "timeout_seconds": int(time.Until(deadline).Seconds())}, &result)
	} else if err == nil {
		err = errors.New("初步调研预算已用尽，可保留已有材料后重试")
	}
	// Search tools register candidates while the Agent runs. Validate against
	// that committed discovery state, fenced to this preparation revision.
	var latest orm.AcademicExpansion
	if e := db.Where("id=? AND user_id=? AND preparation_revision=? AND status='preparing' AND deleted_at IS NULL", record.ID, record.UserID, payload.Revision).Take(&latest).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return asyncjob.Result{}, nil
		}
		return asyncjob.Result{}, e
	}
	record = latest
	status := "awaiting_confirmation"
	if err != nil {
		status = "preparation_failed"
		result = preparation
		result.Error = "初步调研未完成，请检查模型或检索配置后重试"
		if ctx.Err() != nil {
			return asyncjob.Result{}, ctx.Err()
		}
	}
	if err == nil {
		if e := validatePreparation(db, record, result); e != nil {
			status = "preparation_failed"
			result = preparation
			result.Error = e.Error()
		} else if len(result.Questions) > 0 {
			status = "awaiting_info"
		}
	}
	finished := time.Now().UTC()
	result.StartedAt = preparation.StartedAt
	result.FinishedAt = &finished
	for i := range result.Directions {
		result.Directions[i].Papers = nil
		for _, id := range result.Directions[i].PaperIDs {
			if source, e := expansionSource(db, record, id); e == nil {
				result.Directions[i].Papers = append(result.Directions[i].Papers, source.Paper)
			}
		}
	}
	result.Answers = preparation.Answers
	result.RequestKey = preparation.RequestKey
	if name, ok := role["model"].(string); ok {
		result.Model = name
	}
	result.Attempts = append(preparation.Attempts, preparationAttempt{Revision: payload.Revision, StartedAt: preparation.StartedAt, FinishedAt: finished, Model: result.Model, Metrics: result.Metrics, Error: result.Error})
	saveErr := conversationgroup.UserTransaction(ctx, db, record.UserID, func(tx *gorm.DB) error {
		update := tx.Model(&orm.AcademicExpansion{}).Where("id=? AND preparation_revision=? AND status='preparing' AND deleted_at IS NULL", record.ID, payload.Revision).Updates(map[string]any{"preparation_json": marshal(result), "status": status, "updated_at": finished})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return nil
		}
		return taskcenter.NotifyAcademicExpansion(ctx, tx, record.UserID, record.DatasetID, record.ID, payload.Revision, status)
	})
	return asyncjob.Result{Permanent: status == "preparation_failed"}, saveErr
}
func validatePreparation(db *gorm.DB, record orm.AcademicExpansion, p expansionPreparation) error {
	if strings.TrimSpace(p.Summary) == "" {
		return errors.New("调研摘要为空，请重试")
	}
	seen := map[string]bool{}
	for _, q := range p.Questions {
		if q.ID == "" || strings.TrimSpace(q.Question) == "" || seen[q.ID] {
			return errors.New("调研问题无效")
		}
		seen[q.ID] = true
	}
	if len(p.Questions) > 0 {
		return nil
	}
	if len(p.Directions) == 0 {
		return errors.New("尚未得到有依据的研究方向，请补充范围后重试")
	}
	seen = map[string]bool{}
	for _, d := range p.Directions {
		if strings.TrimSpace(d.Name) == "" || seen[d.Name] || d.Reason == "" || len(d.PaperIDs) == 0 {
			return errors.New("研究方向缺少名称、依据或代表论文")
		}
		seen[d.Name] = true
		for _, id := range d.PaperIDs {
			if _, err := expansionSource(db, record, id); err != nil {
				return errors.New("代表论文尚未登记于本次调研")
			}
		}
	}
	for _, name := range p.Selected {
		if !seen[name] {
			return errors.New("预选方向不在调研范围内")
		}
	}
	return nil
}

func SubmitPreparation(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	record, ok := ownedExpansion(w, r, user, mux.Vars(r)["expansion_id"])
	if !ok {
		return
	}
	var req struct {
		Action      string            `json:"action"`
		Revision    int64             `json:"revision"`
		RequestKey  string            `json:"request_key"`
		Answers     map[string]string `json:"answers"`
		Instruction string            `json:"user_instruction"`
		BudgetMode  string            `json:"budget_mode"`
	}
	if !parseJSON(r, &req) || req.RequestKey == "" || len(req.RequestKey) > 128 || (req.Action != "answer" && req.Action != "revise" && req.Action != "retry") {
		replyError(w, 400, "INVALID_REQUEST", "action, revision and request_key are required")
		return
	}
	if _, ok := budgetFor(req.BudgetMode); !ok {
		replyError(w, 400, "INVALID_REQUEST", "invalid budget mode")
		return
	}
	err := conversationgroup.UserTransaction(r.Context(), store.DB(), user, func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND deleted_at IS NULL", record.ID).Take(&record).Error; e != nil {
			return e
		}
		var p expansionPreparation
		_ = json.Unmarshal(record.PreparationJSON, &p)
		if p.RequestKey == req.RequestKey {
			return nil
		}
		if record.PreparationRevision != req.Revision {
			return errors.New("准备内容已更新，请刷新后重试")
		}
		if record.Status == "preparing" || record.Status == "running" || record.Status == "pending" {
			return errors.New("任务正在执行，请等待当前阶段结束")
		}
		if req.Action == "answer" {
			if record.Status != "awaiting_info" {
				return errors.New("当前没有待回答问题")
			}
			if p.Answers == nil {
				p.Answers = map[string]string{}
			}
			for _, q := range p.Questions {
				answer := strings.TrimSpace(req.Answers[q.ID])
				if answer == "" {
					return errors.New("请回答当前待补充问题")
				}
				p.Answers[q.ID] = answer
				p.AnsweredQuestions = append(p.AnsweredQuestions, preparationAnswer{q.Question, answer})
			}
		}
		if req.Action == "revise" && strings.TrimSpace(req.Instruction) == "" {
			return errors.New("请填写新的研究要求")
		}
		if strings.TrimSpace(req.Instruction) != "" {
			p.UserInstruction = strings.TrimSpace(req.Instruction)
			p.Updates = append(p.Updates, p.UserInstruction)
		}
		p.StartedAt = nil
		p.FinishedAt = nil
		p.Error = ""
		p.RequestKey = req.RequestKey
		p.Questions = nil
		p.Selected = nil
		if e := taskcenter.ResolveAcademicExpansionNotifications(r.Context(), tx, record.ID, record.PreparationRevision); e != nil {
			return e
		}
		if record.ConversationID != "" {
			if _, e := asyncjob.CancelResourceJobs(r.Context(), tx, ChatLaunchJobType, "conversation", record.ConversationID, "research scope revised"); e != nil {
				return e
			}
		}
		record.PreparationRevision++
		record.PreparationJSON = marshal(p)
		record.Status = "preparing"
		if req.BudgetMode != "" {
			record.BudgetMode = req.BudgetMode
		}
		if e := tx.Model(&record).Updates(map[string]any{"preparation_revision": record.PreparationRevision, "preparation_json": record.PreparationJSON, "status": record.Status, "budget_mode": record.BudgetMode, "updated_at": time.Now().UTC()}).Error; e != nil {
			return e
		}
		return enqueuePreparation(r.Context(), tx, record)
	})
	if err != nil {
		replyError(w, 409, "PREPARATION_CONFLICT", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"expansion_id": record.ID, "status": record.Status, "revision": record.PreparationRevision})
}
func ConfirmExpansion(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	record, ok := ownedExpansion(w, r, user, mux.Vars(r)["expansion_id"])
	if !ok {
		return
	}
	var req struct {
		Revision   int64    `json:"revision"`
		RequestKey string   `json:"request_key"`
		Selected   []string `json:"selected_directions"`
		BudgetMode string   `json:"budget_mode"`
	}
	if !parseJSON(r, &req) || req.RequestKey == "" || len(req.RequestKey) > 128 || len(req.Selected) == 0 {
		replyError(w, 400, "INVALID_REQUEST", "revision, request_key and selected_directions required")
		return
	}
	if _, ok := budgetFor(req.BudgetMode); !ok {
		replyError(w, 400, "INVALID_REQUEST", "invalid budget mode")
		return
	}
	err := conversationgroup.UserTransaction(r.Context(), store.DB(), user, func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND deleted_at IS NULL", record.ID).Take(&record).Error; e != nil {
			return e
		}
		if record.PreparationRevision != req.Revision {
			return errors.New("准备内容已更新，请重新确认")
		}
		var p expansionPreparation
		_ = json.Unmarshal(record.PreparationJSON, &p)
		if p.RequestKey == "confirm:"+req.RequestKey && record.ConversationID != "" {
			return nil
		}
		if record.Status != "awaiting_confirmation" {
			return errors.New("请先完成初步调研和待补充问题")
		}
		if e := validatePreparation(tx, record, p); e != nil {
			return e
		}
		choices := map[string]bool{}
		for _, d := range p.Directions {
			choices[d.Name] = true
		}
		seen := map[string]bool{}
		for _, d := range req.Selected {
			if !choices[d] || seen[d] {
				return errors.New("请选择已调研的方向；新方向需先补充调研")
			}
			seen[d] = true
		}
		var input ReviewWorkflowInput
		if e := json.Unmarshal(record.ResultJSON, &input); e != nil {
			return e
		}
		input.UserInstruction = p.UserInstruction
		for _, answer := range p.AnsweredQuestions {
			input.UserInstruction += "\n补充：" + answer.Question + "：" + answer.Answer
		}
		input.CandidateDirections = []string{}
		for _, d := range p.Directions {
			input.CandidateDirections = append(input.CandidateDirections, d.Name)
		}
		input.SelectedDirections = req.Selected
		p.Selected = req.Selected
		p.ExecutionStartedAt = nil
		p.StepStartedAt = nil
		confirmed := time.Now().UTC()
		p.ConfirmedAt = &confirmed
		if e := taskcenter.ResolveAcademicExpansionNotifications(r.Context(), tx, record.ID, record.PreparationRevision); e != nil {
			return e
		}
		p.RequestKey = "confirm:" + req.RequestKey
		if record.ConversationID == "" {
			id, e := newAcademicConversation(tx, user, record.DatasetID, "论文扩展："+truncateTitle(record.Title), "expansion")
			if e != nil {
				return e
			}
			record.ConversationID = id
		}
		if req.BudgetMode != "" {
			record.BudgetMode = req.BudgetMode
		}
		record.Status = "pending"
		if e := tx.Model(&record).Updates(map[string]any{"conversation_id": record.ConversationID, "status": record.Status, "result_json": marshal(input), "preparation_json": marshal(p), "budget_mode": record.BudgetMode, "updated_at": time.Now().UTC()}).Error; e != nil {
			return e
		}
		return enqueueAcademicChat(r.Context(), tx, user, record.ConversationID, "paper_expansion", "请按照已确认范围执行四步论文拓展。准备成果已保存，请先读取任务上下文。", fmt.Sprintf("confirm:%d:%s", req.Revision, req.RequestKey))
	})
	if err != nil {
		replyError(w, 409, "CONFIRM_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"expansion_id": record.ID, "conversation_id": record.ConversationID, "status": record.Status})
}
func ExpansionCapabilities(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	db := store.DB()
	academic, err := modelconfig.LoadAcademicSearchToolConfig(r.Context(), db, user)
	if err != nil {
		replyError(w, 500, "CONFIG_UNAVAILABLE", "无法读取学术搜索配置")
		return
	}
	web, err := modelconfig.LoadSearchToolConfig(r.Context(), db, user)
	if err != nil {
		replyError(w, 500, "CONFIG_UNAVAILABLE", "无法读取通用搜索配置")
		return
	}
	key, err := modelconfig.LoadOpenAlexAPIKey(r.Context(), db, user)
	if err != nil {
		replyError(w, 500, "CONFIG_UNAVAILABLE", "无法读取 OpenAlex 配置")
		return
	}
	common.ReplyJSON(w, map[string]any{"openalex_key": key != "", "sciverse": academic["sciverse"] != nil, "web_search": len(web) > 0, "budgets": map[string]expansionBudget{"fast": {120, 600}, "balanced": {180, 1200}, "deep": {300, 1800}}})
}

func configureAcademicStep(ctx context.Context, db *gorm.DB, user, conversation string, params *workflow.WorkflowStepParams) error {
	enabled := false
	for _, name := range params.Runtime.HostExtensions {
		if name == "academic-paper-chat-v1" {
			enabled = true
		}
	}
	if !enabled || !db.Migrator().HasTable(&orm.AcademicExpansion{}) {
		return nil
	}
	var record orm.AcademicExpansion
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=? AND user_id=? AND deleted_at IS NULL", conversation, user).Take(&record).Error; err != nil {
		return err
	}
	if record.Status != "pending" && record.Status != "running" && record.Status != "completed" && record.Status != "failed" && record.Status != "stopped" {
		return errors.New("请在拓展记录中确认方向后开始正式拓展")
	}
	var preparation expansionPreparation
	if err := json.Unmarshal(record.PreparationJSON, &preparation); err != nil {
		return err
	}
	if len(preparation.Selected) == 0 {
		return errors.New("研究范围尚未确认")
	}
	now := time.Now().UTC()
	if preparation.ExecutionStartedAt == nil || record.Status == "completed" {
		preparation.ExecutionStartedAt = &now
		preparation.StepStartedAt = map[string]time.Time{}
	}
	if preparation.StepStartedAt == nil {
		preparation.StepStartedAt = map[string]time.Time{}
	}
	start, ok := preparation.StepStartedAt[params.StepID]
	if !ok {
		start = now
		preparation.StepStartedAt[params.StepID] = start
	}
	budget, _ := budgetFor(record.BudgetMode)
	fraction := map[string]int{"prepare_plan": 10, "expand_and_screen": 40, "analyze_relations": 40, "compose_result": 10}[params.StepID]
	if fraction == 0 {
		return errors.New("unknown paper research step")
	}
	seconds := int(time.Until(start.Add(time.Duration(budget.Formal*fraction/100) * time.Second)).Seconds())
	total := int(time.Until(preparation.ExecutionStartedAt.Add(time.Duration(budget.Formal) * time.Second)).Seconds())
	if seconds > total {
		seconds = total
	}
	if seconds < 1 {
		return errors.New("本轮执行预算已用尽，请在拓展记录中重试并确认预算")
	}
	limits := map[string]graphengine.ExecutionLimit{}
	for key, value := range params.Runtime.ExecutionLimits {
		limits[key] = value
	}
	limit := limits[params.StepID]
	limit.Timeout = seconds
	limits[params.StepID] = limit
	params.Runtime.ExecutionLimits = limits
	return db.Model(&record).Updates(map[string]any{"preparation_json": marshal(preparation), "status": "running"}).Error
}

func expansionContentDeadline(record orm.AcademicExpansion) *time.Time {
	var p expansionPreparation
	_ = json.Unmarshal(record.PreparationJSON, &p)
	budget, _ := budgetFor(record.BudgetMode)
	if record.Status == "preparing" && p.StartedAt != nil {
		deadline := p.StartedAt.Add(time.Duration(budget.Preparation) * time.Second)
		return &deadline
	}
	if p.ExecutionStartedAt != nil {
		deadline := p.ExecutionStartedAt.Add(time.Duration(budget.Formal) * time.Second)
		return &deadline
	}
	return nil
}
