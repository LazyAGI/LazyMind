package subagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
)

var errDisplayCursor = errors.New("display cursor expired")

type displayCursor struct {
	Key        string `json:"k"`
	Revision   int64  `json:"r"`
	Collection string `json:"c"`
	Offset     int    `json:"o"`
}

func encodeDisplayCursor(key string, revision int64, collection string, offset int) *string {
	raw, _ := json.Marshal(displayCursor{key, revision, collection, offset})
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return &encoded
}
func pageOrdinaryTask(view taskdisplay.OrdinaryTaskView, r *http.Request, summary bool) (taskdisplay.OrdinaryTaskView, error) {
	limit := taskdisplay.PageLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > taskdisplay.PageLimit {
			return view, errDisplayCursor
		}
		limit = parsed
	}
	selected := r.URL.Query().Get("collection")
	cursorRaw := r.URL.Query().Get("cursor")
	offset := 0
	if cursorRaw != "" {
		if len(cursorRaw) > 1024 {
			return view, errDisplayCursor
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursorRaw)
		if err != nil {
			return view, errDisplayCursor
		}
		var cursor displayCursor
		if json.Unmarshal(raw, &cursor) != nil || cursor.Key != view.DisplayKey || cursor.Revision != view.Revision || cursor.Collection != selected || cursor.Offset < 0 {
			return view, errDisplayCursor
		}
		offset = cursor.Offset
	}
	if selected != "" && selected != "process_steps" && selected != "sources" && selected != "stage_artifacts" {
		return view, errDisplayCursor
	}
	bounds := func(collection string, total int) (int, int, *string, error) {
		start := 0
		if collection == selected {
			start = offset
		}
		if start > total {
			return 0, 0, nil, errDisplayCursor
		}
		end := min(start+limit, total)
		if summary {
			end = start
		}
		var next *string
		if end < total {
			next = encodeDisplayCursor(view.DisplayKey, view.Revision, collection, end)
		}
		return start, end, next, nil
	}
	a, b, next, err := bounds("process_steps", len(view.ProcessSteps))
	if err != nil {
		return view, err
	}
	view.Pages.ProcessSteps.NextCursor = next
	view.ProcessSteps = view.ProcessSteps[a:b]
	a, b, next, err = bounds("sources", len(view.Sources))
	if err != nil {
		return view, err
	}
	view.Pages.Sources.NextCursor = next
	view.Sources = view.Sources[a:b]
	a, b, next, err = bounds("stage_artifacts", len(view.StageArtifacts))
	if err != nil {
		return view, err
	}
	view.Pages.StageArtifacts.NextCursor = next
	view.StageArtifacts = view.StageArtifacts[a:b]
	return view, nil
}
func ordinarySnapshot(ctx context.Context, db *gorm.DB, taskID string) (taskdisplay.OrdinaryTaskView, error) {
	var out taskdisplay.OrdinaryTaskView
	err := withWorkspaceRunUpdate(ctx, db, taskID, func(tx *gorm.DB) error {
		task, err := GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		out, err = OrdinaryTask(ctx, tx, task)
		return err
	})
	return out, err
}
func replyOrdinaryDetail(w http.ResponseWriter, r *http.Request, db *gorm.DB, task *orm.SubAgentTask, artifactsOnly bool) {
	started := time.Now()
	taskdisplay.PrepareRequest(w, r)
	view, err := ordinarySnapshot(r.Context(), db, task.ID)
	if err != nil {
		taskdisplay.ReplyError(w, r, "query task display failed", http.StatusInternalServerError)
		return
	}
	if artifactsOnly && r.URL.Query().Get("collection") == "" {
		query := r.URL.Query()
		query.Set("collection", "stage_artifacts")
		r.URL.RawQuery = query.Encode()
	}
	observeOrdinarySnapshot(r.Context(), started, []taskdisplay.OrdinaryTaskView{view})
	view, err = pageOrdinaryTask(view, r, false)
	if err != nil {
		taskdisplay.Observe(r.Context(), taskdisplay.EventResync, 0, 1)
		taskdisplay.ReplyError(w, r, "task display changed; reload the task", http.StatusConflict)
		return
	}
	if artifactsOnly {
		common.ReplyOK(w, map[string]any{"artifacts": view.StageArtifacts, "page": view.Pages.StageArtifacts, "display_key": view.DisplayKey, "revision": view.Revision})
		return
	}
	common.ReplyOK(w, map[string]any{"task": view})
}
func replyOrdinaryList(w http.ResponseWriter, r *http.Request, db *gorm.DB, tasks []orm.SubAgentTask) {
	started := time.Now()
	taskdisplay.PrepareRequest(w, r)
	out := make([]taskdisplay.OrdinaryTaskView, 0, len(tasks))
	runs := []taskdisplay.OrdinaryRunView{}
	seen := map[string]bool{}
	for _, task := range tasks {
		view, err := ordinarySnapshot(r.Context(), db, task.ID)
		if err != nil {
			taskdisplay.ReplyError(w, r, "query task display failed", http.StatusInternalServerError)
			return
		}
		view, err = pageOrdinaryTask(view, r, r.URL.Query().Get("summary_only") == "true")
		if err != nil {
			taskdisplay.ReplyError(w, r, "invalid task display page", http.StatusBadRequest)
			return
		}
		out = append(out, view)
		if !seen[view.RunID] {
			runs = append(runs, taskdisplay.OrdinaryRunView{RunID: view.RunID, Revision: view.Revision, FinalOutputRefs: []string{}, FinalArtifacts: []taskdisplay.PublicArtifact{}})
			seen[view.RunID] = true
		}
	}
	observeOrdinarySnapshot(r.Context(), started, out)
	common.ReplyOK(w, map[string]any{"tasks": out, "runs": runs})
}

