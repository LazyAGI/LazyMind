package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/modelconfig"
)

const (
	streamKindMessage     = "message"
	streamKindThreadEvent = "thread_event"

	defaultRecordLimit = 100
	maxRecordLimit     = 1000

	defaultThreadPageSize = 20
	maxThreadPageSize     = 100
)

var recordIDCounter atomic.Uint64

type sseFrame struct {
	ID    string
	Event string
	Data  string
	Raw   string
}

func newStreamRecordID() string {
	return fmt.Sprintf("%020d%06d", time.Now().UnixNano(), recordIDCounter.Add(1)%1000000)
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func agentServiceEndpoint() string {
	return common.EvoServiceEndpoint()
}

func threadCreateURL() string {
	return common.JoinURL(agentServiceEndpoint(), "/v1/evo/threads")
}

func threadStatusesURL() string {
	return common.JoinURL(agentServiceEndpoint(), "/v1/evo/threads/statuses")
}

func threadMessagesURL(threadID string) string {
	return common.JoinURL(agentServiceEndpoint(), "/v1/evo/threads/"+url.PathEscape(threadID)+"/messages")
}

func threadActionURL(threadID, action string) string {
	return common.JoinURL(
		agentServiceEndpoint(),
		"/v1/evo/threads/"+url.PathEscape(threadID)+"/"+strings.Trim(strings.TrimSpace(action), "/"),
	)
}

func threadDeleteURL(threadID string) string {
	return common.JoinURL(agentServiceEndpoint(), "/v1/evo/threads/"+url.PathEscape(threadID))
}

func threadFlowStatusURL(threadID string) string {
	return common.JoinURL(agentServiceEndpoint(), "/v1/evo/threads/"+url.PathEscape(threadID)+"/flow-status")
}

func threadEventsURL(threadID string) string {
	return common.JoinURL(agentServiceEndpoint(), "/v1/evo/threads/"+url.PathEscape(threadID)+"/events")
}

func threadStepEventsURL(threadID, stepID string) string {
	return common.JoinURL(
		agentServiceEndpoint(),
		"/v1/evo/threads/"+url.PathEscape(threadID)+"/events/"+url.PathEscape(stepID),
	)
}

func threadArtifactURL(threadID, artifactID string) string {
	return common.JoinURL(
		agentServiceEndpoint(),
		"/v1/evo/threads/"+url.PathEscape(threadID)+"/artifacts/"+url.PathEscape(artifactID),
	)
}

func threadResultsURL(threadID, resultKind string) string {
	return common.JoinURL(
		agentServiceEndpoint(),
		"/v1/evo/threads/"+url.PathEscape(threadID)+"/results/"+strings.Trim(strings.TrimSpace(resultKind), "/"),
	)
}

func threadResultTraceURL(threadID, traceID string) string {
	return common.JoinURL(
		agentServiceEndpoint(),
		"/v1/evo/threads/"+url.PathEscape(threadID)+"/results/traces/"+url.PathEscape(traceID),
	)
}

func threadResultTraceCompareURL(threadID, aTraceID, bTraceID string) string {
	base := common.JoinURL(agentServiceEndpoint(), "/v1/evo/threads/"+url.PathEscape(threadID)+"/results/traces-compare")
	return base + "?a=" + url.QueryEscape(aTraceID) + "&b=" + url.QueryEscape(bTraceID)
}

func reportContentURL(reportID, format string) string {
	base := common.JoinURL(agentServiceEndpoint(), "/v1/evo/reports/"+url.PathEscape(reportID)+"/content")
	if strings.TrimSpace(format) == "" {
		return base
	}
	return base + "?fmt=" + url.QueryEscape(format)
}

func diffContentURL(applyID, filename string) string {
	return common.JoinURL(agentServiceEndpoint(), "/v1/evo/diffs/"+url.PathEscape(applyID)+"/"+url.PathEscape(filename))
}

func forwardedUpstreamHeaders(r *http.Request) map[string]string {
	headers := map[string]string{
		"Accept": "application/json",
	}
	for _, key := range []string{"Authorization", "X-User-Id", "X-User-Name", "X-Request-Id"} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			headers[key] = value
		}
	}
	return headers
}

