package workflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

type ordinaryProjection struct {
	SchemaVersion int                            `json:"schema_version"`
	SessionID     string                         `json:"session_id"`
	Status        string                         `json:"status"`
	Revision      int64                          `json:"revision"`
	Cursor        int64                          `json:"cursor"`
	Tasks         []taskdisplay.OrdinaryTaskView `json:"tasks"`
	Runs          []taskdisplay.OrdinaryRunView  `json:"runs"`
}

func projectOrdinarySession(ctx context.Context, db *gorm.DB, session *orm.WorkflowSession) (ordinaryProjection, error) {
	out := ordinaryProjection{SchemaVersion: taskdisplay.SchemaVersion, SessionID: session.ID, Status: session.Status, Tasks: []taskdisplay.OrdinaryTaskView{}, Runs: []taskdisplay.OrdinaryRunView{}}
	graph, err := loadSessionGraph(ctx, db, session)
	if err != nil {
		return out, err
	}
	runtime, err := loadRuntimeSnapshot(ctx, db, session.ID)
	if err != nil {
		return out, err
	}
	projected := projectSessionWithApprovalPreferences(db.WithContext(ctx), *session, graph, runtime)
	var events []orm.WorkflowEvent
	if err := db.WithContext(ctx).Where("session_id = ?", session.ID).Order("id ASC").Find(&events).Error; err != nil {
		return out, err
	}
	if len(events) > 0 {
		out.Revision = events[len(events)-1].ID
	}
	out.Cursor = out.Revision
	var attempts []orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("session_id = ?", session.ID).Order("attempt ASC, created_at ASC, id ASC").Find(&attempts).Error; err != nil {
		return out, err
	}
	current := map[string]orm.WorkflowSessionStep{}
	for _, row := range attempts {
		current[row.StepID] = row
	}
	groups, err := ordinaryParallelGroups(ctx, db, session.ID)
	if err != nil {
		return out, err
	}
	order := append([]string{}, graph.StaticOrder...)
	seen := map[string]bool{}
	for _, id := range order {
		seen[id] = true
	}
	missing := []string{}
	for id := range graph.Nodes {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	order = append(order, missing...)
	finalSlots := map[string]bool{}
	for _, edge := range projected.Edges {
		if edge.To == "__end__" && edge.State == "active" {
			for _, slot := range graph.Nodes[edge.From].Outputs {
				finalSlots[slot] = true
			}
		}
	}
	published := map[string]bool{}
	if db.Migrator().HasTable(&orm.DocumentPublicationBinding{}) {
		var bindings []orm.DocumentPublicationBinding
		if err := db.WithContext(ctx).Where("session_id = ? AND owner_user_id = ?", session.ID, session.CreateUserID).Find(&bindings).Error; err != nil {
			return out, err
		}
		for _, binding := range bindings {
			if binding.ResultRevisionID != "" {
				published[binding.ResultRevisionID] = true
			}
		}
	}
	run := taskdisplay.OrdinaryRunView{RunID: session.ID, Revision: out.Revision, FinalOutputRefs: []string{}, FinalArtifacts: []taskdisplay.PublicArtifact{}}
	for index, stepID := range order {
		if stepID == "__start__" || stepID == "__end__" {
			continue
		}
		node := graph.Nodes[stepID]
		view := taskdisplay.NewTask()
		row, exists := current[stepID]
		if exists && row.TaskID != "" {
			var task orm.SubAgentTask
			if err := db.WithContext(ctx).Where("id = ? AND conversation_id = ?", row.TaskID, session.ConversationID).First(&task).Error; err == nil {
				view, err = subagent.OrdinaryTask(ctx, db, &task)
				if err != nil {
					return out, err
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return out, err
			}
		}
		view.DisplayKey = "workflow:" + session.ID + ":" + stepID + ":pending"
		view.RunID, view.ConversationID, view.TriggerHistoryID = session.ID, session.ConversationID, session.TriggerHistoryID
		view.SessionID, view.WorkflowStepID = taskdisplay.String(session.ID), taskdisplay.String(stepID)
		view.AgentType, view.Order, view.Revision = "workflow_step", index, out.Revision
		view.Title = taskdisplay.Text(node.Label, 100)
		if view.Title == "" {
			view.Title = fmt.Sprintf("子任务 %d", index+1)
		}
		view.Status = "pending"
		if publicNode, ok := projected.Nodes[stepID]; ok && !exists {
			switch {
			case publicNode.Branch == "pruned" || publicNode.Branch == "bypassed":
				view.Status = publicNode.Branch
			case publicNode.Readiness == "blocked":
				view.Status = "blocked"
			case publicNode.RequiresApproval && publicNode.Readiness == "ready":
				view.Status = "waiting"
			}
		}
		if exists {
			view.DisplayKey = "workflow:" + session.ID + ":" + stepID + ":" + row.ID
			// Hosted attempts retain an adapter identifier in task_id without a
			// SubAgent row. Only expose task_id when the adapter actually exists.
			view.AttemptID, view.ExecutionID = taskdisplay.String(row.ID), row.ID
			view.Status = ordinaryAttemptStatus(row)
			view.ParallelGroupID = taskdisplay.String(groups[row.TaskID])
			applyOrdinaryAttemptEvents(&view, row, events)
			// Slot revisions establish explicit visibility and attempt ownership for
			// hosted outputs too. Do not promote unrelated task artifacts to slots.
			artifacts, err := ordinaryAttemptArtifacts(ctx, db, session.ID, row, graph, view.DisplayKey)
			if err != nil {
				return out, err
			}
			// A bound SubAgent artifact and its slot revision reference the same
			// saved output. Use the slot identity once so final refs resolve too.
			bound := map[string]bool{}
			for _, artifact := range artifacts {
				if artifact.sourceID != "" {
					bound[artifact.sourceID] = true
				}
			}
			unbound := view.StageArtifacts[:0]
			for _, artifact := range view.StageArtifacts {
				if !bound[strings.Split(artifact.ArtifactID, ":")[0]] {
					unbound = append(unbound, artifact)
				}
			}
			view.StageArtifacts = unbound
			for _, artifact := range artifacts {
				view.StageArtifacts = append(view.StageArtifacts, artifact.item)
				if artifact.selected && row.Validity != "stale" && ((row.Status == "succeeded" && finalSlots[artifact.slot]) || published[artifact.revisionID]) {
					run.FinalOutputRefs = append(run.FinalOutputRefs, artifact.item.ArtifactID)
					run.FinalArtifacts = append(run.FinalArtifacts, artifact.item)
				}
			}
		}
		for i := range view.StageArtifacts {
			view.StageArtifacts[i].ProducerDisplayKey = view.DisplayKey
		}
		view.StageArtifacts = deduplicateOrdinaryArtifacts(view.StageArtifacts)
		out.Tasks = append(out.Tasks, view)
	}
	out.Runs = append(out.Runs, run)
	return out, nil
}

func ordinaryAttemptStatus(row orm.WorkflowSessionStep) string {
	if row.Validity != "" && row.Validity != "effective" {
		return row.Validity
	}
	switch row.Status {
	case "queued", "claimed":
		return "pending"
	case "cancelled":
		return "canceled"
	}
	return row.Status
}

func applyOrdinaryAttemptEvents(view *taskdisplay.OrdinaryTaskView, row orm.WorkflowSessionStep, events []orm.WorkflowEvent) {
	steps := map[string]taskdisplay.PublicProcessStep{}
	for _, step := range view.ProcessSteps {
		steps[step.StepID] = step
	}
	for _, event := range events {
		if event.EntityID != row.ID {
			continue
		}
		switch event.EventType {
		case "attempt.public_display":
			var display attempt.PublicDisplay
			if json.Unmarshal(event.PayloadJSON, &display) != nil || display.SchemaVersion != taskdisplay.SchemaVersion {
				continue
			}
			for _, step := range display.ProcessSteps {
				if taskdisplay.ValidateProcessStep(step) != nil {
					continue
				}
				previous, exists := steps[step.StepID]
				if !exists || step.Revision > previous.Revision {
					steps[step.StepID] = step
				}
			}
			if len(display.Sources) > 0 {
				view.Sources = taskdisplay.NormalizeSources(display.Sources)
			}
		case "attempt.patch", "attempt.progress":
			var patch struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(event.PayloadJSON, &patch)
			if event.EventType == "attempt.progress" {
				patch.Status = "running"
			}
			if patch.Status == "running" && view.Timing.StartedAt == nil {
				at := event.CreatedAt.UTC()
				view.Timing.StartedAt = &at
			}
			if taskdisplay.Terminal(patch.Status) && view.Timing.FinishedAt == nil {
				at := event.CreatedAt.UTC()
				view.Timing.FinishedAt = &at
			}
		}
	}
	view.ProcessSteps = []taskdisplay.PublicProcessStep{}
	for _, step := range steps {
		view.ProcessSteps = append(view.ProcessSteps, step)
	}
	sort.Slice(view.ProcessSteps, func(i, j int) bool {
		if view.ProcessSteps[i].Order == view.ProcessSteps[j].Order {
			return view.ProcessSteps[i].StepID < view.ProcessSteps[j].StepID
		}
		return view.ProcessSteps[i].Order < view.ProcessSteps[j].Order
	})
	if len(view.ProcessSteps) > 0 {
		view.ProcessState = "available"
	}
	if view.Timing.StartedAt != nil {
		end := view.Timing.MeasuredAt
		if view.Timing.FinishedAt != nil {
			end = *view.Timing.FinishedAt
		} else if taskdisplay.Terminal(row.Status) {
			view.Timing.ExecutionElapsedMS = nil
			return
		}
		if !end.Before(*view.Timing.StartedAt) {
			elapsed := end.Sub(*view.Timing.StartedAt).Milliseconds()
			view.Timing.ExecutionElapsedMS = &elapsed
		}
	}
}

func ordinaryParallelGroups(ctx context.Context, db *gorm.DB, sessionID string) (map[string]string, error) {
	groups := map[string]string{}
	var commands []orm.WorkflowTransitionCommand
	if !db.Migrator().HasTable(&orm.WorkflowTransitionCommand{}) {
		return groups, nil
	}
	if err := db.WithContext(ctx).Where("session_id = ? AND status = ?", sessionID, "accepted").Order("created_at ASC").Find(&commands).Error; err != nil {
		return nil, err
	}
	for _, command := range commands {
		var response transitionCommandResponse
		if json.Unmarshal(command.ResponseJSON, &response) != nil || !response.Accepted || len(response.Tasks) < 2 {
			continue
		}
		for _, task := range response.Tasks {
			if task.TaskID != "" {
				groups[task.TaskID] = command.CommandID
			}
		}
	}
	return groups, nil
}

type ordinarySlotArtifact struct {
	item       taskdisplay.PublicArtifact
	slot       string
	selected   bool
	sourceID   string
	revisionID string
}

func ordinaryAttemptArtifacts(ctx context.Context, db *gorm.DB, sessionID string, row orm.WorkflowSessionStep, graph *graphengine.CompiledStateGraph, displayKey string) ([]ordinarySlotArtifact, error) {
	var revisions []orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).Where("session_id = ? AND (producer_attempt_id = ? OR ((producer_attempt_id = '' OR producer_attempt_id IS NULL) AND step_id = ? AND attempt = ?)) AND validity IN ?", sessionID, row.ID, row.StepID, row.Attempt, []string{"", "effective"}).Order("created_at ASC, id ASC").Find(&revisions).Error; err != nil {
		return nil, err
	}
	result := []ordinarySlotArtifact{}
	for _, revision := range revisions {
		value, err := LoadSlotRevisionValue(ctx, db, revision)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		contentType := graph.MaterialTypes[revision.SlotID]
		sourceID := ""
		if contentType == "" {
			contentType = "json"
		}
		if revision.HumanArtifactID != nil {
			var human orm.WorkflowHumanArtifact
			if err := db.WithContext(ctx).Where("id = ?", *revision.HumanArtifactID).First(&human).Error; err != nil {
				return nil, err
			}
			contentType = human.ContentType
		}
		if revision.ArtifactSeq != nil && row.TaskID != "" {
			var original orm.SubAgentArtifact
			if err := db.WithContext(ctx).Where("task_id = ? AND slot = ? AND seq = ? AND hidden = ?", row.TaskID, revision.Slot, *revision.ArtifactSeq, false).First(&original).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return nil, err
			}
			contentType = original.ContentType
			sourceID = original.ID
		}
		adapted := subagent.OrdinaryArtifacts([]orm.SubAgentArtifact{{ID: revision.ID, Slot: revision.SlotID, ContentType: contentType, Value: value, CreatedAt: revision.CreatedAt}}, "", displayKey)
		for _, artifact := range adapted {
			artifact.Revision = int64(revision.Revision)
			result = append(result, ordinarySlotArtifact{artifact, revision.SlotID, revision.Selected, sourceID, revision.ID})
		}
	}
	return result, nil
}

