package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
)

const (
	defaultThreadPageSize = 20
	maxThreadPageSize     = 100
)

var recordIDCounter atomic.Uint64

func newStreamRecordID() string {
	return fmt.Sprintf("%020d%06d", time.Now().UnixNano(), recordIDCounter.Add(1)%1000000)
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
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

func hasThreadRequiredLLMConfig(payload map[string]any) bool {
	llmConfig, ok := payload["llm_config"].(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"eval_policy", "repair_policy", "candidate_config", "abtest_candidate_config"} {
		if _, ok := llmConfig[key]; ok {
			return false
		}
	}
	for _, role := range []string{"llm", "evo_llm"} {
		found := false
		for key, value := range llmConfig {
			if !strings.EqualFold(strings.TrimSpace(key), role) {
				continue
			}
			roleConfig, ok := value.(map[string]any)
			found = ok
			for _, field := range []string{"source", "model", "base_url", "api_key"} {
				found = found && strings.TrimSpace(fmt.Sprint(roleConfig[field])) != ""
			}
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func buildEvoThreadCreatePayload(payload map[string]any) map[string]any {
	inputs, _ := payload["inputs"].(map[string]any)
	if inputs == nil {
		inputs = map[string]any{}
	}
	numCase := firstPositiveInt(
		inputs["num_case"],
		inputs["num_cases"],
		payload["num_case"],
		payload["num_cases"],
	)
	if numCase <= 0 {
		numCase = 1
	}
	deadlineSeconds := firstPositiveFloat(
		inputs["case_deadline_seconds"],
		payload["case_deadline_seconds"],
	)
	if deadlineSeconds <= 0 {
		deadlineSeconds = 300
	}

	routerChatURL := strings.TrimSpace(agentScalarString(inputs["router_chat_url"]))
	if routerChatURL == "" {
		routerChatURL = common.JoinURL(common.ChatServiceEndpoint(), "/api/chat/stream")
	}
	routerAdminURL := strings.TrimSpace(agentScalarString(inputs["router_admin_url"]))
	if routerAdminURL == "" {
		routerAdminURL = common.ChatServiceEndpoint()
	}
	algorithmID := strings.TrimSpace(agentScalarString(inputs["algorithm_id"]))
	if algorithmID == "" {
		algorithmID = "default"
	}

	return map[string]any{
		"mode":       firstNonEmptyScalar(payload["mode"], "auto"),
		"title":      firstNonEmptyScalar(payload["title"]),
		"llm_config": payload["llm_config"],
		"inputs": map[string]any{
			"kb_id":                 stringListFromAny(firstNonNilAny(inputs["kb_id"], inputs["knowledge_base_id"], inputs["dataset_id"])),
			"csv_data":              csvDataListFromAny(inputs["csv_data"]),
			"router_chat_url":       routerChatURL,
			"router_admin_url":      routerAdminURL,
			"algorithm_id":          algorithmID,
			"num_case":              numCase,
			"case_deadline_seconds": deadlineSeconds,
		},
	}
}

func cloneJSONMap(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return map[string]any{}
	}
	var copied map[string]any
	if err := json.Unmarshal(raw, &copied); err != nil {
		return map[string]any{}
	}
	return copied
}

func firstNonNilAny(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonEmptyScalar(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(agentScalarString(value)); text != "" {
			return text
		}
	}
	return ""
}

func stringListFromAny(value any) []string {
	switch typed := value.(type) {
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(agentScalarString(item)); text != "" {
				result = append(result, text)
			}
		}
		return result
	case []string:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		if text := strings.TrimSpace(agentScalarString(value)); text != "" {
			return []string{text}
		}
		return []string{}
	}
}

func csvDataListFromAny(value any) []map[string]string {
	items, ok := value.([]any)
	if !ok {
		return []map[string]string{}
	}
	result := make([]map[string]string, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok || len(row) == 0 {
			continue
		}
		out := map[string]string{}
		for key, raw := range row {
			text := strings.TrimSpace(agentScalarString(raw))
			if strings.TrimSpace(key) != "" && text != "" {
				out[strings.TrimSpace(key)] = text
			}
		}
		if len(out) > 0 {
			result = append(result, out)
		}
	}
	return result
}

func firstPositiveInt(values ...any) int {
	for _, value := range values {
		switch typed := value.(type) {
		case int:
			if typed > 0 {
				return typed
			}
		case int64:
			if typed > 0 {
				return int(typed)
			}
		case float64:
			if typed > 0 {
				return int(typed)
			}
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			if err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func firstPositiveFloat(values ...any) float64 {
	for _, value := range values {
		switch typed := value.(type) {
		case int:
			if typed > 0 {
				return float64(typed)
			}
		case int64:
			if typed > 0 {
				return float64(typed)
			}
		case float64:
			if typed > 0 {
				return typed
			}
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
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

func threadPayloadValue(thread orm.AgentThread) any {
	if parsed := parseJSONValue(thread.ThreadPayload); parsed != nil {
		return parsed
	}
	return thread.ThreadPayload
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
