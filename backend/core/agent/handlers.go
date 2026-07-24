package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type upstreamProxyResponse struct {
	Body        any
	BodyBytes   []byte
	ContentType string
	Header      http.Header
	StatusCode  int
}

type threadFlowStatusResponse struct {
	ThreadID      string   `json:"thread_id,omitempty"`
	Status        string   `json:"status,omitempty"`
	CurrentStep   string   `json:"current_step,omitempty"`
	ActiveTaskIDs []string `json:"active_task_ids,omitempty"`
	ReportReady   bool     `json:"report_ready,omitempty"`
	LastError     any      `json:"last_error,omitempty"`
}

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

func ListThreadSteps(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	proxyEvoResponse(w, r, http.MethodGet, threadProxyPath(threadID, "/steps"), cloneURLValues(r.URL.Query()), nil, "application/json")
}

func StreamThreadMessages(w http.ResponseWriter, r *http.Request) {
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
	if err := ensureUserCanActivateThread(r.Context(), db, r, threadID); err != nil {
		writeUserActiveThreadSSEError(w, threadID, err)
		return
	}
	proxyEvoResponse(w, r, http.MethodPost, threadProxyPath(threadID, "/messages"), cloneURLValues(r.URL.Query()), r.Body, "text/event-stream")
}

func GetThreadMessages(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	proxyEvoResponse(w, r, http.MethodGet, threadProxyPath(threadID, "/messages"), cloneURLValues(r.URL.Query()), nil, "application/json")
}

func GetThreadEvalGateBadCases(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	version := strings.TrimSpace(mux.Vars(r)["version"])
	path := threadProxyPath(threadID, "/gates/eval/versions/"+url.PathEscape(version)+"/bad-cases")
	proxyEvoResponse(w, r, http.MethodGet, path, cloneURLValues(r.URL.Query()), nil, "application/json")
}

func GetThreadABTestGateCaseDetails(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	version := strings.TrimSpace(mux.Vars(r)["version"])
	path := threadProxyPath(threadID, "/gates/abtest/versions/"+url.PathEscape(version)+"/case-details")
	proxyEvoResponse(w, r, http.MethodGet, path, cloneURLValues(r.URL.Query()), nil, "application/json")
}

func GetThreadTraceDetail(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	traceID := strings.TrimSpace(mux.Vars(r)["trace_id"])
	path := threadProxyPath(threadID, "/results/traces/"+url.PathEscape(traceID))
	proxyEvoResponse(w, r, http.MethodGet, path, cloneURLValues(r.URL.Query()), nil, "application/json")
}

func CompareThreadTraces(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	proxyEvoResponse(w, r, http.MethodGet, threadProxyPath(threadID, "/results/traces:compare"), cloneURLValues(r.URL.Query()), nil, "application/json")
}

func StreamThreadEvents(w http.ResponseWriter, r *http.Request) {
	streamThreadEvents(w, r, "")
}

func StreamThreadEventTrace(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	proxyEvoResponse(w, r, http.MethodGet, threadProxyPath(threadID, "/event-trace:stream"), cloneURLValues(r.URL.Query()), nil, "text/event-stream")
}

func streamThreadEvents(w http.ResponseWriter, r *http.Request, stepID string) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	query := cloneURLValues(r.URL.Query())
	if strings.TrimSpace(stepID) != "" {
		if query == nil {
			query = url.Values{}
		}
		query.Set("step_id", strings.TrimSpace(stepID))
	}
	proxyEvoResponse(w, r, http.MethodGet, threadProxyPath(threadID, "/events:stream"), query, nil, "text/event-stream")
}

func ListThreadGates(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	proxyEvoResponse(w, r, http.MethodGet, threadProxyPath(threadID, "/gates"), cloneURLValues(r.URL.Query()), nil, "application/json")
}

func GetThreadGateContent(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	vars := mux.Vars(r)
	path := fmt.Sprintf(
		"%s/gates/%s/versions/%s",
		threadProxyPath(threadID, ""),
		url.PathEscape(strings.TrimSpace(vars["step"])),
		url.PathEscape(strings.TrimSpace(vars["version"])),
	)
	proxyEvoResponse(w, r, http.MethodGet, path, cloneURLValues(r.URL.Query()), nil, "application/json")
}

func DownloadThreadGate(w http.ResponseWriter, r *http.Request) {
	threadID, ok := ownerCheckedThreadID(w, r)
	if !ok {
		return
	}
	vars := mux.Vars(r)
	path := fmt.Sprintf(
		"%s/gates/%s/versions/%s:download",
		threadProxyPath(threadID, ""),
		url.PathEscape(strings.TrimSpace(vars["step"])),
		url.PathEscape(strings.TrimSpace(vars["version"])),
	)
	proxyEvoResponse(w, r, http.MethodGet, path, cloneURLValues(r.URL.Query()), nil, "application/octet-stream")
}