func attachThreadModelConfig(ctx context.Context, db *gorm.DB, userID string, payload map[string]any) error {
	if payload == nil {
		return nil
	}
	llmConfig, err := modelconfig.LoadLLMConfig(ctx, db, userID)
	if err != nil {
		return err
	}
	if len(llmConfig) > 0 {
		payload["llm_config"] = llmConfig
	}
	return nil
}

func hasThreadEvoLLMConfig(payload map[string]any) bool {
	llmConfig, ok := payload["llm_config"].(map[string]any)
	if !ok {
		return false
	}
	for key, value := range llmConfig {
		if !strings.EqualFold(strings.TrimSpace(key), "evo_llm") {
			continue
		}
		roleConfig, ok := value.(map[string]any)
		return ok && len(roleConfig) > 0
	}
	return false
}

func parseRecordLimit(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultRecordLimit
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return defaultRecordLimit
	}
	if value > maxRecordLimit {
		return maxRecordLimit
	}
	return value
}

func parseAfterID(r *http.Request) string {
	if value := strings.TrimSpace(r.URL.Query().Get("after_id")); value != "" {
		return value
	}
	return strings.TrimSpace(r.Header.Get("Last-Event-ID"))
}

func parseThreadPageSize(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultThreadPageSize
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return defaultThreadPageSize
	}
	if value > maxThreadPageSize {
		return maxThreadPageSize
	}
	return value
}

func parseThreadPageToken(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid page_token")
	}
	return value, nil
}

func ensureSSEHeaders(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	return flusher, true
}

func decodeJSONArrayObjects(body []byte) ([]map[string]any, error) {
	if strings.TrimSpace(string(body)) == "" {
		return []map[string]any{}, nil
	}
	var array []map[string]any
	if err := json.Unmarshal(body, &array); err == nil {
		return array, nil
	}

	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	if items := extractArrayObjects(root, []string{"events", "items", "data"}); len(items) > 0 {
		return items, nil
	}
	return nil, fmt.Errorf("no event list found in upstream payload")
}

