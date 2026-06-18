package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	TaskID     string    `json:"task_id,omitempty"`
	StreamKind string    `json:"stream_kind"`
	EventName  string    `json:"event_name,omitempty"`
	Payload    any       `json:"payload"`
	RawFrame   string    `json:"raw_frame"`
	CreatedAt  time.Time `json:"created_at"`
}

type upstreamProxyResponse struct {
	Body        any
	ContentType string
}

type threadFlowStatusResponse struct {
	ThreadID           string   `json:"thread_id,omitempty"`
	Status             string   `json:"status,omitempty"`
	ActiveTaskIDs      []string `json:"active_task_ids,omitempty"`
	LatestAbtestID     any      `json:"latest_abtest_id,omitempty"`
	LatestAbtestStatus any      `json:"latest_abtest_status,omitempty"`
	ReportReady        bool     `json:"report_ready,omitempty"`
	PendingCheckpoint  any      `json:"pending_checkpoint,omitempty"`
}

type threadStatusesResponse struct {
	Total   int                        `json:"total,omitempty"`
	Counts  map[string]int             `json:"counts,omitempty"`
	Threads []threadFlowStatusResponse `json:"threads,omitempty"`
}

var threadEventsKeepaliveInterval = time.Second

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
		statusByThread, statusErr = fetchThreadStatuses(r.Context(), r)
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
	applyThreadCreateTitle(r.Context(), db, requestPayload, time.Now())
	if err := attachThreadModelConfig(r.Context(), db, store.UserID(r), requestPayload); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "load llm config failed", err), http.StatusInternalServerError)
		return
	}

	var creationGuard *userActiveThreadCreationGuard
	// Temporary integration bypass: comment this guard block to disable single-active-thread enforcement.
	if guard, guardErr := reserveUserActiveThreadCreation(r.Context(), db, r); guardErr != nil {
		replyUserActiveThreadError(w, guardErr)
		return
	} else {
		creationGuard = guard
		defer creationGuard.Abort(db)
	}

	var upstreamRaw json.RawMessage
	headers := forwardedUpstreamHeaders(r)
	if err := common.ApiPost(r.Context(), threadCreateURL(), requestPayload, headers, &upstreamRaw, 30*time.Second); err != nil {
		common.ReplyErrWithData(w, "create upstream thread failed", map[string]any{"detail": err.Error()}, http.StatusBadGateway)
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

	thread, err := upsertThread(db, threadID, "", "created", string(upstreamRaw), "", store.UserID(r), store.UserName(r))
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
	common.ReplyOK(w, map[string]any{"thread": toThreadResponse(thread)})
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
	afterID := parseAfterID(r)
	limit := parseRecordLimit(r.URL.Query().Get("limit"))

	records, err := listRecords(db, threadID, streamKind, "", afterID, limit+1)
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
		"stream_kind":   streamKind,
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
		if err := attachThreadModelConfig(r.Context(), db, store.UserID(r), requestPayload); err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "load llm config failed", err), http.StatusInternalServerError)
			return
		}
		requestBytes, err := json.Marshal(requestPayload)
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

	upstreamURL := threadEventsURL(threadID)
	lastUpstreamEventID := strings.TrimSpace(r.URL.Query().Get("since"))
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
		resp, err := openThreadEventsStream(upstreamCtx, r, threadID, lastUpstreamEventID)
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
			resp.Body,
			&lastUpstreamEventID,
			func(reason string, cause error) bool {
				return shouldContinueThreadEvents(r.Context(), r, threadID, reason, cause)
			},
		)
		_ = resp.Body.Close()
		cancelUpstream()
		<-monitorDone
		if streamErr != nil {
			log.Logger.Warn().Err(streamErr).Str("thread_id", threadID).Msg("consume upstream thread events stream failed")
		}
		emitPendingCheckpointWait(w, flusher, r, threadID)
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

