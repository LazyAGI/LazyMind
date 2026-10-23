package modelprovider

import (
	"net/url"
	"strings"
)

// LazyLLMBaseURL normalizes self-hosted OpenAI-compatible endpoints and the
// official OpenRouter origin to the API roots expected by LazyLLM. Provider
// reverse proxies keep their own URL contract.
func LazyLLMBaseURL(providerName, baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	providerName = normalizeProviderName(providerName)
	if providerName == "openrouter" {
		return canonicalOpenRouterBaseURL(baseURL)
	}
	if providerName != "openai" {
		return baseURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return baseURL
	}

	rawPath := parsed.EscapedPath()
	segments := strings.Split(strings.Trim(rawPath, "/"), "/")
	v1Index := -1
	for index, segment := range segments {
		decoded, _ := url.PathUnescape(segment)
		if strings.EqualFold(decoded, "v1") {
			v1Index = index
			break
		}
	}
	if v1Index >= 0 {
		rawPath = "/" + strings.Join(segments[:v1Index+1], "/") + "/"
	} else if strings.Trim(rawPath, "/") == "" {
		rawPath = "/v1/"
	} else {
		// OpenAI-compatible suppliers may version their API differently or put
		// it behind a proxy prefix. urljoin needs a trailing slash to retain it.
		rawPath = strings.TrimRight(rawPath, "/") + "/"
	}
	parsed.Path, _ = url.PathUnescape(rawPath)
	parsed.RawPath = rawPath
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func hasOpenAIRequestPath(providerName, baseURL string) bool {
	if normalizeProviderName(providerName) != "openai" {
		return false
	}

	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index, segment := range segments {
		if strings.EqualFold(segment, "v1") {
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