func extractArrayObjects(root any, keys []string) []map[string]any {
	switch value := root.(type) {
	case []any:
		items := make([]map[string]any, 0, len(value))
		for _, item := range value {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			items = append(items, object)
		}
		return items
	case map[string]any:
		for _, key := range keys {
			if child, ok := value[key]; ok {
				if items := extractArrayObjects(child, keys); len(items) > 0 {
					return items
				}
			}
		}
		for _, child := range value {
			if items := extractArrayObjects(child, keys); len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

func extractStringByKeys(root any, keys ...string) string {
	lookup := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		lookup[key] = struct{}{}
	}
	return walkString(root, lookup)
}

func extractStringByExactKeys(root any, keys ...string) string {
	lookup := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		lookup[key] = struct{}{}
	}
	return walkStringByExactKeys(root, lookup)
}

func walkStringByExactKeys(root any, lookup map[string]struct{}) string {
	switch value := root.(type) {
	case map[string]any:
		for key, child := range value {
			if _, ok := lookup[key]; ok {
				if result := stringifyMatchedString(child); result != "" {
					return result
				}
			}
		}
		for _, child := range value {
			if result := walkStringByExactKeys(child, lookup); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range value {
			if result := walkStringByExactKeys(child, lookup); result != "" {
				return result
			}
		}
	}
	return ""
}

func stringifyMatchedString(root any) string {
	switch value := root.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64, bool, int, int64, uint64:
		return strings.TrimSpace(fmt.Sprint(value))
	default:
		return ""
	}
}

func walkString(root any, lookup map[string]struct{}) string {
	switch value := root.(type) {
	case string:
		return strings.TrimSpace(value)
	case map[string]any:
		for key, child := range value {
			if _, ok := lookup[key]; ok {
				if result := walkString(child, lookup); result != "" {
					return result
				}
			}
		}
		for _, child := range value {
			if result := walkString(child, lookup); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range value {
			if result := walkString(child, lookup); result != "" {
				return result
			}
		}
	}
	return ""
}

func parseJSONValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func extractPreferredText(root any, keys ...string) string {
	lookup := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		lookup[key] = struct{}{}
	}
	return walkPreferredText(root, lookup)
}

func walkPreferredText(root any, lookup map[string]struct{}) string {
	switch value := root.(type) {
	case string:
		return strings.TrimSpace(value)
	case map[string]any:
		for key, child := range value {
			if _, ok := lookup[key]; ok {
				if result := walkPreferredText(child, lookup); result != "" {
					return result
				}
			}
		}
		for _, child := range value {
			if result := walkPreferredText(child, lookup); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range value {
			if result := walkPreferredText(child, lookup); result != "" {
				return result
			}
		}
	}
	return ""
}

func extractUserMessageFromRequestBody(rawBody []byte) string {
	payload := parseJSONValue(string(rawBody))
	if payload == nil {
		return ""
	}
	return extractPreferredText(payload, "content", "query", "message", "text", "prompt")
}

func extractAssistantTextFromFrameData(rawData string) string {
	rawData = strings.TrimSpace(rawData)
	if rawData == "" || rawData == "[DONE]" {
		return ""
	}
	payload := parseJSONValue(rawData)
	if payload == nil {
		return ""
	}
	return extractPreferredText(payload, "delta", "reply", "message", "content", "text")
}

func frontendMessageStreamData(eventName, rawData string) string {
	rawData = strings.TrimSpace(rawData)
	if rawData == "" || rawData == "[DONE]" {
		return rawData
	}
	payload := parseJSONValue(rawData)
	record, ok := payload.(map[string]any)
	if !ok {
		return rawData
	}
	eventType := extractStringByExactKeys(record, "type")
	if eventType == "" {
		eventType = strings.TrimSpace(eventName)
	}
	if eventType != "assistant_response" {
		return rawData
	}
	content := extractPreferredText(record, "content", "message", "text", "reply", "delta")
	if content == "" {
		return rawData
	}
	next := make(map[string]any, len(record)+4)
	for key, value := range record {
		next[key] = value
	}
	next["original_type"] = eventType
	next["type"] = "message.assistant"
	next["role"] = "assistant"
	next["content"] = content
	next["message"] = content
	next["delta"] = content
	encoded, err := json.Marshal(next)
	if err != nil {
		return rawData
	}
	return string(encoded)
}

func logUpstreamSSEData(endpoint, threadID, roundID, taskID, eventName, rawData string) {
	rawData = strings.TrimSpace(rawData)
	event := log.Logger.Info().
		Str("sse_endpoint", endpoint).
		Str("thread_id", threadID).
		Str("event_name", strings.TrimSpace(eventName)).
		Int("data_bytes", len(rawData)).
		Str("data", trimStreamLogData(rawData))
	if roundID != "" {
		event = event.Str("round_id", roundID)
	}
	if taskID != "" {
		event = event.Str("task_id", taskID)
	}
	event.Msg("agent upstream sse data received")
}

func trimStreamLogData(rawData string) string {
	const maxLogDataBytes = 512
	if len(rawData) <= maxLogDataBytes {
		return rawData
	}
	return rawData[:maxLogDataBytes] + "..."
}

func recordPayloadValue(record orm.AgentThreadRecord) any {
	if parsed := parseJSONValue(record.PayloadText); parsed != nil {
		return parsed
	}
	return record.PayloadText
}

func threadPayloadValue(thread orm.AgentThread) any {
	if parsed := parseJSONValue(thread.ThreadPayload); parsed != nil {
		return parsed
	}
	return thread.ThreadPayload
}

func listRecords(
	db *gorm.DB,
	threadID, streamKind, roundID, afterID string,
	limit int,
) ([]orm.AgentThreadRecord, error) {
	return listRecordsWithStep(db, threadID, streamKind, roundID, "", afterID, limit)
}

func listRecordsWithStep(
	db *gorm.DB,
	threadID, streamKind, roundID, stepID, afterID string,
	limit int,
) ([]orm.AgentThreadRecord, error) {
	query := db.Model(&orm.AgentThreadRecord{}).Where("thread_id = ?", threadID)
	if streamKind != "" {
		query = query.Where("stream_kind = ?", streamKind)
	}
	if roundID != "" {
		query = query.Where("round_id = ?", roundID)
	}
	if stepID != "" {
		query = query.Where("step_id = ?", stepID)
	}
	if afterID != "" {
		query = query.Where("id > ?", afterID)
	}
	var records []orm.AgentThreadRecord
	if err := query.Order("id ASC").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func listRoundRecords(db *gorm.DB, roundIDs []string) (map[string][]orm.AgentThreadRecord, error) {
	result := make(map[string][]orm.AgentThreadRecord, len(roundIDs))
	if len(roundIDs) == 0 {
		return result, nil
	}
	var records []orm.AgentThreadRecord
	if err := db.Where("round_id IN ?", roundIDs).Order("id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	for _, record := range records {
		result[record.RoundID] = append(result[record.RoundID], record)
	}
	return result, nil
}

func saveThreadRecord(
	db *gorm.DB,
	threadID, roundID, taskID, streamKind, eventName, payloadText, rawFrame string,
) (*orm.AgentThreadRecord, bool, error) {
	return saveThreadRecordWithOptions(db, threadID, roundID, taskID, streamKind, eventName, payloadText, rawFrame, saveThreadRecordOptions{})
}

type saveThreadRecordOptions struct {
	StepID    string
	RecordKey string
}

func saveThreadRecordWithOptions(
	db *gorm.DB,
	threadID, roundID, taskID, streamKind, eventName, payloadText, rawFrame string,
	opts saveThreadRecordOptions,
) (*orm.AgentThreadRecord, bool, error) {
	now := time.Now().UTC()
	recordKey := sha256Hex(rawFrame)
	if strings.TrimSpace(opts.RecordKey) != "" {
		recordKey = strings.TrimSpace(opts.RecordKey)
	} else if streamKind == streamKindMessage || streamKind == streamKindThreadEvent {
		recordKey = newStreamRecordID()
	}
	record := orm.AgentThreadRecord{
		ID:          newStreamRecordID(),
		ThreadID:    threadID,
		RoundID:     roundID,
		StepID:      strings.TrimSpace(opts.StepID),
		TaskID:      taskID,
		StreamKind:  streamKind,
		RecordKey:   recordKey,
		EventName:   eventName,
		PayloadText: payloadText,
		RawFrame:    rawFrame,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error == nil && result.RowsAffected > 0 {
		return &record, true, nil
	}

	var existing orm.AgentThreadRecord
	err := db.Where(
		"thread_id = ? AND round_id = ? AND stream_kind = ? AND record_key = ?",
		threadID, roundID, streamKind, record.RecordKey,
	).First(&existing).Error
	if err == nil {
		return &existing, false, nil
	}
	if result.Error != nil {
		return nil, false, result.Error
	}
	return nil, false, err
}

func updateThreadStepFromEvent(db *gorm.DB, threadID, stepID string, event fetchedThreadEvent) error {
	threadID = strings.TrimSpace(threadID)
	stepID = strings.TrimSpace(stepID)
	if db == nil || threadID == "" || stepID == "" {
		return nil
	}

	now := time.Now().UTC()
	payload := parseJSONValue(event.RawFrame)
	title := extractStringByExactKeys(payload, "step_title", "title", "name", "display_name")
	hasTitle := title != ""
	if title == "" {
		title = stepID
	}
	status := normalizeThreadStepStatus(extractStringByExactKeys(payload, "step_status", "status", "state"), event.EventName)
	active := !isTerminalThreadStepStatus(status)
	var endedAt *time.Time
	if !active {
		endedAt = &now
	}
	orderIndex, hasOrder := extractIntByExactKeys(payload, "step_order", "order_index", "order", "index", "seq")

	step := orm.AgentThreadStep{
		ThreadID:      threadID,
		StepID:        stepID,
		Title:         title,
		Status:        status,
		Active:        active,
		OrderIndex:    orderIndex,
		EventCount:    1,
		CurrentTaskID: event.TaskID,
		StartedAt:     &now,
		EndedAt:       endedAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	updates := map[string]any{
		"status":      status,
		"active":      active,
		"event_count": gorm.Expr("agent_thread_steps.event_count + ?", 1),
		"ended_at":    endedAt,
		"updated_at":  now,
	}
	if hasTitle {
		updates["title"] = title
	}
	if strings.TrimSpace(event.TaskID) != "" {
		updates["current_task_id"] = event.TaskID
	}
	if hasOrder {
		updates["order_index"] = orderIndex
	}
	if nextStepRunID := extractStringByExactKeys(payload, "next_step_run_id"); nextStepRunID != "" {
		step.NextStepRunID = nextStepRunID
		updates["next_step_run_id"] = gorm.Expr(
			"CASE WHEN agent_thread_steps.next_step_run_id = ? THEN ? ELSE agent_thread_steps.next_step_run_id END",
			"",
			nextStepRunID,
		)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if active {
			if err := markOtherThreadStepsInactive(tx, threadID, stepID, now); err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "thread_id"}, {Name: "step_id"}},
			DoUpdates: clause.Assignments(updates),
		}).Create(&step).Error
	})
}

func markOtherThreadStepsInactive(db *gorm.DB, threadID, stepID string, now time.Time) error {
	return db.Model(&orm.AgentThreadStep{}).
		Where("thread_id = ? AND step_id <> ? AND active = ?", threadID, stepID, true).
		Updates(map[string]any{
			"active":     false,
			"status":     gorm.Expr("CASE WHEN status = ? THEN ? ELSE status END", "running", "succeeded"),
			"ended_at":   gorm.Expr("COALESCE(ended_at, ?)", now),
			"updated_at": now,
		}).Error
}

func normalizeThreadStepStatus(rawStatus, eventName string) string {
	status := strings.ToLower(strings.TrimSpace(rawStatus))
	event := strings.ToLower(strings.TrimSpace(eventName))
	switch {
	case strings.Contains(status, "cancel"):
		return "cancelled"
	case strings.Contains(status, "fail") || strings.Contains(status, "error"):
		return "failed"
	case event == "done":
		return "succeeded"
	case status == "":
		status = event
	}
	switch {
	case status == "":
		return "running"
	case strings.Contains(status, "cancel"):
		return "cancelled"
	case strings.Contains(status, "fail") || strings.Contains(status, "error"):
		return "failed"
	case strings.Contains(status, "success") || strings.Contains(status, "succeed") ||
		strings.Contains(status, "complete") || strings.Contains(status, "done") ||
		strings.Contains(status, "finished"):
		return "succeeded"
	case strings.Contains(status, "pause"):
		return "paused"
	case strings.Contains(status, "wait"):
		return "waiting"
	case strings.Contains(status, "start") || strings.Contains(status, "run") ||
		strings.Contains(status, "stream"):
		return "running"
	default:
		return status
	}
}

func isTerminalThreadStepStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "failed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func extractIntByExactKeys(root any, keys ...string) (int, bool) {
	lookup := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		lookup[key] = struct{}{}
	}
	return walkIntByExactKeys(root, lookup)
}

