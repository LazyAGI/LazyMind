package algo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lazymind/core/common"
	corestore "lazymind/core/store"
)

const generateTimeout = 10 * time.Minute
const rewritePath = "/api/chat/rewrite"
const maxRouterChildFallbacks = 5

func GenerateSkill(ctx context.Context, req SkillGenerateRequest) (string, error) {
	return generate(ctx, rewritePayload("skill", req.Content, req.UserInstruct, req.LLMConfig))
}

func GenerateMemory(ctx context.Context, req ManagedGenerateRequest) (string, error) {
	return generate(ctx, rewritePayload("memory", req.Content, req.UserInstruct, req.LLMConfig))
}

func GenerateUserPreference(ctx context.Context, req ManagedGenerateRequest) (string, error) {
	return generate(ctx, rewritePayload("user_preference", req.Content, req.UserInstruct, req.LLMConfig))
}

func GeneratePolish(ctx context.Context, req PolishGenerateRequest) (string, error) {
	return generate(ctx, rewritePayload("polish", req.Content, req.UserInstruct, req.LLMConfig))
}

func generateURL(path string) string {
	return common.ChatServiceEndpoint() + path
}

func generate(ctx context.Context, req RewriteRequest) (string, error) {
	url := generateURL(rewritePath)
	content, err := generateFromURL(ctx, url, req)
	if err == nil {
		return content, nil
	}
	if !isNotFound(err) {
		return "", err
	}
	if content, fallbackErr := generateViaRouterChildren(ctx, req); fallbackErr == nil {
		return content, nil
	}
	return "", err
}

func generateFromURL(ctx context.Context, url string, req RewriteRequest) (string, error) {
	var response map[string]any
	if err := common.ApiPost(ctx, url, req, nil, &response, generateTimeout); err != nil {
		return "", err
	}
	content := extractGeneratedContent(response)
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("generate endpoint returned empty content")
	}
	return content, nil
}

func generateViaRouterChildren(ctx context.Context, req RewriteRequest) (string, error) {
	urls, err := routerChildRewriteURLs()
	if err != nil {
		return "", err
	}
	var lastErr error
	for _, url := range urls {
		content, err := generateFromURL(ctx, url, req)
		if err == nil {
			return content, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no healthy router child process")
}

type routerChildEndpoint struct {
	AlgorithmID string
	Host        string
	Port        int
}

func routerChildRewriteURLs() ([]string, error) {
	db := corestore.DB()
	if db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	var rows []routerChildEndpoint
	if err := db.Raw(`
SELECT algorithm_id, host, port
FROM router_child_processes
WHERE status = ?
ORDER BY
  CASE WHEN algorithm_id = 'default' THEN 0 ELSE 1 END,
  updated_at DESC,
  id
LIMIT ?
`, "healthy", maxRouterChildFallbacks).Scan(&rows).Error; err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(rows)*2)
	seen := map[string]struct{}{}
	for _, row := range rows {
		for _, base := range routerChildBases(row) {
			if base == "" {
				continue
			}
			item := base + rewritePath
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			urls = append(urls, item)
		}
	}
	return urls, nil
}

func routerChildBases(row routerChildEndpoint) []string {
	scheme := chatEndpointScheme()
	bases := []string{baseURLForHostPort(scheme, row.Host, row.Port)}
	if serviceBase := chatServiceBaseWithPort(row.Port); serviceBase != "" {
		bases = append(bases, serviceBase)
	}
	return bases
}

func chatEndpointScheme() string {
	parsed, err := url.Parse(common.ChatServiceEndpoint())
	if err != nil || parsed.Scheme == "" {
		return "http"
	}
	return parsed.Scheme
}

func baseURLForHostPort(scheme, host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return ""
	}
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func chatServiceBaseWithPort(port int) string {
	if port <= 0 {
		return ""
	}
	parsed, err := url.Parse(common.ChatServiceEndpoint())
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	host := parsed.Hostname()
	if host == "" {
		return ""
	}
	parsed.Host = net.JoinHostPort(host, strconv.Itoa(port))
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func isNotFound(err error) bool {
	var httpErr *common.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == 404
}

func rewritePayload(taskType, content, userInstruct string, llmConfig map[string]any) RewriteRequest {
	if llmConfig == nil {
		llmConfig = map[string]any{}
	}
	return RewriteRequest{
		TaskType:     taskType,
		Content:      content,
		UserInstruct: strings.TrimSpace(userInstruct),
		LLMConfig:    llmConfig,
	}
}

func extractGeneratedContent(payload any) string {
	switch typed := payload.(type) {
	case map[string]any:
		if data, ok := typed["data"]; ok {
			if s := extractGeneratedContent(data); strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		for _, key := range []string{"content", "text", "result", "generated_content", "full_content"} {
			if value, ok := typed[key]; ok {
				if s := extractGeneratedContent(value); strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
	case string:
		return strings.TrimSpace(typed)
	}
	return ""
}
