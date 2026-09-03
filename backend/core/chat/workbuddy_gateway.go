package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultWorkBuddyOpenAPIURL = "https://www.workbuddy.cn/openapi/v2"
	maxWorkBuddyResponseBytes  = 2 << 20
	maxWorkBuddyAttachmentSize = 20 << 20
)

var errWorkBuddyAuthorizationRequired = errors.New("WorkBuddy authorization required")

type workBuddyGateway struct {
	baseURL      string
	accessToken  string
	httpClient   *http.Client
	pollInterval time.Duration
}

type workBuddyExecution struct {
	MessageID   string                `json:"message_id"`
	Text        string                `json:"text"`
	Attachments []workBuddyAttachment `json:"attachments"`
}

type workBuddyAttachment struct {
	Filename      string `json:"filename"`
	MediaType     string `json:"media_type"`
	ContentBase64 string `json:"content_base64"`
}

type workBuddyMessage struct {
	MessageID   string           `json:"message_id"`
	Role        string           `json:"role"`
	Content     []any            `json:"content"`
	MessageType string           `json:"msg_type"`
	Attachments []map[string]any `json:"attachments"`
}

func newWorkBuddyGateway(ctx context.Context, owner string) (workBuddyGateway, error) {
	tokens, err := fetchCloudProviderTokens(ctx, ChatExecutorWorkBuddy, owner)
	if err != nil {
		return workBuddyGateway{}, err
	}
	if len(tokens) == 0 {
		return workBuddyGateway{}, errWorkBuddyAuthorizationRequired
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LAZYMIND_WORKBUDDY_OPENAPI_URL")), "/")
	if baseURL == "" {
		baseURL = defaultWorkBuddyOpenAPIURL
	}
	return workBuddyGateway{
		baseURL: baseURL, accessToken: tokens[0], httpClient: http.DefaultClient,
		pollInterval: workBuddyPollInterval(),
	}, nil
}

func (g workBuddyGateway) online(ctx context.Context) (bool, error) {
	var response struct {
		Online bool `json:"online"`
	}
	if err := g.doJSON(ctx, http.MethodGet, "/localassistant", nil, &response); err != nil {
		return false, err
	}
	return response.Online, nil
}

func (g workBuddyGateway) execute(ctx context.Context, prompt string) (workBuddyExecution, error) {
	if strings.TrimSpace(prompt) == "" {
		return workBuddyExecution{}, errors.New("WorkBuddy prompt is empty")
	}
	var sent struct {
		MessageID string `json:"message_id"`
	}
	if err := g.doJSON(ctx, http.MethodPost, "/localassistant/message", map[string]string{
		"content": prompt, "msg_type": "text",
	}, &sent); err != nil {
		return workBuddyExecution{}, fmt.Errorf("send WorkBuddy message: %w", err)
	}
	sent.MessageID = strings.TrimSpace(sent.MessageID)
	if sent.MessageID == "" {
		return workBuddyExecution{}, errors.New("WorkBuddy returned an empty message ID")
	}

	timeout := time.NewTimer(workBuddyPollTimeout())
	defer timeout.Stop()
	interval := g.pollInterval
	if interval <= 0 {
		interval = workBuddyPollInterval()
	}
	for {
		var history struct {
			Messages []workBuddyMessage `json:"messages"`
		}
		path := "/localassistant/message?message_id=" + url.QueryEscape(sent.MessageID)
		if err := g.doJSON(ctx, http.MethodGet, path, nil, &history); err != nil {
			return workBuddyExecution{}, fmt.Errorf("poll WorkBuddy message: %w", err)
		}
		for _, message := range history.Messages {
			result, complete, err := g.executionFromMessage(ctx, sent.MessageID, message)
			if err != nil {
				return workBuddyExecution{}, err
			}
			if complete {
				return result, nil
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return workBuddyExecution{}, ctx.Err()
		case <-timeout.C:
			timer.Stop()
			return workBuddyExecution{}, errors.New("WorkBuddy response timed out")
		case <-timer.C:
		}
	}
}

func (g workBuddyGateway) executionFromMessage(
	ctx context.Context,
	sentMessageID string,
	message workBuddyMessage,
) (workBuddyExecution, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") ||
		strings.Contains(strings.ToLower(message.MessageType), "permission") {
		return workBuddyExecution{}, false, nil
	}
	parts := make([]string, 0, len(message.Content))
	for _, item := range message.Content {
		switch value := item.(type) {
		case string:
			if value = strings.TrimSpace(value); value != "" {
				parts = append(parts, value)
			}
		case map[string]any:
			if text := firstString(value, "text", "content"); text != "" {
				parts = append(parts, text)
			}
		}
	}
	attachments := make([]workBuddyAttachment, 0, len(message.Attachments))
	for _, raw := range message.Attachments {
		attachment, ok, err := g.loadAttachment(ctx, raw)
		if err != nil {
			return workBuddyExecution{}, false, err
		}
		if ok {
			attachments = append(attachments, attachment)
		}
		if len(attachments) == 4 {
			break
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" && len(attachments) == 0 {
		return workBuddyExecution{}, false, nil
	}
	return workBuddyExecution{MessageID: sentMessageID, Text: text, Attachments: attachments}, true, nil
}

func (g workBuddyGateway) loadAttachment(ctx context.Context, raw map[string]any) (workBuddyAttachment, bool, error) {
	mediaType := firstString(raw, "mime_type", "mimeType", "media_type", "mediaType")
	filename := filepath.Base(firstString(raw, "name", "filename", "file_name", "fileName"))
	encoded := firstString(raw, "content_base64", "contentBase64", "base64", "data")
	if strings.HasPrefix(encoded, "data:") {
		if comma := strings.IndexByte(encoded, ','); comma >= 0 {
			if mediaType == "" {
				mediaType = strings.TrimPrefix(strings.SplitN(encoded[:comma], ";", 2)[0], "data:")
			}
			encoded = encoded[comma+1:]
		}
	}
	if encoded != "" {
		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(content) == 0 || len(content) > maxWorkBuddyAttachmentSize {
			return workBuddyAttachment{}, false, errors.New("WorkBuddy returned an invalid attachment")
		}
		if filename == "" || filename == "." {
			filename = workBuddyAttachmentName(mediaType)
		}
		return workBuddyAttachment{Filename: filename, MediaType: mediaType, ContentBase64: encoded}, true, nil
	}

	assetURL := firstString(raw, "url", "download_url", "downloadUrl")
	if assetURL == "" {
		return workBuddyAttachment{}, false, nil
	}
	content, detectedType, err := g.downloadAttachment(ctx, assetURL)
	if err != nil {
		return workBuddyAttachment{}, false, err
	}
	if mediaType == "" {
		mediaType = detectedType
	}
	if filename == "" || filename == "." {
		if parsed, parseErr := url.Parse(assetURL); parseErr == nil {
			filename = filepath.Base(parsed.Path)
		}
	}
	if filename == "" || filename == "." || filename == "/" {
		filename = workBuddyAttachmentName(mediaType)
	}
	return workBuddyAttachment{
		Filename: filename, MediaType: mediaType,
		ContentBase64: base64.StdEncoding.EncodeToString(content),
	}, true, nil
}

func (g workBuddyGateway) downloadAttachment(ctx context.Context, rawURL string) ([]byte, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.User != nil || parsed.Hostname() == "" ||
		(parsed.Scheme != "https" && !sameOrigin(parsed, g.baseURL)) {
		return nil, "", errors.New("WorkBuddy returned an unsafe attachment URL")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !sameOrigin(parsed, g.baseURL) {
		return nil, "", errors.New("WorkBuddy returned an unsafe attachment URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Authorization", "Bearer "+g.accessToken)
	response, err := g.client().Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("download WorkBuddy attachment: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download WorkBuddy attachment: HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxWorkBuddyAttachmentSize+1))
	if err != nil || len(content) == 0 || len(content) > maxWorkBuddyAttachmentSize {
		return nil, "", errors.New("WorkBuddy attachment is empty or too large")
	}
	mediaType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	return content, mediaType, nil
}

func (g workBuddyGateway) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+g.accessToken)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := g.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxWorkBuddyResponseBytes+1))
	if err != nil {
		return err
	}
	if len(responseBody) > maxWorkBuddyResponseBytes {
		return errors.New("WorkBuddy response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("WorkBuddy HTTP %d: %s", response.StatusCode, compactWorkBuddyError(responseBody))
	}
	var envelope struct {
		Code    *int            `json:"code"`
		Message string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("decode WorkBuddy response: %w", err)
	}
	if envelope.Code != nil && *envelope.Code != 0 {
		return fmt.Errorf("WorkBuddy error %d: %s", *envelope.Code, strings.TrimSpace(envelope.Message))
	}
	payload := responseBody
	if envelope.Code != nil {
		payload = envelope.Data
	}
	if output == nil || len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	return json.Unmarshal(payload, output)
}

func (g workBuddyGateway) client() *http.Client {
	if g.httpClient != nil {
		return g.httpClient
	}
	return http.DefaultClient
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sameOrigin(target *url.URL, base string) bool {
	parsedBase, err := url.Parse(base)
	return err == nil && strings.EqualFold(target.Scheme, parsedBase.Scheme) &&
		strings.EqualFold(target.Host, parsedBase.Host)
}

func workBuddyAttachmentName(mediaType string) string {
	extensions, _ := mime.ExtensionsByType(mediaType)
	if len(extensions) > 0 {
		return "workbuddy-result" + extensions[0]
	}
	return "workbuddy-result.bin"
}

func workBuddyPollInterval() time.Duration {
	return workBuddyDurationFromEnv("LAZYMIND_WORKBUDDY_POLL_INTERVAL_MS", time.Second)
}

func workBuddyPollTimeout() time.Duration {
	return workBuddyDurationFromEnv("LAZYMIND_WORKBUDDY_RESPONSE_TIMEOUT_MS", 10*time.Minute)
}

func workBuddyDurationFromEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func compactWorkBuddyError(body []byte) string {
	value := strings.TrimSpace(string(body))
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}