func walkIntByExactKeys(root any, lookup map[string]struct{}) (int, bool) {
	switch value := root.(type) {
	case map[string]any:
		for key, child := range value {
			if _, ok := lookup[key]; ok {
				if result, ok := stringifyMatchedInt(child); ok {
					return result, true
				}
			}
		}
		for _, child := range value {
			if result, ok := walkIntByExactKeys(child, lookup); ok {
				return result, true
			}
		}
	case []any:
		for _, child := range value {
			if result, ok := walkIntByExactKeys(child, lookup); ok {
				return result, true
			}
		}
	}
	return 0, false
}

func stringifyMatchedInt(root any) (int, bool) {
	switch value := root.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case uint64:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func buildReplayFrame(record orm.AgentThreadRecord) string {
	if record.StreamKind == streamKindMessage {
		return buildDataSSEFrame(recordDataPayload(record))
	}
	return buildDataSSEFrame(record.RawFrame)
}

func buildThreadEventFrame(rawFrame string) string {
	return buildDataSSEFrame(rawFrame)
}

func buildDataSSEFrame(rawData string) string {
	rawData = strings.TrimRight(strings.ReplaceAll(rawData, "\r\n", "\n"), "\n")
	var builder strings.Builder
	for _, line := range strings.Split(rawData, "\n") {
		builder.WriteString("data: ")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	return builder.String()
}

func recordDataPayload(record orm.AgentThreadRecord) string {
	if strings.TrimSpace(record.PayloadText) != "" {
		return record.PayloadText
	}
	frame := parseSSEFrame(strings.Split(strings.ReplaceAll(record.RawFrame, "\r\n", "\n"), "\n"))
	if frame != nil {
		return frame.Data
	}
	return ""
}

func writeReplayFrame(w http.ResponseWriter, flusher http.Flusher, record orm.AgentThreadRecord) {
	_, _ = io.WriteString(w, buildReplayFrame(record))
	if flusher != nil {
		flusher.Flush()
	}
}

func writeSSEKeepalive(w http.ResponseWriter, flusher http.Flusher) error {
	_, err := io.WriteString(w, ": keepalive\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	return err
}

func writeNamedSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	body, _ := json.Marshal(payload)
	if event != "" {
		_, _ = io.WriteString(w, "event: "+event+"\n")
	}
	_, _ = io.WriteString(w, "data: ")
	_, _ = w.Write(body)
	_, _ = io.WriteString(w, "\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func readSSEFrame(reader *bufio.Reader) (*sseFrame, error) {
	lines := make([]string, 0, 8)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				line = strings.TrimRight(line, "\r\n")
				if line != "" {
					lines = append(lines, line)
				}
				if len(lines) == 0 {
					return nil, io.EOF
				}
				return parseSSEFrame(lines), nil
			}
			return nil, err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(lines) == 0 {
				continue
			}
			return parseSSEFrame(lines), nil
		}
		lines = append(lines, line)
	}
}

func readThreadEventSSEFrame(reader *bufio.Reader) (*sseFrame, error) {
	lines := make([]string, 0, 8)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				line = strings.TrimRight(line, "\r\n")
				if line != "" {
					lines = append(lines, line)
				}
				if len(lines) == 0 {
					return nil, io.EOF
				}
				return parseSSEFrame(lines), nil
			}
			return nil, err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(lines) == 0 {
				continue
			}
			return parseSSEFrame(lines), nil
		}
		lines = append(lines, line)
		if isSingleLineJSONDataFrame(lines) {
			return parseSSEFrame(lines), nil
		}
	}
}

func isSingleLineJSONDataFrame(lines []string) bool {
	dataLines := 0
	data := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "data:") {
			dataLines++
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if dataLines != 1 {
		return false
	}
	if data == "[DONE]" {
		return true
	}
	return json.Valid([]byte(data))
}

func parseSSEFrame(lines []string) *sseFrame {
	frame := &sseFrame{
		Event: "message",
		Raw:   strings.Join(lines, "\n"),
	}
	dataLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "id:") {
			frame.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			continue
		}
		if strings.HasPrefix(line, "event:") {
			frame.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	frame.Data = strings.Join(dataLines, "\n")
	return frame
}
