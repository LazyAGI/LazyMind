package modelprovider

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

func apiVersionSegment(value string) bool {
	decoded, err := url.PathUnescape(value)
	if err != nil || len(decoded) < 2 || strings.ToLower(decoded[:1]) != "v" {
		return false
	}
	version, err := strconv.Atoi(decoded[1:])
	return err == nil && version > 0
}

// APIModelsListURL uses the exact API root used for inference. It neither drops
// v3/v4 nor inserts v1 underneath an explicitly configured gateway prefix.
func APIModelsListURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", errors.New("invalid group base_url")
	}
	root := openAICompatibleRoot(parsed.EscapedPath())
	rawPath := root + "models"
	parsed.Path, _ = url.PathUnescape(rawPath)
	parsed.RawPath = rawPath
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String(), nil
}

// ProviderModelsListURL preserves native providers' established origin paths.
func ProviderModelsListURL(providerName, baseURL string) (string, error) {
	if LazyLLMSource(providerName) == "openai" {
		return APIModelsListURL(baseURL)
	}
	baseURL = LazyLLMBaseURL(providerName, baseURL)
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", errors.New("invalid group base_url")
	}
	rawRoot := strings.TrimRight(parsed.EscapedPath(), "/")
	switch LazyLLMSource(providerName) {
	case "qwen":
		rawRoot += "/compatible-mode/v1"
	case "kimi":
		rawRoot += "/v1"
	case "claude":
		if !strings.HasSuffix(strings.ToLower(rawRoot), "/v1") {
			rawRoot += "/v1"
		}
	case "ppio":
		if !strings.HasSuffix(strings.ToLower(rawRoot), "/openai") && !strings.HasSuffix(strings.ToLower(rawRoot), "/v1") {
			rawRoot += "/openai"
		}
	case "sensenova":
		// SenseNova's classic compatible-mode API has no model directory.
		// Match its native adapter's read-only validation endpoint instead.
		switch strings.ToLower(parsed.Hostname()) {
		case "api.sensenova.cn":
			rawRoot = "/v1/llm"
		case "token.sensenova.cn":
			rawRoot = "/v1"
		default:
			rawRoot = strings.TrimSuffix(rawRoot, "/chat/completions")
		}
	}
	rawPath := rawRoot + "/models"
	parsed.Path, _ = url.PathUnescape(rawPath)
	parsed.RawPath = rawPath
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String(), nil
}

func openAICompatibleRoot(rawPath string) string {
	segments := strings.Split(strings.Trim(rawPath, "/"), "/")
	for index := len(segments) - 1; index >= 0; index-- {
		if apiVersionSegment(segments[index]) {
			return "/" + strings.Join(segments[:index+1], "/") + "/"
		}
	}
	if strings.Trim(rawPath, "/") == "" || strings.EqualFold(strings.Trim(rawPath, "/"), "chat/completions") {
		return "/v1/"
	}
	return strings.TrimRight(rawPath, "/") + "/"
}

// LazyLLMBaseURL normalizes self-hosted OpenAI-compatible endpoints and the
// official OpenRouter origin to the API roots expected by LazyLLM. Provider
// reverse proxies keep their own URL contract.
func LazyLLMBaseURL(providerName, baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	providerName = normalizeProviderName(providerName)
	if providerName == "openrouter" {
		baseURL = canonicalOpenRouterBaseURL(baseURL)
	}
	if LazyLLMSource(providerName) != "openai" {
		if providerName == "qwen" || providerName == "kimi" {
			if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" && parsed.Scheme != "" {
				raw := strings.TrimRight(parsed.EscapedPath(), "/")
				suffix := "/v1"
				if providerName == "qwen" {
					suffix = "/compatible-mode/v1"
				}
				if strings.HasSuffix(strings.ToLower(raw), suffix) {
					raw = raw[:len(raw)-len(suffix)]
				}
				raw += "/"
				parsed.Path, _ = url.PathUnescape(raw)
				parsed.RawPath = raw
				return parsed.String()
			}
		}
		// These suppliers use their configured API root with relative paths.
		// urljoin discards its last segment when the trailing slash is absent.
		switch providerName {
		case "doubao", "glm", "deepseek", "siliconflow", "openrouter", "ppio", "aiping":
			if parsed, err := url.Parse(baseURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
				parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
				if parsed.RawPath != "" {
					parsed.RawPath = strings.TrimRight(parsed.RawPath, "/") + "/"
				}
				return parsed.String()
			}
		}
		return baseURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return baseURL
	}

	rawPath := openAICompatibleRoot(parsed.EscapedPath())
	parsed.Path, _ = url.PathUnescape(rawPath)
	parsed.RawPath = rawPath
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func hasOpenAIRequestPath(providerName, baseURL string) bool {
	if LazyLLMSource(providerName) != "openai" {
		return false
	}

	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index := len(segments) - 1; index >= 0; index-- {
		if apiVersionSegment(segments[index]) {
			return index < len(segments)-1
		}
	}
	return false
}

// canonicalOpenRouterBaseURL removes request-path suffixes only from the
// official OpenRouter API origin. Reverse proxies keep their configured path.
func canonicalOpenRouterBaseURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
		!strings.EqualFold(parsed.Host, "openrouter.ai") {
		return baseURL
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 || !strings.EqualFold(segments[0], "api") ||
		!strings.EqualFold(segments[1], "v1") {
		return baseURL
	}

	parsed.Path = "/api/v1/"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

// canonicalSenseNovaTokenPlanURL accepts the legacy chat endpoint saved by older clients.
func canonicalSenseNovaTokenPlanURL(baseURL string) string {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if normalized == "https://token.sensenova.cn/v1" || normalized == "https://token.sensenova.cn/v1/chat/completions" {
		return sensenovaNewPlatformBaseURL
	}
	return baseURL
}
