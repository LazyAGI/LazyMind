package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
	"lazymind/core/doc"
)

// OrdinaryTask never loads raw reasoning/tool history. The complete public
// projection is shared by REST and SSE; pagination is applied at the boundary.
func OrdinaryTask(ctx context.Context, db *gorm.DB, task *orm.SubAgentTask) (taskdisplay.OrdinaryTaskView, error) {
	out := taskdisplay.NewTask()
	out.TaskID = taskdisplay.String(task.ID)
	out.ExecutionID = task.ExecutionID
	if out.ExecutionID == "" {
		out.ExecutionID = "legacy"
	}
	out.DisplayKey = "task:" + task.ID + ":" + out.ExecutionID
	out.ConversationID = task.ConversationID
	out.TriggerHistoryID = task.TriggerHistoryID
	out.AgentType = task.AgentType
	out.RunID = task.TriggerHistoryID
	if out.RunID == "" {
		out.RunID = "task:" + task.ID
	}
	out.Order = task.SeqInConversation
	out.Title = taskdisplay.Text(task.Title, 100)
	if out.Title == "" || strings.Contains(out.Title, "-workflow:") {
		out.Title = fmt.Sprintf("子任务 %d", task.SeqInConversation)
	}
	out.Status = task.Status
	if task.Status == StatusFailed {
		out.CapabilityDependency = taskdisplay.CapabilityRecovery(task.Summary)
	}
	progress := min(100, max(0, task.ProgressPct))
	out.ProgressPct = &progress
	out.Revision = task.DisplayRevision
	out.Sources = taskdisplay.NormalizeSources(json.RawMessage(task.Sources))
	plan, err := ordinaryPlan(ctx, db, task)
	if err != nil {
		return out, err
	}
	out.PlanSteps = plan
	var rows []orm.SubAgentStep
	if err := db.WithContext(ctx).Where("task_id = ? AND execution_id = ? AND role = ?", task.ID, task.ExecutionID, "process_step").Order("seq ASC").Find(&rows).Error; err != nil {
		return out, err
	}
	steps := map[string]taskdisplay.PublicProcessStep{}
	for _, row := range rows {
		var record publicStepRecord
		if json.Unmarshal(row.Content, &record) == nil && taskdisplay.ValidateProcessStep(record.Step) == nil {
			previous, ok := steps[record.Step.StepID]
			if !ok || record.Step.Revision > previous.Revision {
				steps[record.Step.StepID] = record.Step
			}
		}
	}
	for _, step := range steps {
		out.ProcessSteps = append(out.ProcessSteps, step)
	}
	sort.Slice(out.ProcessSteps, func(i, j int) bool {
		a, b := out.ProcessSteps[i], out.ProcessSteps[j]
		if a.Order == b.Order {
			return a.StepID < b.StepID
		}
		return a.Order < b.Order
	})
	if len(out.ProcessSteps) > 0 {
		out.ProcessState = "available"
	}
	var artifacts []orm.SubAgentArtifact
	if err := db.WithContext(ctx).Where("task_id = ? AND execution_id = ? AND hidden = ?", task.ID, task.ExecutionID, false).Order("created_at ASC, id ASC").Find(&artifacts).Error; err != nil {
		return out, err
	}
	out.StageArtifacts = OrdinaryArtifacts(artifacts, task.WorkspacePath, out.DisplayKey)
	out.Timing.StartedAt = task.StartedAt
	out.Timing.FinishedAt = task.FinishedAt
	if task.StartedAt != nil {
		end := out.Timing.MeasuredAt
		if task.FinishedAt != nil {
			end = *task.FinishedAt
		}
		if !end.Before(*task.StartedAt) {
			elapsed := end.Sub(*task.StartedAt).Milliseconds()
			out.Timing.ExecutionElapsedMS = &elapsed
		}
	}
	out.Pages = taskdisplay.Pages{ProcessSteps: taskdisplay.CollectionPage{Total: len(out.ProcessSteps), Revision: out.Revision}, Sources: taskdisplay.CollectionPage{Total: len(out.Sources), Revision: out.Revision}, StageArtifacts: taskdisplay.CollectionPage{Total: len(out.StageArtifacts), Revision: out.Revision}}
	return out, nil
}

