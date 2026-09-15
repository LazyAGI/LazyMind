package chat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/externalcontext"
	"lazymind/core/log"
	"lazymind/core/store"
)

// ListChatExecutors is the authoritative catalog consumed by every LazyMind
// client. Provider-specific process discovery stays in the local Host adapter.
func ListChatExecutors(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	app := newExternalChatApplication(store.DB())
	executors := make([]chatExecutorStatus, 0, len(chatExecutorDefinitions))
	for _, definition := range chatExecutorDefinitions {
		status := chatExecutorStatus{ID: definition.ID, DisplayName: definition.DisplayName, Kind: definition.Kind}
		if definition.Kind == "internal" {
			status.Installed, status.HostOnline, status.Available = true, true, true
		} else {
			host, err := app.hostStatus(r.Context(), owner, definition.ID)
			if err != nil {
				common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			status.Installed, status.HostOnline, status.Available = host.Installed, host.HostOnline, host.Available
			status.UnavailableReason = host.UnavailableReason
			var connectedSessions int64
			if err := store.DB().WithContext(r.Context()).Model(&orm.ExternalAgentSession{}).
				Where("owner_user_id = ? AND provider = ? AND active = ?", owner, definition.ID, true).
				Count(&connectedSessions).Error; err != nil {
				common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			status.Connected = connectedSessions > 0
		}
		executors = append(executors, status)
	}
	common.ReplyOK(w, map[string]any{"executors": executors})
}

// ExternalChatHostStatus is retained for CLI diagnostics. Product clients use
// ListChatExecutors so provider names and availability rules have one source.
func ExternalChatHostStatus(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	if owner == "" || !isExternalChatProvider(provider) {
		common.ReplyErr(w, "unsupported external chat provider", http.StatusBadRequest)
		return
	}
	status, err := newExternalChatApplication(store.DB()).hostStatus(r.Context(), owner, provider)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, map[string]any{
		"provider": provider, "installed": status.Installed, "host_online": status.HostOnline,
		"available": status.Available, "unavailable_reason": status.UnavailableReason,
	})
}

func SyncExternalAgentSessions(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	var input struct {
		HostID               string                          `json:"host_id"`
		Sessions             []externalcontext.NativeSession `json:"sessions"`
		Reset                bool                            `json:"reset"`
		SessionAccessEnabled *bool                           `json:"session_access_enabled"`
	}
	if owner == "" || !isExternalChatProvider(provider) ||
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&input) != nil {
		common.ReplyErr(w, "invalid external Agent session catalog", http.StatusBadRequest)
		return
	}
	service := externalcontext.New(store.DB())
	updated := 0
	var err error
	if input.SessionAccessEnabled != nil && !*input.SessionAccessEnabled {
		err = service.DisableSessionCatalog(r.Context(), owner, provider)
	} else {
		updated, err = service.SyncSessionCatalog(
			r.Context(), owner, provider, strings.TrimSpace(input.HostID), input.Sessions, input.Reset,
		)
	}
	if errors.Is(err, externalcontext.ErrInvalidSource) {
		common.ReplyErr(w, "invalid external Agent session catalog", http.StatusBadRequest)
		return
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, map[string]any{
		"updated": updated, "rejected": len(input.Sessions) - updated,
	})
}

func ListExternalAgentSessions(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	if owner == "" || !isExternalChatProvider(provider) {
		common.ReplyErr(w, "unsupported external chat provider", http.StatusBadRequest)
		return
	}
	pageSize := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			common.ReplyErr(w, "page_size must be between 1 and 100", http.StatusBadRequest)
			return
		}
		pageSize = value
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("page_token")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			common.ReplyErr(w, "invalid page_token", http.StatusBadRequest)
			return
		}
		offset = value
	}
	page, err := externalcontext.New(store.DB()).ListNativeSessions(
		r.Context(), owner, provider, offset, pageSize,
	)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	nextToken := ""
	if offset+len(page.Items) < int(page.Total) {
		nextToken = strconv.Itoa(offset + len(page.Items))
	}
	common.ReplyOK(w, map[string]any{
		"sessions": page.Items, "total_size": page.Total, "next_page_token": nextToken,
	})
}

