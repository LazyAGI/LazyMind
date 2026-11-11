package doc

import (
	"lazymind/core/common/orm"
	"regexp"
	"strings"
)

type ProcessingDiagnostic struct {
	Stage    string `json:"stage"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	TaskID   string `json:"task_id,omitempty"`
	Recovery string `json:"recovery"`
}

var diagnosticURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var diagnosticBearer = regexp.MustCompile(`(?i)bearer\s+[^\s,;"']+`)
var diagnosticSecret = regexp.MustCompile(`(?i)(authorization|bearer|api[_-]?key|token|password|secret|signature|sig)["']?\s*[:= ]\s*["']?[^\s,;"'}]+`)

// UI/MCP diagnostics must not carry upstream signed URLs, credentials or tracebacks.
func SafeProcessingMessage(raw string) string {
	text := extractParseTaskErrorMessage(raw)
	for _, marker := range []string{"Traceback (", "\n  File ", "\ngoroutine ", "\n\tat "} {
		if i := strings.Index(text, marker); i >= 0 {
			text = text[:i]
		}
	}
	text = diagnosticURL.ReplaceAllString(text, "[URL omitted]")
	text = diagnosticBearer.ReplaceAllString(text, "[credential omitted]")
	text = diagnosticSecret.ReplaceAllString(text, "[credential omitted]")
	text = strings.TrimSpace(text)
	if text == "" {
		return "解析服务未返回具体原因，请根据任务 ID 检查任务记录。"
	}
	runes := []rune(text)
	if len(runes) > 500 {
		text = string(runes[:500]) + "…"
	}
	return text
}

func processingDiagnostic(state orm.DocumentProcessingState, task orm.Task, observedLevel, raw, code, message string) *ProcessingDiagnostic {
	d := &ProcessingDiagnostic{Stage: "processing", Code: code, TaskID: task.ID, Message: message, Recovery: "可明确要求重新解析；若仍失败，请根据任务 ID 检查文件或解析服务。"}
	failed := NormalizeTaskStateForUI(raw) == "FAILED"
	if observedLevel == ProcessingLevelParsed && failed && state.ParseStatus == "failed" {
		d.Stage = "parse"
		if code == "" && message == "" {
			d.Code, d.Message = state.ParseErrorCode, state.ParseErrorMessage
		}
	} else {
		stages := []struct{ stage, status, code, message string }{{"parse", state.ParseStatus, state.ParseErrorCode, state.ParseErrorMessage}, {"chunk", state.ChunkStatus, state.ChunkErrorCode, state.ChunkErrorMessage}, {"index", state.IndexStatus, state.IndexErrorCode, state.IndexErrorMessage}}
		for _, s := range stages {
			if s.status == "failed" {
				failed = true
				if s.code != "" || s.message != "" {
					d.Stage = s.stage
					d.Code = s.code
					d.Message = s.message
					break
				}
			}
		}
	}
	if !failed {
		return nil
	}
	if d.Code == "" {
		d.Code = "PROCESSING_FAILED"
	}
	d.Message = SafeProcessingMessage(d.Message)
	return d
}

func DocumentProcessingStatus(level string, state orm.DocumentProcessingState) string {
	if level == "stored" {
		return "not_requested"
	}
	if state.ParseStatus == "failed" || (level != "parsed" && state.ChunkStatus == "failed") {
		return "failed"
	}
	status := state.IndexStatus
	switch level {
	case "parsed":
		status = state.ParseStatus
	case "chunked":
		status = state.ChunkStatus
	}
	switch status {
	case "pending", "running", "failed":
		return status
	case "succeeded":
		return "ready"
	default:
		return "unknown"
	}
}

// DocumentBodyProcessingStatus reports the readiness of the parsed body.
func DocumentBodyProcessingStatus(observedLevel string, state orm.DocumentProcessingState) string {
	if observedLevel == ProcessingLevelStored {
		return "not_requested"
	}
	switch state.ParseStatus {
	case "succeeded":
		return "ready"
	case "pending", "running", "failed":
		return state.ParseStatus
	default:
		return "unknown"
	}
}

// ObservedDocumentProcessingStatus reports status at the submitted level when available.
func ObservedDocumentProcessingStatus(observedLevel, configuredLevel string, state orm.DocumentProcessingState) string {
	level := strings.TrimSpace(observedLevel)
	if level == "" {
		level = configuredLevel
	}
	return DocumentProcessingStatus(level, state)
}