// Full snapshots make reconnection independent of process-local brokers/Redis.
// Last-Event-ID is a hint only: every connection begins at the current DB state.
func streamOrdinaryTask(w http.ResponseWriter, r *http.Request, db *gorm.DB, taskID string, flusher http.Flusher) {
	requestID := taskdisplay.PrepareRequest(w, r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeats := time.NewTicker(15 * time.Second)
	defer heartbeats.Stop()
	lastRevision := int64(-1)
	lastKey := ""
	for {
		started := time.Now()
		view, err := ordinarySnapshot(r.Context(), db, taskID)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			taskdisplay.Observe(r.Context(), taskdisplay.EventResync, time.Since(started), 1)
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"resync_required\",\"request_id\":%q}\n\n", requestID)
			flusher.Flush()
			return
		}
		var owner string
		if err := db.WithContext(r.Context()).Model(&orm.SubAgentTask{}).Where("id = ?", taskID).Pluck("create_user_id", &owner).Error; err != nil || owner != requestUserID(r) {
			return
		}
		if view.Revision != lastRevision || view.DisplayKey != lastKey {
			observeOrdinarySnapshot(r.Context(), started, []taskdisplay.OrdinaryTaskView{view})
			view, err = pageOrdinaryTask(view, r, false)
			if err != nil {
				return
			}
			eventID := view.DisplayKey + ":" + strconv.FormatInt(view.Revision, 10)
			event := taskdisplay.SnapshotEvent{Type: "task_snapshot", SchemaVersion: 1, EventID: eventID, DisplayKey: view.DisplayKey, Revision: view.Revision, EmittedAt: time.Now().UTC(), Data: view}
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", eventID, raw)
			flusher.Flush()
			lastRevision = view.Revision
			lastKey = view.DisplayKey
		}
		if isTerminal(view.Status) {
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		case <-heartbeats.C:
			writeTaskHeartbeat(w, flusher)
		}
	}
}

func observeOrdinarySnapshot(ctx context.Context, started time.Time, views []taskdisplay.OrdinaryTaskView) {
	taskdisplay.Observe(ctx, taskdisplay.EventSnapshot, time.Since(started), len(views))
	missing := 0
	for _, view := range views {
		if view.ProcessState == "not_provided" {
			missing++
		}
	}
	if missing > 0 {
		taskdisplay.Observe(ctx, taskdisplay.EventMissingProcess, 0, missing)
	}
}
func replyTaskError(w http.ResponseWriter, r *http.Request, message string, status int) {
	if r.URL.Query().Get("view") == "ordinary" {
		taskdisplay.ReplyError(w, r, message, status)
		return
	}
	common.ReplyErr(w, message, status)
}
