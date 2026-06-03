package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
)

type Logger struct {
	logger *slog.Logger
}

func NewLogger(w io.Writer) *Logger {
	if w == nil {
		return &Logger{logger: slog.Default()}
	}
	return &Logger{logger: slog.New(slog.NewJSONHandler(w, nil))}
}

func DefaultLogger() *Logger {
	return &Logger{logger: slog.Default()}
}

func (l *Logger) Info(ctx context.Context, msg string, fields map[string]any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.InfoContext(ctx, msg, attrs(fields)...)
}

func (l *Logger) Error(ctx context.Context, msg string, fields map[string]any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.ErrorContext(ctx, msg, attrs(fields)...)
}

func attrs(fields map[string]any) []any {
	out := make([]any, 0, len(fields)*2)
	for key, value := range fields {
		if forbiddenLogField(key, value) {
			continue
		}
		out = append(out, key, value)
	}
	return out
}

func forbiddenLogField(key string, value any) bool {
	name := strings.ToLower(key)
	if strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "refresh") {
		return true
	}
	if strings.Contains(name, "path") || strings.Contains(name, "url") || strings.Contains(name, "uri") {
		return true
	}
	if text, ok := value.(string); ok {
		lower := strings.ToLower(text)
		return strings.Contains(lower, "access_token") || strings.Contains(lower, "refresh_token") || strings.Contains(lower, "scan-temp://")
	}
	return false
}

func AuditFields(event string, fields map[string]any) map[string]any {
	out := map[string]any{"audit_event": event}
	for key, value := range fields {
		if !forbiddenLogField(key, value) {
			out[key] = value
		}
	}
	return out
}

func JSONFields(fields map[string]any) string {
	body, _ := json.Marshal(fields)
	return string(body)
}