func deduplicateOrdinaryArtifacts(items []taskdisplay.PublicArtifact) []taskdisplay.PublicArtifact {
	result := []taskdisplay.PublicArtifact{}
	seen := map[string]bool{}
	for _, item := range items {
		if !seen[item.ArtifactID] {
			seen[item.ArtifactID] = true
			result = append(result, item)
		}
	}
	return result
}

type ordinaryPageCursor struct {
	Key        string `json:"k"`
	Collection string `json:"c"`
	Revision   int64  `json:"r"`
	Offset     int    `json:"o"`
}

var errOrdinaryCursor = errors.New("invalid ordinary cursor")

func paginateOrdinaryProjection(out *ordinaryProjection, r *http.Request) error {
	q := r.URL.Query()
	limit := taskdisplay.PageLimit
	if raw := q.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > taskdisplay.PageLimit {
			return errOrdinaryCursor
		}
		limit = parsed
	}
	var cursor ordinaryPageCursor
	if raw := q.Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Key != q.Get("display_key") || cursor.Collection != q.Get("collection") || cursor.Revision != out.Revision || cursor.Offset < 0 {
			return errOrdinaryCursor
		}
	}
	selected := q.Get("display_key")
	found := selected == ""
	for i := range out.Tasks {
		view := &out.Tasks[i]
		if view.DisplayKey == selected {
			found = true
		}
		page := func(collection string, total int) (int, int, taskdisplay.CollectionPage, error) {
			start := 0
			if view.DisplayKey == selected && collection == q.Get("collection") {
				start = cursor.Offset
			}
			if start > total {
				return 0, 0, taskdisplay.CollectionPage{}, errOrdinaryCursor
			}
			end := min(total, start+limit)
			metadata := taskdisplay.CollectionPage{Total: total, Revision: out.Revision}
			if end < total {
				encoded, _ := json.Marshal(ordinaryPageCursor{view.DisplayKey, collection, out.Revision, end})
				next := base64.RawURLEncoding.EncodeToString(encoded)
				metadata.NextCursor = &next
			}
			return start, end, metadata, nil
		}
		start, end, metadata, err := page("process_steps", len(view.ProcessSteps))
		if err != nil {
			return err
		}
		view.ProcessSteps, view.Pages.ProcessSteps = view.ProcessSteps[start:end], metadata
		start, end, metadata, err = page("sources", len(view.Sources))
		if err != nil {
			return err
		}
		view.Sources, view.Pages.Sources = view.Sources[start:end], metadata
		start, end, metadata, err = page("stage_artifacts", len(view.StageArtifacts))
		if err != nil {
			return err
		}
		view.StageArtifacts, view.Pages.StageArtifacts = view.StageArtifacts[start:end], metadata
	}
	if !found {
		return errOrdinaryCursor
	}
	if collection := q.Get("collection"); collection != "" && collection != "process_steps" && collection != "sources" && collection != "stage_artifacts" {
		return errOrdinaryCursor
	}
	return nil
}

