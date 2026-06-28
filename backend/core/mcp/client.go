package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lazymind/core/common/orm"
	appLog "lazymind/core/log"
)

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolsListResult struct {
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
}

func listRemoteTools(ctx context.Context, row orm.MCPServer) ([]discoveredTool, error) {
	headers, err := decodeHeaders(row.HeadersJSON)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(normalizedTimeout(row.Timeout)) * time.Second
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{Timeout: timeout}
	endpoint := row.URL
	closeSSE := func() {}
	if row.Transport == transportSSE {
		endpoint, closeSSE, err = resolveSSEMessageEndpoint(callCtx, client, endpoint, headers)
		if err != nil {
			return nil, err
		}
	}
	defer closeSSE()

	sessionHeaders, err := rpcInitialize(callCtx, client, endpoint, headers)
	if err != nil {
		return nil, err
	}
	result, err := rpcToolsList(callCtx, client, endpoint, sessionHeaders)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func rpcInitialize(ctx context.Context, client *http.Client, endpoint string, headers map[string]any) (map[string]any, error) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "lazymind-core",
				"version": "0.1.0",
			},
		},
	}
	_, respHeaders, err := doRPC(ctx, client, endpoint, headers, req)
	if err != nil {
		return nil, err
	}
	sessionHeaders := cloneHeaders(headers)
	if sessionID := strings.TrimSpace(respHeaders.Get("Mcp-Session-Id")); sessionID != "" {
		sessionHeaders["Mcp-Session-Id"] = sessionID
	}
	notify := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  map[string]any{},
	}
	_, _, _ = doRPC(ctx, client, endpoint, sessionHeaders, notify)
	return sessionHeaders, nil
}

func rpcToolsList(ctx context.Context, client *http.Client, endpoint string, headers map[string]any) ([]discoveredTool, error) {
	raw, _, err := doRPC(ctx, client, endpoint, headers, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
		Params:  map[string]any{},
	})
	if err != nil {
		return nil, err
	}
	var result toolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	out := make([]discoveredTool, 0, len(result.Tools))
	for _, tool := range result.Tools {
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{}`)
		}
		out = append(out, discoveredTool{
			Name:        strings.TrimSpace(tool.Name),
			Description: strings.TrimSpace(tool.Description),
			InputSchema: schema,
		})
	}
	return out, nil
}

func doRPC(ctx context.Context, client *http.Client, endpoint string, headers map[string]any, payload jsonRPCRequest) (json.RawMessage, http.Header, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	applyHeaders(req.Header, headers)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, resp.Header, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		appLog.Logger.Warn().
			Int("status", resp.StatusCode).
			Str("endpoint", endpoint).
			Str("method", payload.Method).
			Str("body", strings.TrimSpace(string(raw))).
			Msg("mcp rpc returned non-2xx response")
		return nil, resp.Header, fmt.Errorf("mcp rpc returned %d", resp.StatusCode)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, resp.Header, nil
	}
	raw = unwrapSSEData(raw)
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return nil, resp.Header, fmt.Errorf("decode mcp rpc response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, resp.Header, fmt.Errorf("mcp rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, resp.Header, nil
}

func resolveSSEMessageEndpoint(ctx context.Context, client *http.Client, endpoint string, headers map[string]any) (string, func(), error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	applyHeaders(req.Header, headers)

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", nil, fmt.Errorf("mcp sse returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(nil, 256*1024)
	closeSSE := func() { _ = resp.Body.Close() }
	eventName := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if eventName == "endpoint" || strings.HasPrefix(data, "/") || strings.HasPrefix(data, "http://") || strings.HasPrefix(data, "https://") {
				messageEndpoint, err := joinMCPURL(endpoint, data)
				if err != nil {
					closeSSE()
					return "", nil, err
				}
				return messageEndpoint, closeSSE, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		closeSSE()
		return "", nil, err
	}
	closeSSE()
	return "", nil, fmt.Errorf("mcp sse endpoint event not found")
}

func applyHeaders(dst http.Header, headers map[string]any) {
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				dst.Set(key, typed)
			}
		case []string:
			for _, item := range typed {
				if strings.TrimSpace(item) != "" {
					dst.Add(key, item)
				}
			}
		case []any:
			for _, item := range typed {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					dst.Add(key, s)
				}
			}
		default:
			dst.Set(key, fmt.Sprint(value))
		}
	}
}

func cloneHeaders(headers map[string]any) map[string]any {
	out := make(map[string]any, len(headers)+1)
	for key, value := range headers {
		out[key] = value
	}
	return out
}

func unwrapSSEData(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if !bytes.Contains(trimmed, []byte("data:")) {
		return trimmed
	}
	var b strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data != "" && data != "[DONE]" {
				b.WriteString(data)
			}
		}
	}
	if out := strings.TrimSpace(b.String()); out != "" {
		return []byte(out)
	}
	return trimmed
}

func joinMCPURL(base, endpoint string) (string, error) {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint, nil
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	rel, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(rel).String(), nil
}