func emitPendingCheckpointWait(w http.ResponseWriter, flusher http.Flusher, r *http.Request, threadID string) {
	flowStatus, err := fetchThreadFlowStatus(r.Context(), r, threadID)
	if err != nil || flowStatus == nil {
		if err != nil {
			log.Logger.Warn().Err(err).Str("thread_id", threadID).Msg("fetch checkpoint flow status failed")
		}
		return
	}
	status := strings.ToLower(strings.TrimSpace(flowStatus.Status))
	if status != "waiting_checkpoint" && status != "paused" {
		return
	}
	checkpoint, ok := flowStatus.PendingCheckpoint.(map[string]any)
	if !ok || len(checkpoint) == 0 {
		return
	}
	payload := make(map[string]any, len(checkpoint)+2)
	payload["type"] = "checkpoint.wait"
	payload["thread_id"] = threadID
	for key, value := range checkpoint {
		payload[key] = value
	}
	writeNamedSSE(w, flusher, "", payload)
}

func GetThreadResultDatasets(w http.ResponseWriter, r *http.Request) {
	getThreadResults(w, r, "datasets")
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
	proxyThreadGet(w, r, func(threadID string) string { return threadFlowStatusURL(threadID) }, "fetch thread flow status failed")
}
func GetThreadArtifact(w http.ResponseWriter, r *http.Request) {
	artifactID := strings.TrimSpace(mux.Vars(r)["artifact_id"])
	if artifactID == "" {
		common.ReplyErr(w, "artifact_id required", http.StatusBadRequest)
		return
	}
	proxyThreadGet(w, r, func(threadID string) string { return threadArtifactURL(threadID, artifactID) }, "fetch thread artifact failed")
func GetThreadResultTrace(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	traceID := strings.TrimSpace(mux.Vars(r)["trace_id"])
	if threadID == "" || traceID == "" {
		common.ReplyErr(w, "thread_id and trace_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	proxy, statusCode, err := fetchUpstreamProxy(r.Context(), r, threadResultTraceURL(threadID, traceID))
	if err != nil {
		common.ReplyErrWithData(w, "fetch trace result failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	writeProxyResponse(w, proxy)
}
func GetThreadResultTraceCompare(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	aTraceID := strings.TrimSpace(r.URL.Query().Get("a"))
	bTraceID := strings.TrimSpace(r.URL.Query().Get("b"))
	if threadID == "" || aTraceID == "" || bTraceID == "" {
		common.ReplyErr(w, "thread_id, a and b required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	proxy, statusCode, err := fetchUpstreamProxy(r.Context(), r, threadResultTraceCompareURL(threadID, aTraceID, bTraceID))
	if err != nil {
		common.ReplyErrWithData(w, "fetch trace comparison failed", map[string]any{"detail": err.Error()}, statusCode)
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

func GetReportContent(w http.ResponseWriter, r *http.Request) {
	reportID := strings.TrimSpace(mux.Vars(r)["report_id"])
	if reportID == "" {
		common.ReplyErr(w, "report_id required", http.StatusBadRequest)
		return
	}
	proxy, statusCode, err := fetchUpstreamProxy(r.Context(), r, reportContentURL(reportID, strings.TrimSpace(r.URL.Query().Get("fmt"))))
	if err != nil {
		common.ReplyErrWithData(w, "fetch report content failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	writeProxyResponse(w, proxy)
}

func GetDiffContent(w http.ResponseWriter, r *http.Request) {
	applyID := strings.TrimSpace(mux.Vars(r)["apply_id"])
	filename := strings.TrimSpace(mux.Vars(r)["filename"])
	if applyID == "" || filename == "" {
		common.ReplyErr(w, "apply_id and filename required", http.StatusBadRequest)
		return
	}
	proxy, statusCode, err := fetchUpstreamProxy(r.Context(), r, diffContentURL(applyID, filename))
	if err != nil {
		common.ReplyErrWithData(w, "fetch diff content failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	writeProxyResponse(w, proxy)
}

func GetAgentFileContent(w http.ResponseWriter, r *http.Request) {
	body, _, err := decodeRequestBody(r)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	path := strings.TrimSpace(caseCSVScalarString(body["path"]))
	if path == "" {
		common.ReplyErr(w, "path required", http.StatusBadRequest)
		return
	}
	result, err := buildAgentFileContentResult(path)
	if err != nil {
		common.ReplyErrWithData(w, "read agent file content failed", map[string]any{"detail": err.Error()}, http.StatusInternalServerError)
		return
	}
	common.ReplyJSON(w, result)
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
	proxy, statusCode, err := fetchUpstreamProxy(r.Context(), r, threadResultsURL(threadID, resultKind))
	if err != nil {
		common.ReplyErrWithData(w, "fetch thread results failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	if proxy != nil {
		switch resultKind {
		case "datasets":
			if strings.Contains(proxy.ContentType, "application/json") {
				if _, found, csvErr := attachCaseCSVFileURL(r.Context(), proxy.Body, caseCSVOptions{
					ThreadID:   threadID,
					ResultKind: resultKind,
					FieldNames: []string{"case", "cases", "eval_data", "data", "items", "records"},
				}); csvErr != nil {
					log.Logger.Warn().Err(csvErr).Str("thread_id", threadID).Str("result_kind", resultKind).Bool("case_field_found", found).Msg("attach case csv file url failed")
				}
			}
		case "eval-reports", "abtests":
			if strings.Contains(proxy.ContentType, "application/json") {
				if resultKind == "eval-reports" {
					if found, summaryErr := attachEvalReportSummaryResult(proxy.Body, threadID); summaryErr != nil {
						log.Logger.Warn().Err(summaryErr).Str("thread_id", threadID).Bool("eval_report_found", found).Msg("attach eval report summary result failed")
					}
				}
				if _, found, reportErr := attachCaseDetailsReportResult(r.Context(), proxy.Body, caseDetailsReportOptions{
					ThreadID:   threadID,
					ResultKind: resultKind,
				}); reportErr != nil {
					log.Logger.Warn().Err(reportErr).Str("thread_id", threadID).Str("result_kind", resultKind).Bool("case_details_found", found).Msg("attach case details report result failed")
				}
			}
		case "analysis-reports":
			body, found, resultErr := buildAnalysisMarkdownResult(proxy.Body)
			if resultErr != nil {
				common.ReplyErrWithData(w, "read analysis report content failed", map[string]any{"detail": resultErr.Error()}, http.StatusInternalServerError)
				return
			}
			if found {
				proxy.Body = body
				proxy.ContentType = "application/json"
			}
		case "diffs":
			body, found, resultErr := buildDiffJSONResult(proxy.Body)
			if resultErr != nil {
				common.ReplyErrWithData(w, "read diff result content failed", map[string]any{"detail": resultErr.Error()}, http.StatusInternalServerError)
				return
			}
			if found {
				proxy.Body = body
				proxy.ContentType = "application/json"
			}
		}
	}
	writeProxyResponse(w, proxy)
}

func proxyThreadGet(w http.ResponseWriter, r *http.Request, urlFor func(string) string, errorMessage string) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if threadID == "" {
		common.ReplyErr(w, "thread_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	proxy, statusCode, err := fetchUpstreamProxy(r.Context(), r, urlFor(threadID))
	if err != nil {
		common.ReplyErrWithData(w, errorMessage, map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	writeProxyResponse(w, proxy)
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
		if attachErr := attachThreadModelConfig(r.Context(), store.DB(), store.UserID(r), payload); attachErr != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "load llm config failed", attachErr), http.StatusInternalServerError)
			return
		}
		proxy, statusCode, err = postUpstreamProxy(r.Context(), r, threadActionURL(threadID, action), payload)
	} else {
		proxy, statusCode, err = postUpstreamProxy(r.Context(), r, threadActionURL(threadID, action), nil)
	}
	if err != nil {
		common.ReplyErrWithData(w, "post thread action failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	writeProxyResponse(w, proxy)
}

type fetchedThreadEvent struct {
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
}

func openThreadEventsStream(ctx context.Context, r *http.Request, threadID, lastEventID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, threadEventsURL(threadID), nil)
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
	chunk threadEventStreamChunk,
	lastUpstreamEventID *string,
) error {
	if chunk.UpstreamEventID != "" && lastUpstreamEventID != nil {
		*lastUpstreamEventID = chunk.UpstreamEventID
	}
	downstreamFrame := buildThreadEventFrame(chunk.Event.RawFrame)
	writeStarted := time.Now()
	bytesWritten, writeErr := io.WriteString(w, downstreamFrame)
	flushStarted := time.Now()
	flusher.Flush()
	flushElapsed := time.Since(flushStarted)
	writeElapsed := time.Since(writeStarted)
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
		Err(writeErr).
		Msg("agent thread events frame forwarded")
	if writeErr != nil {
		return writeErr
	}

	saveStarted := time.Now()
	_, saveCreated, saveErr := saveThreadRecord(
		db,
		threadID,
		"",
		chunk.Event.TaskID,
		streamKindThreadEvent,
		chunk.Event.EventName,
		chunk.Event.RawFrame,
		chunk.Event.RawFrame,
	)
	saveElapsed := time.Since(saveStarted)
	if saveErr != nil {
		log.Logger.Warn().Err(saveErr).Str("thread_id", threadID).Msg("save thread event record failed")
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
		Err(firstNonNil(saveErr, updateErr)).
		Msg("agent thread events frame persisted")
	return nil
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
	rawData := strings.TrimSpace(frame.Data)
	if rawData == "" || rawData == "[DONE]" {
		return fetchedThreadEvent{}, false
	}
	payload := parseJSONValue(rawData)
	eventName := strings.TrimSpace(frame.Event)
	taskID := ""
	if payload != nil {
		taskID = extractStringByExactKeys(payload, "task_id", "current_task_id")
		if name := extractStringByExactKeys(payload, "kind", "event", "type"); name != "" {
			eventName = name
		}
	}
	if shouldSkipStreamData(eventName, payload, rawData) {
		return fetchedThreadEvent{}, false
	}
	return fetchedThreadEvent{
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
		eventName := extractStringByExactKeys(item, "kind", "event", "type")
		if shouldSkipStreamData(eventName, item, string(rawJSON)) {
			continue
		}
		result = append(result, fetchedThreadEvent{
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

func firstNonNil(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
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
	case "running", "pending", "waiting_checkpoint", "paused":
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
	headers := forwardedUpstreamHeaders(r)
	var flowStatus threadFlowStatusResponse
	if err := common.ApiGet(ctx, threadFlowStatusURL(threadID), headers, &flowStatus, 15*time.Second); err != nil {
		return nil, err
	}
	return &flowStatus, nil
}

func fetchThreadStatuses(ctx context.Context, r *http.Request) (map[string]threadFlowStatusResponse, error) {
	headers := forwardedUpstreamHeaders(r)
	var statuses threadStatusesResponse
	if err := common.ApiGet(ctx, threadStatusesURL(), headers, &statuses, 5*time.Second); err != nil {
		return nil, err
	}
	result := make(map[string]threadFlowStatusResponse, len(statuses.Threads))
	for _, status := range statuses.Threads {
		threadID := strings.TrimSpace(status.ThreadID)
		if threadID == "" {
			continue
		}
		result[threadID] = status
	}
	return result, nil
}

func fetchUpstreamProxy(ctx context.Context, r *http.Request, targetURL string) (*upstreamProxyResponse, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	for key, value := range forwardedUpstreamHeaders(r) {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, http.StatusBadGateway, fmt.Errorf("%s", strings.TrimSpace(string(bodyBytes)))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var payload any
		if err := json.Unmarshal(bodyBytes, &payload); err == nil {
			return &upstreamProxyResponse{Body: payload, ContentType: "application/json"}, http.StatusOK, nil
		}
	}
	return &upstreamProxyResponse{Body: string(bodyBytes), ContentType: contentType}, http.StatusOK, nil
}

func postUpstreamProxy(ctx context.Context, r *http.Request, targetURL string, payload map[string]any) (*upstreamProxyResponse, int, error) {
	body := ""
	if payload != nil {
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		body = string(bodyBytes)
	} else {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		body = strings.TrimSpace(string(bodyBytes))
	}
	var reqBody io.Reader = http.NoBody
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, reqBody)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	for key, value := range forwardedUpstreamHeaders(r) {
		req.Header.Set(key, value)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, http.StatusBadGateway, fmt.Errorf("%s", strings.TrimSpace(string(respBody)))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var payload any
		if err := json.Unmarshal(respBody, &payload); err == nil {
			return &upstreamProxyResponse{Body: payload, ContentType: "application/json"}, http.StatusOK, nil
		}
	}
	return &upstreamProxyResponse{Body: string(respBody), ContentType: contentType}, http.StatusOK, nil
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
		TaskID:     record.TaskID,
		StreamKind: record.StreamKind,
		EventName:  record.EventName,
		Payload:    recordPayloadValue(record),
		RawFrame:   record.RawFrame,
		CreatedAt:  record.CreatedAt,
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
