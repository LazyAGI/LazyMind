package doc

import "testing"

func TestMapParseTaskErrorFFmpegMissing(t *testing.T) {
	tests := []string{
		"ffmpeg not found in PATH.",
		"ffprobe not found in PATH.",
		"[Errno 2] No such file or directory: 'ffmpeg'",
	}

	for _, input := range tests {
		if got := mapParseTaskError(input); got != parseTaskErrCodeFFmpegMissing {
			t.Errorf("mapParseTaskError(%q) = %q, want %q", input, got, parseTaskErrCodeFFmpegMissing)
		}
	}
}

// TestIsParseTaskErrCode recognises 7-char codes starting with 200072.
func TestIsParseTaskErrCode(t *testing.T) {
	if !isParseTaskErrCode(parseTaskErrCodeSubmitFailed) {
		t.Fatal("expected 2000720 to be a parse task error code")
	}
	if !isParseTaskErrCode(parseTaskErrCodeRateLimit) {
		t.Fatal("expected 2000721 to be a parse task error code")
	}
	if isParseTaskErrCode("") {
		t.Fatal("empty string is not a parse task error code")
	}
	if isParseTaskErrCode("20007201") {
		t.Fatal("8-char code is not a parse task error code")
	}
	if isParseTaskErrCode("2000700") {
		t.Fatal("wrong prefix should not match")
	}
}

// TestExtractParseTaskErrorMessage extracts the message field from JSON or returns raw text.
func TestExtractParseTaskErrorMessage(t *testing.T) {
	// JSON with message field.
	if got := extractParseTaskErrorMessage(`{"message":"submit failed"}`); got != "submit failed" {
		t.Fatalf("message field: got %q", got)
	}
	// JSON with msg field.
	if got := extractParseTaskErrorMessage(`{"msg":"rate limited"}`); got != "rate limited" {
		t.Fatalf("msg field: got %q", got)
	}
	// JSON with error field.
	if got := extractParseTaskErrorMessage(`{"error":"timeout"}`); got != "timeout" {
		t.Fatalf("error field: got %q", got)
	}
	// JSON with detail field.
	if got := extractParseTaskErrorMessage(`{"detail":"connection refused"}`); got != "connection refused" {
		t.Fatalf("detail field: got %q", got)
	}
	// Plain text is returned as-is.
	if got := extractParseTaskErrorMessage("plain error text"); got != "plain error text" {
		t.Fatalf("plain text: got %q", got)
	}
	// Empty string.
	if got := extractParseTaskErrorMessage(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	// Whitespace only.
	if got := extractParseTaskErrorMessage("   "); got != "" {
		t.Fatalf("whitespace: got %q", got)
	}
}

// TestMapParseTaskError maps raw error messages to i18n error codes.
func TestMapParseTaskError(t *testing.T) {
	// Already a parse task error code.
	if got := mapParseTaskError(parseTaskErrCodeReparseFailed); got != parseTaskErrCodeReparseFailed {
		t.Fatalf("direct code: got %q", got)
	}
	// Empty.
	if got := mapParseTaskError(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	// Legacy Chinese text.
	if got := mapParseTaskError("任务提交失败"); got != parseTaskErrCodeSubmitFailed {
		t.Fatalf("legacy text: got %q, want %q", got, parseTaskErrCodeSubmitFailed)
	}
	if got := mapParseTaskError("解析超时"); got != parseTaskErrCodeTimeout {
		t.Fatalf("legacy timeout: got %q", got)
	}
	// Keyword matching (case-insensitive).
	if got := mapParseTaskError("Rate Limiting Exceeded"); got != parseTaskErrCodeRateLimit {
		t.Fatalf("keyword rate limiting: got %q", got)
	}
	if got := mapParseTaskError("connection refused by peer"); got != parseTaskErrCodeServiceUnavailable {
		t.Fatalf("keyword connection refused: got %q", got)
	}
	// Unknown message returned as-is.
	if got := mapParseTaskError("something unexpected"); got != "something unexpected" {
		t.Fatalf("unknown msg: got %q", got)
	}
}
