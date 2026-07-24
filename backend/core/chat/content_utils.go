package chat

import (
	"regexp"
	"strings"

	"lazymind/core/common/orm"
)

var (
	toolCallTagPattern   = regexp.MustCompile(`(?s)<tool_call\b[^>]*>.*?</tool_call>`)
	toolResultTagPattern = regexp.MustCompile(`(?s)<tool_result\b[^>]*>.*?</tool_result>`)
	thinkBlockPattern    = regexp.MustCompile(`(?s)<think>.*?</think>`)
)

func stripToolTags(text string) string {
	if text == "" {
		return ""
	}
	text = toolCallTagPattern.ReplaceAllString(text, "")
	text = toolResultTagPattern.ReplaceAllString(text, "")
	return text
}

func stripThinkTags(text string) string {
	if text == "" {
		return ""
	}
	return strings.TrimSpace(thinkBlockPattern.ReplaceAllString(text, ""))
}

func buildAssistantHistoryContent(history orm.ChatHistory) string {
	return history.Result
}
