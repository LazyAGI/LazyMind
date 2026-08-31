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

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	v1Index := -1
	for index, segment := range segments {
		if strings.EqualFold(segment, "v1") {
			v1Index = index
			break
		}
	}
	if v1Index >= 0 {
		parsed.Path = "/" + strings.Join(segments[:v1Index+1], "/") + "/"
	} else {
		parsed.Path = "/v1/"
	}
	parsed.RawPath = ""
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
