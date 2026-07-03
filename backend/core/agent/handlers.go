package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

type threadResponse struct {
	ThreadID      string    `json:"thread_id"`
	CurrentTaskID string    `json:"current_task_id,omitempty"`
	Status        string    `json:"status"`
	ThreadPayload any       `json:"thread_payload,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type threadListResponse struct {
	Threads       []threadResponse `json:"threads"`
	TotalSize     int64            `json:"total_size"`
	NextPageToken string           `json:"next_page_token"`
}

type recordResponse struct {
	ID         string    `json:"id"`
	ThreadID   string    `json:"thread_id"`
	StepID     string    `json:"step_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	StreamKind string    `json:"stream_kind"`
	EventName  string    `json:"event_name,omitempty"`
	Payload    any       `json:"payload"`
	RawFrame   string    `json:"raw_frame"`
	CreatedAt  time.Time `json:"created_at"`
}

type threadStepResponse struct {
	ThreadID      string     `json:"thread_id"`
	StepID        string     `json:"step_id"`
	Title         string     `json:"title,omitempty"`
	Status        string     `json:"status"`
	Active        bool       `json:"active"`
	OrderIndex    int        `json:"order_index"`
	EventCount    int64      `json:"event_count"`
	CurrentTaskID string     `json:"current_task_id,omitempty"`
	NextStepRunID string     `json:"next_step_run_id"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type upstreamProxyResponse struct {
	Body        any
	ContentType string
}

type threadFlowStatusResponse struct {
	ThreadID      string   `json:"thread_id,omitempty"`
	Status        string   `json:"status,omitempty"`
	CurrentStep   string   `json:"current_step,omitempty"`
	ActiveTaskIDs []string `json:"active_task_ids,omitempty"`
	ReportReady   bool     `json:"report_ready,omitempty"`
	LastError     any      `json:"last_error,omitempty"`
}

var (
	threadEventsKeepaliveInterval = time.Second
	errThreadEventsDone           = errors.New("thread events done")
)

func ListThreads(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	pageSize := parseThreadPageSize(r.URL.Query().Get("page_size"))
	offset, err := parseThreadPageToken(r.URL.Query().Get("page_token"))
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	threads, total, err := listThreads(db, userID, offset, pageSize)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "list agent threads failed", err), http.StatusInternalServerError)
		return
	}

	statusByThread := map[string]threadFlowStatusResponse{}
	if len(threads) > 0 {
		var statusErr error
		statusByThread, statusErr = fetchThreadStatuses(r.Context(), r, threads)
		if statusErr != nil {
			log.Logger.Warn().Err(statusErr).Str("user_id", userID).Msg("list agent thread statuses failed; using local thread status")
		}
	}

	items := make([]threadResponse, 0, len(threads))
	for _, thread := range threads {
		item := toThreadResponse(thread)
		if upstreamStatus, ok := statusByThread[thread.ThreadID]; ok {
			if status := strings.TrimSpace(upstreamStatus.Status); status != "" {
				item.Status = status
			}
		}
		items = append(items, item)
	}
	nextPageToken := ""
	if offset+len(threads) < int(total) {
		nextPageToken = fmt.Sprintf("%d", offset+len(threads))
	}

	common.ReplyOK(w, threadListResponse{
		Threads:       items,
		TotalSize:     total,
		NextPageToken: nextPageToken,
	})
}

func CreateThread(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	requestPayload, _, err := decodeRequestBody(r)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	delete(requestPayload, "llm_config")
	applyThreadCreateTitle(r.Context(), db, requestPayload, time.Now())
	localThreadPayload := cloneJSONMap(requestPayload)
	if err := attachThreadModelConfig(r.Context(), db, store.UserID(r), requestPayload); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "load llm config failed", err), http.StatusInternalServerError)
		return
	}
	if !hasThreadRequiredLLMConfig(requestPayload) {
		common.ReplyErr(w, "请先配置 llm 和 evo_llm 模型后再创建任务", http.StatusUnprocessableEntity)
		return
	}

	var creationGuard *userActiveThreadCreationGuard
	if guard, guardErr := reserveUserActiveThreadCreation(r.Context(), db, r); guardErr != nil {
		replyUserActiveThreadError(w, guardErr)
		return
	} else {
		creationGuard = guard
		defer creationGuard.Abort(db)
	}

	var upstreamRaw json.RawMessage
	headers := forwardedUpstreamHeaders(r)
	upstreamPayload := buildEvoThreadCreatePayload(requestPayload)
	if err := newEvoClient(headers).CreateThread(r.Context(), upstreamPayload, &upstreamRaw); err != nil {
		common.ReplyErrWithData(w, "create upstream thread failed", map[string]any{"detail": err.Error()}, evoProxyStatusCode(err))
		return
	}

	var upstreamValue any
	if err := json.Unmarshal(upstreamRaw, &upstreamValue); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid upstream response", err), http.StatusBadGateway)
		return
	}

	threadID := extractStringByKeys(upstreamValue, "thread_id", "id")
	if threadID == "" {
		common.ReplyErr(w, "upstream thread response missing thread_id", http.StatusBadGateway)
		return
	}

	localThreadPayload["upstream"] = upstreamValue
	if status := extractStringByExactKeys(upstreamValue, "status"); status != "" {
		localThreadPayload["status"] = status
	}
	localThreadPayloadBytes, err := json.Marshal(localThreadPayload)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "marshal thread payload failed", err), http.StatusInternalServerError)
		return
	}

	thread, err := upsertThread(db, threadID, "", "created", string(localThreadPayloadBytes), "", store.UserID(r), store.UserName(r))
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "save thread failed", err), http.StatusInternalServerError)
		return
	}
	if err := creationGuard.Commit(db, threadID); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "activate user thread failed", err), http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, map[string]any{
		"thread":   toThreadResponse(thread),
		"upstream": upstreamValue,
	})
}

func GetThread(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	thread, err := loadUserThread(db, r, threadID)
	if err != nil {
		replyThreadLoadError(w, err)
		return
	}
	item := toThreadResponse(thread)
	if flowStatus, statusErr := fetchThreadFlowStatus(r.Context(), r, threadID); statusErr != nil {
		log.Logger.Warn().Err(statusErr).Str("thread_id", threadID).Msg("get agent thread status failed; using local thread status")
	} else if flowStatus != nil && strings.TrimSpace(flowStatus.Status) != "" {
		item.Status = strings.TrimSpace(flowStatus.Status)
	}
	common.ReplyOK(w, map[string]any{"thread": item})
}

func ListThreadRecords(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if _, err := loadUserThread(db, r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}

	streamKind := strings.TrimSpace(r.URL.Query().Get("stream_kind"))
	stepID := strings.TrimSpace(r.URL.Query().Get("step_id"))
	afterID := parseAfterID(r)
	limit := parseRecordLimit(r.URL.Query().Get("limit"))

	records, err := listRecordsWithStep(db, threadID, streamKind, "", stepID, afterID, limit+1)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "list thread records failed", err), http.StatusInternalServerError)
		return
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	nextAfterID := afterID
	if len(records) > 0 {
		nextAfterID = records[len(records)-1].ID
	}

	items := make([]recordResponse, 0, len(records))
	for _, record := range records {
		items = append(items, toRecordResponse(record))
	}

	common.ReplyOK(w, map[string]any{
		"thread_id":     threadID,
		"step_id":       stepID,
		"stream_kind":   streamKind,
		"items":         items,
		"next_after_id": nextAfterID,
		"has_more":      hasMore,
	})
}

func ListThreadSteps(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if _, err := loadUserThread(db, r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}

	var steps []orm.AgentThreadStep
	if err := db.Where("thread_id = ?", threadID).
		Order("order_index ASC, created_at ASC, step_id ASC").
		Find(&steps).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "list thread steps failed", err), http.StatusInternalServerError)
		return
	}

	items := make([]threadStepResponse, 0, len(steps))
	activeStepID := ""
	var activeUpdatedAt time.Time
	for _, step := range steps {
		items = append(items, toThreadStepResponse(step))
		if step.Active && (activeStepID == "" || step.UpdatedAt.After(activeUpdatedAt)) {
			activeStepID = step.StepID
			activeUpdatedAt = step.UpdatedAt
		}
	}

	common.ReplyOK(w, map[string]any{
		"thread_id":      threadID,
		"active_step_id": activeStepID,
		"items":          items,
		"total_size":     len(items),
	})
}

func ListThreadStepRecords(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	stepID := strings.TrimSpace(mux.Vars(r)["step_id"])
	if stepID == "" {
		common.ReplyErr(w, "step_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(db, r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}

	afterID := parseAfterID(r)
	limit := parseRecordLimit(r.URL.Query().Get("limit"))
	records, err := listRecordsWithStep(db, threadID, streamKindThreadEvent, "", stepID, afterID, limit+1)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "list thread step records failed", err), http.StatusInternalServerError)
		return
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	nextAfterID := afterID
	if len(records) > 0 {
		nextAfterID = records[len(records)-1].ID
	}

	items := make([]recordResponse, 0, len(records))
	for _, record := range records {
		items = append(items, toRecordResponse(record))
	}

	common.ReplyOK(w, map[string]any{
		"thread_id":     threadID,
		"step_id":       stepID,
		"items":         items,
		"next_after_id": nextAfterID,
		"has_more":      hasMore,
	})
}

func StreamThreadMessages(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	thread, err := loadUserThread(db, r, threadID)
	if err != nil {
		replyThreadLoadError(w, err)
		return
	}

	afterID := parseAfterID(r)
	resumeOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("resume_only")), "1") ||
		strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("resume_only")), "true")

	var session *activeMessageStream
	if !resumeOnly {
		requestPayload, _, err := decodeRequestBody(r)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
			return
		}
		if len(requestPayload) == 0 {
			common.ReplyErr(w, "messages request body required", http.StatusBadRequest)
			return
		}
		messagePayload := map[string]any{}
		for _, key := range []string{"message_id", "text", "content"} {
			if value, ok := requestPayload[key]; ok {
				messagePayload[key] = value
			}
		}
		if _, ok := messagePayload["content"]; !ok {
			if value, ok := requestPayload["message"]; ok {
				messagePayload["content"] = value
			} else if value, ok := requestPayload["query"]; ok {
				messagePayload["content"] = value
			}
		}
		requestBytes, err := json.Marshal(messagePayload)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "marshal body failed", err), http.StatusInternalServerError)
			return
		}

		if err := ensureUserCanActivateThread(r.Context(), db, r, threadID); err != nil {
			writeUserActiveThreadSSEError(w, threadID, err)
			return
		}

		session, err = ensureMessageStream(db, thread, requestBytes, forwardedUpstreamHeaders(r))
		if err != nil {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
	} else {
		session = activeStreams.get(threadID)
	}

	flusher, ok := ensureSSEHeaders(w)
	if !ok {
		common.ReplyErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)

	streamMessageRecords(r, w, flusher, db, threadID, afterID, session)
}

func StreamThreadEvents(w http.ResponseWriter, r *http.Request) {
	streamThreadEvents(w, r, "")
}

func StreamThreadStepEvents(w http.ResponseWriter, r *http.Request) {
	stepID := strings.TrimSpace(mux.Vars(r)["step_id"])
	if stepID == "" {
		common.ReplyErr(w, "step_id required", http.StatusBadRequest)
		return
	}
	streamThreadEvents(w, r, stepID)
}

func streamThreadEvents(w http.ResponseWriter, r *http.Request, stepID string) {
	requestStarted := time.Now()
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	loadThreadStarted := time.Now()
	if _, err := loadUserThread(db, r, threadID); err != nil {
		log.Logger.Warn().
			Err(err).
			Str("thread_id", threadID).
			Dur("load_thread_elapsed", time.Since(loadThreadStarted)).
			Dur("request_elapsed", time.Since(requestStarted)).
			Msg("agent thread events load user thread failed")
		replyThreadLoadError(w, err)
		return
	}
	writeHeaderStarted := time.Now()
	flusher, ok := ensureSSEHeaders(w)
	if !ok {
		common.ReplyErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	log.Logger.Info().
		Str("thread_id", threadID).
		Str("sse_endpoint", ":events").
		Dur("write_header_elapsed", time.Since(writeHeaderStarted)).
		Dur("request_elapsed", time.Since(requestStarted)).
		Msg("agent thread events response header written")

	log.Logger.Info().
		Str("thread_id", threadID).
		Dur("load_thread_elapsed", time.Since(loadThreadStarted)).
		Dur("request_elapsed", time.Since(requestStarted)).
		Msg("agent thread events load thread completed")

	upstreamURL := newEvoClient(forwardedUpstreamHeaders(r)).EventsStreamURL(threadID, stepID)
	lastUpstreamEventID := parseUpstreamThreadEventID(r)
	for {
		if r.Context().Err() != nil {
			return
		}

		openUpstreamStarted := time.Now()
		log.Logger.Info().
			Str("thread_id", threadID).
			Str("upstream_url", upstreamURL).
			Str("last_upstream_event_id", lastUpstreamEventID).
			Dur("request_elapsed", time.Since(requestStarted)).
			Msg("agent thread events opening upstream sse")
		upstreamCtx, cancelUpstream := context.WithCancel(r.Context())
		resp, err := openThreadEventsStream(upstreamCtx, r, upstreamURL, lastUpstreamEventID)
		if err != nil {
			cancelUpstream()
			log.Logger.Warn().
				Err(err).
				Str("thread_id", threadID).
				Str("upstream_url", upstreamURL).
				Str("last_upstream_event_id", lastUpstreamEventID).
				Dur("open_upstream_elapsed", time.Since(openUpstreamStarted)).
				Dur("request_elapsed", time.Since(requestStarted)).
				Msg("agent thread events open upstream sse failed")
			if !shouldContinueThreadEvents(r.Context(), r, threadID, "open_upstream_failed", err) {
				return
			}
			if !sleepBeforeThreadEventsReconnect(r.Context()) {
				return
			}
			continue
		}
		log.Logger.Info().
			Str("thread_id", threadID).
			Str("upstream_url", upstreamURL).
			Str("last_upstream_event_id", lastUpstreamEventID).
			Int("upstream_status", resp.StatusCode).
			Str("upstream_content_type", resp.Header.Get("Content-Type")).
			Dur("open_upstream_elapsed", time.Since(openUpstreamStarted)).
			Dur("request_elapsed", time.Since(requestStarted)).
			Msg("agent thread events upstream sse opened")

		flowStopped, monitorDone := monitorThreadEventsFlowStatus(upstreamCtx, cancelUpstream, r, threadID)
		streamErr := streamUpstreamThreadEvents(
			upstreamCtx,
			w,
			flusher,
			db,
			threadID,
			stepID,
			resp.Body,
			&lastUpstreamEventID,
			func(reason string, cause error) bool {
				return shouldContinueThreadEvents(r.Context(), r, threadID, reason, cause)
			},
		)
		_ = resp.Body.Close()
		cancelUpstream()
		<-monitorDone
		if errors.Is(streamErr, errThreadEventsDone) {
			log.Logger.Info().
				Str("thread_id", threadID).
				Str("step_id", stepID).
				Msg("agent thread events stopping after done")
			return
		}
		if streamErr != nil {
			log.Logger.Warn().Err(streamErr).Str("thread_id", threadID).Msg("consume upstream thread events stream failed")
		}
		select {
		case <-flowStopped:
			log.Logger.Info().
				Str("thread_id", threadID).
				Str("reason", "flow_status_not_running").
				Msg("agent thread events stopping downstream stream")
			return
		default:
		}
		if r.Context().Err() != nil {
			return
		}
		if !shouldContinueThreadEvents(r.Context(), r, threadID, "upstream_stream_ended", streamErr) {
			return
		}
		if !sleepBeforeThreadEventsReconnect(r.Context()) {
			return
		}
	}
}

func GetThreadResultDatasets(w http.ResponseWriter, r *http.Request) {
	getThreadResults(w, r, "datasets")
}
func DownloadThreadResult(w http.ResponseWriter, r *http.Request) {
	downloadThreadResultCSV(w, r)
}
func GetThreadResultEvalReports(w http.ResponseWriter, r *http.Request) {
	getThreadResults(w, r, "eval-reports")
}
func GetThreadResultAnalysisReports(w http.ResponseWriter, r *http.Request) {
	getThreadResults(w, r, "analysis-reports")
}
func GetThreadResultDiffs(w http.ResponseWriter, r *http.Request) { getThreadResults(w, r, "diffs") }
func GetThreadResultAbtests(w http.ResponseWriter, r *http.Request) {
	getThreadResults(w, r, "abtests")
}
func GetThreadFlowStatus(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if threadID == "" {
		common.ReplyErr(w, "thread_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	flowStatus, err := fetchThreadFlowStatus(r.Context(), r, threadID)
	if err != nil {
		common.ReplyErrWithData(w, "fetch thread flow status failed", map[string]any{"detail": err.Error()}, evoProxyStatusCode(err))
		return
	}
	writeProxyResponse(w, &upstreamProxyResponse{Body: flowStatus, ContentType: "application/json"})
}
func GetThreadArtifact(w http.ResponseWriter, r *http.Request) {
	artifactID := strings.TrimSpace(mux.Vars(r)["artifact_id"])
	if artifactID == "" {
		common.ReplyErr(w, "artifact_id required", http.StatusBadRequest)
		return
	}
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if threadID == "" {
		common.ReplyErr(w, "thread_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	proxy, statusCode, err := fetchThreadArtifactProxy(r.Context(), r, threadID, artifactID)
	if err != nil {
		common.ReplyErrWithData(w, "fetch thread artifact failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	writeProxyResponse(w, proxy)
}
func StartThread(w http.ResponseWriter, r *http.Request)  { postThreadAction(w, r, "start") }
func PauseThread(w http.ResponseWriter, r *http.Request)  { postThreadAction(w, r, "pause") }
func CancelThread(w http.ResponseWriter, r *http.Request) { postThreadAction(w, r, "cancel") }
func RetryThread(w http.ResponseWriter, r *http.Request)  { postThreadAction(w, r, "retry") }
func ContinueThread(w http.ResponseWriter, r *http.Request) {
	postThreadAction(w, r, "continue")
}

func writeUserActiveThreadSSEError(w http.ResponseWriter, threadID string, err error) {
	var activeErr *userActiveThreadError
	if !errors.As(err, &activeErr) || activeErr.data["type"] != userActiveThreadExistsType {
		replyUserActiveThreadError(w, err)
		return
	}

	flusher, ok := ensureSSEHeaders(w)
	if !ok {
		replyUserActiveThreadError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)

	message := userActiveThreadExistsMessage
	if strings.TrimSpace(activeErr.message) != "" {
		message = activeErr.message
	}
	writeNamedSSE(w, flusher, "", userActiveThreadSSEErrorPayload{
		Type:      userActiveThreadExistsType,
		ThreadID:  threadID,
		MessageID: fmt.Sprintf("msg_%s_%s", threadID, newStreamRecordID()),
		Message:   message,
		Delta:     message,
	})
}

type userActiveThreadSSEErrorPayload struct {
	Type      string `json:"type"`
	ThreadID  string `json:"thread_id"`
	MessageID string `json:"message_id"`
	Message   string `json:"message"`
	Delta     string `json:"delta"`
}

func getThreadResults(w http.ResponseWriter, r *http.Request, resultKind string) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if threadID == "" {
		common.ReplyErr(w, "thread_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	version, err := parsePositiveIntQuery(r, "version")
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	proxy, statusCode, err := fetchThreadResultProxy(r.Context(), r, threadID, resultKind, version)
	if err != nil {
		common.ReplyErrWithData(w, "fetch thread results failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	writeProxyResponse(w, proxy)
}

func downloadThreadResultCSV(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	resultKind := strings.TrimSpace(mux.Vars(r)["kind"])
	if threadID == "" {
		common.ReplyErr(w, "thread_id required", http.StatusBadRequest)
		return
	}
	if _, ok := resultKindGateStep[resultKind]; !ok {
		common.ReplyErr(w, "unsupported result kind", http.StatusBadRequest)
		return
	}
	if format := strings.TrimSpace(r.URL.Query().Get("format")); format != "" && format != "csv" {
		common.ReplyErr(w, "only csv download is supported", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	version, err := parsePositiveIntQuery(r, "version")
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, content, ok, err := fetchThreadResultContent(r.Context(), r, threadID, resultKind, version)
	if err != nil {
		common.ReplyErrWithData(w, "fetch thread result download failed", map[string]any{"detail": err.Error()}, evoProxyStatusCode(err))
		return
	}
	if !ok {
		common.ReplyErr(w, "thread result gate not found", http.StatusNotFound)
		return
	}
	csvBytes, _, err := buildGateCSV(resultKind, content.Content)
	if err != nil {
		common.ReplyErrWithData(w, "build thread result csv failed", map[string]any{"detail": err.Error()}, http.StatusInternalServerError)
		return
	}
	filename := gateCSVDownloadFilename(threadID, resultKind, content.Version)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", gateCSVContentDisposition(filename))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(csvBytes)
}

func parsePositiveIntQuery(r *http.Request, name string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func postThreadAction(w http.ResponseWriter, r *http.Request, action string) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if threadID == "" {
		common.ReplyErr(w, "thread_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	if action == "start" || action == "retry" || action == "continue" {
		if err := ensureUserCanActivateThread(r.Context(), store.DB(), r, threadID); err != nil {
			replyUserActiveThreadError(w, err)
			return
		}
	}
	var proxy *upstreamProxyResponse
	var statusCode int
	var err error
	if action == "start" || action == "retry" || action == "continue" {
		payload, _, decodeErr := decodeRequestBody(r)
		if decodeErr != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", decodeErr), http.StatusBadRequest)
			return
		}
		commandPayload := map[string]any{}
		for _, key := range []string{"command_id", "until_step"} {
			if value, ok := payload[key]; ok {
				commandPayload[key] = value
			}
		}
		proxy, statusCode, err = newEvoClient(forwardedUpstreamHeaders(r)).PostCommand(r.Context(), threadID, action, commandPayload)
	} else {
		payload, _, decodeErr := decodeRequestBody(r)
		if decodeErr != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", decodeErr), http.StatusBadRequest)
			return
		}
		commandPayload := map[string]any{}
		if value, ok := payload["command_id"]; ok {
			commandPayload["command_id"] = value
		}
		proxy, statusCode, err = newEvoClient(forwardedUpstreamHeaders(r)).PostCommand(r.Context(), threadID, action, commandPayload)
	}
	if err != nil {
		common.ReplyErrWithData(w, "post thread action failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	writeProxyResponse(w, proxy)
}

type fetchedThreadEvent struct {
	EventID   string
	TaskID    string
	EventName string
	RawFrame  string
}

type threadEventStreamChunk struct {
	Event           fetchedThreadEvent
	UpstreamEventID string
	FrameIndex      int
	ReadElapsed     time.Duration
	ParseElapsed    time.Duration
	FrameStarted    time.Time
	StreamElapsed   time.Duration
	Keepalive       bool
}

type threadEventStreamResult struct {
	Err         error
	LastEventID string
	StopReason  string
}

func openThreadEventsStream(ctx context.Context, r *http.Request, upstreamURL, lastEventID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range forwardedUpstreamHeaders(r) {
		if strings.EqualFold(key, "Accept") {
			continue
		}
		req.Header.Set(key, value)
	}
	req.Header.Set("Accept", "text/event-stream")
	if strings.TrimSpace(lastEventID) != "" {
		req.Header.Set("Last-Event-ID", strings.TrimSpace(lastEventID))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return resp, nil
}

func streamUpstreamThreadEvents(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	db *gorm.DB,
	threadID string,
	stepID string,
	body io.Reader,
	lastUpstreamEventID *string,
	shouldContinue func(reason string, cause error) bool,
) error {
	readerCtx, cancelReader := context.WithCancel(ctx)
	defer cancelReader()
	chunks, done := readUpstreamThreadEvents(readerCtx, threadID, body, shouldContinue)
	streamStarted := time.Now()
	keepaliveTimer := time.NewTimer(threadEventsKeepaliveInterval)
	defer keepaliveTimer.Stop()
	resetKeepaliveTimer := func() {
		if !keepaliveTimer.Stop() {
			select {
			case <-keepaliveTimer.C:
			default:
			}
		}
		keepaliveTimer.Reset(threadEventsKeepaliveInterval)
	}

	for {
		select {
		case <-ctx.Done():
			cancelReader()
			return ctx.Err()
		case <-keepaliveTimer.C:
			keepalive := threadEventStreamChunk{
				Keepalive:     true,
				FrameStarted:  time.Now(),
				StreamElapsed: time.Since(streamStarted),
			}
			if err := writeThreadEventKeepalive(w, flusher, threadID, keepalive); err != nil {
				cancelReader()
				return err
			}
			keepaliveTimer.Reset(threadEventsKeepaliveInterval)
		case chunk, ok := <-chunks:
			if !ok {
				result := waitThreadEventStreamResult(done)
				if result.LastEventID != "" && lastUpstreamEventID != nil {
					*lastUpstreamEventID = result.LastEventID
				}
				switch result.StopReason {
				case "done":
					return errThreadEventsDone
				}
				return result.Err
			}
			if chunk.Keepalive {
				if chunk.UpstreamEventID != "" && lastUpstreamEventID != nil {
					*lastUpstreamEventID = chunk.UpstreamEventID
				}
				if err := writeThreadEventKeepalive(w, flusher, threadID, chunk); err != nil {
					cancelReader()
					return err
				}
				resetKeepaliveTimer()
				continue
			}
			if err := writeThreadEventStreamChunk(
				w,
				flusher,
				db,
				threadID,
				stepID,
				chunk,
				lastUpstreamEventID,
			); err != nil {
				cancelReader()
				return err
			}
			resetKeepaliveTimer()
		}
	}
}

func writeThreadEventKeepalive(
	w http.ResponseWriter,
	flusher http.Flusher,
	threadID string,
	chunk threadEventStreamChunk,
) error {
	writeStarted := time.Now()
	writeErr := writeSSEKeepalive(w, flusher)
	log.Logger.Info().
		Str("thread_id", threadID).
		Str("sse_endpoint", ":events").
		Str("upstream_event_id", chunk.UpstreamEventID).
		Int("frame_index", chunk.FrameIndex).
		Dur("read_frame_elapsed", chunk.ReadElapsed).
		Dur("parse_frame_elapsed", chunk.ParseElapsed).
		Dur("write_frontend_elapsed", time.Since(writeStarted)).
		Dur("frame_total_elapsed", time.Since(chunk.FrameStarted)).
		Dur("stream_elapsed", chunk.StreamElapsed).
		Err(writeErr).
		Msg("agent thread events keepalive forwarded")
	return writeErr
}

func writeThreadEventStreamChunk(
	w http.ResponseWriter,
	flusher http.Flusher,
	db *gorm.DB,
	threadID string,
	stepID string,
	chunk threadEventStreamChunk,
	lastUpstreamEventID *string,
) error {
	if chunk.UpstreamEventID != "" && lastUpstreamEventID != nil {
		*lastUpstreamEventID = chunk.UpstreamEventID
	}
	eventStepID := strings.TrimSpace(threadEventStepID(chunk.Event))
	requestedStepID := strings.TrimSpace(stepID)
	if requestedStepID != "" {
		if eventStepID == "" {
			eventStepID = requestedStepID
			chunk.Event.RawFrame = threadEventRawFrameWithStepID(chunk.Event.RawFrame, requestedStepID)
		} else if eventStepID != requestedStepID {
			log.Logger.Info().
				Str("thread_id", threadID).
				Str("step_id", stepID).
				Str("event_step_id", eventStepID).
				Str("event_name", chunk.Event.EventName).
				Str("upstream_event_id", chunk.UpstreamEventID).
				Int("frame_index", chunk.FrameIndex).
				Msg("agent thread step event skipped")
			return nil
		}
	}

	downstreamFrame := buildThreadEventFrame(chunk.Event.RawFrame)
	writeStarted := time.Now()
	bytesWritten, writeErr := io.WriteString(w, downstreamFrame)
	flushStarted := time.Now()
	flusher.Flush()
	flushElapsed := time.Since(flushStarted)
	writeElapsed := time.Since(writeStarted)
	if writeErr != nil {
		return writeErr
	}
	log.Logger.Info().
		Str("thread_id", threadID).
		Str("sse_endpoint", ":events").
		Str("task_id", chunk.Event.TaskID).
		Str("event_name", chunk.Event.EventName).
		Str("upstream_event_id", chunk.UpstreamEventID).
		Int("frame_index", chunk.FrameIndex).
		Int("data_bytes", len(chunk.Event.RawFrame)).
		Int("downstream_frame_bytes", len(downstreamFrame)).
		Int("downstream_bytes_written", bytesWritten).
		Dur("read_frame_elapsed", chunk.ReadElapsed).
		Dur("parse_frame_elapsed", chunk.ParseElapsed).
		Dur("write_frontend_elapsed", writeElapsed).
		Dur("flush_frontend_elapsed", flushElapsed).
		Dur("frame_total_elapsed", time.Since(chunk.FrameStarted)).
		Dur("stream_elapsed", chunk.StreamElapsed).
		Msg("agent thread events frame forwarded")
	if writeErr != nil {
		return writeErr
	}

	saveStarted := time.Now()
	recordKey := ""
	eventID := firstNonEmptyString(chunk.UpstreamEventID, chunk.Event.EventID)
	if eventStepID != "" && eventID != "" {
		recordKey = sha256Hex(eventStepID + "\x00" + eventID)
	}
	_, saveCreated, saveErr := saveThreadRecordWithOptions(
		db,
		threadID,
		"",
		chunk.Event.TaskID,
		streamKindThreadEvent,
		chunk.Event.EventName,
		chunk.Event.RawFrame,
		chunk.Event.RawFrame,
		saveThreadRecordOptions{
			StepID:    eventStepID,
			RecordKey: recordKey,
		},
	)
	saveElapsed := time.Since(saveStarted)
	if saveErr != nil {
		log.Logger.Warn().Err(saveErr).Str("thread_id", threadID).Msg("save thread event record failed")
	}
	if eventStepID != "" {
		if stepErr := updateThreadStepFromEvent(db, threadID, eventStepID, chunk.Event); stepErr != nil {
			log.Logger.Warn().Err(stepErr).Str("thread_id", threadID).Str("step_id", eventStepID).Msg("update thread step from event failed")
		}
	}

	updates := map[string]any{
		"status":     "event_streaming",
		"updated_at": time.Now().UTC(),
	}
	if chunk.Event.TaskID != "" {
		updates["current_task_id"] = chunk.Event.TaskID
	}
	updateStarted := time.Now()
	updateErr := db.Model(&orm.AgentThread{}).Where("thread_id = ?", threadID).Updates(updates).Error
	updateElapsed := time.Since(updateStarted)
	if updateErr != nil {
		log.Logger.Warn().Err(updateErr).Str("thread_id", threadID).Msg("update thread event stream status failed")
	}
	log.Logger.Info().
		Str("thread_id", threadID).
		Str("task_id", chunk.Event.TaskID).
		Str("event_name", chunk.Event.EventName).
		Str("upstream_event_id", chunk.UpstreamEventID).
		Int("frame_index", chunk.FrameIndex).
		Bool("record_created", saveCreated).
		Dur("save_record_elapsed", saveElapsed).
		Dur("update_thread_elapsed", updateElapsed).
		Msg("agent thread events frame persisted")
	return nil
}

func threadEventRawFrameWithStepID(rawFrame, stepID string) string {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return rawFrame
	}
	payload, ok := parseJSONValue(rawFrame).(map[string]any)
	if !ok {
		return rawFrame
	}
	changed := false
	if firstNonEmptyScalar(payload["step_id"]) == "" {
		payload["step_id"] = stepID
		changed = true
	}
	if firstNonEmptyScalar(payload["step_run_id"]) == "" {
		payload["step_run_id"] = stepID
		changed = true
	}
	if !changed {
		return rawFrame
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return rawFrame
	}
	return string(encoded)
}

func readUpstreamThreadEvents(
	ctx context.Context,
	threadID string,
	body io.Reader,
	shouldContinue func(reason string, cause error) bool,
) (<-chan threadEventStreamChunk, <-chan threadEventStreamResult) {
	chunks := make(chan threadEventStreamChunk, 1)
	done := make(chan threadEventStreamResult, 1)

	go func() {
		defer close(chunks)
		reader := bufio.NewReader(body)
		streamStarted := time.Now()
		frameIndex := 0
		result := threadEventStreamResult{}
		defer func() {
			done <- result
			close(done)
		}()

		for {
			readStarted := time.Now()
			frame, err := readThreadEventSSEFrame(reader)
			readElapsed := time.Since(readStarted)
			if err != nil {
				if err == io.EOF || ctx.Err() != nil {
					log.Logger.Info().
						Str("thread_id", threadID).
						Int("frame_count", frameIndex).
						Str("stream_end_reason", threadEventsStreamEndReason(err, ctx.Err())).
						Dur("stream_elapsed", time.Since(streamStarted)).
						Err(err).
						AnErr("ctx_err", ctx.Err()).
						Msg("agent thread events upstream stream ended")
					return
				}
				result.Err = err
				return
			}

			frameIndex++
			if frame.ID != "" {
				result.LastEventID = frame.ID
			}
			frameStarted := time.Now()
			parseStarted := time.Now()
			event, ok := fetchedThreadEventFromSSEFrame(frame)
			parseElapsed := time.Since(parseStarted)
			if !ok {
				if strings.TrimSpace(frame.Data) == "[DONE]" {
					return
				}
				log.Logger.Info().
					Str("thread_id", threadID).
					Int("frame_index", frameIndex).
					Str("upstream_event", strings.TrimSpace(frame.Event)).
					Int("data_bytes", len(strings.TrimSpace(frame.Data))).
					Dur("read_frame_elapsed", readElapsed).
					Dur("parse_frame_elapsed", parseElapsed).
					Dur("stream_elapsed", time.Since(streamStarted)).
					Msg("agent thread events upstream frame skipped")
				keepalive := threadEventStreamChunk{
					Keepalive:       true,
					UpstreamEventID: frame.ID,
					FrameIndex:      frameIndex,
					ReadElapsed:     readElapsed,
					ParseElapsed:    parseElapsed,
					FrameStarted:    frameStarted,
					StreamElapsed:   time.Since(streamStarted),
				}
				queueStarted := time.Now()
				select {
				case chunks <- keepalive:
					log.Logger.Info().
						Str("thread_id", threadID).
						Str("sse_endpoint", ":events").
						Str("upstream_event_id", frame.ID).
						Int("frame_index", frameIndex).
						Dur("queue_frontend_elapsed", time.Since(queueStarted)).
						Dur("stream_elapsed", time.Since(streamStarted)).
						Msg("agent thread events keepalive queued for frontend")
				case <-ctx.Done():
					return
				}
				if shouldContinue != nil && !shouldContinue("upstream_keepalive", nil) {
					return
				}
				continue
			}

			logUpstreamSSEData(":events", threadID, "", event.TaskID, event.EventName, event.RawFrame)
			chunk := threadEventStreamChunk{
				Event:           event,
				UpstreamEventID: frame.ID,
				FrameIndex:      frameIndex,
				ReadElapsed:     readElapsed,
				ParseElapsed:    parseElapsed,
				FrameStarted:    frameStarted,
				StreamElapsed:   time.Since(streamStarted),
			}
			queueStarted := time.Now()
			select {
			case chunks <- chunk:
				log.Logger.Info().
					Str("thread_id", threadID).
					Str("sse_endpoint", ":events").
					Str("task_id", event.TaskID).
					Str("event_name", event.EventName).
					Str("upstream_event_id", frame.ID).
					Int("frame_index", frameIndex).
					Int("data_bytes", len(event.RawFrame)).
					Dur("queue_frontend_elapsed", time.Since(queueStarted)).
					Dur("stream_elapsed", time.Since(streamStarted)).
					Msg("agent thread events frame queued for frontend")
			case <-ctx.Done():
				return
			}
			if isDoneThreadEvent(event) {
				result.StopReason = "done"
				log.Logger.Info().
					Str("thread_id", threadID).
					Str("task_id", event.TaskID).
					Str("event_name", event.EventName).
					Str("upstream_event_id", frame.ID).
					Int("frame_index", frameIndex).
					Msg("agent thread events upstream done received")
				return
			}
		}
	}()

	return chunks, done
}

func waitThreadEventStreamResult(done <-chan threadEventStreamResult) threadEventStreamResult {
	result, ok := <-done
	if !ok {
		return threadEventStreamResult{}
	}
	return result
}

func fetchedThreadEventFromSSEFrame(frame *sseFrame) (fetchedThreadEvent, bool) {
	if frame == nil {
		return fetchedThreadEvent{}, false
	}
	rawData := normalizeThreadEventRawData(frame.Data, frame.Event)
	if rawData == "" || rawData == "[DONE]" {
		return fetchedThreadEvent{}, false
	}
	payload := parseJSONValue(rawData)
	eventName := strings.TrimSpace(frame.Event)
	taskID := ""
	eventID := ""
	if payload != nil {
		taskID = extractStringByExactKeys(payload, "task_id", "current_task_id")
		eventID = extractStringByExactKeys(payload, "event_id")
		if name := extractStringByExactKeys(payload, "event_type", "kind", "event", "type"); name != "" {
			eventName = name
		}
	}
	if shouldSkipStreamData(eventName, payload, rawData) {
		return fetchedThreadEvent{}, false
	}
	return fetchedThreadEvent{
		EventID:   eventID,
		TaskID:    taskID,
		EventName: eventName,
		RawFrame:  rawData,
	}, true
}

func buildFetchedThreadEvents(events []map[string]any) ([]fetchedThreadEvent, error) {
	result := make([]fetchedThreadEvent, 0, len(events))
	for _, item := range events {
		if item == nil {
			continue
		}
		rawJSON, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		eventName := extractStringByExactKeys(item, "event_type", "kind", "event", "type")
		if shouldSkipStreamData(eventName, item, string(rawJSON)) {
			continue
		}
		result = append(result, fetchedThreadEvent{
			EventID:   extractStringByExactKeys(item, "event_id"),
			TaskID:    extractStringByExactKeys(item, "task_id", "current_task_id"),
			EventName: eventName,
			RawFrame:  string(rawJSON),
		})
	}
	return result, nil
}

func shouldSkipStreamData(eventName string, payload any, rawData string) bool {
	rawData = strings.TrimSpace(rawData)
	if rawData == "" || rawData == "[DONE]" || rawData == "null" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(eventName), "heartbeat") {
		return true
	}
	switch value := payload.(type) {
	case map[string]any:
		return len(value) == 0
	case []any:
		return len(value) == 0
	default:
		return false
	}
}

func isDoneThreadEvent(event fetchedThreadEvent) bool {
	if strings.EqualFold(strings.TrimSpace(event.EventName), "done") {
		return true
	}
	payload, ok := parseJSONValue(event.RawFrame).(map[string]any)
	if !ok {
		return false
	}
	rawType := extractStringByExactKeys(payload, "event_type", "event", "type")
	return strings.EqualFold(strings.TrimSpace(rawType), "done")
}

func threadEventStepID(event fetchedThreadEvent) string {
	payload := parseJSONValue(event.RawFrame)
	return extractStringByExactKeys(payload, "step_id", "step_run_id")
}

func threadEventsStreamEndReason(readErr, ctxErr error) string {
	if ctxErr != nil {
		return "context_canceled"
	}
	if errors.Is(readErr, io.EOF) {
		return "upstream_eof"
	}
	if readErr != nil {
		return "read_error"
	}
	return "unknown"
}

func monitorThreadEventsFlowStatus(
	ctx context.Context,
	cancelUpstream context.CancelFunc,
	r *http.Request,
	threadID string,
) (<-chan struct{}, <-chan struct{}) {
	flowStopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if shouldContinueThreadEvents(ctx, r, threadID, "idle_flow_status_check", nil) {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			close(flowStopped)
			cancelUpstream()
			log.Logger.Info().
				Str("thread_id", threadID).
				Str("reason", "flow_status_not_running").
				Msg("agent thread events closing upstream sse")
			return
		}
	}()
	return flowStopped, done
}

func shouldContinueThreadEvents(ctx context.Context, r *http.Request, threadID, reason string, cause error) bool {
	if ctx.Err() != nil {
		return false
	}
	flowStatus, err := fetchThreadFlowStatus(ctx, r, threadID)
	if err != nil {
		log.Logger.Warn().
			Err(err).
			Str("thread_id", threadID).
			Str("reason", reason).
			AnErr("stream_error", cause).
			Msg("agent thread events flow status check failed; keep stream alive")
		return true
	}
	streamAlive := shouldKeepThreadFlowStreamAlive(flowStatus)
	status := ""
	if flowStatus != nil {
		status = flowStatus.Status
	}
	log.Logger.Info().
		Str("thread_id", threadID).
		Str("reason", reason).
		Str("flow_status", status).
		Bool("flow_stream_alive", streamAlive).
		AnErr("stream_error", cause).
		Msg("agent thread events flow status checked")
	return streamAlive
}

func shouldKeepThreadFlowStreamAlive(flowStatus *threadFlowStatusResponse) bool {
	if flowStatus == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(flowStatus.Status)) {
	case "running", "pending", "paused":
		return true
	default:
		return false
	}
}

func sleepBeforeThreadEventsReconnect(ctx context.Context) bool {
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func fetchThreadFlowStatus(ctx context.Context, r *http.Request, threadID string) (*threadFlowStatusResponse, error) {
	thread, err := newEvoClient(forwardedUpstreamHeaders(r)).GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return threadFlowStatusFromEvo(thread), nil
}

func fetchThreadStatuses(ctx context.Context, r *http.Request, threads []orm.AgentThread) (map[string]threadFlowStatusResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := newEvoClient(forwardedUpstreamHeaders(r))
	result := make(map[string]threadFlowStatusResponse, len(threads))
	var firstErr error
	for _, localThread := range threads {
		threadID := strings.TrimSpace(localThread.ThreadID)
		if threadID == "" {
			continue
		}
		thread, err := client.GetThread(ctx, threadID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		result[threadID] = *threadFlowStatusFromEvo(thread)
	}
	return result, firstErr
}

func threadFlowStatusFromEvo(thread *evoThread) *threadFlowStatusResponse {
	if thread == nil {
		return nil
	}
	threadID := strings.TrimSpace(thread.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(thread.ID)
	}
	flowStatus := &threadFlowStatusResponse{
		ThreadID:      threadID,
		Status:        strings.TrimSpace(thread.Status),
		CurrentStep:   strings.TrimSpace(thread.CurrentStep),
		ReportReady:   strings.EqualFold(strings.TrimSpace(thread.Status), "ended"),
		LastError:     thread.LastError,
		ActiveTaskIDs: []string{},
	}
	if strings.EqualFold(flowStatus.Status, "running") && flowStatus.CurrentStep != "" {
		flowStatus.ActiveTaskIDs = []string{flowStatus.CurrentStep}
	}
	return flowStatus
}

func writeProxyResponse(w http.ResponseWriter, proxy *upstreamProxyResponse) {
	if proxy == nil {
		common.ReplyOK(w, map[string]any{})
		return
	}
	if strings.Contains(proxy.ContentType, "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(proxy.Body)
		return
	}
	if proxy.ContentType != "" {
		w.Header().Set("Content-Type", proxy.ContentType)
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	_, _ = io.WriteString(w, fmt.Sprint(proxy.Body))
}

func streamMessageRecords(
	r *http.Request,
	w http.ResponseWriter,
	flusher http.Flusher,
	db *gorm.DB,
	threadID, afterID string,
	session *activeMessageStream,
) {
	lastSent := afterID
	replayRoundID := ""
	var sub *messageStreamSubscription
	if session != nil {
		replayRoundID = session.roundID
		sub = session.subscribe()
		defer session.unsubscribe(sub)
	}

	replay := func() bool {
		for {
			if r.Context().Err() != nil {
				return false
			}
			records, err := listRecords(db, threadID, streamKindMessage, replayRoundID, lastSent, 200)
			if err != nil {
				log.Logger.Warn().Err(err).Str("thread_id", threadID).Str("stream_kind", streamKindMessage).Msg("load stored stream records failed")
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if len(records) == 0 {
				return true
			}
			for _, record := range records {
				lastSent = record.ID
				if shouldSkipStreamRecord(record) {
					continue
				}
				writeReplayFrame(w, flusher, record)
			}
		}
	}

	if !replay() {
		return
	}
	if session == nil || sub == nil {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-session.done:
			_ = replay()
			return
		case <-sub.heartbeats:
			if err := writeSSEKeepalive(w, flusher); err != nil {
				return
			}
		case record := <-sub.records:
			if record.ID <= lastSent {
				continue
			}
			lastSent = record.ID
			if shouldSkipStreamRecord(record) {
				continue
			}
			writeReplayFrame(w, flusher, record)
		}
	}
}

func streamStoredRecords(
	r *http.Request,
	w http.ResponseWriter,
	flusher http.Flusher,
	db *gorm.DB,
	threadID, streamKind, roundID, afterID string,
	session *activeMessageStream,
) {
	lastSent := afterID

	for {
		if r.Context().Err() != nil {
			return
		}

		records, err := listRecords(db, threadID, streamKind, roundID, lastSent, 200)
		if err != nil {
			log.Logger.Warn().Err(err).Str("thread_id", threadID).Str("stream_kind", streamKind).Msg("load stored stream records failed")
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if len(records) > 0 {
			for _, record := range records {
				lastSent = record.ID
				if shouldSkipStreamRecord(record) {
					continue
				}
				writeReplayFrame(w, flusher, record)
			}
			continue
		}

		if session == nil {
			return
		}
		select {
		case <-session.done:
			if session.Err() != nil {
				return
			}
			if trailing, err := listRecords(db, threadID, streamKind, roundID, lastSent, 200); err == nil {
				for _, record := range trailing {
					lastSent = record.ID
					if shouldSkipStreamRecord(record) {
						continue
					}
					writeReplayFrame(w, flusher, record)
				}
			}
			return
		default:
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func shouldSkipStreamRecord(record orm.AgentThreadRecord) bool {
	rawData := record.RawFrame
	if record.StreamKind == streamKindMessage {
		rawData = recordDataPayload(record)
	}
	return shouldSkipStreamData(record.EventName, parseJSONValue(rawData), rawData)
}

func decodeRequestBody(r *http.Request) (map[string]any, []byte, error) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, nil, err
	}
	bodyBytes = []byte(strings.TrimSpace(string(bodyBytes)))
	if len(bodyBytes) == 0 {
		return map[string]any{}, []byte("{}"), nil
	}
	var payload map[string]any
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, nil, err
	}
	return payload, bodyBytes, nil
}

func applyThreadCreateTitle(ctx context.Context, db *gorm.DB, payload map[string]any, now time.Time) {
	title := buildThreadCreateTitle(ctx, db, payload, now)
	if title == "" {
		return
	}
	payload["title"] = title
}

func buildThreadCreateTitle(ctx context.Context, db *gorm.DB, payload map[string]any, now time.Time) string {
	kbID := extractThreadCreateKnowledgeBaseID(payload)
	kbName := lookupThreadCreateKnowledgeBaseName(ctx, db, kbID)
	if kbName == "" {
		kbName = strings.TrimSpace(extractThreadCreatePayloadTitle(payload))
	}
	if kbName == "" {
		kbName = strings.TrimSpace(kbID)
	}
	if kbName == "" {
		return ""
	}

	date := now.Format("2006-01-02")
	suffix := "-" + date
	if strings.HasSuffix(kbName, suffix) {
		return kbName
	}
	return kbName + suffix
}

func extractThreadCreateKnowledgeBaseID(payload map[string]any) string {
	for _, rootKey := range []string{"inputs", "input", "config"} {
		if object, ok := payload[rootKey].(map[string]any); ok {
			if value := firstThreadCreateString(object, "kb_id", "knowledge_base_id", "knowledgeBaseId", "dataset_id", "datasetId"); value != "" {
				return value
			}
		}
	}
	return firstThreadCreateString(payload, "kb_id", "knowledge_base_id", "knowledgeBaseId", "dataset_id", "datasetId")
}

func extractThreadCreatePayloadTitle(payload map[string]any) string {
	return firstThreadCreateString(payload, "title", "thread_name", "name", "display_name")
}

func firstThreadCreateString(payload map[string]any, keys ...string) string {
	if payload == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if result := stringifyMatchedString(value); result != "" {
				return result
			}
		}
	}
	return ""
}

func lookupThreadCreateKnowledgeBaseName(ctx context.Context, db *gorm.DB, kbID string) string {
	kbID = strings.TrimSpace(kbID)
	if db == nil || kbID == "" {
		return ""
	}

	var ds orm.Dataset
	if err := db.WithContext(ctx).
		Where("(id = ? OR kb_id = ?) AND deleted_at IS NULL", kbID, kbID).
		First(&ds).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Logger.Warn().Err(err).Str("kb_id", kbID).Msg("lookup thread knowledge base name failed")
		}
		return ""
	}
	return strings.TrimSpace(ds.DisplayName)
}

func loadThread(db *gorm.DB, threadID string) (orm.AgentThread, error) {
	if db == nil {
		return orm.AgentThread{}, errors.New("store not initialized")
	}
	if threadID == "" {
		return orm.AgentThread{}, errors.New("thread_id required")
	}
	var thread orm.AgentThread
	if err := db.Where("thread_id = ?", threadID).First(&thread).Error; err != nil {
		return orm.AgentThread{}, err
	}
	return thread, nil
}

func loadUserThread(db *gorm.DB, r *http.Request, threadID string) (orm.AgentThread, error) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		return orm.AgentThread{}, errors.New("missing X-User-Id")
	}
	thread, err := loadThread(db, threadID)
	if err != nil {
		return orm.AgentThread{}, err
	}
	if strings.TrimSpace(thread.CreateUserID) != userID {
		return orm.AgentThread{}, gorm.ErrRecordNotFound
	}
	return thread, nil
}

func listThreads(db *gorm.DB, userID string, offset, pageSize int) ([]orm.AgentThread, int64, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, 0, errors.New("user_id required")
	}
	if offset < 0 {
		offset = 0
	}
	if pageSize <= 0 {
		pageSize = defaultThreadPageSize
	}
	if pageSize > maxThreadPageSize {
		pageSize = maxThreadPageSize
	}

	query := db.Model(&orm.AgentThread{}).Where("create_user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var threads []orm.AgentThread
	if err := query.Order("updated_at DESC").Offset(offset).Limit(pageSize).Find(&threads).Error; err != nil {
		return nil, 0, err
	}
	return threads, total, nil
}

func upsertThread(
	db *gorm.DB,
	threadID, currentTaskID, status, threadPayload, requestHash, userID, userName string,
) (orm.AgentThread, error) {
	now := time.Now().UTC()
	var thread orm.AgentThread
	err := db.Where("thread_id = ?", threadID).First(&thread).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.AgentThread{}, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		thread = orm.AgentThread{
			ThreadID:               threadID,
			CurrentTaskID:          currentTaskID,
			Status:                 status,
			ThreadPayload:          threadPayload,
			LastMessageRequestHash: requestHash,
			CreateUserID:           userID,
			CreateUserName:         userName,
			CreatedAt:              now,
			UpdatedAt:              now,
		}
		return thread, db.Create(&thread).Error
	}

	if currentTaskID != "" {
		thread.CurrentTaskID = currentTaskID
	}
	if status != "" {
		thread.Status = status
	}
	if threadPayload != "" {
		thread.ThreadPayload = threadPayload
	}
	if requestHash != "" {
		thread.LastMessageRequestHash = requestHash
	}
	if userID != "" {
		thread.CreateUserID = userID
	}
	if userName != "" {
		thread.CreateUserName = userName
	}
	thread.UpdatedAt = now
	return thread, db.Save(&thread).Error
}

func toThreadResponse(thread orm.AgentThread) threadResponse {
	return threadResponse{
		ThreadID:      thread.ThreadID,
		CurrentTaskID: thread.CurrentTaskID,
		Status:        thread.Status,
		ThreadPayload: threadPayloadValue(thread),
		CreatedAt:     thread.CreatedAt,
		UpdatedAt:     thread.UpdatedAt,
	}
}

func toRecordResponse(record orm.AgentThreadRecord) recordResponse {
	return recordResponse{
		ID:         record.ID,
		ThreadID:   record.ThreadID,
		StepID:     record.StepID,
		TaskID:     record.TaskID,
		StreamKind: record.StreamKind,
		EventName:  record.EventName,
		Payload:    recordPayloadValue(record),
		RawFrame:   record.RawFrame,
		CreatedAt:  record.CreatedAt,
	}
}

func toThreadStepResponse(step orm.AgentThreadStep) threadStepResponse {
	return threadStepResponse{
		ThreadID:      step.ThreadID,
		StepID:        step.StepID,
		Title:         step.Title,
		Status:        step.Status,
		Active:        step.Active,
		OrderIndex:    step.OrderIndex,
		EventCount:    step.EventCount,
		CurrentTaskID: step.CurrentTaskID,
		NextStepRunID: step.NextStepRunID,
		StartedAt:     step.StartedAt,
		EndedAt:       step.EndedAt,
		CreatedAt:     step.CreatedAt,
		UpdatedAt:     step.UpdatedAt,
	}
}

func replyThreadLoadError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, "thread not found", http.StatusNotFound)
	case err.Error() == "thread_id required":
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	case err.Error() == "missing X-User-Id":
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	case err.Error() == "store not initialized":
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
	default:
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "load thread failed", err), http.StatusInternalServerError)
	}
}
