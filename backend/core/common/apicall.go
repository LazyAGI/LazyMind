package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ApiGet text HTTP GET(JSON) text。
func ApiGet(ctx context.Context, url string, header map[string]string, response any, timeout time.Duration) error {
	return do(ctx, url, http.MethodGet, nil, header, response, timeout)
}

// ApiPost text HTTP POST(JSON) text。
func ApiPost(ctx context.Context, url string, body any, header map[string]string, response any, timeout time.Duration) error {
	return do(ctx, url, http.MethodPost, body, header, response, timeout)
}

// ApiDelete text HTTP DELETE(JSON) text。
func ApiDelete(ctx context.Context, url string, header map[string]string, response any, timeout time.Duration) error {
	return do(ctx, url, http.MethodDelete, nil, header, response, timeout)
}

func do(ctx context.Context, url, method string, body any, header map[string]string, response any, timeout time.Duration) error {
	var reqBody io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	cli := &http.Client{Timeout: timeout}
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    summarizeExternalErrorMessage(respBytes),
		}
	}
	if response == nil {
		return nil
	}
	if len(respBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBytes, response); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}

func summarizeExternalErrorMessage(respBytes []byte) string {
	const maxLen = 240
	body := strings.TrimSpace(string(respBytes))
	if body == "" {
		return "upstream returned empty error body"
	}

	var v any
	if err := json.Unmarshal(respBytes, &v); err == nil {
		if msg := extractExternalErrorMessage(v); msg != "" {
			return trimErrorText(msg, maxLen)
		}
		return "upstream returned error payload without message"
	}

	return trimErrorText(body, maxLen)
}

func extractExternalErrorMessage(v any) string {
	switch t := v.(type) {
	case map[string]any:
		preferred := []string{"message", "msg", "error", "detail", "reason", "error_message"}
		for _, k := range preferred {
			if val, ok := t[k]; ok {
				if s := stringifyExternalErrorValue(val); s != "" {
					return s
				}
			}
		}
		for _, val := range t {
			if s := extractExternalErrorMessage(val); s != "" {
				return s
			}
		}
	case []any:
		for _, item := range t {
			if s := extractExternalErrorMessage(item); s != "" {
				return s
			}
		}
	case string:
		return strings.TrimSpace(t)
	}
	return ""
}

func stringifyExternalErrorValue(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64, bool, int, int64, uint64:
		return fmt.Sprint(t)
	case map[string]any, []any:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func trimErrorText(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