func BindExternalAgentSession(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	hostID := strings.TrimSpace(r.URL.Query().Get("host_id"))
	if owner == "" || !isExternalChatProvider(provider) || hostID == "" || threadID == "" {
		common.ReplyErr(w, "unsupported external chat provider", http.StatusBadRequest)
		return
	}
	binding, err := externalcontext.New(store.DB()).BindNativeSession(r.Context(), owner, provider, hostID, threadID)
	if errors.Is(err, externalcontext.ErrInvalidSource) {
		common.ReplyErr(w, "invalid external Agent session catalog", http.StatusBadRequest)
		return
	}
	if errors.Is(err, externalcontext.ErrThreadOwned) {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, binding)
}

func ListExternalChatRuns(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	query := store.DB().WithContext(r.Context()).Where("actor_user_id = ?", owner)
	if conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id")); conversationID != "" {
		query = query.Where("conversation_id = ?", conversationID)
	}
	if requestID := strings.TrimSpace(r.URL.Query().Get("request_id")); requestID != "" {
		if len(requestID) > 512 {
			common.ReplyErr(w, "external chat request_id is too long", http.StatusBadRequest)
			return
		}
		query = query.Where("request_id = ?", externalChatRequestKey(owner, requestID))
	}
	var runs []orm.ExternalChatRun
	if err := query.Order("created_at DESC").Limit(100).Find(&runs).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if runs == nil {
		runs = []orm.ExternalChatRun{}
	}
	common.ReplyOK(w, map[string]any{"runs": runs})
}

