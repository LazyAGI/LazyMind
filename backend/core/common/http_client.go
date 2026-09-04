package common

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

const (
	// defaultHTTPTimeout textDefaulttext，textUnset。
	defaultHTTPTimeout = 10 * time.Minute
)

// HTTPPost text POST text。
// - ctx text/Unset；text nil text context.Background。
// - contentType text "application/json"。
// textResponse body、HTTP text error（text/text/text body Failedtext error）。
func HTTPPost(ctx context.Context, url, contentType string, body []byte) ([]byte, int, error) {
	return HTTPPostWithTimeout(ctx, url, contentType, body, defaultHTTPTimeout)
}

func HTTPPostWithTimeout(ctx context.Context, url, contentType string, body []byte, timeout time.Duration) ([]byte, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	client := &http.Client{
		Timeout: timeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return respBytes, resp.StatusCode, nil
}