func getOrdinarySessionProjection(w http.ResponseWriter, r *http.Request) {
	requestID := taskdisplay.PrepareRequest(w, r)
	started := time.Now()
	defer func() { taskdisplay.Observe(r.Context(), taskdisplay.EventSnapshot, time.Since(started), 1) }()
	owner, sessionID := strings.TrimSpace(r.Header.Get("X-User-Id")), common.PathVar(r, "session_id")
	if owner == "" {
		taskdisplay.ReplyError(w, r, "workflow session unavailable", http.StatusNotFound)
		return
	}
	var result ordinaryProjection
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := workflowstore.New(tx).AuthorizeSession(r.Context(), sessionID, owner); err != nil {
			return gorm.ErrRecordNotFound
		}
		var session orm.WorkflowSession
		if err := tx.Where("id = ? AND dismissed = false", sessionID).First(&session).Error; err != nil {
			return err
		}
		var err error
		result, err = projectOrdinarySession(r.Context(), tx, &session)
		if err != nil {
			return err
		}
		return paginateOrdinaryProjection(&result, r)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		taskdisplay.ReplyError(w, r, "workflow session unavailable", http.StatusNotFound)
		return
	}
	if errors.Is(err, errOrdinaryCursor) {
		taskdisplay.Observe(r.Context(), taskdisplay.EventResync, 0, 1)
		common.ReplyErrWithData(w, "Please reload task details", map[string]any{"code": "PUBLIC_CURSOR_EXPIRED", "request_id": requestID}, http.StatusConflict)
		return
	}
	if err != nil {
		taskdisplay.Observe(r.Context(), taskdisplay.EventRejected, 0, 1)
		common.ReplyErrWithData(w, "Unable to load task details", map[string]any{"code": "PUBLIC_PROJECTION_UNAVAILABLE", "request_id": requestID}, http.StatusServiceUnavailable)
		return
	}
	missing := 0
	for _, task := range result.Tasks {
		if task.AttemptID != nil && task.ProcessState == "not_provided" {
			missing++
		}
	}
	if missing > 0 {
		taskdisplay.Observe(r.Context(), taskdisplay.EventMissingProcess, 0, missing)
	}
	common.ReplyOK(w, result)
}