func ListCandidates(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.URL.Query().Get("thread_id"))
	if threadID == "" {
		common.ReplyErr(w, "thread_id query is required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	proxyEvoResponse(w, r, http.MethodGet, "/candidates", cloneURLValues(r.URL.Query()), nil, "application/json")
}

func GetCandidate(w http.ResponseWriter, r *http.Request) {
	candidateID := decodePathValue(strings.TrimSpace(mux.Vars(r)["candidate_id"]))
	if candidateID == "" {
		common.ReplyErr(w, "candidate_id required", http.StatusBadRequest)
		return
	}
	threadID := candidateThreadID(candidateID)
	if threadID == "" {
		common.ReplyErr(w, "candidate_id must include thread_id prefix", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}
	proxyEvoResponse(w, r, http.MethodGet, "/candidates/"+url.PathEscape(candidateID), cloneURLValues(r.URL.Query()), nil, "application/json")
}

func GetRouterStatus(w http.ResponseWriter, r *http.Request) {
	proxyEvoResponse(w, r, http.MethodGet, "/router/status", cloneURLValues(r.URL.Query()), nil, "application/json")
}

func ListRouterAlgorithms(w http.ResponseWriter, r *http.Request) {
	proxyEvoResponse(w, r, http.MethodGet, "/router/algorithms", cloneURLValues(r.URL.Query()), nil, "application/json")
}

func RegisterRouterAlgorithm(w http.ResponseWriter, r *http.Request) {
	proxyEvoResponse(w, r, http.MethodPost, "/router/algorithms", cloneURLValues(r.URL.Query()), r.Body, "application/json")
}

func PostRouterAlgorithmAction(w http.ResponseWriter, r *http.Request) {
	algorithmID := strings.TrimSpace(mux.Vars(r)["algorithm_id"])
	if algorithmID == "" {
		common.ReplyErr(w, "algorithm_id required", http.StatusBadRequest)
		return
	}
	proxyEvoResponse(w, r, http.MethodPost, "/router/algorithms/"+url.PathEscape(algorithmID)+"/action", cloneURLValues(r.URL.Query()), r.Body, "application/json")
}

func DeleteRouterAlgorithm(w http.ResponseWriter, r *http.Request) {
	algorithmID := strings.TrimSpace(mux.Vars(r)["algorithm_id"])
	if algorithmID == "" {
		common.ReplyErr(w, "algorithm_id required", http.StatusBadRequest)
		return
	}
	proxyEvoResponse(w, r, http.MethodDelete, "/router/algorithms/"+url.PathEscape(algorithmID), cloneURLValues(r.URL.Query()), nil, "application/json")
}

func GetRouterABStrategy(w http.ResponseWriter, r *http.Request) {
	proxyEvoResponse(w, r, http.MethodGet, "/router/ab-strategy", cloneURLValues(r.URL.Query()), nil, "application/json")
}

func PutRouterABStrategy(w http.ResponseWriter, r *http.Request) {
	proxyEvoResponse(w, r, http.MethodPut, "/router/ab-strategy", cloneURLValues(r.URL.Query()), r.Body, "application/json")
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

func candidateThreadID(candidateID string) string {
	candidateID = decodePathValue(candidateID)
	threadID, _, _ := strings.Cut(candidateID, ":")
	if threadID == candidateID {
		return ""
	}
	return strings.TrimSpace(threadID)
}

func decodePathValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if decoded, err := url.PathUnescape(raw); err == nil {
		return strings.TrimSpace(decoded)
	}
	return raw
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
	if statusCode >= 200 && statusCode < 300 {
		syncThreadAfterAction(r.Context(), r, threadID, action)
	} else if action == "start" || action == "retry" || action == "continue" {
		if finishErr := markUserActiveThreadFinished(store.DB(), threadID); finishErr != nil {
			log.Logger.Warn().Err(finishErr).Str("thread_id", threadID).Str("action", action).Msg("release active thread after rejected action failed")
		}
	}
	writeProxyResponse(w, proxy)
}

func syncThreadAfterAction(ctx context.Context, r *http.Request, threadID, action string) {
	db := store.DB()
	if db == nil {
		return
	}
	flowStatus, err := fetchThreadFlowStatus(ctx, r, threadID)
	if err != nil {
		log.Logger.Warn().Err(err).Str("thread_id", threadID).Str("action", action).Msg("sync thread status after action failed")
		if action == "cancel" {
			_ = markUserActiveThreadFinished(db, threadID)
		}
		return
	}
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if flowStatus != nil {
		if status := strings.TrimSpace(flowStatus.Status); status != "" {
			updates["status"] = status
		}
		if currentStep := strings.TrimSpace(flowStatus.CurrentStep); currentStep != "" {
			updates["current_task_id"] = currentStep
		}
	}
	if len(updates) > 1 {
		if err := db.Model(&orm.AgentThread{}).Where("thread_id = ?", threadID).Updates(updates).Error; err != nil {
			log.Logger.Warn().Err(err).Str("thread_id", threadID).Str("action", action).Msg("update local thread status after action failed")
		}
	}
	if action == "cancel" || isTerminalThreadFlowStatus(flowStatus) {
		if err := markUserActiveThreadFinished(db, threadID); err != nil {
			log.Logger.Warn().Err(err).Str("thread_id", threadID).Str("action", action).Msg("mark active thread finished after action failed")
		}
	}
}

func isTerminalThreadFlowStatus(flowStatus *threadFlowStatusResponse) bool {
	if flowStatus == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(flowStatus.Status)) {
	case "ended", "failed", "cancelled", "canceled", "completed", "succeeded":
		return true
	default:
		return false
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