// ClaimExternalChatRun is the Host-facing long poll. Claim and lease fencing
// are performed transactionally by Core; connector processes keep no queue.
func ClaimExternalChatRun(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	var input struct {
		HostID            string `json:"host_id"`
		Installed         *bool  `json:"installed"`
		Ready             *bool  `json:"ready"`
		UnavailableReason string `json:"unavailable_reason"`
	}
	if owner == "" || !isExternalChatProvider(provider) ||
		json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.HostID) == "" {
		common.ReplyErr(w, "valid provider and host_id are required", http.StatusBadRequest)
		return
	}
	input.HostID = strings.TrimSpace(input.HostID)
	if len(input.HostID) > 128 {
		common.ReplyErr(w, "host_id is too long", http.StatusBadRequest)
		return
	}
	input.UnavailableReason = strings.TrimSpace(input.UnavailableReason)
	if len(input.UnavailableReason) > 512 {
		input.UnavailableReason = input.UnavailableReason[:512]
	}
	installed, ready := true, true
	if input.Installed != nil {
		installed = *input.Installed
	}
	if input.Ready != nil {
		ready = *input.Ready
	}
	app := newExternalChatApplication(store.DB())
	if err := app.reportHost(r.Context(), owner, provider, input.HostID, installed, ready, input.UnavailableReason); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		// Subscribe before checking the database so a run created between the
		// query and the wait cannot be missed. The timer is only a multi-process
		// fallback; local turns wake immediately without hot polling PostgreSQL.
		available := externalRunsAvailable.subscribe()
		var job *externalChatJob
		var err error
		if ready {
			job, err = app.claim(r.Context(), owner, provider, input.HostID)
		}
		if err != nil {
			common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if job != nil {
			common.ReplyOK(w, map[string]any{"run": job})
			return
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			common.ReplyOK(w, map[string]any{"run": nil})
			return
		}
		if remaining > externalRunClaimFallback {
			remaining = externalRunClaimFallback
		}
		timer := time.NewTimer(remaining)
		select {
		case <-r.Context().Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-available:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func HeartbeatExternalChatRun(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	var input struct {
		HostID     string `json:"host_id"`
		LeaseToken string `json:"lease_token"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil ||
		strings.TrimSpace(input.HostID) == "" || strings.TrimSpace(input.LeaseToken) == "" {
		common.ReplyErr(w, "host_id and lease_token are required", http.StatusBadRequest)
		return
	}
	stop, err := newExternalChatApplication(store.DB()).heartbeat(
		r.Context(), owner, mux.Vars(r)["run_id"], strings.TrimSpace(input.HostID), strings.TrimSpace(input.LeaseToken),
	)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	common.ReplyOK(w, map[string]any{"stop_requested": stop})
}

func PublishExternalChatEvent(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	var input struct {
		HostID     string `json:"host_id"`
		LeaseToken string `json:"lease_token"`
		externalChatEvent
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&input) != nil ||
		strings.TrimSpace(input.HostID) == "" || strings.TrimSpace(input.LeaseToken) == "" || strings.TrimSpace(input.EventID) == "" {
		common.ReplyErr(w, "host_id, lease_token and event_id are required", http.StatusBadRequest)
		return
	}
	input.EventID = strings.TrimSpace(input.EventID)
	if len(input.EventID) > 64 {
		common.ReplyErr(w, "event_id is too long", http.StatusBadRequest)
		return
	}
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	if input.Type != "thread_started" && input.Type != "message" && input.Type != "turn_completed" &&
		input.Type != "completed" && input.Type != "failed" {
		common.ReplyErr(w, "unsupported external chat event", http.StatusBadRequest)
		return
	}
	input.ProviderThreadID = strings.TrimSpace(input.ProviderThreadID)
	input.Error = strings.TrimSpace(input.Error)
	if len(input.ProviderThreadID) > 128 || len(input.Text) > 1<<20 || len(input.Error) > 64<<10 {
		common.ReplyErr(w, "external chat event payload is too large", http.StatusBadRequest)
		return
	}
	sequence, err := newExternalChatApplication(store.DB()).appendEvent(
		r.Context(), owner, mux.Vars(r)["run_id"], strings.TrimSpace(input.HostID),
		strings.TrimSpace(input.LeaseToken), input.externalChatEvent,
	)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	if input.Type == "completed" || input.Type == "failed" {
		runID := mux.Vars(r)["run_id"]
		if err := projectExternalChatRunCache(r.Context(), store.DB(), store.State(), owner, runID); err != nil {
			log.Logger.Warn().Err(err).Str("run_id", runID).
				Msg("external chat terminal committed but cache projection failed")
		}
	}
	common.ReplyOK(w, map[string]any{"sequence": sequence})
}

func PublishExternalChatAttachment(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	var input struct {
		HostID        string `json:"host_id"`
		LeaseToken    string `json:"lease_token"`
		EventID       string `json:"event_id"`
		Filename      string `json:"filename"`
		MediaType     string `json:"media_type"`
		ContentBase64 string `json:"content_base64"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&input) != nil ||
		strings.TrimSpace(input.HostID) == "" || strings.TrimSpace(input.LeaseToken) == "" ||
		strings.TrimSpace(input.EventID) == "" || !validArtifactFilename(input.Filename) {
		common.ReplyErr(w, "invalid external Agent attachment", http.StatusBadRequest)
		return
	}
	input.EventID = strings.TrimSpace(input.EventID)
	if len(input.EventID) > 64 {
		common.ReplyErr(w, "event_id is too long", http.StatusBadRequest)
		return
	}
	content, err := base64.StdEncoding.DecodeString(input.ContentBase64)
	if err != nil || len(content) == 0 || len(content) > maxExternalChatAttachmentBytes {
		common.ReplyErr(w, "invalid external Agent attachment content", http.StatusBadRequest)
		return
	}
	sequence, err := newExternalChatApplication(store.DB()).appendAttachment(
		r.Context(), owner, mux.Vars(r)["run_id"], strings.TrimSpace(input.HostID),
		strings.TrimSpace(input.LeaseToken), externalChatAttachment{
			EventID: input.EventID, Filename: strings.TrimSpace(input.Filename),
			MediaType: strings.TrimSpace(input.MediaType), Content: content,
		},
	)
	if errors.Is(err, errInvalidExternalChatAttachment) {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	common.ReplyOK(w, map[string]any{"sequence": sequence})
}

func externalResumeCursor(r *http.Request, bodyCursor int64) int64 {
	if bodyCursor > 0 {
		return bodyCursor
	}
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		return 0
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0
	}
	return cursor
}

// resumeExternalChatStream returns false when the requested history does not
// belong to an External Chat run, allowing the existing Chat resume path to
// continue unchanged.
func resumeExternalChatStream(
	r *http.Request,
	db *gorm.DB,
	owner, conversationID, historyID string,
	after int64,
	w http.ResponseWriter,
	flusher http.Flusher,
) bool {
	app := newExternalChatApplication(db)
	run, err := app.findRunForResume(r.Context(), owner, conversationID, historyID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}
	if err != nil {
		writeSSEChunk(w, flusher, map[string]any{"runtime_event": failedRunEvent(newID("run_"), "external_resume_failed", false)})
		return true
	}
	hasSemanticOutput, err := app.hasSemanticOutput(r.Context(), run.ID)
	if err != nil {
		writeSSEChunk(w, flusher, map[string]any{
			"conversation_id": conversationID, "history_id": run.HistoryID,
			"runtime_event": failedRunEvent(run.ID, "external_resume_failed", false),
		})
		return true
	}
	cursor := after
	heartbeatAt := time.Time{}
	for {
		events, current, readErr := app.eventsAfter(r.Context(), owner, run.ID, cursor)
		if readErr != nil {
			writeSSEChunk(w, flusher, map[string]any{
				"conversation_id": conversationID, "history_id": run.HistoryID,
				"runtime_event": failedRunEvent(run.ID, "external_resume_failed", hasSemanticOutput),
			})
			return true
		}
		for _, event := range events {
			cursor = event.Sequence
			projection := basicExternalExecutionProjection(current, time.Now().UTC())
			switch event.Type {
			case "message":
				if event.Text != "" {
					hasSemanticOutput = true
				}
				writeSSEChunk(w, flusher, &ChatChunkResponse{
					ConversationID: conversationID, Seq: int32(run.Sequence), HistoryID: run.HistoryID,
					Delta: event.Text, ExternalEventSequence: event.Sequence,
					Execution: &projection,
				})
			case "failed":
				if !hasSemanticOutput {
					hasSemanticOutput = true
					writeSSEChunk(w, flusher, &ChatChunkResponse{
						ConversationID: conversationID, Seq: int32(run.Sequence), HistoryID: run.HistoryID,
						Delta: externalAgentFailureText(event.ErrorMessage), ExternalEventSequence: event.Sequence,
						Execution: &projection,
					})
				}
				writeSSEChunk(w, flusher, &ChatChunkResponse{
					ConversationID: conversationID, Seq: int32(run.Sequence), HistoryID: run.HistoryID,
					ExternalEventSequence: event.Sequence,
					Execution:             &projection,
					RuntimeEvent:          failedRunEvent(run.ID, "external_agent_failed", hasSemanticOutput),
				})
				return true
			case "completed", "stopped":
				projection = app.executionProjection(r.Context(), owner, run.HistoryID, projection)
				writeSSEChunk(w, flusher, &ChatChunkResponse{
					ConversationID: conversationID, Seq: int32(run.Sequence), HistoryID: run.HistoryID,
					ExternalEventSequence: event.Sequence,
					Execution:             &projection,
					RuntimeEvent:          externalRunTerminalEvent(run.ID, event.Type, hasSemanticOutput),
				})
				return true
			}
		}
		if externalRunTerminal(current.Status) {
			projection := app.executionProjection(
				r.Context(), owner, run.HistoryID,
				basicExternalExecutionProjection(current, time.Now().UTC()),
			)
			if current.Status == "failed" && !hasSemanticOutput {
				hasSemanticOutput = true
				writeSSEChunk(w, flusher, &ChatChunkResponse{
					ConversationID: conversationID, Seq: int32(run.Sequence), HistoryID: run.HistoryID,
					Delta: externalAgentFailureText(current.ErrorMessage), ExternalEventSequence: current.NextEventSequence,
					Execution: &projection,
				})
			}
			writeSSEChunk(w, flusher, &ChatChunkResponse{
				ConversationID: conversationID, Seq: int32(run.Sequence), HistoryID: run.HistoryID,
				ExternalEventSequence: current.NextEventSequence,
				Execution:             &projection,
				RuntimeEvent:          externalRunTerminalEvent(run.ID, current.Status, hasSemanticOutput),
			})
			return true
		}
		now := time.Now()
		if heartbeatAt.IsZero() || now.Sub(heartbeatAt) >= time.Second {
			heartbeatAt = now
			projection := basicExternalExecutionProjection(current, now.UTC())
			writeSSEChunk(w, flusher, &ChatChunkResponse{
				ConversationID: conversationID, Seq: int32(run.Sequence), HistoryID: run.HistoryID,
				ExternalEventSequence: cursor,
				Execution:             &projection,
			})
		}
		select {
		case <-r.Context().Done():
			return true
		case <-time.After(200 * time.Millisecond):
		}
	}
}