// OrdinaryArtifacts adapts only visible saved outputs. Internal paths/metadata
// never leave this function; file URLs use the existing signed-file boundary.
func OrdinaryArtifacts(rows []orm.SubAgentArtifact, workspacePath, displayKey string) []taskdisplay.PublicArtifact {
	result := []taskdisplay.PublicArtifact{}
	for _, row := range rows {
		if row.Hidden {
			continue
		}
		logical := strings.ToLower(row.ContentType)
		var value map[string]any
		raw := common.CanonicalizeTextArtifactValue(row.ContentType, row.Value)
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		base := taskdisplay.PublicArtifact{ArtifactID: row.ID, Revision: max(1, row.CreatedAt.UnixMilli()), ProducerDisplayKey: displayKey, Name: taskdisplay.Text(row.Slot, 100), ContentType: row.ContentType, State: "ready", CreatedAt: row.CreatedAt}
		if name, _ := value["filename"].(string); name != "" {
			base.Name = taskdisplay.Text(filepath.Base(name), 100)
		}
		if base.Name == "" {
			base.Name = "产物"
		}
		if common.IsTextArtifactContentType(logical) || logical == "markdown" || logical == "json" || logical == "application/json" {
			text, _ := value["text"].(string)
			if text == "" {
				text, _ = value["content"].(string)
			}
			if text == "" && (logical == "json" || logical == "application/json") {
				if data, ok := value["data"]; ok {
					encoded, _ := json.MarshalIndent(data, "", "  ")
					text = string(encoded)
				}
			}
			size := int64(len(text))
			base.SizeBytes = &size
			kind := "text"
			if logical == "markdown" || strings.Contains(logical, "markdown") {
				kind = "markdown"
			}
			if strings.Contains(logical, "json") {
				kind = "json"
			}
			base.PreviewKind = &kind
			if len(text) <= 1024*1024 {
				base.InlineContent = text
				base.Capabilities.Preview = true
				base.Capabilities.Download = true
			} else {
				base.State = "unavailable"
			}
			result = append(result, base)
			continue
		}
		if logical != "file" && logical != "file_list" && logical != "image" && !strings.HasPrefix(logical, "image/") {
			continue
		}
		var original map[string]any
		_ = json.Unmarshal(resolveArtifactPaths(raw, workspacePath), &original)
		paths := []string{}
		if logical == "file_list" {
			if entries, ok := original["paths"].([]any); ok {
				for _, entry := range entries {
					if path, ok := entry.(string); ok {
						paths = append(paths, path)
					}
				}
			}
		} else {
			path, _ := original["path"].(string)
			if path == "" {
				path, _ = original["url"].(string)
			}
			paths = append(paths, path)
		}
		for i, path := range paths {
			art := base
			if logical == "file_list" {
				art.ArtifactID = fmt.Sprintf("%s:%d", row.ID, i)
			}
			parsed, _ := url.Parse(path)
			if parsed != nil && parsed.Path != "" {
				art.Name = taskdisplay.Text(filepath.Base(parsed.Path), 100)
			}
			art.ContentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(art.Name)))
			if art.ContentType == "" {
				art.ContentType = "application/octet-stream"
			}
			safeURL := doc.StaticFileURLFromAnyStoragePath(path)
			// Remote public outputs may be opened, but credential-bearing URLs are never exported.
			if safeURL == "" {
				safeURL = taskdisplay.PublicURL(path)
			}
			if safeURL == "" {
				art.State = "unavailable"
				result = append(result, art)
				continue
			}
			if filepath.IsAbs(path) && !strings.HasPrefix(path, "/static-files/") && doc.StaticFileReferenceFromAnyStoragePath(path) != "" {
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
					size := info.Size()
					art.SizeBytes = &size
				} else if os.IsNotExist(err) {
					art.State = "unavailable"
					result = append(result, art)
					continue
				}
			}
			kind := "file"
			if strings.HasPrefix(art.ContentType, "image/") {
				kind = "image"
			}
			art.PreviewKind = &kind
			art.PreviewURL = safeURL
			art.OpenURL = safeURL
			art.DownloadURL = safeURL
			art.Capabilities = taskdisplay.Capabilities{Preview: true, Open: true, Download: true}
			result = append(result, art)
		}
	}
	return result
}
